package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	// ErrQualityProfileInUse means a title, file, list or Seerr route still
	// points at the profile, so removing it would change what they require.
	ErrQualityProfileInUse = errors.New("quality profile is in use")
	// ErrQualityProfileIsDefault means the profile is the fallback for its
	// media kind; another profile must become the default first.
	ErrQualityProfileIsDefault = errors.New("quality profile is the default")
)

// DeleteQualityProfile removes a profile that nothing refers to. A profile
// that any workflow (finished ones included), adopted file, adoption batch,
// import list or Seerr route uses is left untouched: its language and quality
// requirements are part of the record of why those files were accepted.
func (store *Store) DeleteQualityProfile(ctx context.Context, id uuid.UUID, actor string, now time.Time) error {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var name string
	var isDefault bool
	if err := transaction.QueryRow(ctx, `SELECT name,is_default FROM quality_profiles WHERE id=$1 FOR UPDATE`, id.String()).Scan(&name, &isDefault); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if isDefault {
		return ErrQualityProfileIsDefault
	}
	var inUse bool
	if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workflows WHERE quality_profile_id=$1)
		OR EXISTS(SELECT 1 FROM adopted_files WHERE quality_profile_id=$1)
		OR EXISTS(SELECT 1 FROM adoption_batches WHERE default_quality_profile_id=$1)
		OR EXISTS(SELECT 1 FROM adoption_items WHERE quality_profile_id=$1)
		OR EXISTS(SELECT 1 FROM import_lists WHERE movie_quality_profile_id=$1)
		OR EXISTS(SELECT 1 FROM seerr_library_routes WHERE quality_profile_id=$1)`, id.String()).Scan(&inUse); err != nil {
		return err
	}
	if inUse {
		return ErrQualityProfileInUse
	}
	if _, err := transaction.Exec(ctx, `DELETE FROM quality_profiles WHERE id=$1`, id.String()); err != nil {
		// A reference created between the check and the delete.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return ErrQualityProfileInUse
		}
		return err
	}
	detail, _ := json.Marshal(map[string]string{"name": name})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,$2,'quality_profile.deleted','quality_profile',$3,$4,$5)`, uuid.NewV7().String(), actor, id.String(), string(detail), now); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}
