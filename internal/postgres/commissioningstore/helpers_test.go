package commissioningstore

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/media-manager/media-manager/internal/postgres/migrations"
)

func openStore(ctx context.Context, databaseURL string) (*Commissioning, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Commissioning{Pool: pool}, nil
}

func (store *Commissioning) Close() { store.Pool.Close() }

func isolatedCommissioningStore(t *testing.T) (*Commissioning, context.Context) {
	t.Helper()
	databaseURL := os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	schema := "mm_indexer_commissioning_repository_" + strings.ReplaceAll(uuid.New().String(), "-", "")
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	store, err := openStore(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := migrations.Migrate(ctx, store.Pool); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		store.Close()
		cleanup, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanup, `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
		admin.Close()
	})
	return store, ctx
}
