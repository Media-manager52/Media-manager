package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
)

var ErrReleaseBlockExists = errors.New("release block already exists")

type ReleaseBlock struct {
	ID                uuid.UUID  `json:"id"`
	Scope             string     `json:"scope"`
	IndexerInstanceID *uuid.UUID `json:"indexer_instance_id,omitempty"`
	IndexerName       string     `json:"indexer_name,omitempty"`
	MediaID           *uuid.UUID `json:"media_id,omitempty"`
	MediaTitle        string     `json:"media_title,omitempty"`
	Variant           string     `json:"variant,omitempty"`
	NativeKey         string     `json:"native_key,omitempty"`
	ReleaseTitle      string     `json:"release_title"`
	Reason            string     `json:"reason"`
	ExpiresAt         *time.Time `json:"expires_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	CreatedBy         string     `json:"created_by"`
}

func (block ReleaseBlock) Matches(indexerID uuid.UUID, nativeKey string, mediaID uuid.UUID, variant, title string) bool {
	if block.Scope == "native_release" {
		return block.IndexerInstanceID != nil && *block.IndexerInstanceID == indexerID && block.NativeKey == nativeKey
	}
	return block.Scope == "pack_release" && block.MediaID != nil && *block.MediaID == mediaID && block.Variant == variant && strings.EqualFold(block.ReleaseTitle, title)
}

func (store *Store) CreateReleaseBlockFromCandidate(ctx context.Context, candidateID uuid.UUID, scope, reason string, expiresAt *time.Time, now time.Time) (ReleaseBlock, error) {
	scope, reason = strings.TrimSpace(scope), strings.TrimSpace(reason)
	if (scope != "native_release" && scope != "pack_release") || len(reason) < 3 || len(reason) > 500 || expiresAt != nil && !expiresAt.After(now) {
		return ReleaseBlock{}, errors.New("invalid release block")
	}
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return ReleaseBlock{}, err
	}
	defer transaction.Rollback(ctx)
	var indexerText, mediaText, nativeKey, variant, title string
	err = transaction.QueryRow(ctx, `SELECT candidate.indexer_instance_id::text,workflow.media_id::text,candidate.native_key,workflow.variant,candidate.title
		FROM release_candidates candidate JOIN workflows workflow ON workflow.id=candidate.workflow_id WHERE candidate.id=$1`, candidateID.String()).Scan(&indexerText, &mediaText, &nativeKey, &variant, &title)
	if errors.Is(err, pgx.ErrNoRows) {
		return ReleaseBlock{}, ErrNotFound
	}
	if err != nil {
		return ReleaseBlock{}, err
	}
	indexerID, _ := uuid.Parse(indexerText)
	mediaID, _ := uuid.Parse(mediaText)
	if scope == "pack_release" && !strings.HasPrefix(variant, "season:") {
		return ReleaseBlock{}, errors.New("pack blocks require a season workflow")
	}
	result := ReleaseBlock{ID: uuid.NewV7(), Scope: scope, ReleaseTitle: title, Reason: reason, ExpiresAt: expiresAt, CreatedAt: now.UTC(), CreatedBy: "owner"}
	var indexerValue, mediaValue, variantValue, nativeValue any
	if scope == "native_release" {
		result.IndexerInstanceID, result.NativeKey = &indexerID, nativeKey
		indexerValue, nativeValue = indexerID.String(), nativeKey
	} else {
		result.MediaID, result.Variant = &mediaID, variant
		mediaValue, variantValue = mediaID.String(), variant
	}
	lockKey := scope + ":" + indexerText + ":" + mediaText + ":" + variant + ":" + nativeKey + ":" + strings.ToLower(title)
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, lockKey); err != nil {
		return ReleaseBlock{}, err
	}
	var exists bool
	if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM release_blocks WHERE (expires_at IS NULL OR expires_at>$1) AND (
		(scope='native_release' AND $2='native_release' AND indexer_instance_id=$3 AND native_key=$4)
		OR (scope='pack_release' AND $2='pack_release' AND media_id=$5 AND variant=$6 AND lower(release_title)=lower($7))))`, now.UTC(), scope, indexerValue, nativeValue, mediaValue, variantValue, title).Scan(&exists); err != nil {
		return ReleaseBlock{}, err
	}
	if exists {
		return ReleaseBlock{}, ErrReleaseBlockExists
	}
	_, err = transaction.Exec(ctx, `INSERT INTO release_blocks(id,scope,indexer_instance_id,media_id,variant,native_key,release_title,reason,expires_at,created_at,created_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'owner')`, result.ID.String(), scope, indexerValue, mediaValue, variantValue, nativeValue, title, reason, expiresAt, now.UTC())
	if err != nil {
		return ReleaseBlock{}, err
	}
	detail, _ := json.Marshal(map[string]any{"candidate_id": candidateID.String(), "scope": scope, "release_title": title, "reason": reason, "expires_at": expiresAt})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','release.blocked','release_block',$2,$3,$4)`, uuid.NewV7().String(), result.ID.String(), detail, now.UTC()); err != nil {
		return ReleaseBlock{}, err
	}
	return result, transaction.Commit(ctx)
}

func (store *Store) ListReleaseBlocks(ctx context.Context, limit int) ([]ReleaseBlock, error) {
	if limit < 1 || limit > 500 {
		limit = 200
	}
	rows, err := store.Pool.Query(ctx, `SELECT block.id::text,block.scope,block.indexer_instance_id::text,COALESCE(indexer.name,''),block.media_id::text,COALESCE(media.title,''),COALESCE(block.variant,''),COALESCE(block.native_key,''),block.release_title,block.reason,block.expires_at,block.created_at,block.created_by
		FROM release_blocks block
		LEFT JOIN integration_instances indexer ON indexer.id=block.indexer_instance_id
		LEFT JOIN media_items media ON media.id=block.media_id
		ORDER BY CASE WHEN block.expires_at IS NULL OR block.expires_at>now() THEN 0 ELSE 1 END,block.created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return readReleaseBlocks(rows)
}

func readReleaseBlocks(rows pgx.Rows) ([]ReleaseBlock, error) {
	defer rows.Close()
	result := []ReleaseBlock{}
	for rows.Next() {
		var item ReleaseBlock
		var idText string
		var indexerText, mediaText *string
		if err := rows.Scan(&idText, &item.Scope, &indexerText, &item.IndexerName, &mediaText, &item.MediaTitle, &item.Variant, &item.NativeKey, &item.ReleaseTitle, &item.Reason, &item.ExpiresAt, &item.CreatedAt, &item.CreatedBy); err != nil {
			return nil, err
		}
		item.ID, _ = uuid.Parse(idText)
		if indexerText != nil {
			parsed, parseErr := uuid.Parse(*indexerText)
			if parseErr != nil {
				return nil, parseErr
			}
			item.IndexerInstanceID = &parsed
		}
		if mediaText != nil {
			parsed, parseErr := uuid.Parse(*mediaText)
			if parseErr != nil {
				return nil, parseErr
			}
			item.MediaID = &parsed
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (store *Store) ListActiveReleaseBlocks(ctx context.Context, mediaID uuid.UUID, variant string, now time.Time) ([]ReleaseBlock, error) {
	rows, err := store.Pool.Query(ctx, `SELECT block.id::text,block.scope,block.indexer_instance_id::text,COALESCE(indexer.name,''),block.media_id::text,COALESCE(media.title,''),COALESCE(block.variant,''),COALESCE(block.native_key,''),block.release_title,block.reason,block.expires_at,block.created_at,block.created_by
		FROM release_blocks block
		LEFT JOIN integration_instances indexer ON indexer.id=block.indexer_instance_id
		LEFT JOIN media_items media ON media.id=block.media_id
		WHERE (block.expires_at IS NULL OR block.expires_at>$3) AND (block.scope='native_release' OR (block.media_id=$1 AND block.variant=$2))
		ORDER BY block.created_at DESC`, mediaID.String(), variant, now.UTC())
	if err != nil {
		return nil, err
	}
	return readReleaseBlocks(rows)
}

func (store *Store) DeleteReleaseBlock(ctx context.Context, id uuid.UUID, now time.Time) error {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var title, scope string
	if err := transaction.QueryRow(ctx, `DELETE FROM release_blocks WHERE id=$1 RETURNING release_title,scope`, id.String()).Scan(&title, &scope); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	detail, _ := json.Marshal(map[string]string{"scope": scope, "release_title": title})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','release.unblocked','release_block',$2,$3,$4)`, uuid.NewV7().String(), id.String(), detail, now.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}
