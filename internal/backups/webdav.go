package backups

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type webDAVStore struct {
	base     *url.URL
	username string
	password string
	prefix   string
	client   *http.Client
}

func newWebDAVStore(config WebDAVConfig) (*webDAVStore, error) {
	raw := strings.TrimSpace(config.URL)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("off-site WebDAV URL is invalid")
	}
	if parsed.Scheme != "https" && !(config.AllowHTTP && parsed.Scheme == "http") {
		return nil, errors.New("off-site WebDAV URL must use HTTPS")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	return &webDAVStore{
		base:     parsed,
		username: strings.TrimSpace(config.Username),
		password: config.Password,
		client:   offsiteHTTPClient(config.Client, 15*time.Minute),
	}, nil
}

func (store *webDAVStore) objectURL(key string) string {
	copied := *store.base
	copied.Path = strings.TrimRight(store.base.Path, "/") + "/" + key
	return copied.String()
}

func (store *webDAVStore) Put(ctx context.Context, key string, source io.Reader, size int64, _ string) error {
	if size < 1 {
		return errors.New("off-site backup size is invalid")
	}
	if err := store.ensureCollections(ctx); err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, store.objectURL(key), source)
	if err != nil {
		return err
	}
	request.ContentLength = size
	request.Header.Set("Content-Type", "application/octet-stream")
	store.authorize(request)
	response, err := store.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusNoContent {
		return offsiteResponseError("WebDAV upload", response)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	return nil
}

func (store *webDAVStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, store.objectURL(key), nil)
	if err != nil {
		return nil, err
	}
	store.authorize(request)
	response, err := store.client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode == http.StatusNotFound {
		response.Body.Close()
		return nil, ErrOffsiteObjectNotFound
	}
	if response.StatusCode != http.StatusOK {
		defer response.Body.Close()
		return nil, offsiteResponseError("WebDAV download", response)
	}
	return response.Body, nil
}

// ensureCollections creates every prefix segment in order. WebDAV servers
// differ on whether a PUT can create parent collections; creating them first
// keeps uploads portable across NAS implementations.
func (store *webDAVStore) ensureCollections(ctx context.Context) error {
	if store.prefix == "" {
		return nil
	}
	segments := strings.Split(store.prefix, "/")
	for index := range segments {
		relative := strings.Join(segments[:index+1], "/")
		request, err := http.NewRequestWithContext(ctx, "MKCOL", store.objectURL(relative), nil)
		if err != nil {
			return err
		}
		store.authorize(request)
		response, err := store.client.Do(request)
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		response.Body.Close()
		switch response.StatusCode {
		case http.StatusCreated, http.StatusOK, http.StatusNoContent, http.StatusMethodNotAllowed:
			continue
		default:
			return fmt.Errorf("WebDAV upload could not create the backup folder (HTTP %d)", response.StatusCode)
		}
	}
	return nil
}

func (store *webDAVStore) authorize(request *http.Request) {
	if store.username != "" || store.password != "" {
		request.SetBasicAuth(store.username, store.password)
	}
}
