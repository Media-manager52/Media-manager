package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Problem is one owner-actionable condition derived from persisted state. The
// feed is a read model: recording a problem never writes to the database and
// fixing the underlying condition makes it disappear.
type Problem struct {
	Code     string     `json:"code"`
	Area     string     `json:"area"`
	Level    string     `json:"level"`
	Title    string     `json:"title"`
	Detail   string     `json:"detail,omitempty"`
	Count    int64      `json:"count"`
	OldestAt *time.Time `json:"oldest_at,omitempty"`
	LatestAt *time.Time `json:"latest_at,omitempty"`
	Href     string     `json:"href,omitempty"`
}

type ProblemFeed struct {
	Problems  []Problem `json:"problems"`
	CheckedAt time.Time `json:"checked_at"`
}

// Problems derives the current actionable conditions. It deliberately groups
// repeat conditions so one misconfigured integration or one broken release
// does not hide the rest of the system.
func (store *Store) Problems(ctx context.Context, now time.Time) (ProblemFeed, error) {
	feed := ProblemFeed{Problems: []Problem{}, CheckedAt: now.UTC()}

	workflowRows, err := store.Pool.Query(ctx, `SELECT COALESCE(NULLIF(attention_code,''),'needs_attention'),count(*),min(updated_at),max(updated_at),
		(array_agg(COALESCE(NULLIF(attention_detail,''),'') ORDER BY updated_at DESC))[1]
		FROM workflows WHERE terminal_at IS NULL AND state='needs_attention'
		GROUP BY 1 ORDER BY 2 DESC`)
	if err != nil {
		return ProblemFeed{}, err
	}
	for workflowRows.Next() {
		var code, detail string
		var count int64
		var oldest, latest time.Time
		if err := workflowRows.Scan(&code, &count, &oldest, &latest, &detail); err != nil {
			workflowRows.Close()
			return ProblemFeed{}, err
		}
		feed.Problems = append(feed.Problems, Problem{
			Code: "workflow.needs_attention", Area: "workflows", Level: "error",
			Title: "Workflow: " + HumanizeCode(code), Detail: clipDetail(detail),
			Count: count, OldestAt: &oldest, LatestAt: &latest, Href: "/activity/queue",
		})
	}
	workflowRows.Close()
	if err := workflowRows.Err(); err != nil {
		return ProblemFeed{}, err
	}

	outboxRows, err := store.Pool.Query(ctx, `SELECT event_type,count(*),min(available_at),max(occurred_at),
		(array_agg(COALESCE(NULLIF(last_error,''),'') ORDER BY occurred_at DESC))[1]
		FROM outbox_events WHERE completed_at IS NULL AND attempts>0
		GROUP BY 1 ORDER BY 2 DESC LIMIT 20`)
	if err != nil {
		return ProblemFeed{}, err
	}
	for outboxRows.Next() {
		var eventType, detail string
		var count int64
		var oldest, latest time.Time
		if err := outboxRows.Scan(&eventType, &count, &oldest, &latest, &detail); err != nil {
			outboxRows.Close()
			return ProblemFeed{}, err
		}
		feed.Problems = append(feed.Problems, Problem{
			Code: "worker.retrying", Area: "worker", Level: "warning",
			Title: "Worker retrying: " + HumanizeCode(strings.TrimPrefix(eventType, "workflow.")), Detail: clipDetail(detail),
			Count: count, OldestAt: &oldest, LatestAt: &latest, Href: "/system/health",
		})
	}
	outboxRows.Close()
	if err := outboxRows.Err(); err != nil {
		return ProblemFeed{}, err
	}

	components, err := store.ListSourceStatuses(ctx)
	if err != nil {
		return ProblemFeed{}, err
	}
	for _, component := range components {
		if !component.Enabled || component.ConsecutiveFailure == 0 && component.ConfigurationState == "configured" {
			continue
		}
		detail := fmt.Sprintf("%s · %s", component.Kind, HumanizeCode(component.ConfigurationState))
		if component.LastOutcome != "" {
			detail += " · last outcome " + HumanizeCode(component.LastOutcome)
		}
		if component.ConsecutiveFailure > 0 {
			detail += fmt.Sprintf(" · %d consecutive failure(s)", component.ConsecutiveFailure)
		}
		latest := component.UpdatedAt
		if component.BudgetUpdatedAt != nil {
			latest = *component.BudgetUpdatedAt
		}
		feed.Problems = append(feed.Problems, Problem{
			Code: "integration.failure", Area: "integrations", Level: "error",
			Title: component.Name, Detail: clipDetail(detail), Count: 1, LatestAt: &latest,
			Href: integrationHref(component.Kind, component.ID.String()),
		})
	}

	backupRows, err := store.Pool.Query(ctx, `SELECT id::text,state,COALESCE(last_error,''),COALESCE(offsite_state,''),COALESCE(offsite_error,''),created_at
		FROM backup_records WHERE state='failed' OR offsite_state='failed' ORDER BY created_at DESC LIMIT 10`)
	if err != nil {
		return ProblemFeed{}, err
	}
	for backupRows.Next() {
		var id, state, lastError, offsiteState, offsiteError string
		var createdAt time.Time
		if err := backupRows.Scan(&id, &state, &lastError, &offsiteState, &offsiteError, &createdAt); err != nil {
			backupRows.Close()
			return ProblemFeed{}, err
		}
		if state == "failed" {
			feed.Problems = append(feed.Problems, Problem{
				Code: "backup.failed", Area: "backups", Level: "error",
				Title: "Encrypted backup failed", Detail: clipDetail(lastError), Count: 1,
				OldestAt: &createdAt, LatestAt: &createdAt, Href: "/system/backups",
			})
		}
		if offsiteState == "failed" {
			feed.Problems = append(feed.Problems, Problem{
				Code: "backup.offsite_failed", Area: "backups", Level: "warning",
				Title: "Off-site backup copy failed", Detail: clipDetail(offsiteError), Count: 1,
				OldestAt: &createdAt, LatestAt: &createdAt, Href: "/system/backups",
			})
		}
	}
	backupRows.Close()
	if err := backupRows.Err(); err != nil {
		return ProblemFeed{}, err
	}

	notificationRows, err := store.Pool.Query(ctx, `SELECT destination.name,count(*),min(delivery.created_at),max(delivery.created_at),
		(array_agg(COALESCE(NULLIF(delivery.last_error,''),'') ORDER BY delivery.created_at DESC))[1]
		FROM notification_deliveries delivery JOIN notification_destinations destination ON destination.id=delivery.destination_id
		WHERE delivery.state='failed' GROUP BY destination.name ORDER BY 2 DESC`)
	if err != nil {
		return ProblemFeed{}, err
	}
	for notificationRows.Next() {
		var destination, detail string
		var count int64
		var oldest, latest time.Time
		if err := notificationRows.Scan(&destination, &count, &oldest, &latest, &detail); err != nil {
			notificationRows.Close()
			return ProblemFeed{}, err
		}
		feed.Problems = append(feed.Problems, Problem{
			Code: "notification.failed", Area: "notifications", Level: "warning",
			Title: "Notification delivery: " + destination, Detail: clipDetail(detail),
			Count: count, OldestAt: &oldest, LatestAt: &latest, Href: "/settings/notifications",
		})
	}
	notificationRows.Close()
	if err := notificationRows.Err(); err != nil {
		return ProblemFeed{}, err
	}

	repairRows, err := store.Pool.Query(ctx, `SELECT reason,count(*),min(created_at),max(created_at),
		(array_agg(COALESCE(NULLIF(detail,''),'') ORDER BY created_at DESC))[1]
		FROM altmount_repairs WHERE state='needs_attention' GROUP BY reason ORDER BY 2 DESC`)
	if err != nil {
		return ProblemFeed{}, err
	}
	for repairRows.Next() {
		var reason, detail string
		var count int64
		var oldest, latest time.Time
		if err := repairRows.Scan(&reason, &count, &oldest, &latest, &detail); err != nil {
			repairRows.Close()
			return ProblemFeed{}, err
		}
		feed.Problems = append(feed.Problems, Problem{
			Code: "repair.needs_attention", Area: "repairs", Level: "warning",
			Title: "Repair: " + HumanizeCode(reason), Detail: clipDetail(detail),
			Count: count, OldestAt: &oldest, LatestAt: &latest, Href: "/wanted",
		})
	}
	repairRows.Close()
	if err := repairRows.Err(); err != nil {
		return ProblemFeed{}, err
	}

	var schemaVersion int64
	if err := store.Pool.QueryRow(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&schemaVersion); err != nil {
		return ProblemFeed{}, err
	}
	if schemaVersion != CurrentSchemaVersion {
		feed.Problems = append(feed.Problems, Problem{
			Code: "system.schema", Area: "system", Level: "error",
			Title:  "Schema version mismatch",
			Detail: fmt.Sprintf("running %d, expected %d", schemaVersion, CurrentSchemaVersion),
			Count:  1, Href: "/system/health",
		})
	}
	return feed, nil
}

func integrationHref(kind, id string) string {
	switch kind {
	case "newznab", "oldboys":
		return "/sources/indexers/" + id
	case "altmount", "plex", "seerr":
		return "/settings/connections"
	default:
		return "/system/health"
	}
}

func clipDetail(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 500 {
		cut := 500
		for cut > 0 && !utf8.RuneStart(value[cut]) {
			cut--
		}
		value = value[:cut]
	}
	return value
}

// HumanizeCode turns a stable code into the short label shown in the problem
// list and the support text: "season_pack_incomplete" -> "Season pack incomplete".
func HumanizeCode(code string) string {
	words := strings.Fields(strings.ReplaceAll(strings.TrimSpace(code), "_", " "))
	if len(words) == 0 {
		return "Unknown"
	}
	words[0] = strings.ToUpper(words[0][:1]) + words[0][1:]
	return strings.Join(words, " ")
}
