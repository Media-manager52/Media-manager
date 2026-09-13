package indexers

import (
	"context"
	"strings"
	"sync/atomic"
	"time"
)

// CommissioningBudgetSnapshot is the provenance-carrying scheduler
// configuration for ONE admitted commissioning attempt. It is exported so the
// executor and repository can carry it across package boundaries, but its
// contents are unexported: outside internal/indexers the only way to obtain a
// non-zero value is Permit.CommissioningBudgetSnapshot() on a permit admitted
// through AcquireCommissioningV2, and the value carries the exact indexer and
// step it was issued for.
//
// There is no constructor from a Budget, no setter, no exported field, no
// rebinding API and no identity getter. The zero value is constructible (as
// for every exported Go struct type) and is ALWAYS invalid.
type CommissioningBudgetSnapshot struct {
	budget    Budget
	indexerID string // canonical, validated at admission
	stepID    string // canonical, validated at admission
	// send is the single attempt-scoped transmission-authority cell,
	// allocated exactly once by AcquireCommissioningV2. It is pointer-backed
	// so every copy of the snapshot aliases the same cell; the zero value
	// has nil and is always unusable. ConsumeCommissioningTransmission is
	// the only consume path and lives in this package.
	send *commissioningSendCell
}

// commissioningSendCell is the one-use attempt send cell shared by every
// copy of one admitted snapshot and every capability minted from it.
type commissioningSendCell struct {
	consumed atomic.Bool
}

// ConsumeCommissioningTransmission consumes the attempt's single
// transmission authority. It reports true exactly once per admitted attempt
// across all snapshot copies and all minted capabilities. The zero/legacy
// snapshot (nil cell) always reports false. This is the ONLY consume path;
// the newznab capability calls it rather than owning its own cell.
func (snapshot CommissioningBudgetSnapshot) ConsumeCommissioningTransmission() bool {
	cell := snapshot.send
	if cell == nil {
		return false
	}
	return cell.consumed.CompareAndSwap(false, true)
}

// TransmissionConsumed is the non-mutating observability read of the
// attempt's send cell. It reports true once ConsumeCommissioningTransmission
// has succeeded for this attempt, false for fresh attempts and for
// zero/legacy snapshots (nil cell). It never mutates the cell.
func (snapshot CommissioningBudgetSnapshot) TransmissionConsumed() bool {
	cell := snapshot.send
	if cell == nil {
		return false
	}
	return cell.consumed.Load()
}

func (snapshot CommissioningBudgetSnapshot) MinimumInterval() time.Duration {
	return snapshot.budget.MinimumInterval
}

func (snapshot CommissioningBudgetSnapshot) FailureBackoff() time.Duration {
	return snapshot.budget.FailureBackoff
}

func (snapshot CommissioningBudgetSnapshot) MaximumBackoff() time.Duration {
	return snapshot.budget.MaximumBackoff
}

func (snapshot CommissioningBudgetSnapshot) QueryLimit() int {
	return snapshot.budget.QueryLimit
}

func (snapshot CommissioningBudgetSnapshot) GrabLimit() int {
	return snapshot.budget.GrabLimit
}

func (snapshot CommissioningBudgetSnapshot) LimitWindow() time.Duration {
	return snapshot.budget.LimitWindow
}

// Valid is exactly ValidCommissioningBudget over the private budget, so the
// zero snapshot and any non-commissioning lane configuration are rejected.
func (snapshot CommissioningBudgetSnapshot) Valid() bool {
	return ValidCommissioningBudget(snapshot.budget)
}

// MatchesAttempt reports whether this snapshot was issued by
// AcquireCommissioningV2 for exactly this indexer and commissioning step.
// True only when all three hold: s.Valid(), TrimSpace(indexerID) equals the
// identity captured at admission, TrimSpace(stepID) equals the identity
// captured at admission. The stored identities are GUARANTEED canonical, so
// exact equality is the correct comparison. The zero snapshot never matches.
// No mutation or rebinding path exists.
func (snapshot CommissioningBudgetSnapshot) MatchesAttempt(indexerID, stepID string) bool {
	if !snapshot.Valid() {
		return false
	}
	return strings.TrimSpace(indexerID) == snapshot.indexerID && strings.TrimSpace(stepID) == snapshot.stepID
}

// AuthoritativeSnapshot is the complete durable scheduling decision state for
// one indexer lane: pacing, cooldown, failure history, outcome, revision, and
// the unresolved-attempt gate with its referenced step state.
type AuthoritativeSnapshot struct {
	Snapshot
	Revision        uint64
	Gate            CommissioningGateState
	GateStepID      string
	GateStepState   string
	GateStepOutcome string
}

// AuthoritativeStore loads the complete authoritative durable state in ONE
// statement (one MVCC snapshot). Implementations must return a definite
// clear zero snapshot with nil error when no indexer_budget_state row exists;
// actual database/query failures remain fail-closed errors.
type AuthoritativeStore interface {
	LoadAuthoritativeIndexerState(context.Context, string) (AuthoritativeSnapshot, error)
}
