package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"
	plexadapter "github.com/media-manager/media-manager/internal/adapters/plex"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/mediainfo"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/subtitles"
)

func TestSubtitleVerificationRetriesExactReleaseAndPreservesCurrentMedia(t *testing.T) {
	databaseURL := os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store := subtitleIntegrationStore(t, ctx, databaseURL)
	defer store.Close()

	plex, err := store.CreateIntegration(ctx, Integration{Kind: "plex", Name: "Plex", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://plex:32400","allow_http":true}`), SecretRef: "PLEX_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	altMount, err := store.CreateIntegration(ctx, Integration{Kind: "altmount", Name: "AltMount", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://altmount:8080","allow_http":true,"local_read_only_root":"/mnt/altmount"}`)})
	if err != nil {
		t.Fatal(err)
	}
	indexer, err := store.CreateIntegration(ctx, Integration{Kind: "newznab", Name: "Indexer", Config: json.RawMessage(`{"base_url":"https://indexer.invalid/api"}`), SecretRef: "INDEXER_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	indexerTwo, err := store.CreateIntegration(ctx, Integration{Kind: "newznab", Name: "Indexer Two", Config: json.RawMessage(`{"base_url":"https://indexer-two.invalid/api"}`), SecretRef: "INDEXER_TWO_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	root, err := store.CreateLibraryRoot(ctx, LibraryRoot{MediaKind: catalog.KindMovie, Name: "Movies", DestinationRoot: "/libraries/movies", PlexVisibleRoot: "/libraries/movies", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &plex.ID, PlexLibraryID: "1", Default: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	policy, _ := json.Marshal(map[string]any{
		"rules":                        []releases.Rule{{Name: "Resolution", Fact: "resolution", AllowedValues: []string{"1080p"}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold}},
		"required_subtitle_languages":  []string{"Danish"},
		"preferred_subtitle_languages": []string{"da-DK"},
	})
	profile, err := store.CreateQualityProfile(ctx, QualityProfile{MediaKind: catalog.KindMovie, Name: "Danish", Policy: policy, UpgradesEnabled: true, Default: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	parsedPolicy, err := subtitles.ParsePolicy(profile.Policy)
	if err != nil || len(parsedPolicy.Required) != 1 || parsedPolicy.Required[0] != "da" || len(parsedPolicy.Preferred) != 1 || parsedPolicy.Preferred[0] != "da" {
		t.Fatalf("canonical profile=%s parsed=%+v err=%v", profile.Policy, parsedPolicy, err)
	}
	year := uint16(2026)
	media, err := catalog.New(catalog.KindMovie, "Subtitle Integration", &year, []catalog.ExternalIdentity{{Namespace: "tmdb", Value: "7654321", State: catalog.IdentityVerified, Source: "test"}}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateManualMedia(ctx, media, root.ID, profile.ID, altMount.ID, nil, false, "test")
	if err != nil || len(created) != 1 {
		t.Fatalf("workflow=%+v err=%v", created, err)
	}
	workflowID := created[0].ID
	retrievalMedia, err := catalog.New(catalog.KindMovie, "Retrieval Failure Integration", &year, []catalog.ExternalIdentity{{Namespace: "tmdb", Value: "7654320", State: catalog.IdentityVerified, Source: "test"}}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	retrievalWorkflows, err := store.CreateManualMedia(ctx, retrievalMedia, root.ID, profile.ID, altMount.ID, nil, false, "test")
	if err != nil || len(retrievalWorkflows) != 1 {
		t.Fatalf("retrieval workflow=%+v err=%v", retrievalWorkflows, err)
	}
	retrievalWorkflow := retrievalWorkflows[0]
	retrieval := seedRetrievalCandidate(t, ctx, store, retrievalWorkflow.ID, indexer.ID, "https://indexer.invalid/details/exact-retrieval-guid")
	if err := store.RecordReleaseRetrievalFailure(ctx, retrieval, "bounded test retrieval failure", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var retrievalState, retrievalAttention, failureKind string
	var verificationID *string
	if err := store.Pool.QueryRow(ctx, `SELECT workflow.state,workflow.attention_code,exclusion.failure_kind,exclusion.verification_id::text
		FROM workflows workflow JOIN workflow_release_exclusions exclusion ON exclusion.workflow_id=workflow.id
		WHERE workflow.id=$1`, retrievalWorkflow.ID.String()).Scan(&retrievalState, &retrievalAttention, &failureKind, &verificationID); err != nil {
		t.Fatal(err)
	}
	if retrievalState != "needs_attention" || retrievalAttention != "release_retrieval_failed" || failureKind != "release_retrieval" || verificationID != nil {
		t.Fatalf("retrieval failure state=%q attention=%q kind=%q verification=%v", retrievalState, retrievalAttention, failureKind, verificationID)
	}
	retrievalSearch, err := store.LoadSearchContext(ctx, retrievalWorkflow.ID)
	if err != nil || len(retrievalSearch.Exclusions) != 1 || !retrievalSearch.Exclusions[0].Matches(indexer.ID, "https://indexer.invalid/details/exact-retrieval-guid") || retrievalSearch.Exclusions[0].Matches(indexer.ID, "https://indexer.invalid/details/exact-retrieval-guid-2") || retrievalSearch.Exclusions[0].Matches(uuid.NewV7(), "https://indexer.invalid/details/exact-retrieval-guid") {
		t.Fatalf("retrieval exclusion=%+v err=%v", retrievalSearch.Exclusions, err)
	}
	mainSearch, err := store.LoadSearchContext(ctx, workflowID)
	if err != nil || len(mainSearch.Exclusions) != 0 {
		t.Fatalf("retrieval exclusion escaped workflow: %+v err=%v", mainSearch.Exclusions, err)
	}

	submissionMedia, err := catalog.New(catalog.KindMovie, "Submission Integration", &year, []catalog.ExternalIdentity{{Namespace: "tmdb", Value: "7654319", State: catalog.IdentityVerified, Source: "test"}}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	submissionWorkflows, err := store.CreateManualMedia(ctx, submissionMedia, root.ID, profile.ID, altMount.ID, nil, false, "test")
	if err != nil || len(submissionWorkflows) != 1 {
		t.Fatalf("submission workflow=%+v err=%v", submissionWorkflows, err)
	}
	submission := seedRetrievalCandidate(t, ctx, store, submissionWorkflows[0].ID, indexer.ID, "submission-guid")
	intent, err := store.BeginAltMountSubmission(ctx, submission, testNZBPackageIdentity(1), time.Now().UTC())
	if err != nil || !intent.ShouldSubmit {
		t.Fatalf("submission intent=%+v err=%v", intent, err)
	}
	loadedSubmission, err := store.LoadRetrievalContext(ctx, submission.Workflow.ID, submission.CandidateID)
	if err != nil || loadedSubmission.Workflow.GrabAttemptID == nil || *loadedSubmission.Workflow.GrabAttemptID != intent.JobID {
		t.Fatalf("loaded submitting workflow=%+v err=%v", loadedSubmission.Workflow, err)
	}
	if err := store.MarkAltMountSubmitted(ctx, submission.Workflow.ID, intent.JobID, 12345, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var submittedWorkflowState, submittedJobState string
	var submittedQueueID int64
	if err := store.Pool.QueryRow(ctx, `SELECT workflow.state,job.state,job.external_queue_id FROM workflows workflow JOIN altmount_jobs job ON job.id=workflow.grab_attempt_id WHERE workflow.id=$1`, submission.Workflow.ID.String()).Scan(&submittedWorkflowState, &submittedJobState, &submittedQueueID); err != nil {
		t.Fatal(err)
	}
	if submittedWorkflowState != "queued" || submittedJobState != "queued" || submittedQueueID != 12345 {
		t.Fatalf("submitted workflow=%q job=%q queue=%d", submittedWorkflowState, submittedJobState, submittedQueueID)
	}

	unknownMedia, err := catalog.New(catalog.KindMovie, "Unknown Submission Integration", &year, []catalog.ExternalIdentity{{Namespace: "tmdb", Value: "7654318", State: catalog.IdentityVerified, Source: "test"}}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	unknownWorkflows, err := store.CreateManualMedia(ctx, unknownMedia, root.ID, profile.ID, altMount.ID, nil, false, "test")
	if err != nil || len(unknownWorkflows) != 1 {
		t.Fatalf("unknown workflow=%+v err=%v", unknownWorkflows, err)
	}
	unknown := seedRetrievalCandidate(t, ctx, store, unknownWorkflows[0].ID, indexer.ID, "unknown-submission-guid")
	unknownIntent, err := store.BeginAltMountSubmission(ctx, unknown, testNZBPackageIdentity(2), time.Now().UTC())
	if err != nil || !unknownIntent.ShouldSubmit {
		t.Fatalf("unknown submission intent=%+v err=%v", unknownIntent, err)
	}
	if err := store.MarkAltMountUnknown(ctx, unknown.Workflow.ID, unknownIntent.JobID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var unknownWorkflowState, unknownJobState string
	var unknownQueueID *int64
	if err := store.Pool.QueryRow(ctx, `SELECT workflow.state,job.state,job.external_queue_id FROM workflows workflow JOIN altmount_jobs job ON job.id=workflow.grab_attempt_id WHERE workflow.id=$1`, unknown.Workflow.ID.String()).Scan(&unknownWorkflowState, &unknownJobState, &unknownQueueID); err != nil {
		t.Fatal(err)
	}
	if unknownWorkflowState != "transport_unknown" || unknownJobState != "transport_unknown" || unknownQueueID != nil {
		t.Fatalf("unknown workflow=%q job=%q queue=%v", unknownWorkflowState, unknownJobState, unknownQueueID)
	}
	unknownJob, err := store.LoadAltMountJob(ctx, unknownIntent.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AttachRecoveredQueue(ctx, unknownJob, 54321, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	unknownJob, err = store.LoadAltMountJob(ctx, unknownIntent.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if unknownJob.ExternalQueueID == nil || *unknownJob.ExternalQueueID != 54321 || unknownJob.JobState != "queued" || unknownJob.Workflow.State != "queued" {
		t.Fatalf("recovered job=%+v", unknownJob)
	}
	var recoveredEvents int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND aggregate_revision=$2 AND event_type='altmount.reconcile_requested' AND completed_at IS NULL`, unknownJob.JobID.String(), int64(unknownJob.JobRevision)).Scan(&recoveredEvents); err != nil {
		t.Fatal(err)
	}
	if recoveredEvents != 1 {
		t.Fatalf("recovered reconcile events=%d", recoveredEvents)
	}
	if err := store.RequestAltMountReconciliation(ctx, unknownJob.Workflow.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND aggregate_revision=$2 AND event_type='altmount.reconcile_requested' AND completed_at IS NULL`, unknownJob.JobID.String(), int64(unknownJob.JobRevision)).Scan(&recoveredEvents); err != nil {
		t.Fatal(err)
	}
	if recoveredEvents != 1 {
		t.Fatalf("manual reconcile was not idempotent: events=%d", recoveredEvents)
	}
	oldVersionID := seedCurrentSubtitleVersion(t, ctx, store, media.ID, root.ID, profile.ID, altMount.ID, indexer.ID)

	firstJob, firstInventory, firstAssets := seedSubtitleAttempt(t, ctx, store, workflowID, altMount.ID, indexer.ID, 1, "exact-guid-1")
	firstResult := unverifiedSubtitleResult(firstAssets[0], "attempt one has no verified da subtitle")
	firstResult.PreferredLanguages = nil
	firstRecord, err := store.RecordSubtitleVerification(ctx, firstJob, firstInventory, firstResult, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if firstRecord.RetryState != "queued" || firstRecord.Status != "unverified" {
		t.Fatalf("first record=%+v", firstRecord)
	}
	var preferredCount int
	if err := store.Pool.QueryRow(ctx, `SELECT cardinality(preferred_languages) FROM subtitle_verifications WHERE id=$1`, firstRecord.ID.String()).Scan(&preferredCount); err != nil || preferredCount != 0 {
		t.Fatalf("preferred language empty set=%d err=%v", preferredCount, err)
	}
	var firstWorkflowState, firstJobState, oldState string
	var selected, attempt *string
	if err := store.Pool.QueryRow(ctx, `SELECT state,selected_release_id::text,grab_attempt_id::text FROM workflows WHERE id=$1`, workflowID.String()).Scan(&firstWorkflowState, &selected, &attempt); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT state FROM altmount_jobs WHERE id=$1`, firstJob.JobID.String()).Scan(&firstJobState); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT state FROM library_link_versions WHERE id=$1`, oldVersionID.String()).Scan(&oldState); err != nil {
		t.Fatal(err)
	}
	if firstWorkflowState != "waiting" || selected != nil || attempt != nil || firstJobState != "needs_attention" || oldState != "current" {
		t.Fatalf("first failure workflow=%q selected=%v attempt=%v job=%q old=%q", firstWorkflowState, selected, attempt, firstJobState, oldState)
	}
	search, err := store.LoadSearchContext(ctx, workflowID)
	if err != nil || !search.SubtitleRetry || len(search.Exclusions) != 1 || !search.Exclusions[0].Matches(indexer.ID, "exact-guid-1") || search.Exclusions[0].Matches(indexer.ID, "exact-guid-10") || search.Exclusions[0].Matches(uuid.NewV7(), "exact-guid-1") {
		t.Fatalf("workflow-scoped exclusion=%+v err=%v", search.Exclusions, err)
	}

	duplicateCandidate := seedEligibleCandidate(t, ctx, store, workflowID, indexerTwo.ID, "cross-index-duplicate", 200, time.Now().UTC().Add(time.Second))
	alternateCandidate := seedEligibleCandidate(t, ctx, store, workflowID, indexer.ID, "exact-guid-2", 100, time.Now().UTC())
	if _, err := store.Pool.Exec(ctx, `UPDATE workflows SET state='candidate_selected',selected_release_id=$2,revision=revision+1,updated_at=now() WHERE id=$1`, workflowID.String(), duplicateCandidate.String()); err != nil {
		t.Fatal(err)
	}
	duplicateRetrieval, err := store.LoadRetrievalContext(ctx, workflowID, duplicateCandidate)
	if err != nil {
		t.Fatal(err)
	}
	duplicateIntent, err := store.BeginAltMountSubmission(ctx, duplicateRetrieval, testNZBPackageIdentity(1), time.Now().UTC())
	if err != nil || !duplicateIntent.DuplicatePackage || duplicateIntent.ShouldSubmit || duplicateIntent.NextCandidateID == nil || *duplicateIntent.NextCandidateID != alternateCandidate {
		t.Fatalf("duplicate intent=%+v err=%v", duplicateIntent, err)
	}
	var jobCount int
	var duplicateKind, selectedCandidate string
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1`, workflowID.String()).Scan(&jobCount); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT failure_kind FROM workflow_release_exclusions WHERE workflow_id=$1 AND candidate_id=$2`, workflowID.String(), duplicateCandidate.String()).Scan(&duplicateKind); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT selected_release_id::text FROM workflows WHERE id=$1`, workflowID.String()).Scan(&selectedCandidate); err != nil {
		t.Fatal(err)
	}
	if jobCount != 1 || duplicateKind != "duplicate_package" || selectedCandidate != alternateCandidate.String() {
		t.Fatalf("duplicate created jobs=%d kind=%q selected=%q", jobCount, duplicateKind, selectedCandidate)
	}
	secondRetrieval, err := store.LoadRetrievalContext(ctx, workflowID, alternateCandidate)
	if err != nil {
		t.Fatal(err)
	}
	secondIntent, err := store.BeginAltMountSubmission(ctx, secondRetrieval, testNZBPackageIdentity(2), time.Now().UTC())
	if err != nil || !secondIntent.ShouldSubmit {
		t.Fatalf("alternate submission=%+v err=%v", secondIntent, err)
	}
	secondJob, secondInventory, secondAssets := seedSubmittedSubtitleOutput(t, ctx, store, workflowID, alternateCandidate, secondIntent.JobID, "exact-guid-2")
	secondRecord, err := store.RecordSubtitleVerification(ctx, secondJob, secondInventory, unverifiedSubtitleResult(secondAssets[0], "attempt two has no verified da subtitle"), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	var finalWorkflowState, secondJobState string
	var currentPointer string
	if err := store.Pool.QueryRow(ctx, `SELECT state FROM workflows WHERE id=$1`, workflowID.String()).Scan(&finalWorkflowState); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT state FROM altmount_jobs WHERE id=$1`, secondJob.JobID.String()).Scan(&secondJobState); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT current_version_id::text FROM library_links WHERE current_version_id=$1`, oldVersionID.String()).Scan(&currentPointer); err != nil {
		t.Fatal(err)
	}
	if secondRecord.RetryState != "exhausted" || finalWorkflowState != "needs_attention" || secondJobState != "needs_attention" || currentPointer != oldVersionID.String() {
		t.Fatalf("second record=%+v workflow=%q job=%q pointer=%q", secondRecord, finalWorkflowState, secondJobState, currentPointer)
	}
	if _, err := store.BeginAltMountSubmission(ctx, RetrievalContext{Workflow: secondJob.Workflow, CandidateID: secondJob.CandidateID, DownloadClient: Integration{ID: altMount.ID}}, testNZBPackageIdentity(3), time.Now().UTC()); err == nil || !strings.Contains(err.Error(), "attempt limit") {
		t.Fatalf("third subtitle acquisition attempt was not blocked: %v", err)
	}
	var records, exclusions, activeVersions int
	if err := store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM subtitle_verifications WHERE workflow_id=$1),(SELECT count(*) FROM workflow_release_exclusions WHERE workflow_id=$1),(SELECT count(*) FROM library_link_versions WHERE workflow_id=$1)`, workflowID.String()).Scan(&records, &exclusions, &activeVersions); err != nil {
		t.Fatal(err)
	}
	if records != 2 || exclusions != 3 || activeVersions != 0 {
		t.Fatalf("records=%d exclusions=%d unverified publications=%d", records, exclusions, activeVersions)
	}
	review, err := store.ListSubtitleVerifications(ctx, 10)
	if err != nil || len(review) != 2 || review[0].Status != "unverified" {
		t.Fatalf("owner review=%+v err=%v", review, err)
	}

	boundedMedia, err := catalog.New(catalog.KindMovie, "Bounded Package Dedupe", &year, []catalog.ExternalIdentity{{Namespace: "tmdb", Value: "7654323", State: catalog.IdentityVerified, Source: "test"}}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	boundedWorkflows, err := store.CreateManualMedia(ctx, boundedMedia, root.ID, profile.ID, altMount.ID, nil, false, "test")
	if err != nil || len(boundedWorkflows) != 1 {
		t.Fatalf("bounded workflow=%+v err=%v", boundedWorkflows, err)
	}
	boundedWorkflowID := boundedWorkflows[0].ID
	boundedJob, boundedInventory, boundedAssets := seedSubtitleAttempt(t, ctx, store, boundedWorkflowID, altMount.ID, indexer.ID, 1, "bounded-original")
	if _, err := store.RecordSubtitleVerification(ctx, boundedJob, boundedInventory, unverifiedSubtitleResult(boundedAssets[0], "bounded original has no verified da subtitle"), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	boundedCandidates := make([]uuid.UUID, maxDuplicatePackageSkips+2)
	for index := range boundedCandidates {
		boundedCandidates[index] = seedEligibleCandidate(t, ctx, store, boundedWorkflowID, indexerTwo.ID, "bounded-duplicate-"+string(rune('a'+index)), 100-index, time.Now().UTC())
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE workflows SET state='candidate_selected',selected_release_id=$2,revision=revision+1,updated_at=now() WHERE id=$1`, boundedWorkflowID.String(), boundedCandidates[0].String()); err != nil {
		t.Fatal(err)
	}
	for index := 0; index <= maxDuplicatePackageSkips; index++ {
		boundedRetrieval, loadErr := store.LoadRetrievalContext(ctx, boundedWorkflowID, boundedCandidates[index])
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		boundedIntent, beginErr := store.BeginAltMountSubmission(ctx, boundedRetrieval, testNZBPackageIdentity(1), time.Now().UTC())
		if beginErr != nil || !boundedIntent.DuplicatePackage || boundedIntent.ShouldSubmit {
			t.Fatalf("bounded duplicate %d intent=%+v err=%v", index+1, boundedIntent, beginErr)
		}
		if index < maxDuplicatePackageSkips && (boundedIntent.NextCandidateID == nil || *boundedIntent.NextCandidateID != boundedCandidates[index+1]) {
			t.Fatalf("bounded duplicate %d selected=%v want=%s", index+1, boundedIntent.NextCandidateID, boundedCandidates[index+1])
		}
	}
	var boundedState, boundedAttention, boundedRetry string
	var boundedSelected *string
	if err := store.Pool.QueryRow(ctx, `SELECT workflow.state,workflow.attention_code,workflow.selected_release_id::text,verification.retry_state
		FROM workflows workflow JOIN subtitle_verifications verification ON verification.workflow_id=workflow.id
		WHERE workflow.id=$1`, boundedWorkflowID.String()).Scan(&boundedState, &boundedAttention, &boundedSelected, &boundedRetry); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1`, boundedWorkflowID.String()).Scan(&jobCount); err != nil {
		t.Fatal(err)
	}
	if boundedState != "needs_attention" || boundedAttention != "duplicate_package_skip_limit" || boundedSelected != nil || boundedRetry != "no_alternate" || jobCount != 1 {
		t.Fatalf("bounded state=%q attention=%q selected=%v retry=%q jobs=%d", boundedState, boundedAttention, boundedSelected, boundedRetry, jobCount)
	}

	verifiedMedia, err := catalog.New(catalog.KindMovie, "Verified Subtitle", &year, []catalog.ExternalIdentity{{Namespace: "tmdb", Value: "7654322", State: catalog.IdentityVerified, Source: "test"}}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	verifiedWorkflows, err := store.CreateManualMedia(ctx, verifiedMedia, root.ID, profile.ID, altMount.ID, nil, false, "test")
	if err != nil || len(verifiedWorkflows) != 1 {
		t.Fatalf("verified workflow=%+v err=%v", verifiedWorkflows, err)
	}
	verificationRetryJob, verificationRetryInventory, verificationRetryAssets := seedSubtitleAttempt(t, ctx, store, verifiedWorkflows[0].ID, altMount.ID, indexer.ID, 1, "verified-failed-guid")
	verificationRetryRecord, err := store.RecordSubtitleVerification(ctx, verificationRetryJob, verificationRetryInventory, unverifiedSubtitleResult(verificationRetryAssets[0], "first verification requires an alternate"), time.Now().UTC())
	if err != nil || verificationRetryRecord.RetryState != "queued" {
		t.Fatalf("verification retry=%+v err=%v", verificationRetryRecord, err)
	}
	verifiedJob, verifiedInventory, verifiedAssets := seedSubtitleAttempt(t, ctx, store, verifiedWorkflows[0].ID, altMount.ID, indexer.ID, 2, "verified-guid")
	verifiedResult := subtitles.Verification{
		SchemaVersion: 1, Status: "verified", RequiredLanguages: []string{"da"}, PreferredLanguages: []string{"da"}, ObservedLanguages: []string{"da"},
		Videos:     []subtitles.VideoEvidence{{AssetID: verifiedAssets[0], RelativePath: "Movie.mkv", MediaInfo: mediainfo.Evidence{SchemaVersion: 1, ContainerNames: []string{"matroska"}, VideoTracks: []mediainfo.Track{{Index: 0, Codec: "hevc", Width: 1920, Height: 1080}}, AudioTracks: []mediainfo.Track{}, SubtitleTracks: []mediainfo.Track{}, Facts: map[string]any{"subtitle_languages": []string{}}}, ObservedLanguages: []string{"da"}}},
		Companions: []subtitles.Companion{{VideoAssetID: verifiedAssets[0], SubtitleAssetID: verifiedAssets[1], RelativePath: "Movie.da.srt", Language: "da", RawLanguage: "da"}}, FailureReasons: []string{},
	}
	if _, err := store.RecordSubtitleVerification(ctx, verifiedJob, verifiedInventory, verifiedResult, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var successfulRetryState string
	if err := store.Pool.QueryRow(ctx, `SELECT retry_state FROM subtitle_verifications WHERE id=$1`, verificationRetryRecord.ID.String()).Scan(&successfulRetryState); err != nil || successfulRetryState != "succeeded" {
		t.Fatalf("successful retry state=%q err=%v", successfulRetryState, err)
	}
	if err := store.CompleteImport(ctx, verifiedJob, true, "subtitle requirements verified", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	publicationContext, err := store.LoadPublicationContext(ctx, verifiedJob.Workflow.ID, verifiedJob.JobID)
	if err != nil || len(publicationContext.Assets) != 1 || len(publicationContext.Assets[0].Companions) != 1 || publicationContext.Assets[0].Companions[0].AssetID != verifiedAssets[1] {
		t.Fatalf("publication companions=%+v err=%v", publicationContext.Assets, err)
	}
	destination := "Verified Subtitle (2026) {tmdb-7654322}/Verified Subtitle (2026).mkv"
	companionDestination := strings.TrimSuffix(destination, ".mkv") + ".da.srt"
	versions, err := store.PreparePublication(ctx, verifiedJob.Workflow.ID, verifiedJob.JobID, []PublicationPlan{{AssetID: verifiedAssets[0], DestinationRelative: destination, SourceRelative: "completed/verified-guid/Movie.mkv", PlexVisibleTarget: "/mnt/altmount/completed/verified-guid/Movie.mkv", Companions: []PublicationCompanionPlan{{AssetID: verifiedAssets[1], Language: "da", DestinationRelative: companionDestination, SourceRelative: "completed/verified-guid/Movie.da.srt", PlexVisibleTarget: "/mnt/altmount/completed/verified-guid/Movie.da.srt"}}}}, time.Now().UTC())
	if err != nil || len(versions) != 1 {
		t.Fatalf("prepare publication=%+v err=%v", versions, err)
	}
	if err := store.MarkPublicationPublishing(ctx, verifiedJob.Workflow.ID, verifiedJob.JobID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	scanID, err := store.CompletePublication(ctx, verifiedJob.Workflow.ID, verifiedJob.JobID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	var companionBefore string
	if err := store.Pool.QueryRow(ctx, `SELECT state FROM library_link_companions WHERE link_version_id=$1`, versions[0].ID.String()).Scan(&companionBefore); err != nil || companionBefore != "verified" {
		t.Fatalf("companion before Plex=%q err=%v", companionBefore, err)
	}
	scan, err := store.LoadPlexScan(ctx, scanID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkPlexRefreshRequested(ctx, scan, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	scan, err = store.LoadPlexScan(ctx, scanID)
	if err != nil {
		t.Fatal(err)
	}
	plexPath := "/libraries/movies/" + destination
	if err := store.ConfirmPlexScan(ctx, scan, []plexadapter.Confirmation{{RatingKey: "subtitle-verified", Path: plexPath, ProviderGUID: "tmdb://7654322"}}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var companionAfter, versionAfter, workflowAfter string
	if err := store.Pool.QueryRow(ctx, `SELECT companion.state,version.state,workflow.state FROM library_link_companions companion JOIN library_link_versions version ON version.id=companion.link_version_id JOIN workflows workflow ON workflow.id=version.workflow_id WHERE companion.link_version_id=$1`, versions[0].ID.String()).Scan(&companionAfter, &versionAfter, &workflowAfter); err != nil {
		t.Fatal(err)
	}
	if companionAfter != "current" || versionAfter != "current" || workflowAfter != "plex_confirmed" {
		t.Fatalf("confirmed companion=%q version=%q workflow=%q", companionAfter, versionAfter, workflowAfter)
	}
}

func TestDeferredOutboxWorkDoesNotConsumeAnAttempt(t *testing.T) {
	databaseURL := os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store := subtitleIntegrationStore(t, ctx, databaseURL)
	defer store.Close()

	eventID, aggregateID := uuid.NewV7(), uuid.NewV7()
	if _, err := store.Pool.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'test',$2,1,'test.deferred','{}',now(),now())`, eventID.String(), aggregateID.String()); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimOutbox(ctx, "test-owner", 1, time.Minute)
	if err != nil || len(claimed) != 1 || claimed[0].ID != eventID || claimed[0].Attempts != 1 {
		t.Fatalf("claimed=%+v err=%v", claimed, err)
	}
	retryAt := time.Now().UTC().Add(time.Minute)
	if err := store.DeferOutbox(ctx, eventID, "test-owner", "indexer_pacing_deferred", retryAt); err != nil {
		t.Fatal(err)
	}
	var attempts int
	var availableAt time.Time
	var leaseOwner *string
	var lastError string
	if err := store.Pool.QueryRow(ctx, `SELECT attempts,available_at,lease_owner,last_error FROM outbox_events WHERE id=$1`, eventID.String()).Scan(&attempts, &availableAt, &leaseOwner, &lastError); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 || leaseOwner != nil || lastError != "indexer_pacing_deferred" || availableAt.Before(retryAt.Add(-time.Second)) {
		t.Fatalf("attempts=%d available=%v owner=%v error=%q", attempts, availableAt, leaseOwner, lastError)
	}
}

func testNZBPackageIdentity(seed byte) [sha256.Size]byte {
	var identity [sha256.Size]byte
	identity[0] = seed
	return identity
}

func subtitleIntegrationStore(t *testing.T, ctx context.Context, databaseURL string) *Store {
	t.Helper()
	schema := "mm_subtitle_" + strings.ReplaceAll(uuid.New().String(), "-", "")
	connectionString := databaseURL
	if databaseURL == "env" {
		connectionString = ""
	}
	adminConfig, err := pgxpool.ParseConfig(connectionString)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanup, `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
	})
	storeConfig, err := pgxpool.ParseConfig(connectionString)
	if err != nil {
		t.Fatal(err)
	}
	storeConfig.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, storeConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	store := &Store{Pool: pool}
	if err := Migrate(ctx, store.Pool); err != nil {
		store.Close()
		t.Fatal(err)
	}
	return store
}

func seedSubtitleAttempt(t *testing.T, ctx context.Context, store *Store, workflowID, altMountID, indexerID uuid.UUID, generation int, nativeKey string) (AltMountJobContext, uuid.UUID, []uuid.UUID) {
	t.Helper()
	runID, observationID, candidateID, jobID, inventoryID := uuid.NewV7(), uuid.NewV7(), uuid.NewV7(), uuid.NewV7(), uuid.NewV7()
	videoID, sidecarID := uuid.NewV7(), uuid.NewV7()
	if _, err := store.Pool.Exec(ctx, `INSERT INTO search_runs(id,workflow_id,mode,query_fingerprint,state,policy_version,started_at,completed_at) VALUES($1,$2,'targeted',$3,'complete',1,now(),now())`, runID.String(), workflowID.String(), []byte{byte(generation)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO release_observations(id,indexer_instance_id,operation_key,native_guid,title,body_sha256,raw_fields,normalized_facts,parse_version,problems,observed_at) VALUES($1,$2,'test',$3,$4,$5,'[]','[]','test','[]',now())`, observationID.String(), indexerID.String(), nativeKey, "Subtitle.Integration.2026."+nativeKey, []byte{byte(generation)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO release_candidates(id,workflow_id,search_run_id,observation_id,indexer_instance_id,native_key,title,download_locator_ref,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,now())`, candidateID.String(), workflowID.String(), runID.String(), observationID.String(), indexerID.String(), nativeKey, "Subtitle.Integration.2026."+nativeKey, "native:"+nativeKey); err != nil {
		t.Fatal(err)
	}
	packageIdentity := testNZBPackageIdentity(byte(generation))
	if _, err := store.Pool.Exec(ctx, `INSERT INTO altmount_jobs(id,workflow_id,candidate_id,altmount_instance_id,attempt_generation,upload_name,state,submission_started_at,revision,nzb_package_sha256) VALUES($1,$2,$3,$4,$5,$6,'processing',now(),1,$7)`, jobID.String(), workflowID.String(), candidateID.String(), altMountID.String(), generation, nativeKey+".nzb", packageIdentity[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO altmount_output_inventories(id,job_id,generation,root_relative,inventory_digest,observed_at,frozen_at) VALUES($1,$2,1,$3,$4,now(),now())`, inventoryID.String(), jobID.String(), "completed/"+nativeKey, []byte(nativeKey)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO altmount_output_assets(id,inventory_id,relative_path,size_bytes,media_kind) VALUES($1,$2,'Movie.mkv',1000,'movie'),($3,$2,'Movie.da.srt',100,'movie')`, videoID.String(), inventoryID.String(), sidecarID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE workflows SET state='streaming_ready',selected_release_id=$2,grab_attempt_id=$3,revision=revision+1,updated_at=now(),attention_code=NULL,attention_detail=NULL WHERE id=$1`, workflowID.String(), candidateID.String(), jobID.String()); err != nil {
		t.Fatal(err)
	}
	job, err := store.LoadAltMountJob(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	return job, inventoryID, []uuid.UUID{videoID, sidecarID}
}

func seedEligibleCandidate(t *testing.T, ctx context.Context, store *Store, workflowID, indexerID uuid.UUID, nativeKey string, score int, observedAt time.Time) uuid.UUID {
	t.Helper()
	runID, observationID, candidateID := uuid.NewV7(), uuid.NewV7(), uuid.NewV7()
	if _, err := store.Pool.Exec(ctx, `INSERT INTO search_runs(id,workflow_id,mode,query_fingerprint,state,policy_version,started_at,completed_at) VALUES($1,$2,'targeted',$3,'complete',1,$4,$4)`, runID.String(), workflowID.String(), []byte(nativeKey), observedAt.UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO release_observations(id,indexer_instance_id,operation_key,native_guid,title,body_sha256,raw_fields,normalized_facts,parse_version,problems,observed_at) VALUES($1,$2,'test',$3,$4,$5,'[]','[]','test','[]',$6)`, observationID.String(), indexerID.String(), nativeKey, "Subtitle.Integration.2026."+nativeKey, []byte(nativeKey), observedAt.UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO release_candidates(id,workflow_id,search_run_id,observation_id,indexer_instance_id,native_key,title,download_locator_ref,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, candidateID.String(), workflowID.String(), runID.String(), observationID.String(), indexerID.String(), nativeKey, "Subtitle.Integration.2026."+nativeKey, "native:"+nativeKey, observedAt.UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO candidate_decisions(workflow_id,candidate_id,policy_version,verdict,score,reasons,actor,decided_at) VALUES($1,$2,1,'eligible',$3,'[]','policy',$4)`, workflowID.String(), candidateID.String(), score, observedAt.UTC()); err != nil {
		t.Fatal(err)
	}
	return candidateID
}

func seedSubmittedSubtitleOutput(t *testing.T, ctx context.Context, store *Store, workflowID, candidateID, jobID uuid.UUID, nativeKey string) (AltMountJobContext, uuid.UUID, []uuid.UUID) {
	t.Helper()
	inventoryID, videoID, sidecarID := uuid.NewV7(), uuid.NewV7(), uuid.NewV7()
	if _, err := store.Pool.Exec(ctx, `UPDATE altmount_jobs SET state='processing',last_reconciled_at=now() WHERE id=$1 AND workflow_id=$2 AND candidate_id=$3`, jobID.String(), workflowID.String(), candidateID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO altmount_output_inventories(id,job_id,generation,root_relative,inventory_digest,observed_at,frozen_at) VALUES($1,$2,1,$3,$4,now(),now())`, inventoryID.String(), jobID.String(), "completed/"+nativeKey, []byte(nativeKey)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO altmount_output_assets(id,inventory_id,relative_path,size_bytes,media_kind) VALUES($1,$2,'Movie.mkv',1000,'movie'),($3,$2,'Movie.da.srt',100,'movie')`, videoID.String(), inventoryID.String(), sidecarID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE workflows SET state='streaming_ready',selected_release_id=$2,grab_attempt_id=$3,revision=revision+1,updated_at=now(),attention_code=NULL,attention_detail=NULL WHERE id=$1`, workflowID.String(), candidateID.String(), jobID.String()); err != nil {
		t.Fatal(err)
	}
	job, err := store.LoadAltMountJob(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	return job, inventoryID, []uuid.UUID{videoID, sidecarID}
}

func seedRetrievalCandidate(t *testing.T, ctx context.Context, store *Store, workflowID, indexerID uuid.UUID, nativeKey string) RetrievalContext {
	t.Helper()
	runID, observationID, candidateID := uuid.NewV7(), uuid.NewV7(), uuid.NewV7()
	if _, err := store.Pool.Exec(ctx, `INSERT INTO search_runs(id,workflow_id,mode,query_fingerprint,state,policy_version,started_at,completed_at) VALUES($1,$2,'targeted',$3,'complete',1,now(),now())`, runID.String(), workflowID.String(), []byte{7}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO release_observations(id,indexer_instance_id,operation_key,native_guid,title,body_sha256,raw_fields,normalized_facts,parse_version,problems,observed_at) VALUES($1,$2,'test',$3,'Retrieval.Test.2026.1080p.WEB-DL',$4,'[]','[]','test','[]',now())`, observationID.String(), indexerID.String(), nativeKey, []byte{7}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO release_candidates(id,workflow_id,search_run_id,observation_id,indexer_instance_id,native_key,title,download_locator_ref,created_at) VALUES($1,$2,$3,$4,$5,$6,'Retrieval.Test.2026.1080p.WEB-DL',$7,now())`, candidateID.String(), workflowID.String(), runID.String(), observationID.String(), indexerID.String(), nativeKey, "native:"+nativeKey); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE workflows SET state='candidate_selected',selected_release_id=$2,revision=revision+1,updated_at=now() WHERE id=$1`, workflowID.String(), candidateID.String()); err != nil {
		t.Fatal(err)
	}
	retrieval, err := store.LoadRetrievalContext(ctx, workflowID, candidateID)
	if err != nil {
		t.Fatal(err)
	}
	return retrieval
}

func seedCurrentSubtitleVersion(t *testing.T, ctx context.Context, store *Store, mediaID, rootID, profileID, altMountID, indexerID uuid.UUID) uuid.UUID {
	t.Helper()
	workflowID, runID, observationID, candidateID, jobID, inventoryID, assetID, linkID, versionID := uuid.NewV7(), uuid.NewV7(), uuid.NewV7(), uuid.NewV7(), uuid.NewV7(), uuid.NewV7(), uuid.NewV7(), uuid.NewV7(), uuid.NewV7()
	if _, err := store.Pool.Exec(ctx, `INSERT INTO workflows(id,media_id,state,root_id,quality_profile_id,download_client_id,variant,revision,terminal_at,created_at,updated_at) VALUES($1,$2,'plex_confirmed',$3,$4,$5,'default',1,now(),now(),now())`, workflowID.String(), mediaID.String(), rootID.String(), profileID.String(), altMountID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO search_runs(id,workflow_id,mode,query_fingerprint,state,policy_version,started_at,completed_at) VALUES($1,$2,'targeted',$3,'complete',1,now(),now())`, runID.String(), workflowID.String(), []byte{9}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO release_observations(id,indexer_instance_id,operation_key,native_guid,title,body_sha256,raw_fields,normalized_facts,parse_version,problems,observed_at) VALUES($1,$2,'test','old-guid','Old.Release',$3,'[]','[]','test','[]',now())`, observationID.String(), indexerID.String(), []byte{9}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO release_candidates(id,workflow_id,search_run_id,observation_id,indexer_instance_id,native_key,title,download_locator_ref,created_at) VALUES($1,$2,$3,$4,$5,'old-guid','Old.Release','native:old-guid',now())`, candidateID.String(), workflowID.String(), runID.String(), observationID.String(), indexerID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO altmount_jobs(id,workflow_id,candidate_id,altmount_instance_id,attempt_generation,upload_name,state,submission_started_at,revision) VALUES($1,$2,$3,$4,1,'old.nzb','import_complete',now(),1)`, jobID.String(), workflowID.String(), candidateID.String(), altMountID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO altmount_output_inventories(id,job_id,generation,root_relative,inventory_digest,observed_at,frozen_at) VALUES($1,$2,1,'old',$3,now(),now())`, inventoryID.String(), jobID.String(), []byte{9}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO altmount_output_assets(id,inventory_id,relative_path,size_bytes,media_kind) VALUES($1,$2,'Old.mkv',1000,'movie')`, assetID.String(), inventoryID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE workflows SET selected_release_id=$2,grab_attempt_id=$3 WHERE id=$1`, workflowID.String(), candidateID.String(), jobID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO library_links(id,root_id,destination_relative,current_version_id,created_at) VALUES($1,$2,'Subtitle Integration (2026)/old.mkv',NULL,now())`, linkID.String(), rootID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO library_link_versions(id,library_link_id,workflow_id,source_asset_id,altmount_job_id,generation,source_relative,plex_visible_target,state,verified_at) VALUES($1,$2,$3,$4,$5,1,'old/Old.mkv','/mnt/altmount/old/Old.mkv','current',now())`, versionID.String(), linkID.String(), workflowID.String(), assetID.String(), jobID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE library_links SET current_version_id=$2 WHERE id=$1`, linkID.String(), versionID.String()); err != nil {
		t.Fatal(err)
	}
	return versionID
}

func unverifiedSubtitleResult(videoAssetID uuid.UUID, reason string) subtitles.Verification {
	return subtitles.Verification{SchemaVersion: 1, Status: "unverified", RequiredLanguages: []string{"da"}, PreferredLanguages: []string{"da"}, ObservedLanguages: []string{}, Videos: []subtitles.VideoEvidence{{AssetID: videoAssetID, RelativePath: "Movie.mkv", MediaInfo: mediainfo.Evidence{SchemaVersion: 1, ContainerNames: []string{"matroska"}, VideoTracks: []mediainfo.Track{{Index: 0, Codec: "hevc", Width: 1920, Height: 1080}}, AudioTracks: []mediainfo.Track{}, SubtitleTracks: []mediainfo.Track{}, Facts: map[string]any{"subtitle_languages": []string{}}}}}, FailureReasons: []string{reason}}
}
