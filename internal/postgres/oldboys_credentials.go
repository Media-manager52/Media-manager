package postgres

import (
	"context"
	"errors"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
)

func (store *Store) OldBoysCredentialStatus(ctx context.Context) (*time.Time, error) {
	var updated time.Time
	err := store.Pool.QueryRow(ctx, `SELECT updated_at FROM oldboys_credentials WHERE singleton=true`).Scan(&updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

func (store *Store) SaveOldBoysCredentials(ctx context.Context, sealed []byte) (time.Time, error) {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return time.Time{}, err
	}
	defer transaction.Rollback(ctx)
	var updated time.Time
	if err := transaction.QueryRow(ctx, `INSERT INTO oldboys_credentials(singleton,sealed,updated_at) VALUES(true,$1,clock_timestamp()) ON CONFLICT(singleton) DO UPDATE SET sealed=EXCLUDED.sealed,updated_at=EXCLUDED.updated_at RETURNING updated_at`, sealed).Scan(&updated); err != nil {
		return time.Time{}, err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,detail,occurred_at) VALUES($1,'owner','oldboys.credentials_saved','oldboys_credentials','{"credentials_saved":true}',$2)`, uuid.NewV7().String(), updated); err != nil {
		return time.Time{}, err
	}
	return updated, transaction.Commit(ctx)
}

func (store *Store) LoadOldBoysCredentials(ctx context.Context) ([]byte, error) {
	var sealed []byte
	err := store.Pool.QueryRow(ctx, `SELECT sealed FROM oldboys_credentials WHERE singleton=true`).Scan(&sealed)
	return sealed, err
}
