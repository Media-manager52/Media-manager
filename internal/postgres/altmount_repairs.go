package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/languages"
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
	RepairNotNeeded      = "not_needed"      // the title's original language is the required audio
	RepairRecentlyTried  = "recently_tried"  // an upgrade found nothing recently; retried later
)

// Repair reasons.
const (
	RepairReasonCorrupted    = "corrupted"
	RepairReasonMissingAudio = "missing_required_audio"
)

// AudioUpgrade describes a published file that lacks the audio language its
// library requires. The replacement is acquired under ProfileID (the
// library's profile), not the profile the old file was acquired under.
type AudioUpgrade struct {
	Language  string
	ProfileID uuid.UUID
}

// AltMountRepair is one broken published file and its replacement workflow.
type AltMountRepair struct {
	ID                 uuid.UUID  `json:"id"`
	AltMountFilePath   string     `json:"altmount_file_path"`
	MediaID            *uuid.UUID `json:"media_id,omitempty"`
	Title              string     `json:"title"`
	Variant            string     `json:"variant"`
	RepairWorkflowID   *uuid.UUID `json:"repair_workflow_id,omitempty"`
	BrokenReleaseTitle string     `json:"broken_release_title"`
	Reason             string     `json:"reason"`
	State              string     `json:"state"`
	Detail             string     `json:"detail"`
	CreatedAt          time.Time  `json:"created_at"`
	ResolvedAt         *time.Time `json:"resolved_at,omitempty"`
	RetryAfter         *time.Time `json:"retry_after,omitempty"`
}

// PlanAltMountRepair opens a repair for a file AltMount reported corrupted
// (Usenet articles are gone, so playback fails). The file must back a current
// Media Manager link. The repair acquires a replacement for exactly that movie
// or episode: an episode inside a season pack gets its own episode workflow.
// The broken release title is excluded from the repair's search, and the
// broken link is displaced once Plex confirms the replacement. AltMount
// content is never modified. Planning is idempotent per broken link.
func (store *Store) PlanAltMountRepair(ctx context.Context, altMountID uuid.UUID, healthID int64, filePath string, now time.Time) (string, error) {
	return store.planManagedRepair(ctx, altMountID, healthID, filePath, nil, now)
}

// PlanAudioUpgrade opens an upgrade for a published Media Manager file that
// lacks the audio language its library requires, exactly like a repair of a
// broken file: the old file plays until Plex confirms the replacement. A
// title originally in the required language needs none, and a file whose
// last upgrade found nothing is not retried before its retry time.
func (store *Store) PlanAudioUpgrade(ctx context.Context, altMountID uuid.UUID, filePath string, upgrade AudioUpgrade, now time.Time) (string, error) {
	return store.planManagedRepair(ctx, altMountID, 0, filePath, &upgrade, now)
}

func (store *Store) planManagedRepair(ctx context.Context, altMountID uuid.UUID, healthID int64, filePath string, upgrade *AudioUpgrade, now time.Time) (string, error) {
	filePath = strings.Trim(strings.TrimSpace(filePath), "/")
	if filePath == "" {
		return "", errors.New("AltMount health item has no file path")
	}
	tx, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var linkVersionID, workflowID, mediaID, variant, rootID, profileID, assetID, releaseTitle, kind, originalLanguage string
	var clientID *string
	var terminal bool
	err = tx.QueryRow(ctx, `SELECT version.id::text,workflow.id::text,workflow.media_id::text,workflow.variant,workflow.root_id::text,workflow.quality_profile_id::text,
			workflow.download_client_id::text,asset.id::text,COALESCE(candidate.title,''),media.kind,workflow.terminal_at IS NOT NULL,
			COALESCE((SELECT data->>'original_language' FROM media_metadata WHERE media_id=media.id),'')
		FROM altmount_output_inventories inventory
		JOIN altmount_output_assets asset ON asset.inventory_id=inventory.id AND inventory.root_relative || '/' || asset.relative_path=$1
		JOIN library_link_versions version ON version.source_asset_id=asset.id AND version.state='current'
		JOIN workflows workflow ON workflow.id=version.workflow_id
		JOIN media_items media ON media.id=workflow.media_id AND media.deleted_at IS NULL
		LEFT JOIN altmount_jobs job ON job.id=version.altmount_job_id
		LEFT JOIN release_candidates candidate ON candidate.id=job.candidate_id
		WHERE $1 LIKE inventory.root_relative || '/%'
		ORDER BY version.id LIMIT 1`, filePath).Scan(&linkVersionID, &workflowID, &mediaID, &variant, &rootID, &profileID, &clientID, &assetID, &releaseTitle, &kind, &terminal, &originalLanguage)
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
	reason, openDetail := RepairReasonCorrupted, "AltMount reports missing Usenet articles; a replacement is being acquired"
	if upgrade != nil {
		reason, openDetail = RepairReasonMissingAudio, "the file has no "+upgrade.Language+" audio track; a dubbed replacement is being acquired"
		if language, _ := languages.Canonical(originalLanguage); language == upgrade.Language {
			return RepairNotNeeded, nil
		}
		var recent bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM altmount_repairs WHERE broken_link_version_id=$1 AND reason=$2 AND state='not_found' AND retry_after>$3)`, linkVersionID, reason, now.UTC()).Scan(&recent); err != nil {
			return "", err
		}
		if recent {
			return RepairRecentlyTried, nil
		}
	}
	repairID := uuid.NewV7()
	createdWorkflow := false
	insertRepair := func(state, scopeVariant, detail string, repairWorkflow *string) error {
		_, err := tx.Exec(ctx, `INSERT INTO altmount_repairs(id,altmount_instance_id,altmount_health_id,altmount_file_path,broken_link_version_id,media_id,variant,repair_workflow_id,broken_release_title,state,detail,created_at,reason,created_workflow)
			VALUES($1,$2,NULLIF($3,0),$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, repairID.String(), altMountID.String(), healthID, filePath, linkVersionID, mediaID, scopeVariant, repairWorkflow, releaseTitle, state, detail, now.UTC(), reason, createdWorkflow)
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
		if upgrade != nil {
			profile = upgrade.ProfileID
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
		createdWorkflow = true
	}
	if err := insertRepair("open", scopeVariant, openDetail, &activeWorkflow); err != nil {
		return "", err
	}
	auditDetail, _ := json.Marshal(map[string]any{"repair_id": repairID.String(), "file": filePath, "variant": scopeVariant, "workflow_id": activeWorkflow, "broken_release": releaseTitle, "outcome": outcome, "reason": reason})
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
	rows, err := store.Pool.Query(ctx, `SELECT repair.id::text,repair.altmount_file_path,repair.media_id::text,COALESCE(media.title,''),repair.variant,repair.repair_workflow_id::text,
			repair.broken_release_title,repair.reason,repair.state,repair.detail,repair.created_at,repair.resolved_at,repair.retry_after
		FROM altmount_repairs repair LEFT JOIN media_items media ON media.id=repair.media_id
		ORDER BY repair.created_at DESC,repair.id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []AltMountRepair{}
	for rows.Next() {
		var item AltMountRepair
		var idText string
		var mediaText, workflowText *string
		if err := rows.Scan(&idText, &item.AltMountFilePath, &mediaText, &item.Title, &item.Variant, &workflowText, &item.BrokenReleaseTitle, &item.Reason, &item.State, &item.Detail, &item.CreatedAt, &item.ResolvedAt, &item.RetryAfter); err != nil {
			return nil, err
		}
		item.ID, _ = uuid.Parse(idText)
		if mediaText != nil {
			parsed, _ := uuid.Parse(*mediaText)
			item.MediaID = &parsed
		}
		if workflowText != nil {
			parsed, _ := uuid.Parse(*workflowText)
			item.RepairWorkflowID = &parsed
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// UnmanagedRepair describes a corrupted AltMount file reached through a
// library symlink Media Manager did not publish (older content), identified
// through Plex. MediaID is set when the title is known; Season/Episode and
// Episode (the TMDB snapshot) are set for an episode.
type UnmanagedRepair struct {
	AltMountID   uuid.UUID
	HealthID     int64
	FilePath     string
	RootID       uuid.UUID
	Location     string
	PlexLocation string
	Relative     string
	Target       string
	MediaID      uuid.UUID
	// NewMedia is catalogued in the same transaction when the title is not
	// known yet (MediaID is then taken from it).
	NewMedia     *catalog.MediaItem
	Kind         catalog.MediaKind
	Season       uint16
	Episode      uint16
	Snapshot     ManualEpisode
	ProfileID    uuid.UUID
	ReleaseTitle string
	// AudioLanguage is set for an upgrade of a file that lacks the audio
	// language its library requires (HealthID is then 0).
	AudioLanguage string
}

func (repair UnmanagedRepair) reason() (string, string) {
	if repair.AudioLanguage != "" {
		return RepairReasonMissingAudio, "the file has no " + repair.AudioLanguage + " audio track; a dubbed replacement is being acquired"
	}
	return RepairReasonCorrupted, "AltMount reports missing Usenet articles; a replacement is being acquired"
}

// UnmanagedRepairTracked reports whether a broken unmanaged symlink already has
// a repair, so a sweep does not repeat Plex and metadata lookups for it. For an
// audio upgrade, a skipped title and an upgrade that recently found nothing
// count as tracked too.
func (store *Store) UnmanagedRepairTracked(ctx context.Context, location, relative string, audioUpgrade bool, now time.Time) (bool, error) {
	var exists bool
	err := store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM altmount_repairs WHERE unmanaged_location=$1 AND unmanaged_relative=$2
		AND (state IN ('open','needs_attention') OR ($3 AND reason='missing_required_audio' AND (state='skipped' OR (state='not_found' AND retry_after>$4)))))`, location, relative, audioUpgrade, now.UTC()).Scan(&exists)
	return exists, err
}

// RecordUnmanagedRepairAttention records a broken unmanaged file that cannot
// be repaired automatically, with the reason, so it is visible and not
// retried every sweep.
func (store *Store) RecordUnmanagedRepairAttention(ctx context.Context, repair UnmanagedRepair, detail string, now time.Time) error {
	return store.recordUnmanagedRepair(ctx, repair, "needs_attention", detail, now)
}

// RecordUnmanagedRepairSkipped records an unmanaged file that needs no audio
// upgrade (the title's original language is the required one), so later
// sweeps do not look it up again.
func (store *Store) RecordUnmanagedRepairSkipped(ctx context.Context, repair UnmanagedRepair, detail string, now time.Time) error {
	return store.recordUnmanagedRepair(ctx, repair, "skipped", detail, now)
}

func (store *Store) recordUnmanagedRepair(ctx context.Context, repair UnmanagedRepair, state, detail string, now time.Time) error {
	var media *string
	if repair.MediaID != (uuid.UUID{}) {
		value := repair.MediaID.String()
		media = &value
	}
	reason, _ := repair.reason()
	_, err := store.Pool.Exec(ctx, `INSERT INTO altmount_repairs(id,altmount_instance_id,altmount_health_id,altmount_file_path,unmanaged_root_id,unmanaged_location,unmanaged_plex_location,unmanaged_relative,unmanaged_target,media_id,variant,broken_release_title,state,detail,created_at,reason,resolved_at)
		VALUES($1,$2,NULLIF($3,0),$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,CASE WHEN $13='skipped' THEN $15::timestamptz END) ON CONFLICT DO NOTHING`, uuid.NewV7().String(), repair.AltMountID.String(), repair.HealthID, repair.FilePath, repair.RootID.String(), repair.Location, repair.PlexLocation, repair.Relative, repair.Target, media, unmanagedVariant(repair), repair.ReleaseTitle, state, detail, now.UTC(), reason)
	return err
}

func unmanagedVariant(repair UnmanagedRepair) string {
	if repair.Kind == catalog.KindSeries {
		return workflows.EpisodeVariant(repair.Season, repair.Episode)
	}
	return "default"
}

// MediaIDByIdentities returns the active catalogue item owning any of the
// verified identities, and whether that item is archived.
func (store *Store) MediaIDByIdentities(ctx context.Context, kind catalog.MediaKind, identities []catalog.ExternalIdentity) (uuid.UUID, bool, bool, error) {
	for _, identity := range identities {
		if identity.State != catalog.IdentityVerified {
			continue
		}
		var mediaText string
		var archived bool
		err := store.Pool.QueryRow(ctx, `SELECT identity.media_id::text,media.deleted_at IS NOT NULL FROM external_identities identity JOIN media_items media ON media.id=identity.media_id
			WHERE identity.media_kind=$1 AND identity.namespace=$2 AND identity.value=$3 AND identity.state='verified'`, kind, identity.Namespace, identity.Value).Scan(&mediaText, &archived)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return uuid.UUID{}, false, false, err
		}
		parsed, _ := uuid.Parse(mediaText)
		return parsed, true, archived, nil
	}
	return uuid.UUID{}, false, false, nil
}

// ResolveRepairProfile chooses the quality profile for repairing an unmanaged
// file in a library root: the profile an enabled Seerr route assigns to that
// root (for example WWE), else an enabled profile routed for the title's
// original language (Danish originals), else the default profile.
func (store *Store) ResolveRepairProfile(ctx context.Context, kind catalog.MediaKind, rootID uuid.UUID, originalLanguage string) (uuid.UUID, error) {
	var profileText string
	err := store.Pool.QueryRow(ctx, `SELECT route.quality_profile_id::text FROM seerr_library_routes route JOIN quality_profiles profile ON profile.id=route.quality_profile_id AND profile.enabled
		WHERE route.library_root_id=$1 AND route.enabled AND route.media_kind=$2 AND route.quality_profile_id IS NOT NULL ORDER BY route.root_folder LIMIT 1`, rootID.String(), kind).Scan(&profileText)
	if err == nil {
		return uuid.Parse(profileText)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.UUID{}, err
	}
	if language := strings.ToLower(strings.TrimSpace(originalLanguage)); language != "" {
		err = store.Pool.QueryRow(ctx, `SELECT id::text FROM quality_profiles WHERE media_kind=$1 AND enabled AND policy->'original_language_routing' ? $2 ORDER BY name LIMIT 1`, kind, language).Scan(&profileText)
		if err == nil {
			return uuid.Parse(profileText)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return uuid.UUID{}, err
		}
	}
	if err := store.Pool.QueryRow(ctx, `SELECT id::text FROM quality_profiles WHERE media_kind=$1 AND enabled AND is_default LIMIT 1`, kind).Scan(&profileText); err != nil {
		return uuid.UUID{}, err
	}
	return uuid.Parse(profileText)
}

// PlanUnmanagedRepair opens a repair for a broken unmanaged symlink. The
// catalogue item must already exist (MediaID). A movie reuses its open
// workflow; an episode gets an episode workflow with a single-episode
// expectation from the TMDB snapshot. When Plex confirms the replacement the
// broken symlink is removed only if it still points at the corrupted file.
func (store *Store) PlanUnmanagedRepair(ctx context.Context, repair UnmanagedRepair, now time.Time) (string, error) {
	tx, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM altmount_repairs WHERE unmanaged_location=$1 AND unmanaged_relative=$2 AND state IN ('open','needs_attention'))`, repair.Location, repair.Relative).Scan(&exists); err != nil {
		return "", err
	}
	if exists {
		return RepairAlreadyOpen, nil
	}
	if repair.NewMedia != nil {
		if err := insertMediaItem(ctx, tx, *repair.NewMedia); err != nil {
			return "", err
		}
		repair.MediaID = repair.NewMedia.ID
	}
	variant := unmanagedVariant(repair)
	var activeWorkflow string
	err = tx.QueryRow(ctx, `SELECT id::text FROM workflows WHERE media_id=$1 AND variant=$2 AND terminal_at IS NULL`, repair.MediaID.String(), variant).Scan(&activeWorkflow)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	outcome := RepairAttached
	createdWorkflow := false
	if activeWorkflow == "" {
		outcome = RepairOpened
		createdWorkflow = true
		workflow := workflows.NewWithClient(repair.MediaID, repair.RootID, repair.ProfileID, repair.AltMountID, now.UTC())
		if err := store.insertManualWorkflow(ctx, tx, workflow, variant, true); err != nil {
			return "", err
		}
		if repair.Kind == catalog.KindSeries {
			if err := insertSnapshotEpisodeExpectation(ctx, tx, workflow.ID, repair, now); err != nil {
				return "", err
			}
		}
		activeWorkflow = workflow.ID.String()
	}
	reason, detail := repair.reason()
	if _, err := tx.Exec(ctx, `INSERT INTO altmount_repairs(id,altmount_instance_id,altmount_health_id,altmount_file_path,unmanaged_root_id,unmanaged_location,unmanaged_plex_location,unmanaged_relative,unmanaged_target,media_id,variant,repair_workflow_id,broken_release_title,state,detail,created_at,reason,created_workflow)
		VALUES($1,$2,NULLIF($3,0),$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'open',$14,$15,$16,$17)`, uuid.NewV7().String(), repair.AltMountID.String(), repair.HealthID, repair.FilePath, repair.RootID.String(), repair.Location, repair.PlexLocation, repair.Relative, repair.Target,
		repair.MediaID.String(), variant, activeWorkflow, repair.ReleaseTitle, detail, now.UTC(), reason, createdWorkflow); err != nil {
		return "", err
	}
	auditDetail, _ := json.Marshal(map[string]any{"file": repair.FilePath, "variant": variant, "workflow_id": activeWorkflow, "broken_release": repair.ReleaseTitle, "outcome": outcome, "unmanaged_link": repair.Relative, "reason": reason})
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'worker','library.repair_opened','media',$2,$3,$4)`, uuid.NewV7().String(), repair.MediaID.String(), string(auditDetail), now.UTC()); err != nil {
		return "", err
	}
	return outcome, tx.Commit(ctx)
}

func insertSnapshotEpisodeExpectation(ctx context.Context, tx pgx.Tx, workflowID uuid.UUID, repair UnmanagedRepair, now time.Time) error {
	var seriesIdentity string
	if err := tx.QueryRow(ctx, `SELECT namespace || ':' || value FROM external_identities WHERE media_id=$1 AND state='verified' AND namespace IN ('tvdb','tmdb','imdb')
		ORDER BY CASE namespace WHEN 'tvdb' THEN 0 WHEN 'tmdb' THEN 1 ELSE 2 END LIMIT 1`, repair.MediaID.String()).Scan(&seriesIdentity); err != nil {
		return err
	}
	expectationID := uuid.NewV7()
	digest := sha256.Sum256([]byte(fmt.Sprintf("repair:%s:%d:%d", repair.MediaID.String(), repair.Season, repair.Episode)))
	if _, err := tx.Exec(ctx, `INSERT INTO pack_expectation_sets(id,workflow_id,series_identity,season,source_revision,expectation_digest,created_at,frozen_at) VALUES($1,$2,$3,$4,1,$5,$6,$6)`, expectationID.String(), workflowID.String(), seriesIdentity, int16(repair.Season), digest[:], now.UTC()); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO pack_expected_episodes(expectation_id,season,episode,provider_episode_id,title,air_date) VALUES($1,$2,$3,NULLIF($4,''),$5,NULLIF($6,'')::date)`,
		expectationID.String(), int16(repair.Season), int16(repair.Episode), strings.TrimSpace(repair.Snapshot.ProviderID), strings.TrimSpace(repair.Snapshot.Title), strings.TrimSpace(repair.Snapshot.AirDate))
	return err
}

// UnmanagedRemoval is a broken unmanaged symlink whose replacement Plex
// confirmed.
type UnmanagedRemoval struct {
	RepairID        uuid.UUID
	DestinationRoot string
	Relative        string
	Target          string
	PlexLibraryID   string
	PlexVisibleRoot string
	PlexInstanceID  uuid.UUID
}

// LoadUnmanagedRemoval loads a pending removal of a broken unmanaged symlink.
func (store *Store) LoadUnmanagedRemoval(ctx context.Context, repairID uuid.UUID) (UnmanagedRemoval, error) {
	removal := UnmanagedRemoval{RepairID: repairID}
	var plexText string
	err := store.Pool.QueryRow(ctx, `SELECT repair.unmanaged_location,repair.unmanaged_relative,repair.unmanaged_target,COALESCE(root.plex_library_id,''),repair.unmanaged_plex_location,root.plex_instance_id::text
		FROM altmount_repairs repair JOIN library_roots root ON root.id=repair.unmanaged_root_id
		WHERE repair.id=$1 AND repair.state='open'`, repairID.String()).Scan(&removal.DestinationRoot, &removal.Relative, &removal.Target, &removal.PlexLibraryID, &removal.PlexVisibleRoot, &plexText)
	if errors.Is(err, pgx.ErrNoRows) {
		return UnmanagedRemoval{}, ErrNotFound
	}
	if err != nil {
		return UnmanagedRemoval{}, err
	}
	removal.PlexInstanceID, _ = uuid.Parse(plexText)
	return removal, nil
}

// CompleteUnmanagedRemoval marks an unmanaged repair repaired after its broken
// symlink was removed.
func (store *Store) CompleteUnmanagedRemoval(ctx context.Context, repairID uuid.UUID, now time.Time) error {
	_, err := store.Pool.Exec(ctx, `UPDATE altmount_repairs SET state='repaired',resolved_at=$2,detail='replacement confirmed in Plex; the old symlink was removed' WHERE id=$1 AND state='open'`, repairID.String(), now.UTC())
	return err
}

// AudioUpgradeWindow is how long an audio upgrade searches before it is closed
// as not found; AudioUpgradeRetry is how long a closed upgrade waits before the
// file is tried again (new dubs appear over time).
const (
	AudioUpgradeWindow = 7 * 24 * time.Hour
	AudioUpgradeRetry  = 60 * 24 * time.Hour
)

// OpenAudioUpgrades counts the audio upgrades that are acquiring now.
func (store *Store) OpenAudioUpgrades(ctx context.Context) (int, error) {
	var count int
	err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM altmount_repairs WHERE reason='missing_required_audio' AND state='open'`).Scan(&count)
	return count, err
}

// RequiredAudioByRoot returns, per library root, the audio languages the
// quality profile its enabled Seerr route assigns requires (for example
// Danish for the kids libraries). Roots without such a profile are absent.
func (store *Store) RequiredAudioByRoot(ctx context.Context) (map[uuid.UUID]AudioUpgradeRoot, error) {
	rows, err := store.Pool.Query(ctx, `SELECT DISTINCT ON (route.library_root_id) route.library_root_id::text,profile.id::text,profile.policy
		FROM seerr_library_routes route JOIN quality_profiles profile ON profile.id=route.quality_profile_id AND profile.enabled
		JOIN library_roots root ON root.id=route.library_root_id AND root.enabled AND root.media_kind=route.media_kind
		WHERE route.enabled AND jsonb_array_length(COALESCE(profile.policy->'required_audio_languages','[]'::jsonb))>0
		ORDER BY route.library_root_id,route.root_folder`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[uuid.UUID]AudioUpgradeRoot{}
	for rows.Next() {
		var rootText, profileText string
		var policy json.RawMessage
		if err := rows.Scan(&rootText, &profileText, &policy); err != nil {
			return nil, err
		}
		var document struct {
			Languages []string `json:"required_audio_languages"`
		}
		if json.Unmarshal(policy, &document) != nil {
			continue
		}
		root, rootErr := uuid.Parse(rootText)
		profile, profileErr := uuid.Parse(profileText)
		if rootErr != nil || profileErr != nil {
			continue
		}
		for _, value := range document.Languages {
			if language, reliable := languages.Canonical(value); reliable {
				result[root] = AudioUpgradeRoot{Language: language, ProfileID: profile}
				break
			}
		}
	}
	return result, rows.Err()
}

// AudioUpgradeRoot is the audio language a library root requires and the
// profile its upgrades acquire under.
type AudioUpgradeRoot struct {
	Language  string
	ProfileID uuid.UUID
}

// ExpireAudioUpgrades closes the audio upgrades that found nothing within
// AudioUpgradeWindow, so other files get their turn. A workflow the upgrade
// created is cancelled when it is only waiting; one already retrieving or
// publishing keeps its upgrade open. The old file stays in place and the
// file is tried again after AudioUpgradeRetry.
func (store *Store) ExpireAudioUpgrades(ctx context.Context, now time.Time) (int, error) {
	rows, err := store.Pool.Query(ctx, `SELECT repair.id::text,repair.created_workflow,workflow.id::text,workflow.state,workflow.revision,workflow.terminal_at IS NOT NULL
		FROM altmount_repairs repair JOIN workflows workflow ON workflow.id=repair.repair_workflow_id
		WHERE repair.reason='missing_required_audio' AND repair.state='open' AND repair.created_at<$1`, now.UTC().Add(-AudioUpgradeWindow))
	if err != nil {
		return 0, err
	}
	type expiring struct {
		repair, workflow, state string
		created, terminal       bool
		revision                int64
	}
	var items []expiring
	for rows.Next() {
		var item expiring
		if err := rows.Scan(&item.repair, &item.created, &item.workflow, &item.state, &item.revision, &item.terminal); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	closed := 0
	for _, item := range items {
		if !item.terminal {
			switch workflows.State(item.state) {
			case workflows.StateMonitored, workflows.StateWaiting, workflows.StateHeld, workflows.StateBlocked:
			default:
				// Retrieving, verifying or publishing: let it finish.
				continue
			}
			if item.created {
				workflowID, _ := uuid.Parse(item.workflow)
				_, err := store.TransitionWorkflow(ctx, workflowID, uint64(item.revision), workflows.StateCancelled, "no dubbed release found within the audio upgrade window; retried later", "worker", now)
				if errors.Is(err, ErrConcurrentModification) {
					continue
				}
				if err != nil {
					return closed, err
				}
			}
		}
		command, err := store.Pool.Exec(ctx, `UPDATE altmount_repairs SET state='not_found',resolved_at=$2,retry_after=$3,detail='no dubbed release was found; the current file stays and is tried again later' WHERE id=$1 AND state='open'`, item.repair, now.UTC(), now.UTC().Add(AudioUpgradeRetry))
		if err != nil {
			return closed, err
		}
		closed += int(command.RowsAffected())
	}
	return closed, nil
}
