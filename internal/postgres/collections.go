package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/catalog"
)

type MediaCollection struct {
	ID          uuid.UUID           `json:"id"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	CreatedAt   time.Time           `json:"created_at"`
	UpdatedAt   time.Time           `json:"updated_at"`
	Members     []catalog.MediaItem `json:"members"`
}

func (store *Store) PreviewCollection(ctx context.Context, mediaIDs []uuid.UUID) ([]catalog.MediaItem, error) {
	if len(mediaIDs) < 1 || len(mediaIDs) > 1000 {
		return nil, errors.New("collection must contain 1 to 1000 media items")
	}
	seen := map[uuid.UUID]bool{}
	result := make([]catalog.MediaItem, 0, len(mediaIDs))
	for _, id := range mediaIDs {
		if seen[id] {
			return nil, errors.New("collection contains duplicate media")
		}
		seen[id] = true
		item, err := store.GetMedia(ctx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

func (store *Store) CreateCollection(ctx context.Context, name, description string, mediaIDs []uuid.UUID, now time.Time) (MediaCollection, error) {
	name, description = strings.TrimSpace(name), strings.TrimSpace(description)
	if name == "" || len(name) > 200 || len(description) > 2000 {
		return MediaCollection{}, errors.New("invalid collection")
	}
	members, err := store.PreviewCollection(ctx, mediaIDs)
	if err != nil {
		return MediaCollection{}, err
	}
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return MediaCollection{}, err
	}
	defer transaction.Rollback(ctx)
	result := MediaCollection{ID: uuid.NewV7(), Name: name, Description: description, CreatedAt: now.UTC(), UpdatedAt: now.UTC(), Members: members}
	if _, err := transaction.Exec(ctx, `INSERT INTO media_collections(id,name,description,created_at,updated_at) VALUES($1,$2,$3,$4,$4)`, result.ID.String(), name, description, now.UTC()); err != nil {
		return MediaCollection{}, err
	}
	for position, mediaID := range mediaIDs {
		if _, err := transaction.Exec(ctx, `INSERT INTO media_collection_members(collection_id,media_id,position,added_at) VALUES($1,$2,$3,$4)`, result.ID.String(), mediaID.String(), position, now.UTC()); err != nil {
			return MediaCollection{}, err
		}
	}
	detail, _ := json.Marshal(map[string]any{"name": name, "members": len(mediaIDs)})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','collection.created','collection',$2,$3,$4)`, uuid.NewV7().String(), result.ID.String(), detail, now.UTC()); err != nil {
		return MediaCollection{}, err
	}
	return result, transaction.Commit(ctx)
}

func (store *Store) ListCollections(ctx context.Context, limit int) ([]MediaCollection, error) {
	if limit < 1 || limit > 200 {
		limit = 100
	}
	rows, err := store.Pool.Query(ctx, `SELECT id::text,name,description,created_at,updated_at FROM media_collections ORDER BY name LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []MediaCollection{}
	for rows.Next() {
		var item MediaCollection
		var idText string
		if err := rows.Scan(&idText, &item.Name, &item.Description, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		item.ID, _ = uuid.Parse(idText)
		item.Members, err = store.collectionMembers(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (store *Store) collectionMembers(ctx context.Context, collectionID uuid.UUID) ([]catalog.MediaItem, error) {
	rows, err := store.Pool.Query(ctx, `SELECT media.id::text,media.kind,media.title,media.year,media.revision,media.created_at,media.updated_at FROM media_collection_members member JOIN media_items media ON media.id=member.media_id WHERE member.collection_id=$1 AND media.deleted_at IS NULL ORDER BY member.position`, collectionID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []catalog.MediaItem{}
	for rows.Next() {
		var item catalog.MediaItem
		var idText string
		var year *int16
		var revision int64
		if err := rows.Scan(&idText, &item.Kind, &item.Title, &year, &revision, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		item.ID, _ = uuid.Parse(idText)
		item.Revision = uint64(revision)
		if year != nil {
			value := uint16(*year)
			item.Year = &value
		}
		item.Identities, err = store.identities(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (store *Store) DeleteCollection(ctx context.Context, id uuid.UUID, now time.Time) error {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var name string
	if err := transaction.QueryRow(ctx, `DELETE FROM media_collections WHERE id=$1 RETURNING name`, id.String()).Scan(&name); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	detail, _ := json.Marshal(map[string]string{"name": name})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','collection.deleted','collection',$2,$3,$4)`, uuid.NewV7().String(), id.String(), detail, now.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}
