package platform

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

// TestBackupLaneDoesNotHoldUpOtherWork runs both lanes as production does:
// while a backup is stuck in its handler, ordinary events still finish, and
// the main lane never picks up a second backup.
func TestBackupLaneDoesNotHoldUpOtherWork(t *testing.T) {
	databaseURL := os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	schema := "mm_lane_" + strings.ReplaceAll(uuid.NewV7().String(), "-", "")
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
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
	defer store.Close()
	if err := postgres.Migrate(ctx, store.Pool); err != nil {
		t.Fatal(err)
	}
	insert := func(eventType string) uuid.UUID {
		id := uuid.NewV7()
		if _, err := store.Pool.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'test',$2,1,$3,'{}',now(),now())`, id.String(), uuid.NewV7().String(), eventType); err != nil {
			t.Fatal(err)
		}
		return id
	}
	backups := []string{"backup.create_requested", "backup.offsite_requested"}
	backupStarted, releaseBackup := make(chan struct{}, 2), make(chan struct{})
	ordinaryDone := make(chan struct{}, 1)
	backupHandler := func(handlerCtx context.Context, _ postgres.OutboxEvent) error {
		backupStarted <- struct{}{}
		select {
		case <-releaseBackup:
		case <-handlerCtx.Done():
		}
		return nil
	}
	handlers := map[string]Handler{
		"backup.create_requested":  backupHandler,
		"backup.offsite_requested": backupHandler,
		"plex.confirm_requested":   func(context.Context, postgres.OutboxEvent) error { ordinaryDone <- struct{}{}; return nil },
	}
	main := &Worker{Store: store, Owner: "main", Handlers: handlers, Lease: time.Minute, Lane: postgres.OutboxLane{Except: backups}}
	backup := &Worker{Store: store, Owner: "main-backup", Handlers: handlers, Lease: time.Minute, Lane: postgres.OutboxLane{Only: backups}}
	insert("backup.create_requested")
	second := insert("backup.offsite_requested")
	if err := main.RunBatch(ctx); err != nil {
		t.Fatal(err)
	}
	var attempts int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE lease_owner IS NOT NULL OR completed_at IS NOT NULL`).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("the main lane took a backup: %d err=%v", attempts, err)
	}
	backupDone := make(chan error, 1)
	go func() { backupDone <- backup.RunBatch(ctx) }()
	select {
	case <-backupStarted:
	case <-ctx.Done():
		t.Fatal("backup did not start")
	}
	insert("plex.confirm_requested")
	if err := main.RunBatch(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ordinaryDone:
	case <-ctx.Done():
		t.Fatal("ordinary work waited for the backup")
	}
	var secondClaimed bool
	if err := store.Pool.QueryRow(ctx, `SELECT lease_owner IS NOT NULL FROM outbox_events WHERE id=$1`, second.String()).Scan(&secondClaimed); err != nil || secondClaimed {
		t.Fatalf("a second backup started alongside the first: %v err=%v", secondClaimed, err)
	}
	close(releaseBackup)
	if err := <-backupDone; err != nil {
		t.Fatal(err)
	}
}
