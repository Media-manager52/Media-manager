package errorlogstore

import (
	"context"
	"time"
)

// ErrorLogWindow is how far back the error log reads closed failures. Open
// cases are always included, however old they are.
const ErrorLogWindow = 30 * 24 * time.Hour

// ErrorLogEntry is one thing that went wrong, in the order it happened. Like
// the problem feed it is a read model over state that is already stored.
type ErrorLogEntry struct {
	ID         string    `json:"id"`
	Area       string    `json:"area"`
	Code       string    `json:"code"`
	Title      string    `json:"title,omitempty"`
	MediaID    string    `json:"media_id,omitempty"`
	MediaKind  string    `json:"media_kind,omitempty"`
	WorkflowID string    `json:"workflow_id,omitempty"`
	Variant    string    `json:"variant,omitempty"`
	Subject    string    `json:"subject,omitempty"`
	Detail     string    `json:"detail,omitempty"`
	Count      int64     `json:"count"`
	Open       bool      `json:"open"`
	OccurredAt time.Time `json:"occurred_at"`
}

type ErrorLogFilter struct {
	Area  string
	Query string
	Limit int
}

type ErrorLog struct {
	Entries []ErrorLogEntry  `json:"entries"`
	Total   int64            `json:"total"`
	Areas   map[string]int64 `json:"areas"`
	Since   time.Time        `json:"since"`
}

// errorLogEntries merges every stored failure. A failed indexer request is
// grouped per indexer, reason and day so one outage cannot bury the rest.
const errorLogEntries = `WITH entries AS (
	SELECT CASE WHEN job.error_code='subtitle_requirements_unverified' THEN 'language' ELSE 'download' END AS area,
		job.id::text AS id,job.error_code AS code,media.title AS title,media.id::text AS media_id,media.kind::text AS media_kind,
		workflow.id::text AS workflow_id,workflow.variant AS variant,COALESCE(candidate.title,job.upload_name,'') AS subject,
		left(COALESCE(job.error_detail,''),2000) AS detail,1::bigint AS count,false AS open,
		COALESCE(job.acquisition_closed_at,job.last_reconciled_at,job.submitted_at,job.submission_started_at) AS occurred_at
	FROM altmount_jobs job
	JOIN workflows workflow ON workflow.id=job.workflow_id
	JOIN media_items media ON media.id=workflow.media_id
	LEFT JOIN release_candidates candidate ON candidate.id=job.candidate_id
	WHERE COALESCE(job.error_code,'')<>''
	UNION ALL
	SELECT 'search',run.indexer_instance_id::text||':'||COALESCE(run.detail,'')||':'||date_trunc('day',run.completed_at)::date::text,
		COALESCE(NULLIF(run.detail,''),'indexer_request_failed'),'','','','','',integration.name,'',count(*),false,max(run.completed_at)
	FROM search_run_indexers run
	JOIN integration_instances integration ON integration.id=run.indexer_instance_id
	WHERE run.state='failed' AND run.completed_at>=$1
	GROUP BY run.indexer_instance_id,integration.name,run.detail,date_trunc('day',run.completed_at)
	UNION ALL
	SELECT 'repair',repair.id::text,repair.reason,COALESCE(media.title,''),COALESCE(media.id::text,''),COALESCE(media.kind::text,''),
		COALESCE(repair.repair_workflow_id::text,''),COALESCE(repair.variant,''),COALESCE(NULLIF(repair.broken_release_title,''),repair.altmount_file_path,''),
		left(COALESCE(repair.detail,''),2000),1,true,repair.created_at
	FROM altmount_repairs repair
	LEFT JOIN media_items media ON media.id=repair.media_id
	WHERE repair.state='needs_attention'
	UNION ALL
	SELECT 'backup',backup.id::text,'backup_failed','','','','','',COALESCE(backup.filename,''),left(COALESCE(backup.last_error,''),2000),1,false,
		COALESCE(backup.completed_at,backup.started_at,backup.created_at)
	FROM backup_records backup WHERE backup.state='failed'
	UNION ALL
	SELECT 'connection',connection.integration_id::text,'connection_failed','','','','','',integration.name,'',1,true,connection.checked_at
	FROM integration_connection_checks connection
	JOIN integration_instances integration ON integration.id=connection.integration_id
	WHERE NOT connection.ok AND integration.enabled
	UNION ALL
	SELECT 'title',workflow.id::text,COALESCE(NULLIF(workflow.attention_code,''),workflow.state::text),media.title,media.id::text,media.kind::text,
		workflow.id::text,workflow.variant,'',left(COALESCE(workflow.attention_detail,''),2000),1,true,workflow.updated_at
	FROM workflows workflow
	JOIN media_items media ON media.id=workflow.media_id
	WHERE workflow.terminal_at IS NULL AND workflow.state IN ('needs_attention','blocked','quarantined','transport_unknown')
), matching AS (
	SELECT * FROM entries
	WHERE (occurred_at>=$1 OR open)
		AND ($2='' OR position(lower($2) IN lower(title||' '||subject||' '||code||' '||detail))>0)
)`

// ErrorLog returns the newest entries that match the filter and how many
// entries each area holds under the same search.
func (store *ErrorLogs) ErrorLog(ctx context.Context, filter ErrorLogFilter, now time.Time) (ErrorLog, error) {
	since := now.Add(-ErrorLogWindow).UTC()
	result := ErrorLog{Entries: []ErrorLogEntry{}, Areas: map[string]int64{}, Since: since}
	counts, err := store.Pool.Query(ctx, errorLogEntries+` SELECT area,count(*) FROM matching GROUP BY area`, since, filter.Query)
	if err != nil {
		return ErrorLog{}, err
	}
	defer counts.Close()
	for counts.Next() {
		var area string
		var count int64
		if err = counts.Scan(&area, &count); err != nil {
			return ErrorLog{}, err
		}
		result.Areas[area] = count
		if filter.Area == "" || filter.Area == area {
			result.Total += count
		}
	}
	if err = counts.Err(); err != nil {
		return ErrorLog{}, err
	}
	rows, err := store.Pool.Query(ctx, errorLogEntries+` SELECT area,id,code,title,media_id,media_kind,workflow_id,variant,subject,detail,count,open,occurred_at
		FROM matching WHERE $3='' OR area=$3 ORDER BY occurred_at DESC,id LIMIT $4`, since, filter.Query, filter.Area, filter.Limit)
	if err != nil {
		return ErrorLog{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var entry ErrorLogEntry
		if err = rows.Scan(&entry.Area, &entry.ID, &entry.Code, &entry.Title, &entry.MediaID, &entry.MediaKind, &entry.WorkflowID, &entry.Variant, &entry.Subject, &entry.Detail, &entry.Count, &entry.Open, &entry.OccurredAt); err != nil {
			return ErrorLog{}, err
		}
		result.Entries = append(result.Entries, entry)
	}
	return result, rows.Err()
}
