package backups

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/media-manager/media-manager/internal/integrationsecrets"
)

// KeyFileReference names the file that holds the backup key when it is not
// given directly in BACKUP_ENCRYPTION_KEY. The Cosmos market installer points
// it at the API's private state volume, and Media Manager creates the key
// there on its first start, so backups work without the owner generating a
// key by hand.
const KeyFileReference = KeyReference + "_FILE"

// ErrKeyFileLost means the key file is missing although backups made with an
// earlier key are recorded. A new key could not open them, so none is
// created: the owner restores the old key file.
var ErrKeyFileLost = errors.New("the backup key file is missing, but backups made with an earlier key are recorded; restore the key file instead of creating a new key")

// EnsureKeyFile returns the backup key stored at path. When no file exists
// and no backup was made with an earlier key, it creates a random base64
// 32-byte key there (mode 0600) and reports created=true. An existing file is
// never replaced.
func EnsureKeyFile(path string, backupsExist func() (bool, error)) (key string, created bool, err error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", false, nil
	}
	body, err := os.ReadFile(path)
	if err == nil {
		key = strings.TrimSpace(string(body))
		if _, parseErr := ParseKey(key); parseErr != nil {
			return "", false, fmt.Errorf("the backup key file %s is not a valid key; restore it", path)
		}
		return key, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", false, fmt.Errorf("read the backup key file: %w", err)
	}
	if backupsExist != nil {
		exists, err := backupsExist()
		if err != nil {
			return "", false, fmt.Errorf("check for earlier backups before creating a key: %w", err)
		}
		if exists {
			return "", false, ErrKeyFileLost
		}
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", false, errors.New("could not generate a backup key")
	}
	key = base64.StdEncoding.EncodeToString(raw)
	if err := integrationsecrets.WriteNewFile(path, key+"\n"); err != nil {
		if errors.Is(err, os.ErrExist) {
			return EnsureKeyFile(path, nil)
		}
		return "", false, fmt.Errorf("create the backup key file: %w", err)
	}
	return key, true, nil
}
