package postgres

import (
	"testing"

	"github.com/media-manager/media-manager/internal/workflows"
)

func TestWorkflowOwnerActionRequiredUsesOnlyStopStates(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		item WorkflowSummary
		want bool
	}{
		{name: "needs attention", item: WorkflowSummary{State: workflows.StateNeedsAttention}, want: true},
		{name: "blocked", item: WorkflowSummary{State: workflows.StateBlocked}, want: true},
		{name: "quarantined", item: WorkflowSummary{State: workflows.StateQuarantined}, want: true},
		{name: "transport unknown", item: WorkflowSummary{State: workflows.StateTransportUnknown}, want: true},
		{name: "waiting with ready discovery", item: WorkflowSummary{State: workflows.StateWaiting, RetryDiscoveryState: "ready"}, want: true},
		{name: "waiting with scheduled discovery", item: WorkflowSummary{State: workflows.StateWaiting, RetryDiscoveryState: "waiting"}},
		{name: "plain waiting", item: WorkflowSummary{State: workflows.StateWaiting}},
		{name: "held is recoverable", item: WorkflowSummary{State: workflows.StateHeld}},
		{name: "searching", item: WorkflowSummary{State: workflows.StateSearching}},
		{name: "queued", item: WorkflowSummary{State: workflows.StateQueued}},
		{name: "import complete", item: WorkflowSummary{State: workflows.StateImportComplete}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := workflowOwnerActionRequired(test.item); got != test.want {
				t.Fatalf("workflowOwnerActionRequired(%+v)=%v, want %v", test.item, got, test.want)
			}
		})
	}
}

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
		{name: "restored open round with old discovery", item: WorkflowSummary{State: workflows.StateWaiting, RoundState: "open", RetryDiscoveryState: "waiting"}, allowed: true},
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
