package indexers

import (
	"context"
	"errors"
	"hash/fnv"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Workload string

const (
	WorkloadFeed          Workload = "feed"
	WorkloadInteractive   Workload = "interactive"
	WorkloadCommissioning Workload = "commissioning"
	WorkloadRetrieval     Workload = "retrieval"
	WorkloadBackground    Workload = "background"
	// WorkloadBrowse is a viewer-facing indexer search. Unlike other
	// interactive acquisition traffic it waits for the remainder of the
	// per-indexer minimum interval while holding the lane, so a continuous
	// background recovery sweep cannot keep taking the next pacing slot.
	WorkloadBrowse Workload = "browse"
)

type Outcome string

const (
	OutcomeSuccess       Outcome = "success"
	OutcomeNoResults     Outcome = "no_results"
	OutcomeRateLimited   Outcome = "rate_limited"
	OutcomeAuthFailed    Outcome = "auth_failed"
	OutcomeSystemFailure Outcome = "system_failure"
	OutcomeCancelled     Outcome = "cancelled"
)

// FailureScope controls whether a failed request may back off the whole
// indexer. Operation-specific status remains the responsibility of the caller;
// the scheduler needs only enough scope to avoid penalizing unrelated work.
type FailureScope string

const (
	FailureScopeNone      FailureScope = "none"
	FailureScopeInstance  FailureScope = "instance"
	FailureScopeOperation FailureScope = "operation"
)

// RecordResult contains only protocol-neutral scheduling information. RetryAt
// is used as an explicit instance-wide rate-limit deadline when applicable.
type RecordResult struct {
	Outcome Outcome
	Scope   FailureScope
	RetryAt time.Time
}

type Budget struct {
	MinimumInterval time.Duration `json:"minimum_interval"`
	FailureBackoff  time.Duration `json:"failure_backoff"`
	MaximumBackoff  time.Duration `json:"maximum_backoff"`
	QueryLimit      int           `json:"query_limit"`
	GrabLimit       int           `json:"grab_limit"`
	LimitWindow     time.Duration `json:"limit_window"`
}

type Snapshot struct {
	IndexerID          string    `json:"indexer_id"`
	NextAllowedAt      time.Time `json:"next_allowed_at"`
	CooldownUntil      time.Time `json:"cooldown_until"`
	ConsecutiveFailure uint32    `json:"consecutive_failure"`
	LastOutcome        Outcome   `json:"last_outcome"`
	// LastOutcomeAt is the authoritative time of the last outcome-producing
	// request. It is separate from UpdatedAt, which is a read-model
	// observation timestamp that pacing-only writes also advance. A pre-send
	// pacing write must not refresh the apparent time of a historical
	// outcome. Durable separation is provided by migration 0035
	// (indexer_budget_state.last_outcome_at); a NULL durable value means the
	// time of the last outcome is not known and is never synthesized from
	// UpdatedAt.
	LastOutcomeAt time.Time `json:"last_outcome_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	// MinimumInterval is the scheduler configuration bound to this snapshot
	// read. Legacy reads carry the durable minimum_interval_ms; the
	// transmission-boundary cross-check compares it against the permit-bound
	// snapshot.
	MinimumInterval time.Duration `json:"minimum_interval"`
	// Revision is the durable indexer_budget_state revision. It is zero when
	// no row exists and is otherwise the exact persisted revision.
	Revision uint64 `json:"revision"`
}

type SnapshotStore interface {
	LoadIndexerBudget(context.Context, string) (Snapshot, error)
	SaveIndexerBudget(context.Context, Snapshot) error
}

type ReservationStore interface {
	ReserveIndexerRequest(context.Context, string, string, int, time.Duration, time.Time) (time.Time, error)
}

type Scheduler struct {
	mu     sync.Mutex
	lanes  map[string]*lane
	global chan struct{}
	store  SnapshotStore
	now    func() time.Time
	wait   func(context.Context, time.Duration) error
}

type lane struct {
	mu       sync.RWMutex
	gate     chan struct{}
	budget   Budget
	snapshot Snapshot
	loaded   bool
	// cacheUnknown is the fail-closed stale-cache barrier. When true, every
	// Acquire must reload authoritative durable state before another
	// transmission can occur, even when a fresh durable-gate check returns
	// NULL/clear. It is set while still owning the lane permit on uncertain
	// finalization or failed cache refresh, before recovery reload or lane
	// release. Normal cache operation is untouched: known-valid state does
	// not trigger a database reload on every acquisition.
	cacheUnknown bool
	gateSource   CommissioningGateSource
}

type Permit struct {
	scheduler *Scheduler
	lane      *lane
	indexerID string
	workload  Workload
	// commissioningBudget is populated ONLY by AcquireCommissioningV2. It is
	// zero for every permit from Acquire, so a legacy permit can never
	// authorize a commissioning durable write.
	commissioningBudget CommissioningBudgetSnapshot
	once                sync.Once
}

// CommissioningBudgetSnapshot returns the provenance-carrying scheduler
// configuration bound to this permit. Permits from Acquire yield the zero
// snapshot, which is always invalid.
func (permit *Permit) CommissioningBudgetSnapshot() CommissioningBudgetSnapshot {
	if permit == nil {
		return CommissioningBudgetSnapshot{}
	}
	return permit.commissioningBudget
}

var (
	ErrIndexerIDRequired = errors.New("indexer ID is required")
	ErrInvalidBudget     = errors.New("invalid indexer budget")
	ErrInvalidResult     = errors.New("invalid indexer scheduling result")
	// ErrInvalidCommissioningIdentity is returned by AcquireCommissioningV2
	// for every identity failure (empty, whitespace-only, non-canonical
	// spelling, malformed, either field), deterministically and
	// indistinguishably. Fixed message; contains no raw identity,
	// configuration value, provider text, or secret.
	ErrInvalidCommissioningIdentity = errors.New("invalid commissioning identity")
	// ErrCommissioningResultPath is returned when a commissioning permit is
	// used through the legacy scheduler result path. Commissioning permits
	// carry an admitted snapshot for the durable live path only; they must
	// never persist via SaveIndexerBudget. Fixed message; contains no IDs,
	// configuration values, provider text, or secrets.
	ErrCommissioningResultPath = errors.New("commissioning permit cannot record through the legacy scheduler result path")
)

const (
	ConservativeQueryLimit = 1000
	ConservativeGrabLimit  = 100
)

type CooldownError struct {
	Until time.Time
}

func (err CooldownError) Error() string      { return "indexer request is deferred by persisted pacing" }
func (err CooldownError) SafeCode() string   { return "indexer_pacing_deferred" }
func (err CooldownError) RetryAt() time.Time { return err.Until.UTC() }

type RequestBudgetError struct {
	Until time.Time
}

func (err RequestBudgetError) Error() string {
	return "indexer request is deferred by its rolling request budget"
}
func (err RequestBudgetError) SafeCode() string   { return "indexer_budget_deferred" }
func (err RequestBudgetError) RetryAt() time.Time { return err.Until.UTC() }

// CommissioningGateBlockedError is a closed fail-closed error: the durable
// unresolved-attempt gate is unresolved, or the gate could not be read. It
// carries no provider URL, header, or error text.
type CommissioningGateBlockedError struct {
	IndexerID string
}

func (err CommissioningGateBlockedError) Error() string {
	return "indexer request is blocked by unresolved commissioning accounting"
}
func (err CommissioningGateBlockedError) SafeCode() string {
	return "commissioning_accounting_unresolved"
}

func NewznabBudget(minimumIntervalMS int64, queryLimit, grabLimit int, windowSeconds int64) Budget {
	minimum := time.Duration(minimumIntervalMS) * time.Millisecond
	if minimum < 3*time.Second {
		minimum = 3 * time.Second
	}
	if queryLimit <= 0 {
		queryLimit = ConservativeQueryLimit
	}
	if grabLimit <= 0 {
		grabLimit = ConservativeGrabLimit
	}
	window := time.Duration(windowSeconds) * time.Second
	if window <= 0 {
		window = 24 * time.Hour
	}
	return Budget{MinimumInterval: minimum, FailureBackoff: 15 * time.Second, MaximumBackoff: 30 * time.Minute, QueryLimit: queryLimit, GrabLimit: grabLimit, LimitWindow: window}
}

func NewScheduler(globalLimit int, store SnapshotStore) *Scheduler {
	if globalLimit < 1 {
		globalLimit = 1
	}
	return &Scheduler{lanes: make(map[string]*lane), global: make(chan struct{}, globalLimit), store: store, now: time.Now, wait: waitForPacing}
}

func waitForPacing(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Maximum time a viewer-facing browse request may hold an indexer lane while
// waiting for its next permitted pacing slot. Longer waits are caused by a
// failure cooldown, which is reported rather than held.
const maxBrowsePacingWait = 10 * time.Second

// ValidBudget is Scheduler.Configure's predicate verbatim, extracted so
// callers outside the package can reject a configuration the scheduler itself
// would refuse. Configure calls it, so the two cannot drift.
func ValidBudget(budget Budget) bool {
	return !(budget.MinimumInterval < 0 || budget.FailureBackoff <= 0 || budget.MaximumBackoff < budget.FailureBackoff || budget.QueryLimit < 0 || budget.GrabLimit < 0 || (budget.QueryLimit > 0 || budget.GrabLimit > 0) && budget.LimitWindow <= 0)
}

// ValidCommissioningBudget adds what the commissioning workload requires: the
// architecture section 14 minimum-interval floor plus a usable query budget.
// The QueryLimit/LimitWindow requirements are what reject getLane's fabricated
// fallback budget (2s interval, zero limits), so an indexer whose lane was
// never Configure'd cannot obtain a commissioning permit.
//
// Note (N-3, documentation only, NOT a correctness dependency):
// indexers.NewznabBudget currently returns constant FailureBackoff = 15s and
// MaximumBackoff = 30m for every indexer; only MinimumInterval, QueryLimit,
// GrabLimit and LimitWindow vary per owner configuration. That constancy is
// why a cross-permit snapshot substitution could not have shortened a
// production cooldown before the MatchesAttempt binding existed. MatchesAttempt
// is what guarantees correctness and must hold if backoff values later become
// owner-configurable; do not encode 15s/30m into this predicate.
func ValidCommissioningBudget(budget Budget) bool {
	return ValidBudget(budget) && budget.MinimumInterval >= 3*time.Second && budget.QueryLimit > 0 && budget.LimitWindow > 0
}

func (scheduler *Scheduler) Configure(indexerID string, budget Budget) error {
	indexerID = strings.TrimSpace(indexerID)
	if indexerID == "" {
		return ErrIndexerIDRequired
	}
	if !ValidBudget(budget) {
		return ErrInvalidBudget
	}
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	current := scheduler.lanes[indexerID]
	if current == nil {
		current = &lane{gate: make(chan struct{}, 1)}
		scheduler.lanes[indexerID] = current
	}
	current.mu.Lock()
	current.budget = budget
	current.mu.Unlock()
	return nil
}

// SetCommissioningGateSource injects the durable unresolved-attempt gate
// source for one lane. Milestone A tests inject an in-memory source; the
// migration 0035 implementation must back this with durable storage checked
// on every acquisition (never a once-loaded cache). A nil source preserves
// legacy behavior.
func (scheduler *Scheduler) SetCommissioningGateSource(indexerID string, source CommissioningGateSource) {
	lane := scheduler.getLane(indexerID)
	lane.mu.Lock()
	lane.gateSource = source
	lane.mu.Unlock()
}

// Acquire is the only supported path to an indexer network request. The lane
// token is held until Record or Close, enforcing one request at a time even in
// commissioning and tests. When the lane cache is fail-closed unknown,
// Acquire reloads authoritative durable state while holding the lane before
// any transmission decision; reload failure or cancellation permits zero
// transmissions. A NULL/clear durable gate alone never authorizes acquisition
// while scheduling state is unknown.
func (scheduler *Scheduler) Acquire(ctx context.Context, indexerID string, workload Workload) (*Permit, error) {
	indexerID = strings.TrimSpace(indexerID)
	if indexerID == "" {
		return nil, ErrIndexerIDRequired
	}
	lane := scheduler.getLane(indexerID)
	select {
	case lane.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case scheduler.global <- struct{}{}:
	case <-ctx.Done():
		<-lane.gate
		return nil, ctx.Err()
	}

	if err := scheduler.load(ctx, indexerID, lane); err != nil {
		<-scheduler.global
		<-lane.gate
		return nil, err
	}
	lane.mu.RLock()
	unknown := lane.cacheUnknown
	source := lane.gateSource
	lane.mu.RUnlock()
	if unknown {
		if err := scheduler.ReloadLaneCache(ctx, indexerID); err != nil {
			<-scheduler.global
			<-lane.gate
			return nil, err
		}
		lane.mu.RLock()
		source = lane.gateSource
		lane.mu.RUnlock()
	}
	if !gateAllowsTransmission(ctx, source, indexerID) {
		<-scheduler.global
		<-lane.gate
		return nil, CommissioningGateBlockedError{IndexerID: indexerID}
	}
	now := scheduler.now().UTC()
	lane.mu.RLock()
	notBefore := lane.snapshot.NextAllowedAt
	cooldownActive := lane.snapshot.CooldownUntil.After(now)
	if lane.snapshot.CooldownUntil.After(notBefore) {
		notBefore = lane.snapshot.CooldownUntil
	}
	lane.mu.RUnlock()
	if notBefore.After(now) {
		// Browse waits for a pacing slot while retaining the lane. Releasing
		// the global slot during the wait keeps other indexers moving. A
		// failure cooldown, or a pacing interval longer than the browse
		// budget, is still reported immediately.
		if workload != WorkloadBrowse || cooldownActive || notBefore.Sub(now) > maxBrowsePacingWait {
			<-scheduler.global
			<-lane.gate
			return nil, CooldownError{Until: notBefore}
		}
		<-scheduler.global
		waitErr := scheduler.wait(ctx, notBefore.Sub(now))
		if waitErr == nil {
			select {
			case scheduler.global <- struct{}{}:
			case <-ctx.Done():
				waitErr = ctx.Err()
			}
		}
		if waitErr != nil {
			<-lane.gate
			return nil, waitErr
		}
		if err := ctx.Err(); err != nil {
			<-scheduler.global
			<-lane.gate
			return nil, err
		}
		// No other goroutine can advance this lane while its gate is held.
		// Re-read only to keep the decision explicit and testable.
		now = scheduler.now().UTC()
		lane.mu.RLock()
		notBefore = lane.snapshot.NextAllowedAt
		cooldownActive = lane.snapshot.CooldownUntil.After(now)
		if lane.snapshot.CooldownUntil.After(notBefore) {
			notBefore = lane.snapshot.CooldownUntil
		}
		lane.mu.RUnlock()
		if cooldownActive || notBefore.After(now) {
			<-scheduler.global
			<-lane.gate
			return nil, CooldownError{Until: notBefore}
		}
	}
	lane.mu.RLock()
	budget := lane.budget
	lane.mu.RUnlock()
	limit, family := requestLimit(workload, budget)
	if limit > 0 && budget.LimitWindow > 0 {
		if reservations, ok := scheduler.store.(ReservationStore); ok {
			retryAt, err := reservations.ReserveIndexerRequest(ctx, indexerID, family, limit, budget.LimitWindow, now)
			if err != nil {
				<-scheduler.global
				<-lane.gate
				return nil, err
			}
			if !retryAt.IsZero() {
				<-scheduler.global
				<-lane.gate
				return nil, RequestBudgetError{Until: retryAt}
			}
		}
	}
	return &Permit{scheduler: scheduler, lane: lane, indexerID: indexerID, workload: workload}, nil
}

// canonicalCommissioningID enforces the commissioning-v2 admission identity
// contract. It applies the same strings.TrimSpace the scheduler already uses,
// then requires the result to be ALREADY canonical lowercase UUID text:
// exactly 36 characters; hyphens at indices 8, 13, 18, 23; lowercase
// hexadecimal [0-9a-f] at every other index. It returns that string unchanged
// on success. It deliberately does not parse or normalize: uppercase,
// urn:uuid:-prefixed, brace-wrapped, 32-character unhyphenated, empty,
// whitespace-only and malformed inputs are all REJECTED rather than converted.
func canonicalCommissioningID(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if len(value) != 36 {
		return "", false
	}
	for index := 0; index < 36; index++ {
		character := value[index]
		switch index {
		case 8, 13, 18, 23:
			if character != '-' {
				return "", false
			}
		default:
			if character >= '0' && character <= '9' || character >= 'a' && character <= 'f' {
				continue
			}
			return "", false
		}
	}
	return value, true
}

// AcquireCommissioningV2 is the v2 executor's only acquisition path. It runs
// the identical lane/global/gate/cooldown sequence as Acquire and takes NO
// reservation of its own. An attempt is admitted, paced, reserved and
// accounted under exactly one scheduler configuration - the one in effect
// when its permit was acquired.
func (scheduler *Scheduler) AcquireCommissioningV2(ctx context.Context, indexerID, stepID string) (*Permit, error) {
	canonicalIndexerID, ok := canonicalCommissioningID(indexerID)
	if !ok {
		return nil, ErrInvalidCommissioningIdentity
	}
	canonicalStepID, ok := canonicalCommissioningID(stepID)
	if !ok {
		return nil, ErrInvalidCommissioningIdentity
	}
	lane := scheduler.getLane(canonicalIndexerID)
	select {
	case lane.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case scheduler.global <- struct{}{}:
	case <-ctx.Done():
		<-lane.gate
		return nil, ctx.Err()
	}
	if err := scheduler.load(ctx, canonicalIndexerID, lane); err != nil {
		<-scheduler.global
		<-lane.gate
		return nil, err
	}
	lane.mu.RLock()
	unknown := lane.cacheUnknown
	source := lane.gateSource
	lane.mu.RUnlock()
	if unknown {
		if err := scheduler.ReloadLaneCache(ctx, canonicalIndexerID); err != nil {
			<-scheduler.global
			<-lane.gate
			return nil, err
		}
		lane.mu.RLock()
		source = lane.gateSource
		lane.mu.RUnlock()
	}
	if !gateAllowsTransmission(ctx, source, canonicalIndexerID) {
		<-scheduler.global
		<-lane.gate
		return nil, CommissioningGateBlockedError{IndexerID: canonicalIndexerID}
	}
	now := scheduler.now().UTC()
	lane.mu.RLock()
	notBefore := lane.snapshot.NextAllowedAt
	if lane.snapshot.CooldownUntil.After(notBefore) {
		notBefore = lane.snapshot.CooldownUntil
	}
	budget := lane.budget
	lane.mu.RUnlock()
	if notBefore.After(now) {
		<-scheduler.global
		<-lane.gate
		return nil, CooldownError{Until: notBefore}
	}
	if !ValidCommissioningBudget(budget) {
		<-scheduler.global
		<-lane.gate
		return nil, ErrInvalidBudget
	}
	// Bind the SAME local budget value that passed ValidCommissioningBudget.
	// No second lane read: Configure between validation and capture must not
	// swap the bound configuration. The send cell is allocated exactly once
	// here: it is the single attempt-scoped one-use cell for this admitted
	// attempt, shared by every copy of the snapshot and every transmission
	// capability minted from it.
	snapshot := CommissioningBudgetSnapshot{budget: budget, indexerID: canonicalIndexerID, stepID: canonicalStepID, send: &commissioningSendCell{}}
	return &Permit{scheduler: scheduler, lane: lane, indexerID: canonicalIndexerID, workload: WorkloadCommissioning, commissioningBudget: snapshot}, nil
}

// Record is the compatibility path for existing callers. Its behavior remains
// instance-scoped for every failure and continues to understand Retry-After.
func (permit *Permit) Record(ctx context.Context, outcome Outcome, headers http.Header) error {
	scope := FailureScopeNone
	if failureOutcome(outcome) {
		scope = FailureScopeInstance
	}
	return permit.recordResult(ctx, RecordResult{Outcome: outcome, Scope: scope}, headers.Get("Retry-After"))
}

// RecordResult records a protocol-neutral result. Every result consumes the
// request and advances minimum pacing, but only an instance-scoped failure may
// change the shared failure count or cooldown.
func (permit *Permit) RecordResult(ctx context.Context, result RecordResult) error {
	// A commissioning permit must never reach the legacy persistence path,
	// even with an invalid RecordResult: route through the same guard so the
	// sentinel is returned and no SaveIndexerBudget occurs. Legacy permits
	// keep the existing invalid-result behavior below.
	if permit != nil && permit.commissioningBudget.Valid() {
		return permit.recordResult(ctx, result, "")
	}
	if !validRecordResult(result) {
		recordErr := permit.recordResult(ctx, RecordResult{Outcome: OutcomeSystemFailure, Scope: FailureScopeInstance}, "")
		return errors.Join(ErrInvalidResult, recordErr)
	}
	return permit.recordResult(ctx, result, "")
}

func validRecordResult(result RecordResult) bool {
	switch result.Outcome {
	case OutcomeSuccess, OutcomeNoResults, OutcomeCancelled:
		return result.Scope == FailureScopeNone && result.RetryAt.IsZero()
	case OutcomeRateLimited:
		return result.Scope == FailureScopeInstance
	case OutcomeAuthFailed:
		return result.Scope == FailureScopeInstance && result.RetryAt.IsZero()
	case OutcomeSystemFailure:
		return (result.Scope == FailureScopeInstance || result.Scope == FailureScopeOperation) && result.RetryAt.IsZero()
	default:
		return false
	}
}

func (permit *Permit) recordResult(ctx context.Context, result RecordResult, retryAfter string) error {
	var recordErr error
	// Commissioning permits are structurally barred from the legacy result
	// path: they must never persist via SaveIndexerBudget. The lane/global
	// ownership is still released exactly once through permit.once, and the
	// caller receives a fixed sanitized sentinel. ReleaseWithoutOutcome
	// remains the correct commissioning release mechanism; this guard only
	// covers callers that mistakenly use Record/RecordResult/Close.
	if permit != nil && permit.commissioningBudget.Valid() {
		permit.once.Do(func() {
			<-permit.scheduler.global
			<-permit.lane.gate
		})
		return ErrCommissioningResultPath
	}
	permit.once.Do(func() {
		now := permit.scheduler.now().UTC()
		permit.lane.mu.Lock()
		permit.lane.snapshot = computeOutcomeEffectLocked(permit.lane.snapshot, permit.lane.budget, permit.indexerID, result, retryAfter, now)
		snapshot := permit.lane.snapshot
		permit.lane.mu.Unlock()
		if permit.scheduler.store != nil {
			recordErr = permit.scheduler.store.SaveIndexerBudget(ctx, snapshot)
		}
		<-permit.scheduler.global
		<-permit.lane.gate
	})
	return recordErr
}

// ComputeOutcomeEffect is the single pure implementation of the pacing /
// backoff / cooldown rules shared by legacy Permit.recordResult and the
// commissioning completion path. It performs no I/O and mutates no scheduler
// state. backoff() is FNV-deterministic over (indexerID, failures), so the
// function is exactly reproducible.
func ComputeOutcomeEffect(start Snapshot, budget Budget, indexerID string, result RecordResult, retryAfter string, now time.Time) Snapshot {
	return computeOutcomeEffectLocked(start, budget, indexerID, result, retryAfter, now.UTC())
}

func computeOutcomeEffectLocked(start Snapshot, budget Budget, indexerID string, result RecordResult, retryAfter string, now time.Time) Snapshot {
	now = now.UTC()
	snapshot := start
	snapshot.IndexerID = indexerID
	if result.Scope != FailureScopeOperation {
		snapshot.LastOutcome = result.Outcome
		snapshot.LastOutcomeAt = now
	}
	snapshot.UpdatedAt = now
	snapshot.MinimumInterval = budget.MinimumInterval
	snapshot.NextAllowedAt = now.Add(budget.MinimumInterval)
	switch result.Outcome {
	case OutcomeSuccess, OutcomeNoResults:
		snapshot.ConsecutiveFailure = 0
	case OutcomeRateLimited:
		if result.Scope == FailureScopeInstance {
			snapshot.ConsecutiveFailure++
			deadline := result.RetryAt.UTC()
			if retryAfter != "" {
				if parsed, ok := ParseRetryAfter(retryAfter, now); ok {
					deadline = parsed
				}
			}
			if !deadline.IsZero() && deadline.After(snapshot.CooldownUntil) {
				snapshot.CooldownUntil = deadline
			} else {
				snapshot.CooldownUntil = now.Add(backoff(indexerID, snapshot.ConsecutiveFailure, budget))
			}
		}
	case OutcomeSystemFailure:
		if result.Scope == FailureScopeInstance {
			snapshot.ConsecutiveFailure++
			snapshot.CooldownUntil = now.Add(backoff(indexerID, snapshot.ConsecutiveFailure, budget))
		}
	case OutcomeAuthFailed:
		if result.Scope == FailureScopeInstance {
			snapshot.ConsecutiveFailure++
			snapshot.CooldownUntil = now.Add(budget.MaximumBackoff)
		}
	case OutcomeCancelled:
		// A cancelled request still consumes the normal minimum interval.
	}
	return snapshot
}

// ComputeCommissioningOutcomeEffect applies the scheduler's outcome rules
// using the configuration captured by an admitted commissioning permit. It
// delegates to ComputeOutcomeEffect with the snapshot's PRIVATE budget, so
// there is exactly one implementation of the rules and *postgres.Store never
// names indexers.Budget on this path. The classified result already carries
// the parsed bounded RetryAt, so provider header text cannot reach this path.
//
// Commissioning-only RetryAt ceiling (not legacy semantics): an unbounded
// classified RetryAt is clamped to now + snapshot.MaximumBackoff() before
// delegation. Classification stays independent of BudgetSnapshot (it is not
// an input to ClassifyLiveAttempt), ComputeOutcomeEffect itself is
// unchanged, and the durable GREATEST merge still preserves any stronger
// existing cooldown.
func ComputeCommissioningOutcomeEffect(start Snapshot, snapshot CommissioningBudgetSnapshot, result RecordResult, now time.Time) (Snapshot, error) {
	if !snapshot.Valid() {
		return Snapshot{}, ErrInvalidBudget
	}
	now = now.UTC()
	if !result.RetryAt.IsZero() {
		ceiling := now.Add(snapshot.MaximumBackoff())
		if result.RetryAt.After(ceiling) {
			result.RetryAt = ceiling
		}
	}
	return ComputeOutcomeEffect(start, snapshot.budget, start.IndexerID, result, "", now), nil
}

func failureOutcome(outcome Outcome) bool {
	switch outcome {
	case OutcomeRateLimited, OutcomeAuthFailed, OutcomeSystemFailure:
		return true
	default:
		return false
	}
}

func requestLimit(workload Workload, budget Budget) (int, string) {
	if workload == WorkloadRetrieval {
		return budget.GrabLimit, "grab"
	}
	if workload == WorkloadBackground {
		limit := budget.QueryLimit / 5
		if limit < 1 {
			limit = 1
		}
		// Use the same persisted family as interactive queries so background
		// work can use at most the first 20% of the provider's total allowance.
		return limit, "query"
	}
	return budget.QueryLimit, "query"
}

func (permit *Permit) Close() { _ = permit.Record(context.Background(), OutcomeCancelled, nil) }

// ReleaseWithoutOutcome releases the lane and global slot without an
// outcome-recording Record/RecordResult write that could overwrite already
// committed state. It is used on the uncertain-finalization path after the
// complete lane cache has been invalidated: the committed cooldown/pacing
// floor must survive, and the next Acquire must reload authoritative state.
func (permit *Permit) ReleaseWithoutOutcome() {
	permit.once.Do(func() {
		<-permit.scheduler.global
		<-permit.lane.gate
	})
}

func (scheduler *Scheduler) Snapshot(indexerID string) Snapshot {
	lane := scheduler.getLane(indexerID)
	lane.mu.RLock()
	defer lane.mu.RUnlock()
	return lane.snapshot
}

func (scheduler *Scheduler) getLane(indexerID string) *lane {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	current := scheduler.lanes[indexerID]
	if current == nil {
		current = &lane{gate: make(chan struct{}, 1), budget: Budget{MinimumInterval: 2 * time.Second, FailureBackoff: 15 * time.Second, MaximumBackoff: 30 * time.Minute}}
		scheduler.lanes[indexerID] = current
	}
	return current
}

func (scheduler *Scheduler) load(ctx context.Context, indexerID string, lane *lane) error {
	lane.mu.Lock()
	if lane.loaded {
		lane.mu.Unlock()
		return nil
	}
	lane.loaded = true
	if scheduler.store == nil {
		lane.snapshot.IndexerID = indexerID
		lane.mu.Unlock()
		return nil
	}
	lane.mu.Unlock()
	snapshot, err := scheduler.store.LoadIndexerBudget(ctx, indexerID)
	if err != nil {
		lane.mu.Lock()
		lane.loaded = false
		lane.mu.Unlock()
		return err
	}
	if snapshot.IndexerID == "" {
		snapshot.IndexerID = indexerID
	}
	lane.mu.Lock()
	lane.snapshot = snapshot
	lane.mu.Unlock()
	return nil
}

func ParseRetryAfter(value string, now time.Time) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds < 0 {
			return time.Time{}, false
		}
		return now.Add(time.Duration(seconds) * time.Second).UTC(), true
	}
	parsed, err := http.ParseTime(value)
	if err != nil {
		return time.Time{}, false
	}
	return parsed.UTC(), true
}

func backoff(indexerID string, failures uint32, budget Budget) time.Duration {
	base := budget.FailureBackoff
	for i := uint32(1); i < failures && base < budget.MaximumBackoff/2; i++ {
		base *= 2
	}
	if base > budget.MaximumBackoff {
		base = budget.MaximumBackoff
	}
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(indexerID))
	_, _ = hash.Write([]byte{byte(failures), byte(failures >> 8)})
	half := base / 2
	if half <= 0 {
		return base
	}
	return half + time.Duration(hash.Sum64()%uint64(half+1))
}
