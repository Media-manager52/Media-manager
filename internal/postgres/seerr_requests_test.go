package postgres

import "testing"

func TestSeerrRequestLifecycleMapping(t *testing.T) {
	cases := []struct {
		name    string
		summary SeerrRequestSummary
		want    string
	}{
		{
			name:    "active movie is not successful",
			summary: SeerrRequestSummary{WorkflowCount: 1, CurrentState: "searching", SeerrState: "2"},
			want:    "searching",
		},
		{
			name:    "all workflows plex confirmed",
			summary: SeerrRequestSummary{WorkflowCount: 2, ConfirmedCount: 2, CurrentState: "plex_confirmed", SeerrState: "5"},
			want:    "plex_confirmed",
		},
		{
			name:    "partial confirmation is not complete",
			summary: SeerrRequestSummary{WorkflowCount: 2, ConfirmedCount: 1, CurrentState: "verifying", SeerrState: "2"},
			want:    "verifying",
		},
		{
			name:    "workflow attention wins over intake outcome",
			summary: SeerrRequestSummary{WorkflowCount: 2, ConfirmedCount: 1, AttentionCount: 1, CurrentState: "needs_attention", IntakeOutcome: "media_manager_added"},
			want:    "needs_attention",
		},
		{
			name:    "held workflow is surfaced as held",
			summary: SeerrRequestSummary{WorkflowCount: 1, AttentionCount: 1, CurrentState: "held"},
			want:    "held",
		},
		{
			name:    "intake needs attention without a workflow",
			summary: SeerrRequestSummary{IntakeOutcome: "needs_attention", IntakeError: "identity missing", SeerrState: "2"},
			want:    "needs_attention",
		},
		{
			name:    "intake needs configuration is attention, not success",
			summary: SeerrRequestSummary{IntakeOutcome: "needs_configuration", SeerrState: "2"},
			want:    "needs_attention",
		},
		{
			name:    "observed not approved",
			summary: SeerrRequestSummary{IntakeOutcome: "observed_not_approved", SeerrState: "1"},
			want:    "not_approved",
		},
		{
			name:    "already available before Media Manager",
			summary: SeerrRequestSummary{IntakeOutcome: "already_available", SeerrState: "5"},
			want:    "available",
		},
		{
			name:    "approved snapshot without a workflow stays monitored",
			summary: SeerrRequestSummary{SeerrState: "2"},
			want:    "monitored",
		},
		{
			name:    "declined request",
			summary: SeerrRequestSummary{IntakeOutcome: "observed_not_approved", SeerrState: "3"},
			want:    "not_approved",
		},
		{
			name:    "unknown snapshot state never looks successful",
			summary: SeerrRequestSummary{SeerrState: "99"},
			want:    "unknown",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := seerrRequestLifecycle(test.summary); got != test.want {
				t.Fatalf("lifecycle=%q, want %q", got, test.want)
			}
		})
	}
}
