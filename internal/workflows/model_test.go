package workflows

import (
	"testing"
	"time"
	"uuid"
)

func TestWorkflowCannotSkipVerification(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	workflow := New(uuid.NewV7(), uuid.NewV7(), uuid.NewV7(), now)
	if _, err := workflow.Transition(StatePlexConfirmed, "unsafe shortcut", "test", now); err == nil {
		t.Fatal("expected direct Plex confirmation transition to fail")
	}
}

func TestOwnerAttentionStateMakesExhaustedRoundTerminal(t *testing.T) {
	exhausted := Workflow{State: StateWaiting, AttentionCode: AttentionRoundExhaustedWaitingNewIdentity}
	if got := exhausted.OwnerAttentionState(); got != StateNeedsAttention {
		t.Fatalf("exhausted round reported %q, want %q", got, StateNeedsAttention)
	}
	releaseFailed := Workflow{State: StateWaiting, ReleaseAttemptFailed: true}
	if got := releaseFailed.OwnerAttentionState(); got != StateNeedsAttention {
		t.Fatalf("release-failed waiting round reported %q, want %q", got, StateNeedsAttention)
	}
	policyChanged := Workflow{State: StateWaiting, AttentionCode: AttentionQualityPolicyChanged}
	if got := policyChanged.OwnerAttentionState(); got != StateNeedsAttention {
		t.Fatalf("quality-policy-changed waiting round reported %q, want %q", got, StateNeedsAttention)
	}
	plainWaiting := Workflow{State: StateWaiting}
	if got := plainWaiting.OwnerAttentionState(); got != StateWaiting {
		t.Fatalf("plain waiting round reported %q, want %q", got, StateWaiting)
	}
	searching := Workflow{State: StateSearching, ReleaseAttemptFailed: true}
	if got := searching.OwnerAttentionState(); got != StateSearching {
		t.Fatalf("searching workflow reported %q, want %q", got, StateSearching)
	}
	if got := (Workflow{State: StatePlexConfirmed, AttentionCode: AttentionRoundExhaustedWaitingNewIdentity}).OwnerAttentionState(); got != StatePlexConfirmed {
		t.Fatalf("completed workflow reported %q, want %q", got, StatePlexConfirmed)
	}
}

func TestWorkflowHappyPath(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	workflow := New(uuid.NewV7(), uuid.NewV7(), uuid.NewV7(), now)
	path := []State{StateSearching, StateCandidateSelected, StateSubmitting, StateQueued, StateStreamingReady, StateImportComplete, StateVerifying, StatePublishing, StatePlexScanning, StatePlexConfirmed}
	for _, state := range path {
		if _, err := workflow.Transition(state, "test", "test", now); err != nil {
			t.Fatalf("transition to %s failed: %v", state, err)
		}
	}
	if !workflow.Complete() {
		t.Fatal("expected workflow to be complete")
	}
}
