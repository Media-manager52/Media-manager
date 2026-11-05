package integrationsecrets

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"uuid"
)

func TestEnsureKeyFileCreatesAPrivateKeyOnFirstStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret-encryption.key")
	key, created, err := EnsureKeyFile(path, func() (bool, error) { return false, nil })
	if err != nil || !created {
		t.Fatalf("EnsureKeyFile() = %q, %v, %v; want a new key", key, created, err)
	}
	if decoded, err := hex.DecodeString(key); err != nil || len(decoded) != 32 {
		t.Fatalf("generated key %q is not 64 hexadecimal characters", key)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("key file mode = %o, want 600", mode)
	}
	if _, err := New(&memoryStore{sealed: map[uuid.UUID][]byte{}, reference: map[uuid.UUID]string{}}, key); err != nil {
		t.Fatalf("generated key is not accepted: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v", entries)
	}

	again, created, err := EnsureKeyFile(path, func() (bool, error) { return true, nil })
	if err != nil || created || again != key {
		t.Fatalf("second start = %q, %v, %v; want the same key, not a new one", again, created, err)
	}
}

func TestEnsureKeyFileNeverReplacesALostKeyWhileSecretsAreStored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret-encryption.key")
	_, created, err := EnsureKeyFile(path, func() (bool, error) { return true, nil })
	if !errors.Is(err, ErrKeyFileLost) || created {
		t.Fatalf("EnsureKeyFile() = %v, %v; want ErrKeyFileLost", created, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a key file was created although sealed secrets exist: %v", err)
	}
}

func TestEnsureKeyFileKeepsAnEmptyFileAndReportsIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret-encryption.key")
	if err := os.WriteFile(path, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := EnsureKeyFile(path, func() (bool, error) { return false, nil }); err == nil {
		t.Fatal("an empty key file was accepted")
	}
	body, _ := os.ReadFile(path)
	if string(body) != "\n" {
		t.Fatalf("the empty key file was replaced: %q", body)
	}
}

func TestEnsureKeyFileWithoutPathDoesNothing(t *testing.T) {
	key, created, err := EnsureKeyFile("  ", nil)
	if key != "" || created || err != nil {
		t.Fatalf("EnsureKeyFile(\"\") = %q, %v, %v", key, created, err)
	}
}
