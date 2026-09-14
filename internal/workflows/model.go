package workflows

import (
	"errors"
	"time"
	"uuid"
)

type State string

const (
	StateMonitored         State = "monitored"
	StateWaiting           State = "waiting"
	StateSearching         State = "searching"
	StateCandidateSelected State = "candidate_selected"
	StateSubmitting        State = "submitting"
	StateTransportUnknown  State = "transport_unknown"
	StateQueued            State = "queued"
	StateStreamingReady    State = "streaming_ready"
	StateImportComplete    State = "import_complete"
	StateVerifying         State = "verifying"
	StatePublishing        State = "publishing"
	StatePlexScanning      State = "plex_scanning"
	StatePlexConfirmed     State = "plex_confirmed"
	StateNeedsAttention    State = "needs_attention"
	StateHeld              State = "held"
	StateBlocked           State = "blocked"
	StateQuarantined       State = "quarantined"
	StateCancelled         State = "cancelled"
)

// AttentionRoundExhaustedWaitingNewIdentity is the durable attention code for
// a workflow that used both release-attributable acquisition slots and is
// parked in waiting so low-priority discovery can continue. It requires an
// explicit owner action before another acquisition round.
const AttentionRoundExhaustedWaitingNewIdentity = "round_exhausted_waiting_new_identity"

type Workflow struct {
	ID               uuid.UUID  `json:"id"`
	MediaID          uuid.UUID  `json:"media_id"`
	State            State      `json:"state"`
	RootID           uuid.UUID  `json:"root_id"`
	QualityProfileID uuid.UUID  `json:"quality_profile_id"`
	DownloadClientID uuid.UUID  `json:"download_client_id"`
	SelectedRelease  *uuid.UUID `json:"selected_release_id,omitempty"`
	GrabAttemptID    *uuid.UUID `json:"grab_attempt_id,omitempty"`
	AttentionCode    string     `json:"attention_code,omitempty"`
	AttentionDetail  string     `json:"attention_detail,omitempty"`
	// ReleaseAttemptFailed reports that this workflow has at least one durable
	// release-attributable failure exclusion. It is an internal read-model fact
	// and is never serialized.
	ReleaseAttemptFailed bool      `json:"-"`
	Revision             uint64    `json:"revision"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// OwnerAttentionState reports the state an owner-facing read should show. A
// workflow that is parked in waiting after a release-attributable failure (and
// therefore cannot advance without an explicit owner action or a later
// discovery-complete rescan) is reported as needs_attention. This keeps the
// durable waiting state that discovery depends on while making the required
// owner action visible to callers that only inspect State (for example the
// real-acceptance driver, which treats waiting as still in progress and
// needs_attention as terminal).
func (workflow Workflow) OwnerAttentionState() State {
	if workflow.State == StateWaiting && (workflow.AttentionCode == AttentionRoundExhaustedWaitingNewIdentity || workflow.ReleaseAttemptFailed) {
		return StateNeedsAttention
	}
	return workflow.State
}

type Transition struct {
	From   State     `json:"from"`
	To     State     `json:"to"`
	At     time.Time `json:"at"`
	Reason string    `json:"reason"`
	Actor  string    `json:"actor"`
}

var ErrInvalidTransition = errors.New("invalid workflow transition")

var allowedTransitions = map[State]map[State]struct{}{
	StateMonitored:         set(StateWaiting, StateSearching, StateCancelled, StateNeedsAttention),
	StateWaiting:           set(StateSearching, StateCandidateSelected, StateCancelled, StateNeedsAttention),
	StateSearching:         set(StateWaiting, StateCandidateSelected, StateHeld, StateNeedsAttention, StateCancelled),
	StateCandidateSelected: set(StateSubmitting, StateHeld, StateBlocked, StateCancelled, StateNeedsAttention),
	StateSubmitting:        set(StateQueued, StateTransportUnknown, StateNeedsAttention, StateCancelled),
	StateTransportUnknown:  set(StateQueued, StateImportComplete, StateNeedsAttention, StateCancelled),
	StateQueued:            set(StateStreamingReady, StateImportComplete, StateBlocked, StateNeedsAttention, StateCancelled),
	StateStreamingReady:    set(StateImportComplete, StateBlocked, StateNeedsAttention, StateCancelled),
	StateImportComplete:    set(StateVerifying, StateQuarantined, StateNeedsAttention),
	StateVerifying:         set(StatePublishing, StateQuarantined, StateNeedsAttention),
	StatePublishing:        set(StatePlexScanning, StateNeedsAttention),
	StatePlexScanning:      set(StatePlexConfirmed, StateNeedsAttention),
	StateNeedsAttention:    set(StateWaiting, StateSearching, StateCandidateSelected, StateSubmitting, StateVerifying, StatePublishing, StatePlexScanning, StateCancelled),
	StateHeld:              set(StateSearching, StateCandidateSelected, StateWaiting, StateCancelled),
	StateBlocked:           set(StateWaiting, StateCancelled),
	StateQuarantined:       set(StateVerifying, StateCancelled),
}

func set(states ...State) map[State]struct{} {
	result := make(map[State]struct{}, len(states))
	for _, state := range states {
		result[state] = struct{}{}
	}
	return result
}

func New(mediaID, rootID, profileID uuid.UUID, now time.Time) Workflow {
	return Workflow{ID: uuid.NewV7(), MediaID: mediaID, RootID: rootID, QualityProfileID: profileID, State: StateMonitored, Revision: 1, CreatedAt: now.UTC(), UpdatedAt: now.UTC()}
}

func NewWithClient(mediaID, rootID, profileID, clientID uuid.UUID, now time.Time) Workflow {
	workflow := New(mediaID, rootID, profileID, now)
	workflow.DownloadClientID = clientID
	return workflow
}

func (workflow *Workflow) Transition(to State, reason, actor string, now time.Time) (Transition, error) {
	if workflow.State == to {
		return Transition{From: workflow.State, To: to, At: now.UTC(), Reason: "idempotent: " + reason, Actor: actor}, nil
	}
	allowed, exists := allowedTransitions[workflow.State]
	if !exists {
		return Transition{}, ErrInvalidTransition
	}
	if _, ok := allowed[to]; !ok {
		return Transition{}, ErrInvalidTransition
	}
	transition := Transition{From: workflow.State, To: to, At: now.UTC(), Reason: reason, Actor: actor}
	workflow.State = to
	workflow.Revision++
	workflow.UpdatedAt = now.UTC()
	return transition, nil
}

func (workflow Workflow) Complete() bool { return workflow.State == StatePlexConfirmed }
