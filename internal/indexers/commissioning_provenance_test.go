package indexers

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func (store *stubBudgetStore) LoadAuthoritativeIndexerState(_ context.Context, indexerID string) (AuthoritativeSnapshot, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return AuthoritativeSnapshot{Snapshot: Snapshot{IndexerID: indexerID, NextAllowedAt: store.snapshot.NextAllowedAt, CooldownUntil: store.snapshot.CooldownUntil, ConsecutiveFailure: store.snapshot.ConsecutiveFailure, LastOutcome: store.snapshot.LastOutcome, LastOutcomeAt: store.snapshot.LastOutcomeAt, UpdatedAt: store.snapshot.UpdatedAt, MinimumInterval: store.snapshot.MinimumInterval}, Gate: CommissioningGateClear}, nil
}

func recordingBudgetStore() *stubBudgetStore {
	return &stubBudgetStore{}
}

func TestComputeOutcomeEffectMatchesLegacyPermitRecordResult(t *testing.T) {
	startStates := []uint32{0, 1, 5}
	indexerIDs := []string{"550e8400-e29b-41d4-a716-446655440000", "6ba7b810-9dad-11d1-80b4-00c04fd430c8"}
	budget := Budget{MinimumInterval: 3 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute}
	cases := []struct {
		name       string
		result     RecordResult
		retryAfter string
	}{
		{name: "success", result: RecordResult{Outcome: OutcomeSuccess, Scope: FailureScopeNone}},
		{name: "no_results", result: RecordResult{Outcome: OutcomeNoResults, Scope: FailureScopeNone}},
		{name: "rate_limited_no_retry", result: RecordResult{Outcome: OutcomeRateLimited, Scope: FailureScopeInstance}},
		{name: "rate_limited_delta", result: RecordResult{Outcome: OutcomeRateLimited, Scope: FailureScopeInstance}, retryAfter: "120"},
		{name: "rate_limited_http_date", result: RecordResult{Outcome: OutcomeRateLimited, Scope: FailureScopeInstance}, retryAfter: time.Unix(4_000_000_000, 0).UTC().Format("Mon, 02 Jan 2006 15:04:05 GMT")},
		{name: "rate_limited_malformed", result: RecordResult{Outcome: OutcomeRateLimited, Scope: FailureScopeInstance}, retryAfter: "not-a-date"},
		{name: "auth_failed", result: RecordResult{Outcome: OutcomeAuthFailed, Scope: FailureScopeInstance}},
		{name: "system_instance", result: RecordResult{Outcome: OutcomeSystemFailure, Scope: FailureScopeInstance}},
		{name: "system_operation", result: RecordResult{Outcome: OutcomeSystemFailure, Scope: FailureScopeOperation}},
		{name: "cancelled", result: RecordResult{Outcome: OutcomeCancelled, Scope: FailureScopeNone}},
	}
	for _, indexerID := range indexerIDs {
		for _, failures := range startStates {
			for _, test := range cases {
				now := time.Unix(1_800_000_000, 0).UTC()
				start := Snapshot{IndexerID: indexerID, ConsecutiveFailure: failures, CooldownUntil: now.Add(-time.Hour), MinimumInterval: budget.MinimumInterval}
				want := ComputeOutcomeEffect(start, budget, indexerID, test.result, test.retryAfter, now)
				store := recordingBudgetStore()
				scheduler := NewScheduler(1, store)
				scheduler.now = func() time.Time { return now }
				if err := scheduler.Configure(indexerID, budget); err != nil {
					t.Fatal(err)
				}
				lane := scheduler.getLane(indexerID)
				lane.mu.Lock()
				lane.loaded = true
				lane.snapshot = start
				lane.mu.Unlock()
				permit, err := scheduler.Acquire(context.Background(), indexerID, WorkloadInteractive)
				if err != nil {
					t.Fatalf("%s/%s/%d: acquire: %v", indexerID, test.name, failures, err)
				}
				if test.retryAfter != "" && test.result.Outcome == OutcomeRateLimited {
					headers := make(http.Header)
					headers.Set("Retry-After", test.retryAfter)
					if err := permit.Record(context.Background(), OutcomeRateLimited, headers); err != nil {
						t.Fatalf("%s/%s/%d: record: %v", indexerID, test.name, failures, err)
					}
				} else if err := permit.RecordResult(context.Background(), test.result); err != nil {
					t.Fatalf("%s/%s/%d: record: %v", indexerID, test.name, failures, err)
				}
				got := store.snapshot
				// The legacy path persists via SaveIndexerBudget; compare fields.
				if !got.NextAllowedAt.Equal(want.NextAllowedAt) || !got.CooldownUntil.Equal(want.CooldownUntil) ||
					got.ConsecutiveFailure != want.ConsecutiveFailure || got.LastOutcome != want.LastOutcome ||
					!got.LastOutcomeAt.Equal(want.LastOutcomeAt) || !got.UpdatedAt.Equal(want.UpdatedAt) ||
					got.MinimumInterval != want.MinimumInterval {
					t.Fatalf("%s/%s/%d: got=%#v want=%#v", indexerID, test.name, failures, got, want)
				}
			}
		}
	}
	// Case 11 (erratum): an existing LATER CooldownUntil with an earlier-or-absent
	// new deadline takes the LEGACY else branch and OVERWRITES with now+backoff,
	// per frozen computeOutcomeEffectLocked; the A-vs-B differential (not a
	// preserved-cooldown assertion) compares that frozen behavior exactly.
	for _, indexerID := range indexerIDs {
		for _, failures := range startStates {
			now11 := time.Unix(1_800_000_000, 0).UTC()
			start11 := Snapshot{IndexerID: indexerID, ConsecutiveFailure: failures, CooldownUntil: now11.Add(time.Hour), MinimumInterval: budget.MinimumInterval}
			result11 := RecordResult{Outcome: OutcomeRateLimited, Scope: FailureScopeInstance, RetryAt: now11.Add(time.Minute)}
			want11 := ComputeOutcomeEffect(start11, budget, indexerID, result11, "", now11)
			store11 := recordingBudgetStore()
			scheduler11 := NewScheduler(1, store11)
			scheduler11.now = func() time.Time { return now11 }
			if err := scheduler11.Configure(indexerID, budget); err != nil {
				t.Fatal(err)
			}
			lane11 := scheduler11.getLane(indexerID)
			// Acquire against a clear snapshot first (a future CooldownUntil
			// would itself refuse Acquire), then install the case-11
			// future-cooldown state while holding the permit: legacy
			// recordResult reads lane.snapshot at record time.
			lane11.mu.Lock()
			lane11.loaded = true
			lane11.snapshot = Snapshot{IndexerID: indexerID, CooldownUntil: now11.Add(-time.Hour), MinimumInterval: budget.MinimumInterval}
			lane11.mu.Unlock()
			permit11, err := scheduler11.Acquire(context.Background(), indexerID, WorkloadInteractive)
			if err != nil {
				t.Fatalf("%s/case11/%d: acquire: %v", indexerID, failures, err)
			}
			lane11.mu.Lock()
			lane11.snapshot = start11
			lane11.mu.Unlock()
			if err := permit11.RecordResult(context.Background(), result11); err != nil {
				t.Fatalf("%s/case11/%d: record: %v", indexerID, failures, err)
			}
			got11 := store11.snapshot
			if !got11.CooldownUntil.Equal(want11.CooldownUntil) ||
				!got11.NextAllowedAt.Equal(want11.NextAllowedAt) ||
				got11.ConsecutiveFailure != want11.ConsecutiveFailure ||
				got11.LastOutcome != want11.LastOutcome ||
				!got11.LastOutcomeAt.Equal(want11.LastOutcomeAt) ||
				!got11.UpdatedAt.Equal(want11.UpdatedAt) ||
				got11.MinimumInterval != want11.MinimumInterval {
				t.Fatalf("%s/case11/%d: got=%#v want=%#v", indexerID, failures, got11, want11)
			}
			// The frozen legacy behavior for this weaker-deadline case is the
			// else branch: now + backoff(indexerID, failures+1), NOT the
			// preserved existing cooldown.
			if got11.CooldownUntil.Equal(start11.CooldownUntil) {
				t.Fatalf("%s/case11/%d: stronger existing cooldown was preserved, want legacy else-branch overwrite: %#v", indexerID, failures, got11)
			}
			if want := now11.Add(backoff(indexerID, failures+1, budget)); !got11.CooldownUntil.Equal(want) {
				t.Fatalf("%s/case11/%d: got=%s want now+backoff=%s", indexerID, failures, got11.CooldownUntil, want)
			}
		}
	}
	// Existing cooldown later than the new deadline: preserved (deadline
	// branch). Covered per-case in the table via the HTTP-date form; assert
	// one explicit later-deadline-wins case here.
	nowLater := time.Unix(1_800_000_000, 0).UTC()
	startLater := Snapshot{IndexerID: indexerIDs[0], CooldownUntil: nowLater.Add(time.Hour), ConsecutiveFailure: 1}
	gotLater := ComputeOutcomeEffect(startLater, budget, indexerIDs[0], RecordResult{Outcome: OutcomeRateLimited, Scope: FailureScopeInstance, RetryAt: nowLater.Add(2 * time.Hour)}, "", nowLater)
	if !gotLater.CooldownUntil.Equal(nowLater.Add(2 * time.Hour)) {
		t.Fatalf("later deadline not applied: %#v", gotLater)
	}
	// Existing cooldown later than the new deadline: the else branch applies
	// backoff(). Here assert backoff fallback: malformed Retry-After falls
	// through to backoff().
	now2 := time.Unix(1_800_000_000, 0).UTC()
	start2 := Snapshot{IndexerID: indexerIDs[0], CooldownUntil: now2.Add(2 * time.Hour), ConsecutiveFailure: 1}
	got2 := ComputeOutcomeEffect(start2, budget, indexerIDs[0], RecordResult{Outcome: OutcomeRateLimited, Scope: FailureScopeInstance}, "not-a-date", now2)
	want := now2.Add(backoff(indexerIDs[0], 2, budget))
	if !got2.CooldownUntil.Equal(want) {
		t.Fatalf("malformed retry-after did not fall through to backoff: %#v", got2)
	}
}

func TestAcquireCommissioningV2BindsConfiguredBudgetSnapshot(t *testing.T) {
	scheduler := NewScheduler(4, nil)
	indexerID := "550e8400-e29b-41d4-a716-446655440000"
	stepID := "6ba7b811-9dad-11d1-80b4-00c04fd430c8"
	budget := Budget{MinimumInterval: 5 * time.Second, FailureBackoff: 2 * time.Second, MaximumBackoff: time.Minute, QueryLimit: 50, GrabLimit: 5, LimitWindow: time.Hour}
	if err := scheduler.Configure(indexerID, budget); err != nil {
		t.Fatal(err)
	}
	permit, err := scheduler.AcquireCommissioningV2(context.Background(), indexerID, stepID)
	if err != nil {
		t.Fatal(err)
	}
	defer permit.ReleaseWithoutOutcome()
	snapshot := permit.CommissioningBudgetSnapshot()
	if !snapshot.Valid() {
		t.Fatal("snapshot invalid")
	}
	if snapshot.MinimumInterval() != budget.MinimumInterval || snapshot.FailureBackoff() != budget.FailureBackoff ||
		snapshot.MaximumBackoff() != budget.MaximumBackoff || snapshot.QueryLimit() != budget.QueryLimit ||
		snapshot.GrabLimit() != budget.GrabLimit || snapshot.LimitWindow() != budget.LimitWindow {
		t.Fatalf("snapshot accessors mismatch: %#v", snapshot)
	}
	if !snapshot.MatchesAttempt(indexerID, stepID) {
		t.Fatal("snapshot does not match attempt")
	}
}

func TestAcquireCommissioningV2RefusesUnconfiguredLaneBudget(t *testing.T) {
	scheduler := NewScheduler(1, nil)
	indexerID := "550e8400-e29b-41d4-a716-446655440000"
	stepID := "6ba7b811-9dad-11d1-80b4-00c04fd430c8"
	if _, err := scheduler.AcquireCommissioningV2(context.Background(), indexerID, stepID); !errors.Is(err, ErrInvalidBudget) {
		t.Fatalf("err=%v", err)
	}
	// Tokens released: a legacy acquire must proceed immediately.
	if _, err := scheduler.Acquire(context.Background(), indexerID, WorkloadInteractive); err != nil {
		t.Fatalf("lane token leaked: %v", err)
	} else {
		_ = err
	}
}

func TestValidCommissioningBudgetRejectsZeroAndSubMinimumConfigurations(t *testing.T) {
	good := Budget{MinimumInterval: 3 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 10, LimitWindow: time.Hour}
	if !ValidBudget(good) || !ValidCommissioningBudget(good) {
		t.Fatal("good budget rejected")
	}
	for _, bad := range []Budget{
		{},
		{FailureBackoff: 0},
		{MinimumInterval: time.Second, FailureBackoff: 2 * time.Second, MaximumBackoff: time.Second},
		{MinimumInterval: 2 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 10, LimitWindow: time.Hour},
		{MinimumInterval: 3 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, LimitWindow: time.Hour},
		{MinimumInterval: 3 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 10},
	} {
		if ValidCommissioningBudget(bad) {
			t.Fatalf("invalid commissioning budget accepted: %#v", bad)
		}
	}
	scheduler := NewScheduler(1, nil)
	for _, bad := range []Budget{
		{MinimumInterval: -time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute},
		{FailureBackoff: 0, MaximumBackoff: time.Minute},
	} {
		if ValidBudget(bad) {
			t.Fatalf("ValidBudget disagrees with Configure for %#v", bad)
		}
		if err := scheduler.Configure("550e8400-e29b-41d4-a716-446655440000", bad); !errors.Is(err, ErrInvalidBudget) {
			t.Fatalf("Configure accepted %#v", bad)
		}
	}
}

func TestPermitBudgetImmutableAcrossLaneReconfiguration(t *testing.T) {
	scheduler := NewScheduler(1, nil)
	indexerID := "550e8400-e29b-41d4-a716-446655440000"
	stepID := "6ba7b811-9dad-11d1-80b4-00c04fd430c8"
	budget1 := Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 10, LimitWindow: time.Hour}
	budget2 := Budget{MinimumInterval: 9 * time.Second, FailureBackoff: 3 * time.Second, MaximumBackoff: 2 * time.Minute, QueryLimit: 20, LimitWindow: 2 * time.Hour}
	if err := scheduler.Configure(indexerID, budget1); err != nil {
		t.Fatal(err)
	}
	permit, err := scheduler.AcquireCommissioningV2(context.Background(), indexerID, stepID)
	if err != nil {
		t.Fatal(err)
	}
	defer permit.ReleaseWithoutOutcome()
	if err := scheduler.Configure(indexerID, budget2); err != nil {
		t.Fatal(err)
	}
	snapshot := permit.CommissioningBudgetSnapshot()
	if snapshot.MinimumInterval() != budget1.MinimumInterval || snapshot.QueryLimit() != budget1.QueryLimit {
		t.Fatalf("permit snapshot mutated: %#v", snapshot)
	}
}

func TestLegacyRecordResultStillUsesCurrentLaneBudget(t *testing.T) {
	scheduler := NewScheduler(1, nil)
	now := time.Unix(1_800_000_000, 0).UTC()
	scheduler.now = func() time.Time { return now }
	indexerID := "legacy-budget-pinned"
	budget1 := Budget{MinimumInterval: 3 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute}
	budget2 := Budget{MinimumInterval: 30 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute}
	if err := scheduler.Configure(indexerID, budget1); err != nil {
		t.Fatal(err)
	}
	permit, err := scheduler.Acquire(context.Background(), indexerID, WorkloadInteractive)
	if err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Configure(indexerID, budget2); err != nil {
		t.Fatal(err)
	}
	if err := permit.Record(context.Background(), OutcomeSuccess, nil); err != nil {
		t.Fatal(err)
	}
	if got := scheduler.Snapshot(indexerID); got.NextAllowedAt != now.Add(budget2.MinimumInterval) {
		t.Fatalf("legacy record did not use current lane budget: %#v", got)
	}
}

func TestLegacyAcquireYieldsZeroCommissioningSnapshot(t *testing.T) {
	scheduler := NewScheduler(1, recordingBudgetStore())
	indexerID := "550e8400-e29b-41d4-a716-446655440000"
	stepID := "6ba7b811-9dad-11d1-80b4-00c04fd430c8"
	budget := Budget{MinimumInterval: 5 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 10, LimitWindow: time.Hour}
	if err := scheduler.Configure(indexerID, budget); err != nil {
		t.Fatal(err)
	}
	permit, err := scheduler.Acquire(context.Background(), indexerID, WorkloadInteractive)
	if err != nil {
		t.Fatal(err)
	}
	defer permit.ReleaseWithoutOutcome()
	snapshot := permit.CommissioningBudgetSnapshot()
	if snapshot.Valid() || snapshot.MatchesAttempt(indexerID, stepID) {
		t.Fatal("legacy permit yielded usable commissioning snapshot")
	}
	// E-64: the zero snapshot from a legacy Acquire is rejected at ALL THREE
	// durable commissioning boundaries. The boundary types live in
	// commissioningv2/postgres (import cycle), so this test proves rejection
	// through the same predicates the boundaries apply: Valid()==false,
	// MatchesAttempt()==false for the exact attempt, and each accessor at its
	// zero value (a boundary comparing MinimumInterval/LimitWindow against a
	// bound snapshot necessarily fails closed here).
	if snapshot.MinimumInterval() != 0 || snapshot.FailureBackoff() != 0 || snapshot.MaximumBackoff() != 0 ||
		snapshot.QueryLimit() != 0 || snapshot.GrabLimit() != 0 || snapshot.LimitWindow() != 0 {
		t.Fatalf("legacy zero snapshot accessors non-zero: min=%s q=%d w=%s", snapshot.MinimumInterval(), snapshot.QueryLimit(), snapshot.LimitWindow())
	}
	if snapshot.ConsumeCommissioningTransmission() || snapshot.TransmissionConsumed() {
		t.Fatal("legacy zero snapshot carries transmission authority")
	}
	// The DB-backed E-64 arms (reservation / transmit / completion zero-write
	// with a legacy snapshot) live in the postgres matrix test
	// TestLegacySnapshotRejectedAtAllDurableBoundaries, which presents this
	// exact zero snapshot at all three boundaries and proves rejection with
	// zero durable effect.
}

func TestCommissioningBudgetSnapshotCannotBeSubstituted(t *testing.T) {
	snapshotType := reflect.TypeOf(CommissioningBudgetSnapshot{})
	if snapshotType.NumField() < 1 {
		t.Fatal("snapshot has no fields")
	}
	for index := 0; index < snapshotType.NumField(); index++ {
		if snapshotType.Field(index).PkgPath == "" {
			t.Fatalf("exported field %q", snapshotType.Field(index).Name)
		}
	}
	wantMethods := map[string]bool{"MinimumInterval": true, "FailureBackoff": true, "MaximumBackoff": true, "QueryLimit": true, "GrabLimit": true, "LimitWindow": true, "Valid": true, "MatchesAttempt": true, "ConsumeCommissioningTransmission": true, "TransmissionConsumed": true}
	seen := map[string]bool{}
	for index := 0; index < snapshotType.NumMethod(); index++ {
		seen[snapshotType.Method(index).Name] = true
	}
	if !reflect.DeepEqual(seen, wantMethods) {
		t.Fatalf("method set=%v", seen)
	}
	for index := 0; index < snapshotType.NumMethod(); index++ {
		method := snapshotType.Method(index)
		for arg := 1; arg < method.Type.NumIn(); arg++ {
			if method.Type.In(arg) == reflect.TypeOf(Budget{}) {
				t.Fatalf("method %q takes Budget", method.Name)
			}
		}
		if method.Type.NumOut() == 1 && method.Type.Out(0) == reflect.TypeOf(CommissioningBudgetSnapshot{}) {
			t.Fatalf("method %q returns snapshot", method.Name)
		}
	}
	var zero CommissioningBudgetSnapshot
	if zero.Valid() || zero.MatchesAttempt("x", "y") || zero.MatchesAttempt("", "") {
		t.Fatal("zero snapshot usable")
	}
	// CP1-1: the zero/legacy snapshot has no send cell — consume is false
	// and the provenance read is false, with no mutation.
	if zero.ConsumeCommissioningTransmission() {
		t.Fatal("zero snapshot consumed transmission authority")
	}
	if zero.TransmissionConsumed() {
		t.Fatal("zero snapshot reports consumed")
	}
	if zero.MinimumInterval() != 0 || zero.FailureBackoff() != 0 || zero.MaximumBackoff() != 0 || zero.QueryLimit() != 0 || zero.GrabLimit() != 0 || zero.LimitWindow() != 0 {
		t.Fatal("zero snapshot accessors non-zero")
	}
	// E-65 item 6: cross-package structural proof that the commissioning
	// contracts carry CommissioningBudgetSnapshot and no raw Budget.
	assertCommissioningContractsCarrySnapshot(t)
	negativeCompileProof(t)
}

// TestAuthoritativeReloadPublishesDurableRevision is the regression guard for
// the AuthoritativeSnapshot revision-shadowing defect. The durable revision
// must survive the authoritative reload into lane.snapshot, where
// Snapshot.Revision is the exact persisted indexer_budget_state revision.
func TestAuthoritativeReloadPublishesDurableRevision(t *testing.T) {
	t.Parallel()
	indexerID := "550e8400-e29b-41d4-a716-446655440000"
	durableUpdatedAt := time.Unix(1_800_000_500, 0).UTC()
	store := &recordingSaveStore{authority: AuthoritativeSnapshot{
		Snapshot: Snapshot{
			IndexerID:          indexerID,
			NextAllowedAt:      durableUpdatedAt.Add(time.Minute),
			CooldownUntil:      durableUpdatedAt.Add(2 * time.Minute),
			ConsecutiveFailure: 3,
			LastOutcome:        OutcomeRateLimited,
			LastOutcomeAt:      durableUpdatedAt.Add(-time.Minute),
			UpdatedAt:          durableUpdatedAt,
			MinimumInterval:    7 * time.Second,
			Revision:           42,
		},
		Gate: CommissioningGateClear,
	}}
	scheduler := NewScheduler(1, store)
	if err := scheduler.Configure(indexerID, commissioningTestBudget()); err != nil {
		t.Fatal(err)
	}
	scheduler.InvalidateLaneCache(indexerID, nil)
	if err := scheduler.ReloadLaneCache(context.Background(), indexerID); err != nil {
		t.Fatal(err)
	}
	published := scheduler.Snapshot(indexerID)
	// The revision-shadowing defect dropped Revision to zero here even though
	// the durable row carried 42; Snapshot.Revision is the exact persisted
	// revision and must survive the authoritative reload.
	if published.Revision != 42 {
		t.Fatalf("authoritative reload dropped durable revision: got %d want 42", published.Revision)
	}
	if !published.UpdatedAt.Equal(durableUpdatedAt) || !published.NextAllowedAt.Equal(durableUpdatedAt.Add(time.Minute)) ||
		!published.CooldownUntil.Equal(durableUpdatedAt.Add(2*time.Minute)) || published.ConsecutiveFailure != 3 ||
		published.LastOutcome != OutcomeRateLimited || !published.LastOutcomeAt.Equal(durableUpdatedAt.Add(-time.Minute)) ||
		published.MinimumInterval != 7*time.Second {
		t.Fatalf("authoritative reload published a partial snapshot: %#v", published)
	}
	if scheduler.laneCacheUnknown(indexerID) {
		t.Fatal("authoritative reload must clear cacheUnknown")
	}
	// Structural guard: AuthoritativeSnapshot must expose Revision ONLY as the
	// promoted embedded Snapshot.Revision. A re-added outer Revision declaration
	// would shadow the embedded field (Index path length 1 instead of 2) and
	// recreate the defect where a loader writing the outer field is dropped by
	// ReloadLaneCache's authoritative.Snapshot assignment.
	revisionField, ok := reflect.TypeOf(AuthoritativeSnapshot{}).FieldByName("Revision")
	if !ok || len(revisionField.Index) != 2 || revisionField.Index[0] != 0 {
		t.Fatalf("AuthoritativeSnapshot.Revision must be promoted from embedded Snapshot, got index path %v", revisionField.Index)
	}
}

func TestAcquireCommissioningV2RejectsNonCanonicalIdentity(t *testing.T) {
	// E-66: table over BOTH fields (malformed indexerID with canonical
	// stepID, and the reverse) x every non-canonical spelling, with nil store
	// (no durable writes possible; the postgres E-66 companion proves the
	// DB zero-write arms). Run with the global limit at 1, so any leaked
	// lane/global token would deadlock the follow-up acquisition rather than
	// pass silently.
	canonicalIndexer := "550e8400-e29b-41d4-a716-446655440000"
	canonicalStep := "6ba7b811-9dad-11d1-80b4-00c04fd430c8"
	spellings := []string{
		"550E8400-E29B-41D4-A716-446655440000",
		"urn:uuid:550e8400-e29b-41d4-a716-446655440000",
		"{550e8400-e29b-41d4-a716-446655440000}",
		"550e8400e29b41d4a716446655440000",
		"",
		"   ",
		"not-a-uuid",
	}
	for _, spelling := range spellings {
		for _, swapIndexer := range []bool{true, false} {
			scheduler := NewScheduler(1, nil)
			indexerID, stepID := canonicalIndexer, canonicalStep
			if swapIndexer {
				indexerID = spelling
			} else {
				stepID = spelling
			}
			permit, err := scheduler.AcquireCommissioningV2(context.Background(), indexerID, stepID)
			if !errors.Is(err, ErrInvalidCommissioningIdentity) || permit != nil {
				t.Fatalf("spelling %q swapIndexer=%v: permit=%v err=%v", spelling, swapIndexer, permit, err)
			}
			// No lane or global token retained: with global limit 1, a
			// leaked token would block this follow-up forever. Configure
			// the canonical lane FIRST so the follow-up must succeed
			// IMMEDIATELY (not merely with a different error).
			if err := scheduler.Configure(canonicalIndexer, commissioningTestBudget()); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				follow, err := scheduler.AcquireCommissioningV2(context.Background(), canonicalIndexer, canonicalStep)
				if err == nil && follow != nil {
					follow.ReleaseWithoutOutcome()
				}
				done <- err
			}()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("spelling %q swapIndexer=%v: follow-up canonical acquire blocked/leaked: %v", spelling, swapIndexer, err)
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("spelling %q swapIndexer=%v: leaked token deadlocked follow-up acquire", spelling, swapIndexer)
			}
			// The malformed identity created no lane-map entry; only the
			// canonical lane exists.
			scheduler.mu.Lock()
			_, malformedLane := scheduler.lanes[strings.TrimSpace(spelling)]
			_, canonicalLane := scheduler.lanes[canonicalIndexer]
			laneCount := len(scheduler.lanes)
			scheduler.mu.Unlock()
			if spelling != "" && strings.TrimSpace(spelling) != "" && malformedLane {
				t.Fatalf("malformed identity created lane entry: %q", spelling)
			}
			if !canonicalLane || laneCount != 1 {
				t.Fatalf("spelling %q: lane map=%d entries, want exactly the canonical lane", spelling, laneCount)
			}
		}
	}
	// The durable zero-write arms (no reservation row, no commissioning state
	// write, no token leak against a real store, subsequent canonical
	// acquisition works) live in the postgres matrix companion
	// TestAcquireCommissioningV2RejectsNonCanonicalIdentityDurable, which
	// uses a real *Store so "no row / no write" is observable.
}
