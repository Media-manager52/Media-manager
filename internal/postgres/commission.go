package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/indexers"
)

func (store *Store) CompleteNewznabCommission(ctx context.Context, integrationID uuid.UUID, caps newznab.CapsSnapshot, dialects newznab.SearchDialects, now time.Time) (Integration, error) {
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Integration{}, err
	}
	defer transaction.Rollback(ctx)
	var kind string
	var configBody []byte
	if err := transaction.QueryRow(ctx, `SELECT kind,config FROM integration_instances WHERE id=$1 FOR UPDATE`, integrationID.String()).Scan(&kind, &configBody); errors.Is(err, pgx.ErrNoRows) {
		return Integration{}, ErrNotFound
	} else if err != nil {
		return Integration{}, err
	}
	var config map[string]any
	if kind != "newznab" || json.Unmarshal(configBody, &config) != nil {
		return Integration{}, errors.New("integration is not a valid Newznab endpoint")
	}
	if dialects.Movie.Mode == "" || dialects.Series.Mode == "" {
		return Integration{}, errors.New("commissioned Newznab search dialects are incomplete")
	}
	config["mode"] = "search" // Backward-compatible fallback for pre-dialect readers.
	config["mode_verified"] = true
	config["search_dialects"] = dialects
	config["default_limit"] = caps.DefaultLimit
	config["maximum_limit"] = caps.MaximumLimit
	configuredQuery, _ := numericJSON(config["query_limit"])
	configuredGrab, _ := numericJSON(config["grab_limit"])
	configuredWindow, _ := numericJSON(config["budget_window_seconds"])
	budget := indexers.NewznabBudget(0, configuredQuery, configuredGrab, int64(configuredWindow))
	config["query_limit"] = budget.QueryLimit
	config["grab_limit"] = budget.GrabLimit
	config["budget_window_seconds"] = int64(budget.LimitWindow / time.Second)
	if _, exists := config["budget_source"]; !exists {
		config["budget_source"] = "conservative_fallback"
	}
	config["commissioned_at"] = now.UTC().Format(time.RFC3339Nano)
	config["caps_sha256"] = caps.BodyHash
	config["server_version"] = caps.ServerVersion
	encoded, err := json.Marshal(config)
	if err != nil {
		return Integration{}, err
	}
	if _, err := transaction.Exec(ctx, `UPDATE integration_instances SET enabled=true,is_default=false,config=$2,updated_at=$3 WHERE id=$1`, integrationID.String(), string(encoded), now.UTC()); err != nil {
		return Integration{}, err
	}
	detail, _ := json.Marshal(map[string]any{"caps_sha256": caps.BodyHash, "server_version": caps.ServerVersion, "movie_search_mode": dialects.Movie.Mode, "series_search_mode": dialects.Series.Mode})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','newznab.commissioned','integration',$2,$3,$4)`, uuid.NewV7().String(), integrationID.String(), string(detail), now.UTC()); err != nil {
		return Integration{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return Integration{}, err
	}
	return store.Integration(ctx, integrationID)
}

func numericJSON(value any) (int, bool) {
	switch typed := value.(type) {
	case float64:
		if typed > 0 && typed == float64(int(typed)) {
			return int(typed), true
		}
	case int:
		if typed > 0 {
			return typed, true
		}
	}
	return 0, false
}
