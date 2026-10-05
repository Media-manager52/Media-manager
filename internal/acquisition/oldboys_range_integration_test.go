package acquisition

import (
	"context"
	"crypto/sha256"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/oldboyscredentials"
	"github.com/media-manager/media-manager/internal/packs"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/workflows"
)

func TestNativeOldBoysCollectionSearchSelectsOneGuardedSeasonAcquisition(t *testing.T) {
	store := openMovieSearchTestStore(t)
	ctx := context.Background()
	server, _ := newMovieSearchServer(t, func(url.Values) string { return emptySearchXML() })
	defer server.Close()
	existing := configureSeasonSearchIndexer(t, store, server.URL, false)
	w := createSeasonSearchWorkflow(t, store, true)
	seasons := uint16(16)
	if _, err := store.RefreshMediaMetadata(ctx, w.MediaID, &catalog.MediaMetadata{OriginalTitle: "Example Series", OriginalLanguage: "da", SeasonCount: &seasons}, nil, "test", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	vault, _ := oldboyscredentials.New(store, strings.Repeat("ab", 32))
	status, err := vault.Save(ctx, oldboyscredentials.Credentials{APIToken: "fixture-api", PassKey: "fixture-pid", RSSKey: "fixture-rid"})
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := store.PrepareOldBoysCommission(ctx, *status.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	native, err := store.CompleteOldBoysCommission(ctx, attempt, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET enabled=false WHERE id=$1`, existing.ID.String()); err != nil {
		t.Fatal(err)
	}
	calls := 0
	service := SearchService{Store: store, Scheduler: indexers.NewScheduler(1, store), OldBoysCredentials: vault, Transport: oldBoysAcquisitionTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "oldboys.pw" || r.URL.Query().Get("name") != "Example Series DANiSH" || r.URL.Query().Get("page") != "1" || r.URL.Query().Get("categories[]") != "2" {
			t.Fatalf("unbounded or scoped-only native request: %s", r.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"77005","attributes":{"name":"Example.Series.S01-S16.DANISH.1080p.AAC2.0-PlexLover","category_id":2,"resolution":"1080p","imdb_id":null,"tvdb_id":null,"tmdb_id":null,"size":null}}]}`))}, nil
	})}
	if err := service.Handle(ctx, postgres.OutboxEvent{AggregateID: w.ID, AggregateRevision: w.Revision}); err != nil {
		t.Fatal(err)
	}
	selected, err := store.GetWorkflow(ctx, w.ID)
	if err != nil || selected.State != workflows.StateCandidateSelected || selected.SelectedRelease == nil || calls != 1 {
		t.Fatalf("collection selection: %+v calls=%d err=%v", selected, calls, err)
	}
	var events, jobs int
	var locator string
	if err := store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='release.retrieve_requested'),(SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1),download_locator_ref FROM release_candidates WHERE workflow_id=$1 AND indexer_instance_id=$2`, w.ID.String(), native.ID.String()).Scan(&events, &jobs, &locator); err != nil {
		t.Fatal(err)
	}
	if events != 1 || jobs != 0 || locator != "native:77005" {
		t.Fatalf("collection bypass/fan-out events=%d jobs=%d locator=%q", events, jobs, locator)
	}
	// The normal job loader must retain the exact admitted native observation,
	// so output projection cannot be enabled merely by a queue/display name.
	retrieval, err := store.LoadRetrievalContext(ctx, w.ID, *selected.SelectedRelease)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := store.BeginAltMountSubmission(ctx, retrieval, sha256.Sum256([]byte("fixture-native-collection")), time.Now().UTC())
	if err != nil || !intent.ShouldSubmit {
		t.Fatalf("intent=%+v err=%v", intent, err)
	}
	job, err := store.LoadAltMountJob(ctx, intent.JobID)
	if err != nil {
		t.Fatal(err)
	}
	_, series, season, expected, err := store.PackExpectation(ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	assets := []packs.ObservedAsset{{ID: uuid.NewV7(), RelativePath: "Example.Series.S01E01.mkv", Size: 100}, {ID: uuid.NewV7(), RelativePath: "Example.Series.S01E02.mkv", Size: 100}, {ID: uuid.NewV7(), RelativePath: "Example.Series.S16E06.mkv", Size: 100}}
	report := evaluateJobPack(job, series, season, expected, assets, packs.EpisodeNumbering{})
	if !report.Publishable() || len(report.Groups) != 2 {
		t.Fatalf("loaded native collection did not verify exact frozen scope: %+v", report)
	}
	job.ReleaseObservation.ParseVersion = "newznab-v1"
	if evaluateJobPack(job, series, season, expected, assets, packs.EpisodeNumbering{}).Publishable() {
		t.Fatal("native projection escaped into generic Newznab")
	}
}
