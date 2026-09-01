package postgres

import (
	"context"
	"errors"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/workflows"
)

type WorkflowSummary struct {
	ID                                     uuid.UUID         `json:"id"`
	MediaID                                uuid.UUID         `json:"media_id"`
	MediaKind                              catalog.MediaKind `json:"media_kind"`
	Title                                  string            `json:"title"`
	Year                                   *uint16           `json:"year,omitempty"`
	Variant                                string            `json:"variant"`
	State                                  workflows.State   `json:"state"`
	Revision                               uint64            `json:"revision"`
	AttentionCode                          string            `json:"attention_code,omitempty"`
	AttentionDetail                        string            `json:"attention_detail,omitempty"`
	AdoptedReplacementConfirmationRequired bool              `json:"adopted_replacement_confirmation_required"`
	TerminalAt                             *time.Time        `json:"terminal_at,omitempty"`
	CreatedAt                              time.Time         `json:"created_at"`
	UpdatedAt                              time.Time         `json:"updated_at"`
}

func (store *Store) GetMedia(ctx context.Context, id uuid.UUID) (catalog.MediaItem, error) {
	var item catalog.MediaItem
	var idText string
	var year *int16
	var revision int64
	err := store.Pool.QueryRow(ctx, `SELECT id::text,kind,title,year,revision,created_at,updated_at FROM media_items WHERE id=$1 AND deleted_at IS NULL`, id.String()).Scan(&idText, &item.Kind, &item.Title, &year, &revision, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return catalog.MediaItem{}, ErrNotFound
	}
	if err != nil {
		return catalog.MediaItem{}, err
	}
	item.ID, _ = uuid.Parse(idText)
	item.Revision = uint64(revision)
	if year != nil {
		converted := uint16(*year)
		item.Year = &converted
	}
	item.Identities, err = store.identities(ctx, item.ID)
	if err != nil {
		return catalog.MediaItem{}, err
	}
	item.Metadata, err = store.metadata(ctx, item.ID)
	return item, err
}

func (store *Store) ListWorkflowSummaries(ctx context.Context, limit int, activeOnly bool) ([]WorkflowSummary, error) {
	return store.workflowSummaries(ctx, limit, activeOnly, nil)
}

func (store *Store) workflowSummaries(ctx context.Context, limit int, activeOnly bool, mediaID *uuid.UUID) ([]WorkflowSummary, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	var mediaIDValue any
	if mediaID != nil {
		mediaIDValue = mediaID.String()
	}
	rows, err := store.Pool.Query(ctx, `SELECT workflow.id::text,workflow.media_id::text,media.kind,media.title,media.year,workflow.variant,workflow.state,workflow.revision,COALESCE(workflow.attention_code,''),COALESCE(workflow.attention_detail,''),
		EXISTS(SELECT 1 FROM adopted_files adopted WHERE adopted.media_id=workflow.media_id AND adopted.variant=workflow.variant AND adopted.state='current' AND NOT EXISTS(SELECT 1 FROM adopted_replacement_authorizations replacement_auth WHERE replacement_auth.workflow_id=workflow.id AND replacement_auth.adopted_file_id=adopted.id AND replacement_auth.destination_relative=adopted.destination_relative AND replacement_auth.source_size=adopted.source_size AND replacement_auth.source_modtime_ns=adopted.source_modtime_ns)),
		workflow.terminal_at,workflow.created_at,workflow.updated_at
		FROM workflows workflow JOIN media_items media ON media.id=workflow.media_id
		WHERE media.deleted_at IS NULL AND (NOT $2 OR workflow.terminal_at IS NULL)
			AND ($3::uuid IS NULL OR workflow.media_id=$3::uuid)
		ORDER BY workflow.updated_at DESC,workflow.id LIMIT $1`, limit, activeOnly, mediaIDValue)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []WorkflowSummary{}
	for rows.Next() {
		var item WorkflowSummary
		var idText, mediaText string
		var year *int16
		var revision int64
		if err := rows.Scan(&idText, &mediaText, &item.MediaKind, &item.Title, &year, &item.Variant, &item.State, &revision, &item.AttentionCode, &item.AttentionDetail, &item.AdoptedReplacementConfirmationRequired, &item.TerminalAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		item.ID, _ = uuid.Parse(idText)
		item.MediaID, _ = uuid.Parse(mediaText)
		item.Revision = uint64(revision)
		if year != nil {
			converted := uint16(*year)
			item.Year = &converted
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
