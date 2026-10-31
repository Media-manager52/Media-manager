package postgres

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/releases"
)

// seedExplainCandidate stores one observation and its candidate with the exact
// normalized_facts JSON supplied, so tests can pin the legacy "{}" shape as well
// as the current fact-array shape. last_observed_at is inserted explicitly: the
// release_candidates immutability trigger forbids updating identity columns.
func seedExplainCandidate(t *testing.T, f *failureRecoveryFixture, workflowID uuid.UUID, key, facts string, lastObserved time.Time) uuid.UUID {
	t.Helper()
	runID, observationID, candidateID := uuid.NewV7(), uuid.NewV7(), uuid.NewV7()
	digest := sha256.Sum256([]byte(key))
	title := "Explain.Test.2026.1080p-" + key
	if _, err := f.store.Pool.Exec(f.ctx, `INSERT INTO search_runs(id,workflow_id,mode,query_fingerprint,state,policy_version,started_at,completed_at) VALUES($1,$2,'targeted',$3,'complete',1,now(),now())`, runID.String(), workflowID.String(), digest[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Pool.Exec(f.ctx, `INSERT INTO release_observations(id,indexer_instance_id,operation_key,native_guid,title,body_sha256,raw_fields,normalized_facts,parse_version,problems,observed_at) VALUES($1,$2,'test',$3,$4,$5,'[]',$6,'test','[]',$7)`, observationID.String(), f.indexer.ID.String(), key, title, digest[:], facts, lastObserved); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Pool.Exec(f.ctx, `INSERT INTO release_candidates(id,workflow_id,search_run_id,observation_id,indexer_instance_id,native_key,title,download_locator_ref,created_at,last_observed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)`, candidateID.String(), workflowID.String(), runID.String(), observationID.String(), f.indexer.ID.String(), key, title, "native:"+key, lastObserved); err != nil {
		t.Fatal(err)
	}
	return candidateID
}

func seedExplainDecision(t *testing.T, f *failureRecoveryFixture, workflowID, candidateID uuid.UUID, verdict string, score int, reasons string) {
	t.Helper()
	if _, err := f.store.Pool.Exec(f.ctx, `INSERT INTO candidate_decisions(workflow_id,candidate_id,policy_version,verdict,score,reasons,actor,decided_at) VALUES($1,$2,1,$3,$4,$5,'policy',now())`, workflowID.String(), candidateID.String(), verdict, score, reasons); err != nil {
		t.Fatal(err)
	}
}

const explainObservedAt = "2026-01-02T03:04:05Z"

var explainArrayFacts = `[{"name":"resolution","values":["1080p"],"state":"observed","evidence_at":[0],"observed_at":"` + explainObservedAt + `"},{"name":"subtitle_languages","values":["da"],"state":"observed","evidence_at":[1],"observed_at":"` + explainObservedAt + `"}]`

// The first reason is a legacy row (no rule_kind/category); the second is a
// current row. The explanation must derive a category for the first without
// inventing its hardness.
const explainMixedReasons = `[{"rule":"Prefer 1080p","fact":"resolution","decision":"pass","values":["1080p"],"score":42,"reason":"observed evidence satisfies the rule"},{"rule":"Required Danish subtitles","fact":"subtitle_languages","decision":"pass","values":["da"],"score":0,"reason":"observed evidence satisfies the rule","rule_kind":"required","category":"danish_language","evidence_state":"observed"}]`

func TestCandidateReadModelsAttachDecisionExplanation(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	w := f.newMovieWorkflow(t, "Explain Read Model", "930001")
	now := time.Now().UTC().Truncate(time.Second)
	decided := seedExplainCandidate(t, f, w.ID, "decided", explainArrayFacts, now)
	legacy := seedExplainCandidate(t, f, w.ID, "legacy", `{}`, now.Add(-time.Hour))
	undecided := seedExplainCandidate(t, f, w.ID, "undecided", explainArrayFacts, now.Add(-2*time.Hour))
	seedExplainDecision(t, f, w.ID, decided, "eligible", 42, explainMixedReasons)
	seedExplainDecision(t, f, w.ID, legacy, "held", 0, `[]`)

	checkDecided := func(t *testing.T, label string, explanation *releases.DecisionExplanation) {
		t.Helper()
		if explanation == nil {
			t.Fatalf("%s: explanation is missing for a decided candidate", label)
		}
		if explanation.Version != 1 || explanation.Verdict != releases.VerdictEligible || explanation.Score != 42 || explanation.EvidenceScope != releases.CurrentLinkedObservationEvidenceScope {
			t.Fatalf("%s: explanation header=%+v", label, explanation)
		}
		if len(explanation.Reasons) != 2 {
			t.Fatalf("%s: reasons=%+v, want 2", label, explanation.Reasons)
		}
		legacyReason, currentReason := explanation.Reasons[0], explanation.Reasons[1]
		if legacyReason.RuleKind != "" || legacyReason.Category != releases.CategoryResolutionCodec {
			t.Fatalf("%s: legacy reason must keep an unrecorded rule kind and a derived category, got %+v", label, legacyReason)
		}
		if currentReason.RuleKind != releases.RuleRequired || currentReason.Category != releases.CategoryDanishLanguage {
			t.Fatalf("%s: current reason=%+v", label, currentReason)
		}
		if len(explanation.Evidence) != 2 || explanation.Evidence[0].Fact != "resolution" || explanation.Evidence[1].Fact != "subtitle_languages" || explanation.Evidence[1].Category != releases.CategoryDanishLanguage {
			t.Fatalf("%s: evidence=%+v", label, explanation.Evidence)
		}
		if explanation.Evidence[0].ObservedAt == nil || explanation.Evidence[0].ObservedAt.Format(time.RFC3339) != explainObservedAt {
			t.Fatalf("%s: evidence observed_at=%v, want %s", label, explanation.Evidence[0].ObservedAt, explainObservedAt)
		}
	}
	checkLegacy := func(t *testing.T, label string, explanation *releases.DecisionExplanation) {
		t.Helper()
		if explanation == nil {
			t.Fatalf("%s: a decided candidate with legacy facts must still carry an explanation", label)
		}
		if len(explanation.Evidence) != 0 || len(explanation.Reasons) != 0 {
			t.Fatalf("%s: legacy empty facts/reasons must not produce invented evidence, got %+v", label, explanation)
		}
	}

	t.Run("workflow candidates", func(t *testing.T) {
		items, err := f.store.WorkflowCandidates(f.ctx, w.ID, "", 50)
		if err != nil {
			t.Fatal(err)
		}
		byID := map[uuid.UUID]WorkflowCandidateView{}
		for _, item := range items {
			byID[item.ID] = item
		}
		if len(byID) != 3 {
			t.Fatalf("candidates=%d, want 3", len(byID))
		}
		if byID[decided].Decision == nil {
			t.Fatal("decided candidate lost its decision")
		}
		checkDecided(t, "decided", byID[decided].Decision.DecisionExplanation)
		// The stored reasons stay byte-for-byte meaningful: the explanation is
		// additive and never replaces the persisted JSON.
		var stored, expected any
		if err := json.Unmarshal(byID[decided].Decision.Reasons, &stored); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(explainMixedReasons), &expected); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(stored, expected) {
			t.Fatalf("stored reasons changed: %s", byID[decided].Decision.Reasons)
		}
		if byID[legacy].Decision == nil {
			t.Fatal("legacy candidate lost its decision")
		}
		checkLegacy(t, "legacy", byID[legacy].Decision.DecisionExplanation)
		if byID[undecided].Decision != nil {
			t.Fatalf("an undecided candidate must not receive an invented decision: %+v", byID[undecided].Decision)
		}
	})

	t.Run("release candidates list", func(t *testing.T) {
		items, err := f.store.ListReleaseCandidates(f.ctx, 50)
		if err != nil {
			t.Fatal(err)
		}
		byID := map[uuid.UUID]ReleaseCandidateSummary{}
		for _, item := range items {
			byID[item.ID] = item
		}
		if len(byID) != 3 {
			t.Fatalf("candidates=%d, want 3", len(byID))
		}
		checkDecided(t, "decided", byID[decided].DecisionExplanation)
		checkLegacy(t, "legacy", byID[legacy].DecisionExplanation)
		if byID[undecided].Verdict != "" || byID[undecided].DecisionExplanation != nil {
			t.Fatalf("undecided candidate verdict=%q explanation=%+v, want none", byID[undecided].Verdict, byID[undecided].DecisionExplanation)
		}
	})
}

func TestWorkflowCandidateObservationsLoadsStoredEvidenceReadOnly(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	w := f.newMovieWorkflow(t, "Dry Run Loader", "930002")
	now := time.Now().UTC().Truncate(time.Second)
	oldest := seedExplainCandidate(t, f, w.ID, "oldest", explainArrayFacts, now.Add(-2*time.Hour))
	middle := seedExplainCandidate(t, f, w.ID, "middle", `{}`, now.Add(-time.Hour))
	newest := seedExplainCandidate(t, f, w.ID, "newest", explainArrayFacts, now)

	// Selected: mirrors seedRetrievalCandidate's direct workflow update.
	if _, err := f.store.Pool.Exec(f.ctx, `UPDATE workflows SET state='candidate_selected',selected_release_id=$2,revision=revision+1,updated_at=now() WHERE id=$1`, w.ID.String(), newest.String()); err != nil {
		t.Fatal(err)
	}
	// Attempted: an AltMount job and its first-attempt marker for the middle candidate.
	jobID := uuid.NewV7()
	var roundID string
	if err := f.store.Pool.QueryRow(f.ctx, `SELECT id::text FROM acquisition_rounds WHERE workflow_id=$1 ORDER BY round_ordinal LIMIT 1`, w.ID.String()).Scan(&roundID); err != nil {
		t.Fatalf("a new workflow must already have an acquisition round: %v", err)
	}
	if _, err := f.store.Pool.Exec(f.ctx, `INSERT INTO altmount_jobs(id,workflow_id,candidate_id,altmount_instance_id,attempt_generation,upload_name,state,submission_started_at,revision,round_id,round_slot,acquisition_outcome) VALUES($1,$2,$3,$4,1,'dryrun-middle.nzb','needs_attention',now(),1,$5,1,'output_empty')`, jobID.String(), w.ID.String(), middle.String(), f.altMount.ID.String(), roundID); err != nil {
		t.Fatalf("seed altmount job: %v", err)
	}
	if _, err := f.store.Pool.Exec(f.ctx, `INSERT INTO workflow_attempted_releases(workflow_id,candidate_id,first_job_id,created_at) VALUES($1,$2,$3,now())`, w.ID.String(), middle.String(), jobID.String()); err != nil {
		t.Fatalf("seed attempted release: %v", err)
	}

	snapshot := func() string {
		t.Helper()
		var text string
		err := f.store.Pool.QueryRow(f.ctx, `SELECT concat_ws('|',
			(SELECT count(*) FROM release_candidates),(SELECT count(*) FROM release_observations),
			(SELECT count(*) FROM candidate_decisions),(SELECT count(*) FROM workflow_release_exclusions),
			(SELECT count(*) FROM workflow_attempted_releases),(SELECT count(*) FROM search_runs),
			(SELECT count(*) FROM altmount_jobs),(SELECT revision FROM workflows WHERE id=$1),
			(SELECT state FROM workflows WHERE id=$1),(SELECT selected_release_id::text FROM workflows WHERE id=$1))`, w.ID.String()).Scan(&text)
		if err != nil {
			t.Fatal(err)
		}
		return text
	}
	before := snapshot()

	loaded, err := f.store.WorkflowCandidateObservations(f.ctx, w.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if after := snapshot(); after != before {
		t.Fatalf("the dry-run loader changed durable state:\nbefore=%s\nafter =%s", before, after)
	}
	if len(loaded) != 3 {
		t.Fatalf("loaded=%d, want 3", len(loaded))
	}
	wantOrder := []uuid.UUID{newest, middle, oldest}
	for index, want := range wantOrder {
		if loaded[index].CandidateID != want {
			t.Fatalf("position %d is %s, want %s (newest last_observed_at first)", index, loaded[index].CandidateID, want)
		}
	}
	byID := map[uuid.UUID]CandidateObservation{}
	for _, item := range loaded {
		byID[item.CandidateID] = item
	}
	if !byID[newest].Selected || byID[newest].Attempted || byID[newest].Excluded {
		t.Fatalf("newest flags=%+v, want selected only", byID[newest])
	}
	if byID[middle].Selected || !byID[middle].Attempted || byID[middle].Excluded {
		t.Fatalf("middle flags=%+v, want attempted only", byID[middle])
	}
	if byID[oldest].Selected || byID[oldest].Attempted || byID[oldest].Excluded {
		t.Fatalf("oldest flags=%+v, want none", byID[oldest])
	}
	if got := byID[newest].Observation; len(got.Facts) != 2 || got.Facts[0].Name != "resolution" || got.NativeGUID != "newest" || got.IndexerID != f.indexer.ID || byID[newest].IndexerKind != "newznab" {
		t.Fatalf("newest observation=%+v kind=%q", got, byID[newest].IndexerKind)
	}
	if byID[middle].Observation.Facts != nil && len(byID[middle].Observation.Facts) != 0 {
		t.Fatalf("legacy empty-object facts must load as no facts, got %+v", byID[middle].Observation.Facts)
	}

	limited, err := f.store.WorkflowCandidateObservations(f.ctx, w.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 2 || limited[0].CandidateID != newest || limited[1].CandidateID != middle {
		t.Fatalf("limited load=%+v, want the two newest candidates", limited)
	}

	if _, err := f.store.WorkflowCandidateObservations(f.ctx, uuid.NewV7(), 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown workflow error=%v, want ErrNotFound", err)
	}
}

// Malformed stored facts must fail closed: the loader never guesses facts from
// a shape it cannot read, because a guess would make the replay look conclusive.
func TestWorkflowCandidateObservationsRejectsMalformedStoredFacts(t *testing.T) {
	for name, facts := range map[string]string{
		"scalar":                  `"not facts"`,
		"non-empty legacy object": `{"resolution":"1080p"}`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFailureRecoveryFixture(t)
			w := f.newMovieWorkflow(t, "Dry Run Malformed", "930003")
			candidate := seedExplainCandidate(t, f, w.ID, "malformed", facts, time.Now().UTC())
			_, err := f.store.WorkflowCandidateObservations(f.ctx, w.ID, 10)
			if err == nil || errors.Is(err, ErrNotFound) {
				t.Fatalf("error=%v, want an invalid stored facts error", err)
			}
			if !strings.Contains(err.Error(), candidate.String()) {
				t.Fatalf("error %q does not name the candidate %s", err, candidate)
			}
		})
	}
}

// An excluded candidate must be reported as excluded so the dry run never
// proposes a release the owner or the pipeline has already ruled out.
func TestWorkflowCandidateObservationsMarksExcludedCandidates(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	w := f.newMovieWorkflow(t, "Dry Run Excluded", "930004")
	retrieval := seedRetrievalCandidate(t, f.ctx, f.store, w.ID, f.indexer.ID, "excluded-release")
	seedAccessRetryDecision(t, f, retrieval)
	if err := f.store.RecordReleaseRetrievalFailure(f.ctx, retrieval, "newznab_http_403", "access denied", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	loaded, err := f.store.WorkflowCandidateObservations(f.ctx, w.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].CandidateID != retrieval.CandidateID || !loaded[0].Excluded {
		t.Fatalf("loaded=%+v, want the single candidate flagged excluded", loaded)
	}
}

// The Go writer stores json.Marshal of an empty fact slice, which is JSON null.
// That is "no facts", never corruption: both the replay loader and the owner read
// model must accept it without inventing evidence.
func TestStoredJSONNullFactsAreNoFactsNotCorruption(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	w := f.newMovieWorkflow(t, "Null Facts", "930005")
	candidate := seedExplainCandidate(t, f, w.ID, "null-facts", `null`, time.Now().UTC())
	seedExplainDecision(t, f, w.ID, candidate, "held", 0, `[]`)

	loaded, err := f.store.WorkflowCandidateObservations(f.ctx, w.ID, 10)
	if err != nil {
		t.Fatalf("JSON null facts must load: %v", err)
	}
	if len(loaded) != 1 || len(loaded[0].Observation.Facts) != 0 {
		t.Fatalf("loaded=%+v, want one candidate with no facts", loaded)
	}
	items, err := f.store.WorkflowCandidates(f.ctx, w.ID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Decision == nil || items[0].Decision.DecisionExplanation == nil || len(items[0].Decision.DecisionExplanation.Evidence) != 0 {
		t.Fatalf("items=%+v, want an explanation without invented evidence", items)
	}
}
