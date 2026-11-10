package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
)

// DeleteLibraryRoot removes a library root from the settings. It never touches
// the folder on disk. The root must not be the default and nothing may refer
// to it: no title (finished ones included), published link, adopted file,
// recycle entry, relocation, import list or Seerr route. Those references are
// the record of where files are, so a root that has ever held a title can only
// be disabled.
func (store *Store) DeleteLibraryRoot(ctx context.Context, id uuid.UUID, actor string, now time.Time) error {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var name, kind string
	var isDefault bool
	if err := transaction.QueryRow(ctx, `SELECT name,media_kind,is_default FROM library_roots WHERE id=$1 FOR UPDATE`, id.String()).Scan(&name, &kind, &isDefault); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if isDefault {
		return refuse("library_root_is_default", "the default library root cannot be deleted", "Make another root of the same media kind the default first.")
	}
	inUse := refuse("library_root_in_use", "the library root is used by titles, files or a Seerr route", "Disable it instead; a root that has held titles is part of their history.")
	// Seerr routes would be removed silently by their ON DELETE CASCADE, so
	// they are checked here; every other reference is refused by its own
	// foreign key.
	var routed bool
	if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM seerr_library_routes WHERE library_root_id=$1)`, id.String()).Scan(&routed); err != nil {
		return err
	}
	if routed {
		return inUse
	}
	if _, err := transaction.Exec(ctx, `DELETE FROM library_roots WHERE id=$1`, id.String()); err != nil {
		if isForeignKeyViolation(err) {
			return inUse
		}
		return err
	}
	detail, _ := json.Marshal(map[string]string{"name": name, "media_kind": kind})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,$2,'library.root_deleted','library_root',$3,$4,$5)`, uuid.NewV7().String(), actor, id.String(), string(detail), now); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

// DeleteIntegration removes a connection or an indexer together with its
// stored secret and connection checks. It must be disabled first, and nothing
// may refer to it: no search, release, download, request, library root, Seerr
// route, budget or commissioning history. An integration that has done real
// work therefore stays (disabled) as part of that history.
func (store *Store) DeleteIntegration(ctx context.Context, id uuid.UUID, actor string, now time.Time) error {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var name, kind string
	var enabled bool
	if err := transaction.QueryRow(ctx, `SELECT name,kind,enabled FROM integration_instances WHERE id=$1 FOR UPDATE`, id.String()).Scan(&name, &kind, &enabled); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if enabled {
		return refuse("integration_enabled", "an enabled integration cannot be deleted", "Disable it first.")
	}
	inUse := refuse("integration_in_use", "the integration has history or is used by other settings", "Leave it disabled; its searches, downloads or requests are part of the titles' history.")
	// These three would be removed silently by ON DELETE CASCADE; every other
	// reference is refused by its own foreign key.
	var cascades bool
	if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM seerr_library_routes WHERE seerr_instance_id=$1)
		OR EXISTS(SELECT 1 FROM indexer_request_reservations WHERE indexer_instance_id=$1)
		OR EXISTS(SELECT 1 FROM streaming_choices WHERE integration_id=$1 AND expires_at>$2)`, id.String(), now.UTC()).Scan(&cascades); err != nil {
		return err
	}
	if cascades {
		return inUse
	}
	if _, err := transaction.Exec(ctx, `DELETE FROM integration_instances WHERE id=$1`, id.String()); err != nil {
		if isForeignKeyViolation(err) {
			return inUse
		}
		return err
	}
	detail, _ := json.Marshal(map[string]string{"name": name, "kind": kind})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,$2,'integration.deleted','integration',$3,$4,$5)`, uuid.NewV7().String(), actor, id.String(), string(detail), now); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}
