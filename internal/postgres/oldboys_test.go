package postgres

import (
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
