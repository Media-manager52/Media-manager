package metadataproviders

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/seerr"
	"github.com/media-manager/media-manager/internal/catalog"
)

// ErrTMDBNotConfigured means no TMDB read access token is set, neither in
// TMDB_ACCESS_TOKEN nor as a TMDB connection on the website.
var ErrTMDBNotConfigured = errors.New("TMDB is not configured")

// TMDBSource is the TMDB client Media Manager currently has. The token comes
// from TMDB_ACCESS_TOKEN when set; otherwise from the TMDB connection saved on
// the website, read through Resolve. A token saved on the website therefore
// takes effect without restarting the API: the source re-reads it at most
// every refreshInterval, and at once after Invalidate.
type TMDBSource struct {
	// EnvironmentToken, when set, always wins.
	EnvironmentToken string
	// Resolve returns the token of the enabled default TMDB connection, or ""
	// when there is none.
	Resolve   func(context.Context) (string, error)
	Transport http.RoundTripper
	// BaseURL and AllowHTTP are for tests.
	BaseURL   string
	AllowHTTP bool

	mutex     sync.Mutex
	checkedAt time.Time
	digest    [32]byte
	client    *TMDBClient
}

const tmdbSourceRefreshInterval = 15 * time.Second

// Invalidate makes the next call re-read the token.
func (source *TMDBSource) Invalidate() {
	if source == nil {
		return
	}
	source.mutex.Lock()
	source.checkedAt = time.Time{}
	source.mutex.Unlock()
}

// Client returns the current client, or ErrTMDBNotConfigured.
func (source *TMDBSource) Client(ctx context.Context) (*TMDBClient, error) {
	if source == nil {
		return nil, ErrTMDBNotConfigured
	}
	source.mutex.Lock()
	defer source.mutex.Unlock()
	if !source.checkedAt.IsZero() && time.Since(source.checkedAt) < tmdbSourceRefreshInterval {
		if source.client == nil {
			return nil, ErrTMDBNotConfigured
		}
		return source.client, nil
	}
	token := source.EnvironmentToken
	if token == "" && source.Resolve != nil {
		resolved, err := source.Resolve(ctx)
		if err != nil {
			// Keep the last good client through a transient read failure.
			if source.client != nil {
				return source.client, nil
			}
			return nil, ErrTMDBNotConfigured
		}
		token = resolved
	}
	source.checkedAt = time.Now()
	if token == "" {
		source.client, source.digest = nil, [32]byte{}
		return nil, ErrTMDBNotConfigured
	}
	digest := sha256.Sum256([]byte(token))
	if source.client != nil && digest == source.digest {
		return source.client, nil
	}
	client, err := NewTMDB(TMDBConfig{AccessToken: token, BaseURL: source.BaseURL, AllowHTTP: source.AllowHTTP}, source.Transport)
	if err != nil {
		source.client, source.digest = nil, [32]byte{}
		return nil, ErrTMDBNotConfigured
	}
	source.client, source.digest = client, digest
	return client, nil
}

// MetadataSourceLabel labels identities proven by TMDB queried directly.
func (source *TMDBSource) MetadataSourceLabel() string { return "tmdb_metadata" }

// Configured reports whether a TMDB token is currently available.
func (source *TMDBSource) Configured(ctx context.Context) bool {
	_, err := source.Client(ctx)
	return err == nil
}

// Lookup enriches metadata like TMDBClient.Lookup.
func (source *TMDBSource) Lookup(ctx context.Context, kind catalog.MediaKind, identities []catalog.ExternalIdentity, now time.Time) (Contribution, error) {
	client, err := source.Client(ctx)
	if err != nil {
		return Contribution{}, err
	}
	return client.Lookup(ctx, kind, identities, now)
}

// SeasonEpisodeRuntimes is TMDBClient.SeasonEpisodeRuntimes.
func (source *TMDBSource) SeasonEpisodeRuntimes(ctx context.Context, seriesID string, season uint16) (map[string]uint16, error) {
	client, err := source.Client(ctx)
	if err != nil {
		return nil, err
	}
	return client.SeasonEpisodeRuntimes(ctx, seriesID, season)
}

// Search is TMDBClient.Search.
func (source *TMDBSource) Search(ctx context.Context, query string, page int) (seerr.SearchPage, error) {
	client, err := source.Client(ctx)
	if err != nil {
		return seerr.SearchPage{}, err
	}
	return client.Search(ctx, query, page)
}

// Metadata is TMDBClient.Metadata.
func (source *TMDBSource) Metadata(ctx context.Context, mediaType string, tmdbID int64) (seerr.Metadata, error) {
	client, err := source.Client(ctx)
	if err != nil {
		return seerr.Metadata{}, err
	}
	return client.Metadata(ctx, mediaType, tmdbID)
}

// SeasonMetadata is TMDBClient.SeasonMetadata.
func (source *TMDBSource) SeasonMetadata(ctx context.Context, tmdbID int64, season int16) (seerr.SeasonMetadata, error) {
	client, err := source.Client(ctx)
	if err != nil {
		return seerr.SeasonMetadata{}, err
	}
	return client.SeasonMetadata(ctx, tmdbID, season)
}
