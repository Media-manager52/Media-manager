package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/workflows"
)

// Newest-uploads watching: the store side. The reader in internal/acquisition
// compares each indexer's newest uploads with the waiting titles; this file
// lists those titles, remembers what was read and matched, and brings a
// matched title's own, already scheduled search forward. Nothing here selects
// a release or opens a download round.

// feedMatchLifetime is how long an unsettled match keeps trying to bring a
// search forward. After that the title's normal schedule has covered it.
const feedMatchLifetime = 24 * time.Hour

// feedMatchRetention is how long settled matches are kept for the owner to
// read.
const feedMatchRetention = 30 * 24 * time.Hour

// FeedWorkflow is one waiting title the newest uploads are compared with.
// Media carries the saved titles and identities only.
type FeedWorkflow struct {
	WorkflowID uuid.UUID
	Variant    string
	Media      catalog.MediaItem
}

// ListFeedWorkflows returns every workflow that is waiting for a release:
// waiting ones and held ones. Both have a search of their own scheduled.
func (store *Store) ListFeedWorkflows(ctx context.Context) ([]FeedWorkflow, error) {
	rows, err := store.Pool.Query(ctx, `SELECT workflow.id::text,workflow.variant,media.id::text,media.kind,media.title,media.year,
		COALESCE(metadata.data->>'original_title',''),COALESCE(metadata.data->'aliases','[]'::jsonb),
		COALESCE((SELECT jsonb_agg(jsonb_build_object('namespace',identity.namespace,'value',identity.value,'state',identity.state,'source',identity.source) ORDER BY identity.namespace,identity.value)
			FROM external_identities identity WHERE identity.media_id=media.id),'[]'::jsonb)
		FROM workflows workflow
		JOIN media_items media ON media.id=workflow.media_id
		LEFT JOIN media_metadata metadata ON metadata.media_id=media.id
		WHERE workflow.state IN ('waiting','held') AND media.deleted_at IS NULL
		ORDER BY media.id,workflow.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []FeedWorkflow{}
	media := map[string]catalog.MediaItem{}
	for rows.Next() {
		var workflowText, mediaText, kind, title, originalTitle string
		var item FeedWorkflow
		var year *int16
		var aliases, identities []byte
		if err := rows.Scan(&workflowText, &item.Variant, &mediaText, &kind, &title, &year, &originalTitle, &aliases, &identities); err != nil {
			return nil, err
		}
		item.WorkflowID, _ = uuid.Parse(workflowText)
		known, found := media[mediaText]
		if !found {
			known = catalog.MediaItem{Kind: catalog.MediaKind(kind), Title: title, Metadata: &catalog.MediaMetadata{OriginalTitle: originalTitle}}
			known.ID, _ = uuid.Parse(mediaText)
			if year != nil && *year > 0 {
				value := uint16(*year)
				known.Year = &value
			}
			// A malformed alias or identity list leaves that list empty: the
			// title then simply matches less, never more.
			_ = json.Unmarshal(aliases, &known.Metadata.Aliases)
			_ = json.Unmarshal(identities, &known.Identities)
			media[mediaText] = known
		}
		item.Media = known
		result = append(result, item)
	}
	return result, rows.Err()
}

// IndexerFeedState is what the reader last did for one indexer.
type IndexerFeedState struct {
	IndexerInstanceID   uuid.UUID
	LastReadAt          *time.Time
	NextReadAt          *time.Time
	IntervalSeconds     int
	LastOutcome         string
	ConsecutiveFailures int
	LastItemCount       int
	LastNewCount        int
}

func (store *Store) IndexerFeedStates(ctx context.Context) (map[uuid.UUID]IndexerFeedState, error) {
	rows, err := store.Pool.Query(ctx, `SELECT indexer_instance_id::text,last_read_at,next_read_at,interval_seconds,last_outcome,consecutive_failures,last_item_count,last_new_count FROM indexer_feed_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[uuid.UUID]IndexerFeedState{}
	for rows.Next() {
		var state IndexerFeedState
		var idText string
		if err := rows.Scan(&idText, &state.LastReadAt, &state.NextReadAt, &state.IntervalSeconds, &state.LastOutcome, &state.ConsecutiveFailures, &state.LastItemCount, &state.LastNewCount); err != nil {
			return nil, err
		}
		state.IndexerInstanceID, _ = uuid.Parse(idText)
		result[state.IndexerInstanceID] = state
	}
	return result, rows.Err()
}

// FeedMatch is one new upload that passed the title, identity and scope
// checks of a waiting workflow.
type FeedMatch struct {
	WorkflowID   uuid.UUID
	NativeGUID   string
	ReleaseTitle string
}

// RecordIndexerFeedRead saves the reader's state for one indexer together
// with the matches of that read, and reports how many matches are new. An
// upload that was matched before is left as it is.
func (store *Store) RecordIndexerFeedRead(ctx context.Context, state IndexerFeedState, matches []FeedMatch, now time.Time) (int, error) {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer transaction.Rollback(ctx)
	if _, err := transaction.Exec(ctx, `INSERT INTO indexer_feed_state(indexer_instance_id,last_read_at,next_read_at,interval_seconds,last_outcome,consecutive_failures,last_item_count,last_new_count,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (indexer_instance_id) DO UPDATE SET last_read_at=EXCLUDED.last_read_at,next_read_at=EXCLUDED.next_read_at,interval_seconds=EXCLUDED.interval_seconds,
			last_outcome=EXCLUDED.last_outcome,consecutive_failures=EXCLUDED.consecutive_failures,last_item_count=EXCLUDED.last_item_count,last_new_count=EXCLUDED.last_new_count,updated_at=EXCLUDED.updated_at`,
		state.IndexerInstanceID.String(), state.LastReadAt, state.NextReadAt, state.IntervalSeconds, state.LastOutcome, state.ConsecutiveFailures, state.LastItemCount, state.LastNewCount, now.UTC()); err != nil {
		return 0, err
	}
	inserted := 0
	for _, match := range matches {
		title := match.ReleaseTitle
		if len(title) > 500 {
			title = title[:500]
		}
		// The workflow may have been removed since the titles were listed.
		tag, err := transaction.Exec(ctx, `INSERT INTO indexer_feed_matches(id,indexer_instance_id,native_guid,workflow_id,release_title,seen_at)
			SELECT $1,$2,$3,$4,$5,$6 WHERE EXISTS(SELECT 1 FROM workflows WHERE id=$4)
			ON CONFLICT (indexer_instance_id,native_guid,workflow_id) DO NOTHING`,
			uuid.NewV7().String(), state.IndexerInstanceID.String(), match.NativeGUID, match.WorkflowID.String(), title, now.UTC())
		if err != nil {
			return 0, err
		}
		inserted += int(tag.RowsAffected())
	}
	return inserted, transaction.Commit(ctx)
}

// FeedAdvanceResult counts what one pass over the unsettled matches did.
type FeedAdvanceResult struct {
	SearchAdvanced    int
	SearchDue         int
	DiscoveryAdvanced int
	NotApplicable     int
	Expired           int
}

// AdvanceFeedMatches brings the scheduled search of matched workflows forward,
// at most limit workflows per call. A workflow that was searched within quiet
// is left for a later call, so a burst of uploads for one title costs one
// search per quiet period, and old settled matches are removed.
func (store *Store) AdvanceFeedMatches(ctx context.Context, now time.Time, quiet time.Duration, limit int) (FeedAdvanceResult, error) {
	var result FeedAdvanceResult
	now = now.UTC()
	tag, err := store.Pool.Exec(ctx, `UPDATE indexer_feed_matches SET outcome='expired',settled_at=$1 WHERE outcome='pending' AND seen_at<$2`, now, now.Add(-feedMatchLifetime))
	if err != nil {
		return result, err
	}
	result.Expired = int(tag.RowsAffected())
	if _, err := store.Pool.Exec(ctx, `DELETE FROM indexer_feed_matches WHERE seen_at<$1`, now.Add(-feedMatchRetention)); err != nil {
		return result, err
	}
	rows, err := store.Pool.Query(ctx, `SELECT hit.workflow_id::text FROM indexer_feed_matches hit
		WHERE hit.outcome='pending'
		  AND NOT EXISTS(SELECT 1 FROM search_runs run WHERE run.workflow_id=hit.workflow_id AND run.started_at>$1)
		GROUP BY hit.workflow_id ORDER BY min(hit.seen_at),hit.workflow_id LIMIT $2`, now.Add(-quiet), limit)
	if err != nil {
		return result, err
	}
	var due []uuid.UUID
	for rows.Next() {
		var idText string
		if err := rows.Scan(&idText); err != nil {
			rows.Close()
			return result, err
		}
		if id, parseErr := uuid.Parse(idText); parseErr == nil {
			due = append(due, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return result, err
	}
	for _, workflowID := range due {
		outcome, err := store.advanceFeedWorkflow(ctx, workflowID, now, quiet)
		if err != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			// A concurrent change to this workflow: its matches stay pending
			// and the next pass decides again.
			continue
		}
		switch outcome {
		case "search_advanced":
			result.SearchAdvanced++
		case "search_due":
			result.SearchDue++
		case "discovery_advanced":
			result.DiscoveryAdvanced++
		case "not_applicable":
			result.NotApplicable++
		}
	}
	return result, nil
}

// advanceFeedWorkflow settles the pending matches of one workflow. It returns
// "" when they stay pending.
func (store *Store) advanceFeedWorkflow(ctx context.Context, workflowID uuid.UUID, now time.Time, quiet time.Duration) (string, error) {
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return "", err
	}
	defer transaction.Rollback(ctx)
	var state workflows.State
	var revision int64
	if err := transaction.QueryRow(ctx, `SELECT state,revision FROM workflows WHERE id=$1 FOR UPDATE`, workflowID.String()).Scan(&state, &revision); errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	outcome := ""
	switch {
	case state != workflows.StateWaiting && state != workflows.StateHeld:
		// Searching, downloading, done or with the owner: nothing to bring forward.
		outcome = "not_applicable"
	default:
		var searchedRecently bool
		if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM search_runs WHERE workflow_id=$1 AND started_at>$2)`, workflowID.String(), now.Add(-quiet)).Scan(&searchedRecently); err != nil {
			return "", err
		}
		if searchedRecently {
			return "", nil
		}
		outcome, err = advanceFeedSearch(ctx, transaction, workflowID, revision, state, now)
		if err != nil {
			return "", err
		}
	}
	if outcome == "" {
		return "", nil
	}
	if _, err := transaction.Exec(ctx, `UPDATE indexer_feed_matches SET outcome=$2,settled_at=$3 WHERE workflow_id=$1 AND outcome='pending'`, workflowID.String(), outcome, now); err != nil {
		return "", err
	}
	return outcome, transaction.Commit(ctx)
}

// advanceFeedSearch brings forward whichever search the workflow already has
// scheduled: its targeted search, or the paced discovery of an exhausted round.
func advanceFeedSearch(ctx context.Context, transaction pgx.Tx, workflowID uuid.UUID, revision int64, state workflows.State, now time.Time) (string, error) {
	var scheduled, partial, leased bool
	var availableAt time.Time
	err := transaction.QueryRow(ctx, `SELECT true,payload ? 'partial_retry',lease_until IS NOT NULL AND lease_until>=$3,available_at FROM outbox_events
		WHERE aggregate_type='workflow' AND aggregate_id=$1 AND aggregate_revision=$2 AND event_type='workflow.search_requested' AND completed_at IS NULL
		FOR UPDATE`, workflowID.String(), revision, now).Scan(&scheduled, &partial, &leased, &availableAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	if scheduled {
		if leased || !availableAt.After(now) {
			return "search_due", nil
		}
		if partial {
			// The rest of an unfinished search: it keeps its own short
			// schedule and its saved answers. The search after it is advanced.
			return "", nil
		}
		if _, err := transaction.Exec(ctx, `UPDATE outbox_events SET available_at=$3
			WHERE aggregate_type='workflow' AND aggregate_id=$1 AND aggregate_revision=$2 AND event_type='workflow.search_requested' AND completed_at IS NULL`, workflowID.String(), revision, now); err != nil {
			return "", err
		}
		return "search_advanced", nil
	}
	if state != workflows.StateWaiting {
		return "not_applicable", nil
	}
	err = advanceWorkflowDiscovery(ctx, transaction, workflowID, now, "worker", "feed_match")
	var refusal ActionRefusal
	if errors.As(err, &refusal) {
		// No exhausted round is waiting for discovery (for example: the owner
		// is already offered a new release, or discovery is paused).
		return "not_applicable", nil
	}
	if err != nil {
		return "", err
	}
	return "discovery_advanced", nil
}

// IndexerFeedSummary is the owner's view of the newest-uploads reader for one
// indexer. LastOutcome is "ok" after a read, "deferred" when the indexer's
// pacing or daily limit postponed it, else the failure code of the last
// attempt. Matches24h counts uploads that matched a waiting title.
type IndexerFeedSummary struct {
	LastReadAt  *time.Time `json:"last_read_at,omitempty"`
	NextReadAt  *time.Time `json:"next_read_at,omitempty"`
	LastOutcome string     `json:"last_outcome,omitempty"`
	Matches24h  int        `json:"matches_24h"`
}

func (store *Store) IndexerFeedSummaries(ctx context.Context, now time.Time) (map[uuid.UUID]IndexerFeedSummary, error) {
	rows, err := store.Pool.Query(ctx, `SELECT feed.indexer_instance_id::text,feed.last_read_at,feed.next_read_at,feed.last_outcome,
		(SELECT count(*) FROM indexer_feed_matches hit WHERE hit.indexer_instance_id=feed.indexer_instance_id AND hit.seen_at>$1)
		FROM indexer_feed_state feed`, now.UTC().Add(-24*time.Hour))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[uuid.UUID]IndexerFeedSummary{}
	for rows.Next() {
		var summary IndexerFeedSummary
		var idText string
		if err := rows.Scan(&idText, &summary.LastReadAt, &summary.NextReadAt, &summary.LastOutcome, &summary.Matches24h); err != nil {
			return nil, err
		}
		if id, parseErr := uuid.Parse(idText); parseErr == nil {
			result[id] = summary
		}
	}
	return result, rows.Err()
}
