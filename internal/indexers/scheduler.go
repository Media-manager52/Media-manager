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
	// WorkloadFeed reads an indexer's newest uploads. Like browse it waits for
	// the next pacing slot; it is charged to the query allowance.
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
	// browse holds the in-flight slots for viewer stream lists. A list asks
	// every indexer at once, so sharing the four background slots made it
	// wait for two or three rounds of requests (6-11 s in production) behind
	// background searches. Per-indexer pacing, the one-request-per-indexer
	// lane and the query budgets still apply unchanged.
	browse chan struct{}
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
}

type Permit struct {
	scheduler *Scheduler
	lane      *lane
	// global is the in-flight pool this permit holds a slot in; nil means
	// the scheduler's global pool.
	global    chan struct{}
	indexerID string
	workload  Workload
	once      sync.Once
}

var (
	ErrIndexerIDRequired = errors.New("indexer ID is required")
	ErrInvalidBudget     = errors.New("invalid indexer budget")
	ErrInvalidResult     = errors.New("invalid indexer scheduling result")
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
	return &Scheduler{lanes: make(map[string]*lane), global: make(chan struct{}, globalLimit), browse: make(chan struct{}, BrowseConcurrency), store: store, now: time.Now, wait: waitForPacing}
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

// BrowseConcurrency is how many viewer stream-list requests may be in flight
// across all indexers, separately from the global limit for other work.
const BrowseConcurrency = 16

// slots returns the in-flight pool a workload draws from.
func (scheduler *Scheduler) slots(workload Workload) chan struct{} {
	if workload == WorkloadBrowse && scheduler.browse != nil {
		return scheduler.browse
	}
	return scheduler.global
}

// waitsForPacing reports whether a workload waits for the indexer's next
// pacing slot while holding its lane instead of deferring. Browse does so for
// the viewer; the newest-uploads feed does so because it is one small request
// every few minutes that would otherwise keep losing the slot to the steady
// stream of searches. Pacing and failure cooldowns are honoured unchanged.
func waitsForPacing(workload Workload) bool {
	return workload == WorkloadBrowse || workload == WorkloadFeed
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

// Acquire is the only supported path to an indexer network request. The lane
// token is held until Record or Close, enforcing one request at a time even in
// commissioning and tests.
func (scheduler *Scheduler) Acquire(ctx context.Context, indexerID string, workload Workload) (*Permit, error) {
	indexerID = strings.TrimSpace(indexerID)
	if indexerID == "" {
		return nil, ErrIndexerIDRequired
	}
	lane := scheduler.getLane(indexerID)
	global := scheduler.slots(workload)
	select {
	case lane.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case global <- struct{}{}:
	case <-ctx.Done():
		<-lane.gate
		return nil, ctx.Err()
	}

	if err := scheduler.load(ctx, indexerID, lane); err != nil {
		<-global
		<-lane.gate
		return nil, err
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
		if !waitsForPacing(workload) || cooldownActive || notBefore.Sub(now) > maxBrowsePacingWait {
			<-global
			<-lane.gate
			return nil, CooldownError{Until: notBefore}
		}
		<-global
		waitErr := scheduler.wait(ctx, notBefore.Sub(now))
		if waitErr == nil {
			select {
			case global <- struct{}{}:
			case <-ctx.Done():
				waitErr = ctx.Err()
			}
		}
		if waitErr != nil {
			<-lane.gate
			return nil, waitErr
		}
		if err := ctx.Err(); err != nil {
			<-global
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
			<-global
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
				<-global
				<-lane.gate
				return nil, err
			}
			if !retryAt.IsZero() {
				<-global
				<-lane.gate
				return nil, RequestBudgetError{Until: retryAt}
			}
		}
	}
	return &Permit{scheduler: scheduler, lane: lane, global: global, indexerID: indexerID, workload: workload}, nil
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
	permit.once.Do(func() {
		now := permit.scheduler.now().UTC()
		permit.lane.mu.Lock()
		permit.lane.snapshot = computeOutcomeEffectLocked(permit.lane.snapshot, permit.lane.budget, permit.indexerID, result, retryAfter, now)
		snapshot := permit.lane.snapshot
		permit.lane.mu.Unlock()
		if permit.scheduler.store != nil {
			recordErr = permit.scheduler.store.SaveIndexerBudget(ctx, snapshot)
		}
		<-permit.slots()
		<-permit.lane.gate
	})
	return recordErr
}

// computeOutcomeEffectLocked is the single pure implementation of the pacing /
// backoff / cooldown rules. It performs no I/O and mutates no scheduler
// state. backoff() is FNV-deterministic over (indexerID, failures), so the
// function is exactly reproducible.
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
					// The provider's wait is bounded: never past the maximum
					// backoff (a year-long Retry-After would block the indexer
					// until someone cleared it), and a wait that is already
					// over (Retry-After: 0) falls back to the normal backoff.
					if ceiling := now.Add(budget.MaximumBackoff); budget.MaximumBackoff > 0 && parsed.After(ceiling) {
						parsed = ceiling
					}
					if !parsed.After(now) {
						parsed = time.Time{}
					}
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

func failureOutcome(outcome Outcome) bool {
	switch outcome {
	case OutcomeRateLimited, OutcomeAuthFailed, OutcomeSystemFailure:
		return true
	default:
		return false
	}
}

// BackgroundQueryLimit is the part of an indexer's query allowance that
// background discovery may use: the first 60%. The remaining 40% stays
// reserved for searches the owner or a new request starts. Measured use was
// about 3,000 queries a day against allowances of 10,000-20,000, so the
// earlier 20% share deferred most background searches with quota unused.
func BackgroundQueryLimit(queryLimit int) int {
	limit := int(int64(queryLimit) * 3 / 5)
	if limit < 1 {
		limit = 1
	}
	return limit
}

func requestLimit(workload Workload, budget Budget) (int, string) {
	if workload == WorkloadRetrieval {
		return budget.GrabLimit, "grab"
	}
	if workload == WorkloadBackground {
		// Use the same persisted family as interactive queries so background
		// work can use at most the first part of the provider's total allowance.
		return BackgroundQueryLimit(budget.QueryLimit), "query"
	}
	return budget.QueryLimit, "query"
}

func (permit *Permit) slots() chan struct{} {
	if permit.global != nil {
		return permit.global
	}
	return permit.scheduler.global
}

func (permit *Permit) Close() { _ = permit.Record(context.Background(), OutcomeCancelled, nil) }

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
