package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/adapters/oldboys"
	"github.com/media-manager/media-manager/internal/catalog"
)

func OldBoysMediaAllowed(media catalog.MediaItem) bool {
	return (media.Kind == catalog.KindMovie || media.Kind == catalog.KindSeries) && media.Metadata != nil && media.Metadata.OriginalLanguage == "da"
}

// PrepareOldBoysCommission converts only the disabled legacy OldBoys row (if
// present), preserving its provider UUID. Every attempt gets a fresh identity;
// no external request takes place inside this transaction.
func (store *Store) PrepareOldBoysCommission(ctx context.Context, credentialsAt time.Time) (Integration, error) {
	tx, err := store.Pool.Begin(ctx)
	if err != nil {
		return Integration{}, err
	}
	defer tx.Rollback(ctx)
	var saved time.Time
	if err := tx.QueryRow(ctx, `SELECT updated_at FROM oldboys_credentials WHERE singleton=true FOR UPDATE`).Scan(&saved); err != nil {
		return Integration{}, err
	}
	if !saved.Equal(credentialsAt) {
		return Integration{}, ErrConcurrentModification
	}
	var text string
	err = tx.QueryRow(ctx, `SELECT id::text FROM integration_instances WHERE kind='oldboys' OR (kind='newznab' AND NOT enabled AND secret_ref='NEWZNAB_OLDBOYS_DK_API_KEY' AND config->>'base_url'='http://danish-intelligence:9699/ob/api') ORDER BY (kind='oldboys') DESC,id LIMIT 1 FOR UPDATE`).Scan(&text)
	if errors.Is(err, pgx.ErrNoRows) {
		text = uuid.NewV7().String()
	} else if err != nil {
		return Integration{}, err
	}
	config, _ := json.Marshal(map[string]any{"base_url": oldboys.Origin, "mode": "oldboys_filter", "mode_verified": false, "scope": "danish_original_movies_series", "movie_category_ids": []int{1}, "series_category_ids": []int{2}, "minimum_interval_ms": 5000, "query_limit": 1000, "grab_limit": 100, "budget_window_seconds": 86400, "budget_source": "conservative_fallback", "credentials_updated_at": saved.UTC().Format(time.RFC3339Nano), "commission_attempt": uuid.NewV7().String(), "download_key": "PID", "maximum_pages": oldboys.MaxPage})
	_, err = tx.Exec(ctx, `INSERT INTO integration_instances(id,kind,name,enabled,is_default,config,secret_ref,created_at,updated_at) VALUES($1,'oldboys','OldBoys {DK}',false,false,$2,'sealed:oldboys_credentials',clock_timestamp(),clock_timestamp()) ON CONFLICT(id) DO UPDATE SET kind='oldboys',enabled=false,is_default=false,config=EXCLUDED.config,secret_ref=EXCLUDED.secret_ref,updated_at=EXCLUDED.updated_at`, text, string(config))
	if err != nil {
		return Integration{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Integration{}, err
	}
	id, _ := uuid.Parse(text)
	return store.Integration(ctx, id)
}

// ValidateOldBoysSnapshot is the last pre-send guard. Credentials replacement,
// another commissioning attempt, disablement or config changes stale the client.
func (store *Store) ValidateOldBoysSnapshot(ctx context.Context, integration Integration, commissioning bool) error {
	var valid bool
	err := store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM integration_instances i CROSS JOIN oldboys_credentials c WHERE i.id=$1 AND i.kind='oldboys' AND i.config=$2::jsonb AND c.singleton AND (i.config->>'credentials_updated_at')::timestamptz=c.updated_at AND i.enabled=$3)`, integration.ID.String(), string(integration.Config), !commissioning).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrConcurrentModification
	}
	return nil
}

func (store *Store) CompleteOldBoysCommission(ctx context.Context, expected Integration, now time.Time) (Integration, error) {
	tx, err := store.Pool.Begin(ctx)
	if err != nil {
		return Integration{}, err
	}
	defer tx.Rollback(ctx)
	var stamp time.Time
	if err := tx.QueryRow(ctx, `SELECT updated_at FROM oldboys_credentials WHERE singleton=true FOR UPDATE`).Scan(&stamp); err != nil {
		return Integration{}, err
	}
	var config map[string]any
	if json.Unmarshal(expected.Config, &config) != nil {
		return Integration{}, ErrConcurrentModification
	}
	configuredVersion, _ := config["credentials_updated_at"].(string)
	configuredStamp, err := time.Parse(time.RFC3339Nano, configuredVersion)
	if err != nil || !configuredStamp.Equal(stamp) {
		return Integration{}, ErrConcurrentModification
	}
	config["mode_verified"] = true
	config["commissioned_at"] = now.UTC().Format(time.RFC3339Nano)
	config["proof"] = map[string]any{"movie_positive": true, "movie_negative": true, "series_positive": true, "series_negative": true, "valid_nzb": true}
	encoded, _ := json.Marshal(config)
	tag, err := tx.Exec(ctx, `UPDATE integration_instances SET enabled=true,config=$3,updated_at=$4 WHERE id=$1 AND kind='oldboys' AND NOT enabled AND config=$2::jsonb`, expected.ID.String(), string(expected.Config), string(encoded), now.UTC())
	if err != nil {
		return Integration{}, err
	}
	if tag.RowsAffected() != 1 {
		return Integration{}, ErrConcurrentModification
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','oldboys.commissioned','integration',$2,'{"scope":"danish_original_movies_series","download_key":"PID","requests":5}',$3)`, uuid.NewV7().String(), expected.ID.String(), now.UTC())
	if err != nil {
		return Integration{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Integration{}, err
	}
	return store.Integration(ctx, expected.ID)
}

func (store *Store) OldBoysEnabled(ctx context.Context) (bool, error) {
	var enabled bool
	err := store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM integration_instances i CROSS JOIN oldboys_credentials c WHERE i.kind='oldboys' AND i.enabled AND i.config->'mode_verified'='true'::jsonb AND c.singleton AND (i.config->>'credentials_updated_at')::timestamptz=c.updated_at)`).Scan(&enabled)
	return enabled, err
}

const commissionedScopedIndexerSQL = `SELECT EXISTS(SELECT 1 FROM integration_instances i WHERE i.enabled AND i.config->'mode_verified'='true'::jsonb AND (i.kind='newznab' OR (i.kind='oldboys' AND $1 AND EXISTS(SELECT 1 FROM oldboys_credentials c WHERE c.singleton AND (i.config->>'credentials_updated_at')::timestamptz=c.updated_at))))`

// OldBoysSearchPhase starts every new commission on page one. Deferred or
// incomplete scans retain the same request; older Newznab history cannot skip
// native pages or query shapes when the provider is introduced.
func (store *Store) OldBoysSearchPhase(ctx context.Context, workflowID uuid.UUID, integration Integration) (uint64, error) {
	var phase uint64
	err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM search_run_indexers outcome JOIN search_runs run ON run.id=outcome.search_run_id WHERE run.workflow_id=$1 AND outcome.indexer_instance_id=$2 AND outcome.state='succeeded' AND run.state='complete' AND run.started_at>=$3`, workflowID.String(), integration.ID.String(), integration.UpdatedAt).Scan(&phase)
	return phase, err
}
