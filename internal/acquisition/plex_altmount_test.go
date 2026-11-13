package acquisition

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/media-manager/media-manager/internal/postgres"
	"uuid"
)

func TestCheckPlexSeesAltMountComparesPlexWithTheDrive(t *testing.T) {
	plexID, otherPlexID, altID := uuid.NewV7(), uuid.NewV7(), uuid.NewV7()
	altConfig, _ := json.Marshal(map[string]any{"local_read_only_root": "/mnt/altmount"})
	integrations := []postgres.Integration{
		{ID: altID, Kind: "altmount", Name: "AltMount", Enabled: true, Default: true, Config: altConfig},
		{ID: plexID, Kind: "plex", Name: "Stue", Enabled: true},
		{ID: otherPlexID, Kind: "plex", Name: "Kontor", Enabled: true},
	}
	root := func(plex uuid.UUID, path string, enabled bool) postgres.LibraryRoot {
		id := plex
		return postgres.LibraryRoot{ID: uuid.NewV7(), Enabled: enabled, PlexInstanceID: &id, SourceVisibleRoot: path}
	}
	roots := []postgres.LibraryRoot{
		root(plexID, "/mnt/altmount", true),
		root(plexID, "/mnt/altmount", true),       // same view: checked once
		root(plexID, "/old/altmount", false),      // disabled: not checked
		root(otherPlexID, "/data/altmount", true), // another Plex
	}
	local := func(string) ([]string, error) { return []string{"media-manager", "complete"}, nil }
	calls := map[string]int{}
	list := func(plexListing map[string][]string, failing map[string]bool) PlexFolderLister {
		return func(_ context.Context, plex postgres.Integration, path string) ([]string, error) {
			calls[plex.Name+path]++
			if failing[plex.Name+path] {
				return nil, errors.New("plex refused")
			}
			return plexListing[plex.Name+path], nil
		}
	}

	views := CheckPlexSeesAltMount(context.Background(), integrations, roots, local, list(map[string][]string{
		"Stue/mnt/altmount":    {"/mnt/altmount/complete", "/mnt/altmount/media-manager/"},
		"Kontor/data/altmount": {}, // a dead copy of the drive lists nothing
	}, nil))
	got := map[string]PlexAltMountView{}
	for _, view := range views {
		got[view.PlexName] = view
	}
	if len(views) != 2 || got["Stue"].State != PlexAltMountOK || got["Kontor"].State != PlexAltMountMissing {
		t.Fatalf("views = %+v", views)
	}
	if !reflect.DeepEqual(got["Kontor"].Missing, []string{"complete", "media-manager"}) || got["Kontor"].Path != "/data/altmount" {
		t.Fatalf("missing view = %+v", got["Kontor"])
	}
	if calls["Stue/mnt/altmount"] != 1 || calls["Stue/old/altmount"] != 0 {
		t.Fatalf("calls = %v", calls)
	}

	views = CheckPlexSeesAltMount(context.Background(), integrations, roots[:1], local, list(nil, map[string]bool{"Stue/mnt/altmount": true}))
	if len(views) != 1 || views[0].State != PlexAltMountUnreadable {
		t.Fatalf("unreadable views = %+v", views)
	}

	// Nothing to compare: an empty drive, or a drive Media Manager cannot read
	// itself (reported separately as the AltMount drive), never asks Plex.
	calls = map[string]int{}
	empty := func(string) ([]string, error) { return []string{}, nil }
	if views := CheckPlexSeesAltMount(context.Background(), integrations, roots[:1], empty, list(nil, nil)); len(views) != 1 || views[0].State != PlexAltMountEmpty {
		t.Fatalf("empty views = %+v", views)
	}
	broken := func(string) ([]string, error) { return nil, errors.New("transport endpoint is not connected") }
	if views := CheckPlexSeesAltMount(context.Background(), integrations, roots[:1], broken, list(nil, nil)); len(views) != 1 || views[0].State != PlexAltMountUnknown {
		t.Fatalf("unknown views = %+v", views)
	}
	if len(calls) != 0 {
		t.Fatalf("Plex was asked without anything to compare: %v", calls)
	}

	// Without one enabled AltMount connection there is nothing to check.
	if views := CheckPlexSeesAltMount(context.Background(), integrations[1:], roots, local, list(nil, nil)); len(views) != 0 {
		t.Fatalf("views without AltMount = %+v", views)
	}
}
