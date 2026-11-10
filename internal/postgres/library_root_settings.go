package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// LibraryRootPatch changes a root's display name, which root is the default
// for its media kind and whether a root is enabled. Nil fields are left
// unchanged. The name is only a label: folders and Plex paths do not change.
type LibraryRootPatch struct {
	Name    *string `json:"name"`
	Default *bool   `json:"is_default"`
	Enabled *bool   `json:"enabled"`
}

// UpdateLibraryRoot applies an owner patch to one library root. Making a root
// the default clears the default flag of every other root of the same media
// kind. A root can be disabled only when nothing still depends on it: it is
// not the default, no open workflow targets it, it holds no current published
// links, and no enabled Seerr route sends requests to it. Relocate workflows
// (POST /api/v1/workflows/{id}/relocate-links) before disabling a root.
func (store *Store) UpdateLibraryRoot(ctx context.Context, id uuid.UUID, patch LibraryRootPatch, now time.Time) (LibraryRoot, error) {
	if patch.Name == nil && patch.Default == nil && patch.Enabled == nil {
		return LibraryRoot{}, refuse("empty_patch", "nothing to change", "Send name, is_default and/or enabled.")
	}
	if patch.Name != nil {
		trimmed := strings.TrimSpace(*patch.Name)
		if trimmed == "" || len(trimmed) > 120 {
			return LibraryRoot{}, refuse("invalid_name", "the name must be 1 to 120 characters", "Send a short display name.")
		}
		patch.Name = &trimmed
	}
	tx, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return LibraryRoot{}, err
	}
	defer tx.Rollback(ctx)
	var kind, edition string
	var isDefault, enabled bool
	if err := tx.QueryRow(ctx, `SELECT media_kind,is_default,enabled,edition FROM library_roots WHERE id=$1 FOR UPDATE`, id.String()).Scan(&kind, &isDefault, &enabled, &edition); errors.Is(err, pgx.ErrNoRows) {
		return LibraryRoot{}, ErrNotFound
	} else if err != nil {
		return LibraryRoot{}, err
	}
	wantDefault, wantEnabled := isDefault, enabled
	if patch.Default != nil {
		wantDefault = *patch.Default
	}
	if patch.Enabled != nil {
		wantEnabled = *patch.Enabled
	}
	if isDefault && !wantDefault {
		return LibraryRoot{}, refuse("invalid_default_change", "a media kind always needs a default root", "Make another root of the same media kind the default instead.")
	}
	if wantDefault && edition == Edition4K {
		return LibraryRoot{}, refuse("invalid_default_change", "a 4K root cannot be the default", "4K roots are only used for 4K requests; choose a standard root as the default.")
	}
	if wantDefault && !wantEnabled {
		return LibraryRoot{}, refuse("invalid_default_change", "a disabled root cannot be the default", "Enable the root, or choose an enabled root as the default.")
	}
	if enabled && !wantEnabled {
		var openWorkflows, currentLinks, routes int
		if err := tx.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM workflows WHERE root_id=$1 AND terminal_at IS NULL),
			(SELECT count(*) FROM library_links WHERE root_id=$1 AND current_version_id IS NOT NULL),
			(SELECT count(*) FROM seerr_library_routes WHERE library_root_id=$1 AND enabled)`, id.String()).Scan(&openWorkflows, &currentLinks, &routes); err != nil {
			return LibraryRoot{}, err
		}
		if openWorkflows > 0 || currentLinks > 0 || routes > 0 {
			return LibraryRoot{}, refuse("library_root_in_use", "the library root is still in use", "Relocate its workflows (relocate-links) and point its Seerr routes elsewhere, then disable it.")
		}
	}
	if wantDefault && !isDefault {
		if _, err := tx.Exec(ctx, `UPDATE library_roots SET is_default=false WHERE media_kind=$1 AND id<>$2`, kind, id.String()); err != nil {
			return LibraryRoot{}, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE library_roots SET is_default=$2,enabled=$3,name=COALESCE($4,name) WHERE id=$1`, id.String(), wantDefault, wantEnabled, patch.Name); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "library_roots_media_kind_name_key" {
			return LibraryRoot{}, refuse("invalid_name", "another library root of this media kind already has that name", "Choose a different name.")
		}
		return LibraryRoot{}, err
	}
	detail, _ := json.Marshal(map[string]any{"media_kind": kind, "is_default": map[string]bool{"from": isDefault, "to": wantDefault}, "enabled": map[string]bool{"from": enabled, "to": wantEnabled}, "renamed_to": patch.Name})
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','library.root_updated','library_root',$2,$3,$4)`, uuid.NewV7().String(), id.String(), string(detail), now.UTC()); err != nil {
		return LibraryRoot{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return LibraryRoot{}, err
	}
	roots, err := store.ListLibraryRoots(ctx)
	if err != nil {
		return LibraryRoot{}, err
	}
	for _, root := range roots {
		if root.ID == id {
			return root, nil
		}
	}
	return LibraryRoot{}, ErrNotFound
}
