package acquisition

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
	"uuid"
)

type fakeDryRunStore struct {
	search         postgres.SearchContext
	candidates     []postgres.CandidateObservation
	loadCalls      int
	candidateCalls int
	candidateLimit int
}

func (store *fakeDryRunStore) LoadSearchContext(context.Context, uuid.UUID) (postgres.SearchContext, error) {
	store.loadCalls++
	return store.search, nil
}

func (store *fakeDryRunStore) WorkflowCandidateObservations(_ context.Context, _ uuid.UUID, limit int) ([]postgres.CandidateObservation, error) {
	store.candidateCalls++
	store.candidateLimit = limit
	return store.candidates, nil
}

func TestDryRunWorkflowReplaysStoredObservationsWithoutSideEffects(t *testing.T) {
	workflowID, mediaID := uuid.NewV7(), uuid.NewV7()
	indexerA, indexerB, indexerC, indexerD, indexerE, indexerF := uuid.NewV7(), uuid.NewV7(), uuid.NewV7(), uuid.NewV7(), uuid.NewV7(), uuid.NewV7()
	candidateA, candidateB, candidateC, candidateD, candidateE, candidateF := uuid.NewV7(), uuid.NewV7(), uuid.NewV7(), uuid.NewV7(), uuid.NewV7(), uuid.NewV7()
	observationA, observationB, observationC, observationD, observationE, observationF := uuid.NewV7(), uuid.NewV7(), uuid.NewV7(), uuid.NewV7(), uuid.NewV7(), uuid.NewV7()
	year := uint16(2020)
	search := postgres.SearchContext{
		Media:                catalog.MediaItem{ID: mediaID, Kind: catalog.KindMovie, Title: "Example", Year: &year},
		PolicyVersion:        1,
		ProfilePolicyVersion: 1,
		Policy: json.RawMessage(`{"rules":[
			{"name":"Required resolution","fact":"resolution","allowed_values":["1080p","2160p"],"kind":"required","unknown":"hold"},
			{"name":"Prefer 2160p","fact":"resolution","allowed_values":["2160p"],"kind":"preferred","weight":100,"unknown":"allow"}
		]}`),
		Exclusions: []postgres.WorkflowReleaseExclusion{{
			CandidateID:        candidateC,
			IndexerInstanceID:  indexerC,
			NativeKey:          "guid-c",
			FailureKind:        "subtitle_verification",
			Reason:             "required language was not verified",
			ReleaseTitle:       "Example.2020.2160p.WEB-DL",
			SubtitleUnverified: true,
		}},
	}
	store := &fakeDryRunStore{
		search: search,
		candidates: []postgres.CandidateObservation{
			dryRunCandidate(candidateA, observationA, indexerA, "Example.2020.1080p.WEB-DL", "guid-a", "1080p", false, false),
			dryRunCandidate(candidateB, observationB, indexerB, "Example.2020.2160p.WEB-DL-GROUPB", "guid-b", "2160p", true, false),
			dryRunCandidate(candidateC, observationC, indexerC, "Example.2020.2160p.WEB-DL", "guid-c", "2160p", false, true),
			dryRunCandidate(candidateD, observationD, indexerD, "Example.2020.Unknown", "guid-d", "", false, false),
			dryRunCandidate(candidateE, observationE, indexerE, "Other.2020.1080p.WEB-DL", "guid-e", "1080p", false, false),
			selectedCandidate(candidateF, observationF, indexerF, "Example.2020.2160p.WEB-DL-GROUPC", "guid-f", "2160p"),
		},
	}
	originalCandidates := append([]postgres.CandidateObservation(nil), store.candidates...)

	result, err := DryRunWorkflow(context.Background(), store, workflowID, 200)
	if err != nil {
		t.Fatal(err)
	}
	if store.loadCalls != 1 || store.candidateCalls != 1 {
		t.Fatalf("store calls load=%d candidates=%d, want 1/1", store.loadCalls, store.candidateCalls)
	}
	if result.WorkflowID != workflowID || result.PolicyVersion != 1 || result.ProfilePolicyVersion != 1 || result.PolicySource != "profile" || result.GeneratedAt.IsZero() || result.Truncated {
		t.Fatalf("result metadata=%+v", result)
	}
	if len(result.Candidates) != 6 || len(result.RankedCandidateIDs) != 6 {
		t.Fatalf("candidates=%d ranked=%d, want 6/6", len(result.Candidates), len(result.RankedCandidateIDs))
	}
	// The attempted and already-selected 2160p candidates rank first by score
	// but must not be named as the best eligible replacement.
	topTwo := map[uuid.UUID]bool{result.RankedCandidateIDs[0]: true, result.RankedCandidateIDs[1]: true}
	if !topTwo[candidateB] || !topTwo[candidateF] || result.RankedCandidateIDs[2] != candidateA {
		t.Fatalf("ranking=%v, want attempted/selected then best eligible", result.RankedCandidateIDs)
	}
	if result.BestEligibleCandidateID == nil || *result.BestEligibleCandidateID != candidateA {
		t.Fatalf("best eligible=%v, want %s", result.BestEligibleCandidateID, candidateA)
	}
	byID := map[uuid.UUID]DryRunCandidate{}
	for _, candidate := range result.Candidates {
		byID[candidate.CandidateID] = candidate
	}
	a := byID[candidateA]
	if a.Evaluation.Verdict != releases.VerdictEligible || a.Explanation.Verdict != releases.VerdictEligible || a.Attempted || a.Excluded {
		t.Fatalf("candidate A=%+v", a)
	}
	if a.Explanation.EvidenceScope != releases.DryRunReplayEvidenceScope {
		t.Fatalf("dry-run evidence scope=%q, want %q", a.Explanation.EvidenceScope, releases.DryRunReplayEvidenceScope)
	}
	foundRequired := false
	for _, reason := range a.Explanation.Reasons {
		if reason.Rule == "Required resolution" {
			foundRequired = true
			if reason.RuleKind != releases.RuleRequired || reason.Category != releases.CategoryResolutionCodec || reason.EvidenceState != releases.FactObserved {
				t.Fatalf("required reason=%+v", reason)
			}
		}
	}
	if !foundRequired {
		t.Fatal("explanation omitted the required resolution rule")
	}
	if len(a.Explanation.Evidence) == 0 || a.Explanation.Evidence[0].Fact != "resolution" || a.Explanation.Evidence[0].State != releases.FactObserved {
		t.Fatalf("evidence=%+v", a.Explanation.Evidence)
	}
	d := byID[candidateD]
	if d.Evaluation.Verdict != releases.VerdictHeld {
		t.Fatalf("unknown resolution verdict=%s, want held", d.Evaluation.Verdict)
	}
	for _, reason := range d.Explanation.Reasons {
		if reason.Fact == "resolution" && reason.EvidenceState != releases.FactUnknown {
			t.Fatalf("unknown evidence was invented: %+v", reason)
		}
	}
	if len(d.Explanation.Evidence) != 0 {
		t.Fatalf("unknown candidate fabricated evidence: %+v", d.Explanation.Evidence)
	}
	c := byID[candidateC]
	if c.Evaluation.Verdict != releases.VerdictRejected || !c.Excluded {
		t.Fatalf("excluded candidate=%+v", c)
	}
	e := byID[candidateE]
	if e.Evaluation.Verdict != releases.VerdictRejected {
		t.Fatalf("wrong-title candidate verdict=%s, want rejected", e.Evaluation.Verdict)
	}
	if len(result.Limitations) == 0 || !strings.Contains(strings.Join(result.Limitations, " "), "no indexer search") {
		t.Fatalf("limitations=%v", result.Limitations)
	}
	for index, candidate := range store.candidates {
		original := originalCandidates[index]
		if candidate.Attempted != original.Attempted || candidate.Excluded != original.Excluded || candidate.Selected != original.Selected {
			t.Fatalf("dry run mutated candidate %d state", index)
		}
	}
}

func TestDryRunWorkflowUsesDanishSubtitleRanking(t *testing.T) {
	workflowID, mediaID := uuid.NewV7(), uuid.NewV7()
	year := uint16(2020)
	candidateA, candidateB := uuid.NewV7(), uuid.NewV7()
	indexerA, indexerB := uuid.NewV7(), uuid.NewV7()
	hevc := postgres.CandidateObservation{
		CandidateID: candidateA,
		Observation: releases.Observation{
			ID:           uuid.NewV7(),
			IndexerID:    indexerA,
			NativeGUID:   "hevc",
			Title:        "Example.2020.1080p.HEVC.WEB-DL",
			Facts:        []releases.Fact{{Name: "resolution", Values: []string{"1080p"}, State: releases.FactObserved}, {Name: "video_codec", Values: []string{"hevc"}, State: releases.FactObserved}},
			ParseVersion: "newznab-v1",
		},
		IndexerKind: "newznab",
	}
	nordic := postgres.CandidateObservation{
		CandidateID: candidateB,
		Observation: releases.Observation{
			ID:           uuid.NewV7(),
			IndexerID:    indexerB,
			NativeGUID:   "nordic",
			Title:        "Example.2020.1080p.NORDIC.WEB-DL.AVC",
			Facts:        []releases.Fact{{Name: "resolution", Values: []string{"1080p"}, State: releases.FactObserved}, {Name: "video_codec", Values: []string{"avc"}, State: releases.FactObserved}},
			ParseVersion: "newznab-v1",
		},
		IndexerKind: "newznab",
	}
	store := &fakeDryRunStore{
		search: postgres.SearchContext{
			Media:                catalog.MediaItem{ID: mediaID, Kind: catalog.KindMovie, Title: "Example", Year: &year},
			PolicyVersion:        1,
			ProfilePolicyVersion: 1,
			Policy: json.RawMessage(`{
				"rules":[
					{"name":"Required resolution","fact":"resolution","allowed_values":["1080p"],"kind":"required","unknown":"hold"},
					{"name":"Prefer HEVC","fact":"video_codec","allowed_values":["hevc"],"kind":"preferred","weight":100,"unknown":"allow"}
				],
				"required_subtitle_languages":["da"]
			}`),
		},
		candidates: []postgres.CandidateObservation{hevc, nordic},
	}
	result, err := DryRunWorkflow(context.Background(), store, workflowID, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.RankedCandidateIDs) != 2 || result.RankedCandidateIDs[0] != candidateB {
		t.Fatalf("ranking=%v, want the broad Nordic clue first under Danish-subtitle ranking", result.RankedCandidateIDs)
	}
	if result.BestEligibleCandidateID == nil || *result.BestEligibleCandidateID != candidateB {
		t.Fatalf("best eligible=%v, want %s", result.BestEligibleCandidateID, candidateB)
	}
}

func TestDryRunPolicySource(t *testing.T) {
	if got := dryRunPolicySource(postgres.SearchContext{OpenRound: &postgres.AcquisitionRound{}}); got != "open_round" {
		t.Fatalf("open round source=%q", got)
	}
	if got := dryRunPolicySource(postgres.SearchContext{SearchRoundID: uuid.NewV7()}); got != "discovery_round" {
		t.Fatalf("discovery source=%q", got)
	}
	if got := dryRunPolicySource(postgres.SearchContext{}); got != "profile" {
		t.Fatalf("profile source=%q", got)
	}
}

func TestDryRunWorkflowReplaysUpgradeBaseline(t *testing.T) {
	workflowID, mediaID := uuid.NewV7(), uuid.NewV7()
	indexerCurrent, indexerBetter := uuid.NewV7(), uuid.NewV7()
	candidateCurrent, candidateBetter := uuid.NewV7(), uuid.NewV7()
	observationCurrent, observationBetter := uuid.NewV7(), uuid.NewV7()
	year := uint16(2020)
	current := releases.Observation{
		ID:           observationCurrent,
		IndexerID:    indexerCurrent,
		NativeGUID:   "current-guid",
		Title:        "Example.2020.1080p.WEB-DL",
		Facts:        []releases.Fact{{Name: "resolution", Values: []string{"1080p"}, State: releases.FactObserved}},
		ParseVersion: "newznab-v1",
	}
	store := &fakeDryRunStore{
		search: postgres.SearchContext{
			Media:                catalog.MediaItem{ID: mediaID, Kind: catalog.KindMovie, Title: "Example", Year: &year},
			PolicyVersion:        1,
			ProfilePolicyVersion: 1,
			Policy: json.RawMessage(`{"rules":[
				{"name":"Required resolution","fact":"resolution","allowed_values":["1080p","2160p"],"kind":"required","unknown":"hold"},
				{"name":"Prefer 2160p","fact":"resolution","allowed_values":["2160p"],"kind":"preferred","weight":100,"unknown":"allow"}
			]}`),
			CurrentRelease: &current,
		},
		candidates: []postgres.CandidateObservation{
			selectedCandidate(candidateCurrent, observationCurrent, indexerCurrent, current.Title, current.NativeGUID, "1080p"),
			dryRunCandidate(candidateBetter, observationBetter, indexerBetter, "Example.2020.2160p.WEB-DL-BETTER", "better-guid", "2160p", false, false),
		},
	}
	result, err := DryRunWorkflow(context.Background(), store, workflowID, 200)
	if err != nil {
		t.Fatal(err)
	}
	if result.BestEligibleCandidateID == nil || *result.BestEligibleCandidateID != candidateBetter {
		t.Fatalf("best eligible=%v, want better candidate", result.BestEligibleCandidateID)
	}
	byID := map[uuid.UUID]DryRunCandidate{}
	for _, candidate := range result.Candidates {
		byID[candidate.CandidateID] = candidate
	}
	currentResult := byID[candidateCurrent]
	if currentResult.Evaluation.Verdict != releases.VerdictRejected {
		t.Fatalf("current release verdict=%s, want rejected", currentResult.Evaluation.Verdict)
	}
	foundUpgrade := false
	for _, reason := range currentResult.Evaluation.Explanations {
		if reason.Rule == "upgrade improvement" {
			foundUpgrade = true
		}
	}
	if !foundUpgrade {
		t.Fatal("current release was not rejected by the upgrade baseline")
	}
}

func TestDryRunWorkflowMarksTruncatedReplay(t *testing.T) {
	workflowID, mediaID := uuid.NewV7(), uuid.NewV7()
	year := uint16(2020)
	store := &fakeDryRunStore{
		search: postgres.SearchContext{
			Media:                catalog.MediaItem{ID: mediaID, Kind: catalog.KindMovie, Title: "Example", Year: &year},
			PolicyVersion:        1,
			ProfilePolicyVersion: 1,
			Policy:               json.RawMessage(`{"rules":[{"name":"Required resolution","fact":"resolution","allowed_values":["1080p"],"kind":"required","unknown":"hold"}]}`),
		},
		candidates: []postgres.CandidateObservation{
			dryRunCandidate(uuid.NewV7(), uuid.NewV7(), uuid.NewV7(), "Example.2020.1080p.WEB-DL-A", "guid-a", "1080p", false, false),
			dryRunCandidate(uuid.NewV7(), uuid.NewV7(), uuid.NewV7(), "Example.2020.1080p.WEB-DL-B", "guid-b", "1080p", false, false),
			dryRunCandidate(uuid.NewV7(), uuid.NewV7(), uuid.NewV7(), "Example.2020.1080p.WEB-DL-C", "guid-c", "1080p", false, false),
		},
	}
	result, err := DryRunWorkflow(context.Background(), store, workflowID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Truncated || len(result.Candidates) != 2 || len(result.RankedCandidateIDs) != 2 {
		t.Fatalf("truncated=%v candidates=%d ranked=%d", result.Truncated, len(result.Candidates), len(result.RankedCandidateIDs))
	}
	if !strings.Contains(strings.Join(result.Limitations, " "), "Only the most recently observed") {
		t.Fatalf("limitations=%v", result.Limitations)
	}
	if store.candidateLimit != 3 {
		t.Fatalf("store limit=%d, want 3 (limit+1)", store.candidateLimit)
	}
	if len(result.Candidates) != 2 {
		t.Fatalf("kept candidates=%d, want 2", len(result.Candidates))
	}
}

func minimalDryRunSearch() postgres.SearchContext {
	year := uint16(2020)
	return postgres.SearchContext{
		Media:                catalog.MediaItem{ID: uuid.NewV7(), Kind: catalog.KindMovie, Title: "Example", Year: &year},
		PolicyVersion:        1,
		ProfilePolicyVersion: 1,
		Policy:               json.RawMessage(`{"rules":[{"name":"Required resolution","fact":"resolution","allowed_values":["1080p"],"kind":"required","unknown":"hold"}]}`),
	}
}

// A release that is eligible on paper but already attempted or selected must
// never be proposed again; when nothing else qualifies the answer is "none",
// not the blocked release.
func TestDryRunWorkflowHasNoBestEligibleWhenEveryEligibleCandidateIsBlocked(t *testing.T) {
	store := &fakeDryRunStore{
		search: minimalDryRunSearch(),
		candidates: []postgres.CandidateObservation{
			dryRunCandidate(uuid.NewV7(), uuid.NewV7(), uuid.NewV7(), "Example.2020.1080p.WEB-DL-A", "guid-a", "1080p", true, false),
			selectedCandidate(uuid.NewV7(), uuid.NewV7(), uuid.NewV7(), "Example.2020.1080p.WEB-DL-B", "guid-b", "1080p"),
		},
	}
	result, err := DryRunWorkflow(context.Background(), store, uuid.NewV7(), 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range result.Candidates {
		if candidate.Evaluation.Verdict != releases.VerdictEligible {
			t.Fatalf("%s verdict=%s; the scenario needs both candidates eligible so only blocking can explain the result", candidate.Title, candidate.Evaluation.Verdict)
		}
	}
	if result.BestEligibleCandidateID != nil {
		t.Fatalf("best eligible=%s, want none because every eligible candidate is attempted or selected", result.BestEligibleCandidateID)
	}
	if len(result.RankedCandidateIDs) != 2 {
		t.Fatalf("ranked=%d, want both candidates still ranked and explained", len(result.RankedCandidateIDs))
	}
}

// Ranking is keyed by observation. Two candidates sharing one would make the
// ranked order ambiguous, so the replay refuses instead of guessing.
func TestDryRunWorkflowRefusesCandidatesSharingOneObservation(t *testing.T) {
	shared := uuid.NewV7()
	store := &fakeDryRunStore{
		search: minimalDryRunSearch(),
		candidates: []postgres.CandidateObservation{
			dryRunCandidate(uuid.NewV7(), shared, uuid.NewV7(), "Example.2020.1080p.WEB-DL-A", "guid-a", "1080p", false, false),
			dryRunCandidate(uuid.NewV7(), shared, uuid.NewV7(), "Example.2020.1080p.WEB-DL-B", "guid-b", "1080p", false, false),
		},
	}
	_, err := DryRunWorkflow(context.Background(), store, uuid.NewV7(), 10)
	if err == nil || !strings.Contains(err.Error(), "share one stored observation") {
		t.Fatalf("error=%v, want the ambiguous-observation refusal", err)
	}
}

// The caller-supplied limit is clamped to the replay maximum, and the store is
// always asked for one extra row so truncation can be detected honestly.
func TestDryRunWorkflowClampsTheCandidateLimit(t *testing.T) {
	for requested, want := range map[int]int{-5: dryRunCandidateLimit + 1, 0: dryRunCandidateLimit + 1, dryRunCandidateLimit + 1: dryRunCandidateLimit + 1, 100000: dryRunCandidateLimit + 1, 1: 2, 5: 6, dryRunCandidateLimit: dryRunCandidateLimit + 1} {
		store := &fakeDryRunStore{search: minimalDryRunSearch()}
		if _, err := DryRunWorkflow(context.Background(), store, uuid.NewV7(), requested); err != nil {
			t.Fatal(err)
		}
		if store.candidateLimit != want {
			t.Fatalf("requested limit %d asked the store for %d rows, want %d", requested, store.candidateLimit, want)
		}
	}
}

func selectedCandidate(candidateID, observationID, indexerID uuid.UUID, title, guid, resolution string) postgres.CandidateObservation {
	candidate := dryRunCandidate(candidateID, observationID, indexerID, title, guid, resolution, false, false)
	candidate.Selected = true
	return candidate
}

func dryRunCandidate(candidateID, observationID, indexerID uuid.UUID, title, guid, resolution string, attempted, excluded bool) postgres.CandidateObservation {
	facts := []releases.Fact{}
	if resolution != "" {
		facts = append(facts, releases.Fact{Name: "resolution", Values: []string{resolution}, State: releases.FactObserved})
	}
	return postgres.CandidateObservation{
		CandidateID: candidateID,
		Observation: releases.Observation{
			ID:           observationID,
			IndexerID:    indexerID,
			NativeGUID:   guid,
			Title:        title,
			Facts:        facts,
			ParseVersion: "newznab-v1",
		},
		IndexerKind: "newznab",
		Attempted:   attempted,
		Excluded:    excluded,
	}
}
