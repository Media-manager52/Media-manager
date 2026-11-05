package backups

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureKeyFileCreatesAValidKeyOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backup.key")
	key, created, err := EnsureKeyFile(path, func() (bool, error) { return false, nil })
	if err != nil || !created {
		t.Fatalf("EnsureKeyFile() = %q, %v, %v", key, created, err)
	}
	if _, err := ParseKey(key); err != nil {
		t.Fatalf("generated key is invalid: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode = %v, %v", info.Mode(), err)
	}
	again, created, err := EnsureKeyFile(path, func() (bool, error) { t.Fatal("must not check backups when the file exists"); return false, nil })
	if err != nil || created || again != key {
		t.Fatalf("second EnsureKeyFile() = %q, %v, %v", again, created, err)
	}
}

func TestEnsureKeyFileNeverReplacesALostKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backup.key")
	if _, _, err := EnsureKeyFile(path, func() (bool, error) { return true, nil }); !errors.Is(err, ErrKeyFileLost) {
		t.Fatalf("with earlier backups = %v, want ErrKeyFileLost", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a key was created although earlier backups exist")
	}
	if err := os.WriteFile(path, []byte("not-a-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := EnsureKeyFile(path, nil); err == nil {
		t.Fatal("an invalid key file was accepted")
	}
	if body, _ := os.ReadFile(path); string(body) != "not-a-key\n" {
		t.Fatal("an existing key file was replaced")
	}
}
