package postgres

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
	"uuid"

	plexadapter "github.com/media-manager/media-manager/internal/adapters/plex"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/releases"
)

// episodeStatusFixture is a series tracked by one approved Seerr request whose
// requested seasons are acquired through single-episode workflows.
type episodeStatusFixture struct {
	ctx       context.Context
	store     *Store
	seerrID   string
	requestID string
	mediaID   uuid.UUID
	rootID    string
	profileID string
	clientID  string
}

func newEpisodeStatusFixture(t *testing.T) *episodeStatusFixture {
	t.Helper()
	databaseURL := os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	store := subtitleIntegrationStore(t, ctx, databaseURL)
	t.Cleanup(store.Close)

	seerr, err := store.CreateIntegration(ctx, Integration{Kind: "seerr", Name: "Seerr", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://seerr:5055","allow_http":true}`), SecretRef: "SEERR_API_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	altmount, err := store.CreateIntegration(ctx, Integration{Kind: "altmount", Name: "AltMount", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://altmount:8080","allow_http":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	plex, err := store.CreateIntegration(ctx, Integration{Kind: "plex", Name: "Plex", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://plex:32400","allow_http":true}`), SecretRef: "PLEX_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	root, err := store.CreateLibraryRoot(ctx, LibraryRoot{MediaKind: catalog.KindSeries, Name: "TV", DestinationRoot: "/libraries/tv", PlexVisibleRoot: "/libraries/tv", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &plex.ID, PlexLibraryID: "2", Default: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := json.Marshal(map[string]any{"rules": []releases.Rule{{Name: "Required resolution", Fact: "resolution", AllowedValues: []string{"1080p"}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold}}})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := store.CreateQualityProfile(ctx, QualityProfile{MediaKind: catalog.KindSeries, Name: "TV quality", Policy: policy, Default: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	fixture := &episodeStatusFixture{ctx: ctx, store: store, seerrID: seerr.ID.String(), requestID: "310", mediaID: uuid.NewV7(), rootID: root.ID.String(), profileID: profile.ID.String(), clientID: altmount.ID.String()}
	now := time.Now().UTC()
	if _, err := store.Pool.Exec(ctx, `INSERT INTO media_items(id,kind,title,year,revision,created_at,updated_at) VALUES($1,'series','Episode Only Series',2026,1,$2,$2)`, fixture.mediaID.String(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO external_identities(id,media_id,media_kind,namespace,value,state,source,observed_at) VALUES($1,$2,'series','tmdb','310310','verified','test',$3)`, uuid.NewV7().String(), fixture.mediaID.String(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO seerr_request_snapshots(seerr_instance_id,request_id,external_updated_at,state,identities,payload_sha256,revision,reconciled_at) VALUES($1,$2,$3,'2','[]',$4,1,$3)`, fixture.seerrID, fixture.requestID, now, []byte{3}); err != nil {
		t.Fatal(err)
	}
	return fixture
}

// seasonWorkflow creates a waiting season workflow tracked by the request, with
// a frozen expectation of the given episode numbers, like DispatchAiringSeason
// leaves a season that is acquired per episode.
func (fixture *episodeStatusFixture) seasonWorkflow(t *testing.T, season int16, episodes ...int16) uuid.UUID {
	t.Helper()
	now := time.Now().UTC()
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `INSERT INTO seerr_request_seasons(seerr_instance_id,request_id,season_number,availability_status,requested) VALUES($1,$2,$3,3,true)`, fixture.seerrID, fixture.requestID, season); err != nil {
		t.Fatal(err)
	}
	workflowID := uuid.NewV7()
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `INSERT INTO workflows(id,media_id,state,root_id,quality_profile_id,download_client_id,variant,revision,created_at,updated_at) VALUES($1,$2,'waiting',$3,$4,$5,$6,2,$7,$7)`, workflowID.String(), fixture.mediaID.String(), fixture.rootID, fixture.profileID, fixture.clientID, seasonVariant(season), now); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `INSERT INTO seerr_request_workflows(seerr_instance_id,request_id,workflow_id,attached_snapshot_revision,owns_workflow) VALUES($1,$2,$3,1,true)`, fixture.seerrID, fixture.requestID, workflowID.String()); err != nil {
		t.Fatal(err)
	}
	expectationID := uuid.NewV7()
	digest := []byte(workflowID.String())
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `INSERT INTO pack_expectation_sets(id,workflow_id,series_identity,season,source_revision,expectation_digest,created_at,frozen_at) VALUES($1,$2,$3,$4,1,$5,$6,$6)`, expectationID.String(), workflowID.String(), fixture.mediaID.String(), season, digest, now); err != nil {
		t.Fatal(err)
	}
	for _, episode := range episodes {
		if _, err := fixture.store.Pool.Exec(fixture.ctx, `INSERT INTO pack_expected_episodes(expectation_id,season,episode,provider_episode_id,title) VALUES($1,$2,$3,$4,$5)`, expectationID.String(), season, episode, "tvdb:"+uuid.NewV7().String(), "Episode"); err != nil {
			t.Fatal(err)
		}
	}
	return workflowID
}

// episodeWorkflow creates a single-episode workflow without request provenance,
// exactly as DispatchAiringSeason does.
func (fixture *episodeStatusFixture) episodeWorkflow(t *testing.T, season, episode int16, state string, terminal time.Time) uuid.UUID {
	t.Helper()
	workflowID := uuid.NewV7()
	var terminalAt any
	if state == "plex_confirmed" {
		terminalAt = terminal
	}
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `INSERT INTO workflows(id,media_id,state,root_id,quality_profile_id,download_client_id,variant,revision,terminal_at,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,5,$8,$9,$9)`, workflowID.String(), fixture.mediaID.String(), state, fixture.rootID, fixture.profileID, fixture.clientID, episodeVariant(season, episode), terminalAt, terminal); err != nil {
		t.Fatal(err)
	}
	return workflowID
}

func seasonVariant(season int16) string {
	return "season:" + twoDigits(season)
}

func episodeVariant(season, episode int16) string {
	return seasonVariant(season) + ":episode:" + twoDigits(episode)
}

func twoDigits(value int16) string {
	return string([]byte{byte('0' + value/10), byte('0' + value%10)})
}

// TestSeerrStatusCountsSeasonsCoveredByEpisodeWorkflows proves a season acquired
// through single-episode workflows becomes available to Seerr once every
// frozen expected episode is Plex-confirmed, that confirmed episodes of an
// incomplete season report partial content, and that unconfirmed episodes
// never count.
func TestSeerrStatusCountsSeasonsCoveredByEpisodeWorkflows(t *testing.T) {
	fixture := newEpisodeStatusFixture(t)
	confirmedAt := time.Now().UTC().Add(-time.Hour)
	fixture.seasonWorkflow(t, 1, 1, 2)
	fixture.seasonWorkflow(t, 2, 1, 2, 3)
	s1e1 := fixture.episodeWorkflow(t, 1, 1, "plex_confirmed", confirmedAt)
	fixture.episodeWorkflow(t, 2, 1, "plex_confirmed", confirmedAt)
	fixture.episodeWorkflow(t, 2, 2, "needs_attention", confirmedAt)

	status, err := fixture.store.LoadSeerrStatusSyncContext(fixture.ctx, fixture.seerrID, s1e1)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.RequestIDs) != 1 || status.RequestIDs[0] != fixture.requestID || len(status.AvailableSeasons) != 0 || status.AllAvailable || !status.PartialContent {
		t.Fatalf("incomplete seasons status=%+v, want no available season and partial content", status)
	}

	fixture.episodeWorkflow(t, 1, 2, "plex_confirmed", confirmedAt)
	status, err = fixture.store.LoadSeerrStatusSyncContext(fixture.ctx, fixture.seerrID, s1e1)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.AvailableSeasons) != 1 || status.AvailableSeasons[0] != 1 || status.AllAvailable || !status.PartialContent {
		t.Fatalf("season 1 covered status=%+v, want season 1 available and season 2 partial", status)
	}

	fixture.episodeWorkflow(t, 2, 3, "plex_confirmed", confirmedAt)
	fixture.episodeWorkflow(t, 2, 2, "plex_confirmed", confirmedAt)
	status, err = fixture.store.LoadSeerrStatusSyncContext(fixture.ctx, fixture.seerrID, s1e1)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.AvailableSeasons) != 2 || !status.AllAvailable || status.PartialContent {
		t.Fatalf("all seasons covered status=%+v, want both available", status)
	}
}

// TestSeerrStatusSeasonWithoutExpectedEpisodesIsNotCovered proves an empty
// expectation never counts as complete episode coverage.
func TestSeerrStatusSeasonWithoutExpectedEpisodesIsNotCovered(t *testing.T) {
	fixture := newEpisodeStatusFixture(t)
	fixture.seasonWorkflow(t, 1)
	episode := fixture.episodeWorkflow(t, 1, 1, "plex_confirmed", time.Now().UTC())
	status, err := fixture.store.LoadSeerrStatusSyncContext(fixture.ctx, fixture.seerrID, episode)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.AvailableSeasons) != 0 || status.AllAvailable || !status.PartialContent {
		t.Fatalf("empty expectation status=%+v, want partial content only", status)
	}
}

// TestSeerrStatusBootstrapQueuesEpisodeWorkflowsOncePerMedia proves the
// periodic bootstrap backfills availability for Plex-confirmed episode
// workflows of a tracked season, queues at most one sync per media, skips
// episodes an existing later sync already covers, and ignores episode
// workflows whose season no request tracks.
func TestSeerrStatusBootstrapQueuesEpisodeWorkflowsOncePerMedia(t *testing.T) {
	fixture := newEpisodeStatusFixture(t)
	base := time.Now().UTC().Add(-2 * time.Hour)
	fixture.seasonWorkflow(t, 1, 1, 2, 3)
	for episode := int16(1); episode <= 3; episode++ {
		fixture.episodeWorkflow(t, 1, episode, "plex_confirmed", base.Add(time.Duration(episode)*time.Minute))
	}
	// Season 3 is not tracked by any request, so its episodes stay untouched.
	fixture.episodeWorkflow(t, 3, 1, "plex_confirmed", base)

	syncCount := func() int {
		t.Helper()
		var count int
		if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT count(*) FROM outbox_events WHERE event_type='seerr.status_sync_requested'`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}

	queued, err := fixture.store.QueueConfirmedSeerrStatusSyncs(fixture.ctx, fixture.seerrID, time.Now().UTC(), 25)
	if err != nil || queued != 1 || syncCount() != 1 {
		t.Fatalf("first bootstrap queued=%d total=%d err=%v, want one sync for the media", queued, syncCount(), err)
	}
	queued, err = fixture.store.QueueConfirmedSeerrStatusSyncs(fixture.ctx, fixture.seerrID, time.Now().UTC(), 25)
	if err != nil || queued != 0 {
		t.Fatalf("second bootstrap queued=%d err=%v, want the later sync to cover every earlier episode", queued, err)
	}

	// An episode confirmed after the last sync needs its own feedback.
	fixture.episodeWorkflow(t, 1, 4, "plex_confirmed", time.Now().UTC().Add(time.Minute))
	queued, err = fixture.store.QueueConfirmedSeerrStatusSyncs(fixture.ctx, fixture.seerrID, time.Now().UTC().Add(2*time.Minute), 25)
	if err != nil || queued != 1 || syncCount() != 2 {
		t.Fatalf("later episode bootstrap queued=%d total=%d err=%v", queued, syncCount(), err)
	}
}

// TestPlexConfirmedEpisodeQueuesSeerrStatusForTrackedSeason proves the exact
// Plex confirmation transaction queues Seerr feedback for a single-episode
// workflow whose season workflow a request tracks, and not for one whose
// season is untracked.
func TestPlexConfirmedEpisodeQueuesSeerrStatusForTrackedSeason(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindMovie)
	seerr, err := fixture.store.CreateIntegration(fixture.ctx, Integration{Kind: "seerr", Name: "Seerr", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://seerr:5055","allow_http":true}`), SecretRef: "SEERR_API_KEY"})
	if err != nil {
		t.Fatal(err)
	}

	confirmAsEpisode := func(t *testing.T, title, tmdb, nativeKey string, tracked bool) uuid.UUID {
		t.Helper()
		// The publication fixture publishes through the real symlink and Plex
		// lifecycle; relabel the workflow as a single episode of season 1 so
		// the confirmation transaction sees an episode dispatched from a season.
		workflowID, _, _, plexPath := publishTimedOutMovie(t, fixture, title, tmdb, nativeKey)
		retryScan, err := fixture.store.RequestPlexReconfirmation(fixture.ctx, workflowID, "owner", time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		var mediaText, rootText, profileText string
		if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT media_id::text,root_id::text,quality_profile_id::text FROM workflows WHERE id=$1`, workflowID.String()).Scan(&mediaText, &rootText, &profileText); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.store.Pool.Exec(fixture.ctx, `UPDATE workflows SET variant='season:01:episode:01' WHERE id=$1`, workflowID.String()); err != nil {
			t.Fatal(err)
		}
		seasonID := uuid.NewV7()
		now := time.Now().UTC()
		if _, err := fixture.store.Pool.Exec(fixture.ctx, `INSERT INTO workflows(id,media_id,state,root_id,quality_profile_id,variant,revision,created_at,updated_at) VALUES($1,$2,'waiting',$3,$4,'season:01',2,$5,$5)`, seasonID.String(), mediaText, rootText, profileText, now); err != nil {
			t.Fatal(err)
		}
		if tracked {
			requestID := "req-" + nativeKey
			if _, err := fixture.store.Pool.Exec(fixture.ctx, `INSERT INTO seerr_request_snapshots(seerr_instance_id,request_id,external_updated_at,state,identities,payload_sha256,revision,reconciled_at) VALUES($1,$2,$3,'2','[]',$4,1,$3)`, seerr.ID.String(), requestID, now, []byte(requestID)); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.store.Pool.Exec(fixture.ctx, `INSERT INTO seerr_request_workflows(seerr_instance_id,request_id,workflow_id,attached_snapshot_revision,owns_workflow) VALUES($1,$2,$3,1,true)`, seerr.ID.String(), requestID, seasonID.String()); err != nil {
				t.Fatal(err)
			}
		}
		scan, err := fixture.store.LoadPlexScan(fixture.ctx, retryScan)
		if err != nil {
			t.Fatal(err)
		}
		if err := fixture.store.MarkPlexRefreshRequested(fixture.ctx, scan, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		if scan, err = fixture.store.LoadPlexScan(fixture.ctx, retryScan); err != nil {
			t.Fatal(err)
		}
		if err := fixture.store.ConfirmPlexScan(fixture.ctx, scan, []plexadapter.Confirmation{{RatingKey: nativeKey, Path: plexPath, ProviderGUID: "tmdb://" + tmdb}}, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		return workflowID
	}

	countSyncs := func(t *testing.T, workflowID uuid.UUID) int {
		t.Helper()
		var count int
		if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_type='workflow' AND aggregate_id=$1 AND event_type='seerr.status_sync_requested'`, workflowID.String()).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}

	tracked := confirmAsEpisode(t, "Tracked Episode", "570001", "tracked-episode", true)
	if got := countSyncs(t, tracked); got != 1 {
		t.Fatalf("tracked episode confirmation queued %d Seerr syncs, want 1", got)
	}
	untracked := confirmAsEpisode(t, "Untracked Episode", "570002", "untracked-episode", false)
	if got := countSyncs(t, untracked); got != 0 {
		t.Fatalf("untracked episode confirmation queued %d Seerr syncs, want 0", got)
	}
}

func TestSeerrRequestsExposeRequestedEpisodeProgressAndAttention(t *testing.T) {
	f := newEpisodeStatusFixture(t)
	f.seasonWorkflow(t, 1, 1, 2)
	now := time.Now().UTC()
	f.episodeWorkflow(t, 1, 1, "plex_confirmed", now)
	second := f.episodeWorkflow(t, 1, 2, "needs_attention", now)
	f.episodeWorkflow(t, 2, 1, "needs_attention", now)  // Another season is not requested.
	f.episodeWorkflow(t, 1, 99, "needs_attention", now) // Not in the frozen expectation.
	detail, err := f.store.SeerrRequest(f.ctx, f.seerrID, f.requestID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Request.WorkflowCount != 3 || detail.Request.ConfirmedCount != 1 || detail.Request.AttentionCount != 1 || detail.Request.CurrentState != "needs_attention" {
		t.Fatalf("episode progress hidden or unrelated scope counted: %+v", detail.Request)
	}
	if _, err = f.store.Pool.Exec(f.ctx, `UPDATE workflows SET state='plex_confirmed',terminal_at=$2 WHERE id=$1`, second.String(), now); err != nil {
		t.Fatal(err)
	}
	detail, err = f.store.SeerrRequest(f.ctx, f.seerrID, f.requestID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Request.WorkflowCount != 3 || detail.Request.ConfirmedCount != 3 || detail.Request.AttentionCount != 0 || detail.Request.Lifecycle != "plex_confirmed" {
		t.Fatalf("completed episode season stayed waiting: %+v", detail.Request)
	}
}
