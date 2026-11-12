package publication

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/adapters/plex"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/packs"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/workflows"
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

func TestBuildPlansPublishesOneOfTwoSubtitlesWithTheSamePlexName(t *testing.T) {
	t.Parallel()
	year := uint16(2026)
	context := postgres.PublicationContext{
		Media:                 catalog.MediaItem{Kind: catalog.KindMovie, Title: "Movie", Year: &year, Identities: []catalog.ExternalIdentity{{Namespace: "tmdb", Value: "42", State: catalog.IdentityVerified}}},
		Root:                  postgres.LibraryRoot{DestinationRoot: "/library/movies", SourceVisibleRoot: "/altmount", PlexVisibleRoot: "/movies"},
		AltMount:              postgres.Integration{Config: json.RawMessage(`{"local_read_only_root":"/source"}`)},
		InventoryRootRelative: "queue/17",
		Assets: []postgres.PublicationAsset{{ID: uuid.NewV7(), RelativePath: "Movie.mkv", Companions: []postgres.PublicationCompanion{
			{AssetID: uuid.NewV7(), RelativePath: "Movie.da.srt", Language: "da"},
			{AssetID: uuid.NewV7(), RelativePath: "Movie.dan.srt", Language: "da"},
		}}},
	}
	plans, links, err := buildPlans(context)
	if err != nil {
		t.Fatalf("two Danish subtitles failed the publication: %v", err)
	}
	if len(plans) != 1 || len(plans[0].Companions) != 1 || len(links) != 2 || plans[0].Companions[0].SourceRelative != "queue/17/Movie.da.srt" {
		t.Fatalf("plans=%+v links=%d", plans, len(links))
	}
}

func TestPublicationResumesOnlyItsOwnLaterRevision(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		event    uint64
		workflow workflows.Workflow
		want     bool
	}{
		{event: 7, workflow: workflows.Workflow{Revision: 9, State: workflows.StatePublishing}, want: true},
		{event: 7, workflow: workflows.Workflow{Revision: 8, State: workflows.StateVerifying}, want: true},
		{event: 7, workflow: workflows.Workflow{Revision: 9, State: workflows.StateNeedsAttention}, want: false},
		{event: 7, workflow: workflows.Workflow{Revision: 9, State: workflows.StateCancelled}, want: false},
		{event: 9, workflow: workflows.Workflow{Revision: 7, State: workflows.StatePublishing}, want: false},
	} {
		if got := resumablePublication(testCase.event, testCase.workflow); got != testCase.want {
			t.Errorf("resumablePublication(%d, %+v) = %v, want %v", testCase.event, testCase.workflow, got, testCase.want)
		}
	}
}

func TestUnreadableStorageDefersPublicationInsteadOfFailingIt(t *testing.T) {
	t.Parallel()
	readable := t.TempDir()
	library := t.TempDir() // an empty library folder is fine
	if err := os.WriteFile(filepath.Join(readable, "queue"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	at := func(source, destination string) postgres.PublicationContext {
		return postgres.PublicationContext{
			AltMount: postgres.Integration{Config: json.RawMessage(`{"local_read_only_root":"` + source + `"}`)},
			Root:     postgres.LibraryRoot{DestinationRoot: destination},
		}
	}
	cause := errors.New("symlink: transport endpoint is not connected")
	outage := storageOutage(at(filepath.Join(readable, "missing-mount"), library), cause)
	var deferred interface{ RetryAt() time.Time }
	if !errors.As(outage, &deferred) || !deferred.RetryAt().After(time.Now()) || !errors.Is(outage, cause) {
		t.Fatalf("unreadable AltMount mount: %v, want a deferral wrapping the cause", outage)
	}
	if outage := storageOutage(at(readable, filepath.Join(library, "missing-library")), cause); !errors.As(outage, &deferred) {
		t.Fatalf("unreadable library folder: %v, want a deferral", outage)
	}
	if outage := storageOutage(at(readable, library), cause); outage != nil {
		t.Fatalf("readable storage deferred the failure: %v", outage)
	}
}

// Plex confirmation may use only verified supported identities, never unverified aliases.
func TestPlexConfirmationUsesOnlyVerifiedSupportedIdentities(t *testing.T) {
	item := catalog.MediaItem{Identities: []catalog.ExternalIdentity{
		{Namespace: "tmdb", Value: "880001", State: catalog.IdentityUnverified},
		{Namespace: " TMDB ", Value: " 880002 ", State: catalog.IdentityVerified},
		{Namespace: "tmdb", Value: "880002", State: catalog.IdentityVerified},
		{Namespace: "imdb", Value: "tt8800001", State: catalog.IdentityVerified},
		{Namespace: "tvdb", Value: "770001", State: catalog.IdentityVerified},
		{Namespace: "simkl", Value: "660001", State: catalog.IdentityVerified},
		{Namespace: "tmdb", Value: " ", State: catalog.IdentityVerified},
	}}
	guids := plexGUIDs(item)
	if got := strings.Join(guids, ","); got != "tmdb://880002,imdb://tt8800001,tvdb://770001" {
		t.Fatalf("Plex confirmation identities = %q", got)
	}
	item.Identities = []catalog.ExternalIdentity{{Namespace: "tmdb", Value: "880001", State: catalog.IdentityUnverified}}
	if guids := plexGUIDs(item); len(guids) != 0 {
		t.Fatalf("unverified identity could confirm publication: %v", guids)
	}
}
