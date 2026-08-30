package backups

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
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
	if !slices.Equal(runner.commands, []string{"pg_dump", "createdb", "pg_restore", "psql", "dropdb"}) {
		t.Fatalf("commands=%v", runner.commands)
	}
}
