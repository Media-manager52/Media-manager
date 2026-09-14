package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"uuid"

	plexadapter "github.com/media-manager/media-manager/internal/adapters/plex"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/library"
	"github.com/media-manager/media-manager/internal/mediainfo"
	"github.com/media-manager/media-manager/internal/packs"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/subtitles"
)

// plexPublicationFixture is the shared substrate for the V1-4 Plex publication
// tests. It uses real temporary source/destination/library directories so the
// managed symlink machinery runs for real; it never contacts Plex, an indexer,
// or AltMount.
type plexPublicationFixture struct {
	store             *Store
	ctx               context.Context
	plex              Integration
	altMount          Integration
	indexer           Integration
	root              LibraryRoot
	profile           QualityProfile
	localRoot         string
	destinationRoot   string
	plexVisibleRoot   string
	sourceVisibleRoot string
}

func newPlexPublicationFixture(t *testing.T, kind catalog.MediaKind) *plexPublicationFixture {
	t.Helper()
	databaseURL := os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	store := subtitleIntegrationStore(t, ctx, databaseURL)
	t.Cleanup(store.Close)

	localRoot := t.TempDir()
	destinationRoot := t.TempDir()
	plexVisibleRoot := t.TempDir()
	sourceVisibleRoot := t.TempDir()

	plex, err := store.CreateIntegration(ctx, Integration{Kind: "plex", Name: "Publication Plex", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://plex.invalid:32400","allow_http":true}`), SecretRef: "PLEX_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	altMountConfig, err := json.Marshal(map[string]any{"base_url": "http://altmount.invalid:8080", "allow_http": true, "local_read_only_root": localRoot})
	if err != nil {
		t.Fatal(err)
	}
	altMount, err := store.CreateIntegration(ctx, Integration{Kind: "altmount", Name: "Publication AltMount", Enabled: true, Default: true, Config: altMountConfig})
	if err != nil {
		t.Fatal(err)
	}
	indexer, err := store.CreateIntegration(ctx, Integration{Kind: "newznab", Name: "Publication Indexer", Config: json.RawMessage(`{"base_url":"https://indexer.invalid/api"}`), SecretRef: "INDEXER_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	name, libraryID := "Publication Movies", "1"
	if kind == catalog.KindSeries {
		name, libraryID = "Publication TV", "2"
	}
	root, err := store.CreateLibraryRoot(ctx, LibraryRoot{MediaKind: kind, Name: name, DestinationRoot: destinationRoot, PlexVisibleRoot: plexVisibleRoot, SourceVisibleRoot: sourceVisibleRoot, PlexInstanceID: &plex.ID, PlexLibraryID: libraryID, Default: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := json.Marshal(map[string]any{"rules": []releases.Rule{{Name: "Resolution", Fact: "resolution", AllowedValues: []string{"1080p"}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold}}})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := store.CreateQualityProfile(ctx, QualityProfile{MediaKind: kind, Name: "Publication quality", PolicyVersion: 1, Policy: policy, UpgradesEnabled: true, Default: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return &plexPublicationFixture{store: store, ctx: ctx, plex: plex, altMount: altMount, indexer: indexer, root: root, profile: profile, localRoot: localRoot, destinationRoot: destinationRoot, plexVisibleRoot: plexVisibleRoot, sourceVisibleRoot: sourceVisibleRoot}
}

func (fixture *plexPublicationFixture) movieWorkflow(t *testing.T, title, tmdb string) (catalog.MediaItem, uuid.UUID) {
	t.Helper()
	year := uint16(2026)
	item, err := catalog.New(catalog.KindMovie, title, &year, []catalog.ExternalIdentity{{Namespace: "tmdb", Value: tmdb, State: catalog.IdentityVerified, Source: "test"}}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	created, err := fixture.store.CreateManualMedia(fixture.ctx, item, fixture.root.ID, fixture.profile.ID, fixture.altMount.ID, nil, false, "test")
	if err != nil || len(created) != 1 {
		t.Fatalf("movie workflow=%+v err=%v", created, err)
	}
	return item, created[0].ID
}

func (fixture *plexPublicationFixture) seriesWorkflow(t *testing.T, title, tvdb string, episodes []ManualEpisode) (catalog.MediaItem, uuid.UUID) {
	t.Helper()
	year := uint16(2020)
	item, err := catalog.New(catalog.KindSeries, title, &year, []catalog.ExternalIdentity{{Namespace: "tvdb", Value: tvdb, State: catalog.IdentityVerified, Source: "test"}}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	created, err := fixture.store.CreateManualMedia(fixture.ctx, item, fixture.root.ID, fixture.profile.ID, fixture.altMount.ID, []ManualSeason{{Number: 1, Monitored: true, PreferSeasonPack: true, SearchImmediately: false, Episodes: episodes}}, false, "test")
	if err != nil || len(created) != 1 {
		t.Fatalf("series workflow=%+v err=%v", created, err)
	}
	return item, created[0].ID
}

// seedVerifiedMovieOutput drives a movie attempt to a verified, publishable
// import without contacting any provider and returns the selected job.
func (fixture *plexPublicationFixture) seedVerifiedMovieOutput(t *testing.T, workflowID uuid.UUID, generation int, nativeKey string) (AltMountJobContext, uuid.UUID, uuid.UUID) {
	t.Helper()
	job, inventoryID, assets := seedSubtitleAttempt(t, fixture.ctx, fixture.store, workflowID, fixture.altMount.ID, fixture.indexer.ID, generation, nativeKey)
	evidence := mediainfo.Evidence{SchemaVersion: 1, ContainerNames: []string{"matroska"}, VideoTracks: []mediainfo.Track{{Index: 0, Codec: "hevc", Width: 1920, Height: 1080}}, AudioTracks: []mediainfo.Track{}, SubtitleTracks: []mediainfo.Track{}, Facts: map[string]any{"resolution": "1080p"}}
	verification := subtitles.Verification{
		SchemaVersion: 1, Status: "verified", RequiredLanguages: []string{}, PreferredLanguages: []string{}, ObservedLanguages: []string{},
		Videos:     []subtitles.VideoEvidence{{AssetID: assets[0], RelativePath: "Movie.mkv", MediaInfo: evidence}},
		Companions: []subtitles.Companion{}, FailureReasons: []string{},
	}
	if _, err := fixture.store.RecordSubtitleVerification(fixture.ctx, job, inventoryID, verification, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.CompleteImport(fixture.ctx, job, true, "movie output verified for publication", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return job, inventoryID, assets[0]
}

// seedVerifiedPackOutput drives a two-asset season-pack attempt to a verified,
// publishable import. Each asset carries the exact contiguous episode coverage
// the pack evaluator verified.
func (fixture *plexPublicationFixture) seedVerifiedPackOutput(t *testing.T, workflowID uuid.UUID, generation int, nativeKey string) (AltMountJobContext, uuid.UUID, []uuid.UUID) {
	t.Helper()
	job, inventoryID, assets := seedSubtitleAttempt(t, fixture.ctx, fixture.store, workflowID, fixture.altMount.ID, fixture.indexer.ID, generation, nativeKey)
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `UPDATE altmount_output_assets SET relative_path=CASE id WHEN $2 THEN 'Show.S01E01-E02.mkv' ELSE 'Show.S01E03.mkv' END,media_kind='series' WHERE inventory_id=$1`, inventoryID.String(), assets[0].String()); err != nil {
		t.Fatal(err)
	}
	expectationID, seriesIdentity, season, expected, err := fixture.store.PackExpectation(fixture.ctx, workflowID)
	if err != nil || len(expected) != 3 {
		t.Fatalf("pack expectation episodes=%d err=%v", len(expected), err)
	}
	report := packs.Evaluate(seriesIdentity, season, expected, []packs.ObservedAsset{{ID: assets[0], RelativePath: "Show.S01E01-E02.mkv", Size: 2000}, {ID: assets[1], RelativePath: "Show.S01E03.mkv", Size: 1000}})
	if !report.Publishable() {
		t.Fatalf("season pack report=%+v", report)
	}
	if err := fixture.store.PersistPackEvaluation(fixture.ctx, expectationID, inventoryID, report, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	evidence := mediainfo.Evidence{SchemaVersion: 1, ContainerNames: []string{"matroska"}, VideoTracks: []mediainfo.Track{{Index: 0, Codec: "hevc", Width: 1920, Height: 1080}}, AudioTracks: []mediainfo.Track{}, SubtitleTracks: []mediainfo.Track{}, Facts: map[string]any{"resolution": "1080p"}}
	verification := subtitles.Verification{
		SchemaVersion: 1, Status: "verified", RequiredLanguages: []string{}, PreferredLanguages: []string{}, ObservedLanguages: []string{},
		Videos: []subtitles.VideoEvidence{
			{AssetID: assets[0], RelativePath: "Show.S01E01-E02.mkv", MediaInfo: evidence},
			{AssetID: assets[1], RelativePath: "Show.S01E03.mkv", MediaInfo: evidence},
		},
		Companions: []subtitles.Companion{}, FailureReasons: []string{},
	}
	if _, err := fixture.store.RecordSubtitleVerification(fixture.ctx, job, inventoryID, verification, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.CompleteImport(fixture.ctx, job, true, "season pack verified for publication", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return job, inventoryID, assets
}

// publishMoviePlans drives the durable store publication lifecycle. It mirrors
// the production service order: validate, persist the plan, mark publishing,
// create the contained symlinks, then request the Plex scan.
func (fixture *plexPublicationFixture) publishMoviePlans(t *testing.T, workflowID, jobID uuid.UUID, publicationContext PublicationContext) (uuid.UUID, PublicationVersion, library.LinkPlan, string) {
	t.Helper()
	asset := publicationContext.Assets[0]
	provider, providerID := "tmdb", ""
	for _, identity := range publicationContext.Media.Identities {
		if identity.State == catalog.IdentityVerified && strings.EqualFold(identity.Namespace, provider) {
			providerID = strings.TrimSpace(identity.Value)
		}
	}
	technical := append([]string(nil), publicationContext.Technical...)
	technical = append(technical, publicationContext.ReleaseToken)
	folder, file, err := library.RenderMovie(library.MovieName{Title: publicationContext.Media.Title, Year: *publicationContext.Media.Year, Provider: provider, ProviderID: providerID, Technical: technical, Extension: strings.TrimPrefix(strings.ToLower(filepath.Ext(asset.RelativePath)), ".")})
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(folder, file)
	sourceRoot := filepath.Join(fixture.localRoot, publicationContext.InventoryRootRelative)
	plexSourceRoot := filepath.Join(fixture.sourceVisibleRoot, publicationContext.InventoryRootRelative)
	link := library.LinkPlan{SourceRoot: sourceRoot, SourceRelative: asset.RelativePath, DestinationRoot: fixture.destinationRoot, DestinationRel: destination, PlexVisibleSourceRoot: plexSourceRoot}
	validated, err := library.ValidatePlan(link)
	if err != nil {
		t.Fatal(err)
	}
	plan := PublicationPlan{AssetID: asset.ID, DestinationRelative: destination, SourceRelative: filepath.Join(publicationContext.InventoryRootRelative, asset.RelativePath), PlexVisibleTarget: validated.PlexVisibleTarget}
	versions, err := fixture.store.PreparePublication(fixture.ctx, workflowID, jobID, []PublicationPlan{plan}, time.Now().UTC())
	if err != nil || len(versions) != 1 {
		t.Fatalf("prepare movie publication=%+v err=%v", versions, err)
	}
	if err := fixture.store.MarkPublicationPublishing(fixture.ctx, workflowID, jobID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := library.PublishBatch([]library.LinkPlan{validated}); err != nil {
		t.Fatal(err)
	}
	scanID, err := fixture.store.CompletePublication(fixture.ctx, workflowID, jobID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return scanID, versions[0], validated, destination
}

// TestMoviePlexPublicationSymlinkRecognitionAndUpgrade proves a verified movie
// publishes through a managed symlink inside the allowed library root with
// deterministic Plex naming, is recognised only by exact identity and path, and
// keeps the existing current media valid until the replacement is confirmed.
func TestMoviePlexPublicationSymlinkRecognitionAndUpgrade(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindMovie)
	item, workflowID := fixture.movieWorkflow(t, "Publication Movie", "550001")
	job, inventoryID, videoAsset := fixture.seedVerifiedMovieOutput(t, workflowID, 1, "publication-movie")
	var inspectionCount int
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT count(*) FROM subtitle_verifications WHERE altmount_job_id=$1 AND inventory_id=$2 AND status='verified'`, job.JobID.String(), inventoryID.String()).Scan(&inspectionCount); err != nil || inspectionCount != 1 {
		t.Fatalf("verified output inspection count=%d err=%v", inspectionCount, err)
	}
	// The real AltMount-completed source file must exist before publication.
	sourceFile := filepath.Join(fixture.localRoot, "completed", "publication-movie", "Movie.mkv")
	if err := os.MkdirAll(filepath.Dir(sourceFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourceFile, []byte("read-only altmount output"), 0o444); err != nil {
		t.Fatal(err)
	}

	publicationContext, err := fixture.store.LoadPublicationContext(fixture.ctx, workflowID, job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if publicationContext.InventoryRootRelative != filepath.Join("completed", "publication-movie") || len(publicationContext.Assets) != 1 || publicationContext.Assets[0].ID != videoAsset {
		t.Fatalf("publication context=%+v", publicationContext)
	}

	scanID, version, link, destination := fixture.publishMoviePlans(t, workflowID, job.JobID, publicationContext)
	if !strings.Contains(destination, "{tmdb-550001}") {
		t.Fatalf("movie destination lost its Plex identity: %q", destination)
	}
	if !strings.HasPrefix(link.PlexVisibleTarget, fixture.sourceVisibleRoot) {
		t.Fatalf("symlink target escaped the Plex-visible source root: %q", link.PlexVisibleTarget)
	}
	published := filepath.Join(fixture.destinationRoot, destination)
	target, err := os.Readlink(published)
	if err != nil || target != link.PlexVisibleTarget {
		t.Fatalf("managed symlink target=%q err=%v, want %q", target, err, link.PlexVisibleTarget)
	}
	relative, err := filepath.Rel(fixture.destinationRoot, published)
	if err != nil || strings.HasPrefix(relative, "..") {
		t.Fatalf("publication escaped the library root: %q", relative)
	}

	scan, err := fixture.store.LoadPlexScan(fixture.ctx, scanID)
	if err != nil || len(scan.Versions) != 1 {
		t.Fatalf("Plex scan=%+v err=%v", scan, err)
	}
	wantPlexPath := filepath.Join(fixture.plexVisibleRoot, destination)
	if scan.Versions[0].PlexVisiblePath != wantPlexPath {
		t.Fatalf("Plex path=%q, want %q", scan.Versions[0].PlexVisiblePath, wantPlexPath)
	}
	if err := fixture.store.MarkPlexRefreshRequested(fixture.ctx, scan, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	scan, err = fixture.store.LoadPlexScan(fixture.ctx, scanID)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.ConfirmPlexScan(fixture.ctx, scan, []plexadapter.Confirmation{{RatingKey: "movie-1", Path: wantPlexPath, ProviderGUID: "tmdb://550001"}}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	assertPublicationState(t, fixture, version.ID, "current", "plex_confirmed")

	// A replacement selects a different release, so it must receive a distinct
	// filename and let the old and new links coexist through verification.
	upgrade, err := fixture.store.CreateManualWorkflowForExisting(fixture.ctx, item, fixture.root.ID, fixture.profile.ID, fixture.altMount.ID, nil, false, "", "test", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	upgradeJob, _, _ := fixture.seedVerifiedMovieOutput(t, upgrade.ID, 2, "publication-movie-upgrade")
	upgradeSource := filepath.Join(fixture.localRoot, "completed", "publication-movie-upgrade", "Movie.mkv")
	if err := os.MkdirAll(filepath.Dir(upgradeSource), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(upgradeSource, []byte("read-only altmount replacement"), 0o444); err != nil {
		t.Fatal(err)
	}
	upgradeContext, err := fixture.store.LoadPublicationContext(fixture.ctx, upgrade.ID, upgradeJob.JobID)
	if err != nil {
		t.Fatal(err)
	}
	upgradeScanID, upgradeVersion, _, upgradeDestination := fixture.publishMoviePlans(t, upgrade.ID, upgradeJob.JobID, upgradeContext)
	if upgradeDestination == destination {
		t.Fatalf("replacement reused the current filename %q", upgradeDestination)
	}
	// The old media remains current until Plex confirms the replacement.
	assertPublicationState(t, fixture, version.ID, "current", "plex_confirmed")
	upgradeScan, err := fixture.store.LoadPlexScan(fixture.ctx, upgradeScanID)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.MarkPlexRefreshRequested(fixture.ctx, upgradeScan, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	upgradeScan, err = fixture.store.LoadPlexScan(fixture.ctx, upgradeScanID)
	if err != nil {
		t.Fatal(err)
	}
	upgradePlexPath := filepath.Join(fixture.plexVisibleRoot, upgradeDestination)
	if err := fixture.store.ConfirmPlexScan(fixture.ctx, upgradeScan, []plexadapter.Confirmation{{RatingKey: "movie-2", Path: upgradePlexPath, ProviderGUID: "tmdb://550001"}}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	assertPublicationState(t, fixture, upgradeVersion.ID, "current", "plex_confirmed")
	assertPublicationState(t, fixture, version.ID, "displaced", "plex_confirmed")

	// The displaced link is removed only after Plex confirmed the replacement,
	// and the read-only AltMount source is retained.
	displaced, err := fixture.store.LoadDisplacedLink(fixture.ctx, version.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := library.RemoveSymlink(displaced.DestinationRoot, displaced.DestinationRelative, displaced.ExpectedTarget); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.RecordDisplacedLinkRemoved(fixture.ctx, displaced, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(published); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("displaced symlink still present: %v", err)
	}
	if _, err := os.Stat(sourceFile); err != nil {
		t.Fatalf("read-only AltMount source was not retained: %v", err)
	}
	if _, err := os.Readlink(filepath.Join(fixture.destinationRoot, upgradeDestination)); err != nil {
		t.Fatalf("replacement symlink missing after displacement: %v", err)
	}
}

// TestSeasonPackPlexPublicationIdentifiesEveryEpisodePath proves a verified
// season pack publishes every exact episode path through managed symlinks and
// confirms the whole pack with its series identity.
func TestSeasonPackPlexPublicationIdentifiesEveryEpisodePath(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindSeries)
	_, workflowID := fixture.seriesWorkflow(t, "Publication Show", "660001", []ManualEpisode{{Number: 1, ProviderID: "tvdb:1", Title: "One"}, {Number: 2, ProviderID: "tvdb:2", Title: "Two"}, {Number: 3, ProviderID: "tvdb:3", Title: "Three"}})
	job, _, _ := fixture.seedVerifiedPackOutput(t, workflowID, 1, "publication-pack")
	for _, relative := range []string{"Show.S01E01-E02.mkv", "Show.S01E03.mkv"} {
		source := filepath.Join(fixture.localRoot, "completed", "publication-pack", relative)
		if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(source, []byte(relative), 0o444); err != nil {
			t.Fatal(err)
		}
	}
	publicationContext, err := fixture.store.LoadPublicationContext(fixture.ctx, workflowID, job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(publicationContext.Assets) != 2 {
		t.Fatalf("pack publication assets=%d, want 2", len(publicationContext.Assets))
	}
	providerID := "660001"
	technical := append([]string(nil), publicationContext.Technical...)
	technical = append(technical, publicationContext.ReleaseToken)
	plans := make([]PublicationPlan, 0, len(publicationContext.Assets))
	links := make([]library.LinkPlan, 0, len(publicationContext.Assets))
	destinations := make([]string, 0, len(publicationContext.Assets))
	for _, asset := range publicationContext.Assets {
		if len(asset.Episodes) == 0 {
			t.Fatalf("pack asset %q has no episodes", asset.RelativePath)
		}
		season := asset.Episodes[0].Key.Season
		episodes := make([]uint16, 0, len(asset.Episodes))
		title := ""
		if len(asset.Episodes) == 1 {
			title = asset.Episodes[0].Title
		}
		for _, episode := range asset.Episodes {
			episodes = append(episodes, episode.Key.Episode)
		}
		extension := strings.TrimPrefix(strings.ToLower(filepath.Ext(asset.RelativePath)), ".")
		folder, seasonFolder, file, err := library.RenderEpisodeSet(publicationContext.Media.Title, *publicationContext.Media.Year, "tvdb", providerID, season, episodes, title, technical, extension)
		if err != nil {
			t.Fatal(err)
		}
		destination := filepath.Join(folder, seasonFolder, file)
		sourceRoot := filepath.Join(fixture.localRoot, publicationContext.InventoryRootRelative)
		plexSourceRoot := filepath.Join(fixture.sourceVisibleRoot, publicationContext.InventoryRootRelative)
		link := library.LinkPlan{SourceRoot: sourceRoot, SourceRelative: asset.RelativePath, DestinationRoot: fixture.destinationRoot, DestinationRel: destination, PlexVisibleSourceRoot: plexSourceRoot}
		validated, err := library.ValidatePlan(link)
		if err != nil {
			t.Fatal(err)
		}
		plans = append(plans, PublicationPlan{AssetID: asset.ID, DestinationRelative: destination, SourceRelative: filepath.Join(publicationContext.InventoryRootRelative, asset.RelativePath), PlexVisibleTarget: validated.PlexVisibleTarget})
		links = append(links, validated)
		destinations = append(destinations, destination)
	}
	if _, err := fixture.store.PreparePublication(fixture.ctx, workflowID, job.JobID, plans, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.MarkPublicationPublishing(fixture.ctx, workflowID, job.JobID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := library.PublishBatch(links); err != nil {
		t.Fatal(err)
	}
	scanID, err := fixture.store.CompletePublication(fixture.ctx, workflowID, job.JobID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	for index, link := range links {
		published := filepath.Join(fixture.destinationRoot, destinations[index])
		target, err := os.Readlink(published)
		if err != nil || target != link.PlexVisibleTarget {
			t.Fatalf("pack symlink %d target=%q err=%v, want %q", index, target, err, link.PlexVisibleTarget)
		}
		if _, err := filepath.Rel(fixture.destinationRoot, published); err != nil {
			t.Fatal(err)
		}
	}
	scan, err := fixture.store.LoadPlexScan(fixture.ctx, scanID)
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Versions) != 2 {
		t.Fatalf("Plex scan versions=%d, want 2", len(scan.Versions))
	}
	expectedPaths := make(map[string]bool, len(destinations))
	for _, destination := range destinations {
		expectedPaths[filepath.Join(fixture.plexVisibleRoot, destination)] = true
	}
	confirmations := make([]plexadapter.Confirmation, 0, len(scan.Versions))
	for index, version := range scan.Versions {
		if !expectedPaths[version.PlexVisiblePath] {
			t.Fatalf("pack Plex path %q was not one of the planned destinations", version.PlexVisiblePath)
		}
		if len(version.Episodes) == 0 {
			t.Fatalf("pack scan version %q has no episode expectations", version.PlexVisiblePath)
		}
		confirmations = append(confirmations, plexadapter.Confirmation{RatingKey: "pack-" + string(rune('a'+index)), Path: version.PlexVisiblePath, ProviderGUID: "tvdb://660001"})
	}
	if err := fixture.store.MarkPlexRefreshRequested(fixture.ctx, scan, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	scan, err = fixture.store.LoadPlexScan(fixture.ctx, scanID)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.ConfirmPlexScan(fixture.ctx, scan, confirmations, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var current int
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT count(*) FROM library_link_versions WHERE workflow_id=$1 AND state='current'`, workflowID.String()).Scan(&current); err != nil || current != 2 {
		t.Fatalf("confirmed pack current versions=%d err=%v, want 2", current, err)
	}
	var workflowState string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT state FROM workflows WHERE id=$1`, workflowID.String()).Scan(&workflowState); err != nil || workflowState != "plex_confirmed" {
		t.Fatalf("pack workflow state=%q err=%v", workflowState, err)
	}
}

// TestPlexPublicationRefusesUnverifiedOutput proves nothing is published when
// the completed-output inspection or the exact pack evaluation is not verified.
func TestPlexPublicationRefusesUnverifiedOutput(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindMovie)
	_, workflowID := fixture.movieWorkflow(t, "Unverified Movie", "770001")
	job, inventoryID, assets := seedSubtitleAttempt(t, fixture.ctx, fixture.store, workflowID, fixture.altMount.ID, fixture.indexer.ID, 1, "unverified-movie")
	if _, err := fixture.store.RecordSubtitleVerification(fixture.ctx, job, inventoryID, unverifiedSubtitleResult(assets[0], "required subtitle evidence is missing"), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.LoadPublicationContext(fixture.ctx, workflowID, job.JobID); err == nil {
		t.Fatal("unverified movie output was accepted for publication")
	}
	var versions, publishEvents int
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT (SELECT count(*) FROM library_link_versions WHERE workflow_id=$1),(SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='library.publish_requested')`, workflowID.String()).Scan(&versions, &publishEvents); err != nil || versions != 0 || publishEvents != 0 {
		t.Fatalf("unverified movie versions=%d publish events=%d err=%v", versions, publishEvents, err)
	}

	seriesFixture := newPlexPublicationFixture(t, catalog.KindSeries)
	_, seriesWorkflowID := seriesFixture.seriesWorkflow(t, "Unverified Show", "770002", []ManualEpisode{{Number: 1}, {Number: 2}, {Number: 3}})
	packJob, packInventory, packAssets := seedSubtitleAttempt(t, seriesFixture.ctx, seriesFixture.store, seriesWorkflowID, seriesFixture.altMount.ID, seriesFixture.indexer.ID, 1, "unverified-pack")
	evidence := mediainfo.Evidence{SchemaVersion: 1, ContainerNames: []string{"matroska"}, VideoTracks: []mediainfo.Track{{Index: 0, Codec: "hevc", Width: 1920, Height: 1080}}, AudioTracks: []mediainfo.Track{}, SubtitleTracks: []mediainfo.Track{}, Facts: map[string]any{"resolution": "1080p"}}
	verified := subtitles.Verification{SchemaVersion: 1, Status: "verified", RequiredLanguages: []string{}, PreferredLanguages: []string{}, ObservedLanguages: []string{}, Videos: []subtitles.VideoEvidence{{AssetID: packAssets[0], RelativePath: "Movie.mkv", MediaInfo: evidence}}, Companions: []subtitles.Companion{}, FailureReasons: []string{}}
	if _, err := seriesFixture.store.RecordSubtitleVerification(seriesFixture.ctx, packJob, packInventory, verified, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := seriesFixture.store.CompleteImport(seriesFixture.ctx, packJob, true, "subtitle verified but pack evaluation absent", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := seriesFixture.store.LoadPublicationContext(seriesFixture.ctx, seriesWorkflowID, packJob.JobID); err == nil {
		t.Fatal("season pack without a verified exact evaluation was accepted for publication")
	}
}

func assertPublicationState(t *testing.T, fixture *plexPublicationFixture, versionID uuid.UUID, wantVersionState, wantWorkflowState string) {
	t.Helper()
	var versionState, workflowState string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT version.state,workflow.state FROM library_link_versions version JOIN workflows workflow ON workflow.id=version.workflow_id WHERE version.id=$1`, versionID.String()).Scan(&versionState, &workflowState); err != nil {
		t.Fatal(err)
	}
	if versionState != wantVersionState || workflowState != wantWorkflowState {
		t.Fatalf("version=%q workflow=%q, want version=%q workflow=%q", versionState, workflowState, wantVersionState, wantWorkflowState)
	}
}
