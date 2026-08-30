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
