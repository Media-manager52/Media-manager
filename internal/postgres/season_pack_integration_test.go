package postgres

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"uuid"

	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/mediainfo"
	"github.com/media-manager/media-manager/internal/packs"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/subtitles"
)

// TestSeasonPackHoldsExactlyOneActiveAcquisitionJob proves the single-job
// invariant at the durable layer: a complete season becomes one open
// acquisition round and one active AltMount job, and a second independent
// candidate (for example an episode the owner might otherwise submit on its
// own) cannot open a parallel job while the pack job is in flight.
func TestSeasonPackHoldsExactlyOneActiveAcquisitionJob(t *testing.T) {
	databaseURL := os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	store := subtitleIntegrationStore(t, ctx, databaseURL)
	defer store.Close()

	now := time.Now().UTC()
	year := uint16(2020)
	plex, err := store.CreateIntegration(ctx, Integration{Kind: "plex", Name: "Season Pack Plex", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://plex.invalid:32400","allow_http":true}`), SecretRef: "PLEX_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	altMount, err := store.CreateIntegration(ctx, Integration{Kind: "altmount", Name: "Season Pack AltMount", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://altmount.invalid:8080","allow_http":true,"local_read_only_root":"/mnt/altmount"}`)})
	if err != nil {
		t.Fatal(err)
	}
	indexer, err := store.CreateIntegration(ctx, Integration{Kind: "newznab", Name: "Season Pack Indexer", Config: json.RawMessage(`{"base_url":"https://indexer.invalid/api"}`), SecretRef: "INDEXER_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	root, err := store.CreateLibraryRoot(ctx, LibraryRoot{MediaKind: catalog.KindSeries, Name: "Season Pack TV", DestinationRoot: "/libraries/tv", PlexVisibleRoot: "/libraries/tv", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &plex.ID, PlexLibraryID: "2", Default: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := json.Marshal(map[string]any{"rules": []releases.Rule{{Name: "Resolution", Fact: "resolution", AllowedValues: []string{"1080p"}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold}}})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := store.CreateQualityProfile(ctx, QualityProfile{MediaKind: catalog.KindSeries, Name: "Season Pack Quality", PolicyVersion: 1, Policy: policy, UpgradesEnabled: true, Default: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	series, err := catalog.New(catalog.KindSeries, "Season Pack Single Job", &year, []catalog.ExternalIdentity{{Namespace: "tvdb", Value: "700001", State: catalog.IdentityVerified, Source: "test"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateManualMedia(ctx, series, root.ID, profile.ID, altMount.ID, []ManualSeason{{
		Number:           1,
		Monitored:        true,
		PreferSeasonPack: true,
		Episodes:         []ManualEpisode{{Number: 1}, {Number: 2}, {Number: 3}},
	}}, false, "test")
	if err != nil || len(created) != 1 {
		t.Fatalf("season workflows=%+v err=%v", created, err)
	}
	workflow := created[0]

	expectationID, seriesIdentity, season, expected, err := store.PackExpectation(ctx, workflow.ID)
	if err != nil || expectationID == (uuid.UUID{}) || seriesIdentity == "" || season != 1 || len(expected) != 3 {
		t.Fatalf("pack expectation id=%s identity=%q season=%d episodes=%d err=%v", expectationID, seriesIdentity, season, len(expected), err)
	}
	var expectations int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM pack_expectation_sets WHERE workflow_id=$1`, workflow.ID.String()).Scan(&expectations); err != nil || expectations != 1 {
		t.Fatalf("expectation sets=%d err=%v, want exactly one", expectations, err)
	}

	retrievalA := seedRetrievalCandidate(t, ctx, store, workflow.ID, indexer.ID, "season-pack-winner")
	intentA, err := store.BeginAltMountSubmission(ctx, retrievalA, testNZBPackageIdentity(40), now)
	if err != nil || !intentA.ShouldSubmit || intentA.JobID == (uuid.UUID{}) {
		t.Fatalf("first pack submission intent=%+v err=%v", intentA, err)
	}

	var jobs, attempted int
	if err := store.Pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1),
		(SELECT count(*) FROM workflow_attempted_releases WHERE workflow_id=$1)`, workflow.ID.String()).Scan(&jobs, &attempted); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 || attempted != 1 {
		t.Fatalf("first pack submission jobs=%d attempted=%d, want 1/1", jobs, attempted)
	}

	// A second independent candidate must not open a parallel job while the
	// pack job is active. This is the durable half of the "no per-episode
	// submission" invariant enforced by the real acceptance budget.
	candidateB := seedEligibleCandidate(t, ctx, store, workflow.ID, indexer.ID, "season-pack-episode-two", 500, now)
	if _, err := store.Pool.Exec(ctx, `UPDATE workflows SET state='candidate_selected',selected_release_id=$2,grab_attempt_id=NULL,revision=revision+1,updated_at=now() WHERE id=$1`, workflow.ID.String(), candidateB.String()); err != nil {
		t.Fatal(err)
	}
	retrievalB, err := store.LoadRetrievalContext(ctx, workflow.ID, candidateB)
	if err != nil {
		t.Fatal(err)
	}
	intentB, err := store.BeginAltMountSubmission(ctx, retrievalB, testNZBPackageIdentity(41), now)
	if err != nil {
		t.Fatal(err)
	}
	if intentB.ShouldSubmit {
		t.Fatal("a second independent candidate opened a parallel acquisition job for one season")
	}
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1`, workflow.ID.String()).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 {
		t.Fatalf("independent candidate fanned out to %d AltMount jobs, want 1", jobs)
	}
}

// TestSeasonPackPublicationMapsVerifiedEpisodes proves the completed pack is
// evaluated as a whole season and every verified content group carries the
// exact episode identities that publication renders into Plex-safe paths. One
// multi-episode file covers two expected episodes; a second file covers the
// remaining episode.
func TestSeasonPackPublicationMapsVerifiedEpisodes(t *testing.T) {
	databaseURL := os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	store := subtitleIntegrationStore(t, ctx, databaseURL)
	defer store.Close()

	now := time.Now().UTC()
	year := uint16(2020)
	plex, err := store.CreateIntegration(ctx, Integration{Kind: "plex", Name: "Season Map Plex", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://plex.invalid:32400","allow_http":true}`), SecretRef: "PLEX_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	altMount, err := store.CreateIntegration(ctx, Integration{Kind: "altmount", Name: "Season Map AltMount", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://altmount.invalid:8080","allow_http":true,"local_read_only_root":"/mnt/altmount"}`)})
	if err != nil {
		t.Fatal(err)
	}
	indexer, err := store.CreateIntegration(ctx, Integration{Kind: "newznab", Name: "Season Map Indexer", Config: json.RawMessage(`{"base_url":"https://indexer.invalid/api"}`), SecretRef: "INDEXER_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	root, err := store.CreateLibraryRoot(ctx, LibraryRoot{MediaKind: catalog.KindSeries, Name: "Season Map TV", DestinationRoot: "/libraries/tv", PlexVisibleRoot: "/libraries/tv", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &plex.ID, PlexLibraryID: "2", Default: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := json.Marshal(map[string]any{"rules": []releases.Rule{{Name: "Resolution", Fact: "resolution", AllowedValues: []string{"1080p"}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold}}})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := store.CreateQualityProfile(ctx, QualityProfile{MediaKind: catalog.KindSeries, Name: "Season Map Quality", PolicyVersion: 1, Policy: policy, UpgradesEnabled: true, Default: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	series, err := catalog.New(catalog.KindSeries, "Season Map Show", &year, []catalog.ExternalIdentity{{Namespace: "tvdb", Value: "700002", State: catalog.IdentityVerified, Source: "test"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateManualMedia(ctx, series, root.ID, profile.ID, altMount.ID, []ManualSeason{{
		Number:           1,
		Monitored:        true,
		PreferSeasonPack: true,
		Episodes:         []ManualEpisode{{Number: 1, ProviderID: "tvdb:2001", Title: "One"}, {Number: 2, ProviderID: "tvdb:2002", Title: "Two"}, {Number: 3, ProviderID: "tvdb:2003", Title: "Three"}},
	}}, false, "test")
	if err != nil || len(created) != 1 {
		t.Fatalf("season workflows=%+v err=%v", created, err)
	}
	workflow := created[0]

	job, inventoryID, assets := seedSubtitleAttempt(t, ctx, store, workflow.ID, altMount.ID, indexer.ID, 1, "season-map-pack")
	if len(assets) != 2 {
		t.Fatalf("seed assets=%d, want 2", len(assets))
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE altmount_output_assets SET relative_path=CASE id WHEN $2 THEN 'Season.Map.Show.S01E01-E02.mkv' ELSE 'Season.Map.Show.S01E03.mkv' END,media_kind='series' WHERE inventory_id=$1`, inventoryID.String(), assets[0].String()); err != nil {
		t.Fatal(err)
	}
	expectationID, seriesIdentity, season, expected, err := store.PackExpectation(ctx, workflow.ID)
	if err != nil || len(expected) != 3 {
		t.Fatalf("pack expectation episodes=%d err=%v", len(expected), err)
	}
	report := packs.Evaluate(seriesIdentity, season, expected, []packs.ObservedAsset{
		{ID: assets[0], RelativePath: "Season.Map.Show.S01E01-E02.mkv", Size: 1000},
		{ID: assets[1], RelativePath: "Season.Map.Show.S01E03.mkv", Size: 100},
	})
	if !report.Publishable() {
		t.Fatalf("season pack report=%+v", report)
	}
	if err := store.PersistPackEvaluation(ctx, expectationID, inventoryID, report, now); err != nil {
		t.Fatal(err)
	}
	evidence := mediainfo.Evidence{
		SchemaVersion:  1,
		ContainerNames: []string{"matroska"},
		VideoTracks:    []mediainfo.Track{{Index: 0, Codec: "hevc", Width: 1920, Height: 1080}},
		AudioTracks:    []mediainfo.Track{},
		SubtitleTracks: []mediainfo.Track{},
		Facts:          map[string]any{"resolution": "1080p"},
	}
	verification := subtitles.Verification{
		SchemaVersion: 1, Status: "verified", RequiredLanguages: []string{}, PreferredLanguages: []string{}, ObservedLanguages: []string{},
		Videos: []subtitles.VideoEvidence{
			{AssetID: assets[0], RelativePath: "Season.Map.Show.S01E01-E02.mkv", MediaInfo: evidence},
			{AssetID: assets[1], RelativePath: "Season.Map.Show.S01E03.mkv", MediaInfo: evidence},
		},
		Companions: []subtitles.Companion{}, FailureReasons: []string{},
	}
	if _, err := store.RecordSubtitleVerification(ctx, job, inventoryID, verification, now); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteImport(ctx, job, true, "season pack inventory and subtitle requirements verified", now); err != nil {
		t.Fatal(err)
	}

	publicationContext, err := store.LoadPublicationContext(ctx, workflow.ID, job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(publicationContext.Assets) != 2 {
		t.Fatalf("publication assets=%d, want 2", len(publicationContext.Assets))
	}
	episodesByPath := make(map[string][]uint16, 2)
	for _, asset := range publicationContext.Assets {
		for _, episode := range asset.Episodes {
			episodesByPath[asset.RelativePath] = append(episodesByPath[asset.RelativePath], episode.Key.Episode)
		}
	}
	for path, want := range map[string][]uint16{
		"Season.Map.Show.S01E01-E02.mkv": {1, 2},
		"Season.Map.Show.S01E03.mkv":     {3},
	} {
		got := episodesByPath[path]
		if len(got) != len(want) {
			t.Fatalf("asset %q episodes=%v, want %v", path, got, want)
		}
		for index := range want {
			if got[index] != want[index] {
				t.Fatalf("asset %q episodes=%v, want %v", path, got, want)
			}
		}
	}
}
