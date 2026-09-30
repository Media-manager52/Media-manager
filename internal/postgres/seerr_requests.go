package postgres

import (
	"context"
	"errors"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
)

// SeerrRequestSeason reports one requested season's durable Seerr availability
// and, when a workflow exists, its current Media Manager lifecycle state.
type SeerrRequestSeason struct {
	Number        uint16     `json:"number"`
	Availability  int        `json:"availability"`
	Requested     bool       `json:"requested"`
	Monitored     *bool      `json:"monitored,omitempty"`
	WorkflowID    *uuid.UUID `json:"workflow_id,omitempty"`
	WorkflowState string     `json:"workflow_state,omitempty"`
}

// SeerrRequestSummary is the owner-facing read model for one Seerr request. It
// joins the durable request snapshot with the linked media/workflows and the
// last intake outcome; it never invents a success state.
type SeerrRequestSummary struct {
	ID                string     `json:"id"`
	RequestID         string     `json:"request_id"`
	SeerrInstanceID   string     `json:"seerr_instance_id"`
	SeerrInstanceName string     `json:"seerr_instance_name,omitempty"`
	MediaKind         string     `json:"media_kind"`
	Title             string     `json:"title"`
	Year              *uint16    `json:"year,omitempty"`
	PosterURL         string     `json:"poster_url,omitempty"`
	Requester         string     `json:"requester,omitempty"`
	RequestedAt       *time.Time `json:"requested_at,omitempty"`
	UpdatedAt         time.Time  `json:"updated_at"`
	SeerrState        string     `json:"seerr_state"`
	IntakeOutcome     string     `json:"intake_outcome,omitempty"`
	IntakeError       string     `json:"intake_error,omitempty"`
	MediaID           *uuid.UUID `json:"media_id,omitempty"`
	WorkflowCount     int        `json:"workflow_count"`
	ConfirmedCount    int        `json:"confirmed_count"`
	AttentionCount    int        `json:"attention_count"`
	CurrentState      string     `json:"current_state,omitempty"`
	QualityProfile    string     `json:"quality_profile,omitempty"`
	Variant           string     `json:"variant,omitempty"`
	AttentionCode     string     `json:"attention_code,omitempty"`
	AttentionDetail   string     `json:"attention_detail,omitempty"`
	Lifecycle         string     `json:"lifecycle"`
	SeasonCount       int        `json:"season_count"`
	AvailableSeasons  int        `json:"available_seasons"`
}

type SeerrRequestDetail struct {
	Request   SeerrRequestSummary  `json:"request"`
	Workflows []WorkflowSummary    `json:"workflows"`
	Seasons   []SeerrRequestSeason `json:"seasons"`
	Files     []ManagedFile        `json:"files"`
}

const seerrRequestSummaryColumns = `s.seerr_instance_id::text,COALESCE(instance.name,''),s.request_id,
	COALESCE(NULLIF(s.title,''),media.title,''),
	COALESCE(s.year,media.year),
	COALESCE(NULLIF(s.media_kind,''),media.kind,''),
	COALESCE(NULLIF(s.poster_url,''),metadata.data->>'poster_url',''),
	s.requested_at,
	COALESCE(s.requester,''),
	s.state,
	COALESCE(delivery.outcome,''),
	COALESCE(delivery.error_detail,''),
	primary_workflow.media_id::text,
	COALESCE(agg.workflow_count,0),
	COALESCE(agg.attention_count,0),
	COALESCE(agg.confirmed_count,0),
	COALESCE(primary_workflow.state,''),
	COALESCE(profile.name,''),
	COALESCE(primary_workflow.variant,''),
	COALESCE(primary_workflow.attention_code,''),
	COALESCE(primary_workflow.attention_detail,''),
	COALESCE(primary_workflow.updated_at,s.reconciled_at),
	COALESCE(seasons.season_count,0),
	COALESCE(seasons.available_count,0)`

const seerrRequestSummaryJoins = `FROM seerr_request_snapshots s
	JOIN integration_instances instance ON instance.id=s.seerr_instance_id
	LEFT JOIN seerr_deliveries delivery ON delivery.id=s.last_delivery_id
	LEFT JOIN LATERAL (
		SELECT workflow.media_id,workflow.state,workflow.quality_profile_id,workflow.variant,workflow.attention_code,workflow.attention_detail,workflow.updated_at
		FROM seerr_request_workflows link
		JOIN workflows workflow ON workflow.id=link.workflow_id
		WHERE link.seerr_instance_id=s.seerr_instance_id AND link.request_id=s.request_id
		ORDER BY CASE workflow.state WHEN 'needs_attention' THEN 0 WHEN 'quarantined' THEN 1 WHEN 'held' THEN 2 WHEN 'blocked' THEN 3 WHEN 'transport_unknown' THEN 4 WHEN 'plex_confirmed' THEN 9 ELSE 5 END,workflow.updated_at DESC,workflow.id DESC
		LIMIT 1
	) primary_workflow ON true
	LEFT JOIN media_items media ON media.id=primary_workflow.media_id
	LEFT JOIN media_metadata metadata ON metadata.media_id=media.id
	LEFT JOIN quality_profiles profile ON profile.id=primary_workflow.quality_profile_id
	LEFT JOIN LATERAL (
		SELECT count(*)::int AS workflow_count,
			count(*) FILTER (WHERE workflow.state IN ('needs_attention','quarantined','blocked','transport_unknown')
				OR (workflow.state='waiting' AND EXISTS(SELECT 1 FROM workflow_retry_discovery discovery WHERE discovery.workflow_id=workflow.id AND discovery.state='ready')))::int AS attention_count,
			count(*) FILTER (WHERE workflow.state='plex_confirmed')::int AS confirmed_count
		FROM seerr_request_workflows link
		JOIN workflows workflow ON workflow.id=link.workflow_id
		WHERE link.seerr_instance_id=s.seerr_instance_id AND link.request_id=s.request_id
	) agg ON true
	LEFT JOIN LATERAL (
		SELECT count(*)::int AS season_count,count(*) FILTER (WHERE season.availability_status=5)::int AS available_count
		FROM seerr_request_seasons season
		WHERE season.seerr_instance_id=s.seerr_instance_id AND season.request_id=s.request_id
	) seasons ON true`

type seerrRequestScanner interface {
	Scan(dest ...any) error
}

func scanSeerrRequestSummary(row seerrRequestScanner) (SeerrRequestSummary, error) {
	var item SeerrRequestSummary
	var year *int16
	var requestedAt *time.Time
	var mediaText *string
	if err := row.Scan(
		&item.SeerrInstanceID, &item.SeerrInstanceName, &item.RequestID, &item.Title, &year, &item.MediaKind, &item.PosterURL,
		&requestedAt, &item.Requester, &item.SeerrState, &item.IntakeOutcome, &item.IntakeError,
		&mediaText, &item.WorkflowCount, &item.AttentionCount, &item.ConfirmedCount,
		&item.CurrentState, &item.QualityProfile, &item.Variant, &item.AttentionCode, &item.AttentionDetail, &item.UpdatedAt,
		&item.SeasonCount, &item.AvailableSeasons,
	); err != nil {
		return SeerrRequestSummary{}, err
	}
	// The read model spans every Seerr instance so switching the configured
	// intake instance never hides historical requests. The instance-qualified
	// key keeps colliding request IDs (both Seerr instances number from 1)
	// addressable by the detail route.
	item.ID = item.SeerrInstanceID + ":" + item.RequestID
	if year != nil {
		converted := uint16(*year)
		item.Year = &converted
	}
	item.RequestedAt = requestedAt
	if mediaText != nil {
		if parsed, parseErr := uuid.Parse(*mediaText); parseErr == nil {
			item.MediaID = &parsed
		}
	}
	item.Lifecycle = seerrRequestLifecycle(item)
	return item, nil
}

// seerrRequestLifecycle derives the owner-facing state. Workflow attention and
// Plex confirmation take precedence over the intake outcome, so a request never
// looks successful while a linked workflow is waiting or failed.
func seerrRequestLifecycle(item SeerrRequestSummary) string {
	if item.AttentionCount > 0 {
		if item.CurrentState != "" {
			return item.CurrentState
		}
		return "needs_attention"
	}
	if item.WorkflowCount > 0 && item.ConfirmedCount == item.WorkflowCount {
		return "plex_confirmed"
	}
	if item.SeerrState == "deleted" {
		return "deleted"
	}
	switch item.IntakeOutcome {
	case "needs_attention", "needs_configuration":
		if item.SeerrState == "5" {
			// Seerr completed the request (the media is available) and no linked
			// workflow needs anything; the intake outcome is a leftover of older
			// builds that read Completed as a revocation.
			return "available"
		}
		return "needs_attention"
	case "observed_not_approved":
		// Intake saw the request before it was approved. Seerr's own state
		// says what happened since: a decision recorded by the owner (or by
		// Seerr) shows at once, while admission still waits for intake.
		switch item.SeerrState {
		case "2":
			return "approved_in_seerr"
		case "3":
			return "declined"
		case "4":
			return "failed"
		case "5":
			// Seerr completed the request itself because the media became
			// available. Older builds recorded that as not approved.
			return "available"
		}
		return "not_approved"
	case "already_available":
		return "available"
	}
	if item.WorkflowCount > 0 && item.CurrentState != "" {
		if item.CurrentState == "held" {
			// Held search results already schedule a paced retry. Workflow
			// attention and intake problems were handled above; this request
			// is waiting for eligible evidence, not an owner decision.
			return "waiting"
		}
		return item.CurrentState
	}
	switch item.SeerrState {
	case "1":
		return "pending"
	case "2":
		return "monitored"
	case "3":
		return "declined"
	case "4":
		return "failed"
	case "5":
		return "available"
	}
	return "unknown"
}

func (store *Store) ListSeerrRequests(ctx context.Context, instanceID string, limit, offset int) ([]SeerrRequestSummary, error) {
	if limit < 1 || limit > 500 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	// An empty instance selects every Seerr instance. The owner-facing request
	// history must survive a change of configured intake instance, so the list
	// is deliberately not scoped to the runtime instance alone.
	rows, err := store.Pool.Query(ctx, `SELECT `+seerrRequestSummaryColumns+` `+seerrRequestSummaryJoins+
		` WHERE ($1::text='' OR s.seerr_instance_id=NULLIF($1::text,'')::uuid)
		ORDER BY COALESCE(primary_workflow.updated_at,s.reconciled_at) DESC,s.seerr_instance_id DESC,s.request_id DESC LIMIT $2 OFFSET $3`,
		instanceID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []SeerrRequestSummary{}
	for rows.Next() {
		item, scanErr := scanSeerrRequestSummary(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (store *Store) SeerrRequest(ctx context.Context, instanceID, requestID string) (SeerrRequestDetail, error) {
	row := store.Pool.QueryRow(ctx, `SELECT `+seerrRequestSummaryColumns+` `+seerrRequestSummaryJoins+
		` WHERE s.seerr_instance_id=$1 AND s.request_id=$2`, instanceID, requestID)
	summary, err := scanSeerrRequestSummary(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return SeerrRequestDetail{}, ErrNotFound
	}
	if err != nil {
		return SeerrRequestDetail{}, err
	}
	detail := SeerrRequestDetail{Request: summary, Workflows: []WorkflowSummary{}, Seasons: []SeerrRequestSeason{}, Files: []ManagedFile{}}
	if summary.MediaID != nil {
		detail.Workflows, err = store.workflowSummaries(ctx, 500, false, summary.MediaID)
		if err != nil {
			return SeerrRequestDetail{}, err
		}
		detail.Files, err = store.managedFiles(ctx, nil, summary.MediaID, 500)
		if err != nil {
			return SeerrRequestDetail{}, err
		}
	}
	detail.Seasons, err = store.seerrRequestSeasons(ctx, instanceID, requestID, summary.MediaID)
	if err != nil {
		return SeerrRequestDetail{}, err
	}
	return detail, nil
}

func (store *Store) seerrRequestSeasons(ctx context.Context, instanceID, requestID string, mediaID *uuid.UUID) ([]SeerrRequestSeason, error) {
	var mediaValue any
	if mediaID != nil {
		mediaValue = mediaID.String()
	}
	rows, err := store.Pool.Query(ctx, `SELECT season.season_number,season.availability_status,season.requested,
		monitoring.monitored,workflow.id::text,COALESCE(workflow.state,'')
		FROM seerr_request_seasons season
		LEFT JOIN season_monitoring monitoring ON monitoring.media_id=$3::uuid AND monitoring.season_number=season.season_number
		LEFT JOIN LATERAL (
			SELECT item.id,item.state FROM workflows item
			WHERE item.media_id=$3::uuid AND item.variant='season:'||lpad(season.season_number::text,2,'0')
			ORDER BY item.updated_at DESC,item.id DESC LIMIT 1
		) workflow ON true
		WHERE season.seerr_instance_id=$1 AND season.request_id=$2
		ORDER BY season.season_number`, instanceID, requestID, mediaValue)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []SeerrRequestSeason{}
	for rows.Next() {
		var season SeerrRequestSeason
		var number int16
		var monitored *bool
		var workflowText *string
		if err := rows.Scan(&number, &season.Availability, &season.Requested, &monitored, &workflowText, &season.WorkflowState); err != nil {
			return nil, err
		}
		season.Number = uint16(number)
		season.Monitored = monitored
		if workflowText != nil {
			if parsed, parseErr := uuid.Parse(*workflowText); parseErr == nil {
				season.WorkflowID = &parsed
			}
		}
		result = append(result, season)
	}
	return result, rows.Err()
}
