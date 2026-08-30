package publication

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"uuid"

	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/packs"
	"github.com/media-manager/media-manager/internal/postgres"
)

func TestBuildSeasonPackPlansEveryVerifiedGroup(t *testing.T) {
	year := uint16(2026)
	context := postgres.PublicationContext{
		Media:                 catalog.MediaItem{Kind: catalog.KindSeries, Title: "Show", Year: &year, Identities: []catalog.ExternalIdentity{{Namespace: "tvdb", Value: "123", State: catalog.IdentityVerified}}},
		Root:                  postgres.LibraryRoot{DestinationRoot: "/library/tv", SourceVisibleRoot: "/altmount", PlexVisibleRoot: "/tv"},
		AltMount:              postgres.Integration{Config: json.RawMessage(`{"local_read_only_root":"/source"}`)},
		InventoryRootRelative: "queue/17",
		Assets:                []postgres.PublicationAsset{{ID: uuid.NewV7(), RelativePath: "Show.S01E01-E02.mkv", Episodes: []packs.ExpectedEpisode{{Key: packs.EpisodeKey{Season: 1, Episode: 1}}, {Key: packs.EpisodeKey{Season: 1, Episode: 2}}}}},
	}
	plans, links, err := buildPlans(context)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || len(links) != 1 {
		t.Fatalf("unexpected plans: %d %d", len(plans), len(links))
	}
	if plans[0].SourceRelative != filepath.Join("queue", "17", "Show.S01E01-E02.mkv") {
		t.Fatalf("wrong source path %q", plans[0].SourceRelative)
	}
	if plans[0].DestinationRelative != filepath.Join("Show (2026) {tvdb-123}", "Season 01", "Show (2026) - S01E01-E02.mkv") {
		t.Fatalf("wrong destination %q", plans[0].DestinationRelative)
	}
}
