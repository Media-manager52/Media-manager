package acquisition

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/workflows"
)

func TestInvalidOutputEncodingFailsInspectionAndPreservesOutput(t *testing.T) {
	store := openMovieSearchTestStore(t)
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "job", "movie-\xfc.mkv")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("retained"), 0o644); err != nil {
		t.Fatal(err)
	}
	const queueID = 4242
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/auth/config":
			_, _ = w.Write([]byte(`{"success":true,"data":{"login_required":false}}`))
		case "/api/user":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":"1","name":"admin","is_admin":true}}`))
		case "/api/queue/4242":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"id": queueID, "status": "completed", "storage_path": "/job", "created_at": time.Now().UTC(), "updated_at": time.Now().UTC()}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	indexer := configureMovieSearchIndexer(t, store, "http://indexer.invalid")
	workflow := createMovieSearchWorkflow(t, store)
	config, _ := json.Marshal(map[string]any{"base_url": server.URL, "allow_http": true, "authentication": "anonymous_admin", "reported_root": "/", "local_read_only_root": root})
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET config=$2 WHERE id=$1`, workflow.DownloadClientID.String(), config); err != nil {
		t.Fatal(err)
	}
	jobID := seedQueuedAltMountJob(t, store, workflow.ID, indexer.ID, workflow.DownloadClientID, queueID)
	job, err := store.LoadAltMountJob(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	service := &AltMountReconcileService{Store: store, CheckMount: func(string) error { return nil }}
	event := postgres.OutboxEvent{AggregateID: jobID, AggregateRevision: job.JobRevision}
	if err := service.Handle(ctx, event); err != nil {
		t.Fatalf("reconcile retried instead of recording the inspection failure: %v", err)
	}
	if err := service.Handle(ctx, event); err != nil {
		t.Fatalf("duplicate reconcile: %v", err)
	}
	after, err := store.LoadAltMountJob(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	var outcome string
	if err := store.Pool.QueryRow(ctx, `SELECT acquisition_outcome FROM altmount_jobs WHERE id=$1`, jobID.String()).Scan(&outcome); err != nil {
		t.Fatal(err)
	}
	if after.Workflow.State != workflows.StateWaiting || outcome != postgres.InspectionFailedCode {
		t.Fatalf("failure not recorded safely: state=%s outcome=%s", after.Workflow.State, outcome)
	}
	var assets int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM altmount_output_assets`).Scan(&assets); err != nil || assets != 0 {
		t.Fatalf("invalid paths persisted: assets=%d err=%v", assets, err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "retained" {
		t.Fatalf("failed output changed: %q err=%v", data, err)
	}
}
