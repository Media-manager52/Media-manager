package integrationsecrets

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// KeyFileReference names the file that holds the server key when the key is
// not given directly in the environment. An installer that cannot generate a
// key itself points it at a private, persistent folder, and Media Manager
// creates the key there on its first start. The Cosmos market installer needs
// this: it generates only four passwords, and all of them are already in use.
const KeyFileReference = KeyReference + "_FILE"

// ErrKeyFileLost means the key file is missing although secrets sealed with
// an earlier key are stored. A new key could never open them, so none is
// created: the owner restores the old key file (or enters the secrets again
// after setting a new key on purpose).
var ErrKeyFileLost = errors.New("the secret encryption key file is missing, but secrets saved with an earlier key are stored; restore the key file instead of creating a new key")

// EnsureKeyFile returns the key stored at path. When no file exists yet and
// nothing has been sealed with an earlier key, it first creates a random
// 64-character hexadecimal key there, readable only by this user, and reports
// created=true. An existing file is never replaced, even when it is empty or
// invalid: replacing it would make every saved secret unreadable.
func EnsureKeyFile(path string, sealedSecretsExist func() (bool, error)) (key string, created bool, err error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", false, nil
	}
	body, err := os.ReadFile(path)
	if err == nil {
		key = strings.TrimSpace(string(body))
		if key == "" {
			return "", false, fmt.Errorf("the secret encryption key file %s is empty; restore it or remove it so a new key can be created", path)
		}
		return key, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", false, fmt.Errorf("read the secret encryption key file: %w", err)
	}
	if sealedSecretsExist != nil {
		exists, err := sealedSecretsExist()
		if err != nil {
			return "", false, fmt.Errorf("check for saved secrets before creating a key: %w", err)
		}
		if exists {
			return "", false, ErrKeyFileLost
		}
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", false, errors.New("could not generate a secret encryption key")
	}
	key = hex.EncodeToString(raw)
	if err := WriteNewFile(path, key+"\n"); err != nil {
		if errors.Is(err, os.ErrExist) {
			// Another start won the race; use the key it wrote.
			return EnsureKeyFile(path, nil)
		}
		return "", false, fmt.Errorf("create the secret encryption key file: %w", err)
	}
	return key, true, nil
}

// WriteNewFile writes content to path only if path does not exist yet
// (os.ErrExist otherwise), readable only by this user. The content is fully
// written and synced to a temporary file first, then linked into place, so a
// crash never leaves a partial key behind.
func WriteNewFile(path, content string) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".secret-encryption-key-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.WriteString(content); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Link(temporaryPath, path); err != nil {
		return err
	}
	if handle, err := os.Open(directory); err == nil {
		_ = handle.Sync()
		handle.Close()
	}
	return nil
}
