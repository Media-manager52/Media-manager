package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/workflows"
)

// AiringDispatchResult summarizes how a complete-season workflow was scheduled.
// It is used by tests and by the owner-facing read model.
type AiringDispatchResult struct {
	Dispatched       bool        `json:"dispatched"`
	SeasonComplete   bool        `json:"season_complete"`
	EntirelyMissing  bool        `json:"entirely_missing"`
	ReleasedMissing  []uint16    `json:"released_missing"`
	FutureEpisodes   []uint16    `json:"future_episodes"`
	InProgress       []uint16    `json:"in_progress"`
	CoveredEpisodes  []uint16    `json:"covered_episodes"`
	CreatedWorkflows []uuid.UUID `json:"created_workflow_ids"`
	NextDispatchAt   *time.Time  `json:"next_dispatch_at,omitempty"`
}

type airingExpectedEpisode struct {
	Episode    uint16
	ProviderID string
	Title      string
	AirDate    *time.Time
}

// DispatchAiringSeason coordinates a complete-season workflow. It returns
// dispatched=true when the season is currently airing or only partially
// acquired, meaning released missing episodes must be acquired individually and
// the caller must not search for a complete season pack. It returns
// dispatched=false only for a complete season that is entirely missing, where
// the existing single season-pack path remains correct.
//
// The method is idempotent: an episode workflow that is already confirmed or in
// progress is never recreated, and the partial unique index on active workflow
// variants closes the creation race.
func (store *Store) DispatchAiringSeason(ctx context.Context, workflowID uuid.UUID, now time.Time) (bool, error) {
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return false, err
	}
	defer transaction.Rollback(ctx)

	var mediaText, variant, workflowState string
	var revision int64
	var rootText, profileText string
	var clientText *string
	if err := transaction.QueryRow(ctx, `SELECT media_id::text,variant,state,revision,root_id::text,quality_profile_id::text,download_client_id::text FROM workflows WHERE id=$1 FOR UPDATE`, workflowID.String()).Scan(&mediaText, &variant, &workflowState, &revision, &rootText, &profileText, &clientText); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrNotFound
		}
		return false, err
	}
	scope, ok := workflows.ParseVariant(variant)
	if !ok || scope.Kind != workflows.VariantSeason {
		return false, nil
	}
	mediaID, err := uuid.Parse(mediaText)
	if err != nil {
		return false, err
	}
	rootID, err := uuid.Parse(rootText)
	if err != nil {
		return false, err
	}
	profileID, err := uuid.Parse(profileText)
	if err != nil {
		return false, err
	}
	var clientID uuid.UUID
	if clientText != nil {
		clientID, _ = uuid.Parse(*clientText)
	}

	var expectationText, seriesIdentity string
	var sourceRevision int64
	if err := transaction.QueryRow(ctx, `SELECT id::text,series_identity,source_revision FROM pack_expectation_sets WHERE workflow_id=$1 ORDER BY frozen_at DESC,id DESC LIMIT 1`, workflowID.String()).Scan(&expectationText, &seriesIdentity, &sourceRevision); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// A season without a frozen expectation keeps the legacy pack
			// search; it is not an airing coordination failure.
			return false, nil
		}
		return false, err
	}
	rows, err := transaction.Query(ctx, `SELECT season,episode,COALESCE(provider_episode_id,''),title,air_date FROM pack_expected_episodes WHERE expectation_id=$1 ORDER BY episode`, expectationText)
	if err != nil {
		return false, err
	}
	expected := []airingExpectedEpisode{}
	for rows.Next() {
		var season, episode int16
		var providerID, title string
		var airDate *time.Time
		if err := rows.Scan(&season, &episode, &providerID, &title, &airDate); err != nil {
			rows.Close()
			return false, err
		}
		expected = append(expected, airingExpectedEpisode{Episode: uint16(episode), ProviderID: providerID, Title: title, AirDate: airDate})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}
	if len(expected) == 0 {
		return false, nil
	}

	// An episode is only known released once its date has passed. A date-only
	// value is treated conservatively: the episode becomes actionable at the
	// next UTC midnight after its air date, which matches when a same-day web
	// release is normally available.
	today := now.UTC().Truncate(24 * time.Hour)
	seasonComplete := true
	nextDispatchAt := time.Time{}
	for _, episode := range expected {
		if episode.AirDate != nil && !episode.AirDate.Before(today) {
			seasonComplete = false
			candidate := episode.AirDate.UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
			if nextDispatchAt.IsZero() || candidate.Before(nextDispatchAt) {
				nextDispatchAt = candidate
			}
		}
	}

	existing, err := loadEpisodeWorkflowStates(ctx, transaction, mediaID, scope.Season)
	if err != nil {
		return false, err
	}
	releasedMissing := []uint16{}
	futureEpisodes := []uint16{}
	inProgress := []uint16{}
	covered := []uint16{}
	for _, episode := range expected {
		state, exists := existing[episode.Episode]
		switch {
		case !exists || state == string(workflows.StateCancelled):
			if seasonComplete || (episode.AirDate != nil && episode.AirDate.Before(today)) {
				releasedMissing = append(releasedMissing, episode.Episode)
			} else {
				futureEpisodes = append(futureEpisodes, episode.Episode)
			}
		case state == string(workflows.StatePlexConfirmed):
			covered = append(covered, episode.Episode)
		default:
			inProgress = append(inProgress, episode.Episode)
		}
	}

	entirelyMissing := len(covered) == 0 && len(inProgress) == 0
	if seasonComplete && entirelyMissing {
		// Complete and entirely missing: the existing one-pack workflow is the
		// correct acquisition. The caller continues the season-pack search.
		return false, nil
	}

	// A future episode with a nil air date is never guessed at: the caller keeps
	// monitoring and the next dispatch attempts it once the season is complete
	// or a date arrives.
	created := []uuid.UUID{}
	for _, episode := range expected {
		if !containsEpisode(releasedMissing, episode.Episode) {
			continue
		}
		episodeVariant := workflows.EpisodeVariant(scope.Season, episode.Episode)
		episodeWorkflowID := uuid.NewV7()
		command, insertErr := transaction.Exec(ctx, `INSERT INTO workflows(id,media_id,state,root_id,quality_profile_id,download_client_id,variant,revision,created_at,updated_at)
			VALUES($1,$2,'monitored',$3,$4,$5,$6,1,$7,$7)
			ON CONFLICT (media_id,variant) WHERE terminal_at IS NULL DO NOTHING`, episodeWorkflowID.String(), mediaID.String(), rootID.String(), profileID.String(), nullableUUID(clientID), episodeVariant, now.UTC())
		if insertErr != nil {
			return true, insertErr
		}
		if command.RowsAffected() != 1 {
			continue
		}
		if err := insertInitialAcquisitionRound(ctx, transaction, episodeWorkflowID, mediaID, profileID, episodeVariant, now); err != nil {
			return true, err
		}
		if err := insertEpisodeExpectation(ctx, transaction, episodeWorkflowID, mediaID, seriesIdentity, sourceRevision, scope.Season, episode.Episode, now); err != nil {
			return true, err
		}
		payload, _ := json.Marshal(map[string]string{"workflow_id": episodeWorkflowID.String(), "variant": episodeVariant})
		if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'workflow',$2,1,'workflow.search_requested',$3,$4,$4)`, uuid.NewV7().String(), episodeWorkflowID.String(), string(payload), now.UTC()); err != nil {
			return true, err
		}
		created = append(created, episodeWorkflowID)
	}

	detail := "Airing season: released episodes are acquired individually as they air; future episodes are not searched before their release date."
	observation := AiringDispatchResult{
		Dispatched:       true,
		SeasonComplete:   seasonComplete,
		EntirelyMissing:  entirelyMissing,
		ReleasedMissing:  releasedMissing,
		FutureEpisodes:   futureEpisodes,
		InProgress:       inProgress,
		CoveredEpisodes:  covered,
		CreatedWorkflows: created,
	}
	if !nextDispatchAt.IsZero() {
		observation.NextDispatchAt = &nextDispatchAt
	}
	detailJSON, _ := json.Marshal(observation)

	if workflowState == string(workflows.StateMonitored) {
		if _, err := transaction.Exec(ctx, `INSERT INTO workflow_transitions(workflow_id,to_revision,from_state,to_state,reason,actor,occurred_at) VALUES($1,$2,$3,'waiting','airing season coordinated into individual episodes','worker',$4)`, workflowID.String(), revision+1, workflowState, now.UTC()); err != nil {
			return true, err
		}
	}
	// attention_detail carries the owner-facing waiting reason; attention_code
	// stays NULL so a healthy monitoring wait is never mistaken for an owner
	// recovery state.
	if _, err := transaction.Exec(ctx, `UPDATE workflows SET state='waiting',revision=$2,updated_at=$3,attention_code=NULL,attention_detail=$4 WHERE id=$1`, workflowID.String(), revision+1, now.UTC(), detail); err != nil {
		return true, err
	}
	// Schedule the next coordination at the earliest next release moment. When
	// no future episode remains, no season wake is needed: any in-progress
	// episode workflow owns its own retry.
	if observation.NextDispatchAt != nil && observation.NextDispatchAt.After(now.UTC()) {
		payload, _ := json.Marshal(map[string]string{"workflow_id": workflowID.String(), "reason": "airing season next episode"})
		if _, err := transaction.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'workflow',$2,$3,'workflow.search_requested',$4,$5,$6) ON CONFLICT DO NOTHING`, uuid.NewV7().String(), workflowID.String(), revision+1, string(payload), now.UTC(), observation.NextDispatchAt.UTC()); err != nil {
			return true, err
		}
	}
	audit, _ := json.Marshal(map[string]any{"workflow_id": workflowID.String(), "season": scope.Season, "detail": json.RawMessage(detailJSON)})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'worker','season.airing_coordinated','workflow',$2,$3,$4)`, uuid.NewV7().String(), workflowID.String(), string(audit), now.UTC()); err != nil {
		return true, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return true, err
	}
	return true, nil
}

func loadEpisodeWorkflowStates(ctx context.Context, transaction pgx.Tx, mediaID uuid.UUID, season uint16) (map[uint16]string, error) {
	rows, err := transaction.Query(ctx, `SELECT variant,state FROM workflows WHERE media_id=$1 AND variant LIKE $2`, mediaID.String(), fmt.Sprintf("season:%02d:episode:%%", season))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[uint16]string{}
	for rows.Next() {
		var variant, state string
		if err := rows.Scan(&variant, &state); err != nil {
			return nil, err
		}
		parsed, ok := workflows.ParseVariant(variant)
		if !ok || parsed.Kind != workflows.VariantEpisode {
			continue
		}
		// A confirmed episode always wins so a later cancelled duplicate can
		// never make covered content look missing.
		if previous, exists := result[parsed.Episode]; exists && previous == string(workflows.StatePlexConfirmed) {
			continue
		}
		result[parsed.Episode] = state
	}
	return result, rows.Err()
}

func insertEpisodeExpectation(ctx context.Context, transaction pgx.Tx, workflowID, mediaID uuid.UUID, seriesIdentity string, sourceRevision int64, season, episode uint16, now time.Time) error {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d", mediaID.String(), season, episode)))
	expectationID := uuid.NewV7()
	if _, err := transaction.Exec(ctx, `INSERT INTO pack_expectation_sets(id,workflow_id,series_identity,season,source_revision,expectation_digest,created_at,frozen_at) VALUES($1,$2,$3,$4,$5,$6,$7,$7)`, expectationID.String(), workflowID.String(), seriesIdentity, int16(season), sourceRevision, digest[:], now.UTC()); err != nil {
		return err
	}
	_, err := transaction.Exec(ctx, `INSERT INTO pack_expected_episodes(expectation_id,season,episode,provider_episode_id,title,air_date)
		SELECT $1,$2,$3,provider_episode_id,title,air_date FROM pack_expected_episodes expected
		JOIN pack_expectation_sets set_row ON set_row.id=expected.expectation_id
		WHERE set_row.series_identity=$4 AND expected.season=$2 AND expected.episode=$3
		ORDER BY set_row.frozen_at DESC,set_row.id DESC LIMIT 1
		ON CONFLICT DO NOTHING`, expectationID.String(), int16(season), int16(episode), seriesIdentity)
	if err != nil {
		return err
	}
	return nil
}

func nullableUUID(value uuid.UUID) any {
	if value == (uuid.UUID{}) {
		return nil
	}
	return value.String()
}

func containsEpisode(values []uint16, episode uint16) bool {
	for _, value := range values {
		if value == episode {
			return true
		}
	}
	return false
}
