package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
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
		(SELECT count(*) FROM workflows WHERE terminal_at IS NULL AND (
			state IN ('needs_attention','blocked','quarantined','transport_unknown')
			OR (state='waiting' AND EXISTS(SELECT 1 FROM workflow_retry_discovery discovery WHERE discovery.workflow_id=workflows.id AND discovery.state='ready'))
		)),
		(SELECT count(*) FROM library_link_versions WHERE state='current') +
			(SELECT count(*) FROM adopted_files WHERE state='current'),
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
	ID                   uuid.UUID         `json:"id"`
	MediaID              uuid.UUID         `json:"media_id"`
	MediaKind            catalog.MediaKind `json:"media_kind"`
	Title                string            `json:"title"`
	Year                 *uint16           `json:"year,omitempty"`
	RootName             string            `json:"root_name"`
	DestinationRelative  string            `json:"destination_relative"`
	SourceRelative       string            `json:"source_relative"`
	PlexVisibleTarget    string            `json:"plex_visible_target"`
	State                string            `json:"state"`
	Generation           int               `json:"generation"`
	PlexConfirmed        bool              `json:"plex_confirmed"`
	VerifiedAt           *time.Time        `json:"verified_at,omitempty"`
	DisplacedAt          *time.Time        `json:"displaced_at,omitempty"`
	RemovedAt            *time.Time        `json:"removed_at,omitempty"`
	Variant              string            `json:"variant"`
	Origin               string            `json:"origin"`
	QualityProfile       string            `json:"quality_profile"`
	QualityEvidenceState string            `json:"quality_evidence_state"`
	QualityEvidence      json.RawMessage   `json:"quality_evidence"`
	SourceSizeBytes      *int64            `json:"source_size_bytes,omitempty"`
	SourceModifiedAt     *time.Time        `json:"source_modified_at,omitempty"`
	OriginalSourceRoot   string            `json:"original_source_root,omitempty"`
	AdoptionBatchID      *uuid.UUID        `json:"adoption_batch_id,omitempty"`
	PlexProviderGUID     string            `json:"plex_provider_guid,omitempty"`
	// PlexTargetInsideLibrary reports whether the stored symlink target already
	// lies inside the Plex-visible library root. When it does not, the managed
	// library directory is the only truthful path that is contained by the
	// library, so the read model reports that directory instead of a target
	// that escapes the root.
	PlexTargetInsideLibrary bool `json:"-"`
}

func (store *Store) ListManagedFiles(ctx context.Context, limit int) ([]ManagedFile, error) {
	return store.managedFiles(ctx, nil, nil, limit)
}

// managedPublicationPath is the files-API presentation rule. A managed symlink
// whose stored target escapes the Plex-visible library cannot be reported as a
// contained publication path, so the real managed library directory (which is
// inside the library) is reported instead. Adoption rows always carry the
// real library file path and are returned unchanged. The stored symlink
// target and every removal check keep using the durable column.
func managedPublicationPath(origin, plexVisibleTarget string, targetInsideLibrary bool) string {
	if origin == "altmount" && !targetInsideLibrary && plexVisibleTarget != "" {
		return filepath.Dir(plexVisibleTarget)
	}
	return plexVisibleTarget
}

func (store *Store) ManagedFile(ctx context.Context, id uuid.UUID) (ManagedFile, error) {
	items, err := store.managedFiles(ctx, &id, nil, 1)
	if err != nil {
		return ManagedFile{}, err
	}
	if len(items) == 0 {
		return ManagedFile{}, ErrNotFound
	}
	return items[0], nil
}

func (store *Store) managedFiles(ctx context.Context, id, mediaID *uuid.UUID, limit int) ([]ManagedFile, error) {
	if limit < 1 || limit > 500 {
		limit = 200
	}
	var idValue any
	if id != nil {
		idValue = id.String()
	}
	var mediaIDValue any
	if mediaID != nil {
		mediaIDValue = mediaID.String()
	}
	rows, err := store.Pool.Query(ctx, `SELECT item.id,item.media_id,item.media_kind,item.title,item.year,item.root_name,item.destination_relative,
		item.source_relative,item.plex_visible_target,item.state,item.generation,item.plex_confirmed,
		item.verified_at,item.displaced_at,item.removed_at,item.variant,item.origin,item.quality_profile,
		item.quality_evidence_state,item.quality_evidence,item.source_size_bytes,item.source_modified_at,
		item.original_source_root,item.adoption_batch_id,item.plex_provider_guid,item.plex_target_inside_library
		FROM (
			SELECT version.id::text AS id,media.id::text AS media_id,media.kind AS media_kind,media.title,media.year,
				root.name AS root_name,link.destination_relative,version.source_relative,
				root.plex_visible_root || '/' || link.destination_relative AS plex_visible_target,
				version.state,version.generation,(confirmation.provider_namespace IS NOT NULL) AS plex_confirmed,
				version.verified_at,version.displaced_at,removal.removed_at,workflow.variant,'altmount'::text AS origin,
				profile.name AS quality_profile,
				CASE WHEN asset.media_info IS NOT NULL THEN 'media_info' WHEN observation.id IS NOT NULL THEN 'release_evidence' ELSE 'unassessed' END AS quality_evidence_state,
				COALESCE(asset.media_info,observation.normalized_facts,'{}'::jsonb) AS quality_evidence,
				asset.size_bytes AS source_size_bytes,NULL::timestamptz AS source_modified_at,''::text AS original_source_root,
				NULL::text AS adoption_batch_id,
				COALESCE(confirmation.provider_namespace || '://' || confirmation.provider_value,'') AS plex_provider_guid,
				(version.plex_visible_target = root.plex_visible_root OR version.plex_visible_target LIKE root.plex_visible_root || '/%') AS plex_target_inside_library,
				COALESCE(version.verified_at,workflow.updated_at) AS sort_at
			FROM library_link_versions version
			JOIN library_links link ON link.id=version.library_link_id
			JOIN library_roots root ON root.id=link.root_id
			JOIN workflows workflow ON workflow.id=version.workflow_id
			JOIN quality_profiles profile ON profile.id=workflow.quality_profile_id
			JOIN media_items media ON media.id=workflow.media_id
			JOIN altmount_output_assets asset ON asset.id=version.source_asset_id
			LEFT JOIN release_candidates candidate ON candidate.id=workflow.selected_release_id
			LEFT JOIN release_observations observation ON observation.id=candidate.observation_id
			LEFT JOIN LATERAL (
				SELECT item.provider_namespace,item.provider_value FROM plex_confirmations item
				WHERE item.link_version_id=version.id ORDER BY item.confirmed_at DESC LIMIT 1
			) confirmation ON true
			LEFT JOIN library_link_removals removal ON removal.version_id=version.id
			UNION ALL
			SELECT adopted.id::text,media.id::text,media.kind,media.title,media.year,root.name,
				adopted.destination_relative,adopted.original_relative,adopted.plex_observed_path,
				adopted.state,1,true,adopted.verified_at,adopted.displaced_at,adopted.purged_at,
				adopted.variant,'adoption'::text,profile.name,adopted.quality_evidence_state,
				adopted.quality_evidence,adopted.source_size,to_timestamp(adopted.source_modtime_ns / 1000000000.0),
				adopted.original_source_root,adopted.adoption_batch_id::text,adopted.provider_guid,true,adopted.verified_at
			FROM adopted_files adopted
			JOIN media_items media ON media.id=adopted.media_id
			JOIN library_roots root ON root.id=adopted.root_id
			JOIN quality_profiles profile ON profile.id=adopted.quality_profile_id
		) item
		WHERE ($1::uuid IS NULL OR item.id::uuid=$1::uuid) AND ($2::uuid IS NULL OR item.media_id::uuid=$2::uuid)
		ORDER BY CASE item.state WHEN 'current' THEN 0 WHEN 'verified' THEN 1 WHEN 'publishing' THEN 2 ELSE 3 END,
			item.sort_at DESC,item.id
		LIMIT $3`, idValue, mediaIDValue, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ManagedFile{}
	for rows.Next() {
		var item ManagedFile
		var idText, mediaText string
		var adoptionBatchText *string
		var year *int16
		var qualityEvidence []byte
		if err := rows.Scan(&idText, &mediaText, &item.MediaKind, &item.Title, &year, &item.RootName, &item.DestinationRelative, &item.SourceRelative, &item.PlexVisibleTarget, &item.State, &item.Generation, &item.PlexConfirmed, &item.VerifiedAt, &item.DisplacedAt, &item.RemovedAt, &item.Variant, &item.Origin, &item.QualityProfile, &item.QualityEvidenceState, &qualityEvidence, &item.SourceSizeBytes, &item.SourceModifiedAt, &item.OriginalSourceRoot, &adoptionBatchText, &item.PlexProviderGUID, &item.PlexTargetInsideLibrary); err != nil {
			return nil, err
		}
		item.PlexVisibleTarget = managedPublicationPath(item.Origin, item.PlexVisibleTarget, item.PlexTargetInsideLibrary)
		item.ID, _ = uuid.Parse(idText)
		item.MediaID, _ = uuid.Parse(mediaText)
		if year != nil {
			converted := uint16(*year)
			item.Year = &converted
		}
		item.QualityEvidence = append(json.RawMessage(nil), qualityEvidence...)
		if adoptionBatchText != nil {
			parsed, parseErr := uuid.Parse(*adoptionBatchText)
			if parseErr != nil {
				return nil, parseErr
			}
			item.AdoptionBatchID = &parsed
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

type MediaSeason struct {
	Number            uint16     `json:"number"`
	Monitored         bool       `json:"monitored"`
	PreferSeasonPack  bool       `json:"prefer_season_pack"`
	SearchImmediately bool       `json:"search_immediately"`
	State             string     `json:"state"`
	ExpectedEpisodes  int        `json:"expected_episodes"`
	ReleasedEpisodes  int        `json:"released_episodes"`
	FutureEpisodes    int        `json:"future_episodes"`
	WorkflowID        *uuid.UUID `json:"workflow_id,omitempty"`
	WorkflowState     string     `json:"workflow_state,omitempty"`
	WaitingReason     string     `json:"waiting_reason,omitempty"`
	LastSearchAt      *time.Time `json:"last_search_at,omitempty"`
	NextSearchAt      *time.Time `json:"next_search_at,omitempty"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type MediaDetail struct {
	Media     catalog.MediaItem `json:"media"`
	Workflows []WorkflowSummary `json:"workflows"`
	Seasons   []MediaSeason     `json:"seasons"`
	Files     []ManagedFile     `json:"files"`
}

func (store *Store) MediaDetail(ctx context.Context, mediaID uuid.UUID) (MediaDetail, error) {
	item, err := store.GetMedia(ctx, mediaID)
	if err != nil {
		return MediaDetail{}, err
	}
	result := MediaDetail{Media: item, Workflows: []WorkflowSummary{}, Seasons: []MediaSeason{}, Files: []ManagedFile{}}
	result.Workflows, err = store.workflowSummaries(ctx, 500, false, &mediaID)
	if err != nil {
		return MediaDetail{}, err
	}
	result.Files, err = store.managedFiles(ctx, nil, &mediaID, 500)
	if err != nil {
		return MediaDetail{}, err
	}
	rows, err := store.Pool.Query(ctx, `SELECT monitoring.season_number,monitoring.monitored,monitoring.prefer_season_pack,monitoring.search_immediately,monitoring.state,
		COALESCE(expectation.expected_episodes,0),COALESCE(expectation.released_episodes,0),COALESCE(expectation.future_episodes,0),workflow.id::text,COALESCE(workflow.state,''),COALESCE(workflow.attention_detail,''),search_state.last_search_at,search_state.next_search_at,monitoring.updated_at
		FROM season_monitoring monitoring
		LEFT JOIN LATERAL (
			SELECT item.id,item.state,item.attention_detail FROM workflows item
			WHERE item.media_id=monitoring.media_id AND item.variant='season:' || lpad(monitoring.season_number::text,2,'0')
			ORDER BY item.updated_at DESC,item.id DESC LIMIT 1
		) workflow ON true
		LEFT JOIN LATERAL (
			SELECT count(*)::int AS expected_episodes,
				count(*) FILTER (WHERE episode.air_date < CURRENT_DATE)::int AS released_episodes,
				count(*) FILTER (WHERE episode.air_date IS NULL OR episode.air_date >= CURRENT_DATE)::int AS future_episodes
			FROM pack_expected_episodes episode
			JOIN pack_expectation_sets set_row ON set_row.id=episode.expectation_id
			WHERE set_row.workflow_id=workflow.id
		) expectation ON true
		LEFT JOIN LATERAL (
			SELECT max(run.started_at) AS last_search_at,
				(SELECT min(event.available_at) FROM outbox_events event
					WHERE event.event_type='workflow.search_requested' AND event.completed_at IS NULL
					AND event.aggregate_id IN (
						SELECT candidate.id FROM workflows candidate
						WHERE candidate.media_id=monitoring.media_id
						AND (candidate.variant='season:' || lpad(monitoring.season_number::text,2,'0')
							OR candidate.variant LIKE 'season:' || lpad(monitoring.season_number::text,2,'0') || ':episode:%')
					)
				) AS next_search_at
			FROM search_runs run
			JOIN workflows scoped ON scoped.id=run.workflow_id
			WHERE scoped.media_id=monitoring.media_id
			AND (scoped.variant='season:' || lpad(monitoring.season_number::text,2,'0')
				OR scoped.variant LIKE 'season:' || lpad(monitoring.season_number::text,2,'0') || ':episode:%')
		) search_state ON true
		WHERE monitoring.media_id=$1 ORDER BY monitoring.season_number`, mediaID.String())
	if err != nil {
		return MediaDetail{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var season MediaSeason
		var number int16
		var workflowText *string
		if err := rows.Scan(&number, &season.Monitored, &season.PreferSeasonPack, &season.SearchImmediately, &season.State, &season.ExpectedEpisodes, &season.ReleasedEpisodes, &season.FutureEpisodes, &workflowText, &season.WorkflowState, &season.WaitingReason, &season.LastSearchAt, &season.NextSearchAt, &season.UpdatedAt); err != nil {
			return MediaDetail{}, err
		}
		season.Number = uint16(number)
		if workflowText != nil {
			parsed, parseErr := uuid.Parse(*workflowText)
			if parseErr != nil {
				return MediaDetail{}, parseErr
			}
			season.WorkflowID = &parsed
		}
		result.Seasons = append(result.Seasons, season)
	}
	if err := rows.Err(); err != nil {
		return MediaDetail{}, err
	}
	return result, nil
}

type WantedItem struct {
	WorkflowID      uuid.UUID         `json:"workflow_id"`
	MediaID         uuid.UUID         `json:"media_id"`
	MediaKind       catalog.MediaKind `json:"media_kind"`
	Title           string            `json:"title"`
	Year            *uint16           `json:"year,omitempty"`
	Variant         string            `json:"variant"`
	Gap             string            `json:"gap"`
	CurrentState    string            `json:"current_state"`
	TargetProfile   string            `json:"target_profile"`
	UpgradesEnabled bool              `json:"upgrades_enabled"`
	AttentionDetail string            `json:"attention_detail,omitempty"`
	// OwnerActionRequired mirrors WorkflowSummary.OwnerActionRequired for the
	// wanted read model so every owner-facing list shares one predicate.
	OwnerActionRequired bool      `json:"owner_action_required"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type CalendarEntry struct {
	MediaID       uuid.UUID         `json:"media_id"`
	MediaKind     catalog.MediaKind `json:"media_kind"`
	MediaTitle    string            `json:"media_title"`
	WorkflowID    uuid.UUID         `json:"workflow_id"`
	Variant       string            `json:"variant"`
	Season        uint16            `json:"season"`
	Episode       uint16            `json:"episode"`
	EpisodeTitle  string            `json:"episode_title,omitempty"`
	AirDate       time.Time         `json:"air_date"`
	Monitored     bool              `json:"monitored"`
	WorkflowState string            `json:"workflow_state"`
}

func (store *Store) ListCalendar(ctx context.Context, from, to time.Time, limit int) ([]CalendarEntry, error) {
	if limit < 1 || limit > 1000 {
		limit = 500
	}
	rows, err := store.Pool.Query(ctx, `SELECT media_id,media_kind,media_title,workflow_id,variant,season,episode,episode_title,air_date,monitored,workflow_state FROM (
		SELECT DISTINCT ON (workflow.id,episode.season,episode.episode) media.id::text AS media_id,media.kind AS media_kind,media.title AS media_title,workflow.id::text AS workflow_id,workflow.variant,episode.season,episode.episode,episode.title AS episode_title,episode.air_date,monitoring.monitored,workflow.state AS workflow_state,expectation.frozen_at
		FROM pack_expected_episodes episode
		JOIN pack_expectation_sets expectation ON expectation.id=episode.expectation_id
		JOIN workflows workflow ON workflow.id=expectation.workflow_id
		JOIN media_items media ON media.id=workflow.media_id
		JOIN season_monitoring monitoring ON monitoring.media_id=media.id AND monitoring.season_number=episode.season
		WHERE episode.air_date BETWEEN $1::date AND $2::date AND media.deleted_at IS NULL
		ORDER BY workflow.id,episode.season,episode.episode,expectation.frozen_at DESC
		) item ORDER BY air_date,media_title,season,episode LIMIT $3`, from.UTC(), to.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []CalendarEntry{}
	for rows.Next() {
		var item CalendarEntry
		var mediaText, workflowText string
		var season, episode int16
		if err := rows.Scan(&mediaText, &item.MediaKind, &item.MediaTitle, &workflowText, &item.Variant, &season, &episode, &item.EpisodeTitle, &item.AirDate, &item.Monitored, &item.WorkflowState); err != nil {
			return nil, err
		}
		item.MediaID, _ = uuid.Parse(mediaText)
		item.WorkflowID, _ = uuid.Parse(workflowText)
		item.Season, item.Episode = uint16(season), uint16(episode)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (store *Store) ListWanted(ctx context.Context, limit int) ([]WantedItem, error) {
	if limit < 1 || limit > 500 {
		limit = 300
	}
	rows, err := store.Pool.Query(ctx, `SELECT workflow.id::text,media.id::text,media.kind,media.title,media.year,workflow.variant,
		CASE WHEN current_link.present THEN 'upgrade' ELSE 'missing' END,workflow.state,profile.name,profile.upgrades_enabled,
		COALESCE(workflow.attention_detail,''),
		(workflow.state IN ('needs_attention','blocked','quarantined','transport_unknown') OR (workflow.state='waiting' AND EXISTS(SELECT 1 FROM workflow_retry_discovery discovery WHERE discovery.workflow_id=workflow.id AND discovery.state='ready'))),
		workflow.updated_at
		FROM workflows workflow
		JOIN media_items media ON media.id=workflow.media_id
		JOIN quality_profiles profile ON profile.id=workflow.quality_profile_id
		LEFT JOIN LATERAL (
			SELECT true AS present FROM (
				SELECT published_workflow.media_id,published_workflow.variant
				FROM library_link_versions version
				JOIN workflows published_workflow ON published_workflow.id=version.workflow_id
				WHERE version.state='current'
				UNION ALL
				SELECT adopted.media_id,adopted.variant FROM adopted_files adopted WHERE adopted.state='current'
			) current_file
			WHERE current_file.media_id=workflow.media_id AND current_file.variant=workflow.variant
			LIMIT 1
		) current_link ON true
		WHERE workflow.terminal_at IS NULL AND media.deleted_at IS NULL
		ORDER BY CASE workflow.state WHEN 'needs_attention' THEN 0 WHEN 'held' THEN 1 WHEN 'blocked' THEN 2 ELSE 3 END,workflow.updated_at DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []WantedItem{}
	for rows.Next() {
		var item WantedItem
		var workflowText, mediaText string
		var year *int16
		if err := rows.Scan(&workflowText, &mediaText, &item.MediaKind, &item.Title, &year, &item.Variant, &item.Gap, &item.CurrentState, &item.TargetProfile, &item.UpgradesEnabled, &item.AttentionDetail, &item.OwnerActionRequired, &item.UpdatedAt); err != nil {
			return nil, err
		}
		item.WorkflowID, _ = uuid.Parse(workflowText)
		item.MediaID, _ = uuid.Parse(mediaText)
		if year != nil {
			converted := uint16(*year)
			item.Year = &converted
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

type TaskStatus struct {
	EventType       string     `json:"event_type"`
	Pending         int64      `json:"pending"`
	Due             int64      `json:"due"`
	Retrying        int64      `json:"retrying"`
	Completed       int64      `json:"completed"`
	HighestAttempts int        `json:"highest_attempts"`
	OldestDueAt     *time.Time `json:"oldest_due_at,omitempty"`
	LatestAt        time.Time  `json:"latest_at"`
}

func (store *Store) ListTaskStatuses(ctx context.Context) ([]TaskStatus, error) {
	rows, err := store.Pool.Query(ctx, `SELECT event_type,
		count(*) FILTER (WHERE completed_at IS NULL),
		count(*) FILTER (WHERE completed_at IS NULL AND available_at<=now()),
		count(*) FILTER (WHERE completed_at IS NULL AND attempts>0),
		count(*) FILTER (WHERE completed_at IS NOT NULL),
		COALESCE(max(attempts),0),
		min(available_at) FILTER (WHERE completed_at IS NULL AND available_at<=now()),
		max(occurred_at)
		FROM outbox_events GROUP BY event_type ORDER BY event_type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []TaskStatus{}
	for rows.Next() {
		var item TaskStatus
		if err := rows.Scan(&item.EventType, &item.Pending, &item.Due, &item.Retrying, &item.Completed, &item.HighestAttempts, &item.OldestDueAt, &item.LatestAt); err != nil {
			return nil, err
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
	Variant         string            `json:"variant"`
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
	rows, err := store.Pool.Query(ctx, `SELECT candidate.id::text,candidate.workflow_id::text,media.id::text,media.kind,media.title,media.year,workflow.variant,
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
		if err := rows.Scan(&idText, &workflowText, &mediaText, &item.MediaKind, &item.MediaTitle, &year, &item.Variant, &item.Title, &indexerText, &item.IndexerName, &item.SizeBytes, &item.PublishedAt, &item.Verdict, &item.Score, &reasons, &facts, &problems, &item.Selected, &item.ObservedAt); err != nil {
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
