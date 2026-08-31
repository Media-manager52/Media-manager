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

type Budget struct {
	MinimumInterval time.Duration `json:"minimum_interval"`
	FailureBackoff  time.Duration `json:"failure_backoff"`
	MaximumBackoff  time.Duration `json:"maximum_backoff"`
}

type Snapshot struct {
	IndexerID          string    `json:"indexer_id"`
	NextAllowedAt      time.Time `json:"next_allowed_at"`
	CooldownUntil      time.Time `json:"cooldown_until"`
	ConsecutiveFailure uint32    `json:"consecutive_failure"`
	LastOutcome        Outcome   `json:"last_outcome"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type SnapshotStore interface {
	LoadIndexerBudget(context.Context, string) (Snapshot, error)
	SaveIndexerBudget(context.Context, Snapshot) error
}

type Scheduler struct {
	mu     sync.Mutex
	lanes  map[string]*lane
	global chan struct{}
	store  SnapshotStore
	now    func() time.Time
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
	indexerID string
	workload  Workload
	once      sync.Once
}

var (
	ErrIndexerIDRequired = errors.New("indexer ID is required")
	ErrInvalidBudget     = errors.New("invalid indexer budget")
)

type CooldownError struct {
	Until time.Time
}

func (err CooldownError) Error() string      { return "indexer request is deferred by persisted pacing" }
func (err CooldownError) SafeCode() string   { return "indexer_pacing_deferred" }
func (err CooldownError) RetryAt() time.Time { return err.Until.UTC() }

func NewScheduler(globalLimit int, store SnapshotStore) *Scheduler {
	if globalLimit < 1 {
		globalLimit = 1
	}
	return &Scheduler{lanes: make(map[string]*lane), global: make(chan struct{}, globalLimit), store: store, now: time.Now}
}

func (scheduler *Scheduler) Configure(indexerID string, budget Budget) error {
	indexerID = strings.TrimSpace(indexerID)
	if indexerID == "" {
		return ErrIndexerIDRequired
	}
	if budget.MinimumInterval < 0 || budget.FailureBackoff <= 0 || budget.MaximumBackoff < budget.FailureBackoff {
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
	now := scheduler.now().UTC()
	lane.mu.RLock()
	notBefore := lane.snapshot.NextAllowedAt
	if lane.snapshot.CooldownUntil.After(notBefore) {
		notBefore = lane.snapshot.CooldownUntil
	}
	lane.mu.RUnlock()
	if notBefore.After(now) {
		<-scheduler.global
		<-lane.gate
		return nil, CooldownError{Until: notBefore}
	}
	return &Permit{scheduler: scheduler, lane: lane, indexerID: indexerID, workload: workload}, nil
}

func (permit *Permit) Record(ctx context.Context, outcome Outcome, headers http.Header) error {
	var result error
	permit.once.Do(func() {
		now := permit.scheduler.now().UTC()
		permit.lane.mu.Lock()
		snapshot := permit.lane.snapshot
		budget := permit.lane.budget
		snapshot.IndexerID = permit.indexerID
		snapshot.LastOutcome = outcome
		snapshot.UpdatedAt = now
		snapshot.NextAllowedAt = now.Add(budget.MinimumInterval)
		switch outcome {
		case OutcomeSuccess, OutcomeNoResults:
			snapshot.ConsecutiveFailure = 0
		case OutcomeRateLimited:
			snapshot.ConsecutiveFailure++
			if deadline, ok := ParseRetryAfter(headers.Get("Retry-After"), now); ok && deadline.After(snapshot.CooldownUntil) {
				snapshot.CooldownUntil = deadline
			} else {
				snapshot.CooldownUntil = now.Add(backoff(permit.indexerID, snapshot.ConsecutiveFailure, budget))
			}
		case OutcomeSystemFailure:
			snapshot.ConsecutiveFailure++
			snapshot.CooldownUntil = now.Add(backoff(permit.indexerID, snapshot.ConsecutiveFailure, budget))
		case OutcomeAuthFailed:
			snapshot.ConsecutiveFailure++
			snapshot.CooldownUntil = now.Add(budget.MaximumBackoff)
		case OutcomeCancelled:
			// A cancelled request still consumes the normal minimum interval.
		}
		permit.lane.snapshot = snapshot
		permit.lane.mu.Unlock()
		if permit.scheduler.store != nil {
			result = permit.scheduler.store.SaveIndexerBudget(ctx, snapshot)
		}
		<-permit.scheduler.global
		<-permit.lane.gate
	})
	return result
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
