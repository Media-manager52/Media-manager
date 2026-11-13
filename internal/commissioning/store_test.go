package commissioning

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/media-manager/media-manager/internal/postgres"
)

// testStore opens an isolated schema in the disposable test database.
func testStore(t *testing.T) (*postgres.Store, context.Context) {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	schema := "mm_commissioning_test_" + strings.ReplaceAll(uuid.New().String(), "-", "")
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
	store, err := postgres.Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := postgres.Migrate(ctx, store.Pool); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		store.Close()
		cleanup, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanup, `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
		admin.Close()
	})
	return store, ctx
}
