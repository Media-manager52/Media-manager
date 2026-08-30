package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/adoption"
	"github.com/media-manager/media-manager/internal/catalog"
)

type AdoptionBatch struct {
	ID                          uuid.UUID         `json:"id"`
	SourceRoot                  string            `json:"source_root"`
	MediaKind                   catalog.MediaKind `json:"media_kind"`
	DefaultRootID               uuid.UUID         `json:"default_root_id"`
	DefaultQualityProfileID     uuid.UUID         `json:"default_quality_profile_id"`
	MonitoringEnabled           bool              `json:"monitoring_enabled"`
	SearchImmediately           bool              `json:"search_immediately"`
	ReplacementSearchAuthorized bool              `json:"replacement_search_authorized"`
	State                       string            `json:"state"`
	CreatedAt                   time.Time         `json:"created_at"`
	ApprovedAt                  *time.Time        `json:"approved_at,omitempty"`
	CompletedAt                 *time.Time        `json:"completed_at,omitempty"`
	ExecutionDeadline           *time.Time        `json:"execution_deadline,omitempty"`
	PendingItems                int64             `json:"pending_items"`
	ApprovedItems               int64             `json:"approved_items"`
	RejectedItems               int64             `json:"rejected_items"`
	AttentionItems              int64             `json:"attention_items"`
	Items                       []AdoptionItem    `json:"items,omitempty"`
}

type AdoptionItem struct {
	ID                   uuid.UUID       `json:"id"`
	BatchID              uuid.UUID       `json:"batch_id"`
	OriginalRelative     string          `json:"original_relative"`
	ProposedRelative     string          `json:"proposed_relative,omitempty"`
	MediaID              *uuid.UUID      `json:"media_id,omitempty"`
	RootID               *uuid.UUID      `json:"root_id,omitempty"`
	QualityProfileID     *uuid.UUID      `json:"quality_profile_id,omitempty"`
	Variant              string          `json:"variant,omitempty"`
	MonitoringEnabled    *bool           `json:"monitoring_enabled,omitempty"`
	SearchImmediately    *bool           `json:"search_immediately,omitempty"`
	RecognizedCompanions json.RawMessage `json:"recognized_companions"`
	UnknownCompanions    json.RawMessage `json:"unknown_companions"`
	ReviewState          string          `json:"review_state"`
	ExecutionState       string          `json:"execution_state"`
}

type AdoptionConfiguration struct {
	Root    LibraryRoot
	Profile QualityProfile
}

func (store *Store) AdoptionConfiguration(ctx context.Context, rootID, profileID uuid.UUID) (AdoptionConfiguration, error) {
	var result AdoptionConfiguration
	var rootText, plexText, profileText string
	var policy []byte
	var policyVersion int64
	err := store.Pool.QueryRow(ctx, `SELECT root.id::text,root.media_kind,root.name,root.destination_root,root.plex_visible_root,root.source_visible_root,root.plex_instance_id::text,COALESCE(root.plex_library_id,''),root.is_default,root.enabled,
		profile.id::text,profile.media_kind,profile.name,profile.policy_version,profile.policy,profile.upgrades_enabled,profile.is_default,profile.enabled
		FROM library_roots root JOIN quality_profiles profile ON profile.id=$2
		WHERE root.id=$1 AND root.enabled AND profile.enabled AND root.media_kind=profile.media_kind`, rootID.String(), profileID.String()).Scan(
		&rootText, &result.Root.MediaKind, &result.Root.Name, &result.Root.DestinationRoot, &result.Root.PlexVisibleRoot, &result.Root.SourceVisibleRoot, &plexText, &result.Root.PlexLibraryID, &result.Root.Default, &result.Root.Enabled,
		&profileText, &result.Profile.MediaKind, &result.Profile.Name, &policyVersion, &policy, &result.Profile.UpgradesEnabled, &result.Profile.Default, &result.Profile.Enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdoptionConfiguration{}, ErrNotFound
	}
	if err != nil {
		return AdoptionConfiguration{}, err
	}
	result.Root.ID, _ = uuid.Parse(rootText)
	plexID, _ := uuid.Parse(plexText)
	result.Root.PlexInstanceID = &plexID
	result.Profile.ID, _ = uuid.Parse(profileText)
	result.Profile.PolicyVersion = uint64(policyVersion)
	result.Profile.Policy = append(json.RawMessage(nil), policy...)
	return result, nil
}

func (store *Store) CreateAdoptionBatch(ctx context.Context, sourceRoot string, configuration AdoptionConfiguration, monitoring, search bool, entries []adoption.Entry, now time.Time) (AdoptionBatch, error) {
	if sourceRoot == "" || len(entries) > 50000 || search && !monitoring {
		return AdoptionBatch{}, errors.New("invalid adoption batch")
	}
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return AdoptionBatch{}, err
	}
	defer transaction.Rollback(ctx)
	result := AdoptionBatch{ID: uuid.NewV7(), SourceRoot: filepath.Clean(sourceRoot), MediaKind: configuration.Root.MediaKind, DefaultRootID: configuration.Root.ID, DefaultQualityProfileID: configuration.Profile.ID, MonitoringEnabled: monitoring, SearchImmediately: search, State: "review", CreatedAt: now.UTC(), Items: []AdoptionItem{}}
	if _, err := transaction.Exec(ctx, `INSERT INTO adoption_batches(id,source_root,media_kind,default_root_id,default_quality_profile_id,monitoring_enabled,search_immediately,state,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,'review',$8)`, result.ID.String(), result.SourceRoot, result.MediaKind, result.DefaultRootID.String(), result.DefaultQualityProfileID.String(), monitoring, search, now.UTC()); err != nil {
		return AdoptionBatch{}, err
	}
	for _, entry := range entries {
		recognized, _ := json.Marshal(entry.RecognizedCompanions)
		unknown, _ := json.Marshal(entry.UnknownCompanions)
		item := AdoptionItem{ID: uuid.NewV7(), BatchID: result.ID, OriginalRelative: entry.Relative, RecognizedCompanions: recognized, UnknownCompanions: unknown, ReviewState: "needs_attention", ExecutionState: "pending"}
		if _, err := transaction.Exec(ctx, `INSERT INTO adoption_items(id,batch_id,original_relative,recognized_companions,unknown_companions,review_state,execution_state) VALUES($1,$2,$3,$4,$5,'needs_attention','pending')`, item.ID.String(), result.ID.String(), item.OriginalRelative, string(recognized), string(unknown)); err != nil {
			return AdoptionBatch{}, err
		}
		result.Items = append(result.Items, item)
	}
	result.AttentionItems = int64(len(entries))
	detail, _ := json.Marshal(map[string]any{"source_root": result.SourceRoot, "media_kind": result.MediaKind, "items": len(entries), "read_only": true})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','adoption.discovered','adoption_batch',$2,$3,$4)`, uuid.NewV7().String(), result.ID.String(), detail, now.UTC()); err != nil {
		return AdoptionBatch{}, err
	}
	return result, transaction.Commit(ctx)
}

const adoptionBatchSelect = `SELECT batch.id::text,batch.source_root,batch.media_kind,batch.default_root_id::text,batch.default_quality_profile_id::text,batch.monitoring_enabled,batch.search_immediately,batch.replacement_search_authorized_at IS NOT NULL,batch.state,batch.created_at,batch.approved_at,batch.completed_at,batch.execution_deadline,
	count(item.id) FILTER (WHERE item.review_state='pending'),count(item.id) FILTER (WHERE item.review_state='approved'),count(item.id) FILTER (WHERE item.review_state='rejected'),count(item.id) FILTER (WHERE item.review_state='needs_attention')
	FROM adoption_batches batch LEFT JOIN adoption_items item ON item.batch_id=batch.id`

func (store *Store) ListAdoptionBatches(ctx context.Context, limit int) ([]AdoptionBatch, error) {
	if limit < 1 || limit > 200 {
		limit = 100
	}
	rows, err := store.Pool.Query(ctx, adoptionBatchSelect+` GROUP BY batch.id ORDER BY batch.created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []AdoptionBatch{}
	for rows.Next() {
		item, err := scanAdoptionBatch(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func scanAdoptionBatch(row pgx.Row) (AdoptionBatch, error) {
	var item AdoptionBatch
	var idText, rootText, profileText string
	err := row.Scan(&idText, &item.SourceRoot, &item.MediaKind, &rootText, &profileText, &item.MonitoringEnabled, &item.SearchImmediately, &item.ReplacementSearchAuthorized, &item.State, &item.CreatedAt, &item.ApprovedAt, &item.CompletedAt, &item.ExecutionDeadline, &item.PendingItems, &item.ApprovedItems, &item.RejectedItems, &item.AttentionItems)
	item.ID, _ = uuid.Parse(idText)
	item.DefaultRootID, _ = uuid.Parse(rootText)
	item.DefaultQualityProfileID, _ = uuid.Parse(profileText)
	return item, err
}

func (store *Store) AdoptionBatch(ctx context.Context, id uuid.UUID) (AdoptionBatch, error) {
	row := store.Pool.QueryRow(ctx, adoptionBatchSelect+` WHERE batch.id=$1 GROUP BY batch.id`, id.String())
	result, err := scanAdoptionBatch(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdoptionBatch{}, ErrNotFound
	}
	if err != nil {
		return AdoptionBatch{}, err
	}
	rows, err := store.Pool.Query(ctx, `SELECT id::text,batch_id::text,original_relative,COALESCE(proposed_relative,''),media_id::text,root_id::text,quality_profile_id::text,COALESCE(variant,''),monitoring_enabled,search_immediately,recognized_companions,unknown_companions,review_state,execution_state FROM adoption_items WHERE batch_id=$1 ORDER BY original_relative`, id.String())
	if err != nil {
		return AdoptionBatch{}, err
	}
	defer rows.Close()
	result.Items = []AdoptionItem{}
	for rows.Next() {
		var item AdoptionItem
		var idText, batchText string
		var mediaText, rootText, profileText *string
		var recognized, unknown []byte
		if err := rows.Scan(&idText, &batchText, &item.OriginalRelative, &item.ProposedRelative, &mediaText, &rootText, &profileText, &item.Variant, &item.MonitoringEnabled, &item.SearchImmediately, &recognized, &unknown, &item.ReviewState, &item.ExecutionState); err != nil {
			return AdoptionBatch{}, err
		}
		item.ID, _ = uuid.Parse(idText)
		item.BatchID, _ = uuid.Parse(batchText)
		if mediaText != nil {
			parsed, _ := uuid.Parse(*mediaText)
			item.MediaID = &parsed
		}
		if rootText != nil {
			parsed, _ := uuid.Parse(*rootText)
			item.RootID = &parsed
		}
		if profileText != nil {
			parsed, _ := uuid.Parse(*profileText)
			item.QualityProfileID = &parsed
		}
		item.RecognizedCompanions = append(json.RawMessage(nil), recognized...)
		item.UnknownCompanions = append(json.RawMessage(nil), unknown...)
		result.Items = append(result.Items, item)
	}
	return result, rows.Err()
}

type AdoptionReview struct {
	State             string
	MediaID           *uuid.UUID
	RootID            *uuid.UUID
	QualityProfileID  *uuid.UUID
	ProposedRelative  string
	Variant           string
	MonitoringEnabled bool
	SearchImmediately bool
}

func (store *Store) ReviewAdoptionItem(ctx context.Context, batchID, itemID uuid.UUID, review AdoptionReview, now time.Time) error {
	if review.State != "approved" && review.State != "rejected" {
		return errors.New("invalid adoption review state")
	}
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var kind catalog.MediaKind
	var batchState string
	if err := transaction.QueryRow(ctx, `SELECT media_kind,state FROM adoption_batches WHERE id=$1 FOR UPDATE`, batchID.String()).Scan(&kind, &batchState); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if batchState != "review" {
		return errors.New("adoption batch is not in review")
	}
	var exists bool
	if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM adoption_items WHERE id=$1 AND batch_id=$2)`, itemID.String(), batchID.String()).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	if review.State == "rejected" {
		_, err = transaction.Exec(ctx, `UPDATE adoption_items SET review_state='rejected',media_id=NULL,root_id=NULL,quality_profile_id=NULL,proposed_relative=NULL,variant=NULL,monitoring_enabled=NULL,search_immediately=NULL WHERE id=$1`, itemID.String())
	} else {
		review.Variant = strings.TrimSpace(review.Variant)
		if kind == catalog.KindMovie {
			review.Variant = "default"
		}
		if review.MediaID == nil || review.RootID == nil || review.QualityProfileID == nil || !safeRelative(review.ProposedRelative) || !validAdoptionVariant(kind, review.Variant) || review.SearchImmediately && !review.MonitoringEnabled {
			return errors.New("approved adoption item is incomplete")
		}
		if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM media_items media,library_roots root,quality_profiles profile WHERE media.id=$1 AND media.kind=$4 AND media.deleted_at IS NULL AND root.id=$2 AND root.media_kind=$4 AND root.enabled AND profile.id=$3 AND profile.media_kind=$4 AND profile.enabled)`, review.MediaID.String(), review.RootID.String(), review.QualityProfileID.String(), kind).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return errors.New("adoption review references incompatible configuration")
		}
		if err := transaction.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM adoption_items WHERE batch_id=$1 AND id<>$2 AND review_state='approved' AND root_id=$3 AND lower(proposed_relative)=lower($4)
			UNION ALL
			SELECT 1 FROM library_links WHERE root_id=$3 AND lower(destination_relative)=lower($4)
			UNION ALL
			SELECT 1 FROM adopted_files WHERE root_id=$3 AND lower(destination_relative)=lower($4) AND state='current'
		)`, batchID.String(), itemID.String(), review.RootID.String(), filepath.ToSlash(filepath.Clean(review.ProposedRelative))).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return errors.New("proposed adoption destination is already planned or managed")
		}
		_, err = transaction.Exec(ctx, `UPDATE adoption_items SET review_state='approved',media_id=$2,root_id=$3,quality_profile_id=$4,proposed_relative=$5,variant=$6,monitoring_enabled=$7,search_immediately=$8 WHERE id=$1`, itemID.String(), review.MediaID.String(), review.RootID.String(), review.QualityProfileID.String(), filepath.ToSlash(filepath.Clean(review.ProposedRelative)), review.Variant, review.MonitoringEnabled, review.SearchImmediately)
	}
	if err != nil {
		return err
	}
	detail, _ := json.Marshal(map[string]any{"review_state": review.State, "proposed_relative": review.ProposedRelative, "variant": review.Variant, "monitoring_enabled": review.MonitoringEnabled, "search_immediately": review.SearchImmediately})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','adoption.item_reviewed','adoption_item',$2,$3,$4)`, uuid.NewV7().String(), itemID.String(), detail, now.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

func validAdoptionVariant(kind catalog.MediaKind, variant string) bool {
	if kind == catalog.KindMovie {
		return variant == "default"
	}
	if kind != catalog.KindSeries || len(variant) != len("season:00") || !strings.HasPrefix(variant, "season:") {
		return false
	}
	return variant[7] >= '0' && variant[7] <= '9' && variant[8] >= '0' && variant[8] <= '9'
}

func safeRelative(value string) bool {
	clean := filepath.Clean(strings.TrimSpace(value))
	return clean != "." && !filepath.IsAbs(clean) && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator)) && !strings.Contains(clean, `\`)
}

func (store *Store) ApproveAdoptionBatch(ctx context.Context, id uuid.UUID, now time.Time) error {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var state string
	if err := transaction.QueryRow(ctx, `SELECT state FROM adoption_batches WHERE id=$1 FOR UPDATE`, id.String()).Scan(&state); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if state != "review" {
		return errors.New("adoption batch is not in review")
	}
	var approved, unresolved int64
	if err := transaction.QueryRow(ctx, `SELECT count(*) FILTER (WHERE review_state='approved'),count(*) FILTER (WHERE review_state IN ('pending','needs_attention')) FROM adoption_items WHERE batch_id=$1`, id.String()).Scan(&approved, &unresolved); err != nil {
		return err
	}
	if approved == 0 || unresolved != 0 {
		return errors.New("every adoption item must be approved or rejected before plan approval")
	}
	if _, err := transaction.Exec(ctx, `UPDATE adoption_batches SET state='approved',approved_at=$2 WHERE id=$1`, id.String(), now.UTC()); err != nil {
		return err
	}
	detail, _ := json.Marshal(map[string]any{"approved_items": approved, "execution_queued": false})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','adoption.plan_approved','adoption_batch',$2,$3,$4)`, uuid.NewV7().String(), id.String(), detail, now.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}
