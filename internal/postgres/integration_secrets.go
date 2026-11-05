package postgres

import (
	"context"
	"errors"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
)

// SaveIntegrationSecret stores an integration's sealed secret and points its
// secret_ref at it, in one transaction. The OldBoys integration keeps its own
// credential store and is refused here.
func (store *Store) SaveIntegrationSecret(ctx context.Context, integrationID uuid.UUID, sealed []byte, reference string, now time.Time) error {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var kind string
	if err := transaction.QueryRow(ctx, `SELECT kind FROM integration_instances WHERE id=$1 FOR UPDATE`, integrationID.String()).Scan(&kind); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if kind == "oldboys" {
		return refuse("invalid_integration_kind", "OldBoys credentials are saved on their own page", "Use PUT /api/v1/settings/oldboys/credentials.")
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO integration_secrets(integration_id,ciphertext,updated_at) VALUES($1,$2,$3)
		ON CONFLICT (integration_id) DO UPDATE SET ciphertext=EXCLUDED.ciphertext,updated_at=EXCLUDED.updated_at`, integrationID.String(), sealed, now.UTC()); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `UPDATE integration_instances SET secret_ref=$2,updated_at=$3 WHERE id=$1`, integrationID.String(), reference, now.UTC()); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','integration.secret_saved','integration',$2,'{}',$3)`, uuid.NewV7().String(), integrationID.String(), now.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

func (store *Store) LoadIntegrationSecret(ctx context.Context, integrationID uuid.UUID) ([]byte, error) {
	var sealed []byte
	err := store.Pool.QueryRow(ctx, `SELECT ciphertext FROM integration_secrets WHERE integration_id=$1`, integrationID.String()).Scan(&sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return sealed, err
}

// SealedSecretsExist reports whether any secret sealed with the server key is
// stored: an integration secret or the OldBoys credentials. A missing key file
// is then never replaced by a new key that could not open them.
func (store *Store) SealedSecretsExist(ctx context.Context) (bool, error) {
	var exists bool
	err := store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM integration_secrets) OR EXISTS(SELECT 1 FROM oldboys_credentials)`).Scan(&exists)
	return exists, err
}
