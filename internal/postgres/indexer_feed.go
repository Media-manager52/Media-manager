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
// a release. A download round is opened only where the owner's own "start
// next round" would be accepted: for a title whose round is used up and whose
// discovery holds an eligible release (openFeedRound).

// feedMatchLifetime is how long an unsettled match keeps trying to bring a
// search forward. After that the title's normal schedule has covered it.
const feedMatchLifetime = 24 * time.Hour

// feedMatchRetention is how long settled matches are kept for the owner to
// read.
const feedMatchRetention = 30 * 24 * time.Hour

// feedRoundInterval is the least time between two rounds opened for one title
// because of new uploads. With the attempts per round and the retained-output
// limit it bounds what a title that keeps failing can cost.
const feedRoundInterval = 24 * time.Hour

// feedRoundReason is the transition reason of a round opened for a new upload.
const feedRoundReason = "a new upload matched this title; a new bounded acquisition round was opened after new eligible discovery"

// rescanRoundReason is the transition reason of a round opened because the
// title's own scheduled rescan found a newly observed eligible release.
const rescanRoundReason = "the scheduled rescan found a newly observed eligible release; a new bounded acquisition round was opened"

// automaticRoundReasons are every reason the worker opens a round with. A
// title gets at most one such round per feedRoundInterval, whichever path
// opened it.
var automaticRoundReasons = []string{feedRoundReason, rescanRoundReason}

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
	RoundsOpened      int
	NotApplicable     int
	Expired           int
}

// AdvanceFeedMatches brings the scheduled search of matched workflows forward,
// at most limit workflows per call. A workflow that was searched within quiet
// is left for a later call, so a burst of uploads for one title costs one
// search per quiet period, and old settled matches are removed. With
// openRounds a matched title whose round is used up also gets its next round
// opened when discovery holds an eligible release for it (openFeedRound).
func (store *Store) AdvanceFeedMatches(ctx context.Context, now time.Time, quiet time.Duration, limit int, openRounds bool) (FeedAdvanceResult, error) {
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
		outcome, err := store.advanceFeedWorkflow(ctx, workflowID, now, quiet, openRounds)
		if err != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			// A concurrent change to this workflow: its matches stay pending
			// and the next pass decides again.
			continue
		}
		result.count(outcome)
	}
	if !openRounds {
		return result, nil
	}
	found, err := store.feedDiscoveriesWithARelease(ctx, now, limit)
	if err != nil {
		return result, err
	}
	for _, workflowID := range found {
		outcome, err := store.openFoundFeedRound(ctx, workflowID, now)
		if err != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			continue
		}
		result.count(outcome)
	}
	return result, nil
}

func (result *FeedAdvanceResult) count(outcome string) {
	switch outcome {
	case "search_advanced":
		result.SearchAdvanced++
	case "search_due":
		result.SearchDue++
	case "discovery_advanced":
		result.DiscoveryAdvanced++
	case "round_opened":
		result.RoundsOpened++
	case "not_applicable":
		result.NotApplicable++
	}
}

// advanceFeedWorkflow settles the pending matches of one workflow. It returns
// "" when they stay pending.
func (store *Store) advanceFeedWorkflow(ctx context.Context, workflowID uuid.UUID, now time.Time, quiet time.Duration, openRounds bool) (string, error) {
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
		if outcome == "not_applicable" && openRounds && state == workflows.StateWaiting {
			// No search and no discovery to bring forward. Discovery may
			// already hold a release that waits for a new round.
			outcome, err = store.openFeedRound(ctx, transaction, workflowID, now)
			if err != nil {
				return "", err
			}
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

// openFeedRound opens the next bounded round for a matched title exactly as
// the owner's "start next round" would, and under every guard of that action:
// the workflow is waiting, its newest round is exhausted, discovery holds an
// eligible release that was not tried, the profile's policy is the round's,
// and the retained-output limit is not reached. On top of that a title gets
// at most one such round per feedRoundInterval. The round's search then asks
// the indexers and applies the profile's rules as every search does. It
// returns "round_opened", or "not_applicable" when the action is refused.
func (store *Store) openFeedRound(ctx context.Context, transaction pgx.Tx, workflowID uuid.UUID, now time.Time) (string, error) {
	return store.openAutomaticRound(ctx, transaction, workflowID, now, feedRoundReason, "feed_match")
}

// openAutomaticRound is openFeedRound for either path: reason is recorded on
// the transition and source on the audit event.
func (store *Store) openAutomaticRound(ctx context.Context, transaction pgx.Tx, workflowID uuid.UUID, now time.Time, reason, source string) (string, error) {
	var recent bool
	if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workflow_transitions WHERE workflow_id=$1 AND actor='worker' AND reason=ANY($2) AND occurred_at>$3)`,
		workflowID.String(), automaticRoundReasons, now.Add(-feedRoundInterval)).Scan(&recent); err != nil {
		return "", err
	}
	if recent {
		return "not_applicable", nil
	}
	// A savepoint, so a refusal leaves nothing of the attempt behind in the
	// caller's transaction.
	attempt, err := transaction.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer attempt.Rollback(ctx)
	err = store.openNextAcquisitionRound(ctx, attempt, workflowID, now, "worker", reason)
	var refusal ActionRefusal
	if errors.As(err, &refusal) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrConcurrentModification) {
		if rollbackErr := attempt.Rollback(ctx); rollbackErr != nil {
			return "", rollbackErr
		}
		return "not_applicable", nil
	}
	if err != nil {
		return "", err
	}
	detail, _ := json.Marshal(map[string]any{"reason": source})
	if _, err := attempt.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at)
		VALUES($1,'worker','workflow.acquisition_round_opened','workflow',$2,$3,$4)`, uuid.NewV7().String(), workflowID.String(), detail, now); err != nil {
		return "", err
	}
	if err := attempt.Commit(ctx); err != nil {
		return "", err
	}
	return "round_opened", nil
}

// feedDiscoveriesWithARelease lists the titles whose discovery a match brought
// forward within feedMatchLifetime and which has found a release since.
func (store *Store) feedDiscoveriesWithARelease(ctx context.Context, now time.Time, limit int) ([]uuid.UUID, error) {
	rows, err := store.Pool.Query(ctx, `SELECT hit.workflow_id::text FROM indexer_feed_matches hit
		JOIN workflow_retry_discovery discovery ON discovery.workflow_id=hit.workflow_id AND discovery.state='ready'
		WHERE hit.outcome='discovery_advanced' AND hit.settled_at>$1
		GROUP BY hit.workflow_id ORDER BY min(hit.settled_at),hit.workflow_id LIMIT $2`, now.Add(-feedMatchLifetime), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var found []uuid.UUID
	for rows.Next() {
		var idText string
		if err := rows.Scan(&idText); err != nil {
			return nil, err
		}
		if id, parseErr := uuid.Parse(idText); parseErr == nil {
			found = append(found, id)
		}
	}
	return found, rows.Err()
}

// openFoundFeedRound finishes what a match started: the discovery it brought
// forward found a release, so the round is opened and the matches say so.
// A refusal settles them as not applicable, so the title is not asked again
// for the same uploads.
func (store *Store) openFoundFeedRound(ctx context.Context, workflowID uuid.UUID, now time.Time) (string, error) {
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return "", err
	}
	defer transaction.Rollback(ctx)
	outcome, err := store.openFeedRound(ctx, transaction, workflowID, now)
	if err != nil {
		return "", err
	}
	if _, err := transaction.Exec(ctx, `UPDATE indexer_feed_matches SET outcome=$2,settled_at=$3 WHERE workflow_id=$1 AND outcome='discovery_advanced' AND settled_at>$4`,
		workflowID.String(), outcome, now, now.Add(-feedMatchLifetime)); err != nil {
		return "", err
	}
	return outcome, transaction.Commit(ctx)
}

// RescanRoundResult counts what OpenRescanFoundRounds did.
type RescanRoundResult struct {
	RoundsOpened int
	Refused      int
}

// OpenRescanFoundRounds opens the next round for titles whose round is used up
// and whose own scheduled rescan found a newly observed eligible release
// (discovery `ready`), at most limit titles per call. It is the owner's "start
// next round" under every one of its guards, and shares the limit of one
// automatic round per title per feedRoundInterval with the newest-uploads
// path. A refusal writes nothing but a note on the discovery, so the same
// discovery is not asked again; a later discovery is.
func (store *Store) OpenRescanFoundRounds(ctx context.Context, now time.Time, limit int) (RescanRoundResult, error) {
	var result RescanRoundResult
	now = now.UTC()
	rows, err := store.Pool.Query(ctx, `SELECT discovery.workflow_id::text FROM workflow_retry_discovery discovery
		JOIN workflows workflow ON workflow.id=discovery.workflow_id
		WHERE discovery.state='ready' AND workflow.state='waiting' AND workflow.terminal_at IS NULL
		  AND discovery.automatic_round_refused_generation IS DISTINCT FROM discovery.rescan_generation
		  AND NOT EXISTS(SELECT 1 FROM workflow_transitions transition WHERE transition.workflow_id=discovery.workflow_id
		    AND transition.actor='worker' AND transition.reason=ANY($1) AND transition.occurred_at>$2)
		ORDER BY discovery.updated_at,discovery.workflow_id LIMIT $3`, automaticRoundReasons, now.Add(-feedRoundInterval), limit)
	if err != nil {
		return result, err
	}
	var ready []uuid.UUID
	for rows.Next() {
		var idText string
		if err := rows.Scan(&idText); err != nil {
			rows.Close()
			return result, err
		}
		if id, parseErr := uuid.Parse(idText); parseErr == nil {
			ready = append(ready, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return result, err
	}
	for _, workflowID := range ready {
		outcome, err := store.openRescanRound(ctx, workflowID, now)
		if err != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			// A concurrent change: the next call decides again.
			continue
		}
		switch outcome {
		case "round_opened":
			result.RoundsOpened++
		case "not_applicable":
			result.Refused++
		}
	}
	return result, nil
}

func (store *Store) openRescanRound(ctx context.Context, workflowID uuid.UUID, now time.Time) (string, error) {
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return "", err
	}
	defer transaction.Rollback(ctx)
	outcome, err := store.openAutomaticRound(ctx, transaction, workflowID, now, rescanRoundReason, "rescan_found")
	if err != nil {
		return "", err
	}
	if outcome == "not_applicable" {
		if _, err := transaction.Exec(ctx, `UPDATE workflow_retry_discovery SET automatic_round_refused_generation=rescan_generation WHERE workflow_id=$1 AND state='ready'`, workflowID.String()); err != nil {
			return "", err
		}
	}
	return outcome, transaction.Commit(ctx)
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
