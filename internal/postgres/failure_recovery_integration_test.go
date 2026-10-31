package postgres

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/mediainfo"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/subtitles"
	"github.com/media-manager/media-manager/internal/workflows"
)

// failureRecoveryFixture is the shared, deterministic substrate for the V1-3
// failure/retry/recovery tests. It never contacts a provider; every durable
// transition is driven directly through the Store.
type failureRecoveryFixture struct {
	store      *Store
	ctx        context.Context
	plex       Integration
	altMount   Integration
	indexer    Integration
	indexerTwo Integration
	root       LibraryRoot
	profile    QualityProfile
}

func newFailureRecoveryFixture(t *testing.T, versions ...int64) *failureRecoveryFixture {
	t.Helper()
	databaseURL := os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	store := subtitleIntegrationStore(t, ctx, databaseURL, versions...)
	t.Cleanup(store.Close)

	plex, err := store.CreateIntegration(ctx, Integration{Kind: "plex", Name: "Recovery Plex", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://plex.invalid:32400","allow_http":true}`), SecretRef: "PLEX_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	altMount, err := store.CreateIntegration(ctx, Integration{Kind: "altmount", Name: "Recovery AltMount", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://altmount.invalid:8080","allow_http":true,"local_read_only_root":"/mnt/altmount"}`)})
	if err != nil {
		t.Fatal(err)
	}
	dialects := newznab.SearchDialects{
		Movie:  newznab.SearchDialect{Mode: "movie", Params: []string{"q", "year"}},
		Series: newznab.SearchDialect{Mode: "tvsearch", Params: []string{"q", "season"}},
	}
	indexer, err := store.CreateIntegration(ctx, Integration{Kind: "newznab", Name: "Recovery Indexer", Config: json.RawMessage(`{"base_url":"https://indexer.invalid/api"}`), SecretRef: "INDEXER_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteNewznabCommission(ctx, indexer.ID, newznab.CapsSnapshot{ServerVersion: "test", BodyHash: "recovery-caps", DefaultLimit: 50, MaximumLimit: 100}, dialects, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	indexerTwo, err := store.CreateIntegration(ctx, Integration{Kind: "newznab", Name: "Recovery Indexer Two", Config: json.RawMessage(`{"base_url":"https://indexer-two.invalid/api"}`), SecretRef: "INDEXER_TWO_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteNewznabCommission(ctx, indexerTwo.ID, newznab.CapsSnapshot{ServerVersion: "test", BodyHash: "recovery-caps-two", DefaultLimit: 50, MaximumLimit: 100}, dialects, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	root, err := store.CreateLibraryRoot(ctx, LibraryRoot{MediaKind: catalog.KindMovie, Name: "Recovery Movies", DestinationRoot: "/libraries/movies", PlexVisibleRoot: "/libraries/movies", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &plex.ID, PlexLibraryID: "1", Default: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := json.Marshal(map[string]any{"rules": []releases.Rule{{Name: "Resolution", Fact: "resolution", AllowedValues: []string{"1080p"}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold}}})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := store.CreateQualityProfile(ctx, QualityProfile{MediaKind: catalog.KindMovie, Name: "Recovery quality", PolicyVersion: 1, Policy: policy, UpgradesEnabled: true, Default: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return &failureRecoveryFixture{store: store, ctx: ctx, plex: plex, altMount: altMount, indexer: indexer, indexerTwo: indexerTwo, root: root, profile: profile}
}

func (fixture *failureRecoveryFixture) movieItem(t *testing.T, title, tmdb string) catalog.MediaItem {
	t.Helper()
	year := uint16(2026)
	item, err := catalog.New(catalog.KindMovie, title, &year, []catalog.ExternalIdentity{{Namespace: "tmdb", Value: tmdb, State: catalog.IdentityVerified, Source: "test"}}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func (fixture *failureRecoveryFixture) newMovieWorkflow(t *testing.T, title, tmdb string) workflows.Workflow {
	t.Helper()
	item := fixture.movieItem(t, title, tmdb)
	created, err := fixture.store.CreateManualMedia(fixture.ctx, item, fixture.root.ID, fixture.profile.ID, fixture.altMount.ID, nil, false, "test")
	if err != nil || len(created) != 1 {
		t.Fatalf("movie workflow=%+v err=%v", created, err)
	}
	return created[0]
}

func (fixture *failureRecoveryFixture) publishedMovie(t *testing.T, title, tmdb string) (catalog.MediaItem, uuid.UUID) {
	t.Helper()
	item := fixture.movieItem(t, title, tmdb)
	created, err := fixture.store.CreateManualMedia(fixture.ctx, item, fixture.root.ID, fixture.profile.ID, fixture.altMount.ID, nil, false, "test")
	if err != nil || len(created) != 1 {
		t.Fatalf("bootstrap workflow=%+v err=%v", created, err)
	}
	if _, err := fixture.store.TransitionWorkflow(fixture.ctx, created[0].ID, created[0].Revision, workflows.StateCancelled, "replace bootstrap with upgrade fixture", "test", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return item, seedCurrentSubtitleVersion(t, fixture.ctx, fixture.store, item.ID, fixture.root.ID, fixture.profile.ID, fixture.altMount.ID, fixture.indexer.ID)
}

func (fixture *failureRecoveryFixture) upgradeWorkflow(t *testing.T, item catalog.MediaItem) workflows.Workflow {
	t.Helper()
	workflow, err := fixture.store.CreateManualWorkflowForExisting(fixture.ctx, item, fixture.root.ID, fixture.profile.ID, fixture.altMount.ID, nil, false, "", "test", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return workflow
}

func (fixture *failureRecoveryFixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var count int
	if err := fixture.store.Pool.QueryRow(fixture.ctx, query, args...).Scan(&count); err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	return count
}

// TestRetrievalFailureIsNotAnAttemptAndCreatesNoJob proves that a Newznab
// retrieval failure before the AltMount boundary consumes no acquisition slot,
// creates no AltMount job, records a workflow-local durable exclusion, and
// never fabricates downstream publication work.
func TestRetrievalFailureIsNotAnAttemptAndCreatesNoJob(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	workflow := fixture.newMovieWorkflow(t, "Retrieval Failure", "900001")
	retrieval := seedRetrievalCandidate(t, fixture.ctx, fixture.store, workflow.ID, fixture.indexer.ID, "retrieval-failure-guid")

	if err := fixture.store.RecordReleaseRetrievalFailure(fixture.ctx, retrieval, "newznab_invalid_nzb", "bounded provider rejection", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	var state, attention, failureKind string
	var verificationID *string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT workflow.state,workflow.attention_code,exclusion.failure_kind,exclusion.verification_id::text
		FROM workflows workflow JOIN workflow_release_exclusions exclusion ON exclusion.workflow_id=workflow.id
		WHERE workflow.id=$1`, workflow.ID.String()).Scan(&state, &attention, &failureKind, &verificationID); err != nil {
		t.Fatal(err)
	}
	if state != string(workflows.StateNeedsAttention) || attention != "release_retrieval_failed" || failureKind != "release_retrieval" || verificationID != nil {
		t.Fatalf("retrieval failure state=%q attention=%q kind=%q verification=%v", state, attention, failureKind, verificationID)
	}

	var roundState string
	var roundJobs int
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT round.state,(SELECT count(*) FROM altmount_jobs job WHERE job.round_id=round.id)
		FROM acquisition_rounds round WHERE round.workflow_id=$1 AND round.state='open'`, workflow.ID.String()).Scan(&roundState, &roundJobs); err != nil {
		t.Fatal(err)
	}
	if roundState != "open" || roundJobs != 0 {
		t.Fatalf("retrieval failure round=%q jobs=%d, want an untouched open round", roundState, roundJobs)
	}

	for query, want := range map[string]int{
		`SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1`:                                                                               0,
		`SELECT count(*) FROM workflow_attempted_releases WHERE workflow_id=$1`:                                                                 0,
		`SELECT count(*) FROM workflow_submitted_packages WHERE workflow_id=$1`:                                                                 0,
		`SELECT count(*) FROM altmount_output_inventories inventory JOIN altmount_jobs job ON job.id=inventory.job_id WHERE job.workflow_id=$1`: 0,
		`SELECT count(*) FROM library_link_versions WHERE workflow_id=$1`:                                                                       0,
		`SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='library.publish_requested'`:                                   0,
	} {
		if got := fixture.count(t, query, workflow.ID.String()); got != want {
			t.Fatalf("retrieval failure count %q=%d, want %d", query, got, want)
		}
	}

	search, err := fixture.store.LoadSearchContext(fixture.ctx, workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(search.Exclusions) != 1 || !search.Exclusions[0].Matches(fixture.indexer.ID, "retrieval-failure-guid") || search.Exclusions[0].Matches(fixture.indexer.ID, "retrieval-failure-guid-2") || search.Exclusions[0].Matches(fixture.indexerTwo.ID, "retrieval-failure-guid") {
		t.Fatalf("retrieval exclusion scope=%+v", search.Exclusions)
	}

	// The exclusion is workflow-local: an unrelated workflow never inherits it.
	other := fixture.newMovieWorkflow(t, "Retrieval Failure Unrelated", "9000011")
	otherSearch, err := fixture.store.LoadSearchContext(fixture.ctx, other.ID)
	if err != nil || len(otherSearch.Exclusions) != 0 {
		t.Fatalf("retrieval exclusion escaped its workflow: %+v err=%v", otherSearch.Exclusions, err)
	}
}

// TestFailedOutputIsExcludedRetainedAndNeverPublished proves a completed output
// that fails release verification stays owner-visible, is durably excluded,
// never becomes a library link, and never queues publication or Plex work.
func TestFailedOutputIsExcludedRetainedAndNeverPublished(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	workflow := fixture.newMovieWorkflow(t, "Failed Output", "900002")
	retrieval := seedRetrievalCandidate(t, fixture.ctx, fixture.store, workflow.ID, fixture.indexer.ID, "failed-output-guid")
	intent, err := fixture.store.BeginAltMountSubmission(fixture.ctx, retrieval, testNZBPackageIdentity(11), time.Now().UTC())
	if err != nil || !intent.ShouldSubmit {
		t.Fatalf("submission intent=%+v err=%v", intent, err)
	}
	job, inventoryID, _ := seedSubmittedSubtitleOutput(t, fixture.ctx, fixture.store, workflow.ID, retrieval.CandidateID, intent.JobID, "failed-output-guid")
	if err := fixture.store.CompleteReleaseFailure(fixture.ctx, job, "output_empty", "completed output contains no usable media", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	var workflowState, jobState, outcome string
	var closedAt *time.Time
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT workflow.state,job.state,job.acquisition_outcome,job.acquisition_closed_at
		FROM workflows workflow JOIN altmount_jobs job ON job.workflow_id=workflow.id AND job.id=$2
		WHERE workflow.id=$1`, workflow.ID.String(), intent.JobID.String()).Scan(&workflowState, &jobState, &outcome, &closedAt); err != nil {
		t.Fatal(err)
	}
	if workflowState != string(workflows.StateWaiting) || jobState != "needs_attention" || outcome != "output_empty" || closedAt == nil {
		t.Fatalf("failed output workflow=%q job=%q outcome=%q closed=%v", workflowState, jobState, outcome, closedAt)
	}

	var releaseKind string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT failure_kind FROM workflow_release_exclusions WHERE workflow_id=$1 AND candidate_id=$2`, workflow.ID.String(), retrieval.CandidateID.String()).Scan(&releaseKind); err != nil {
		t.Fatal(err)
	}
	if releaseKind != "output_empty" {
		t.Fatalf("failed output release exclusion kind=%q", releaseKind)
	}
	if got := fixture.count(t, `SELECT count(*) FROM workflow_package_exclusions WHERE workflow_id=$1`, workflow.ID.String()); got != 1 {
		t.Fatalf("failed output package exclusions=%d, want 1", got)
	}
	// The frozen output is retained and readable for owner review.
	if got := fixture.count(t, `SELECT count(*) FROM altmount_output_inventories WHERE id=$1`, inventoryID.String()); got != 1 {
		t.Fatalf("retained inventories=%d, want 1", got)
	}
	if got := fixture.count(t, `SELECT count(*) FROM altmount_output_assets WHERE inventory_id=$1`, inventoryID.String()); got != 2 {
		t.Fatalf("retained assets=%d, want 2", got)
	}
	if got := fixture.count(t, `SELECT count(*) FROM library_link_versions WHERE workflow_id=$1`, workflow.ID.String()); got != 0 {
		t.Fatalf("failed output created library versions=%d, want 0", got)
	}
	if got := fixture.count(t, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type IN ('library.publish_requested','plex.scan_requested')`, workflow.ID.String()); got != 0 {
		t.Fatalf("failed output queued publication work=%d, want 0", got)
	}
	// One remaining slot exists, so the workflow legitimately queues one exact
	// exclusion-aware retry search rather than a blind resubmission.
	if got := fixture.count(t, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='workflow.search_requested'`, workflow.ID.String()); got != 1 {
		t.Fatalf("failed output retry search events=%d, want 1", got)
	}
	search, err := fixture.store.LoadSearchContext(fixture.ctx, workflow.ID)
	if err != nil || len(search.Exclusions) != 1 || !search.Exclusions[0].Matches(fixture.indexer.ID, "failed-output-guid") {
		t.Fatalf("failed output exclusion=%+v err=%v", search.Exclusions, err)
	}
}

// TestActiveOrCreatedJobIsNeverResubmitted proves that an active AltMount job,
// an ambiguous submission, and a restart-style retry all reconcile the exact
// existing job instead of creating a second transmission.
func TestActiveOrCreatedJobIsNeverResubmitted(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	workflow := fixture.newMovieWorkflow(t, "Duplicate Transmission", "900003")
	retrieval := seedRetrievalCandidate(t, fixture.ctx, fixture.store, workflow.ID, fixture.indexer.ID, "duplicate-transmission-guid")

	first, err := fixture.store.BeginAltMountSubmission(fixture.ctx, retrieval, testNZBPackageIdentity(21), time.Now().UTC())
	if err != nil || !first.ShouldSubmit {
		t.Fatalf("first submission=%+v err=%v", first, err)
	}
	second, err := fixture.store.BeginAltMountSubmission(fixture.ctx, retrieval, testNZBPackageIdentity(21), time.Now().UTC())
	if err != nil || second.ShouldSubmit || second.JobID != first.JobID {
		t.Fatalf("active job was resubmitted: first=%+v second=%+v err=%v", first, second, err)
	}
	if got := fixture.count(t, `SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1`, workflow.ID.String()); got != 1 {
		t.Fatalf("jobs after repeated submission=%d, want 1", got)
	}

	// A crash between job creation and AltMount acceptance leaves the workflow
	// submitting. Restart recovery must route to reconciliation, not resubmit.
	if err := fixture.store.MarkAltMountUnknown(fixture.ctx, workflow.ID, first.JobID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	recovered, err := fixture.store.BeginAltMountSubmission(fixture.ctx, retrieval, testNZBPackageIdentity(21), time.Now().UTC())
	if err != nil || recovered.ShouldSubmit || recovered.JobID != first.JobID {
		t.Fatalf("interrupted job was resubmitted: recovered=%+v err=%v", recovered, err)
	}
	if got := fixture.count(t, `SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1`, workflow.ID.String()); got != 1 {
		t.Fatalf("jobs after restart recovery=%d, want 1", got)
	}

	// A second candidate cannot open a parallel transmission while the first
	// job is still active.
	secondCandidate := seedEligibleCandidate(t, fixture.ctx, fixture.store, workflow.ID, fixture.indexerTwo.ID, "duplicate-transmission-second", 100, time.Now().UTC())
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `UPDATE workflows SET state='candidate_selected',selected_release_id=$2,revision=revision+1,updated_at=now() WHERE id=$1`, workflow.ID.String(), secondCandidate.String()); err != nil {
		t.Fatal(err)
	}
	secondRetrieval, err := fixture.store.LoadRetrievalContext(fixture.ctx, workflow.ID, secondCandidate)
	if err != nil {
		t.Fatal(err)
	}
	parallel, err := fixture.store.BeginAltMountSubmission(fixture.ctx, secondRetrieval, testNZBPackageIdentity(22), time.Now().UTC())
	if err != nil || parallel.ShouldSubmit {
		t.Fatalf("parallel transmission opened for an active workflow: %+v err=%v", parallel, err)
	}
	if got := fixture.count(t, `SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1`, workflow.ID.String()); got != 1 {
		t.Fatalf("jobs after parallel candidate=%d, want 1", got)
	}
}

// TestFailedUpgradeVerificationPreservesCurrentPublication proves the current
// verified media survives a replacement whose completed output fails subtitle
// verification.
func TestFailedUpgradeVerificationPreservesCurrentPublication(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	item, oldVersionID := fixture.publishedMovie(t, "Upgrade Verification Failure", "900004")
	upgrade := fixture.upgradeWorkflow(t, item)
	retrieval := seedRetrievalCandidate(t, fixture.ctx, fixture.store, upgrade.ID, fixture.indexer.ID, "upgrade-verification-guid")
	intent, err := fixture.store.BeginAltMountSubmission(fixture.ctx, retrieval, testNZBPackageIdentity(31), time.Now().UTC())
	if err != nil || !intent.ShouldSubmit {
		t.Fatalf("upgrade submission=%+v err=%v", intent, err)
	}
	job, inventoryID, assets := seedSubmittedSubtitleOutput(t, fixture.ctx, fixture.store, upgrade.ID, retrieval.CandidateID, intent.JobID, "upgrade-verification-guid")
	if _, err := fixture.store.RecordSubtitleVerification(fixture.ctx, job, inventoryID, unverifiedSubtitleResult(assets[0], "replacement has no verified da subtitle"), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	assertCurrentPreserved(t, fixture, oldVersionID, upgrade.ID)
	if got := fixture.count(t, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='library.publish_requested'`, upgrade.ID.String()); got != 0 {
		t.Fatalf("verification-failed upgrade queued publication=%d, want 0", got)
	}
}

// TestFailedUpgradePublicationPreservesCurrentPublication proves a verified
// replacement whose publication fails after planning leaves the old current
// version untouched and quarantines the replacement plan.
func TestFailedUpgradePublicationPreservesCurrentPublication(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	item, oldVersionID := fixture.publishedMovie(t, "Upgrade Publication Failure", "900005")
	upgrade := fixture.upgradeWorkflow(t, item)
	retrieval := seedRetrievalCandidate(t, fixture.ctx, fixture.store, upgrade.ID, fixture.indexer.ID, "upgrade-publication-guid")
	intent, err := fixture.store.BeginAltMountSubmission(fixture.ctx, retrieval, testNZBPackageIdentity(32), time.Now().UTC())
	if err != nil || !intent.ShouldSubmit {
		t.Fatalf("publication upgrade submission=%+v err=%v", intent, err)
	}
	job, inventoryID, assets := seedSubmittedSubtitleOutput(t, fixture.ctx, fixture.store, upgrade.ID, retrieval.CandidateID, intent.JobID, "upgrade-publication-guid")
	if _, err := fixture.store.RecordSubtitleVerification(fixture.ctx, job, inventoryID, verifiedSubtitleResult(t, assets[0], assets[1]), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.CompleteImport(fixture.ctx, job, true, "replacement verified", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	linkID, versionID := uuid.NewV7(), uuid.NewV7()
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `INSERT INTO library_links(id,root_id,destination_relative,created_at) VALUES($1,$2,'Upgrade Publication (2026)/replacement.mkv',now())`, linkID.String(), fixture.root.ID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `INSERT INTO library_link_versions(id,library_link_id,workflow_id,source_asset_id,altmount_job_id,generation,source_relative,plex_visible_target,state) VALUES($1,$2,$3,$4,$5,1,'completed/upgrade-publication-guid/Movie.mkv','/mnt/altmount/completed/upgrade-publication-guid/Movie.mkv','publishing')`, versionID.String(), linkID.String(), upgrade.ID.String(), assets[0].String(), intent.JobID.String()); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.FailPublication(fixture.ctx, upgrade.ID, intent.JobID, "synthetic publication preflight failure", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	assertCurrentPreserved(t, fixture, oldVersionID, upgrade.ID)
	var newVersionState string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT state FROM library_link_versions WHERE id=$1`, versionID.String()).Scan(&newVersionState); err != nil {
		t.Fatal(err)
	}
	if newVersionState != "quarantined" {
		t.Fatalf("failed upgrade version state=%q, want quarantined", newVersionState)
	}
}

func assertCurrentPreserved(t *testing.T, fixture *failureRecoveryFixture, oldVersionID, workflowID uuid.UUID) {
	t.Helper()
	var oldState string
	var pointer string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT state FROM library_link_versions WHERE id=$1`, oldVersionID.String()).Scan(&oldState); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT current_version_id::text FROM library_links WHERE current_version_id=$1`, oldVersionID.String()).Scan(&pointer); err != nil {
		t.Fatal(err)
	}
	if oldState != "current" || pointer != oldVersionID.String() {
		t.Fatalf("current media was displaced by a failed upgrade: state=%q pointer=%q", oldState, pointer)
	}
	var newCurrent int
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT count(*) FROM library_link_versions WHERE workflow_id=$1 AND state='current'`, workflowID.String()).Scan(&newCurrent); err != nil {
		t.Fatal(err)
	}
	if newCurrent != 0 {
		t.Fatalf("failed upgrade published %d current versions, want 0", newCurrent)
	}
}

// TestInterruptedSubmissionRecoveryIsTruthful proves restart reconciliation
// resumes the exact durable job, never fabricates completion, and never blames
// an infrastructure failure on the release.
func TestInterruptedSubmissionRecoveryIsTruthful(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	workflow := fixture.newMovieWorkflow(t, "Interrupted Submission", "900006")
	retrieval := seedRetrievalCandidate(t, fixture.ctx, fixture.store, workflow.ID, fixture.indexer.ID, "interrupted-submission-guid")
	intent, err := fixture.store.BeginAltMountSubmission(fixture.ctx, retrieval, testNZBPackageIdentity(41), time.Now().UTC())
	if err != nil || !intent.ShouldSubmit {
		t.Fatalf("submission intent=%+v err=%v", intent, err)
	}

	// The worker crashed after creating the durable job but before AltMount
	// accepted it. Recovery must reconcile the same job, not complete or
	// resubmit anything.
	if err := fixture.store.MarkAltMountUnknown(fixture.ctx, workflow.ID, intent.JobID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	job, err := fixture.store.LoadAltMountJob(fixture.ctx, intent.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.JobState != "transport_unknown" || job.Workflow.State != workflows.StateTransportUnknown {
		t.Fatalf("interrupted job=%q workflow=%q", job.JobState, job.Workflow.State)
	}
	if err := fixture.store.AttachRecoveredQueue(fixture.ctx, job, 4242, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	job, err = fixture.store.LoadAltMountJob(fixture.ctx, intent.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.JobState != "queued" || job.Workflow.State != workflows.StateQueued || job.ExternalQueueID == nil || *job.ExternalQueueID != 4242 {
		t.Fatalf("recovered job=%q workflow=%q queue=%v", job.JobState, job.Workflow.State, job.ExternalQueueID)
	}
	if got := fixture.count(t, `SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1`, workflow.ID.String()); got != 1 {
		t.Fatalf("restart recovery created jobs=%d, want 1", got)
	}
	if got := fixture.count(t, `SELECT count(*) FROM library_link_versions WHERE workflow_id=$1`, workflow.ID.String()); got != 0 {
		t.Fatalf("restart recovery fabricated publications=%d, want 0", got)
	}
	if got := fixture.count(t, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='library.publish_requested'`, workflow.ID.String()); got != 0 {
		t.Fatalf("restart recovery queued publication=%d, want 0", got)
	}
	var state string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT state FROM workflows WHERE id=$1`, workflow.ID.String()).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state == string(workflows.StateImportComplete) || state == string(workflows.StatePlexConfirmed) {
		t.Fatalf("restart recovery fabricated completion: %q", state)
	}

	// An infrastructure failure reopens the same retained job and applies no
	// release exclusion.
	infraWorkflow := fixture.newMovieWorkflow(t, "Infrastructure Recovery", "900007")
	infraRetrieval := seedRetrievalCandidate(t, fixture.ctx, fixture.store, infraWorkflow.ID, fixture.indexer.ID, "infrastructure-recovery-guid")
	infraIntent, err := fixture.store.BeginAltMountSubmission(fixture.ctx, infraRetrieval, testNZBPackageIdentity(42), time.Now().UTC())
	if err != nil || !infraIntent.ShouldSubmit {
		t.Fatalf("infrastructure intent=%+v err=%v", infraIntent, err)
	}
	if err := fixture.store.MarkAltMountSubmitted(fixture.ctx, infraWorkflow.ID, infraIntent.JobID, 777, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	infraJob, err := fixture.store.LoadAltMountJob(fixture.ctx, infraIntent.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.CompleteImportWithCode(fixture.ctx, infraJob, false, "output_inspection_failed", "synthetic bounded probe unavailable", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.RequestAltMountReconciliation(fixture.ctx, infraWorkflow.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var infraWorkflowState, infraJobState string
	var infraClosedAt *time.Time
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT workflow.state,job.state,job.acquisition_closed_at
		FROM workflows workflow JOIN altmount_jobs job ON job.id=workflow.grab_attempt_id WHERE workflow.id=$1`, infraWorkflow.ID.String()).Scan(&infraWorkflowState, &infraJobState, &infraClosedAt); err != nil {
		t.Fatal(err)
	}
	if infraWorkflowState != string(workflows.StateQueued) || infraJobState != "queued" || infraClosedAt != nil {
		t.Fatalf("infrastructure recovery workflow=%q job=%q closed=%v", infraWorkflowState, infraJobState, infraClosedAt)
	}
	if got := fixture.count(t, `SELECT count(*) FROM workflow_release_exclusions WHERE workflow_id=$1`, infraWorkflow.ID.String()); got != 0 {
		t.Fatalf("infrastructure recovery blamed the release with %d exclusions", got)
	}
	if got := fixture.count(t, `SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1`, infraWorkflow.ID.String()); got != 1 {
		t.Fatalf("infrastructure recovery created jobs=%d, want 1", got)
	}
}

// TestExhaustedRetryDiscoveryIsPacedNotImmediate proves that once both
// release-attributable slots are used, the next search is a deterministic,
// paced low-priority discovery rescan rather than an immediate blind retry.
func TestExhaustedRetryDiscoveryIsPacedNotImmediate(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	workflow := fixture.newMovieWorkflow(t, "Paced Retry", "900008")

	first, _, _ := seedSubtitleAttempt(t, fixture.ctx, fixture.store, workflow.ID, fixture.altMount.ID, fixture.indexer.ID, 1, "paced-retry-one")
	if err := fixture.store.CompleteReleaseFailure(fixture.ctx, first, "output_empty", "first output unusable", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	second, _, _ := seedSubtitleAttempt(t, fixture.ctx, fixture.store, workflow.ID, fixture.altMount.ID, fixture.indexer.ID, 2, "paced-retry-two")
	before := time.Now().UTC()
	if err := fixture.store.CompleteReleaseFailure(fixture.ctx, second, "output_empty", "second output unusable", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	var roundState, discoveryState string
	var nextRescan time.Time
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT round.state,discovery.state,discovery.next_rescan_at
		FROM acquisition_rounds round JOIN workflow_retry_discovery discovery ON discovery.exhausted_round_id=round.id
		WHERE round.workflow_id=$1`, workflow.ID.String()).Scan(&roundState, &discoveryState, &nextRescan); err != nil {
		t.Fatal(err)
	}
	if roundState != "exhausted" || discoveryState != "waiting" {
		t.Fatalf("round=%q discovery=%q", roundState, discoveryState)
	}
	delay := nextRescan.Sub(before)
	if delay < 19*time.Hour+12*time.Minute || delay > 28*time.Hour+48*time.Minute {
		t.Fatalf("retry discovery delay=%s escaped the deterministic ±20%% window", delay)
	}

	// The second slot must not queue an immediate targeted retry; the paced
	// discovery event is the only continuation and it is not due yet.
	if got := fixture.count(t, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='workflow.search_requested' AND completed_at IS NULL`, workflow.ID.String()); got != 1 {
		t.Fatalf("exhausted round targeted search events=%d, want only the first-slot retry", got)
	}
	var availableAt time.Time
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT available_at FROM outbox_events WHERE aggregate_id=$1 AND event_type='workflow.discovery_rescan_requested'`, workflow.ID.String()).Scan(&availableAt); err != nil {
		t.Fatal(err)
	}
	if !availableAt.Equal(nextRescan) || !availableAt.After(time.Now().UTC()) {
		t.Fatalf("discovery event available=%s next_rescan=%s", availableAt, nextRescan)
	}
}

func verifiedSubtitleResult(t *testing.T, videoAssetID, companionAssetID uuid.UUID) subtitles.Verification {
	t.Helper()
	return subtitles.Verification{
		SchemaVersion:      1,
		Status:             "verified",
		RequiredLanguages:  []string{"da"},
		PreferredLanguages: []string{"da"},
		ObservedLanguages:  []string{"da"},
		Videos: []subtitles.VideoEvidence{{
			AssetID:      videoAssetID,
			RelativePath: "Movie.mkv",
			MediaInfo: mediainfo.Evidence{
				SchemaVersion:  1,
				ContainerNames: []string{"matroska"},
				VideoTracks:    []mediainfo.Track{{Index: 0, Codec: "hevc", Width: 1920, Height: 1080}},
				AudioTracks:    []mediainfo.Track{},
				SubtitleTracks: []mediainfo.Track{},
				Facts:          map[string]any{"subtitle_languages": []string{}},
			},
			ObservedLanguages: []string{"da"},
		}},
		Companions:     []subtitles.Companion{{VideoAssetID: videoAssetID, SubtitleAssetID: companionAssetID, RelativePath: "Movie.da.srt", Language: "da", RawLanguage: "da"}},
		FailureReasons: []string{},
	}
}

// TestArchiveUnsupportedFailureExcludesReleaseAndRetriesWithinRound proves the
// package-screening failure kinds are release-attributable: the exact release
// is excluded and a replacement search is queued while a round slot remains.
func TestArchiveUnsupportedFailureExcludesReleaseAndRetriesWithinRound(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	workflow := fixture.newMovieWorkflow(t, "Archive Retry", "900009")
	job, _, _ := seedSubtitleAttempt(t, fixture.ctx, fixture.store, workflow.ID, fixture.altMount.ID, fixture.indexer.ID, 1, "archive-retry")
	if err := fixture.store.CompleteReleaseFailure(fixture.ctx, job, "archive_unsupported", "release contains an archive set instead of a playable media file", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT state FROM workflows WHERE id=$1`, workflow.ID.String()).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != string(workflows.StateWaiting) {
		t.Fatalf("workflow state=%q, want waiting", state)
	}
	if got := fixture.count(t, `SELECT count(*) FROM workflow_release_exclusions WHERE workflow_id=$1 AND failure_kind='archive_unsupported'`, workflow.ID.String()); got != 1 {
		t.Fatalf("archive exclusions=%d, want 1", got)
	}
	if got := fixture.count(t, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='workflow.search_requested' AND completed_at IS NULL`, workflow.ID.String()); got != 1 {
		t.Fatalf("replacement search events=%d, want 1", got)
	}
}

// TestSubtitlePolicyForJobFailsClosedToLiveProfileRequirements proves that a
// profile tightened after an attempt started cannot be bypassed: the job's
// round snapshot stays relaxed (immutable), but output verification requires
// the live profile's language.
func TestSubtitlePolicyForJobFailsClosedToLiveProfileRequirements(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	workflow := fixture.newMovieWorkflow(t, "Fail Closed Subtitles", "900010")
	job, _, _ := seedSubtitleAttempt(t, fixture.ctx, fixture.store, workflow.ID, fixture.altMount.ID, fixture.indexer.ID, 1, "fail-closed-subtitles")

	policy, err := json.Marshal(map[string]any{
		"rules":                       []releases.Rule{{Name: "Resolution", Fact: "resolution", AllowedValues: []string{"1080p"}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold}},
		"required_subtitle_languages": []string{"Danish"},
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := fixture.store.UpdateQualityProfile(fixture.ctx, QualityProfile{
		ID: fixture.profile.ID, MediaKind: fixture.profile.MediaKind, Name: fixture.profile.Name,
		Policy: policy, UpgradesEnabled: fixture.profile.UpgradesEnabled, Default: fixture.profile.Default, Enabled: fixture.profile.Enabled,
	}, storedQualityProfileVersion(t, fixture.ctx, fixture.store, fixture.profile.ID))
	if err != nil {
		t.Fatal(err)
	}
	var roundRequired string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT round.policy_snapshot->>'required_subtitle_languages' FROM altmount_jobs job JOIN acquisition_rounds round ON round.id=job.round_id WHERE job.id=$1`, job.JobID.String()).Scan(&roundRequired); err != nil {
		t.Fatal(err)
	}
	if roundRequired != "[]" {
		t.Fatalf("round snapshot required=%q, want the immutable relaxed snapshot", roundRequired)
	}
	effective, err := fixture.store.SubtitlePolicyForJob(fixture.ctx, job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(effective.Required) != 1 || effective.Required[0] != "da" {
		t.Fatalf("effective required=%v, want [da] from live profile version %d", effective.Required, updated.PolicyVersion)
	}
}
