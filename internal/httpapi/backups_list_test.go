package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/postgres"
)

func TestBackupListMarksOnlyConfirmedDeletedFiles(t *testing.T) {
	store := openHTTPTestStore(t)
	directory := t.TempDir()
	server, err := New(Config{OwnerToken: testOwnerToken, Version: "test", BackupDirectory: directory, BackupConfigured: true}, store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UTC()
	complete := func(filename string) {
		t.Helper()
		record, err := store.CreateBackup(ctx, now)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.StartBackup(ctx, record.ID, now); err != nil {
			t.Fatal(err)
		}
		if err := store.CompleteBackup(ctx, record.ID, filename, 1, strings.Repeat("a", 64), 1, now); err != nil {
			t.Fatal(err)
		}
	}
	complete("kept.mmbackup")
	complete("deleted.mmbackup")
	if err := os.WriteFile(filepath.Join(directory, "kept.mmbackup"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	queued, err := store.CreateBackup(ctx, now)
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/backups", nil)
	request.Header.Set("Authorization", "Bearer "+testOwnerToken)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data struct {
			Records []postgres.BackupRecord `json:"records"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	deleted := map[string]bool{}
	for _, record := range envelope.Data.Records {
		if record.ID == queued.ID {
			if record.LocalFileDeleted {
				t.Fatal("a backup without a file yet must not count as deleted")
			}
			continue
		}
		deleted[record.Filename] = record.LocalFileDeleted
	}
	if len(deleted) != 2 || deleted["kept.mmbackup"] || !deleted["deleted.mmbackup"] {
		t.Fatalf("deleted flags = %v", deleted)
	}
}

func TestMissingBackupDirectoryDoesNotClaimFilesAreDeleted(t *testing.T) {
	for _, directory := range []string{"", filepath.Join(t.TempDir(), "not-mounted")} {
		items := []postgres.BackupRecord{{State: "verified", Filename: "a.mmbackup"}}
		markDeletedBackupFiles(directory, items)
		if items[0].LocalFileDeleted {
			t.Fatalf("directory %q claimed the file was deleted", directory)
		}
	}
}
