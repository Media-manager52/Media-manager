package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/adapters/newznab"
)

func (store *Store) CompleteNewznabCommission(ctx context.Context, integrationID uuid.UUID, caps newznab.CapsSnapshot, now time.Time) (Integration, error) {
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Integration{}, err
	}
	defer transaction.Rollback(ctx)
	var kind string
	var enabled bool
	var configBody []byte
	if err := transaction.QueryRow(ctx, `SELECT kind,enabled,config FROM integration_instances WHERE id=$1 FOR UPDATE`, integrationID.String()).Scan(&kind, &enabled, &configBody); errors.Is(err, pgx.ErrNoRows) {
		return Integration{}, ErrNotFound
	} else if err != nil {
		return Integration{}, err
	}
	var config map[string]any
	if kind != "newznab" || json.Unmarshal(configBody, &config) != nil {
		return Integration{}, errors.New("integration is not a valid Newznab endpoint")
	}
	if verified, _ := config["mode_verified"].(bool); enabled && verified {
		if err := transaction.Commit(ctx); err != nil {
			return Integration{}, err
		}
		return store.Integration(ctx, integrationID)
	}
	config["mode"] = "search"
	config["mode_verified"] = true
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
	detail, _ := json.Marshal(map[string]any{"caps_sha256": caps.BodyHash, "server_version": caps.ServerVersion, "search_mode": "search"})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','newznab.commissioned','integration',$2,$3,$4)`, uuid.NewV7().String(), integrationID.String(), string(detail), now.UTC()); err != nil {
		return Integration{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return Integration{}, err
	}
	return store.Integration(ctx, integrationID)
}
