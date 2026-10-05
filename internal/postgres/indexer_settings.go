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

// IndexerSettingsPatch changes owner-controlled settings of one indexer. Nil
// fields are left unchanged.
type IndexerSettingsPatch struct {
	Name              *string `json:"name"`
	Enabled           *bool   `json:"enabled"`
	QueryLimit        *int    `json:"query_limit"`
	GrabLimit         *int    `json:"grab_limit"`
	MinimumIntervalMS *int64  `json:"minimum_interval_ms"`
}

const (
	minIndexerIntervalMS = 3000
	minOldBoysIntervalMS = 5000
	maxIndexerIntervalMS = 600000
)

// UpdateIndexerSettings applies an owner patch to a Newznab or native
// indexer. Disabling is always allowed. Enabling requires a commissioned
// Newznab indexer; OldBoys is only enabled by its own validation. Limits are
// recorded as owner-stated, pacing never drops below the provider floor, and
// every change is audited with the previous and new values.
func (store *Store) UpdateIndexerSettings(ctx context.Context, id uuid.UUID, patch IndexerSettingsPatch, now time.Time) (Integration, error) {
	if patch.Name == nil && patch.Enabled == nil && patch.QueryLimit == nil && patch.GrabLimit == nil && patch.MinimumIntervalMS == nil {
		return Integration{}, refuse("empty_patch", "no setting was given", "Send at least one of name, enabled, query_limit, grab_limit, minimum_interval_ms.")
	}
	tx, err := store.Pool.Begin(ctx)
	if err != nil {
		return Integration{}, err
	}
	defer tx.Rollback(ctx)
	var kind, name string
	var enabled bool
	var body []byte
	err = tx.QueryRow(ctx, `SELECT kind,name,enabled,config FROM integration_instances WHERE id=$1 FOR UPDATE`, id.String()).Scan(&kind, &name, &enabled, &body)
	if errors.Is(err, pgx.ErrNoRows) {
		return Integration{}, ErrNotFound
	}
	if err != nil {
		return Integration{}, err
	}
	if kind != "newznab" && kind != "oldboys" {
		return Integration{}, refuse("not_an_indexer", "only indexer settings can be changed here", "AltMount, Plex and Seerr settings are not editable through this route.")
	}
	var config map[string]any
	if json.Unmarshal(body, &config) != nil {
		return Integration{}, ErrConcurrentModification
	}
	before := map[string]any{"name": name, "enabled": enabled, "query_limit": config["query_limit"], "grab_limit": config["grab_limit"], "minimum_interval_ms": config["minimum_interval_ms"]}
	after := map[string]any{}
	if patch.Name != nil {
		trimmed := strings.TrimSpace(*patch.Name)
		if trimmed == "" || len(trimmed) > 200 {
			return Integration{}, refuse("invalid_name", "name must be 1-200 characters", "")
		}
		name, after["name"] = trimmed, trimmed
	}
	if patch.QueryLimit != nil {
		if *patch.QueryLimit < 1 || *patch.QueryLimit > MaxOldBoysQueryLimit {
			return Integration{}, refuse("invalid_query_limit", "query_limit must be 1-50000", "")
		}
		config["query_limit"], after["query_limit"] = *patch.QueryLimit, *patch.QueryLimit
	}
	if patch.GrabLimit != nil {
		if *patch.GrabLimit < 1 || *patch.GrabLimit > MaxOldBoysGrabLimit {
			return Integration{}, refuse("invalid_grab_limit", "grab_limit must be 1-5000", "")
		}
		config["grab_limit"], after["grab_limit"] = *patch.GrabLimit, *patch.GrabLimit
	}
	if patch.QueryLimit != nil || patch.GrabLimit != nil {
		config["budget_source"] = "owner_stated"
		config["budget_updated_at"] = now.UTC().Format(time.RFC3339Nano)
	}
	if patch.MinimumIntervalMS != nil {
		floor := int64(minIndexerIntervalMS)
		if kind == "oldboys" {
			floor = minOldBoysIntervalMS
		}
		if *patch.MinimumIntervalMS < floor || *patch.MinimumIntervalMS > maxIndexerIntervalMS {
			return Integration{}, refuse("invalid_minimum_interval", "minimum_interval_ms is outside the allowed range", "Use at least the provider floor (3000 ms; OldBoys 5000 ms) and at most 600000 ms.")
		}
		config["minimum_interval_ms"], after["minimum_interval_ms"] = *patch.MinimumIntervalMS, *patch.MinimumIntervalMS
	}
	if patch.Enabled != nil && *patch.Enabled != enabled {
		if *patch.Enabled {
			if kind == "oldboys" {
				return Integration{}, refuse("commission_required", "OldBoys is enabled only by its own validation", "Call POST /api/v1/settings/oldboys/commission.")
			}
			if verified, _ := config["mode_verified"].(bool); !verified {
				return Integration{}, refuse("commission_required", "this indexer has not been commissioned", "Call POST /api/v1/settings/integrations/{id}/commission first.")
			}
		}
		enabled, after["enabled"] = *patch.Enabled, *patch.Enabled
	}
	encoded, _ := json.Marshal(config)
	if _, err := tx.Exec(ctx, `UPDATE integration_instances SET name=$2,enabled=$3,config=$4,updated_at=$5 WHERE id=$1`, id.String(), name, enabled, string(encoded), now.UTC()); err != nil {
		return Integration{}, err
	}
	detail, _ := json.Marshal(map[string]any{"kind": kind, "before": before, "after": after})
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','integration.settings_changed','integration',$2,$3,$4)`, uuid.NewV7().String(), id.String(), string(detail), now.UTC()); err != nil {
		return Integration{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Integration{}, err
	}
	return store.Integration(ctx, id)
}
