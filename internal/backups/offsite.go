package backups

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"
)

var offsiteSegmentPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// ErrOffsiteNotConfigured means no off-site destination is configured.
var ErrOffsiteNotConfigured = errors.New("off-site backup is not configured")

// ErrOffsiteObjectNotFound means the destination does not hold the requested
// backup object.
var ErrOffsiteObjectNotFound = errors.New("off-site backup object was not found")

// OffsiteStore is a durable destination for the already-encrypted backup
// artifact. Implementations never see plaintext database dumps.
type OffsiteStore interface {
	Put(ctx context.Context, key string, source io.Reader, size int64, sha256Hex string) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
}

// OffsiteConfig selects one destination. S3 covers S3-compatible object
// storage; WebDAV covers hosted folders and NAS shares.
type OffsiteConfig struct {
	Kind   string
	Prefix string
	S3     S3Config
	WebDAV WebDAVConfig
}

// S3Config is a path-style S3-compatible destination.
type S3Config struct {
	Endpoint  string
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
	AllowHTTP bool
	Client    *http.Client
}

// WebDAVConfig is a WebDAV collection. The username and password are sent with
// HTTP Basic authentication over HTTPS (or HTTP only when AllowHTTP is set).
type WebDAVConfig struct {
	URL       string
	Username  string
	Password  string
	AllowHTTP bool
	Client    *http.Client
}

// NewOffsite validates cfg and returns the selected destination. An empty kind
// returns (nil, "", nil): off-site copying is disabled.
func NewOffsite(cfg OffsiteConfig) (OffsiteStore, string, error) {
	kind := strings.ToLower(strings.TrimSpace(cfg.Kind))
	if kind == "" {
		return nil, "", nil
	}
	prefix, err := NormalizeOffsitePrefix(cfg.Prefix)
	if err != nil {
		return nil, "", err
	}
	switch kind {
	case "s3":
		store, err := newS3Store(cfg.S3)
		if err != nil {
			return nil, "", err
		}
		store.prefix = prefix
		return store, kind, nil
	case "webdav":
		store, err := newWebDAVStore(cfg.WebDAV)
		if err != nil {
			return nil, "", err
		}
		store.prefix = prefix
		return store, kind, nil
	default:
		return nil, "", fmt.Errorf("unsupported off-site backup kind %q", cfg.Kind)
	}
}

// NormalizeOffsitePrefix makes a relative object prefix. An empty prefix is
// valid. Absolute paths, parent traversal and backslashes are refused so a
// configuration mistake cannot escape the destination root.
func NormalizeOffsitePrefix(prefix string) (string, error) {
	prefix = strings.TrimSpace(strings.ReplaceAll(prefix, "\\", "/"))
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return "", nil
	}
	cleaned := path.Clean(prefix)
	if cleaned == "." {
		return "", nil
	}
	if strings.HasPrefix(cleaned, "/") || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
		return "", errors.New("off-site backup prefix must stay inside the destination")
	}
	for _, segment := range strings.Split(cleaned, "/") {
		if segment == "" || segment == "." || segment == ".." || !offsiteSegmentPattern.MatchString(segment) {
			return "", errors.New("off-site backup prefix may contain only letters, digits, dot, underscore and dash")
		}
	}
	return cleaned, nil
}

// ValidOffsiteKey reports whether key is a relative object path made only of
// safe segments. It is used before reading a key persisted in the database.
func ValidOffsiteKey(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "\\") {
		return false
	}
	for _, segment := range strings.Split(key, "/") {
		if segment == "" || segment == "." || segment == ".." || !offsiteSegmentPattern.MatchString(segment) {
			return false
		}
	}
	return true
}

// OffsiteKey joins a normalized prefix and a validated backup filename.
func OffsiteKey(prefix, filename string) (string, error) {
	if filename == "" || path.Base(filename) != filename || strings.Contains(filename, "\\") || strings.Contains(filename, "..") || !offsiteSegmentPattern.MatchString(filename) {
		return "", errors.New("invalid off-site backup filename")
	}
	normalized, err := NormalizeOffsitePrefix(prefix)
	if err != nil {
		return "", err
	}
	if normalized == "" {
		return filename, nil
	}
	return normalized + "/" + filename, nil
}

func offsiteHTTPClient(client *http.Client, timeout time.Duration) *http.Client {
	if client != nil {
		return client
	}
	return &http.Client{Timeout: timeout}
}
