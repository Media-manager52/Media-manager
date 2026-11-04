package postgres

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

const CurrentSchemaVersion int64 = 52

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	return migrateThrough(ctx, pool, CurrentSchemaVersion)
}

func migrateThrough(ctx context.Context, pool *pgxpool.Pool, maximumVersion int64) error {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	connection, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer connection.Release()
	lock, unlock, err := migrationLock(ctx, connection.Conn())
	if err != nil {
		return err
	}
	if _, err := connection.Exec(ctx, lock.sql, lock.args...); err != nil {
		return err
	}
	defer connection.Exec(context.Background(), unlock.sql, unlock.args...)
	if _, err := connection.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version bigint PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		versionText := strings.SplitN(entry.Name(), "_", 2)[0]
		version, err := strconv.ParseInt(versionText, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid migration filename %s", entry.Name())
		}
		if version > maximumVersion {
			continue
		}
		var already bool
		if err := connection.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version=$1)`, version).Scan(&already); err != nil {
			return err
		}
		if already {
			continue
		}
		body, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return err
		}
		transaction, err := connection.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := transaction.Exec(ctx, string(body)); err != nil {
			_ = transaction.Rollback(ctx)
			return fmt.Errorf("apply migration %s: %w", entry.Name(), err)
		}
		if _, err := transaction.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES ($1)`, version); err != nil {
			_ = transaction.Rollback(ctx)
			return err
		}
		if err := transaction.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

func SchemaVersion(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	var version int64
	err := pool.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version)
	return version, err
}

// migrationLockKey is the advisory lock that serializes migrations of the
// production schema. Every binary that has ever migrated production uses it,
// so it must never change.
const migrationLockKey int64 = 473652773271

type lockStatement struct {
	sql  string
	args []any
}

// migrationLock returns the advisory lock and unlock statements for the
// connection's current schema. The production schema ("public") keeps the
// single historical key, so old and new binaries still exclude each other.
// Any other schema (isolated test schemas) is locked on the two-key form
// (namespace, schema), which PostgreSQL keeps in a separate key space. Two
// migrations of the same schema still exclude each other, but migrations of
// different test schemas no longer queue behind one database-wide lock.
func migrationLock(ctx context.Context, connection *pgx.Conn) (lockStatement, lockStatement, error) {
	var schema string
	if err := connection.QueryRow(ctx, `SELECT COALESCE(current_schema(), '')`).Scan(&schema); err != nil {
		return lockStatement{}, lockStatement{}, err
	}
	if schema == "public" || schema == "" {
		return lockStatement{sql: `SELECT pg_advisory_lock($1)`, args: []any{migrationLockKey}},
			lockStatement{sql: `SELECT pg_advisory_unlock($1)`, args: []any{migrationLockKey}}, nil
	}
	return lockStatement{sql: `SELECT pg_advisory_lock(hashtext('media-manager-migrate'), hashtext($1))`, args: []any{schema}},
		lockStatement{sql: `SELECT pg_advisory_unlock(hashtext('media-manager-migrate'), hashtext($1))`, args: []any{schema}}, nil
}
