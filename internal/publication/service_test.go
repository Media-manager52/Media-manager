package publication

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"uuid"

	"github.com/media-manager/media-manager/internal/adapters/plex"
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

func TestBuildPlansPinsContainedPlexVisibleTarget(t *testing.T) {
	t.Parallel()
	year := uint16(2026)
	context := postgres.PublicationContext{
		Media:                 catalog.MediaItem{Kind: catalog.KindMovie, Title: "Movie", Year: &year, Identities: []catalog.ExternalIdentity{{Namespace: "tmdb", Value: "42", State: catalog.IdentityVerified}}},
		Root:                  postgres.LibraryRoot{DestinationRoot: "/library/movies", SourceVisibleRoot: "/plex/altmount", PlexVisibleRoot: "/plex/movies"},
		AltMount:              postgres.Integration{Config: json.RawMessage(`{"local_read_only_root":"/source"}`)},
		InventoryRootRelative: "queue/17",
		ReleaseToken:          "MM-01234567",
		Assets:                []postgres.PublicationAsset{{ID: uuid.NewV7(), RelativePath: "release/Movie.mkv"}},
	}
	plans, links, err := buildPlans(context)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || len(links) != 1 {
		t.Fatalf("unexpected plans: %d %d", len(plans), len(links))
	}
	if plans[0].SourceRelative != filepath.Join("queue", "17", "release", "Movie.mkv") {
		t.Fatalf("unexpected persisted source %q", plans[0].SourceRelative)
	}
	if plans[0].PlexVisibleTarget != "/plex/altmount/queue/17/release/Movie.mkv" {
		t.Fatalf("unexpected Plex-visible target %q", plans[0].PlexVisibleTarget)
	}
	if links[0].SourceRoot != "/source/queue/17" {
		t.Fatalf("unexpected source root %q", links[0].SourceRoot)
	}
	if links[0].DestinationRoot != "/library/movies" {
		t.Fatalf("unexpected destination root %q", links[0].DestinationRoot)
	}
}

func TestBuildPlansRefusesUnverifiedOrIncompleteSources(t *testing.T) {
	t.Parallel()
	year := uint16(2026)
	base := func() postgres.PublicationContext {
		return postgres.PublicationContext{
			Media:                 catalog.MediaItem{Kind: catalog.KindMovie, Title: "Movie", Year: &year, Identities: []catalog.ExternalIdentity{{Namespace: "tmdb", Value: "42", State: catalog.IdentityVerified}}},
			Root:                  postgres.LibraryRoot{DestinationRoot: "/library/movies", SourceVisibleRoot: "/altmount", PlexVisibleRoot: "/movies"},
			AltMount:              postgres.Integration{Config: json.RawMessage(`{"local_read_only_root":"/source"}`)},
			InventoryRootRelative: "queue/17",
			Assets:                []postgres.PublicationAsset{{ID: uuid.NewV7(), RelativePath: "Movie.mkv"}},
		}
	}
	spanningSeasons := []packs.ExpectedEpisode{{Key: packs.EpisodeKey{Season: 1, Episode: 1}}, {Key: packs.EpisodeKey{Season: 2, Episode: 1}}}
	nonContiguous := []packs.ExpectedEpisode{{Key: packs.EpisodeKey{Season: 1, Episode: 1}}, {Key: packs.EpisodeKey{Season: 1, Episode: 3}}}
	seriesIdentity := func(c *postgres.PublicationContext) {
		c.Media.Kind = catalog.KindSeries
		c.Media.Identities = []catalog.ExternalIdentity{{Namespace: "tvdb", Value: "42", State: catalog.IdentityVerified}}
	}
	cases := []struct {
		name   string
		mutate func(*postgres.PublicationContext)
	}{
		{name: "missing year", mutate: func(c *postgres.PublicationContext) { c.Media.Year = nil }},
		{name: "unverified identity", mutate: func(c *postgres.PublicationContext) {
			c.Media.Identities = []catalog.ExternalIdentity{{Namespace: "tmdb", Value: "42", State: catalog.IdentityUnverified}}
		}},
		{name: "no plex supported identity", mutate: func(c *postgres.PublicationContext) {
			c.Media.Identities = []catalog.ExternalIdentity{{Namespace: "simkl", Value: "42", State: catalog.IdentityVerified}}
		}},
		{name: "relative altmount root", mutate: func(c *postgres.PublicationContext) {
			c.AltMount.Config = json.RawMessage(`{"local_read_only_root":"source"}`)
		}},
		{name: "escaping asset path", mutate: func(c *postgres.PublicationContext) {
			c.Assets = []postgres.PublicationAsset{{ID: uuid.NewV7(), RelativePath: "../Movie.mkv"}}
		}},
		{name: "series asset without episodes", mutate: func(c *postgres.PublicationContext) {
			seriesIdentity(c)
			c.Assets = []postgres.PublicationAsset{{ID: uuid.NewV7(), RelativePath: "Show.S01E01.mkv"}}
		}},
		{name: "series asset spanning seasons", mutate: func(c *postgres.PublicationContext) {
			seriesIdentity(c)
			c.Assets = []postgres.PublicationAsset{{ID: uuid.NewV7(), RelativePath: "Show.S01E01.mkv", Episodes: spanningSeasons}}
		}},
		{name: "series asset with non-contiguous episodes", mutate: func(c *postgres.PublicationContext) {
			seriesIdentity(c)
			c.Assets = []postgres.PublicationAsset{{ID: uuid.NewV7(), RelativePath: "Show.S01E01E03.mkv", Episodes: nonContiguous}}
		}},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			context := base()
			testCase.mutate(&context)
			if _, _, err := buildPlans(context); err == nil {
				t.Fatal("buildPlans accepted an unsafe or unverified source")
			}
		})
	}
}

func TestBuildPlansRejectsUnsafeCompanions(t *testing.T) {
	t.Parallel()
	year := uint16(2026)
	base := func(companion postgres.PublicationCompanion) postgres.PublicationContext {
		return postgres.PublicationContext{
			Media:                 catalog.MediaItem{Kind: catalog.KindMovie, Title: "Movie", Year: &year, Identities: []catalog.ExternalIdentity{{Namespace: "tmdb", Value: "42", State: catalog.IdentityVerified}}},
			Root:                  postgres.LibraryRoot{DestinationRoot: "/library/movies", SourceVisibleRoot: "/altmount", PlexVisibleRoot: "/movies"},
			AltMount:              postgres.Integration{Config: json.RawMessage(`{"local_read_only_root":"/source"}`)},
			InventoryRootRelative: "queue/17",
			Assets:                []postgres.PublicationAsset{{ID: uuid.NewV7(), RelativePath: "Movie.mkv", Companions: []postgres.PublicationCompanion{companion}}},
		}
	}
	for _, testCase := range []struct {
		name      string
		companion postgres.PublicationCompanion
	}{
		{name: "unsupported extension", companion: postgres.PublicationCompanion{AssetID: uuid.NewV7(), RelativePath: "Movie.da.exe", Language: "da"}},
		{name: "missing language", companion: postgres.PublicationCompanion{AssetID: uuid.NewV7(), RelativePath: "Movie.da.srt"}},
		{name: "escaping companion", companion: postgres.PublicationCompanion{AssetID: uuid.NewV7(), RelativePath: "../Movie.da.srt", Language: "da"}},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if _, _, err := buildPlans(base(testCase.companion)); err == nil {
				t.Fatalf("unsafe companion %+v was accepted", testCase.companion)
			}
		})
	}
}

func TestBuildSeasonPackPlansUseDistinctReleaseIdentity(t *testing.T) {
	t.Parallel()
	year := uint16(2026)
	base := postgres.PublicationContext{
		Media:                 catalog.MediaItem{Kind: catalog.KindSeries, Title: "Show", Year: &year, Identities: []catalog.ExternalIdentity{{Namespace: "tvdb", Value: "123", State: catalog.IdentityVerified}}},
		Root:                  postgres.LibraryRoot{DestinationRoot: "/library/tv", SourceVisibleRoot: "/altmount", PlexVisibleRoot: "/tv"},
		AltMount:              postgres.Integration{Config: json.RawMessage(`{"local_read_only_root":"/source"}`)},
		InventoryRootRelative: "queue/17",
		Assets:                []postgres.PublicationAsset{{ID: uuid.NewV7(), RelativePath: "Show.S01E01-E02.mkv", Episodes: []packs.ExpectedEpisode{{Key: packs.EpisodeKey{Season: 1, Episode: 1}}, {Key: packs.EpisodeKey{Season: 1, Episode: 2}}}}},
	}
	first, second := base, base
	first.ReleaseToken = "MM-01234567"
	second.ReleaseToken = "MM-89abcdef"
	firstPlans, firstLinks, err := buildPlans(first)
	if err != nil {
		t.Fatal(err)
	}
	secondPlans, secondLinks, err := buildPlans(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstPlans[0].DestinationRelative == secondPlans[0].DestinationRelative {
		t.Fatalf("different selected releases collided at %q", firstPlans[0].DestinationRelative)
	}
	for _, link := range append(firstLinks, secondLinks...) {
		if !strings.HasPrefix(link.DestinationRel, filepath.Join("Show (2026) {tvdb-123}", "Season 01")) {
			t.Fatalf("season pack escaped its deterministic folder: %q", link.DestinationRel)
		}
	}
}

func TestBuildSeriesPlansFallBackToTMDBIdentity(t *testing.T) {
	t.Parallel()
	year := uint16(2026)
	context := postgres.PublicationContext{
		Media:                 catalog.MediaItem{Kind: catalog.KindSeries, Title: "Show", Year: &year, Identities: []catalog.ExternalIdentity{{Namespace: "tmdb", Value: "99", State: catalog.IdentityVerified}}},
		Root:                  postgres.LibraryRoot{DestinationRoot: "/library/tv", SourceVisibleRoot: "/altmount", PlexVisibleRoot: "/tv"},
		AltMount:              postgres.Integration{Config: json.RawMessage(`{"local_read_only_root":"/source"}`)},
		InventoryRootRelative: "queue/17",
		Assets:                []postgres.PublicationAsset{{ID: uuid.NewV7(), RelativePath: "Show.S01E01.mkv", Episodes: []packs.ExpectedEpisode{{Key: packs.EpisodeKey{Season: 1, Episode: 1}}}}},
	}
	plans, _, err := buildPlans(context)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plans[0].DestinationRelative, "{tmdb-99}") {
		t.Fatalf("tmdb fallback identity missing from %q", plans[0].DestinationRelative)
	}
}

func TestBuildMoviePlansUseDistinctReleaseIdentity(t *testing.T) {
	year := uint16(2026)
	base := postgres.PublicationContext{
		Media:                 catalog.MediaItem{Kind: catalog.KindMovie, Title: "Movie", Year: &year, Identities: []catalog.ExternalIdentity{{Namespace: "tmdb", Value: "42", State: catalog.IdentityVerified}}},
		Root:                  postgres.LibraryRoot{DestinationRoot: "/library/movies", SourceVisibleRoot: "/altmount", PlexVisibleRoot: "/movies"},
		AltMount:              postgres.Integration{Config: json.RawMessage(`{"local_read_only_root":"/source"}`)},
		InventoryRootRelative: "queue/17",
		Assets:                []postgres.PublicationAsset{{ID: uuid.NewV7(), RelativePath: "Movie.1080p.mkv"}},
	}
	first := base
	first.ReleaseToken = "MM-01234567"
	second := base
	second.ReleaseToken = "MM-89abcdef"
	firstPlans, _, err := buildPlans(first)
	if err != nil {
		t.Fatal(err)
	}
	secondPlans, _, err := buildPlans(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstPlans[0].DestinationRelative == secondPlans[0].DestinationRelative {
		t.Fatalf("different selected releases collided at %q", firstPlans[0].DestinationRelative)
	}
	if !strings.Contains(firstPlans[0].DestinationRelative, "MM-01234567") || !strings.Contains(secondPlans[0].DestinationRelative, "MM-89abcdef") {
		t.Fatalf("release identities missing from destinations: %q %q", firstPlans[0].DestinationRelative, secondPlans[0].DestinationRelative)
	}
}

func TestBuildPlansPublishesDanishCompanionBesideVideo(t *testing.T) {
	t.Parallel()
	year := uint16(2026)
	forced := true
	context := postgres.PublicationContext{
		Media:                 catalog.MediaItem{Kind: catalog.KindMovie, Title: "Movie", Year: &year, Identities: []catalog.ExternalIdentity{{Namespace: "tmdb", Value: "42", State: catalog.IdentityVerified}}},
		Root:                  postgres.LibraryRoot{DestinationRoot: "/library/movies", SourceVisibleRoot: "/altmount", PlexVisibleRoot: "/movies"},
		AltMount:              postgres.Integration{Config: json.RawMessage(`{"local_read_only_root":"/source"}`)},
		InventoryRootRelative: "queue/17",
		Assets: []postgres.PublicationAsset{{ID: uuid.NewV7(), RelativePath: "Movie.mkv", Companions: []postgres.PublicationCompanion{{
			AssetID: uuid.NewV7(), RelativePath: "Movie.da-DK.forced.srt", Language: "da", Forced: &forced,
		}}}},
	}
	plans, links, err := buildPlans(context)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || len(plans[0].Companions) != 1 || len(links) != 2 {
		t.Fatalf("unexpected publication plan: %+v links=%d", plans, len(links))
	}
	companion := plans[0].Companions[0]
	want := strings.TrimSuffix(plans[0].DestinationRelative, ".mkv") + ".da.forced.srt"
	if companion.DestinationRelative != want || companion.SourceRelative != filepath.Join("queue", "17", "Movie.da-DK.forced.srt") {
		t.Fatalf("unexpected managed companion: %+v want=%q", companion, want)
	}
}

// TestPlexTimeoutDetailNamesTheStallReason proves a Plex timeout records why
// confirmation stalled instead of one generic message.
func TestPlexTimeoutDetailNamesTheStallReason(t *testing.T) {
	for _, test := range []struct {
		err  error
		want string
	}{
		{nil, "not polled"},
		{plex.ErrNotConfirmed, "did not show every expected path"},
		{plex.ErrIdentityMismatch, "different provider identity"},
		{errConfirmedAfterDeadline, "only after the deadline"},
		{errors.New("Plex returned HTTP 500"), "requests failed"},
	} {
		if detail := plexTimeoutDetail(test.err); !strings.Contains(detail, test.want) {
			t.Fatalf("plexTimeoutDetail(%v)=%q, want it to mention %q", test.err, detail, test.want)
		}
	}
}
