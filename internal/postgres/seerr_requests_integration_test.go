package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/releases"
)

// TestSeerrRequestsReadModelSpansInstances proves the owner-facing request
// history survives a change of configured intake instance: an empty instance
// lists every Seerr source, each row carries its source name, and colliding
// request IDs stay distinct through the instance-qualified key.
func TestSeerrRequestsReadModelSpansInstances(t *testing.T) {
	databaseURL := os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	store := subtitleIntegrationStore(t, ctx, databaseURL)
	defer store.Close()

	production, err := store.CreateIntegration(ctx, Integration{Kind: "seerr", Name: "Production Seerr", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://seerr:5055","allow_http":true}`), SecretRef: "SEERR_API_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	testInstance, err := store.CreateIntegration(ctx, Integration{Kind: "seerr", Name: "Seerr - Media Manager Test", Enabled: true, Config: json.RawMessage(`{"base_url":"http://seerr-media-manager-test:5055","allow_http":true}`), SecretRef: "SEERR_API_KEY"})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	insertRequest := func(t *testing.T, instanceID, title string, payload byte) {
		t.Helper()
		deliveryID := uuid.NewV7()
		if _, err := store.Pool.Exec(ctx, `INSERT INTO seerr_deliveries(id,seerr_instance_id,source,event_id,request_id,payload_sha256,payload,received_at) VALUES($1,$2,'webhook',$3,'1',$4,'{}',$5)`, deliveryID.String(), instanceID, "event-"+title, []byte{payload}, now); err != nil {
			t.Fatal(err)
		}
		intake := SeerrIntake{InstanceID: instanceID, RequestID: "1", UpdatedAt: now, RequestState: "2", Kind: catalog.KindMovie, Title: title, PayloadHash: []byte{payload}, DeliveryID: deliveryID}
		if changed, err := store.UpsertSeerrSnapshot(ctx, intake); err != nil || !changed {
			t.Fatalf("snapshot changed=%v err=%v", changed, err)
		}
	}
	insertRequest(t, production.ID.String(), "Production Request", 11)
	insertRequest(t, testInstance.ID.String(), "Historical Test Request", 22)

	all, err := store.ListSeerrRequests(ctx, "", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("cross-instance requests=%d, want 2", len(all))
	}
	byKey := map[string]SeerrRequestSummary{}
	for _, item := range all {
		if item.ID != item.SeerrInstanceID+":"+item.RequestID {
			t.Fatalf("request key=%q is not instance-qualified", item.ID)
		}
		byKey[item.ID] = item
	}
	productionKey := production.ID.String() + ":1"
	testKey := testInstance.ID.String() + ":1"
	if byKey[productionKey].SeerrInstanceName != "Production Seerr" || byKey[productionKey].Title != "Production Request" {
		t.Fatalf("production row=%+v", byKey[productionKey])
	}
	if byKey[testKey].SeerrInstanceName != "Seerr - Media Manager Test" || byKey[testKey].Title != "Historical Test Request" {
		t.Fatalf("test row=%+v", byKey[testKey])
	}

	onlyProduction, err := store.ListSeerrRequests(ctx, production.ID.String(), 100, 0)
	if err != nil || len(onlyProduction) != 1 || onlyProduction[0].SeerrInstanceName != "Production Seerr" {
		t.Fatalf("scoped production requests=%+v err=%v", onlyProduction, err)
	}
	onlyUnused, err := store.ListSeerrRequests(ctx, uuid.New().String(), 100, 0)
	if err != nil || len(onlyUnused) != 0 {
		t.Fatalf("unknown instance requests=%+v err=%v", onlyUnused, err)
	}
}

func TestSeerrRequestsReadModel(t *testing.T) {
	databaseURL := os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	store := subtitleIntegrationStore(t, ctx, databaseURL)
	defer store.Close()

	plex, err := store.CreateIntegration(ctx, Integration{Kind: "plex", Name: "Plex", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://plex:32400","allow_http":true}`), SecretRef: "PLEX_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateIntegration(ctx, Integration{Kind: "altmount", Name: "AltMount", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://altmount:8080","allow_http":true}`)}); err != nil {
		t.Fatal(err)
	}
	seerr, err := store.CreateIntegration(ctx, Integration{Kind: "seerr", Name: "Seerr", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://seerr:5055","allow_http":true}`), SecretRef: "SEERR_API_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	indexer, err := store.CreateIntegration(ctx, Integration{Kind: "newznab", Name: "Indexer", Config: json.RawMessage(`{"base_url":"https://indexer.invalid/api"}`), SecretRef: "INDEXER_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	dialects := newznab.SearchDialects{
		Movie:  newznab.SearchDialect{Mode: "movie", Params: []string{"q", "imdbid"}},
		Series: newznab.SearchDialect{Mode: "tvsearch", Params: []string{"q", "tvdbid", "season"}},
	}
	if _, err := store.CompleteNewznabCommission(ctx, indexer.ID, newznab.CapsSnapshot{ServerVersion: "test", BodyHash: "test", DefaultLimit: 100, MaximumLimit: 100}, dialects, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateLibraryRoot(ctx, LibraryRoot{MediaKind: catalog.KindMovie, Name: "Movies", DestinationRoot: "/libraries/movies", PlexVisibleRoot: "/libraries/movies", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &plex.ID, PlexLibraryID: "1", Default: true, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateLibraryRoot(ctx, LibraryRoot{MediaKind: catalog.KindSeries, Name: "TV", DestinationRoot: "/libraries/tv", PlexVisibleRoot: "/libraries/tv", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &plex.ID, PlexLibraryID: "2", Default: true, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	rules := []releases.Rule{{Name: "Required resolution", Fact: "resolution", AllowedValues: []string{"1080p"}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold}}
	policy, _ := json.Marshal(map[string]any{"rules": rules})
	if _, err := store.CreateQualityProfile(ctx, QualityProfile{MediaKind: catalog.KindMovie, Name: "Movie", Policy: policy, UpgradesEnabled: true, Default: true, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateQualityProfile(ctx, QualityProfile{MediaKind: catalog.KindSeries, Name: "TV", Policy: policy, UpgradesEnabled: true, Default: true, Enabled: true}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	year := uint16(2024)

	insertDelivery := func(t *testing.T, deliveryID uuid.UUID, requestID string, payloadHash []byte, at time.Time) {
		t.Helper()
		if _, err := store.Pool.Exec(ctx, `INSERT INTO seerr_deliveries(id,seerr_instance_id,source,event_id,request_id,payload_sha256,payload,received_at) VALUES($1,$2,'webhook',$3,$4,$5,'{}',$6)`,
			deliveryID.String(), seerr.ID.String(), "event-"+requestID, requestID, payloadHash, at.UTC()); err != nil {
			t.Fatal(err)
		}
	}

	// Completed movie: Plex confirmed.
	movieDelivery := uuid.NewV7()
	movieIntake := SeerrIntake{
		InstanceID: seerr.ID.String(), RequestID: "1001", UpdatedAt: now, RequestedAt: now.Add(-2 * time.Hour), RequestState: "2",
		Kind: catalog.KindMovie, Title: "Read Model Movie", Year: &year, Requester: "owner", PosterURL: "https://image.tmdb.org/t/p/w500/movie.jpg",
		Identities:  []catalog.ExternalIdentity{{Namespace: "tmdb", Value: "5001", State: catalog.IdentityVerified, Source: "seerr"}},
		Metadata:    &catalog.MediaMetadata{PosterURL: "https://image.tmdb.org/t/p/w500/movie.jpg"},
		PayloadHash: []byte{1}, DeliveryID: movieDelivery,
	}
	insertDelivery(t, movieDelivery, movieIntake.RequestID, movieIntake.PayloadHash, now)
	if changed, err := store.UpsertSeerrSnapshot(ctx, movieIntake); err != nil || !changed {
		t.Fatalf("movie snapshot changed=%v err=%v", changed, err)
	}
	movieResult, err := store.EnsureMediaFromSeerr(ctx, movieIntake, now)
	if err != nil || len(movieResult.WorkflowIDs) != 1 {
		t.Fatalf("movie intake result=%+v err=%v", movieResult, err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE workflows SET state='plex_confirmed',updated_at=$2 WHERE id=$1`, movieResult.WorkflowIDs[0].String(), now.UTC()); err != nil {
		t.Fatal(err)
	}

	// TV request: workflow needs attention.
	seriesDelivery := uuid.NewV7()
	seriesIntake := SeerrIntake{
		InstanceID: seerr.ID.String(), RequestID: "2002", UpdatedAt: now.Add(time.Minute), RequestedAt: now.Add(-time.Hour), RequestState: "2",
		Kind: catalog.KindSeries, Title: "Read Model Series", Year: &year, Requester: "owner2",
		Identities: []catalog.ExternalIdentity{{Namespace: "tmdb", Value: "6001", State: catalog.IdentityVerified, Source: "seerr"}},
		Seasons:    []SeerrSeason{{Number: 1, Availability: 1, Episodes: []SeerrEpisode{{Number: 1, ProviderID: "tmdb:1", Title: "Pilot", AirDate: "2024-01-01"}}}},
		Metadata:   &catalog.MediaMetadata{}, PayloadHash: []byte{2}, DeliveryID: seriesDelivery,
	}
	insertDelivery(t, seriesDelivery, seriesIntake.RequestID, seriesIntake.PayloadHash, now.Add(time.Minute))
	if changed, err := store.UpsertSeerrSnapshot(ctx, seriesIntake); err != nil || !changed {
		t.Fatalf("series snapshot changed=%v err=%v", changed, err)
	}
	seriesResult, err := store.EnsureMediaFromSeerr(ctx, seriesIntake, now.Add(time.Minute))
	if err != nil || len(seriesResult.WorkflowIDs) != 1 {
		t.Fatalf("series intake result=%+v err=%v", seriesResult, err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE workflows SET state='needs_attention',attention_code='subtitle_verification_failed',attention_detail='required Danish subtitles were not verified',updated_at=$2 WHERE id=$1`, seriesResult.WorkflowIDs[0].String(), now.Add(2*time.Minute).UTC()); err != nil {
		t.Fatal(err)
	}

	// Request that never became media: intake needs configuration, latest
	// delivery outcome must surface as attention instead of success.
	configDelivery := uuid.NewV7()
	configIntake := SeerrIntake{
		InstanceID: seerr.ID.String(), RequestID: "3003", UpdatedAt: now.Add(2 * time.Minute), RequestState: "2",
		Kind: catalog.KindMovie, Title: "Needs Config Movie", Year: &year, Requester: "owner3",
		PayloadHash: []byte{3}, DeliveryID: configDelivery,
	}
	insertDelivery(t, configDelivery, configIntake.RequestID, configIntake.PayloadHash, now.Add(2*time.Minute))
	if changed, err := store.UpsertSeerrSnapshot(ctx, configIntake); err != nil || !changed {
		t.Fatalf("config snapshot changed=%v err=%v", changed, err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE seerr_deliveries SET outcome='needs_configuration',error_detail='default root or quality profile is missing' WHERE id=$1`, configDelivery.String()); err != nil {
		t.Fatal(err)
	}

	requests, err := store.ListSeerrRequests(ctx, seerr.ID.String(), 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 3 {
		t.Fatalf("requests=%d, want 3", len(requests))
	}
	byID := map[string]SeerrRequestSummary{}
	for index, item := range requests {
		byID[item.RequestID] = item
		if index > 0 && requests[index-1].UpdatedAt.Before(item.UpdatedAt) {
			t.Fatalf("requests are not ordered by updated_at descending: %s then %s", requests[index-1].UpdatedAt, item.UpdatedAt)
		}
	}

	movie := byID["1001"]
	if movie.Lifecycle != "plex_confirmed" || movie.MediaKind != "movie" || movie.Title != "Read Model Movie" || movie.Requester != "owner" {
		t.Fatalf("movie summary=%+v", movie)
	}
	if movie.PosterURL != "https://image.tmdb.org/t/p/w500/movie.jpg" {
		t.Fatalf("movie poster=%q", movie.PosterURL)
	}
	if movie.MediaID == nil || movie.QualityProfile != "Movie" {
		t.Fatalf("movie media/profile=%v/%q", movie.MediaID, movie.QualityProfile)
	}

	series := byID["2002"]
	if series.Lifecycle != "needs_attention" || series.MediaKind != "series" || series.AttentionDetail == "" {
		t.Fatalf("series summary=%+v", series)
	}
	if series.SeasonCount != 1 || series.AvailableSeasons != 0 {
		t.Fatalf("series seasons=%d/%d", series.AvailableSeasons, series.SeasonCount)
	}

	config := byID["3003"]
	if config.Lifecycle != "needs_attention" || config.MediaID != nil || config.IntakeError == "" {
		t.Fatalf("config summary=%+v", config)
	}

	detail, err := store.SeerrRequest(ctx, seerr.ID.String(), "1001")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Request.RequestID != "1001" || len(detail.Workflows) != 1 || detail.Workflows[0].State != "plex_confirmed" {
		t.Fatalf("detail=%+v", detail)
	}

	if _, err := store.SeerrRequest(ctx, seerr.ID.String(), "9999"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing request error=%v, want ErrNotFound", err)
	}

	page, err := store.ListSeerrRequests(ctx, seerr.ID.String(), 1, 0)
	if err != nil || len(page) != 1 || page[0].RequestID != requests[0].RequestID {
		t.Fatalf("first page=%+v err=%v", page, err)
	}
	second, err := store.ListSeerrRequests(ctx, seerr.ID.String(), 1, 1)
	if err != nil || len(second) != 1 || second[0].RequestID == page[0].RequestID {
		t.Fatalf("second page=%+v err=%v", second, err)
	}

	if empty, err := store.ListSeerrRequests(ctx, uuid.New().String(), 100, 0); err != nil || len(empty) != 0 {
		t.Fatalf("unknown instance requests=%+v err=%v", empty, err)
	}

	t.Run("waiting schedule follows exact workflow revision and discovery generation", func(t *testing.T) {
		workflowID := movieResult.WorkflowIDs[0]
		if _, err := store.Pool.Exec(ctx, `UPDATE workflows SET state='waiting',revision=revision+1 WHERE id=$1`, workflowID.String()); err != nil {
			t.Fatal(err)
		}
		candidateID := seedEligibleCandidate(t, ctx, store, workflowID, indexer.ID, "request-new-release", 100, now)
		next := now.Add(6 * time.Hour)
		eventID := uuid.NewV7()
		if _, err := store.Pool.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at,completed_at)
			SELECT $1,'workflow',id,revision,'workflow.search_requested','{"reason":"no eligible release yet"}',$2,$2,$2 FROM workflows WHERE id=$3`, eventID.String(), now, workflowID.String()); err != nil {
			t.Fatal(err)
		}
		current, err := store.SeerrRequest(ctx, seerr.ID.String(), "1001")
		if err != nil || current.Request.NextSearchAt != nil {
			t.Fatalf("completed or old-revision event became a promised retry: %+v err=%v", current.Request, err)
		}
		if _, err := store.Pool.Exec(ctx, `UPDATE outbox_events SET completed_at=NULL,available_at=$2 WHERE id=$1`, eventID.String(), next); err != nil {
			t.Fatal(err)
		}
		current, err = store.SeerrRequest(ctx, seerr.ID.String(), "1001")
		if err != nil || current.Request.NextSearchAt == nil || !current.Request.NextSearchAt.Equal(next) ||
			current.Request.LastSearchAt == nil || !current.Request.LastSearchAt.Equal(now) ||
			current.Request.WaitingReason != "no eligible release yet" || current.Request.Lifecycle != "waiting" {
			t.Fatalf("waiting request=%+v err=%v", current.Request, err)
		}
		if _, err := store.Pool.Exec(ctx, `UPDATE search_runs SET state='partial' WHERE workflow_id=$1`, workflowID.String()); err != nil {
			t.Fatal(err)
		}
		current, err = store.SeerrRequest(ctx, seerr.ID.String(), "1001")
		if err != nil || current.Request.WaitingReason != "Search incomplete: some indexers were deferred or failed; retrying." || current.Request.NextSearchAt == nil || !current.Request.NextSearchAt.Equal(next) {
			t.Fatalf("historical partial run claimed no eligible release: %+v err=%v", current.Request, err)
		}
		if _, err := store.Pool.Exec(ctx, `UPDATE outbox_events SET payload='{"reason":"airing season next episode"}' WHERE id=$1`, eventID.String()); err != nil {
			t.Fatal(err)
		}
		current, err = store.SeerrRequest(ctx, seerr.ID.String(), "1001")
		if err != nil || current.Request.WaitingReason != "airing season next episode" {
			t.Fatalf("historical partial run replaced the newer airing wait: %+v err=%v", current.Request, err)
		}
		if _, err := store.Pool.Exec(ctx, `UPDATE outbox_events SET completed_at=$2 WHERE aggregate_id=$1 AND event_type='workflow.search_requested'`, workflowID.String(), now); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Pool.Exec(ctx, `UPDATE acquisition_rounds SET state='exhausted',exhausted_at=$2,closed_at=$2 WHERE workflow_id=$1`, workflowID.String(), now); err != nil {
			t.Fatal(err)
		}
		next = now.Add(24 * time.Hour)
		if _, err := store.Pool.Exec(ctx, `INSERT INTO workflow_retry_discovery(workflow_id,exhausted_round_id,state,rescan_generation,next_rescan_at,created_at,updated_at)
			SELECT workflow_id,id,'waiting',7,$2,$3,$3 FROM acquisition_rounds WHERE workflow_id=$1`, workflowID.String(), next, now); err != nil {
			t.Fatal(err)
		}
		for _, generation := range []int{6, 7} {
			at := now
			if generation == 7 {
				at = next
			}
			if _, err := store.Pool.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at)
				VALUES($1,'workflow_retry_discovery',$2,$3,'workflow.discovery_rescan_requested','{}',$4,$5)`, uuid.NewV7().String(), workflowID.String(), generation, now, at); err != nil {
				t.Fatal(err)
			}
		}
		current, err = store.SeerrRequest(ctx, seerr.ID.String(), "1001")
		if err != nil || current.Request.NextSearchAt == nil || !current.Request.NextSearchAt.Equal(next) {
			t.Fatalf("discovery request=%+v err=%v", current.Request, err)
		}
		if _, err := store.Pool.Exec(ctx, `UPDATE workflow_retry_discovery SET state='ready',next_rescan_at=NULL,ready_candidate_id=$2,
			ready_first_seen_sequence=(SELECT first_seen_sequence FROM release_candidates WHERE id=$2) WHERE workflow_id=$1`, workflowID.String(), candidateID.String()); err != nil {
			t.Fatal(err)
		}
		current, err = store.SeerrRequest(ctx, seerr.ID.String(), "1001")
		if err != nil || current.Request.Lifecycle != "needs_attention" || current.Request.AttentionCount != 1 || current.Request.NextSearchAt != nil {
			t.Fatalf("ready-for-owner request=%+v err=%v", current.Request, err)
		}
	})
}

// TestSeerrSnapshotEqualCursorChangeIsAccepted pins the intake behavior for
// Seerr snapshots whose payload changes without a newer request.updatedAt
// cursor. Seerr advances season and media availability without always moving
// the cursor, and rejecting those snapshots stranded requests in
// needs_attention on every poll. A strictly older cursor must still never
// overwrite the stored snapshot, and an unchanged payload stays stale.
func TestSeerrSnapshotEqualCursorChangeIsAccepted(t *testing.T) {
	databaseURL := os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	store := subtitleIntegrationStore(t, ctx, databaseURL)
	defer store.Close()

	instance, err := store.CreateIntegration(ctx, Integration{Kind: "seerr", Name: "Snapshot Cursor Test", Enabled: true, Config: json.RawMessage(`{"base_url":"http://seerr:5055","allow_http":true}`), SecretRef: "SEERR_API_KEY"})
	if err != nil {
		t.Fatal(err)
	}

	cursor := time.Now().UTC().Truncate(time.Second)
	upsert := func(t *testing.T, updatedAt time.Time, hash byte, state string) bool {
		t.Helper()
		deliveryID := uuid.NewV7()
		if _, err := store.Pool.Exec(ctx, `INSERT INTO seerr_deliveries(id,seerr_instance_id,source,event_id,request_id,payload_sha256,payload,received_at) VALUES($1,$2,'poll',$3,'700',$4,'{}',$5)`, deliveryID.String(), instance.ID.String(), "event-"+deliveryID.String(), []byte{hash}, updatedAt); err != nil {
			t.Fatal(err)
		}
		accepted, err := store.UpsertSeerrSnapshot(ctx, SeerrIntake{
			InstanceID: instance.ID.String(), RequestID: "700", UpdatedAt: updatedAt, RequestState: state,
			Kind: catalog.KindMovie, Title: "Cursor Test", PayloadHash: []byte{hash}, DeliveryID: deliveryID,
		})
		if err != nil {
			t.Fatalf("upsert hash=%d state=%s: %v", hash, state, err)
		}
		return accepted
	}

	if !upsert(t, cursor, 1, "2") {
		t.Fatal("initial snapshot was not accepted")
	}
	if upsert(t, cursor, 1, "2") {
		t.Fatal("unchanged snapshot must be ignored as stale")
	}
	if !upsert(t, cursor, 2, "2") {
		t.Fatal("equal-cursor snapshot with a changed payload must be accepted")
	}
	if upsert(t, cursor.Add(-time.Minute), 3, "2") {
		t.Fatal("an older cursor must not overwrite the stored snapshot")
	}

	var revision int64
	var storedHash []byte
	var storedState string
	if err := store.Pool.QueryRow(ctx, `SELECT revision,payload_sha256,state FROM seerr_request_snapshots WHERE seerr_instance_id=$1 AND request_id='700'`, instance.ID.String()).Scan(&revision, &storedHash, &storedState); err != nil {
		t.Fatal(err)
	}
	if revision != 2 || !bytes.Equal(storedHash, []byte{2}) || storedState != "2" {
		t.Fatalf("stored snapshot revision=%d hash=%v state=%q", revision, storedHash, storedState)
	}
}

// TestSeerrIntakeMarksConfirmedMediaAvailable proves that a new request for
// media that already has a plex_confirmed workflow for the same variant is
// attached to it and reported as already available instead of downloading a
// duplicate.
func TestSeerrIntakeMarksConfirmedMediaAvailable(t *testing.T) {
	databaseURL := os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	store := subtitleIntegrationStore(t, ctx, databaseURL)
	defer store.Close()

	plex, err := store.CreateIntegration(ctx, Integration{Kind: "plex", Name: "Plex", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://plex:32400","allow_http":true}`), SecretRef: "PLEX_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateIntegration(ctx, Integration{Kind: "altmount", Name: "AltMount", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://altmount:8080","allow_http":true}`)}); err != nil {
		t.Fatal(err)
	}
	seerr, err := store.CreateIntegration(ctx, Integration{Kind: "seerr", Name: "Seerr", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://seerr:5055","allow_http":true}`), SecretRef: "SEERR_API_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	indexer, err := store.CreateIntegration(ctx, Integration{Kind: "newznab", Name: "Indexer", Config: json.RawMessage(`{"base_url":"https://indexer.invalid/api"}`), SecretRef: "INDEXER_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	dialects := newznab.SearchDialects{
		Movie:  newznab.SearchDialect{Mode: "movie", Params: []string{"q", "imdbid"}},
		Series: newznab.SearchDialect{Mode: "tvsearch", Params: []string{"q", "tvdbid", "season"}},
	}
	if _, err := store.CompleteNewznabCommission(ctx, indexer.ID, newznab.CapsSnapshot{ServerVersion: "test", BodyHash: "test", DefaultLimit: 100, MaximumLimit: 100}, dialects, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateLibraryRoot(ctx, LibraryRoot{MediaKind: catalog.KindMovie, Name: "Movies", DestinationRoot: "/libraries/movies", PlexVisibleRoot: "/libraries/movies", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &plex.ID, PlexLibraryID: "1", Default: true, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	rules := []releases.Rule{{Name: "Required resolution", Fact: "resolution", AllowedValues: []string{"1080p"}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold}}
	policy, _ := json.Marshal(map[string]any{"rules": rules, "required_subtitle_languages": []string{"da"}})
	if _, err := store.CreateQualityProfile(ctx, QualityProfile{MediaKind: catalog.KindMovie, Name: "Movie Danish Subs", Policy: policy, UpgradesEnabled: true, Default: true, Enabled: true}); err != nil {
		t.Fatal(err)
	}

	ingest := func(t *testing.T, requestID, tmdb string) (SeerrIntake, IntakeResult) {
		t.Helper()
		now := time.Now().UTC()
		year := uint16(2026)
		deliveryID := uuid.NewV7()
		intake := SeerrIntake{
			InstanceID: seerr.ID.String(), RequestID: requestID, UpdatedAt: now, RequestState: "2",
			Kind: catalog.KindMovie, Title: "Duplicate Check", Year: &year,
			Identities: []catalog.ExternalIdentity{{Namespace: "tmdb", Value: tmdb, State: catalog.IdentityVerified, Source: "seerr"}},
			Metadata:   &catalog.MediaMetadata{}, PayloadHash: []byte(requestID), DeliveryID: deliveryID,
		}
		if _, err := store.Pool.Exec(ctx, `INSERT INTO seerr_deliveries(id,seerr_instance_id,source,event_id,request_id,payload_sha256,payload,received_at) VALUES($1,$2,'webhook',$3,$4,$5,'{}',$6)`, deliveryID.String(), seerr.ID.String(), "event-"+requestID, requestID, intake.PayloadHash, now); err != nil {
			t.Fatal(err)
		}
		if changed, err := store.UpsertSeerrSnapshot(ctx, intake); err != nil || !changed {
			t.Fatalf("snapshot changed=%v err=%v", changed, err)
		}
		result, err := store.EnsureMediaFromSeerr(ctx, intake, now)
		if err != nil {
			t.Fatalf("intake %s: %v", requestID, err)
		}
		return intake, result
	}

	_, first := ingest(t, "dup-first", "940001")
	if len(first.WorkflowIDs) != 1 || first.AlreadyAvailable {
		t.Fatalf("first intake result=%+v", first)
	}
	workflowID := first.WorkflowIDs[0]
	if _, err := store.Pool.Exec(ctx, `UPDATE workflows SET state='plex_confirmed',terminal_at=$2,updated_at=$2 WHERE id=$1`, workflowID.String(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	// The first request's availability sync already exists at the confirmed
	// workflow's revision. A sync for a later request must not collide with it.
	if _, err := store.Pool.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at,completed_at) SELECT $1,'workflow',id,revision,'seerr.status_sync_requested',$2,$3,$3,$3 FROM workflows WHERE id=$4`, uuid.NewV7().String(), seerrStatusEventPayload(workflowID), time.Now().UTC(), workflowID.String()); err != nil {
		t.Fatal(err)
	}

	secondIntake, second := ingest(t, "dup-second", "940001")
	if !second.AlreadyAvailable {
		t.Fatalf("second intake result=%+v, want AlreadyAvailable", second)
	}
	if second.MediaID != first.MediaID {
		t.Fatalf("second media=%s, want %s", second.MediaID, first.MediaID)
	}
	var workflowCount int
	if err := store.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM workflows WHERE media_id=$1 AND variant='default'`, first.MediaID.String()).Scan(&workflowCount); err != nil {
		t.Fatal(err)
	}
	if workflowCount != 1 {
		t.Fatalf("workflows for media=%d, want 1 (no duplicate acquisition)", workflowCount)
	}
	var provenanceCount int
	if err := store.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM seerr_request_workflows WHERE request_id=$1 AND workflow_id=$2`, secondIntake.RequestID, workflowID.String()).Scan(&provenanceCount); err != nil {
		t.Fatal(err)
	}
	if provenanceCount != 1 {
		t.Fatalf("provenance rows=%d, want 1", provenanceCount)
	}
	var syncEvents int
	if err := store.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM outbox_events WHERE aggregate_type='seerr_request' AND event_type='seerr.status_sync_requested' AND completed_at IS NULL AND payload->>'workflow_id'=$1`, workflowID.String()).Scan(&syncEvents); err != nil {
		t.Fatal(err)
	}
	if syncEvents != 1 {
		t.Fatalf("pending request status sync events=%d, want 1 for the second request", syncEvents)
	}
	// Replaying the same request snapshot is idempotent.
	if _, err := store.EnsureMediaFromSeerr(ctx, secondIntake, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM outbox_events WHERE aggregate_type='seerr_request' AND event_type='seerr.status_sync_requested'`).Scan(&syncEvents); err != nil {
		t.Fatal(err)
	}
	if syncEvents != 1 {
		t.Fatalf("request status sync events after replay=%d, want 1", syncEvents)
	}
}
