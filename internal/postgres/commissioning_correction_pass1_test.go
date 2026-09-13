package postgres

import (
	"strings"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/commissioningv2"
	"github.com/media-manager/media-manager/internal/indexers"
	"uuid"
)

// TestReservationLockIdentitySharedAcrossSpellings proves the single shared
// advisory-lock namespace: equivalent UUID spellings produce the same
// lowercase uuid.String() identity with the same literal prefix, and the
// reservation table semantics are unchanged (no second lock namespace).
func TestReservationLockIdentitySharedAcrossSpellings(t *testing.T) {
	t.Parallel()
	lower := "550e8400-e29b-41d4-a716-446655440000"
	spellings := map[string]string{
		"lower":  lower,
		"upper":  "550E8400-E29B-41D4-A716-446655440000",
		"urn":    "urn:uuid:550e8400-e29b-41d4-a716-446655440000",
		"braced": "{550e8400-e29b-41d4-a716-446655440000}",
	}
	for name, spelling := range spellings {
		if got := ReservationLockKeyForTest(spelling); got != "indexer-request-budget:"+lower {
			t.Fatalf("%s lock key=%q, want canonical", name, got)
		}
	}
	// Non-UUID IDs preserve existing literal behavior rather than rejecting.
	if got := ReservationLockKeyForTest("legacy-indexer-1"); got != "indexer-request-budget:legacy-indexer-1" {
		t.Fatalf("non-UUID lock key changed: %q", got)
	}
	// Sanity: uuid.String() is the canonical lowercase form.
	parsed, err := uuid.Parse(spellings["upper"])
	if err != nil || parsed.String() != lower {
		t.Fatalf("uuid canonicalization broken: %v %q", parsed, lower)
	}
}

// TestC56RealProductionPredicates is the C-56 production-predicate arm. The
// commissioningv2 classification matrix test restates the RecordResult and
// (SafeOutcome, SafeCode) validity shapes locally because importing package
// postgres would create an import cycle. This test closes that gap by
// exercising the REAL predicates CompleteLiveAttempt enforces
// (validCommissioningRecordResult, commissioningStepSafeCodeMatches,
// allowlistedGateOutcome) directly, and by feeding real
// commissioningv2.ClassifyLiveAttempt outputs through them. If a production
// predicate drifts, this test fails even though the local mirror cannot see
// it.
func TestC56RealProductionPredicates(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_100, 0).UTC()

	// Real RecordResult validity predicate over the exact accepted/rejected
	// matrix the scheduler contract permits.
	validResults := []indexers.RecordResult{
		{Outcome: indexers.OutcomeSuccess, Scope: indexers.FailureScopeNone},
		{Outcome: indexers.OutcomeNoResults, Scope: indexers.FailureScopeNone},
		{Outcome: indexers.OutcomeCancelled, Scope: indexers.FailureScopeNone},
		{Outcome: indexers.OutcomeRateLimited, Scope: indexers.FailureScopeInstance},
		{Outcome: indexers.OutcomeRateLimited, Scope: indexers.FailureScopeInstance, RetryAt: now.Add(time.Minute)},
		{Outcome: indexers.OutcomeAuthFailed, Scope: indexers.FailureScopeInstance},
		{Outcome: indexers.OutcomeSystemFailure, Scope: indexers.FailureScopeInstance},
		{Outcome: indexers.OutcomeSystemFailure, Scope: indexers.FailureScopeOperation},
	}
	for _, result := range validResults {
		if !validCommissioningRecordResult(result) {
			t.Fatalf("real predicate rejected valid RecordResult %#v", result)
		}
	}
	invalidResults := []indexers.RecordResult{
		{},
		{Outcome: indexers.OutcomeSuccess, Scope: indexers.FailureScopeInstance},
		{Outcome: indexers.OutcomeRateLimited, Scope: indexers.FailureScopeOperation},
		{Outcome: indexers.OutcomeRateLimited, Scope: indexers.FailureScopeNone},
		{Outcome: indexers.OutcomeAuthFailed, Scope: indexers.FailureScopeInstance, RetryAt: now.Add(time.Minute)},
		{Outcome: indexers.OutcomeSystemFailure, Scope: indexers.FailureScopeNone},
		{Outcome: indexers.OutcomeSystemFailure, Scope: indexers.FailureScopeInstance, RetryAt: now.Add(time.Minute)},
		{Outcome: "bogus", Scope: indexers.FailureScopeNone},
	}
	for _, result := range invalidResults {
		if validCommissioningRecordResult(result) {
			t.Fatalf("real predicate accepted invalid RecordResult %#v", result)
		}
	}

	// Real (SafeOutcome, SafeCode) pairing predicate.
	matching := []struct {
		outcome commissioningv2.SafeOutcome
		code    commissioningv2.SafeCode
	}{
		{commissioningv2.OutcomeCAPSObserved, ""},
		{commissioningv2.OutcomeRateLimited, ""},
		{commissioningv2.OutcomeDNSFailure, ""},
		{commissioningv2.OutcomeConfigurationStale, commissioningv2.SafeCodeConfigurationChanged},
		{commissioningv2.OutcomeCancelled, commissioningv2.SafeCodeOwnerCancelled},
		{commissioningv2.OutcomeSafetyCredentialEcho, commissioningv2.SafeCodeCredentialEcho},
		{commissioningv2.OutcomeIndeterminate, commissioningv2.SafeCodeIndeterminateAttempt},
	}
	for _, row := range matching {
		if !commissioningStepSafeCodeMatches(row.outcome, row.code) {
			t.Fatalf("real predicate rejected valid pairing (%q,%q)", row.outcome, row.code)
		}
	}
	mismatching := []struct {
		outcome commissioningv2.SafeOutcome
		code    commissioningv2.SafeCode
	}{
		{commissioningv2.OutcomeCAPSObserved, commissioningv2.SafeCodeConfigurationChanged},
		{commissioningv2.OutcomeRateLimited, commissioningv2.SafeCodeCredentialEcho},
		{commissioningv2.OutcomeConfigurationStale, commissioningv2.SafeCodeCredentialEcho},
		{commissioningv2.OutcomeSafetyCredentialEcho, commissioningv2.SafeCodeConfigurationChanged},
		{commissioningv2.OutcomeCancelled, commissioningv2.SafeCodeIndeterminateAttempt},
		{commissioningv2.OutcomeIndeterminate, commissioningv2.SafeCodeOwnerCancelled},
		{commissioningv2.OutcomeDNSFailure, commissioningv2.SafeCodeCredentialEcho},
		{commissioningv2.OutcomeCAPSObserved, commissioningv2.SafeCode("bogus.code")},
	}
	for _, row := range mismatching {
		if commissioningStepSafeCodeMatches(row.outcome, row.code) {
			t.Fatalf("real predicate accepted invalid pairing (%q,%q)", row.outcome, row.code)
		}
	}

	// Real 18-member gate allowlist.
	allowlist := []commissioningv2.SafeOutcome{
		commissioningv2.OutcomeCAPSObserved, commissioningv2.OutcomeRecipeVerified,
		commissioningv2.OutcomeNoResults, commissioningv2.OutcomeNoPlausibleMatch,
		commissioningv2.OutcomeUnsupported, commissioningv2.OutcomePermissionRestricted,
		commissioningv2.OutcomeContradicted, commissioningv2.OutcomeMalformedResponse,
		commissioningv2.OutcomeSemanticAmbiguity, commissioningv2.OutcomeRateLimited,
		commissioningv2.OutcomeQuotaExhausted, commissioningv2.OutcomeAuthenticationFailure,
		commissioningv2.OutcomeConfigurationStale, commissioningv2.OutcomeSafetyCredentialEcho,
		commissioningv2.OutcomeSafetyUnsafeRedirect, commissioningv2.OutcomeSafetyUnsafeText,
		commissioningv2.OutcomeSafetyBodyLimit, commissioningv2.OutcomeSafetyXMLLimit,
	}
	if len(allowlist) != 18 {
		t.Fatalf("C-56 allowlist has %d members, want 18", len(allowlist))
	}
	for _, outcome := range allowlist {
		if !allowlistedGateOutcome(outcome) {
			t.Fatalf("real allowlist rejected %q", outcome)
		}
	}
	for _, outcome := range []commissioningv2.SafeOutcome{
		commissioningv2.OutcomeRetryAfterDeferred, commissioningv2.OutcomeCredentialUnavailable,
		commissioningv2.OutcomeCancelled, commissioningv2.OutcomeIndeterminate,
		commissioningv2.OutcomeProviderConnectionFailure, commissioningv2.OutcomeDNSFailure,
		commissioningv2.OutcomeTLSFailure, commissioningv2.SafeOutcome("bogus"),
	} {
		if allowlistedGateOutcome(outcome) {
			t.Fatalf("real allowlist accepted retained/invalid outcome %q", outcome)
		}
	}

	// Feed REAL ClassifyLiveAttempt outputs (the no-evidence matrix rows)
	// through the real predicates so classification cannot emit a shape the
	// production completion gate rejects.
	noEvidenceResponses := []commissioningv2.ResponseVerdict{
		commissioningv2.ResponseMalformed, commissioningv2.ResponseSemanticAmbiguity,
		commissioningv2.ResponseRateLimited, commissioningv2.ResponseQuotaExhausted,
		commissioningv2.ResponseAuthenticationFailure,
		commissioningv2.ResponseSafetyCredentialEcho, commissioningv2.ResponseSafetyUnsafeRedirect,
		commissioningv2.ResponseSafetyUnsafeText, commissioningv2.ResponseSafetyBodyLimit,
		commissioningv2.ResponseSafetyXMLLimit,
	}
	for _, response := range noEvidenceResponses {
		classified, err := commissioningv2.ClassifyLiveAttempt(commissioningv2.LiveAttemptInputs{
			Transport:  commissioningv2.TransportResponseReceived,
			Response:   response,
			TargetKind: newznabCapsTarget(),
		})
		if err != nil {
			t.Fatalf("classify %q: %v", response, err)
		}
		if !validCommissioningRecordResult(classified.SchedulerResult) {
			t.Fatalf("classified %q produced a SchedulerResult the real predicate rejects: %#v", response, classified.SchedulerResult)
		}
		if !commissioningStepSafeCodeMatches(classified.SafeOutcome, classified.SafeCode) {
			t.Fatalf("classified %q produced a (outcome,code) pairing the real predicate rejects: (%q,%q)", response, classified.SafeOutcome, classified.SafeCode)
		}
		if classified.ClearGate != allowlistedGateOutcome(classified.SafeOutcome) {
			t.Fatalf("classified %q clear=%v but real allowlist=%v", response, classified.ClearGate, allowlistedGateOutcome(classified.SafeOutcome))
		}
	}
	for _, failure := range []commissioningv2.TransportFailure{
		commissioningv2.TransportFailureDNS,
		commissioningv2.TransportFailureTLS,
		commissioningv2.TransportFailureConnection,
	} {
		classified, err := commissioningv2.ClassifyLiveAttempt(commissioningv2.LiveAttemptInputs{
			Transport: commissioningv2.TransportNoCompleteResponse,
			Failure:   failure,
		})
		if err != nil {
			t.Fatalf("classify %q: %v", failure, err)
		}
		if !validCommissioningRecordResult(classified.SchedulerResult) {
			t.Fatalf("failure %q produced a SchedulerResult the real predicate rejects: %#v", failure, classified.SchedulerResult)
		}
		if classified.ClearGate || allowlistedGateOutcome(classified.SafeOutcome) {
			t.Fatalf("failure %q reported a clearable gate: clear=%v allowlist=%v", failure, classified.ClearGate, allowlistedGateOutcome(classified.SafeOutcome))
		}
	}
}

// newznabCapsTarget is the C-56 CAPS target for the no-evidence classification
// rows (which never inspect the target beyond identity).
func newznabCapsTarget() newznab.EvidenceTarget {
	return newznab.EvidenceTarget{Kind: newznab.EvidenceTargetCAPS}
}

// splitSQLStatements splits SQL on top-level semicolons while ignoring
// semicolons inside PostgreSQL dollar-quoted function bodies. It is the
// minimal parser needed to assert per-statement additivity without a DB.
func splitSQLStatements(source string) []string {
	var statements []string
	var current []byte
	inDollar := false
	lineComment := false
	for index := 0; index < len(source); index++ {
		character := source[index]
		if lineComment {
			if character == '\n' {
				lineComment = false
			}
			continue
		}
		if !inDollar && character == '-' && index+1 < len(source) && source[index+1] == '-' {
			lineComment = true
			index++
			continue
		}
		if character == '$' && index+1 < len(source) && source[index+1] == '$' {
			inDollar = !inDollar
			current = append(current, '$', '$')
			index++
			continue
		}
		if character == ';' && !inDollar {
			statements = append(statements, string(current))
			current = current[:0]
			continue
		}
		current = append(current, character)
	}
	if len(strings.TrimSpace(string(current))) > 0 {
		statements = append(statements, string(current))
	}
	return statements
}

// TestMigration35SourceIsAdditiveOnlyAndSecretFree is the non-database A-4 /
// B-54b guard over the migration SOURCE (the DB-backed
// TestMigration35IsAdditiveOnly proves the catalog delta). Every top-level
// statement in migration 0035 must begin with an approved additive verb: no
// DROP, DELETE, INSERT, UPDATE, TRUNCATE, RENAME, ALTER COLUMN/TYPE, or
// SET NOT NULL/DEFAULT. Every added column must be on the approved list and
// must not name a raw body/URL/header/secret persistence surface.
func TestMigration35SourceIsAdditiveOnlyAndSecretFree(t *testing.T) {
	t.Parallel()
	body, err := migrationFiles.ReadFile("migrations/0035_indexer_commissioning_live_gate.sql")
	if err != nil {
		t.Fatal(err)
	}
	statements := splitSQLStatements(string(body))
	if len(statements) < 6 {
		t.Fatalf("expected several additive statements, parsed %d", len(statements))
	}
	for _, raw := range statements {
		statement := strings.ToUpper(strings.TrimSpace(raw))
		if statement == "" {
			continue
		}
		switch {
		case strings.HasPrefix(statement, "ALTER TABLE INDEXER_BUDGET_STATE ADD COLUMN"):
		case strings.HasPrefix(statement, "ALTER TABLE INDEXER_BUDGET_STATE ADD CONSTRAINT"):
		case strings.HasPrefix(statement, "CREATE UNIQUE INDEX"):
		case strings.HasPrefix(statement, "CREATE FUNCTION"):
		case strings.HasPrefix(statement, "CREATE TRIGGER"):
		default:
			t.Fatalf("migration 0035 contains a non-additive statement: %.80q", strings.TrimSpace(raw))
		}
	}
	// Exactly the two approved columns may be added, with non-secret names.
	added := map[string]bool{}
	approved := map[string]bool{"last_outcome_at": true, "commissioning_gate_step_id": true}
	for _, raw := range statements {
		statement := strings.TrimSpace(raw)
		upper := strings.ToUpper(statement)
		if !strings.HasPrefix(upper, "ALTER TABLE INDEXER_BUDGET_STATE ADD COLUMN") {
			continue
		}
		fields := strings.Fields(statement)
		if len(fields) < 6 {
			t.Fatalf("unparseable ADD COLUMN statement: %q", statement)
		}
		name := strings.ToLower(fields[5])
		added[name] = false
		if !approved[name] {
			t.Fatalf("unexpected column added by migration 0035: %q", name)
		}
	}
	if len(added) != 2 {
		t.Fatalf("migration 0035 added %d columns, want exactly 2", len(added))
	}
}
