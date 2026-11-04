package integrationsecrets

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"uuid"
)

type memoryStore struct {
	sealed    map[uuid.UUID][]byte
	reference map[uuid.UUID]string
}

func (store *memoryStore) SaveIntegrationSecret(_ context.Context, id uuid.UUID, sealed []byte, reference string, _ time.Time) error {
	store.sealed[id], store.reference[id] = append([]byte(nil), sealed...), reference
	return nil
}

func (store *memoryStore) LoadIntegrationSecret(_ context.Context, id uuid.UUID) ([]byte, error) {
	sealed, ok := store.sealed[id]
	if !ok {
		return nil, ErrNotStored
	}
	return sealed, nil
}

const testKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

// TestSecretsAreEncryptedAndBoundToTheirIntegration proves a saved secret is
// not stored in plaintext, resolves only through its own reference, and a
// ciphertext moved to another integration does not decrypt.
func TestSecretsAreEncryptedAndBoundToTheirIntegration(t *testing.T) {
	store := &memoryStore{sealed: map[uuid.UUID][]byte{}, reference: map[uuid.UUID]string{}}
	service, err := New(store, testKey)
	if err != nil {
		t.Fatal(err)
	}
	plex, indexer := uuid.NewV7(), uuid.NewV7()
	if err := service.Save(context.Background(), plex, "  plex-token-123  ", time.Now()); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(store.sealed[plex], []byte("plex-token-123")) {
		t.Fatal("the secret is stored in plaintext")
	}
	if store.reference[plex] != Reference(plex) || !IsReference(store.reference[plex]) {
		t.Fatalf("reference=%q", store.reference[plex])
	}
	value, err := service.Resolve(context.Background(), Reference(plex))
	if err != nil || value != "plex-token-123" {
		t.Fatalf("resolve=%q err=%v", value, err)
	}
	store.sealed[indexer] = store.sealed[plex]
	if _, err := service.Resolve(context.Background(), Reference(indexer)); err == nil {
		t.Fatal("a ciphertext decrypted for another integration")
	}
	if _, err := service.Resolve(context.Background(), "PLEX_TOKEN"); err == nil {
		t.Fatal("an environment reference resolved through the sealed store")
	}
	for _, bad := range []string{"", "has space", "line\nbreak", strings.Repeat("x", 4097), "ünïcode"} {
		if err := service.Save(context.Background(), plex, bad, time.Now()); !errors.Is(err, ErrInvalidSecret) {
			t.Fatalf("Save(%q) err=%v, want ErrInvalidSecret", bad, err)
		}
	}
	if _, err := New(store, "short"); err == nil {
		t.Fatal("a short key was accepted")
	}
	passphrase, err := New(store, "Cosmos-generated-password-0123456789")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := passphrase.Resolve(context.Background(), Reference(plex)); err == nil {
		t.Fatal("a secret decrypted under another key")
	}
	var missing *Service
	if err := missing.Save(context.Background(), plex, "x", time.Now()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil service save err=%v", err)
	}
}

// TestDerivedSecretsAreStablePerLabelAndKey proves a derived secret survives a
// restart, differs per label and per server key, and never reveals the key.
func TestDerivedSecretsAreStablePerLabelAndKey(t *testing.T) {
	store := &memoryStore{sealed: map[uuid.UUID][]byte{}, reference: map[uuid.UUID]string{}}
	first, err := New(store, testKey)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := New(store, testKey)
	other, _ := New(store, strings.Repeat("ab", 32))
	value := first.Derive("seerr-webhook:a")
	if len(value) != 64 || value != again.Derive("seerr-webhook:a") || value == first.Derive("seerr-webhook:b") || value == other.Derive("seerr-webhook:a") || strings.Contains(value, testKey) {
		t.Fatalf("derived secret is not stable and separated: %q", value)
	}
	var missing *Service
	if missing.Derive("x") != "" {
		t.Fatal("a nil service derived a secret")
	}
}
