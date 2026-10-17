package postgres

import (
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
	"github.com/media-manager/media-manager/internal/subtitles"
)

// publishSeriesOutput publishes every asset of a verified series import and
// confirms it in Plex, returning the link version per asset relative path.
func publishSeriesOutput(t *testing.T, fixture *plexPublicationFixture, workflowID, jobID uuid.UUID, providerID string) map[string]uuid.UUID {
	t.Helper()
	publicationContext, err := fixture.store.LoadPublicationContext(fixture.ctx, workflowID, jobID)
	if err != nil {
		t.Fatal(err)
	}
	technical := append(append([]string(nil), publicationContext.Technical...), publicationContext.ReleaseToken)
	plans, links := []PublicationPlan{}, []library.LinkPlan{}
	for _, asset := range publicationContext.Assets {
		source := filepath.Join(fixture.localRoot, publicationContext.InventoryRootRelative, asset.RelativePath)
		if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(source, []byte(asset.RelativePath), 0o444); err != nil && !os.IsPermission(err) {
			t.Fatal(err)
		}
		episodes, title := []uint16{}, ""
		for _, episode := range asset.Episodes {
			episodes = append(episodes, episode.Key.Episode)
		}
		if len(asset.Episodes) == 1 {
			title = asset.Episodes[0].Title
		}
		extension := strings.TrimPrefix(strings.ToLower(filepath.Ext(asset.RelativePath)), ".")
		folder, seasonFolder, file, err := library.RenderEpisodeSet(publicationContext.Media.Title, *publicationContext.Media.Year, "tvdb", providerID, asset.Episodes[0].Key.Season, episodes, title, technical, extension)
		if err != nil {
			t.Fatal(err)
		}
		validated, err := library.ValidatePlan(library.LinkPlan{SourceRoot: filepath.Join(fixture.localRoot, publicationContext.InventoryRootRelative), SourceRelative: asset.RelativePath, DestinationRoot: fixture.destinationRoot, DestinationRel: filepath.Join(folder, seasonFolder, file), PlexVisibleSourceRoot: filepath.Join(fixture.sourceVisibleRoot, publicationContext.InventoryRootRelative)})
		if err != nil {
			t.Fatal(err)
		}
		plans = append(plans, PublicationPlan{AssetID: asset.ID, DestinationRelative: validated.DestinationRel, SourceRelative: filepath.Join(publicationContext.InventoryRootRelative, asset.RelativePath), PlexVisibleTarget: validated.PlexVisibleTarget})
		links = append(links, validated)
	}
	versions, err := fixture.store.PreparePublication(fixture.ctx, workflowID, jobID, plans, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.MarkPublicationPublishing(fixture.ctx, workflowID, jobID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := library.PublishBatch(links); err != nil {
		t.Fatal(err)
	}
	scanID, err := fixture.store.CompletePublication(fixture.ctx, workflowID, jobID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	scan, err := fixture.store.LoadPlexScan(fixture.ctx, scanID)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.MarkPlexRefreshRequested(fixture.ctx, scan, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if scan, err = fixture.store.LoadPlexScan(fixture.ctx, scanID); err != nil {
		t.Fatal(err)
	}
	confirmations := []plexadapter.Confirmation{}
	for index, version := range scan.Versions {
		confirmations = append(confirmations, plexadapter.Confirmation{RatingKey: workflowID.String() + string(rune('a'+index)), Path: version.PlexVisiblePath, ProviderGUID: "tvdb://" + providerID})
	}
	if err := fixture.store.ConfirmPlexScan(fixture.ctx, scan, confirmations, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	byRelative := map[string]uuid.UUID{}
	for index, asset := range publicationContext.Assets {
		byRelative[asset.RelativePath] = versions[index].ID
	}
	return byRelative
}

// seedVerifiedEpisodeOutput drives a single-episode attempt to a verified,
// publishable import.
func seedVerifiedEpisodeOutput(t *testing.T, fixture *plexPublicationFixture, workflowID uuid.UUID, generation int, nativeKey, relative string) AltMountJobContext {
	t.Helper()
	job, inventoryID, assets := seedSubtitleAttempt(t, fixture.ctx, fixture.store, workflowID, fixture.altMount.ID, fixture.indexer.ID, generation, nativeKey)
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `UPDATE altmount_output_assets SET relative_path=CASE id WHEN $2 THEN $3 ELSE 'extra.txt' END,media_kind=CASE id WHEN $2 THEN 'series' ELSE NULL END WHERE inventory_id=$1`, inventoryID.String(), assets[0].String(), relative); err != nil {
		t.Fatal(err)
	}
	expectationID, seriesIdentity, season, expected, err := fixture.store.PackExpectation(fixture.ctx, workflowID)
	if err != nil || len(expected) != 1 {
		t.Fatalf("episode expectation episodes=%d err=%v", len(expected), err)
	}
	report := packs.Evaluate(seriesIdentity, season, expected, []packs.ObservedAsset{{ID: assets[0], RelativePath: relative, Size: 1000}})
	if !report.Publishable() {
		t.Fatalf("episode report=%+v", report)
	}
	if err := fixture.store.PersistPackEvaluation(fixture.ctx, expectationID, inventoryID, report, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	evidence := mediainfo.Evidence{SchemaVersion: 1, ContainerNames: []string{"matroska"}, VideoTracks: []mediainfo.Track{{Index: 0, Codec: "hevc", Width: 1920, Height: 1080}}, AudioTracks: []mediainfo.Track{}, SubtitleTracks: []mediainfo.Track{}, Facts: map[string]any{"resolution": "1080p"}}
	verification := subtitles.Verification{SchemaVersion: 1, Status: "verified", RequiredLanguages: []string{}, PreferredLanguages: []string{}, ObservedLanguages: []string{},
		Videos: []subtitles.VideoEvidence{{AssetID: assets[0], RelativePath: relative, MediaInfo: evidence}}, Companions: []subtitles.Companion{}, FailureReasons: []string{}}
	if _, err := fixture.store.RecordSubtitleVerification(fixture.ctx, job, inventoryID, verification, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.CompleteImport(fixture.ctx, job, true, "episode output verified for publication", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return job
}

// TestAltMountRepairReplacesBrokenSeasonPackEpisode proves the self-repair
// path end to end: AltMount reports one episode file of a published season
// pack corrupted; Media Manager opens an episode workflow for exactly that
// episode, rejects the broken release, and when Plex confirms the replacement
// it retires the broken pack link while the rest of the pack stays published.
func TestAltMountRepairReplacesBrokenSeasonPackEpisode(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindSeries)
	item, packWorkflow := fixture.seriesWorkflow(t, "Repair Show", "770001", []ManualEpisode{{Number: 1, ProviderID: "tvdb:1", Title: "One"}, {Number: 2, ProviderID: "tvdb:2", Title: "Two"}, {Number: 3, ProviderID: "tvdb:3", Title: "Three"}})
	packJob, _, _ := fixture.seedVerifiedPackOutput(t, packWorkflow, 1, "repair-pack")
	packVersions := publishSeriesOutput(t, fixture, packWorkflow, packJob.JobID, "770001")
	var root string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT root_relative FROM altmount_output_inventories WHERE job_id=$1`, packJob.JobID.String()).Scan(&root); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()

	outcome, err := fixture.store.PlanAltMountRepair(fixture.ctx, fixture.altMount.ID, 41, root+"/Show.S01E03.mkv", now)
	if err != nil || outcome != RepairOpened {
		t.Fatalf("plan E03 repair outcome=%q err=%v, want opened", outcome, err)
	}
	var repairWorkflow string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT id::text FROM workflows WHERE media_id=$1 AND variant='season:01:episode:03' AND terminal_at IS NULL`, item.ID.String()).Scan(&repairWorkflow); err != nil {
		t.Fatalf("repair episode workflow: %v", err)
	}
	var brokenTitle string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT candidate.title FROM altmount_jobs job JOIN release_candidates candidate ON candidate.id=job.candidate_id WHERE job.id=$1`, packJob.JobID.String()).Scan(&brokenTitle); err != nil {
		t.Fatal(err)
	}
	titles, err := fixture.store.RepairBrokenReleaseTitles(fixture.ctx, uuid.MustParse(repairWorkflow))
	if err != nil || len(titles) != 1 || titles[0] != brokenTitle {
		t.Fatalf("broken titles=%v err=%v, want [%s]", titles, err, brokenTitle)
	}
	if search, err := fixture.store.LoadSearchContext(fixture.ctx, uuid.MustParse(repairWorkflow)); err != nil || len(search.RepairBrokenTitles) != 1 {
		t.Fatalf("search context repair titles=%v err=%v", search.RepairBrokenTitles, err)
	}
	if again, err := fixture.store.PlanAltMountRepair(fixture.ctx, fixture.altMount.ID, 41, root+"/Show.S01E03.mkv", now); err != nil || again != RepairAlreadyOpen {
		t.Fatalf("second sweep outcome=%q err=%v, want already_open", again, err)
	}
	// A file covering two episodes is not guessed at.
	if multi, err := fixture.store.PlanAltMountRepair(fixture.ctx, fixture.altMount.ID, 42, root+"/Show.S01E01-E02.mkv", now); err != nil || multi != RepairNeedsAttention {
		t.Fatalf("multi-episode outcome=%q err=%v, want needs_attention", multi, err)
	}
	if unmanaged, err := fixture.store.PlanAltMountRepair(fixture.ctx, fixture.altMount.ID, 43, "tv/Other.Show.S01E01/Other.Show.S01E01.mkv", now); err != nil || unmanaged != RepairUnmanaged {
		t.Fatalf("unmanaged outcome=%q err=%v, want unmanaged", unmanaged, err)
	}

	// The replacement is acquired and confirmed in Plex.
	replacementJob := seedVerifiedEpisodeOutput(t, fixture, uuid.MustParse(repairWorkflow), 2, "repair-episode", "Show.S01E03.REPACK.mkv")
	publishSeriesOutput(t, fixture, uuid.MustParse(repairWorkflow), replacementJob.JobID, "770001")

	assertPublicationState(t, fixture, packVersions["Show.S01E03.mkv"], "displaced", "plex_confirmed")
	assertPublicationState(t, fixture, packVersions["Show.S01E01-E02.mkv"], "current", "plex_confirmed")
	var state string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT state FROM altmount_repairs WHERE repair_workflow_id=$1`, repairWorkflow).Scan(&state); err != nil || state != "repaired" {
		t.Fatalf("repair state=%q err=%v, want repaired", state, err)
	}
	repairs, err := fixture.store.ListAltMountRepairs(fixture.ctx, 10)
	if err != nil || len(repairs) != 2 {
		t.Fatalf("repairs=%+v err=%v, want the repaired episode and the multi-episode attention item", repairs, err)
	}
}

// TestAltMountRepairReplacesBrokenMovie proves a broken movie gets a new
// movie workflow that does not need to improve on the broken publication, and
// that the broken link is retired when Plex confirms the replacement.
func TestAltMountRepairReplacesBrokenMovie(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindMovie)
	item, movieWorkflow := fixture.movieWorkflow(t, "Repair Movie", "770002")
	job, inventoryID, _ := fixture.seedVerifiedMovieOutput(t, movieWorkflow, 1, "repair-movie")
	source := filepath.Join(fixture.localRoot, "completed", "repair-movie", "Movie.mkv")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("broken"), 0o444); err != nil {
		t.Fatal(err)
	}
	publicationContext, err := fixture.store.LoadPublicationContext(fixture.ctx, movieWorkflow, job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	scanID, version, _, destination := fixture.publishMoviePlans(t, movieWorkflow, job.JobID, publicationContext)
	scan, err := fixture.store.LoadPlexScan(fixture.ctx, scanID)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.MarkPlexRefreshRequested(fixture.ctx, scan, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if scan, err = fixture.store.LoadPlexScan(fixture.ctx, scanID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.ConfirmPlexScan(fixture.ctx, scan, []plexadapter.Confirmation{{RatingKey: "repair-movie-1", Path: filepath.Join(fixture.plexVisibleRoot, destination), ProviderGUID: "tmdb://770002"}}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var root string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT root_relative FROM altmount_output_inventories WHERE id=$1`, inventoryID.String()).Scan(&root); err != nil {
		t.Fatal(err)
	}
	outcome, err := fixture.store.PlanAltMountRepair(fixture.ctx, fixture.altMount.ID, 51, root+"/Movie.mkv", time.Now().UTC())
	if err != nil || outcome != RepairOpened {
		t.Fatalf("movie repair outcome=%q err=%v, want opened", outcome, err)
	}
	var repairWorkflow string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT id::text FROM workflows WHERE media_id=$1 AND variant='default' AND terminal_at IS NULL`, item.ID.String()).Scan(&repairWorkflow); err != nil {
		t.Fatalf("repair movie workflow: %v", err)
	}
	search, err := fixture.store.LoadSearchContext(fixture.ctx, uuid.MustParse(repairWorkflow))
	if err != nil || search.CurrentRelease == nil || len(search.RepairBrokenTitles) != 1 {
		t.Fatalf("repair search context current=%v titles=%v err=%v", search.CurrentRelease != nil, search.RepairBrokenTitles, err)
	}
	replacement, _, _ := fixture.seedVerifiedMovieOutput(t, uuid.MustParse(repairWorkflow), 2, "repair-movie-replacement")
	replacementSource := filepath.Join(fixture.localRoot, "completed", "repair-movie-replacement", "Movie.mkv")
	if err := os.MkdirAll(filepath.Dir(replacementSource), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replacementSource, []byte("replacement"), 0o444); err != nil {
		t.Fatal(err)
	}
	replacementContext, err := fixture.store.LoadPublicationContext(fixture.ctx, uuid.MustParse(repairWorkflow), replacement.JobID)
	if err != nil {
		t.Fatal(err)
	}
	replacementScanID, _, _, replacementDestination := fixture.publishMoviePlans(t, uuid.MustParse(repairWorkflow), replacement.JobID, replacementContext)
	replacementScan, err := fixture.store.LoadPlexScan(fixture.ctx, replacementScanID)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.MarkPlexRefreshRequested(fixture.ctx, replacementScan, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if replacementScan, err = fixture.store.LoadPlexScan(fixture.ctx, replacementScanID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.ConfirmPlexScan(fixture.ctx, replacementScan, []plexadapter.Confirmation{{RatingKey: "repair-movie-2", Path: filepath.Join(fixture.plexVisibleRoot, replacementDestination), ProviderGUID: "tmdb://770002"}}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	assertPublicationState(t, fixture, version.ID, "displaced", "plex_confirmed")
	var state string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT state FROM altmount_repairs WHERE repair_workflow_id=$1`, repairWorkflow).Scan(&state); err != nil || state != "repaired" {
		t.Fatalf("repair state=%q err=%v, want repaired", state, err)
	}
}
