package postgres

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestMigrationLockKeepsProductionKeyAndIsolatesTestSchemas pins the advisory
// lock contract: the production schema ("public") must keep the historical
// single key so binaries of every version still exclude each other while
// migrating, and an isolated schema gets its own key so parallel test schemas
// do not queue behind one database-wide lock.
func TestMigrationLockKeepsProductionKeyAndIsolatesTestSchemas(t *testing.T) {
	databaseURL := os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()

	lockFor := func(searchPath string) (lockStatement, lockStatement) {
		t.Helper()
		parsed, err := url.Parse(databaseURL)
		if err != nil {
			t.Fatal(err)
		}
		query := parsed.Query()
		query.Set("search_path", searchPath)
		parsed.RawQuery = query.Encode()
		pool, err := pgxpool.New(ctx, parsed.String())
		if err != nil {
			t.Fatal(err)
		}
		defer pool.Close()
		connection, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer connection.Release()
		lock, unlock, err := migrationLock(ctx, connection.Conn())
		if err != nil {
			t.Fatal(err)
		}
		return lock, unlock
	}

	lock, unlock := lockFor("public")
	if lock.sql != `SELECT pg_advisory_lock($1)` || len(lock.args) != 1 || lock.args[0] != int64(473652773271) {
		t.Fatalf("production schema lock changed: %q %v", lock.sql, lock.args)
	}
	if unlock.sql != `SELECT pg_advisory_unlock($1)` || len(unlock.args) != 1 || unlock.args[0] != int64(473652773271) {
		t.Fatalf("production schema unlock changed: %q %v", unlock.sql, unlock.args)
	}
	missingLock, _ := lockFor("mm_missing_" + strings.ReplaceAll(uuid.New().String(), "-", ""))
	if missingLock.args[0] != int64(473652773271) {
		t.Fatalf("a search_path without an existing schema must fall back to the production key: %v", missingLock.args)
	}

	schemas := make([]string, 2)
	for index := range schemas {
		schemas[index] = "mm_lock_" + strings.ReplaceAll(uuid.New().String(), "-", "")
		if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schemas[index]); err != nil {
			t.Fatal(err)
		}
		schema := schemas[index]
		t.Cleanup(func() { _, _ = admin.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`) })
	}
	first, _ := lockFor(schemas[0])
	second, _ := lockFor(schemas[1])
	if !strings.Contains(first.sql, "hashtext($1)") || first.args[0] != schemas[0] || second.args[0] != schemas[1] {
		t.Fatalf("isolated schemas must lock on their own key: %q %v / %v", first.sql, first.args, second.args)
	}

	// The held lock of one schema must block the same schema and nothing else.
	holder, err := admin.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Release()
	if _, err := holder.Exec(ctx, first.sql, first.args...); err != nil {
		t.Fatal(err)
	}
	defer holder.Exec(context.Background(), `SELECT pg_advisory_unlock_all()`)
	try := func(statement lockStatement) bool {
		t.Helper()
		var acquired bool
		trySQL := strings.Replace(statement.sql, "pg_advisory_lock", "pg_try_advisory_lock", 1)
		other, err := admin.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer other.Release()
		if err := other.QueryRow(ctx, trySQL, statement.args...).Scan(&acquired); err != nil {
			t.Fatal(err)
		}
		if acquired {
			unlockSQL := strings.Replace(statement.sql, "pg_advisory_lock", "pg_advisory_unlock", 1)
			if _, err := other.Exec(ctx, unlockSQL, statement.args...); err != nil {
				t.Fatal(err)
			}
		}
		return acquired
	}
	if try(first) {
		t.Fatal("a second migration of the same schema acquired its lock")
	}
	if !try(second) {
		t.Fatal("a migration of a different schema waited for an unrelated schema")
	}
	if !try(lock) {
		t.Fatal("the production key must not collide with a test schema key")
	}
}
