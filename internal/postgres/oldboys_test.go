package postgres

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/oldboyscredentials"
)

func TestNativeCommissionPreservesProviderAndReplacementStalesSnapshots(t *testing.T) {
	store, ctx := isolatedCommissioningStore(t)
	vault, err := oldboyscredentials.New(store, strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	credentials := oldboyscredentials.Credentials{APIToken: "fixture-api", PassKey: "fixture-pid", RSSKey: "fixture-rid"}
	status, err := vault.Save(ctx, credentials)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := store.CreateIntegration(ctx, Integration{Kind: "newznab", Name: "OldBoys {DK}", SecretRef: "NEWZNAB_OLDBOYS_DK_API_KEY", Config: []byte(`{"base_url":"http://danish-intelligence:9699/ob/api","allow_http":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := store.PrepareOldBoysCommission(ctx, *status.UpdatedAt)
	if err != nil || attempt.ID != legacy.ID || attempt.Enabled || attempt.Kind != "oldboys" {
		t.Fatal("legacy provider identity not preserved", err)
	}
	if err := store.ValidateOldBoysSnapshot(ctx, attempt, true); err != nil {
		t.Fatal(err)
	}
	newer, err := store.PrepareOldBoysCommission(ctx, *status.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ValidateOldBoysSnapshot(ctx, attempt, true); !errors.Is(err, ErrConcurrentModification) {
		t.Fatal("superseded commission could send")
	}
	if _, err := store.CompleteOldBoysCommission(ctx, attempt, time.Now().UTC()); !errors.Is(err, ErrConcurrentModification) {
		t.Fatal("superseded proof activated")
	}
	enabled, err := store.CompleteOldBoysCommission(ctx, newer, time.Now().UTC())
	if err != nil || !enabled.Enabled {
		t.Fatal("current proof not activated", err)
	}
	if phase, err := store.OldBoysSearchPhase(ctx, enabled.ID, enabled); err != nil || phase != 0 {
		t.Fatal("new native provider did not start at page one", err)
	}
	if err := store.ValidateOldBoysSnapshot(ctx, enabled, false); err != nil {
		t.Fatal(err)
	}
	liveStatus, err := vault.Status(ctx)
	if err != nil || !liveStatus.Enabled {
		t.Fatal("enabled status missing", err)
	}
	if _, err := vault.Save(ctx, credentials); err != nil {
		t.Fatal(err)
	}
	current, err := store.Integration(ctx, enabled.ID)
	if err != nil || current.Enabled {
		t.Fatal("replacement did not disable native provider")
	}
	if phase, err := store.OldBoysSearchPhase(ctx, enabled.ID, enabled); err != nil || phase != 0 {
		t.Fatal("new native provider did not start at page one", err)
	}
	if err := store.ValidateOldBoysSnapshot(ctx, enabled, false); !errors.Is(err, ErrConcurrentModification) {
		t.Fatal("stale native credential client could send")
	}
	if _, err := store.PrepareOldBoysCommission(ctx, *status.UpdatedAt); !errors.Is(err, ErrConcurrentModification) {
		t.Fatal("stale credentials commissioned")
	}
}
func TestOldBoysScopeIsDanishOriginalMoviesAndSeriesOnly(t *testing.T) {
	for _, spec := range []struct {
		kind     catalog.MediaKind
		language string
		want     bool
	}{{catalog.KindMovie, "da", true}, {catalog.KindSeries, "da", true}, {catalog.KindMovie, "en", false}, {catalog.KindSeries, "", false}} {
		item := catalog.MediaItem{Kind: spec.kind, Metadata: &catalog.MediaMetadata{OriginalLanguage: spec.language}}
		if OldBoysMediaAllowed(item) != spec.want {
			t.Fatal("incorrect Danish-only scope")
		}
	}
}

func TestOldBoysOwnerBudgetIsAuditedSurvivesRecommissionAndKeepsPaging(t *testing.T) {
	store, ctx := isolatedCommissioningStore(t)
	if _, err := store.SetOldBoysBudget(ctx, 5000, 0, time.Now().UTC()); !errors.Is(err, ErrNotFound) {
		t.Fatal("budget accepted before commissioning", err)
	}
	vault, err := oldboyscredentials.New(store, strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	status, err := vault.Save(ctx, oldboyscredentials.Credentials{APIToken: "fixture-api", PassKey: "fixture-pid", RSSKey: "fixture-rid"})
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := store.PrepareOldBoysCommission(ctx, *status.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	enabled, err := store.CompleteOldBoysCommission(ctx, attempt, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range [][2]int{{0, 0}, {-1, 10}, {MaxOldBoysQueryLimit + 1, 0}, {5000, MaxOldBoysGrabLimit + 1}} {
		if _, err := store.SetOldBoysBudget(ctx, invalid[0], invalid[1], time.Now().UTC()); !errors.Is(err, ErrInvalidOldBoysBudget) {
			t.Fatal("invalid budget accepted", invalid, err)
		}
	}
	updated, err := store.SetOldBoysBudget(ctx, 5000, 0, time.Now().UTC().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		QueryLimit     int    `json:"query_limit"`
		GrabLimit      int    `json:"grab_limit"`
		Source         string `json:"budget_source"`
		Interval       int    `json:"minimum_interval_ms"`
		Verified       bool   `json:"mode_verified"`
		CommissionedAt string `json:"commissioned_at"`
	}
	if err := json.Unmarshal(updated.Config, &config); err != nil || config.QueryLimit != 5000 || config.GrabLimit != 100 || config.Source != "owner_stated" || config.Interval != 5000 || !config.Verified || !updated.Enabled {
		t.Fatalf("budget not applied exactly: %+v %v", config, err)
	}
	if err := store.ValidateOldBoysSnapshot(ctx, updated, false); err != nil {
		t.Fatal("updated provider cannot send", err)
	}
	if !updated.UpdatedAt.After(enabled.UpdatedAt) || !oldBoysPhaseAnchor(updated).Equal(oldBoysPhaseAnchor(enabled)) {
		t.Fatal("budget edit moved the native paging anchor")
	}
	var audits int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='oldboys.budget_set' AND aggregate_id=$1 AND detail->>'query_limit'='5000' AND detail->>'previous_query_limit'='1000'`, enabled.ID.String()).Scan(&audits); err != nil || audits != 1 {
		t.Fatal("budget change not audited", audits, err)
	}
	if _, err := vault.Save(ctx, oldboyscredentials.Credentials{APIToken: "fixture-api-2", PassKey: "fixture-pid-2", RSSKey: "fixture-rid-2"}); err != nil {
		t.Fatal(err)
	}
	replaced, err := vault.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	again, err := store.PrepareOldBoysCommission(ctx, *replaced.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(again.Config, &config); err != nil || config.QueryLimit != 5000 || config.GrabLimit != 100 || config.Source != "owner_stated" || config.Verified {
		t.Fatalf("recommission lost owner budget: %+v %v", config, err)
	}
}
