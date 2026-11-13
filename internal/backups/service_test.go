package backups

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/postgres"
)

type fakeCommandRunner struct{ commands []string }

func (runner *fakeCommandRunner) Run(_ context.Context, name string, arguments ...string) ([]byte, error) {
	runner.commands = append(runner.commands, name)
	if name == "pg_dump" {
		index := slices.Index(arguments, "--file")
		if index < 0 || index+1 >= len(arguments) {
			return nil, os.ErrInvalid
		}
		return nil, os.WriteFile(arguments[index+1], []byte("bounded fake PostgreSQL custom dump"), 0o600)
	}
	if name == "psql" {
		return []byte(fmt.Sprintf("%d\n", postgres.CurrentSchemaVersion)), nil
	}
	return nil, nil
}

func TestCreateAndVerifyUsesIsolatedRestoreLifecycle(t *testing.T) {
	directory := t.TempDir()
	runner := &fakeCommandRunner{}
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	service := &Service{Directory: directory, ResolveSecret: func(reference string) (string, error) {
		if reference != KeyReference {
			t.Fatalf("key reference=%q", reference)
		}
		return key, nil
	}, Runner: runner}
	id := uuid.NewV7()
	filename, size, checksum, schemaVersion, err := service.createAndVerify(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if filename != id.String()+".mmbackup" || size < 1 || len(checksum) != 64 || schemaVersion != postgres.CurrentSchemaVersion {
		t.Fatalf("filename=%q size=%d checksum=%q schema=%d", filename, size, checksum, schemaVersion)
	}
	if info, err := os.Stat(filepath.Join(directory, filename)); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("verified encrypted file info=%v err=%v", info, err)
	}
	if !slices.Equal(runner.commands, []string{"pg_dump", "dropdb", "createdb", "pg_restore", "psql", "dropdb"}) {
		t.Fatalf("commands=%v", runner.commands)
	}
}

// A restart during the restore check used to leave the fixed-name verification
// database and the temporary dumps behind; every later attempt then failed at
// createdb (2026-11-13: 37 attempts). A new attempt must drop the leftover
// database first and remove temporary files older than the handler limit.
func TestCreateAndVerifyRecoversFromKilledAttempt(t *testing.T) {
	directory := t.TempDir()
	old := time.Now().Add(-staleTemporaryAge - time.Minute)
	stale := []string{".backup-1.dump", ".backup-2.encrypted", ".verify-3.dump"}
	for _, name := range append(stale, ".verify-fresh.dump", "kept.mmbackup") {
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if slices.Contains(stale, name) || name == "kept.mmbackup" {
			if err := os.Chtimes(path, old, old); err != nil {
				t.Fatal(err)
			}
		}
	}
	runner := &fakeCommandRunner{}
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	service := &Service{Directory: directory, ResolveSecret: func(string) (string, error) { return key, nil }, Runner: runner}
	if _, _, _, _, err := service.createAndVerify(context.Background(), uuid.NewV7()); err != nil {
		t.Fatal(err)
	}
	if index := slices.Index(runner.commands, "createdb"); index < 1 || runner.commands[index-1] != "dropdb" {
		t.Fatalf("leftover verification database is not dropped before createdb: %v", runner.commands)
	}
	for _, name := range stale {
		if _, err := os.Stat(filepath.Join(directory, name)); !os.IsNotExist(err) {
			t.Fatalf("stale %s still exists: %v", name, err)
		}
	}
	for _, name := range []string{".verify-fresh.dump", "kept.mmbackup"} {
		if _, err := os.Stat(filepath.Join(directory, name)); err != nil {
			t.Fatalf("%s must be kept: %v", name, err)
		}
	}
}
