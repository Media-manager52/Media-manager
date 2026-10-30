package main

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	metadataproviders "github.com/media-manager/media-manager/internal/adapters/metadataproviders"
	"github.com/media-manager/media-manager/internal/adapters/seerr"
	"github.com/media-manager/media-manager/internal/backups"
	"github.com/media-manager/media-manager/internal/catalog"
)

func TestNewMetadataServiceLeavesUnconfiguredProvidersNil(t *testing.T) {
	service := newMetadataService(nil, nil, nil)
	if service.TMDB != nil || service.TVDB != nil || service.Simkl != nil {
		t.Fatalf("unconfigured providers must stay nil interfaces: %#v", service)
	}
	// Seerr intake enriches through this path; with no direct provider it must
	// fall back to Seerr's TMDB data instead of calling a nil client.
	result, err := service.Build(context.Background(), catalog.KindMovie, 603, seerr.Metadata{ID: 603, Title: "The Matrix", ReleaseDate: "1999-03-31"}, nil, time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if result.EnrichmentState != "tmdb_via_seerr" || result.ProviderStates["tmdb"] != "seerr_fallback" {
		t.Fatalf("unexpected enrichment: state=%q providers=%v", result.EnrichmentState, result.ProviderStates)
	}
}

func TestNewMetadataServiceKeepsConfiguredProviders(t *testing.T) {
	tmdb, err := metadataproviders.NewTMDB(metadataproviders.TMDBConfig{AccessToken: "token"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := newMetadataService(tmdb, nil, nil)
	if service.TMDB == nil || service.TVDB != nil || service.Simkl != nil {
		t.Fatalf("only the configured provider should be wired: %#v", service)
	}
}

func TestDecryptBackupWritesAuthenticatedDump(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	t.Setenv("BACKUP_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(key))
	encryptedPath := filepath.Join(t.TempDir(), "input.mmbak")
	outputPath := filepath.Join(t.TempDir(), "output.dump")
	encrypted, err := os.Create(encryptedPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := backups.Encrypt(encrypted, strings.NewReader("postgres custom dump"), key); err != nil {
		t.Fatal(err)
	}
	if err := encrypted.Close(); err != nil {
		t.Fatal(err)
	}
	if err := decryptBackup([]string{encryptedPath, outputPath}); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "postgres custom dump" {
		t.Fatalf("decrypted content=%q", content)
	}
}

func writeEncryptedBackupFixture(t *testing.T, path string, plaintext string) []byte {
	t.Helper()
	key := []byte("0123456789abcdef0123456789abcdef")
	t.Setenv("BACKUP_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(key))
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := backups.Encrypt(file, strings.NewReader(plaintext), key); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestDecryptBackupRejectsSameInputAndOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "same.mmbak")
	before := writeEncryptedBackupFixture(t, path, "postgres custom dump")
	if err := decryptBackup([]string{path, path}); err == nil {
		t.Fatal("same input/output path was accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("input backup changed after same-path rejection")
	}
}

func TestDecryptBackupLeavesNoPartialOutputOnTamper(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "tampered.mmbak")
	output := filepath.Join(dir, "should-not-exist.dump")
	body := writeEncryptedBackupFixture(t, input, "postgres custom dump")
	body[len(body)-1] ^= 0xff
	if err := os.WriteFile(input, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := decryptBackup([]string{input, output}); err == nil {
		t.Fatal("tampered backup was accepted")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("partial plaintext output remains: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".media-manager-backup-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary plaintext files remain: %v", matches)
	}
}
