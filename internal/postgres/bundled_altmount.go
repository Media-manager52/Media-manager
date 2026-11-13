package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"
	"uuid"
)

// BundledAltMountMountRoot is where the API container sees the bundled
// AltMount's files: the install bind-mounts the folder that holds AltMount's
// FUSE mount at /mnt, and AltMount mounts at /mnt/altmount inside its own
// container.
const BundledAltMountMountRoot = "/mnt/altmount"

// ErrInvalidBundledAltMountURL is returned for a BUNDLED_ALTMOUNT_URL that is
// not a plain http(s) address.
var ErrInvalidBundledAltMountURL = errors.New("BUNDLED_ALTMOUNT_URL must be an http(s) address such as http://media-manager-altmount:8080")

// EnsureBundledAltMount makes the AltMount installed together with Media
// Manager its default AltMount connection, once. It does nothing when any
// AltMount connection already exists: the owner's own AltMount, or this one
// from an earlier start (even if the owner has since changed or disabled it).
// The bundled AltMount runs only on Media Manager's private network without a
// login, so the connection logs in as its admin (anonymous_admin).
func (store *Store) EnsureBundledAltMount(ctx context.Context, baseURL string, now time.Time) (bool, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false, ErrInvalidBundledAltMountURL
	}
	config, err := json.Marshal(map[string]any{
		"base_url":             parsed.Scheme + "://" + parsed.Host,
		"allow_http":           parsed.Scheme == "http",
		"authentication":       "anonymous_admin",
		"category":             "media-manager",
		"reported_root":        "/",
		"local_read_only_root": BundledAltMountMountRoot,
		"request_timeout_ms":   30000,
		"bundled":              true,
	})
	if err != nil {
		return false, err
	}
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer transaction.Rollback(ctx)
	// The API and the worker start from the same binary; only one adds it.
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('bundled-altmount',0))`); err != nil {
		return false, err
	}
	var existing bool
	if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM integration_instances WHERE kind='altmount')`).Scan(&existing); err != nil {
		return false, err
	}
	if existing {
		return false, nil
	}
	id := uuid.NewV7().String()
	if _, err := transaction.Exec(ctx, `INSERT INTO integration_instances(id,kind,name,enabled,is_default,config,secret_ref,created_at,updated_at) VALUES($1,'altmount','AltMount',true,true,$2,NULL,$3,$3)`, id, string(config), now.UTC()); err != nil {
		return false, err
	}
	detail, err := json.Marshal(map[string]string{"base_url": parsed.Scheme + "://" + parsed.Host})
	if err != nil {
		return false, err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'system','integration.bundled_altmount_added','integration',$2,$3,$4)`, uuid.NewV7().String(), id, string(detail), now.UTC()); err != nil {
		return false, err
	}
	return true, transaction.Commit(ctx)
}
