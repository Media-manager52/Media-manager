package oldboyscredentials

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type memoryStore struct {
	sealed  []byte
	updated *time.Time
	err     error
}

func (store *memoryStore) OldBoysCredentialStatus(context.Context) (*time.Time, error) {
	return store.updated, store.err
}
func (store *memoryStore) SaveOldBoysCredentials(_ context.Context, sealed []byte) (time.Time, error) {
	if store.err != nil {
		return time.Time{}, store.err
	}
	store.sealed = bytes.Clone(sealed)
	now := time.Now().UTC()
	store.updated = &now
	return now, nil
}
func (store *memoryStore) LoadOldBoysCredentials(context.Context) ([]byte, error) {
	return bytes.Clone(store.sealed), store.err
}

func TestCredentialsRemainEncryptedAndStatusesNeverEcho(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{}
	service, err := New(store, strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	input := Credentials{APIToken: "unique-api-token-fixture", PassKey: "unique-pid-fixture", RSSKey: "unique-rid-fixture"}
	status, err := service.Status(ctx)
	if err != nil || status.Configured || !status.Available {
		t.Fatalf("initial status: %+v %v", status, err)
	}
	status, err = service.Save(ctx, input)
	if err != nil || !status.Configured {
		t.Fatal("save failed", err)
	}
	encoded, _ := json.Marshal(status)
	for _, value := range []string{input.APIToken, input.PassKey, input.RSSKey} {
		if bytes.Contains(store.sealed, []byte(value)) || bytes.Contains(encoded, []byte(value)) {
			t.Fatal("secret appeared in storage or status")
		}
	}
	loaded, err := service.Load(ctx)
	if err != nil || loaded != input {
		t.Fatal("credential roundtrip failed", err)
	}
	first := bytes.Clone(store.sealed)
	if _, err := service.Save(ctx, input); err != nil || bytes.Equal(first, store.sealed) {
		t.Fatal("save must use a fresh nonce")
	}
	input.PassKey = "replacement-pid"
	if _, err := service.Save(ctx, input); err != nil {
		t.Fatal(err)
	}
	loaded, err = service.Load(ctx)
	if err != nil || loaded != input {
		t.Fatal("replacement failed", err)
	}
	other, _ := New(store, strings.Repeat("cd", 32))
	if _, err := other.Load(ctx); err == nil {
		t.Fatal("different key decrypted credentials")
	}
	store.sealed[len(store.sealed)-1] ^= 1
	if _, err := service.Load(ctx); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
}

func TestInvalidInputAndStorageErrorsNeverRevealSecrets(t *testing.T) {
	store := &memoryStore{}
	service, _ := New(store, strings.Repeat("ab", 32))
	ctx := context.Background()
	for _, value := range []string{"", "has space", "secret\nheader", "https://provider/key", strings.Repeat("x", 4097)} {
		if _, err := service.Save(ctx, Credentials{APIToken: value, PassKey: "pid", RSSKey: "rid"}); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatal("invalid token accepted")
		}
	}
	if len(store.sealed) != 0 {
		t.Fatal("invalid input was stored")
	}
	store.err = errors.New("database failure includes very-private-key")
	if _, err := service.Save(ctx, Credentials{APIToken: "api", PassKey: "pid", RSSKey: "rid"}); err == nil || strings.Contains(err.Error(), "very-private-key") {
		t.Fatal("storage error was not sanitized")
	}
	for _, key := range []string{"", strings.Repeat("a", 63), strings.Repeat("z", 64)} {
		if _, err := New(store, key); err == nil {
			t.Fatal("invalid server key accepted")
		}
	}
}
