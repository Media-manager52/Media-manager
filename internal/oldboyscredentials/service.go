package oldboyscredentials

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
)

const KeyReference = "OLDBOYS_CREDENTIAL_ENCRYPTION_KEY"
const associatedData = "media-manager:oldboys-credentials:v1"

var ErrInvalidCredentials = errors.New("enter all three OldBoys credentials as tokens, without spaces or URLs")

type Credentials struct {
	APIToken string `json:"api_token"`
	PassKey  string `json:"pass_key"`
	RSSKey   string `json:"rss_key"`
}

type Status struct {
	Available  bool       `json:"available"`
	Configured bool       `json:"configured"`
	UpdatedAt  *time.Time `json:"updated_at,omitempty"`
}

type Store interface {
	OldBoysCredentialStatus(context.Context) (*time.Time, error)
	SaveOldBoysCredentials(context.Context, []byte) (time.Time, error)
	LoadOldBoysCredentials(context.Context) ([]byte, error)
}

type Service struct {
	store Store
	aead  cipher.AEAD
}

// New requires an independent 256-bit server key. Missing keys disable saving;
// invalid keys are configuration errors and never fall back to plaintext.
func New(store Store, key string) (*Service, error) {
	decoded, err := hex.DecodeString(key)
	if err != nil || len(decoded) != 32 || store == nil {
		return nil, errors.New("OldBoys credential encryption requires a store and a 64-character hexadecimal server key")
	}
	block, err := aes.NewCipher(decoded)
	if err != nil {
		return nil, errors.New("OldBoys credential encryption is unavailable")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("OldBoys credential encryption is unavailable")
	}
	return &Service{store: store, aead: aead}, nil
}

func (service *Service) Status(ctx context.Context) (Status, error) {
	updated, err := service.store.OldBoysCredentialStatus(ctx)
	return Status{Available: true, Configured: updated != nil, UpdatedAt: updated}, err
}

func (service *Service) Save(ctx context.Context, credentials Credentials) (Status, error) {
	if !validToken(credentials.APIToken) || !validToken(credentials.PassKey) || !validToken(credentials.RSSKey) {
		return Status{}, ErrInvalidCredentials
	}
	plaintext, err := json.Marshal(credentials)
	if err != nil {
		return Status{}, ErrInvalidCredentials
	}
	defer clear(plaintext)
	nonce := make([]byte, service.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return Status{}, errors.New("OldBoys credential encryption is unavailable")
	}
	sealed := service.aead.Seal(nonce, nonce, plaintext, []byte(associatedData))
	updated, err := service.store.SaveOldBoysCredentials(ctx, sealed)
	if err != nil {
		return Status{}, errors.New("could not save OldBoys credentials")
	}
	return Status{Available: true, Configured: true, UpdatedAt: &updated}, nil
}

// Load is for server-side provider clients only; no HTTP route returns it.
func (service *Service) Load(ctx context.Context) (Credentials, error) {
	sealed, err := service.store.LoadOldBoysCredentials(ctx)
	if err != nil || len(sealed) < service.aead.NonceSize()+service.aead.Overhead() {
		return Credentials{}, errors.New("OldBoys credentials are unavailable")
	}
	size := service.aead.NonceSize()
	plaintext, err := service.aead.Open(nil, sealed[:size], sealed[size:], []byte(associatedData))
	if err != nil {
		return Credentials{}, errors.New("OldBoys credentials could not be decrypted")
	}
	defer clear(plaintext)
	var credentials Credentials
	if err := json.Unmarshal(plaintext, &credentials); err != nil || !validToken(credentials.APIToken) || !validToken(credentials.PassKey) || !validToken(credentials.RSSKey) {
		return Credentials{}, errors.New("OldBoys credentials could not be decoded")
	}
	return credentials, nil
}

func validToken(value string) bool {
	return len(value) >= 1 && len(value) <= 4096 && !strings.Contains(value, "://") && !strings.ContainsAny(value, "?#&") && strings.IndexFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) || r > 126 }) == -1
}
