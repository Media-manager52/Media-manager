package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/importlists"
)

var snapshotDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)
var importListIMDbID = regexp.MustCompile(`^tt[0-9]{7,12}$`)

var ErrDuplicateSnapshot = errors.New("import-list snapshot already exists")

type ImportList struct {
	ID                    uuid.UUID           `json:"id"`
	Name                  string              `json:"name"`
	Provider              string              `json:"provider"`
	Enabled               bool                `json:"enabled"`
	MovieRootID           *uuid.UUID          `json:"movie_root_id,omitempty"`
	MovieRootName         string              `json:"movie_root_name,omitempty"`
	MovieQualityProfileID *uuid.UUID          `json:"movie_quality_profile_id,omitempty"`
	MovieQualityProfile   string              `json:"movie_quality_profile,omitempty"`
	CreatedAt             time.Time           `json:"created_at"`
	UpdatedAt             time.Time           `json:"updated_at"`
	LatestSnapshot        *ImportListSnapshot `json:"latest_snapshot,omitempty"`
}

type ImportListSnapshot struct {
	ID               uuid.UUID        `json:"id"`
	ImportListID     uuid.UUID        `json:"import_list_id"`
	SourceDigest     string           `json:"source_digest"`
	ItemCount        int              `json:"item_count"`
	PendingCount     int              `json:"pending_count"`
	ExistingCount    int              `json:"existing_count"`
	UnsupportedCount int              `json:"unsupported_count"`
	CreatedAt        time.Time        `json:"created_at"`
	Items            []ImportListItem `json:"items,omitempty"`
}

type ImportListItem struct {
	ID              uuid.UUID  `json:"id"`
	SnapshotID      uuid.UUID  `json:"snapshot_id"`
	Ordinal         int        `json:"ordinal"`
	MediaKind       string     `json:"media_kind"`
	Provider        string     `json:"provider"`
	ProviderID      string     `json:"provider_id"`
	Title           string     `json:"title"`
	Year            *uint16    `json:"year,omitempty"`
	State           string     `json:"state"`
	Attention       string     `json:"attention,omitempty"`
	ExistingMediaID *uuid.UUID `json:"existing_media_id,omitempty"`
}

func (store *Store) CreateImportList(ctx context.Context, item ImportList, now time.Time) (ImportList, error) {
	item.Name, item.Provider = strings.TrimSpace(item.Name), strings.ToLower(strings.TrimSpace(item.Provider))
	if item.Name == "" || len(item.Name) > 200 || item.Provider != "imdb_export" {
		return ImportList{}, errors.New("invalid import list")
	}
	item.ID, item.CreatedAt, item.UpdatedAt = uuid.NewV7(), now.UTC(), now.UTC()
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return ImportList{}, err
	}
	defer transaction.Rollback(ctx)
	if item.MovieRootID != nil {
		if err := transaction.QueryRow(ctx, `SELECT name FROM library_roots WHERE id=$1 AND media_kind='movie' AND enabled`, item.MovieRootID.String()).Scan(&item.MovieRootName); errors.Is(err, pgx.ErrNoRows) {
			return ImportList{}, errors.New("movie root is not available")
		} else if err != nil {
			return ImportList{}, err
		}
	}
	if item.MovieQualityProfileID != nil {
		if err := transaction.QueryRow(ctx, `SELECT name FROM quality_profiles WHERE id=$1 AND media_kind='movie' AND enabled`, item.MovieQualityProfileID.String()).Scan(&item.MovieQualityProfile); errors.Is(err, pgx.ErrNoRows) {
			return ImportList{}, errors.New("movie quality profile is not available")
		} else if err != nil {
			return ImportList{}, err
		}
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO import_lists(id,name,provider,enabled,movie_root_id,movie_quality_profile_id,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$7)`, item.ID.String(), item.Name, item.Provider, item.Enabled, optionalUUID(item.MovieRootID), optionalUUID(item.MovieQualityProfileID), now.UTC()); err != nil {
		return ImportList{}, err
	}
	detail, _ := json.Marshal(map[string]any{"name": item.Name, "provider": item.Provider, "monitoring_only": true})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','import_list.created','import_list',$2,$3,$4)`, uuid.NewV7().String(), item.ID.String(), detail, now.UTC()); err != nil {
		return ImportList{}, err
	}
	return item, transaction.Commit(ctx)
}

func (store *Store) ListImportLists(ctx context.Context) ([]ImportList, error) {
	rows, err := store.Pool.Query(ctx, `SELECT list.id::text,list.name,list.provider,list.enabled,list.movie_root_id::text,COALESCE(root.name,''),list.movie_quality_profile_id::text,COALESCE(profile.name,''),list.created_at,list.updated_at,snapshot.id::text,snapshot.source_digest,snapshot.item_count,snapshot.pending_count,snapshot.existing_count,snapshot.unsupported_count,snapshot.created_at FROM import_lists list LEFT JOIN library_roots root ON root.id=list.movie_root_id LEFT JOIN quality_profiles profile ON profile.id=list.movie_quality_profile_id LEFT JOIN LATERAL (SELECT * FROM import_list_snapshots candidate WHERE candidate.import_list_id=list.id ORDER BY candidate.created_at DESC,candidate.id DESC LIMIT 1) snapshot ON true ORDER BY list.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ImportList{}
	for rows.Next() {
		item, err := scanImportList(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (store *Store) ImportList(ctx context.Context, id uuid.UUID) (ImportList, error) {
	row := store.Pool.QueryRow(ctx, `SELECT list.id::text,list.name,list.provider,list.enabled,list.movie_root_id::text,COALESCE(root.name,''),list.movie_quality_profile_id::text,COALESCE(profile.name,''),list.created_at,list.updated_at,snapshot.id::text,snapshot.source_digest,snapshot.item_count,snapshot.pending_count,snapshot.existing_count,snapshot.unsupported_count,snapshot.created_at FROM import_lists list LEFT JOIN library_roots root ON root.id=list.movie_root_id LEFT JOIN quality_profiles profile ON profile.id=list.movie_quality_profile_id LEFT JOIN LATERAL (SELECT * FROM import_list_snapshots candidate WHERE candidate.import_list_id=list.id ORDER BY candidate.created_at DESC,candidate.id DESC LIMIT 1) snapshot ON true WHERE list.id=$1`, id.String())
	item, err := scanImportList(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ImportList{}, ErrNotFound
	}
	if err != nil {
		return ImportList{}, err
	}
	if item.LatestSnapshot != nil {
		items, err := store.importListSnapshotItems(ctx, item.LatestSnapshot.ID)
		if err != nil {
			return ImportList{}, err
		}
		item.LatestSnapshot.Items = items
	}
	return item, nil
}

type rowScanner interface{ Scan(...any) error }

func scanImportList(row rowScanner) (ImportList, error) {
	var item ImportList
	var idText string
	var rootText, profileText, snapshotText *string
	var digest *string
	var itemCount, pendingCount, existingCount, unsupportedCount *int
	var snapshotCreated *time.Time
	if err := row.Scan(&idText, &item.Name, &item.Provider, &item.Enabled, &rootText, &item.MovieRootName, &profileText, &item.MovieQualityProfile, &item.CreatedAt, &item.UpdatedAt, &snapshotText, &digest, &itemCount, &pendingCount, &existingCount, &unsupportedCount, &snapshotCreated); err != nil {
		return ImportList{}, err
	}
	item.ID, _ = uuid.Parse(idText)
	item.MovieRootID = parseOptionalUUID(rootText)
	item.MovieQualityProfileID = parseOptionalUUID(profileText)
	if snapshotText != nil {
		snapshotID, _ := uuid.Parse(*snapshotText)
		item.LatestSnapshot = &ImportListSnapshot{ID: snapshotID, ImportListID: item.ID, SourceDigest: *digest, ItemCount: *itemCount, PendingCount: *pendingCount, ExistingCount: *existingCount, UnsupportedCount: *unsupportedCount, CreatedAt: *snapshotCreated}
	}
	return item, nil
}

func (store *Store) CreateImportListSnapshot(ctx context.Context, listID uuid.UUID, digest string, parsed []importlists.Item, now time.Time) (ImportListSnapshot, error) {
	if !snapshotDigest.MatchString(digest) || len(parsed) < 1 || len(parsed) > importlists.MaxItems {
		return ImportListSnapshot{}, errors.New("invalid import-list snapshot")
	}
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return ImportListSnapshot{}, err
	}
	defer transaction.Rollback(ctx)
	var enabled bool
	if err := transaction.QueryRow(ctx, `SELECT enabled FROM import_lists WHERE id=$1 FOR UPDATE`, listID.String()).Scan(&enabled); errors.Is(err, pgx.ErrNoRows) {
		return ImportListSnapshot{}, ErrNotFound
	} else if err != nil {
		return ImportListSnapshot{}, err
	}
	if !enabled {
		return ImportListSnapshot{}, errors.New("import list is disabled")
	}
	var duplicate bool
	if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM import_list_snapshots WHERE import_list_id=$1 AND source_digest=$2)`, listID.String(), digest).Scan(&duplicate); err != nil {
		return ImportListSnapshot{}, err
	}
	if duplicate {
		return ImportListSnapshot{}, ErrDuplicateSnapshot
	}
	snapshot := ImportListSnapshot{ID: uuid.NewV7(), ImportListID: listID, SourceDigest: digest, ItemCount: len(parsed), CreatedAt: now.UTC()}
	items := make([]ImportListItem, 0, len(parsed))
	for _, source := range parsed {
		source.Provider = strings.ToLower(strings.TrimSpace(source.Provider))
		source.ProviderID = strings.ToLower(strings.TrimSpace(source.ProviderID))
		source.Title = strings.TrimSpace(source.Title)
		if source.Ordinal < 1 || (source.MediaKind != "movie" && source.MediaKind != "series" && source.MediaKind != "unknown") || source.Provider != "imdb" || !importListIMDbID.MatchString(source.ProviderID) || source.Title == "" || source.Year != nil && (*source.Year < 1870 || *source.Year > 2100) {
			return ImportListSnapshot{}, errors.New("invalid import-list item")
		}
		item := ImportListItem{ID: uuid.NewV7(), SnapshotID: snapshot.ID, Ordinal: source.Ordinal, MediaKind: source.MediaKind, Provider: source.Provider, ProviderID: source.ProviderID, Title: source.Title, Year: source.Year, State: source.State, Attention: source.Attention}
		if item.State == "pending_review" {
			var mediaText string
			err := transaction.QueryRow(ctx, `SELECT identity.media_id::text FROM external_identities identity JOIN media_items media ON media.id=identity.media_id WHERE identity.namespace=$1 AND identity.value=$2 AND identity.state='verified' AND media.deleted_at IS NULL LIMIT 1`, item.Provider, item.ProviderID).Scan(&mediaText)
			if err == nil {
				mediaID, _ := uuid.Parse(mediaText)
				item.ExistingMediaID, item.State = &mediaID, "already_cataloged"
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return ImportListSnapshot{}, err
			}
		}
		switch item.State {
		case "pending_review":
			snapshot.PendingCount++
		case "already_cataloged":
			snapshot.ExistingCount++
		case "unsupported":
			snapshot.UnsupportedCount++
		default:
			return ImportListSnapshot{}, errors.New("invalid import-list item state")
		}
		items = append(items, item)
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO import_list_snapshots(id,import_list_id,source_digest,item_count,pending_count,existing_count,unsupported_count,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, snapshot.ID.String(), listID.String(), digest, snapshot.ItemCount, snapshot.PendingCount, snapshot.ExistingCount, snapshot.UnsupportedCount, now.UTC()); err != nil {
		return ImportListSnapshot{}, err
	}
	for _, item := range items {
		if _, err := transaction.Exec(ctx, `INSERT INTO import_list_snapshot_items(id,snapshot_id,ordinal,media_kind,provider,provider_id,title,year,state,attention,existing_media_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''),$11)`, item.ID.String(), snapshot.ID.String(), item.Ordinal, item.MediaKind, item.Provider, item.ProviderID, item.Title, optionalYear(item.Year), item.State, item.Attention, optionalUUID(item.ExistingMediaID)); err != nil {
			return ImportListSnapshot{}, err
		}
	}
	detail, _ := json.Marshal(map[string]any{"source_digest": digest, "item_count": snapshot.ItemCount, "pending_review": snapshot.PendingCount, "already_cataloged": snapshot.ExistingCount, "unsupported": snapshot.UnsupportedCount, "monitoring_changes": 0})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','import_list.snapshot_ingested','import_list',$2,$3,$4)`, uuid.NewV7().String(), listID.String(), detail, now.UTC()); err != nil {
		return ImportListSnapshot{}, err
	}
	snapshot.Items = items
	return snapshot, transaction.Commit(ctx)
}

func (store *Store) importListSnapshotItems(ctx context.Context, snapshotID uuid.UUID) ([]ImportListItem, error) {
	rows, err := store.Pool.Query(ctx, `SELECT id::text,snapshot_id::text,ordinal,media_kind,provider,provider_id,title,year,state,COALESCE(attention,''),existing_media_id::text FROM import_list_snapshot_items WHERE snapshot_id=$1 ORDER BY ordinal`, snapshotID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ImportListItem{}
	for rows.Next() {
		var item ImportListItem
		var idText, snapshotText string
		var year *int16
		var mediaText *string
		if err := rows.Scan(&idText, &snapshotText, &item.Ordinal, &item.MediaKind, &item.Provider, &item.ProviderID, &item.Title, &year, &item.State, &item.Attention, &mediaText); err != nil {
			return nil, err
		}
		item.ID, _ = uuid.Parse(idText)
		item.SnapshotID, _ = uuid.Parse(snapshotText)
		item.ExistingMediaID = parseOptionalUUID(mediaText)
		if year != nil {
			converted := uint16(*year)
			item.Year = &converted
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func optionalUUID(value *uuid.UUID) any {
	if value == nil {
		return nil
	}
	return value.String()
}

func optionalYear(value *uint16) any {
	if value == nil {
		return nil
	}
	return int16(*value)
}

func parseOptionalUUID(value *string) *uuid.UUID {
	if value == nil {
		return nil
	}
	parsed, err := uuid.Parse(*value)
	if err != nil {
		return nil
	}
	return &parsed
}
