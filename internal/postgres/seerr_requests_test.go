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
			name:    "waiting search without an owner decision remains automatic",
			summary: SeerrRequestSummary{WorkflowCount: 1, CurrentState: "waiting", IntakeOutcome: "media_manager_added", SeerrState: "2"},
			want:    "waiting",
		},
		{
			name:    "waiting discovery with a new release requires the owner",
			summary: SeerrRequestSummary{WorkflowCount: 1, AttentionCount: 1, CurrentState: "waiting", IntakeOutcome: "media_manager_added", SeerrState: "2"},
			want:    "needs_attention",
		},
		{
			name:    "held workflow is surfaced as held",
			summary: SeerrRequestSummary{WorkflowCount: 1, AttentionCount: 1, CurrentState: "held"},
			want:    "held",
		},
		{
			name:    "held search with automatic retry is waiting",
			summary: SeerrRequestSummary{WorkflowCount: 1, CurrentState: "held", IntakeOutcome: "media_manager_added", SeerrState: "2"},
			want:    "waiting",
		},
		{
			name:    "held search does not hide intake configuration failure",
			summary: SeerrRequestSummary{WorkflowCount: 1, CurrentState: "held", IntakeOutcome: "needs_configuration", SeerrState: "2"},
			want:    "needs_attention",
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
			want:    "declined",
		},
		{
			// Recorded by builds that read Seerr's Completed status as a
			// revocation; the request is available, not awaiting approval.
			name:    "completed in Seerr after an older intake saw it as not approved",
			summary: SeerrRequestSummary{IntakeOutcome: "observed_not_approved", SeerrState: "5"},
			want:    "available",
		},
		{
			// Intake outcomes recorded by builds that read Completed as a
			// revocation must not keep a completed request under attention
			// once no linked workflow needs anything.
			name:    "completed in Seerr with a stale intake attention outcome is available",
			summary: SeerrRequestSummary{IntakeOutcome: "needs_attention", SeerrState: "5", WorkflowCount: 1, CurrentState: "cancelled"},
			want:    "available",
		},
		{
			name:    "approved request whose intake needs attention still needs attention",
			summary: SeerrRequestSummary{IntakeOutcome: "needs_attention", SeerrState: "2"},
			want:    "needs_attention",
		},
		{
			name:    "completed request with a workflow that needs attention still needs attention",
			summary: SeerrRequestSummary{IntakeOutcome: "needs_attention", SeerrState: "5", WorkflowCount: 1, AttentionCount: 1, CurrentState: "needs_attention"},
			want:    "needs_attention",
		},
		{
			name:    "failed in Seerr is not awaiting approval",
			summary: SeerrRequestSummary{IntakeOutcome: "observed_not_approved", SeerrState: "4"},
			want:    "failed",
		},
		{
			name:    "completed with a cancelled workflow is available, not cancelled",
			summary: SeerrRequestSummary{IntakeOutcome: "observed_not_approved", SeerrState: "5", WorkflowCount: 1, CurrentState: "cancelled"},
			want:    "available",
		},
		{
			name:    "approved in Seerr before intake re-reads it is not yet monitored",
			summary: SeerrRequestSummary{IntakeOutcome: "observed_not_approved", SeerrState: "2"},
			want:    "approved_in_seerr",
		},
		{
			name:    "unknown snapshot state never looks successful",
			summary: SeerrRequestSummary{SeerrState: "99"},
			want:    "unknown",
		},
		{
			name:    "deleted request is not awaiting approval",
			summary: SeerrRequestSummary{SeerrState: "deleted", IntakeOutcome: "observed_not_approved"},
			want:    "deleted",
		},
		{
			name:    "deleted request ignores stale intake attention",
			summary: SeerrRequestSummary{SeerrState: "deleted", IntakeOutcome: "needs_attention"},
			want:    "deleted",
		},
		{
			name:    "deleted request still exposes workflow attention",
			summary: SeerrRequestSummary{SeerrState: "deleted", IntakeOutcome: "observed_not_approved", WorkflowCount: 1, AttentionCount: 1, CurrentState: "needs_attention"},
			want:    "needs_attention",
		},
		{
			name:    "deleted request preserves completed workflow evidence",
			summary: SeerrRequestSummary{SeerrState: "deleted", WorkflowCount: 1, ConfirmedCount: 1},
			want:    "plex_confirmed",
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
