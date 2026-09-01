package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/catalog"
	"uuid"
)

func persistMetadata(ctx context.Context, transaction pgx.Tx, mediaID uuid.UUID, value *catalog.MediaMetadata, now time.Time) error {
	if value == nil {
		return nil
	}
	normalized, err := catalog.NormalizeMetadata(value, now)
	if err != nil {
		return err
	}
	body, err := json.Marshal(normalized)
	if err != nil {
		return err
	}
	_, err = transaction.Exec(ctx, `INSERT INTO media_metadata(media_id,data,revision,created_at,updated_at) VALUES($1,$2,1,$3,$3)
		ON CONFLICT(media_id) DO UPDATE SET data=EXCLUDED.data,revision=media_metadata.revision+1,updated_at=EXCLUDED.updated_at`, mediaID.String(), body, now.UTC())
	return err
}

func (store *Store) metadata(ctx context.Context, mediaID uuid.UUID) (*catalog.MediaMetadata, error) {
	var body []byte
	err := store.Pool.QueryRow(ctx, `SELECT data FROM media_metadata WHERE media_id=$1`, mediaID.String()).Scan(&body)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var value catalog.MediaMetadata
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, err
	}
	return &value, nil
}

func (store *Store) RefreshMediaMetadata(ctx context.Context, mediaID uuid.UUID, value *catalog.MediaMetadata, identities []catalog.ExternalIdentity, actor string, now time.Time) (catalog.MediaItem, error) {
	normalized, err := catalog.NormalizeMetadata(value, now)
	if err != nil || normalized == nil {
		return catalog.MediaItem{}, catalog.ErrInvalidMetadata
	}
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return catalog.MediaItem{}, err
	}
	defer transaction.Rollback(ctx)
	var kind catalog.MediaKind
	if err := transaction.QueryRow(ctx, `SELECT kind FROM media_items WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, mediaID.String()).Scan(&kind); errors.Is(err, pgx.ErrNoRows) {
		return catalog.MediaItem{}, ErrNotFound
	} else if err != nil {
		return catalog.MediaItem{}, err
	}
	rows, err := transaction.Query(ctx, `SELECT namespace,value,state,source FROM external_identities WHERE media_id=$1 ORDER BY namespace,value`, mediaID.String())
	if err != nil {
		return catalog.MediaItem{}, err
	}
	existing := []catalog.ExternalIdentity{}
	for rows.Next() {
		var identity catalog.ExternalIdentity
		if err := rows.Scan(&identity.Namespace, &identity.Value, &identity.State, &identity.Source); err != nil {
			rows.Close()
			return catalog.MediaItem{}, err
		}
		existing = append(existing, identity)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return catalog.MediaItem{}, err
	}
	rows.Close()
	combined := append(append([]catalog.ExternalIdentity(nil), existing...), identities...)
	if err := catalog.ValidateIdentities(kind, combined); err != nil {
		return catalog.MediaItem{}, ErrIdentityNeedsAttention
	}
	for _, identity := range identities {
		if identity.State != catalog.IdentityVerified {
			continue
		}
		var owner string
		lookupErr := transaction.QueryRow(ctx, `SELECT media_id::text FROM external_identities WHERE media_kind=$1 AND namespace=$2 AND value=$3 AND state='verified'`, kind, identity.Namespace, identity.Value).Scan(&owner)
		if lookupErr == nil && owner != mediaID.String() {
			return catalog.MediaItem{}, ErrIdentityNeedsAttention
		}
		if lookupErr != nil && !errors.Is(lookupErr, pgx.ErrNoRows) {
			return catalog.MediaItem{}, lookupErr
		}
		_, err := transaction.Exec(ctx, `INSERT INTO external_identities(id,media_id,media_kind,namespace,value,state,source,observed_at) VALUES($1,$2,$3,$4,$5,'verified',$6,$7)
			ON CONFLICT(media_id,namespace,value) DO UPDATE SET source=EXCLUDED.source,observed_at=EXCLUDED.observed_at`, uuid.NewV7().String(), mediaID.String(), kind, identity.Namespace, identity.Value, identity.Source, now.UTC())
		if err != nil {
			return catalog.MediaItem{}, err
		}
	}
	if err := persistMetadata(ctx, transaction, mediaID, normalized, now); err != nil {
		return catalog.MediaItem{}, err
	}
	if _, err := transaction.Exec(ctx, `UPDATE media_items SET revision=revision+1,updated_at=$2 WHERE id=$1`, mediaID.String(), now.UTC()); err != nil {
		return catalog.MediaItem{}, err
	}
	providers := make([]string, 0, len(normalized.Sources))
	for _, source := range normalized.Sources {
		providers = append(providers, source.Provider)
	}
	detail, _ := json.Marshal(map[string]any{"providers": providers})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,$2,'media.metadata_refreshed','media',$3,$4,$5)`, uuid.NewV7().String(), actor, mediaID.String(), detail, now.UTC()); err != nil {
		return catalog.MediaItem{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return catalog.MediaItem{}, err
	}
	return store.GetMedia(ctx, mediaID)
}
