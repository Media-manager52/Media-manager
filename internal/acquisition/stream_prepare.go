package acquisition

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/altmount"
	"github.com/media-manager/media-manager/internal/postgres"
	"golang.org/x/sync/singleflight"
)

// StreamPreparer turns a viewer's release choice into a file AltMount can
// serve right away. Media Manager fetches the NZB (with the indexer's
// credentials) and hands it to AltMount (with AltMount's API key). The client
// only learns the prepared file's path on the read-only mount, so it never
// holds an indexer key, an AltMount key or AltMount's download key.
type StreamPreparer struct {
	Browse *BrowseService
	// APIKey is AltMount's API key; its streaming endpoint accepts nothing
	// else.
	APIKey string
	// Category is the AltMount queue category for viewer playback.
	Category  string
	Timeout   time.Duration
	Transport http.RoundTripper
	flights   singleflight.Group
}

var (
	// ErrStreamPreparationUnavailable means streaming preparation is not
	// configured: no AltMount API key or no enabled AltMount download client.
	ErrStreamPreparationUnavailable = errors.New("stream preparation is not configured")
	// ErrStreamRetrieval wraps a failure to get the NZB from the indexer.
	ErrStreamRetrieval = errors.New("the selected release could not be retrieved")
	// ErrStreamReleaseDead means the release can never play: its articles
	// are gone or it holds no playable file.
	ErrStreamReleaseDead = altmount.ErrStreamArticlesMissing
	// ErrStreamAmbiguous means AltMount found zero or several videos.
	ErrStreamAmbiguous = altmount.ErrStreamAmbiguous
)

// PreparedStream is the prepared file: its path on the read-only AltMount
// mount, and nothing that grants access to AltMount.
type PreparedStream = altmount.PreparedStream

// DefaultStreamCategory keeps viewer playback apart from library acquisitions
// in AltMount. It is the category the addon used before preparation moved here.
const DefaultStreamCategory = "nuvio"

// Configured reports whether Prepare can work at all.
func (p *StreamPreparer) Configured() bool {
	return p != nil && p.Browse != nil && p.APIKey != ""
}

// Prepare retrieves the chosen NZB and has AltMount prepare its one video.
// Concurrent calls for the same choice share one retrieval and upload.
func (p *StreamPreparer) Prepare(ctx context.Context, token string) (PreparedStream, error) {
	if !p.Configured() {
		return PreparedStream{}, ErrStreamPreparationUnavailable
	}
	result, err, _ := p.flights.Do(token, func() (any, error) {
		// One viewer closing the request must not abort an upload that
		// another request for the same choice is waiting on.
		work, cancel := context.WithTimeout(context.WithoutCancel(ctx), p.timeout()+15*time.Second)
		defer cancel()
		return p.prepare(work, token)
	})
	if err != nil {
		return PreparedStream{}, err
	}
	return result.(PreparedStream), nil
}

func (p *StreamPreparer) prepare(ctx context.Context, token string) (PreparedStream, error) {
	client, err := p.client(ctx)
	if err != nil {
		return PreparedStream{}, err
	}
	body, filename, in, err := p.Browse.Download(ctx, token)
	if err != nil {
		if errors.Is(err, ErrBrowseExpired) {
			return PreparedStream{}, err
		}
		return PreparedStream{}, fmt.Errorf("%w: %w", ErrStreamRetrieval, err)
	}
	category := p.Category
	if category == "" {
		category = DefaultStreamCategory
	}
	return client.Prepare(ctx, altmount.StreamRequest{Filename: filename, NZB: body, Category: category, Timeout: p.timeout(), Season: int(in.Season), Episode: int(in.Episode)})
}

func (p *StreamPreparer) timeout() time.Duration {
	if p.Timeout > 0 {
		return p.Timeout
	}
	return 100 * time.Second
}

// client reads the enabled (preferably default) AltMount download client on
// every call, so a settings change applies without a restart.
func (p *StreamPreparer) client(ctx context.Context) (*altmount.StreamClient, error) {
	integrations, err := p.Browse.Search.Store.ListIntegrations(ctx)
	if err != nil {
		return nil, err
	}
	var chosen *postgres.Integration
	for index, integration := range integrations {
		if integration.Kind != "altmount" || !integration.Enabled {
			continue
		}
		if chosen == nil || integration.Default && !chosen.Default {
			chosen = &integrations[index]
		}
	}
	if chosen == nil {
		return nil, ErrStreamPreparationUnavailable
	}
	var config altMountConfig
	if err := json.Unmarshal(chosen.Config, &config); err != nil {
		return nil, err
	}
	return altmount.NewStreamClient(config.BaseURL, p.APIKey, config.AllowHTTP, p.Transport)
}
