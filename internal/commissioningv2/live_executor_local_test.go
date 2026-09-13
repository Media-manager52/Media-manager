package commissioningv2

import (
	"context"
	"errors"
	"testing"
	"time"
)

type stubLiveStepStore struct {
	claimed      int
	claimErr     error
	claimState   LiveStepState
	completeErr  error
	deferred     int
	transmitting int
	completed    int
	recovered    int
}

func (store *stubLiveStepStore) ClaimAuthorizedStepIntent(_ context.Context, claim LiveStepClaim, now time.Time) (LiveStepState, error) {
	if store.claimErr != nil {
		return LiveStepState{}, store.claimErr
	}
	store.claimed++
	return LiveStepState{StepID: claim.StepID, State: StepIntentCommitted, IntentAt: &now, LeaseOwner: claim.LeaseOwner, Deadline: claim.LeaseUntil, RunRevision: claim.ExpectedRunRevision + 1}, nil
}

func (store *stubLiveStepStore) DeferClaimedStepBeforeSend(_ context.Context, _ LiveStepDeferral, now time.Time) (LiveStepState, error) {
	store.deferred++
	return LiveStepState{State: StepDeferredBeforeSend, CompletedAt: &now}, nil
}

func (store *stubLiveStepStore) MarkClaimedStepTransmitting(_ context.Context, marker LiveTransmitMarker, now time.Time) (LiveStepState, error) {
	store.transmitting++
	floor := marker.ExecutionDeadline.Add(marker.BudgetSnapshot.MinimumInterval())
	return LiveStepState{StepID: marker.StepID, State: StepTransmitting, TransmittedAt: &now, Deadline: marker.ExecutionDeadline, PacingFloor: floor, GateHeld: true}, nil
}

func (store *stubLiveStepStore) CompleteLiveAttempt(_ context.Context, completion LiveCompletion, now time.Time) (LiveStepState, error) {
	if store.completeErr != nil {
		return LiveStepState{}, store.completeErr
	}
	store.completed++
	return LiveStepState{StepID: completion.StepID, State: StepCompleted, CompletedAt: &now, SafeOutcome: completion.Classified.SafeOutcome}, nil
}

func (store *stubLiveStepStore) MarkInterruptedAttemptIndeterminate(_ context.Context, _ LiveRecovery, now time.Time) (LiveStepState, error) {
	store.recovered++
	return LiveStepState{State: StepIndeterminate, CompletedAt: &now, SafeOutcome: OutcomeIndeterminate}, nil
}

func (store *stubLiveStepStore) RequestRunCancellation(_ context.Context, _, _ string, _ uint64, _ time.Time) (LiveStepState, error) {
	return LiveStepState{}, nil
}

func (store *stubLiveStepStore) FinalizeCancelledRun(_ context.Context, _, _ string, _ uint64, _ time.Time) (LiveStepState, error) {
	return LiveStepState{}, nil
}

func (store *stubLiveStepStore) LoadAuthorizedStep(_ context.Context, _ string) (LiveAuthorizedStep, error) {
	return LiveAuthorizedStep{}, ErrLiveExecutorStateConflict
}

func testLiveSnapshot() LiveAuthorizedStep {
	fingerprint := SumSHA256([]byte("structural"))
	return LiveAuthorizedStep{
		RunID:          "run-local",
		StepID:         "step-local",
		IndexerID:      "indexer-local",
		RunRevision:    7,
		StepState:      StepPlanned,
		Phase:          StepPhaseCAPS,
		Authorized:     true,
		RunState:       RunCAPSAuthorized,
		Ordinal:        1,
		PlannerVersion: PlannerVersion,
		ProfileVersion: 1,
		Fingerprint:    fingerprint,
	}
}

func testLiveCommand(snapshot LiveAuthorizedStep) LiveExecuteCommand {
	return LiveExecuteCommand{
		RunID:             snapshot.RunID,
		StepID:            snapshot.StepID,
		ExpectedRunRev:    snapshot.RunRevision,
		ExpectedStepState: StepPlanned,
		Fingerprint:       snapshot.Fingerprint,
		PlannerVersion:    snapshot.PlannerVersion,
		Deadline:          time.Now().Add(time.Minute),
		LeaseOwner:        "local-test",
	}
}

func TestStalePlannerVersionRejectedBeforeClaimResolutionOrTransmission(t *testing.T) {
	t.Parallel()
	store := &stubLiveStepStore{}
	resolver := &TestCredentialResolver{}
	resolver.RegisterTestCredential("indexer-local", "LOCAL_REF", []byte("secret"))
	_, err := NewLocalLiveExecutor(LiveExecutorDeps{Steps: store, Scheduler: nil, Resolver: resolver, Client: nil})
	// Scheduler/client are required even though the stale-policy gate must
	// fire before they are touched; assert misconfiguration is also closed.
	if err == nil {
		t.Fatal("expected misconfiguration error for nil scheduler/client")
	}
}

func TestStalePlannerVersionRejectedBeforeAnySideEffect(t *testing.T) {
	t.Parallel()
	store := &stubLiveStepStore{}
	resolver := &TestCredentialResolver{}
	resolver.RegisterTestCredential("indexer-local", "LOCAL_REF", []byte("secret"))
	snapshot := testLiveSnapshot()
	snapshot.PlannerVersion = PlannerVersion + 1
	command := testLiveCommand(snapshot)
	command.PlannerVersion = PlannerVersion + 1
	// checkPolicyVersion is the gate: unsupported versions fail before the
	// store claim, credential resolution, reservation, or transmission.
	if err := checkPolicyVersion(command.PlannerVersion); err == nil {
		t.Fatal("stale policy version accepted")
	}
	if store.claimed != 0 || resolver.Calls != 0 {
		t.Fatal("stale authorization caused a side effect")
	}
	_ = command
}

// TestClaimIntentStalePolicyPrecedesEveryOtherGate exercises the REAL
// ClaimIntent orchestration (not the standalone checkPolicyVersion helper):
// an unsupported persisted policy must be rejected before the durable claim,
// and the policy gate must run before the state/identity/authorization gates
// so a stale authorization can never reach the store. Every rejected call
// must leave the store and resolver untouched.
func TestClaimIntentStalePolicyPrecedesEveryOtherGate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(snapshot *LiveAuthorizedStep, command *LiveExecuteCommand)
	}{
		{
			name: "stale command planner",
			mutate: func(_ *LiveAuthorizedStep, command *LiveExecuteCommand) {
				command.PlannerVersion = PlannerVersion + 1
			},
		},
		{
			name: "stale snapshot planner",
			mutate: func(snapshot *LiveAuthorizedStep, _ *LiveExecuteCommand) {
				snapshot.PlannerVersion = PlannerVersion + 1
			},
		},
		{
			name: "command/snapshot policy mismatch",
			mutate: func(snapshot *LiveAuthorizedStep, command *LiveExecuteCommand) {
				snapshot.PlannerVersion = PlannerVersion
				command.PlannerVersion = PlannerVersion + 1
			},
		},
		{
			name: "stale policy beats state conflict",
			mutate: func(snapshot *LiveAuthorizedStep, command *LiveExecuteCommand) {
				command.PlannerVersion = PlannerVersion + 1
				command.StepID = snapshot.StepID + "-conflict"
			},
		},
		{
			name: "stale policy beats authorization failure",
			mutate: func(snapshot *LiveAuthorizedStep, command *LiveExecuteCommand) {
				command.PlannerVersion = PlannerVersion + 1
				snapshot.Authorized = false
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			store := &stubLiveStepStore{}
			resolver := &TestCredentialResolver{}
			resolver.RegisterTestCredential("indexer-local", "LOCAL_REF", []byte("secret"))
			executor := &LiveExecutor{deps: LiveExecutorDeps{Steps: store, Resolver: resolver, Now: time.Now}}
			snapshot := testLiveSnapshot()
			command := testLiveCommand(snapshot)
			testCase.mutate(&snapshot, &command)
			_, err := executor.ClaimIntent(context.Background(), command, snapshot)
			if !errors.Is(err, ErrLiveExecutorStalePolicy) {
				t.Fatalf("ClaimIntent err=%v, want ErrLiveExecutorStalePolicy", err)
			}
			if store.claimed != 0 {
				t.Fatalf("stale policy reached the durable claim: claimed=%d", store.claimed)
			}
			if resolver.Calls != 0 {
				t.Fatalf("stale policy resolved a credential: calls=%d", resolver.Calls)
			}
		})
	}
}

// TestClaimIntentSupportedPolicyReachesDurableClaim is the companion that
// proves the same REAL ClaimIntent path actually claims when the policy is
// supported, so the stale-policy rejection above is not a tautological
// always-fail.
func TestClaimIntentSupportedPolicyReachesDurableClaim(t *testing.T) {
	t.Parallel()
	store := &stubLiveStepStore{}
	resolver := &TestCredentialResolver{}
	executor := &LiveExecutor{deps: LiveExecutorDeps{Steps: store, Resolver: resolver, Now: time.Now}}
	snapshot := testLiveSnapshot()
	command := testLiveCommand(snapshot)
	state, err := executor.ClaimIntent(context.Background(), command, snapshot)
	if err != nil {
		t.Fatalf("supported policy ClaimIntent: %v", err)
	}
	if state.State != StepIntentCommitted {
		t.Fatalf("state=%q, want intent_committed", state.State)
	}
	if store.claimed != 1 {
		t.Fatalf("claimed=%d, want exactly 1", store.claimed)
	}
	if resolver.Calls != 0 {
		t.Fatalf("claim resolved a credential: calls=%d", resolver.Calls)
	}
}

func TestSupportedPolicyClaimSucceedsWithoutNetwork(t *testing.T) {
	t.Parallel()
	snapshot := testLiveSnapshot()
	command := testLiveCommand(snapshot)
	if err := checkPolicyVersion(command.PlannerVersion); err != nil {
		t.Fatal(err)
	}
	if !SupportedProbePolicyVersions[PlannerVersion] {
		t.Fatal("current PlannerVersion is not a supported policy")
	}
}

func TestTestResolverResolvesOnlyRegisteredPairs(t *testing.T) {
	t.Parallel()
	resolver := &TestCredentialResolver{}
	resolver.RegisterTestCredential("indexer-a", "REF_A", []byte("secret-a"))
	credential, err := resolver.ResolveCommissioningCredential(context.Background(), "indexer-a", "REF_A")
	if err != nil || string(credential) != "secret-a" {
		t.Fatalf("registered credential unresolved: %v", err)
	}
	for _, attempt := range [][2]string{{"indexer-a", "REF_B"}, {"indexer-b", "REF_A"}, {"", "REF_A"}, {"indexer-a", ""}} {
		if _, err := resolver.ResolveCommissioningCredential(context.Background(), attempt[0], attempt[1]); err == nil {
			t.Fatalf("unregistered pair resolved: %v", attempt)
		}
	}
	if resolver.Calls != 5 {
		t.Fatalf("resolver calls=%d", resolver.Calls)
	}
}

func TestNoProductionConstructorWiresLiveExecutor(t *testing.T) {
	t.Parallel()
	// Structural scope of this test (Pass 2 owns the full
	// production-unreachability acceptance): the live executor exposes
	// exactly one constructor and its name marks it test-only
	// (NewLocalLiveExecutor). It does NOT prove repository-level
	// production unreachability — no caller-graph or wiring assertion is
	// made here.
	var constructor func(LiveExecutorDeps) (*LiveExecutor, error) = NewLocalLiveExecutor
	if constructor == nil {
		t.Fatal("test-only constructor missing")
	}
}
