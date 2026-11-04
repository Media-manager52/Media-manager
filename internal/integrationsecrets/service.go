// Package integrationsecrets stores an integration's secret (a Plex token, an
// indexer API key, a Seerr API key, an AltMount API secret) encrypted in the
// database, so the owner can enter it on the website instead of editing .env.
// A secret is write-only: no HTTP route ever returns it, and it is decrypted
// only for server-side clients through Resolve.
package integrationsecrets

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode"
	"uuid"
)

// KeyReference names the server key (64 hexadecimal characters, or a random
// passphrase of at least 32 characters). When it is not
// set, the OldBoys credential key is used: both are server-side AES-256-GCM
// keys, and each ciphertext is bound to its own purpose and integration by its
// associated data.
const KeyReference = "SECRET_ENCRYPTION_KEY"

// ReferencePrefix marks an integration secret_ref whose value is stored here.
const ReferencePrefix = "sealed:integration:"

var (
	ErrUnavailable   = errors.New("secret storage is not configured: set SECRET_ENCRYPTION_KEY (64 hexadecimal characters) and recreate the API")
	ErrInvalidSecret = errors.New("enter the secret as one line of printable characters (at most 4096)")
	ErrNotStored     = errors.New("no secret is stored for this integration")
)

type Store interface {
	SaveIntegrationSecret(ctx context.Context, integrationID uuid.UUID, sealed []byte, reference string, now time.Time) error
	LoadIntegrationSecret(ctx context.Context, integrationID uuid.UUID) ([]byte, error)
}

type Service struct {
	store  Store
	aead   cipher.AEAD
	derive []byte
}

// New takes a 64-character hexadecimal key, or a random passphrase of at least
// 32 characters (an installer's generated password) that is stretched into a
// 256-bit key with SHA-256. An invalid key is a configuration error and never
// falls back to plaintext.
func New(store Store, key string) (*Service, error) {
	key = strings.TrimSpace(key)
	decoded, err := hex.DecodeString(key)
	if err != nil || len(decoded) != 32 {
		if len(key) < 32 {
			return nil, errors.New("integration secret encryption requires a 64-character hexadecimal key or a passphrase of at least 32 characters")
		}
		sum := sha256.Sum256([]byte("media-manager:integration-secret-key:v1:" + key))
		decoded = sum[:]
	}
	if store == nil {
		return nil, errors.New("integration secret encryption requires a store")
	}
	block, err := aes.NewCipher(decoded)
	if err != nil {
		return nil, errors.New("integration secret encryption is unavailable")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("integration secret encryption is unavailable")
	}
	deriveKey := sha256.Sum256(append([]byte("media-manager:derived-secret-key:v1:"), decoded...))
	return &Service{store: store, aead: aead, derive: deriveKey[:]}, nil
}

// Derive returns a stable 64-character secret for label, computed from the
// server key, so a secret such as the Seerr webhook's needs no storage. It is
// the same after every restart and differs per label; it is empty for a nil
// service.
func (service *Service) Derive(label string) string {
	if service == nil || len(service.derive) == 0 {
		return ""
	}
	mac := hmac.New(sha256.New, service.derive)
	_, _ = mac.Write([]byte("media-manager:derived-secret:v1:" + label))
	return hex.EncodeToString(mac.Sum(nil))
}

// Reference is the secret_ref an integration gets once its secret is stored.
func Reference(integrationID uuid.UUID) string {
	return ReferencePrefix + integrationID.String()
}

// IsReference reports whether a secret_ref points at this store.
func IsReference(reference string) bool {
	return strings.HasPrefix(strings.TrimSpace(reference), ReferencePrefix)
}

func associatedData(integrationID uuid.UUID) []byte {
	return []byte("media-manager:integration-secret:v1:" + integrationID.String())
}

// Save encrypts value for one integration and points its secret_ref here.
func (service *Service) Save(ctx context.Context, integrationID uuid.UUID, value string, now time.Time) error {
	if service == nil {
		return ErrUnavailable
	}
	value = strings.TrimSpace(value)
	if !ValidSecret(value) {
		return ErrInvalidSecret
	}
	plaintext := []byte(value)
	defer clear(plaintext)
	nonce := make([]byte, service.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return errors.New("integration secret encryption is unavailable")
	}
	sealed := service.aead.Seal(nonce, nonce, plaintext, associatedData(integrationID))
	return service.store.SaveIntegrationSecret(ctx, integrationID, sealed, Reference(integrationID), now)
}

// Resolve decrypts the secret a sealed reference names. It is for server-side
// clients only.
func (service *Service) Resolve(ctx context.Context, reference string) (string, error) {
	if service == nil {
		return "", ErrUnavailable
	}
	id, err := uuid.Parse(strings.TrimPrefix(strings.TrimSpace(reference), ReferencePrefix))
	if err != nil || !IsReference(reference) {
		return "", errors.New("integration secret reference is invalid")
	}
	sealed, err := service.store.LoadIntegrationSecret(ctx, id)
	if err != nil {
		return "", err
	}
	size := service.aead.NonceSize()
	if len(sealed) < size+service.aead.Overhead() {
		return "", errors.New("integration secret could not be decrypted")
	}
	plaintext, err := service.aead.Open(nil, sealed[:size], sealed[size:], associatedData(id))
	if err != nil {
		return "", errors.New("integration secret could not be decrypted")
	}
	defer clear(plaintext)
	return string(plaintext), nil
}

// ValidSecret accepts one line of printable ASCII, at most 4096 characters.
func ValidSecret(value string) bool {
	return len(value) >= 1 && len(value) <= 4096 && strings.IndexFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) || r > 126 }) == -1
}
