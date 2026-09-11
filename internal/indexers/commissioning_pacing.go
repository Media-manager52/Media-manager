package indexers

import (
	"context"
	"sync"
	"time"
)

// Commissioning pre-send pacing and same-process stale-cache substrate.
//
// The durable unresolved-attempt gate and outcome-time separation require
// migration 0035, which has NOT received separate review and is therefore NOT
// created here. This file implements everything that can safely precede that
// blocker without schema changes:
//
//   - durable pre-send pacing floor computation (deadline + minimum interval)
//     so a delayed socket write cannot cause an early post-restart request;
//   - LastOutcomeAt/observation-time separation in memory (a pacing-only
//     write must not refresh the apparent time of a historical outcome);
//   - fail-closed same-process lane cache invalidation/reload barrier:
//     after uncertain finalization or failed cache refresh, the complete lane
//     scheduling cache is marked unknown while still owning the lane, and
//     every subsequent Acquire (legacy or v2) reloads authoritative durable
//     state before another transmission can occur. A NULL unresolved-attempt
//     gate alone NEVER authorizes acquisition while scheduling state is
//     unknown; reload failure or cancellation permits ZERO transmissions and
//     never falls back to stale memory or default values. Retrying the
//     database READ is allowed; retrying the commissioning NETWORK REQUEST
//     is not.
//
// Gate checks themselves MUST read durable storage on every acquisition once
// migration 0035 exists (Gate checks must not use a once-loaded cache). Until
// then, GateState is an explicit process-local unresolved-accounting marker
// used only by local Milestone A tests; Acquire consults it fail-closed when
// present, and production behavior with a nil gate source is unchanged.
type CommissioningGateState string

const (
	CommissioningGateUnknown    CommissioningGateState = ""
	CommissioningGateClear      CommissioningGateState = "clear"
	CommissioningGateUnresolved CommissioningGateState = "unresolved"
)

// CommissioningGateSource reports the durable unresolved-attempt gate for an
// indexer. It returns (state, true) when the gate value is authoritatively
// known and (any, false) when the gate could not be read; a read failure
// fails closed. Milestone A tests inject an in-memory source. The migration
// 0035 implementation must back this with the durable gate column.
type CommissioningGateSource interface {
	CommissioningGate(ctx context.Context, indexerID string) (CommissioningGateState, bool)
}

// PacingFloor returns the durable pre-send pacing floor for a transmission
// committed with the given absolute execution deadline: deadline plus the
// configured minimum interval, preserving any longer existing restriction.
// Merely writing now plus the minimum interval before a delayed socket write
// does not protect a late send.
func PacingFloor(deadline time.Time, minimumInterval time.Duration, existing NextAllowedHint) time.Time {
	floor := deadline.UTC().Add(minimumInterval)
	if existing.At.After(floor) {
		return existing.At.UTC()
	}
	return floor
}

// NextAllowedHint carries an existing durable NextAllowedAt restriction.
type NextAllowedHint struct {
	At time.Time
}

// LaneCacheInvalidation coordinates deterministic test proof that cache
// invalidation happened before lane release. Production code calls
// InvalidateLaneCache while still owning the lane permit; tests observe the
// Invalidated channel before the lane becomes available.
type LaneCacheInvalidation struct {
	Invalidated chan struct{}
	once        sync.Once
}

func (signal *LaneCacheInvalidation) fire() {
	if signal == nil || signal.Invalidated == nil {
		return
	}
	signal.once.Do(func() { close(signal.Invalidated) })
}

// InvalidateLaneCache marks the complete scheduling cache for one indexer
// lane unknown while still owning the lane permit. It must be called before
// attempting recovery reload or making the lane available; no release path
// may expose the old cache as usable. Retained old values are not decision
// inputs. Other indexers are unaffected.
func (scheduler *Scheduler) InvalidateLaneCache(indexerID string, signal *LaneCacheInvalidation) {
	lane := scheduler.getLane(indexerID)
	lane.mu.Lock()
	lane.snapshot = Snapshot{IndexerID: indexerID}
	lane.loaded = false
	lane.cacheUnknown = true
	lane.mu.Unlock()
	signal.fire()
}

// ReloadLaneCache loads a consistent authoritative snapshot of all durable
// scheduling decision state (NextAllowedAt, CooldownUntil, unresolved gate,
// remaining budget/outcome state) and publishes the complete snapshot under
// the lane mutex before marking it valid. A failed or cancelled reload leaves
// the cache unknown and permits zero transmissions.
func (scheduler *Scheduler) ReloadLaneCache(ctx context.Context, indexerID string) error {
	lane := scheduler.getLane(indexerID)
	if scheduler.store == nil {
		return context.DeadlineExceeded
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	snapshot, err := scheduler.store.LoadIndexerBudget(ctx, indexerID)
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if snapshot.IndexerID == "" {
		snapshot.IndexerID = indexerID
	}
	lane.mu.Lock()
	lane.snapshot = snapshot
	lane.loaded = true
	lane.cacheUnknown = false
	lane.mu.Unlock()
	return nil
}

// laneCacheUnknown reports whether the lane cache is fail-closed unknown.
func (scheduler *Scheduler) laneCacheUnknown(indexerID string) bool {
	lane := scheduler.getLane(indexerID)
	lane.mu.RLock()
	defer lane.mu.RUnlock()
	return lane.cacheUnknown
}

// gateAllowsTransmission consults the gate source fail-closed. A read failure
// fails closed. A nil source means no gate storage is configured (schema 34):
// legacy behavior is unchanged.
func gateAllowsTransmission(ctx context.Context, source CommissioningGateSource, indexerID string) bool {
	if source == nil {
		return true
	}
	state, ok := source.CommissioningGate(ctx, indexerID)
	if !ok {
		return false
	}
	return state != CommissioningGateUnresolved
}

// MarkLanePacingFloor durably advances only the pacing floor (NextAllowedAt)
// without refreshing the apparent time of a historical outcome: LastOutcome
// and LastOutcomeAt are preserved. Callers must persist via SaveIndexerBudget.
func MarkLanePacingFloor(snapshot Snapshot, floor time.Time) Snapshot {
	if floor.After(snapshot.NextAllowedAt) {
		snapshot.NextAllowedAt = floor.UTC()
	}
	return snapshot
}
