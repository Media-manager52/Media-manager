package importliststore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/importlists"
	"github.com/media-manager/media-manager/internal/postgres/dbcore"
)

// MDBListSyncInterval is how often a followed list is read again, like
// Sonarr's list sync. One read is one request to mdblist.com; it never
// searches an indexer by itself.
const MDBListSyncInterval = 6 * time.Hour

// MDBListSettings is what the owner chooses for a followed list. Empty
// roots and profiles mean the defaults of each media kind.
type MDBListSettings struct {
	Name                   string
	Owner                  string
	Slug                   string
	Enabled                bool
	MovieRootID            *uuid.UUID
	MovieQualityProfileID  *uuid.UUID
	SeriesRootID           *uuid.UUID
	SeriesQualityProfileID *uuid.UUID
}

// ImportListEntry is one title a followed list has carried.
type ImportListEntry struct {
	MediaKind   string     `json:"media_kind"`
	TMDBID      int64      `json:"tmdb_id"`
	IMDbID      string     `json:"imdb_id,omitempty"`
	TVDBID      int64      `json:"tvdb_id,omitempty"`
	Title       string     `json:"title"`
	Year        int        `json:"year,omitempty"`
	Rank        int        `json:"rank"`
	State       string     `json:"state"`
	Detail      string     `json:"detail,omitempty"`
	MediaID     *uuid.UUID `json:"media_id,omitempty"`
	Attempts    int        `json:"attempts"`
	FirstSeenAt time.Time  `json:"first_seen_at"`
	LastSeenAt  time.Time  `json:"last_seen_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func invalidList(message string) error {
	return dbcore.Refuse("invalid_import_list", message, "Check the list address, the library folders and the quality profiles.")
}

// validateTargets checks the chosen folders and profiles belong to the right
// media kind and are enabled, and fills their names.
func validateTargets(ctx context.Context, transaction pgx.Tx, item *ImportList) error {
	checks := []struct {
		id    *uuid.UUID
		query string
		kind  string
		name  *string
		what  string
	}{
		{item.MovieRootID, `SELECT name FROM library_roots WHERE id=$1 AND media_kind=$2 AND enabled`, "movie", &item.MovieRootName, "the film library folder is not available"},
		{item.MovieQualityProfileID, `SELECT name FROM quality_profiles WHERE id=$1 AND media_kind=$2 AND enabled`, "movie", &item.MovieQualityProfile, "the film quality profile is not available"},
		{item.SeriesRootID, `SELECT name FROM library_roots WHERE id=$1 AND media_kind=$2 AND enabled`, "series", &item.SeriesRootName, "the series library folder is not available"},
		{item.SeriesQualityProfileID, `SELECT name FROM quality_profiles WHERE id=$1 AND media_kind=$2 AND enabled`, "series", &item.SeriesQualityProfile, "the series quality profile is not available"},
	}
	for _, check := range checks {
		if check.id == nil {
			continue
		}
		if err := transaction.QueryRow(ctx, check.query, check.id.String(), check.kind).Scan(check.name); errors.Is(err, pgx.ErrNoRows) {
			return invalidList(check.what)
		} else if err != nil {
			return err
		}
	}
	return nil
}

func normalizeMDBListSettings(settings MDBListSettings) (MDBListSettings, error) {
	settings.Name = strings.TrimSpace(settings.Name)
	settings.Owner, settings.Slug = strings.TrimSpace(settings.Owner), strings.TrimSpace(settings.Slug)
	if settings.Name == "" {
		settings.Name = settings.Slug
	}
	if len(settings.Name) > 200 {
		return settings, invalidList("the name must be at most 200 characters")
	}
	if !(importlists.MDBListRef{Owner: settings.Owner, Slug: settings.Slug}).Valid() {
		return settings, invalidList("the MDBList list address is invalid")
	}
	return settings, nil
}

// CreateMDBList follows a public MDBList list. Its first read is due at once.
func (store *ImportLists) CreateMDBList(ctx context.Context, settings MDBListSettings, now time.Time) (ImportList, error) {
	settings, err := normalizeMDBListSettings(settings)
	if err != nil {
		return ImportList{}, err
	}
	item := ImportList{ID: uuid.NewV7(), Name: settings.Name, Provider: "mdblist", Enabled: settings.Enabled, SourceOwner: settings.Owner, SourceSlug: settings.Slug,
		MovieRootID: settings.MovieRootID, MovieQualityProfileID: settings.MovieQualityProfileID, SeriesRootID: settings.SeriesRootID, SeriesQualityProfileID: settings.SeriesQualityProfileID,
		CreatedAt: now.UTC(), UpdatedAt: now.UTC()}
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return ImportList{}, err
	}
	defer transaction.Rollback(ctx)
	if err := validateTargets(ctx, transaction, &item); err != nil {
		return ImportList{}, err
	}
	var duplicate string
	if err := transaction.QueryRow(ctx, `SELECT name FROM import_lists WHERE lower(name)=lower($1) OR (provider='mdblist' AND lower(source_owner)=lower($2) AND lower(source_slug)=lower($3)) LIMIT 1`, item.Name, item.SourceOwner, item.SourceSlug).Scan(&duplicate); err == nil {
		return ImportList{}, dbcore.Refuse("import_list_exists", "a list with this name or address is already followed", "Open the list "+duplicate+" instead.")
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return ImportList{}, err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO import_lists(id,name,provider,enabled,movie_root_id,movie_quality_profile_id,series_root_id,series_quality_profile_id,source_owner,source_slug,next_sync_at,created_at,updated_at) VALUES($1,$2,'mdblist',$3,$4,$5,$6,$7,$8,$9,$10,$10,$10)`,
		item.ID.String(), item.Name, item.Enabled, optionalUUID(item.MovieRootID), optionalUUID(item.MovieQualityProfileID), optionalUUID(item.SeriesRootID), optionalUUID(item.SeriesQualityProfileID), item.SourceOwner, item.SourceSlug, now.UTC()); err != nil {
		return ImportList{}, err
	}
	next := now.UTC()
	item.NextSyncAt = &next
	item.Entries = &ImportListCounts{}
	item.SourceURL = importlists.MDBListRef{Owner: item.SourceOwner, Slug: item.SourceSlug}.URL()
	detail, _ := json.Marshal(map[string]any{"name": item.Name, "provider": "mdblist", "source": item.SourceURL, "enabled": item.Enabled})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','import_list.created','import_list',$2,$3,$4)`, uuid.NewV7().String(), item.ID.String(), detail, now.UTC()); err != nil {
		return ImportList{}, err
	}
	return item, transaction.Commit(ctx)
}

// UpdateMDBList changes a followed list's name, folders, profiles and
// whether it is followed. The list address stays. Titles already added keep
// their folder and profile; the new choice applies to titles added later.
// Turning a list back on makes its next read due at once.
func (store *ImportLists) UpdateMDBList(ctx context.Context, id uuid.UUID, settings MDBListSettings, now time.Time) (ImportList, error) {
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return ImportList{}, err
	}
	defer transaction.Rollback(ctx)
	var provider, owner, slug string
	var wasEnabled bool
	if err := transaction.QueryRow(ctx, `SELECT provider,COALESCE(source_owner,''),COALESCE(source_slug,''),enabled FROM import_lists WHERE id=$1 FOR UPDATE`, id.String()).Scan(&provider, &owner, &slug, &wasEnabled); errors.Is(err, pgx.ErrNoRows) {
		return ImportList{}, dbcore.ErrNotFound
	} else if err != nil {
		return ImportList{}, err
	}
	if provider != "mdblist" {
		return ImportList{}, invalidList("only MDBList lists can be changed here")
	}
	settings.Owner, settings.Slug = owner, slug
	settings, err = normalizeMDBListSettings(settings)
	if err != nil {
		return ImportList{}, err
	}
	item := ImportList{ID: id, Name: settings.Name, MovieRootID: settings.MovieRootID, MovieQualityProfileID: settings.MovieQualityProfileID, SeriesRootID: settings.SeriesRootID, SeriesQualityProfileID: settings.SeriesQualityProfileID}
	if err := validateTargets(ctx, transaction, &item); err != nil {
		return ImportList{}, err
	}
	var duplicate string
	if err := transaction.QueryRow(ctx, `SELECT name FROM import_lists WHERE lower(name)=lower($1) AND id<>$2 LIMIT 1`, item.Name, id.String()).Scan(&duplicate); err == nil {
		return ImportList{}, dbcore.Refuse("import_list_exists", "another list already has this name", "Choose another name.")
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return ImportList{}, err
	}
	if _, err := transaction.Exec(ctx, `UPDATE import_lists SET name=$2,enabled=$3,movie_root_id=$4,movie_quality_profile_id=$5,series_root_id=$6,series_quality_profile_id=$7,
		next_sync_at=CASE WHEN $3 AND NOT $8 THEN $9 ELSE next_sync_at END,updated_at=$9 WHERE id=$1`,
		id.String(), item.Name, settings.Enabled, optionalUUID(item.MovieRootID), optionalUUID(item.MovieQualityProfileID), optionalUUID(item.SeriesRootID), optionalUUID(item.SeriesQualityProfileID), wasEnabled, now.UTC()); err != nil {
		return ImportList{}, err
	}
	detail, _ := json.Marshal(map[string]any{"name": item.Name, "enabled": settings.Enabled})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','import_list.updated','import_list',$2,$3,$4)`, uuid.NewV7().String(), id.String(), detail, now.UTC()); err != nil {
		return ImportList{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return ImportList{}, err
	}
	return store.ImportList(ctx, id)
}

// DeleteImportList stops following a list and forgets its titles. Titles it
// added stay in the library exactly as they are. Lists with reviewed IMDb
// snapshots are kept, as their snapshots are history.
func (store *ImportLists) DeleteImportList(ctx context.Context, id uuid.UUID, now time.Time) error {
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var name string
	if err := transaction.QueryRow(ctx, `SELECT name FROM import_lists WHERE id=$1 FOR UPDATE`, id.String()).Scan(&name); errors.Is(err, pgx.ErrNoRows) {
		return dbcore.ErrNotFound
	} else if err != nil {
		return err
	}
	var snapshots bool
	if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM import_list_snapshots WHERE import_list_id=$1)`, id.String()).Scan(&snapshots); err != nil {
		return err
	}
	if snapshots {
		return dbcore.Refuse("import_list_has_history", "this list has reviewed snapshots and cannot be deleted", "Turn the list off instead.")
	}
	// A sync still waiting in the queue finds no list and does nothing.
	if _, err := transaction.Exec(ctx, `DELETE FROM import_lists WHERE id=$1`, id.String()); err != nil {
		return err
	}
	detail, _ := json.Marshal(map[string]any{"name": name})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','import_list.deleted','import_list',$2,$3,$4)`, uuid.NewV7().String(), id.String(), detail, now.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

// queueSync adds one sync event for a list unless one is already waiting.
func queueSync(ctx context.Context, transaction pgx.Tx, id string, now time.Time) (bool, error) {
	var waiting bool
	if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM outbox_events WHERE aggregate_type='import_list' AND aggregate_id=$1 AND event_type='import_list.sync_requested' AND completed_at IS NULL)`, id).Scan(&waiting); err != nil {
		return false, err
	}
	if waiting {
		return false, nil
	}
	var revision int64
	if err := transaction.QueryRow(ctx, `UPDATE import_lists SET sync_revision=sync_revision+1 WHERE id=$1 RETURNING sync_revision`, id).Scan(&revision); err != nil {
		return false, err
	}
	payload, _ := json.Marshal(map[string]string{"import_list_id": id})
	if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'import_list',$2,$3,'import_list.sync_requested',$4,$5,$5)`, uuid.NewV7().String(), id, revision, string(payload), now.UTC()); err != nil {
		return false, err
	}
	return true, nil
}

// QueueDueImportListSyncs queues a read of every followed list that is due.
func (store *ImportLists) QueueDueImportListSyncs(ctx context.Context, now time.Time) (int, error) {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer transaction.Rollback(ctx)
	rows, err := transaction.Query(ctx, `SELECT id::text FROM import_lists WHERE provider='mdblist' AND enabled AND (next_sync_at IS NULL OR next_sync_at<=$1) ORDER BY next_sync_at NULLS FIRST,id LIMIT 50 FOR UPDATE SKIP LOCKED`, now.UTC())
	if err != nil {
		return 0, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	queued := 0
	for _, id := range ids {
		added, err := queueSync(ctx, transaction, id, now)
		if err != nil {
			return 0, err
		}
		if added {
			queued++
		}
		// The handler sets the real next read; this only keeps the ticker
		// from queueing the same list again while it waits.
		if _, err := transaction.Exec(ctx, `UPDATE import_lists SET next_sync_at=$2 WHERE id=$1`, id, now.UTC().Add(MDBListSyncInterval)); err != nil {
			return 0, err
		}
	}
	return queued, transaction.Commit(ctx)
}

// QueueImportListSync queues a read of one followed list now. It reports
// false when a read is already waiting.
func (store *ImportLists) QueueImportListSync(ctx context.Context, id uuid.UUID, now time.Time) (bool, error) {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer transaction.Rollback(ctx)
	var provider string
	var enabled bool
	if err := transaction.QueryRow(ctx, `SELECT provider,enabled FROM import_lists WHERE id=$1 FOR UPDATE`, id.String()).Scan(&provider, &enabled); errors.Is(err, pgx.ErrNoRows) {
		return false, dbcore.ErrNotFound
	} else if err != nil {
		return false, err
	}
	if provider != "mdblist" || !enabled {
		return false, dbcore.Refuse("import_list_not_followed", "only a followed MDBList list that is turned on can be read now", "Turn the list on first.")
	}
	added, err := queueSync(ctx, transaction, id.String(), now)
	if err != nil {
		return false, err
	}
	return added, transaction.Commit(ctx)
}

// ImportListSeen is one title read from a list.
type ImportListSeen struct {
	MediaKind string
	TMDBID    int64
	IMDbID    string
	TVDBID    int64
	Title     string
	Year      int
	Rank      int
}

// RecordImportListRead stores what a read of the list found: new titles
// become pending, known titles get their latest name, rank and seen time.
// Titles no longer on the list are left as they are.
func (store *ImportLists) RecordImportListRead(ctx context.Context, id uuid.UUID, seen []ImportListSeen, now time.Time) (int, error) {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer transaction.Rollback(ctx)
	if err := transaction.QueryRow(ctx, `SELECT id::text FROM import_lists WHERE id=$1 FOR UPDATE`, id.String()).Scan(new(string)); errors.Is(err, pgx.ErrNoRows) {
		return 0, dbcore.ErrNotFound
	} else if err != nil {
		return 0, err
	}
	added := 0
	for _, entry := range seen {
		if entry.MediaKind != "movie" && entry.MediaKind != "series" || entry.TMDBID <= 0 || strings.TrimSpace(entry.Title) == "" {
			continue
		}
		var imdb, tvdb, year any
		if entry.IMDbID != "" {
			imdb = entry.IMDbID
		}
		if entry.TVDBID > 0 {
			tvdb = entry.TVDBID
		}
		if entry.Year >= 1800 && entry.Year <= 9999 {
			year = int16(entry.Year)
		}
		var inserted bool
		if err := transaction.QueryRow(ctx, `INSERT INTO import_list_entries(import_list_id,media_kind,tmdb_id,imdb_id,tvdb_id,title,year,rank,state,first_seen_at,last_seen_at,updated_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,'pending',$9,$9,$9)
			ON CONFLICT(import_list_id,media_kind,tmdb_id) DO UPDATE SET imdb_id=COALESCE(EXCLUDED.imdb_id,import_list_entries.imdb_id),tvdb_id=COALESCE(EXCLUDED.tvdb_id,import_list_entries.tvdb_id),
				title=EXCLUDED.title,year=COALESCE(EXCLUDED.year,import_list_entries.year),rank=EXCLUDED.rank,last_seen_at=EXCLUDED.last_seen_at
			RETURNING xmax=0`, id.String(), entry.MediaKind, entry.TMDBID, imdb, tvdb, entry.Title, year, max(entry.Rank, 0), now.UTC()).Scan(&inserted); err != nil {
			return 0, err
		}
		if inserted {
			added++
		}
	}
	return added, transaction.Commit(ctx)
}

const importListEntryColumns = `media_kind,tmdb_id,COALESCE(imdb_id,''),COALESCE(tvdb_id,0),title,COALESCE(year,0),rank,state,COALESCE(detail,''),media_id::text,attempts,first_seen_at,last_seen_at,updated_at`

func scanImportListEntry(row dbcore.RowScanner) (ImportListEntry, error) {
	var entry ImportListEntry
	var year int16
	var mediaText *string
	if err := row.Scan(&entry.MediaKind, &entry.TMDBID, &entry.IMDbID, &entry.TVDBID, &entry.Title, &year, &entry.Rank, &entry.State, &entry.Detail, &mediaText, &entry.Attempts, &entry.FirstSeenAt, &entry.LastSeenAt, &entry.UpdatedAt); err != nil {
		return ImportListEntry{}, err
	}
	entry.Year = int(year)
	entry.MediaID = parseOptionalUUID(mediaText)
	return entry, nil
}

// OpenImportListEntries returns titles still to be added: new ones first in
// list order, then ones that failed before.
func (store *ImportLists) OpenImportListEntries(ctx context.Context, id uuid.UUID, limit int) ([]ImportListEntry, error) {
	rows, err := store.Pool.Query(ctx, `SELECT `+importListEntryColumns+` FROM import_list_entries WHERE import_list_id=$1 AND state IN ('pending','failed') ORDER BY state='failed',rank,tmdb_id LIMIT $2`, id.String(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ImportListEntry{}
	for rows.Next() {
		entry, err := scanImportListEntry(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, entry)
	}
	return result, rows.Err()
}

// ImportListEntries lists every title a followed list has carried, in list
// order.
func (store *ImportLists) ImportListEntries(ctx context.Context, id uuid.UUID) ([]ImportListEntry, error) {
	rows, err := store.Pool.Query(ctx, `SELECT `+importListEntryColumns+` FROM import_list_entries WHERE import_list_id=$1 ORDER BY rank,media_kind,tmdb_id LIMIT 5000`, id.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ImportListEntry{}
	for rows.Next() {
		entry, err := scanImportListEntry(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, entry)
	}
	return result, rows.Err()
}

// SetImportListEntry records what happened to one title. A failure counts an
// attempt; the title is tried again on the next read.
func (store *ImportLists) SetImportListEntry(ctx context.Context, id uuid.UUID, kind string, tmdbID int64, state, detail string, mediaID *uuid.UUID, now time.Time) error {
	switch state {
	case "added", "in_library", "removed_earlier", "failed", "pending":
	default:
		return errors.New("invalid import-list entry state")
	}
	if len(detail) > 500 {
		detail = dbcore.TruncateText(detail, 500)
	}
	_, err := store.Pool.Exec(ctx, `UPDATE import_list_entries SET state=$4,detail=NULLIF($5,''),media_id=COALESCE($6,media_id),attempts=attempts+CASE WHEN $4='failed' THEN 1 ELSE 0 END,updated_at=$7
		WHERE import_list_id=$1 AND media_kind=$2 AND tmdb_id=$3`, id.String(), kind, tmdbID, state, detail, optionalUUID(mediaID), now.UTC())
	return err
}

// FinishImportListSync records how a read went and when the next is due.
func (store *ImportLists) FinishImportListSync(ctx context.Context, id uuid.UUID, state, detail string, synced bool, next time.Time, now time.Time) error {
	if len(detail) > 500 {
		detail = dbcore.TruncateText(detail, 500)
	}
	_, err := store.Pool.Exec(ctx, `UPDATE import_lists SET last_sync_state=$2,last_sync_detail=NULLIF($3,''),last_synced_at=CASE WHEN $4 THEN $5 ELSE last_synced_at END,next_sync_at=$6 WHERE id=$1`, id.String(), state, detail, synced, now.UTC(), next.UTC())
	return err
}

// KnownMedia finds a title already in the catalogue by any of its verified
// identities. deleted is true when the owner removed it.
func (store *ImportLists) KnownMedia(ctx context.Context, kind string, identities map[string]string) (*uuid.UUID, bool, error) {
	for _, namespace := range []string{"tmdb", "imdb", "tvdb"} {
		value := identities[namespace]
		if value == "" {
			continue
		}
		var mediaText string
		var deleted bool
		err := store.Pool.QueryRow(ctx, `SELECT identity.media_id::text,media.deleted_at IS NOT NULL FROM external_identities identity JOIN media_items media ON media.id=identity.media_id
			WHERE identity.media_kind=$1 AND identity.namespace=$2 AND identity.value=$3 AND identity.state='verified' ORDER BY media.deleted_at NULLS FIRST LIMIT 1`, kind, namespace, value).Scan(&mediaText, &deleted)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, false, err
		}
		id, parseErr := uuid.Parse(mediaText)
		if parseErr != nil {
			return nil, false, parseErr
		}
		return &id, deleted, nil
	}
	return nil, false, nil
}
