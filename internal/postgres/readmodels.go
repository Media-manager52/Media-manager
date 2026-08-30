package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/catalog"
	"uuid"
)

type DashboardCounts struct {
	Movies          int64 `json:"movies"`
	Series          int64 `json:"series"`
	ActiveWorkflows int64 `json:"active_workflows"`
	NeedsAttention  int64 `json:"needs_attention"`
	CurrentFiles    int64 `json:"current_files"`
	RecycleEntries  int64 `json:"recycle_entries"`
	PendingEvents   int64 `json:"pending_events"`
}

type DashboardSnapshot struct {
	Counts         DashboardCounts   `json:"counts"`
	RecentWorkflow []WorkflowSummary `json:"recent_workflows"`
	RecentActivity []ActivityEvent   `json:"recent_activity"`
	GeneratedAt    time.Time         `json:"generated_at"`
}

func (store *Store) Dashboard(ctx context.Context, now time.Time) (DashboardSnapshot, error) {
	result := DashboardSnapshot{GeneratedAt: now.UTC()}
	err := store.Pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE kind='movie' AND deleted_at IS NULL),
		count(*) FILTER (WHERE kind='series' AND deleted_at IS NULL),
		(SELECT count(*) FROM workflows WHERE terminal_at IS NULL),
		(SELECT count(*) FROM workflows WHERE terminal_at IS NULL AND state IN ('needs_attention','held','blocked','quarantined','transport_unknown')),
		(SELECT count(*) FROM library_link_versions WHERE state='current'),
		(SELECT count(*) FROM recycle_entries WHERE state NOT IN ('restored','purged')),
		(SELECT count(*) FROM outbox_events WHERE completed_at IS NULL)
		FROM media_items`).Scan(
		&result.Counts.Movies,
		&result.Counts.Series,
		&result.Counts.ActiveWorkflows,
		&result.Counts.NeedsAttention,
		&result.Counts.CurrentFiles,
		&result.Counts.RecycleEntries,
		&result.Counts.PendingEvents,
	)
	if err != nil {
		return DashboardSnapshot{}, err
	}
	result.RecentWorkflow, err = store.ListWorkflowSummaries(ctx, 8, false)
	if err != nil {
		return DashboardSnapshot{}, err
	}
	result.RecentActivity, err = store.ListActivity(ctx, 8)
	if err != nil {
		return DashboardSnapshot{}, err
	}
	return result, nil
}

type ActivityEvent struct {
	ID            string          `json:"id"`
	Kind          string          `json:"kind"`
	Actor         string          `json:"actor"`
	Action        string          `json:"action"`
	AggregateType string          `json:"aggregate_type"`
	AggregateID   string          `json:"aggregate_id,omitempty"`
	Detail        json.RawMessage `json:"detail"`
	OccurredAt    time.Time       `json:"occurred_at"`
}

func (store *Store) ListActivity(ctx context.Context, limit int) ([]ActivityEvent, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := store.Pool.Query(ctx, `SELECT id,kind,actor,action,aggregate_type,aggregate_id,detail,occurred_at FROM (
		SELECT id::text AS id,'audit'::text AS kind,actor,action,aggregate_type,COALESCE(aggregate_id::text,'') AS aggregate_id,detail,occurred_at
		FROM audit_events
		UNION ALL
		SELECT workflow_id::text || ':' || to_revision::text,'workflow_transition',actor,'workflow.' || to_state,'workflow',workflow_id::text,
			jsonb_build_object('from_state',from_state,'to_state',to_state,'reason',reason,'revision',to_revision),occurred_at
		FROM workflow_transitions
	) event ORDER BY occurred_at DESC,id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ActivityEvent{}
	for rows.Next() {
		var item ActivityEvent
		var detail []byte
		if err := rows.Scan(&item.ID, &item.Kind, &item.Actor, &item.Action, &item.AggregateType, &item.AggregateID, &detail, &item.OccurredAt); err != nil {
			return nil, err
		}
		item.Detail = append(json.RawMessage(nil), detail...)
		result = append(result, item)
	}
	return result, rows.Err()
}

type ManagedFile struct {
	ID                  uuid.UUID         `json:"id"`
	MediaID             uuid.UUID         `json:"media_id"`
	MediaKind           catalog.MediaKind `json:"media_kind"`
	Title               string            `json:"title"`
	Year                *uint16           `json:"year,omitempty"`
	RootName            string            `json:"root_name"`
	DestinationRelative string            `json:"destination_relative"`
	SourceRelative      string            `json:"source_relative"`
	PlexVisibleTarget   string            `json:"plex_visible_target"`
	State               string            `json:"state"`
	Generation          int               `json:"generation"`
	PlexConfirmed       bool              `json:"plex_confirmed"`
	VerifiedAt          *time.Time        `json:"verified_at,omitempty"`
	DisplacedAt         *time.Time        `json:"displaced_at,omitempty"`
	RemovedAt           *time.Time        `json:"removed_at,omitempty"`
}

func (store *Store) ListManagedFiles(ctx context.Context, limit int) ([]ManagedFile, error) {
	return store.managedFiles(ctx, nil, limit)
}

func (store *Store) ManagedFile(ctx context.Context, id uuid.UUID) (ManagedFile, error) {
	items, err := store.managedFiles(ctx, &id, 1)
	if err != nil {
		return ManagedFile{}, err
	}
	if len(items) == 0 {
		return ManagedFile{}, ErrNotFound
	}
	return items[0], nil
}

func (store *Store) managedFiles(ctx context.Context, id *uuid.UUID, limit int) ([]ManagedFile, error) {
	if limit < 1 || limit > 500 {
		limit = 200
	}
	var idValue any
	if id != nil {
		idValue = id.String()
	}
	rows, err := store.Pool.Query(ctx, `SELECT version.id::text,media.id::text,media.kind,media.title,media.year,root.name,link.destination_relative,
		version.source_relative,version.plex_visible_target,version.state,version.generation,
		EXISTS(SELECT 1 FROM plex_confirmations confirmation WHERE confirmation.link_version_id=version.id),
		version.verified_at,version.displaced_at,removal.removed_at
		FROM library_link_versions version
		JOIN library_links link ON link.id=version.library_link_id
		JOIN library_roots root ON root.id=link.root_id
		JOIN workflows workflow ON workflow.id=version.workflow_id
		JOIN media_items media ON media.id=workflow.media_id
		LEFT JOIN library_link_removals removal ON removal.version_id=version.id
		WHERE ($1::uuid IS NULL OR version.id=$1::uuid)
		ORDER BY CASE version.state WHEN 'current' THEN 0 WHEN 'verified' THEN 1 WHEN 'publishing' THEN 2 ELSE 3 END,
			COALESCE(version.verified_at,workflow.updated_at) DESC,version.id
		LIMIT $2`, idValue, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ManagedFile{}
	for rows.Next() {
		var item ManagedFile
		var idText, mediaText string
		var year *int16
		if err := rows.Scan(&idText, &mediaText, &item.MediaKind, &item.Title, &year, &item.RootName, &item.DestinationRelative, &item.SourceRelative, &item.PlexVisibleTarget, &item.State, &item.Generation, &item.PlexConfirmed, &item.VerifiedAt, &item.DisplacedAt, &item.RemovedAt); err != nil {
			return nil, err
		}
		item.ID, _ = uuid.Parse(idText)
		item.MediaID, _ = uuid.Parse(mediaText)
		if year != nil {
			converted := uint16(*year)
			item.Year = &converted
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

type ReleaseCandidateSummary struct {
	ID              uuid.UUID         `json:"id"`
	WorkflowID      uuid.UUID         `json:"workflow_id"`
	MediaID         uuid.UUID         `json:"media_id"`
	MediaKind       catalog.MediaKind `json:"media_kind"`
	MediaTitle      string            `json:"media_title"`
	MediaYear       *uint16           `json:"media_year,omitempty"`
	Title           string            `json:"title"`
	IndexerID       uuid.UUID         `json:"indexer_id"`
	IndexerName     string            `json:"indexer_name"`
	SizeBytes       *int64            `json:"size_bytes,omitempty"`
	PublishedAt     *time.Time        `json:"published_at,omitempty"`
	Verdict         string            `json:"verdict,omitempty"`
	Score           *int              `json:"score,omitempty"`
	Reasons         json.RawMessage   `json:"reasons"`
	NormalizedFacts json.RawMessage   `json:"normalized_facts"`
	Problems        json.RawMessage   `json:"problems"`
	Selected        bool              `json:"selected"`
	ObservedAt      time.Time         `json:"observed_at"`
}

func (store *Store) ListReleaseCandidates(ctx context.Context, limit int) ([]ReleaseCandidateSummary, error) {
	if limit < 1 || limit > 500 {
		limit = 200
	}
	rows, err := store.Pool.Query(ctx, `SELECT candidate.id::text,candidate.workflow_id::text,media.id::text,media.kind,media.title,media.year,
		candidate.title,indexer.id::text,indexer.name,candidate.size_bytes,candidate.published_at,
		COALESCE(decision.verdict,''),decision.score,COALESCE(decision.reasons,'[]'::jsonb),observation.normalized_facts,observation.problems,
		(selection.candidate_id IS NOT NULL),observation.observed_at
		FROM release_candidates candidate
		JOIN workflows workflow ON workflow.id=candidate.workflow_id
		JOIN media_items media ON media.id=workflow.media_id
		JOIN integration_instances indexer ON indexer.id=candidate.indexer_instance_id
		JOIN release_observations observation ON observation.id=candidate.observation_id
		LEFT JOIN LATERAL (
			SELECT verdict,score,reasons FROM candidate_decisions item
			WHERE item.workflow_id=candidate.workflow_id AND item.candidate_id=candidate.id
			ORDER BY policy_version DESC LIMIT 1
		) decision ON true
		LEFT JOIN candidate_selections selection ON selection.workflow_id=candidate.workflow_id AND selection.candidate_id=candidate.id
		ORDER BY observation.observed_at DESC,candidate.id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ReleaseCandidateSummary{}
	for rows.Next() {
		var item ReleaseCandidateSummary
		var idText, workflowText, mediaText, indexerText string
		var year *int16
		var reasons, facts, problems []byte
		if err := rows.Scan(&idText, &workflowText, &mediaText, &item.MediaKind, &item.MediaTitle, &year, &item.Title, &indexerText, &item.IndexerName, &item.SizeBytes, &item.PublishedAt, &item.Verdict, &item.Score, &reasons, &facts, &problems, &item.Selected, &item.ObservedAt); err != nil {
			return nil, err
		}
		item.ID, _ = uuid.Parse(idText)
		item.WorkflowID, _ = uuid.Parse(workflowText)
		item.MediaID, _ = uuid.Parse(mediaText)
		item.IndexerID, _ = uuid.Parse(indexerText)
		if year != nil {
			converted := uint16(*year)
			item.MediaYear = &converted
		}
		item.Reasons = append(json.RawMessage(nil), reasons...)
		item.NormalizedFacts = append(json.RawMessage(nil), facts...)
		item.Problems = append(json.RawMessage(nil), problems...)
		result = append(result, item)
	}
	return result, rows.Err()
}

type SourceStatus struct {
	Integration
	ConfigurationState string     `json:"configuration_state"`
	MinimumIntervalMS  *int64     `json:"minimum_interval_ms,omitempty"`
	NextAllowedAt      *time.Time `json:"next_allowed_at,omitempty"`
	CooldownUntil      *time.Time `json:"cooldown_until,omitempty"`
	ConsecutiveFailure int        `json:"consecutive_failure"`
	LastOutcome        string     `json:"last_outcome,omitempty"`
	BudgetUpdatedAt    *time.Time `json:"budget_updated_at,omitempty"`
}

func (store *Store) ListSourceStatuses(ctx context.Context) ([]SourceStatus, error) {
	rows, err := store.Pool.Query(ctx, `SELECT integration.id::text,integration.kind,integration.name,integration.enabled,integration.is_default,integration.config,
		COALESCE(integration.secret_ref,''),integration.created_at,integration.updated_at,budget.minimum_interval_ms,budget.next_allowed_at,
		budget.cooldown_until,COALESCE(budget.consecutive_failure,0),COALESCE(budget.last_outcome,''),budget.updated_at
		FROM integration_instances integration
		LEFT JOIN indexer_budget_state budget ON budget.indexer_instance_id=integration.id
		ORDER BY integration.kind,integration.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []SourceStatus{}
	for rows.Next() {
		var item SourceStatus
		var idText string
		var config []byte
		if err := rows.Scan(&idText, &item.Kind, &item.Name, &item.Enabled, &item.Default, &config, &item.SecretRef, &item.CreatedAt, &item.UpdatedAt, &item.MinimumIntervalMS, &item.NextAllowedAt, &item.CooldownUntil, &item.ConsecutiveFailure, &item.LastOutcome, &item.BudgetUpdatedAt); err != nil {
			return nil, err
		}
		item.ID, _ = uuid.Parse(idText)
		item.Config = append(json.RawMessage(nil), config...)
		switch {
		case item.Kind == "newznab" && !item.Enabled:
			item.ConfigurationState = "awaiting_commission"
		case !item.Enabled:
			item.ConfigurationState = "disabled"
		default:
			item.ConfigurationState = "configured"
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

type OutboxHealth struct {
	Pending     int64      `json:"pending"`
	Due         int64      `json:"due"`
	Retrying    int64      `json:"retrying"`
	OldestDueAt *time.Time `json:"oldest_due_at,omitempty"`
}

type SystemHealthSnapshot struct {
	DatabaseState     string         `json:"database_state"`
	DatabaseLatencyMS int64          `json:"database_latency_ms"`
	SchemaVersion     int64          `json:"schema_version"`
	ExpectedSchema    int64          `json:"expected_schema"`
	Outbox            OutboxHealth   `json:"outbox"`
	Components        []SourceStatus `json:"components"`
	CheckedAt         time.Time      `json:"checked_at"`
}

func (store *Store) SystemHealth(ctx context.Context, now time.Time) (SystemHealthSnapshot, error) {
	started := time.Now()
	if err := store.Ready(ctx); err != nil {
		return SystemHealthSnapshot{}, err
	}
	version, err := SchemaVersion(ctx, store.Pool)
	if err != nil {
		return SystemHealthSnapshot{}, err
	}
	result := SystemHealthSnapshot{
		DatabaseState:     "ready",
		DatabaseLatencyMS: time.Since(started).Milliseconds(),
		SchemaVersion:     version,
		ExpectedSchema:    CurrentSchemaVersion,
		CheckedAt:         now.UTC(),
	}
	err = store.Pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE available_at<=now()),count(*) FILTER (WHERE attempts>0),min(available_at) FILTER (WHERE available_at<=now())
		FROM outbox_events WHERE completed_at IS NULL`).Scan(&result.Outbox.Pending, &result.Outbox.Due, &result.Outbox.Retrying, &result.Outbox.OldestDueAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return SystemHealthSnapshot{}, err
	}
	result.Components, err = store.ListSourceStatuses(ctx)
	if err != nil {
		return SystemHealthSnapshot{}, err
	}
	return result, nil
}
