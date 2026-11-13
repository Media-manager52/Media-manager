package postgres

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// TestEnsureBundledAltMountAddsItOnce: the AltMount installed together with
// Media Manager becomes the default AltMount connection on the first start,
// and never again: not on a later start, and not when the owner already has
// an AltMount of their own.
func TestEnsureBundledAltMountAddsItOnce(t *testing.T) {
	store, ctx := isolatedCommissioningStore(t)
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	if _, err := store.EnsureBundledAltMount(ctx, "ftp://altmount", now); !errors.Is(err, ErrInvalidBundledAltMountURL) {
		t.Fatalf("ftp URL: %v", err)
	}
	if _, err := store.Pool.Exec(ctx, `DELETE FROM integration_instances WHERE kind='altmount'`); err != nil {
		t.Fatal(err)
	}
	added, err := store.EnsureBundledAltMount(ctx, " http://mm-altmount:8080/ ", now)
	if err != nil || !added {
		t.Fatalf("first start: added=%v err=%v", added, err)
	}
	if added, err := store.EnsureBundledAltMount(ctx, "http://mm-altmount:8080", now); err != nil || added {
		t.Fatalf("second start: added=%v err=%v", added, err)
	}
	integrations, err := store.ListIntegrations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, integration := range integrations {
		if integration.Kind != "altmount" {
			continue
		}
		found++
		var config map[string]any
		if err := json.Unmarshal(integration.Config, &config); err != nil {
			t.Fatal(err)
		}
		if !integration.Enabled || !integration.Default || config["base_url"] != "http://mm-altmount:8080" || config["allow_http"] != true || config["authentication"] != "anonymous_admin" || config["local_read_only_root"] != BundledAltMountMountRoot || config["category"] != "media-manager" {
			t.Fatalf("bundled connection: %+v %s", integration, integration.Config)
		}
	}
	if found != 1 {
		t.Fatalf("altmount connections=%d, want 1", found)
	}
	var audits int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='integration.bundled_altmount_added'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audits=%d err=%v", audits, err)
	}

	// The owner's own AltMount is kept, even when it is disabled.
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET enabled=false, config=jsonb_set(config,'{base_url}','"http://own-altmount:8080"') WHERE kind='altmount'`); err != nil {
		t.Fatal(err)
	}
	if added, err := store.EnsureBundledAltMount(ctx, "http://mm-altmount:8080", now); err != nil || added {
		t.Fatalf("with the owner's AltMount: added=%v err=%v", added, err)
	}
}
