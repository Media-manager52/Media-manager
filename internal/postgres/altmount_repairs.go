package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/workflows"
)

// Repair outcomes returned by PlanAltMountRepair.
const (
	RepairOpened         = "opened"          // a new repair workflow was created
	RepairAttached       = "attached"        // an open workflow for the scope already acquires a replacement
	RepairAlreadyOpen    = "already_open"    // this broken file already has a repair
	RepairUnmanaged      = "unmanaged"       // no current Media Manager link uses the file
	RepairBusy           = "busy"            // the publishing workflow is still in flight; retry next sweep
	RepairNeedsAttention = "needs_attention" // the scope cannot be repaired automatically
)

// AltMountRepair is one broken published file and its replacement workflow.
type AltMountRepair struct {
	ID                 uuid.UUID  `json:"id"`
	AltMountFilePath   string     `json:"altmount_file_path"`
	MediaID            uuid.UUID  `json:"media_id"`
	Title              string     `json:"title"`
	Variant            string     `json:"variant"`
	RepairWorkflowID   *uuid.UUID `json:"repair_workflow_id,omitempty"`
	BrokenReleaseTitle string     `json:"broken_release_title"`
	State              string     `json:"state"`
	Detail             string     `json:"detail"`
	CreatedAt          time.Time  `json:"created_at"`
	ResolvedAt         *time.Time `json:"resolved_at,omitempty"`
}

// PlanAltMountRepair opens a repair for a file AltMount reported corrupted
// (Usenet articles are gone, so playback fails). The file must back a current
// Media Manager link. The repair acquires a replacement for exactly that movie
// or episode: an episode inside a season pack gets its own episode workflow.
// The broken release title is excluded from the repair's search, and the
// broken link is displaced once Plex confirms the replacement. AltMount
// content is never modified. Planning is idempotent per broken link.
func (store *Store) PlanAltMountRepair(ctx context.Context, altMountID uuid.UUID, healthID int64, filePath string, now time.Time) (string, error) {
	filePath = strings.Trim(strings.TrimSpace(filePath), "/")
	if filePath == "" {
		return "", errors.New("AltMount health item has no file path")
	}
	tx, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var linkVersionID, workflowID, mediaID, variant, rootID, profileID, assetID, releaseTitle, kind string
	var clientID *string
	var terminal bool
	err = tx.QueryRow(ctx, `SELECT version.id::text,workflow.id::text,workflow.media_id::text,workflow.variant,workflow.root_id::text,workflow.quality_profile_id::text,
			workflow.download_client_id::text,asset.id::text,COALESCE(candidate.title,''),media.kind,workflow.terminal_at IS NOT NULL
		FROM altmount_output_inventories inventory
		JOIN altmount_output_assets asset ON asset.inventory_id=inventory.id AND inventory.root_relative || '/' || asset.relative_path=$1
		JOIN library_link_versions version ON version.source_asset_id=asset.id AND version.state='current'
		JOIN workflows workflow ON workflow.id=version.workflow_id
		JOIN media_items media ON media.id=workflow.media_id AND media.deleted_at IS NULL
		LEFT JOIN altmount_jobs job ON job.id=version.altmount_job_id
		LEFT JOIN release_candidates candidate ON candidate.id=job.candidate_id
		WHERE $1 LIKE inventory.root_relative || '/%'
		ORDER BY version.id LIMIT 1`, filePath).Scan(&linkVersionID, &workflowID, &mediaID, &variant, &rootID, &profileID, &clientID, &assetID, &releaseTitle, &kind, &terminal)
	if errors.Is(err, pgx.ErrNoRows) {
		return RepairUnmanaged, nil
	}
	if err != nil {
		return "", err
	}
	if !terminal {
		// The workflow that published the file is still acquiring or publishing.
		return RepairBusy, nil
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM altmount_repairs WHERE broken_link_version_id=$1 AND state IN ('open','needs_attention'))`, linkVersionID).Scan(&exists); err != nil {
		return "", err
	}
	if exists {
		return RepairAlreadyOpen, nil
	}
	repairID := uuid.NewV7()
	insertRepair := func(state, scopeVariant, detail string, repairWorkflow *string) error {
		_, err := tx.Exec(ctx, `INSERT INTO altmount_repairs(id,altmount_instance_id,altmount_health_id,altmount_file_path,broken_link_version_id,media_id,variant,repair_workflow_id,broken_release_title,state,detail,created_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, repairID.String(), altMountID.String(), healthID, filePath, linkVersionID, mediaID, scopeVariant, repairWorkflow, releaseTitle, state, detail, now.UTC())
		return err
	}
	scopeVariant, detail := repairScope(ctx, tx, kind, variant, assetID)
	if scopeVariant == "" {
		if err := insertRepair("needs_attention", variant, detail, nil); err != nil {
			return "", err
		}
		return RepairNeedsAttention, tx.Commit(ctx)
	}
	var activeWorkflow string
	err = tx.QueryRow(ctx, `SELECT id::text FROM workflows WHERE media_id=$1 AND variant=$2 AND terminal_at IS NULL`, mediaID, scopeVariant).Scan(&activeWorkflow)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	outcome := RepairAttached
	if activeWorkflow == "" {
		outcome = RepairOpened
		scope, _ := workflows.ParseVariant(scopeVariant)
		var seriesIdentity string
		var sourceRevision int64
		if scope.Kind == workflows.VariantEpisode {
			err := tx.QueryRow(ctx, `SELECT expectation.series_identity,expectation.source_revision FROM pack_expectation_sets expectation
				WHERE expectation.workflow_id=$1 AND EXISTS(SELECT 1 FROM pack_expected_episodes episode WHERE episode.expectation_id=expectation.id AND episode.season=$2 AND episode.episode=$3)
				ORDER BY expectation.frozen_at DESC LIMIT 1`, workflowID, int16(scope.Season), int16(scope.Episode)).Scan(&seriesIdentity, &sourceRevision)
			if errors.Is(err, pgx.ErrNoRows) {
				if err := insertRepair("needs_attention", scopeVariant, fmt.Sprintf("season %d episode %d has no frozen episode expectation to repair from", scope.Season, scope.Episode), nil); err != nil {
					return "", err
				}
				return RepairNeedsAttention, tx.Commit(ctx)
			}
			if err != nil {
				return "", err
			}
		}
		media, parseMedia := uuid.Parse(mediaID)
		root, parseRoot := uuid.Parse(rootID)
		profile, parseProfile := uuid.Parse(profileID)
		if parseMedia != nil || parseRoot != nil || parseProfile != nil {
			return "", errors.New("repair source workflow has invalid identifiers")
		}
		// Acquire through the source workflow's download client, or the
		// AltMount client that reported the broken file.
		client := altMountID
		if clientID != nil {
			if parsed, parseErr := uuid.Parse(*clientID); parseErr == nil {
				client = parsed
			}
		}
		workflow := workflows.NewWithClient(media, root, profile, client, now.UTC())
		if err := store.insertManualWorkflow(ctx, tx, workflow, scopeVariant, true); err != nil {
			return "", err
		}
		if scope.Kind == workflows.VariantEpisode {
			if err := insertEpisodeExpectation(ctx, tx, workflow.ID, media, seriesIdentity, sourceRevision, scope.Season, scope.Episode, now); err != nil {
				return "", err
			}
		}
		activeWorkflow = workflow.ID.String()
	}
	detail = "AltMount reports missing Usenet articles; a replacement is being acquired"
	if err := insertRepair("open", scopeVariant, detail, &activeWorkflow); err != nil {
		return "", err
	}
	auditDetail, _ := json.Marshal(map[string]any{"repair_id": repairID.String(), "file": filePath, "variant": scopeVariant, "workflow_id": activeWorkflow, "broken_release": releaseTitle, "outcome": outcome})
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'worker','library.repair_opened','media',$2,$3,$4)`, uuid.NewV7().String(), mediaID, string(auditDetail), now.UTC()); err != nil {
		return "", err
	}
	return outcome, tx.Commit(ctx)
}

// repairScope returns the variant a replacement is acquired for, or "" and a
// reason when the broken file cannot be mapped to one movie or one episode.
func repairScope(ctx context.Context, tx pgx.Tx, kind, variant, assetID string) (string, string) {
	scope, ok := workflows.ParseVariant(variant)
	if !ok {
		return "", "the published workflow scope is not recognized"
	}
	switch {
	case catalog.MediaKind(kind) == catalog.KindMovie && scope.Kind == workflows.VariantDefault:
		return "default", ""
	case scope.Kind == workflows.VariantEpisode:
		return variant, ""
	case scope.Kind == workflows.VariantSeason:
		rows, err := tx.Query(ctx, `SELECT DISTINCT episode.season,episode.episode FROM pack_group_assets asset
			JOIN pack_group_episodes episode ON episode.group_id=asset.group_id WHERE asset.asset_id=$1 ORDER BY 1,2`, assetID)
		if err != nil {
			return "", "the season-pack file could not be mapped to an episode"
		}
		defer rows.Close()
		var episodes [][2]int16
		for rows.Next() {
			var season, episode int16
			if rows.Scan(&season, &episode) == nil {
				episodes = append(episodes, [2]int16{season, episode})
			}
		}
		if len(episodes) != 1 || episodes[0][0] < 0 || episodes[0][1] <= 0 {
			return "", fmt.Sprintf("the season-pack file covers %d episodes; repair it by searching the season", len(episodes))
		}
		return workflows.EpisodeVariant(uint16(episodes[0][0]), uint16(episodes[0][1])), ""
	}
	return "", "the published workflow scope is not repairable"
}

// QueueAltMountRepairSweeps schedules one repair sweep per enabled AltMount
// client for the current half-hour. Repeated calls in the same half-hour are
// no-ops.
func (store *Store) QueueAltMountRepairSweeps(ctx context.Context, now time.Time) (int64, error) {
	bucket := now.UTC().Unix() / 1800
	command, err := store.Pool.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at)
		SELECT gen_random_uuid(),'integration',integration.id,$1,'altmount.repair_sweep_requested',json_build_object('integration_id',integration.id::text),$2,$2
		FROM integration_instances integration WHERE integration.kind='altmount' AND integration.enabled
		ON CONFLICT DO NOTHING`, bucket, now.UTC())
	if err != nil {
		return 0, err
	}
	return command.RowsAffected(), nil
}

// RepairBrokenReleaseTitles lists the release titles whose files are broken
// for the open repairs a workflow acquires a replacement for. Search rejects
// these exact titles.
func (store *Store) RepairBrokenReleaseTitles(ctx context.Context, workflowID uuid.UUID) ([]string, error) {
	rows, err := store.Pool.Query(ctx, `SELECT DISTINCT broken_release_title FROM altmount_repairs WHERE repair_workflow_id=$1 AND state='open' AND broken_release_title<>''`, workflowID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	titles := []string{}
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			return nil, err
		}
		titles = append(titles, title)
	}
	return titles, rows.Err()
}

// ListAltMountRepairs returns the most recent repairs, newest first.
func (store *Store) ListAltMountRepairs(ctx context.Context, limit int) ([]AltMountRepair, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := store.Pool.Query(ctx, `SELECT repair.id::text,repair.altmount_file_path,repair.media_id::text,media.title,repair.variant,repair.repair_workflow_id::text,
			repair.broken_release_title,repair.state,repair.detail,repair.created_at,repair.resolved_at
		FROM altmount_repairs repair JOIN media_items media ON media.id=repair.media_id
		ORDER BY repair.created_at DESC,repair.id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []AltMountRepair{}
	for rows.Next() {
		var item AltMountRepair
		var idText, mediaText string
		var workflowText *string
		if err := rows.Scan(&idText, &item.AltMountFilePath, &mediaText, &item.Title, &item.Variant, &workflowText, &item.BrokenReleaseTitle, &item.State, &item.Detail, &item.CreatedAt, &item.ResolvedAt); err != nil {
			return nil, err
		}
		item.ID, _ = uuid.Parse(idText)
		item.MediaID, _ = uuid.Parse(mediaText)
		if workflowText != nil {
			parsed, _ := uuid.Parse(*workflowText)
			item.RepairWorkflowID = &parsed
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
