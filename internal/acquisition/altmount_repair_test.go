package acquisition

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/adapters/altmount"
	"github.com/media-manager/media-manager/internal/adapters/plex"
	"github.com/media-manager/media-manager/internal/adapters/seerr"
	"github.com/media-manager/media-manager/internal/catalog"
	metadataservice "github.com/media-manager/media-manager/internal/metadata"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
)

type fakeRepairSeerr struct{}

func (fakeRepairSeerr) Metadata(_ context.Context, mediaType string, tmdbID int64) (seerr.Metadata, error) {
	tvdb := int64(123)
	return seerr.Metadata{ID: tmdbID, Name: "Show", FirstAirDate: "2020-01-01", OriginalLanguage: "en", ExternalIDs: seerr.ExternalIDs{TVDBID: &tvdb}}, nil
}

func (fakeRepairSeerr) SeasonMetadata(_ context.Context, _ int64, season int16) (seerr.SeasonMetadata, error) {
	if season != 1 {
		return seerr.SeasonMetadata{}, errors.New("Seerr returned HTTP 500")
	}
	return seerr.SeasonMetadata{SeasonNumber: 1, Episodes: []seerr.EpisodeMetadata{
		{ID: 701, EpisodeNumber: 1, Name: "One", AirDate: "2020-01-01"},
		{ID: 702, EpisodeNumber: 2, Name: "Two", AirDate: "2020-01-08"},
		{ID: 703, EpisodeNumber: 3, Name: "Three", AirDate: "2020-01-15"},
	}}, nil
}

// TestAltMountRepairSweepRepairsUnmanagedEpisode proves the self-repair of
// older content: a corrupted AltMount file reached through a library symlink
// Media Manager did not publish, in a Plex library folder that is not a Media
// Manager root, is identified through Plex, the title is catalogued from
// TMDB, and an episode repair opens. An episode whose Plex and TMDB air dates
// disagree is not guessed at. After confirmation only the exact broken
// symlink is removed.
func TestAltMountRepairSweepRepairsUnmanagedEpisode(t *testing.T) {
	store := openMovieSearchTestStore(t)
	ctx := context.Background()
	base := t.TempDir()
	rootDir, oldDir := filepath.Join(base, "Serier"), filepath.Join(base, "TV-Old")
	episode2 := "Show (2020)/Season 01/Show - S01E02.mkv"
	episode3 := "Show (2020)/Season 01/Show - S01E03.mkv"
	brokenFile2 := "tv/Show.S01E02.1080p.WEB-GRP.[DRI-PROOF-AUDIO]/Show.S01E02.mkv"
	brokenFile3 := "tv/Show.S01E03.1080p.WEB-GRP/Show.S01E03.mkv"
	for relative, target := range map[string]string{episode2: "/mnt/altmount/" + brokenFile2, episode3: "/mnt/altmount/" + brokenFile3} {
		path := filepath.Join(oldDir, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		t.Fatal(err)
	}
	leaf := func(rating, index, aired, file string) string {
		return `<Video ratingKey="` + rating + `" grandparentRatingKey="99" parentIndex="1" index="` + index + `" type="episode" originallyAvailableAt="` + aired + `"><Media><Part file="` + file + `"/></Media></Video>`
	}
	routes := map[string]string{
		"/library/sections":                     `<MediaContainer><Directory key="57" type="show" title="Serier"><Location path="/media/Serier"/><Location path="/media/TV-Old"/></Directory></MediaContainer>`,
		"/library/sections/57/folder":           `<MediaContainer><Directory key="/library/sections/57/folder?parent=10" title="Show (2020)"/></MediaContainer>`,
		"/library/sections/57/folder?parent=10": `<MediaContainer><Directory key="/library/sections/57/folder?parent=11" title="Season 01"/></MediaContainer>`,
		"/library/sections/57/folder?parent=11": `<MediaContainer>` + leaf("102", "2", "2020-01-08", "/media/TV-Old/"+episode2) + leaf("103", "3", "2020-02-01", "/media/TV-Old/"+episode3) + `</MediaContainer>`,
		"/library/metadata/99":                  `<MediaContainer><Directory ratingKey="99" type="show"><Guid id="tmdb://5555"/><Guid id="tvdb://123"/></Directory></MediaContainer>`,
	}
	plexServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		key := request.URL.Path
		if request.URL.RawQuery != "" {
			key += "?" + request.URL.RawQuery
		}
		if body, ok := routes[key]; ok {
			_, _ = writer.Write([]byte(body))
			return
		}
		writer.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(plexServer.Close)

	plexIntegration, err := store.CreateIntegration(ctx, postgres.Integration{Kind: "plex", Name: "Repair Plex", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://plex.invalid:32400","allow_http":true}`), SecretRef: "PLEX_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	altMount, err := store.CreateIntegration(ctx, postgres.Integration{Kind: "altmount", Name: "Repair AltMount", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://altmount.invalid:8080","allow_http":true,"category":"media-manager","local_read_only_root":"/mnt/altmount"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateIntegration(ctx, postgres.Integration{Kind: "newznab", Name: "Repair indexer", Enabled: true, Config: json.RawMessage(`{"base_url":"https://indexer.invalid/api"}`), SecretRef: "INDEXER_KEY"}); err != nil {
		t.Fatal(err)
	}
	root, err := store.CreateLibraryRoot(ctx, postgres.LibraryRoot{MediaKind: catalog.KindSeries, Name: "Serier", DestinationRoot: rootDir, PlexVisibleRoot: "/media/Serier", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &plexIntegration.ID, PlexLibraryID: "57", Default: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	policy, _ := json.Marshal(map[string]any{"rules": []releases.Rule{{Name: "Resolution", Fact: "resolution", AllowedValues: []string{"1080p"}, Kind: releases.RulePreferred, Weight: 1}}, "required_subtitle_languages": []string{"da"}})
	subs, err := store.CreateQualityProfile(ctx, postgres.QualityProfile{MediaKind: catalog.KindSeries, Name: "TV Danish Subs", PolicyVersion: 1, Policy: policy, UpgradesEnabled: true, Default: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	service := &AltMountRepairService{
		Store: store, Seerr: fakeRepairSeerr{}, Metadata: &metadataservice.Service{},
		ListCorrupted: func(context.Context, postgres.Integration) ([]altmount.HealthItem, error) {
			return []altmount.HealthItem{{ID: 9, FilePath: brokenFile2, Status: "corrupted"}, {ID: 10, FilePath: brokenFile3, Status: "corrupted"}}, nil
		},
		PlexClientFor: func(postgres.Integration) (*plex.Client, error) {
			return plex.New(plex.Config{BaseURL: plexServer.URL, Token: "secret", AllowHTTP: true}, plexServer.Client().Transport)
		},
	}
	event := postgres.OutboxEvent{Payload: json.RawMessage(`{"integration_id":"` + altMount.ID.String() + `"}`)}
	if err := service.HandleSweep(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleSweep(ctx, event); err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	repairs, err := store.ListAltMountRepairs(ctx, 10)
	if err != nil || len(repairs) != 2 {
		t.Fatalf("repairs=%+v err=%v, want one open episode repair and one attention item", repairs, err)
	}
	var open, attention postgres.AltMountRepair
	for _, repair := range repairs {
		switch repair.State {
		case "open":
			open = repair
		case "needs_attention":
			attention = repair
		}
	}
	if open.Variant != "season:01:episode:02" || open.BrokenReleaseTitle != "Show.S01E02.1080p.WEB-GRP" || open.RepairWorkflowID == nil || open.MediaID == nil {
		t.Fatalf("open repair=%+v", open)
	}
	if attention.Variant != "season:01:episode:03" || attention.RepairWorkflowID != nil {
		t.Fatalf("mismatched air date must need attention, got %+v", attention)
	}
	media, err := store.GetMedia(ctx, *open.MediaID)
	if err != nil || media.Title != "Show" || media.Kind != catalog.KindSeries {
		t.Fatalf("catalogued media=%+v err=%v", media, err)
	}
	workflow, err := store.GetWorkflow(ctx, *open.RepairWorkflowID)
	if err != nil || workflow.QualityProfileID != subs.ID || workflow.RootID != root.ID {
		t.Fatalf("repair workflow=%+v err=%v, want the root and the default (Danish subtitles) profile", workflow, err)
	}
	expectation, _, season, expected, err := store.PackExpectation(ctx, *open.RepairWorkflowID)
	if err != nil || expectation == (uuid.UUID{}) || season != 1 || len(expected) != 1 || expected[0].Key.Episode != 2 {
		t.Fatalf("episode expectation season=%d expected=%+v err=%v", season, expected, err)
	}

	// After Plex confirmed the replacement, the worker removes only the broken
	// symlink that still points at the corrupted file.
	if err := service.HandleUnmanagedRemoval(ctx, postgres.OutboxEvent{AggregateID: open.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(oldDir, episode2)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("broken symlink still present: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(oldDir, episode3)); err != nil {
		t.Fatalf("an unrelated symlink was touched: %v", err)
	}
	repairs, _ = store.ListAltMountRepairs(ctx, 10)
	for _, repair := range repairs {
		if repair.ID == open.ID && repair.State != "repaired" {
			t.Fatalf("repair state=%q, want repaired", repair.State)
		}
	}
}

func TestRootPathPairsTranslatePlexFolders(t *testing.T) {
	pairs := rootPathPairs([]postgres.LibraryRoot{
		{PlexVisibleRoot: "/media/Serier", DestinationRoot: "/srv/media/Serier"},
		{PlexVisibleRoot: "/mnt/media-manager/libraries/movies", DestinationRoot: "/libraries/movies"},
	})
	for plexPath, want := range map[string]string{
		"/media/Movies":                       "/srv/media/Movies",
		"/mnt/media-manager/libraries/movies": "/libraries/movies",
	} {
		if got, ok := translatePlexPath(pairs, plexPath); !ok || got != want {
			t.Fatalf("translate %q = %q %v, want %q", plexPath, got, ok, want)
		}
	}
	if got := releaseTitleFromPath("tv/Avatar.S02.2160p.NF.WEB-DL-KRATOS.[DRI-PROOF-SUBS]/Avatar.S02E05.mkv"); got != "Avatar.S02.2160p.NF.WEB-DL-KRATOS" {
		t.Fatalf("release title=%q", got)
	}
	_ = time.Now
}
