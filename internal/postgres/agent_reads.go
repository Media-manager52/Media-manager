package postgres

import (
	"context"
	"encoding/json"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/releases"
)

// Read models an operator (or agent) previously had to reconstruct with SQL:
// indexer budgets and usage, the background queue, and per-workflow search
// history and candidate decisions. All are read-only.

type IndexerBudgetView struct {
	QueryLimit           int    `json:"query_limit"`
	GrabLimit            int    `json:"grab_limit"`
	BackgroundQueryLimit int    `json:"background_query_limit"`
	WindowSeconds        int64  `json:"window_seconds"`
	MinimumIntervalMS    int64  `json:"minimum_interval_ms"`
	Source               string `json:"source,omitempty"`
}

type IndexerUsageWindow struct {
	QueriesUsed                int        `json:"queries_used"`
	GrabsUsed                  int        `json:"grabs_used"`
	QueriesRemaining           int        `json:"queries_remaining"`
	GrabsRemaining             int        `json:"grabs_remaining"`
	BackgroundQueriesRemaining int        `json:"background_queries_remaining"`
	OldestReservationAt        *time.Time `json:"oldest_reservation_at,omitempty"`
}

type IndexerPacingView struct {
	NextAllowedAt       *time.Time `json:"next_allowed_at,omitempty"`
	CooldownUntil       *time.Time `json:"cooldown_until,omitempty"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	LastOutcome         string     `json:"last_outcome,omitempty"`
	LastOutcomeAt       *time.Time `json:"last_outcome_at,omitempty"`
}

type IndexerOutcomeCount struct {
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
	Count  int    `json:"count"`
}

type IndexerUsage struct {
	ID                uuid.UUID             `json:"id"`
	Name              string                `json:"name"`
	Kind              string                `json:"kind"`
	Enabled           bool                  `json:"enabled"`
	Commissioned      bool                  `json:"commissioned"`
	Budget            IndexerBudgetView     `json:"budget"`
	Usage             IndexerUsageWindow    `json:"usage"`
	Pacing            IndexerPacingView     `json:"pacing"`
	SearchOutcomes24h []IndexerOutcomeCount `json:"search_outcomes_24h"`
}

// IndexerUsages reports every Newznab and native indexer with the effective
// budget the scheduler enforces, what the rolling window has used, pacing
// state and the last 24 hours of per-search outcomes.
func (store *Store) IndexerUsages(ctx context.Context, now time.Time) ([]IndexerUsage, error) {
	rows, err := store.Pool.Query(ctx, `SELECT i.id::text,i.name,i.kind,i.enabled,COALESCE(i.config->'mode_verified'='true'::jsonb,false),i.config,
		s.next_allowed_at,s.cooldown_until,COALESCE(s.consecutive_failure,0),COALESCE(s.last_outcome,''),s.last_outcome_at
		FROM integration_instances i LEFT JOIN indexer_budget_state s ON s.indexer_instance_id=i.id
		WHERE i.kind IN ('newznab','oldboys') ORDER BY i.enabled DESC,i.name,i.id`)
	if err != nil {
		return nil, err
	}
	result := []IndexerUsage{}
	for rows.Next() {
		var item IndexerUsage
		var idText string
		var config []byte
		if err := rows.Scan(&idText, &item.Name, &item.Kind, &item.Enabled, &item.Commissioned, &config, &item.Pacing.NextAllowedAt, &item.Pacing.CooldownUntil, &item.Pacing.ConsecutiveFailures, &item.Pacing.LastOutcome, &item.Pacing.LastOutcomeAt); err != nil {
			rows.Close()
			return nil, err
		}
		item.ID, _ = uuid.Parse(idText)
		var configured struct {
			MinimumIntervalMS int64  `json:"minimum_interval_ms"`
			QueryLimit        int    `json:"query_limit"`
			GrabLimit         int    `json:"grab_limit"`
			WindowSeconds     int64  `json:"budget_window_seconds"`
			Source            string `json:"budget_source"`
		}
		_ = json.Unmarshal(config, &configured)
		// The same constructor search and retrieval use, so defaults match.
		budget := indexers.NewznabBudget(configured.MinimumIntervalMS, configured.QueryLimit, configured.GrabLimit, configured.WindowSeconds)
		item.Budget = IndexerBudgetView{QueryLimit: budget.QueryLimit, GrabLimit: budget.GrabLimit, BackgroundQueryLimit: max(budget.QueryLimit/5, 1), WindowSeconds: int64(budget.LimitWindow / time.Second), MinimumIntervalMS: budget.MinimumInterval.Milliseconds(), Source: configured.Source}
		result = append(result, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	byID := map[string]*IndexerUsage{}
	for index := range result {
		item := &result[index]
		byID[item.ID.String()] = item
		windowStart := now.Add(-time.Duration(item.Budget.WindowSeconds) * time.Second)
		usage, err := store.Pool.Query(ctx, `SELECT request_family,count(*),min(reserved_at) FROM indexer_request_reservations WHERE indexer_instance_id=$1 AND reserved_at>$2 GROUP BY 1`, item.ID.String(), windowStart)
		if err != nil {
			return nil, err
		}
		for usage.Next() {
			var family string
			var count int
			var oldest time.Time
			if err := usage.Scan(&family, &count, &oldest); err != nil {
				usage.Close()
				return nil, err
			}
			switch family {
			case "query":
				item.Usage.QueriesUsed = count
			case "grab":
				item.Usage.GrabsUsed = count
			}
			if item.Usage.OldestReservationAt == nil || oldest.Before(*item.Usage.OldestReservationAt) {
				item.Usage.OldestReservationAt = &oldest
			}
		}
		usage.Close()
		if err := usage.Err(); err != nil {
			return nil, err
		}
		item.Usage.QueriesRemaining = max(item.Budget.QueryLimit-item.Usage.QueriesUsed, 0)
		item.Usage.GrabsRemaining = max(item.Budget.GrabLimit-item.Usage.GrabsUsed, 0)
		item.Usage.BackgroundQueriesRemaining = max(item.Budget.BackgroundQueryLimit-item.Usage.QueriesUsed, 0)
		item.SearchOutcomes24h = []IndexerOutcomeCount{}
	}
	outcomes, err := store.Pool.Query(ctx, `SELECT o.indexer_instance_id::text,o.state,COALESCE(o.detail,''),count(*) FROM search_run_indexers o JOIN search_runs r ON r.id=o.search_run_id
		WHERE r.started_at>$1 GROUP BY 1,2,3 ORDER BY 1,4 DESC`, now.Add(-24*time.Hour))
	if err != nil {
		return nil, err
	}
	defer outcomes.Close()
	for outcomes.Next() {
		var id string
		var count IndexerOutcomeCount
		if err := outcomes.Scan(&id, &count.State, &count.Detail, &count.Count); err != nil {
			return nil, err
		}
		if item := byID[id]; item != nil {
			item.SearchOutcomes24h = append(item.SearchOutcomes24h, count)
		}
	}
	return result, outcomes.Err()
}

type QueueEventCounts struct {
	EventType       string     `json:"event_type"`
	Due             int64      `json:"due"`
	Scheduled       int64      `json:"scheduled"`
	Leased          int64      `json:"leased"`
	Retrying        int64      `json:"retrying"`
	OldestDueAt     *time.Time `json:"oldest_due_at,omitempty"`
	NextScheduledAt *time.Time `json:"next_scheduled_at,omitempty"`
}

type QueueSnapshot struct {
	Due               int64              `json:"due"`
	Scheduled         int64              `json:"scheduled"`
	Leased            int64              `json:"leased"`
	Retrying          int64              `json:"retrying"`
	CompletedLast10m  int64              `json:"completed_last_10m"`
	CompletedLastHour int64              `json:"completed_last_hour"`
	ByType            []QueueEventCounts `json:"by_type"`
	CheckedAt         time.Time          `json:"checked_at"`
}

// QueueSnapshot describes the durable background queue. The worker handles
// one event at a time: other types before searches, searches before
// discovery rescans, then oldest first.
func (store *Store) QueueSnapshot(ctx context.Context, now time.Time) (QueueSnapshot, error) {
	snapshot := QueueSnapshot{ByType: []QueueEventCounts{}, CheckedAt: now.UTC()}
	rows, err := store.Pool.Query(ctx, `SELECT event_type,
		count(*) FILTER (WHERE available_at<=$1 AND (lease_until IS NULL OR lease_until<$1)),
		count(*) FILTER (WHERE available_at>$1),
		count(*) FILTER (WHERE lease_until>=$1),
		count(*) FILTER (WHERE attempts>0),
		min(available_at) FILTER (WHERE available_at<=$1),
		min(available_at) FILTER (WHERE available_at>$1)
		FROM outbox_events WHERE completed_at IS NULL GROUP BY 1 ORDER BY 2 DESC,1`, now.UTC())
	if err != nil {
		return QueueSnapshot{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var item QueueEventCounts
		if err := rows.Scan(&item.EventType, &item.Due, &item.Scheduled, &item.Leased, &item.Retrying, &item.OldestDueAt, &item.NextScheduledAt); err != nil {
			return QueueSnapshot{}, err
		}
		snapshot.Due += item.Due
		snapshot.Scheduled += item.Scheduled
		snapshot.Leased += item.Leased
		snapshot.Retrying += item.Retrying
		snapshot.ByType = append(snapshot.ByType, item)
	}
	if err := rows.Err(); err != nil {
		return QueueSnapshot{}, err
	}
	err = store.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE completed_at>$1),count(*) FROM outbox_events WHERE completed_at>$2`, now.Add(-10*time.Minute), now.Add(-time.Hour)).Scan(&snapshot.CompletedLast10m, &snapshot.CompletedLastHour)
	return snapshot, err
}

type SearchRunIndexerOutcome struct {
	IndexerID   uuid.UUID  `json:"indexer_id"`
	IndexerName string     `json:"indexer_name"`
	IndexerKind string     `json:"indexer_kind"`
	State       string     `json:"state"`
	Detail      string     `json:"detail,omitempty"`
	RetryAt     *time.Time `json:"retry_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

type SearchRunView struct {
	ID            uuid.UUID                 `json:"id"`
	Mode          string                    `json:"mode"`
	State         string                    `json:"state"`
	StartedAt     time.Time                 `json:"started_at"`
	CompletedAt   *time.Time                `json:"completed_at,omitempty"`
	NewCandidates int                       `json:"new_candidates"`
	Indexers      []SearchRunIndexerOutcome `json:"indexers"`
}

func (store *Store) requireWorkflow(ctx context.Context, workflowID uuid.UUID) error {
	var exists bool
	if err := store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workflows WHERE id=$1)`, workflowID.String()).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

// WorkflowSearchRuns returns the newest search runs of one workflow with each
// indexer's outcome. "partial" runs had at least one deferred or failed
// indexer and are retried; only "complete" runs advance search phases.
func (store *Store) WorkflowSearchRuns(ctx context.Context, workflowID uuid.UUID, limit int) ([]SearchRunView, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	if err := store.requireWorkflow(ctx, workflowID); err != nil {
		return nil, err
	}
	rows, err := store.Pool.Query(ctx, `SELECT r.id::text,r.mode,r.state,r.started_at,r.completed_at,(SELECT count(*) FROM release_candidates c WHERE c.search_run_id=r.id)
		FROM search_runs r WHERE r.workflow_id=$1 ORDER BY r.started_at DESC,r.id DESC LIMIT $2`, workflowID.String(), limit)
	if err != nil {
		return nil, err
	}
	result := []SearchRunView{}
	index := map[string]int{}
	ids := []string{}
	for rows.Next() {
		var item SearchRunView
		var idText string
		if err := rows.Scan(&idText, &item.Mode, &item.State, &item.StartedAt, &item.CompletedAt, &item.NewCandidates); err != nil {
			rows.Close()
			return nil, err
		}
		item.ID, _ = uuid.Parse(idText)
		item.Indexers = []SearchRunIndexerOutcome{}
		index[idText] = len(result)
		ids = append(ids, idText)
		result = append(result, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(ids) == 0 {
		return result, err
	}
	outcomes, err := store.Pool.Query(ctx, `SELECT o.search_run_id::text,o.indexer_instance_id::text,i.name,i.kind,o.state,COALESCE(o.detail,''),o.retry_at,o.completed_at
		FROM search_run_indexers o JOIN integration_instances i ON i.id=o.indexer_instance_id WHERE o.search_run_id=ANY($1::uuid[]) ORDER BY i.name`, ids)
	if err != nil {
		return nil, err
	}
	defer outcomes.Close()
	for outcomes.Next() {
		var runID, indexerID string
		var outcome SearchRunIndexerOutcome
		if err := outcomes.Scan(&runID, &indexerID, &outcome.IndexerName, &outcome.IndexerKind, &outcome.State, &outcome.Detail, &outcome.RetryAt, &outcome.CompletedAt); err != nil {
			return nil, err
		}
		outcome.IndexerID, _ = uuid.Parse(indexerID)
		run := &result[index[runID]]
		run.Indexers = append(run.Indexers, outcome)
	}
	return result, outcomes.Err()
}

type CandidateDecisionView struct {
	Verdict       string          `json:"verdict"`
	Score         int             `json:"score"`
	Reasons       json.RawMessage `json:"reasons"`
	PolicyVersion int64           `json:"policy_version"`
	DecidedAt     time.Time       `json:"decided_at"`
	// DecisionExplanation is additive presentation metadata derived from the
	// stored reasons and the candidate's currently linked observation.
	DecisionExplanation *releases.DecisionExplanation `json:"decision_explanation,omitempty"`
}

type CandidateExclusionView struct {
	Reason      string    `json:"reason"`
	FailureKind string    `json:"failure_kind"`
	CreatedAt   time.Time `json:"created_at"`
}

type WorkflowCandidateView struct {
	ID               uuid.UUID               `json:"id"`
	Title            string                  `json:"title"`
	IndexerID        uuid.UUID               `json:"indexer_id"`
	IndexerName      string                  `json:"indexer_name"`
	IndexerKind      string                  `json:"indexer_kind"`
	SizeBytes        *int64                  `json:"size_bytes,omitempty"`
	PublishedAt      *time.Time              `json:"published_at,omitempty"`
	FirstObservedAt  time.Time               `json:"first_observed_at"`
	LastObservedAt   time.Time               `json:"last_observed_at"`
	ObservationCount int64                   `json:"observation_count"`
	Decision         *CandidateDecisionView  `json:"decision,omitempty"`
	Attempted        bool                    `json:"attempted"`
	Selected         bool                    `json:"selected"`
	Exclusion        *CandidateExclusionView `json:"exclusion,omitempty"`
}

// WorkflowCandidates lists one workflow's stored release candidates with the
// latest decision (verdict, score and reasons), whether it was attempted or
// excluded, and whether it is the selected release. verdict filters by the
// latest decision ("eligible", "held" or "rejected"); empty means all.
func (store *Store) WorkflowCandidates(ctx context.Context, workflowID uuid.UUID, verdict string, limit int) ([]WorkflowCandidateView, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	if err := store.requireWorkflow(ctx, workflowID); err != nil {
		return nil, err
	}
	rows, err := store.Pool.Query(ctx, `SELECT c.id::text,c.title,i.id::text,i.name,i.kind,c.size_bytes,c.published_at,c.first_observed_at,c.last_observed_at,c.observation_count,
		d.verdict,d.score,d.reasons,d.policy_version,d.decided_at,
		EXISTS(SELECT 1 FROM workflow_attempted_releases a WHERE a.workflow_id=c.workflow_id AND a.candidate_id=c.id),
		COALESCE(w.selected_release_id=c.id,false),
		e.reason,e.failure_kind,e.created_at,COALESCE(o.normalized_facts,'[]'::jsonb)
		FROM release_candidates c JOIN integration_instances i ON i.id=c.indexer_instance_id JOIN workflows w ON w.id=c.workflow_id
		LEFT JOIN release_observations o ON o.id=c.observation_id
		LEFT JOIN LATERAL (SELECT verdict,score,reasons,policy_version,decided_at FROM candidate_decisions d WHERE d.workflow_id=c.workflow_id AND d.candidate_id=c.id ORDER BY d.policy_version DESC,d.decided_at DESC LIMIT 1) d ON true
		LEFT JOIN LATERAL (SELECT reason,failure_kind,created_at FROM workflow_release_exclusions e WHERE e.workflow_id=c.workflow_id AND e.candidate_id=c.id ORDER BY e.created_at DESC LIMIT 1) e ON true
		WHERE c.workflow_id=$1 AND ($2='' OR d.verdict=$2)
		ORDER BY (d.verdict='eligible') DESC NULLS LAST,d.score DESC NULLS LAST,c.last_observed_at DESC,c.id LIMIT $3`, workflowID.String(), verdict, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []WorkflowCandidateView{}
	for rows.Next() {
		var item WorkflowCandidateView
		var idText, indexerText string
		var decisionVerdict, exclusionReason, exclusionKind *string
		var score *int
		var reasons, facts []byte
		var policy *int64
		var decidedAt, excludedAt *time.Time
		if err := rows.Scan(&idText, &item.Title, &indexerText, &item.IndexerName, &item.IndexerKind, &item.SizeBytes, &item.PublishedAt, &item.FirstObservedAt, &item.LastObservedAt, &item.ObservationCount,
			&decisionVerdict, &score, &reasons, &policy, &decidedAt, &item.Attempted, &item.Selected, &exclusionReason, &exclusionKind, &excludedAt, &facts); err != nil {
			return nil, err
		}
		item.ID, _ = uuid.Parse(idText)
		item.IndexerID, _ = uuid.Parse(indexerText)
		if decisionVerdict != nil && score != nil && policy != nil && decidedAt != nil {
			reasonJSON := json.RawMessage(reasons)
			explanation := releases.ExplainDecision(releases.Verdict(*decisionVerdict), *score, releases.ParseDecisionReasons(reasonJSON), releases.ParseObservationFacts(json.RawMessage(facts)))
			item.Decision = &CandidateDecisionView{Verdict: *decisionVerdict, Score: *score, Reasons: reasonJSON, PolicyVersion: *policy, DecidedAt: *decidedAt, DecisionExplanation: &explanation}
		}
		if exclusionReason != nil && exclusionKind != nil && excludedAt != nil {
			item.Exclusion = &CandidateExclusionView{Reason: *exclusionReason, FailureKind: *exclusionKind, CreatedAt: *excludedAt}
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
