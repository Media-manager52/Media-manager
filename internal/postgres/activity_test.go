package postgres

import (
	"testing"

	"github.com/media-manager/media-manager/internal/workflows"
)

func TestWorkflowSearchReadinessMatchesRecoveryGuards(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                     string
		item                     WorkflowSummary
		publicationStarted       bool
		currentJobNeedsAttention bool
		allowed                  bool
	}{
		{name: "monitored", item: WorkflowSummary{State: workflows.StateMonitored}, allowed: true},
		{name: "held", item: WorkflowSummary{State: workflows.StateHeld}, allowed: true},
		{name: "first failed output", item: WorkflowSummary{State: workflows.StateNeedsAttention, RoundSlotsUsed: 1, RoundAttemptLimit: 2}, currentJobNeedsAttention: true, allowed: true},
		{name: "second failed output", item: WorkflowSummary{State: workflows.StateNeedsAttention, RoundSlotsUsed: 2, RoundAttemptLimit: 2}, currentJobNeedsAttention: true},
		{name: "exhausted discovery", item: WorkflowSummary{State: workflows.StateWaiting, RoundState: "exhausted", RetryDiscoveryState: "waiting"}},
		{name: "new release ready", item: WorkflowSummary{State: workflows.StateWaiting, RoundState: "exhausted", RetryDiscoveryState: "ready"}},
		{name: "submission rejected", item: WorkflowSummary{State: workflows.StateNeedsAttention, AttentionCode: "altmount_submission_rejected"}},
		{name: "publication started", item: WorkflowSummary{State: workflows.StateNeedsAttention}, publicationStarted: true},
		{name: "owner recovery", item: WorkflowSummary{State: workflows.StateNeedsAttention, AttentionCode: "plex_timeout"}},
		{name: "in progress", item: WorkflowSummary{State: workflows.StateSearching}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			allowed, reason := workflowSearchReadiness(test.item, test.publicationStarted, test.currentJobNeedsAttention)
			if allowed != test.allowed {
				t.Fatalf("allowed=%v, want %v", allowed, test.allowed)
			}
			if allowed && reason != "" || !allowed && reason == "" {
				t.Fatalf("allowed=%v reason=%q", allowed, reason)
			}
		})
	}
}
