package plex

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	BaseURL       string
	Token         string
	AllowHTTP     bool
	Timeout       time.Duration
	ResponseLimit int64
}
type Client struct {
	base          *url.URL
	token         string
	http          *http.Client
	responseLimit int64
}

type Section struct {
	Key   string `xml:"key,attr" json:"key"`
	Title string `xml:"title,attr" json:"title"`
	Type  string `xml:"type,attr" json:"type"`
}
type Part struct {
	File string `xml:"file,attr"`
}
type Media struct {
	Parts []Part `xml:"Part"`
}
type GUID struct {
	ID string `xml:"id,attr"`
}
type Item struct {
	RatingKey string  `xml:"ratingKey,attr"`
	Title     string  `xml:"title,attr"`
	GUID      string  `xml:"guid,attr"`
	Season    string  `xml:"parentIndex,attr"`
	Episode   string  `xml:"index,attr"`
	GUIDs     []GUID  `xml:"Guid"`
	Media     []Media `xml:"Media"`
}
type Expected struct {
	LibraryKey      string
	Title           string
	PlexVisiblePath string
	ProviderGUIDs   []string
}
type ExpectedSeries struct {
	LibraryKey       string
	Title            string
	PlexVisiblePaths []string
	ExpectedEpisodes map[string][]EpisodeLeaf
	ProviderGUIDs    []string
}
type EpisodeLeaf struct {
	Season  uint16
	Episode uint16
}
type Confirmation struct {
	RatingKey    string    `json:"rating_key"`
	Path         string    `json:"path"`
	ProviderGUID string    `json:"provider_guid"`
	ConfirmedAt  time.Time `json:"confirmed_at"`
}

var (
	ErrInvalidConfig    = errors.New("invalid Plex configuration")
	ErrResponseTooLarge = errors.New("Plex response exceeds configured limit")
	ErrNotConfirmed     = errors.New("expected media has not appeared in Plex")
)

func New(config Config, transport http.RoundTripper) (*Client, error) {
	base, err := url.Parse(strings.TrimSpace(config.BaseURL))
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Path != "" && base.Path != "/") || strings.TrimSpace(config.Token) == "" || (base.Scheme != "https" && !(config.AllowHTTP && base.Scheme == "http")) {
		return nil, ErrInvalidConfig
	}
	if config.Timeout <= 0 {
		config.Timeout = 20 * time.Second
	}
	if config.ResponseLimit <= 0 {
		config.ResponseLimit = 16 << 20
	}
	if transport == nil {
		transport = http.DefaultTransport
	}
	base.Path = ""
	return &Client{base: base, token: config.Token, http: &http.Client{Transport: transport, Timeout: config.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("Plex redirects are disabled") }}, responseLimit: config.ResponseLimit}, nil
}

func (client *Client) Sections(ctx context.Context) ([]Section, error) {
	body, err := client.get(ctx, "/library/sections", nil)
	if err != nil {
		return nil, err
	}
	var document struct {
		Sections []Section `xml:"Directory"`
	}
	if err := xml.Unmarshal(body, &document); err != nil {
		return nil, err
	}
	return document.Sections, nil
}

// Refresh requests the narrowest supported scan. A successful response only
// acknowledges the request; it never completes the Media Manager workflow.
func (client *Client) Refresh(ctx context.Context, libraryKey, plexVisiblePath string) error {
	if !numeric(libraryKey) || !filepath.IsAbs(plexVisiblePath) {
		return ErrInvalidConfig
	}
	_, err := client.get(ctx, "/library/sections/"+libraryKey+"/refresh", url.Values{"path": []string{plexVisiblePath}})
	return err
}

func (client *Client) Confirm(ctx context.Context, expected Expected) (Confirmation, error) {
	if !numeric(expected.LibraryKey) || !filepath.IsAbs(expected.PlexVisiblePath) || len(expected.ProviderGUIDs) == 0 {
		return Confirmation{}, ErrInvalidConfig
	}
	body, err := client.get(ctx, "/library/sections/"+expected.LibraryKey+"/all", url.Values{"title": []string{expected.Title}})
	if err != nil {
		return Confirmation{}, err
	}
	var document struct {
		Items []Item `xml:"Video"`
	}
	if err := xml.Unmarshal(body, &document); err != nil {
		return Confirmation{}, err
	}
	expectedPath := filepath.Clean(expected.PlexVisiblePath)
	for _, item := range document.Items {
		guid := matchingGUID(item, expected.ProviderGUIDs)
		if guid == "" {
			continue
		}
		for _, media := range item.Media {
			for _, part := range media.Parts {
				if filepath.Clean(part.File) == expectedPath {
					return Confirmation{RatingKey: item.RatingKey, Path: part.File, ProviderGUID: guid, ConfirmedAt: time.Now().UTC()}, nil
				}
			}
		}
	}
	return Confirmation{}, ErrNotConfirmed
}

// ConfirmSeries verifies the series identity on the show and every expected
// episode path on its leaves. A title match by itself is never confirmation.
func (client *Client) ConfirmSeries(ctx context.Context, expected ExpectedSeries) ([]Confirmation, error) {
	if !numeric(expected.LibraryKey) || len(expected.PlexVisiblePaths) == 0 || len(expected.ProviderGUIDs) == 0 {
		return nil, ErrInvalidConfig
	}
	for _, path := range expected.PlexVisiblePaths {
		if !filepath.IsAbs(path) {
			return nil, ErrInvalidConfig
		}
	}
	body, err := client.get(ctx, "/library/sections/"+expected.LibraryKey+"/all", url.Values{"title": []string{expected.Title}, "type": []string{"2"}})
	if err != nil {
		return nil, err
	}
	var shows struct {
		Items []Item `xml:"Directory"`
	}
	if err := xml.Unmarshal(body, &shows); err != nil {
		return nil, err
	}
	for _, show := range shows.Items {
		guid := matchingGUID(show, expected.ProviderGUIDs)
		if guid == "" || !numeric(show.RatingKey) {
			continue
		}
		leavesBody, err := client.get(ctx, "/library/metadata/"+show.RatingKey+"/allLeaves", nil)
		if err != nil {
			return nil, err
		}
		var leaves struct {
			Items []Item `xml:"Video"`
		}
		if err := xml.Unmarshal(leavesBody, &leaves); err != nil {
			return nil, err
		}
		available := make(map[string]map[EpisodeLeaf]string)
		availablePaths := make(map[string]string)
		for _, leaf := range leaves.Items {
			season, seasonErr := strconv.ParseUint(leaf.Season, 10, 16)
			episode, episodeErr := strconv.ParseUint(leaf.Episode, 10, 16)
			for _, media := range leaf.Media {
				for _, part := range media.Parts {
					if filepath.IsAbs(part.File) {
						path := filepath.Clean(part.File)
						availablePaths[path] = leaf.RatingKey
						if seasonErr == nil && episodeErr == nil && season <= 65535 && episode > 0 && episode <= 65535 {
							if available[path] == nil {
								available[path] = make(map[EpisodeLeaf]string)
							}
							available[path][EpisodeLeaf{Season: uint16(season), Episode: uint16(episode)}] = leaf.RatingKey
						}
					}
				}
			}
		}
		confirmations := make([]Confirmation, 0, len(expected.PlexVisiblePaths))
		for _, wanted := range expected.PlexVisiblePaths {
			clean := filepath.Clean(wanted)
			expectedForPath := expected.ExpectedEpisodes[clean]
			itemKey := availablePaths[clean]
			if itemKey == "" {
				confirmations = nil
				break
			}
			for _, wantedEpisode := range expectedForPath {
				candidate, exists := available[clean][wantedEpisode]
				if !exists {
					confirmations = nil
					break
				}
				itemKey = candidate
			}
			if confirmations == nil && len(expectedForPath) > 0 {
				break
			}
			confirmations = append(confirmations, Confirmation{RatingKey: itemKey, Path: clean, ProviderGUID: guid, ConfirmedAt: time.Now().UTC()})
		}
		if len(confirmations) == len(expected.PlexVisiblePaths) {
			return confirmations, nil
		}
	}
	return nil, ErrNotConfirmed
}

func (client *Client) WaitForConfirmation(ctx context.Context, expected Expected, interval time.Duration) (Confirmation, error) {
	if interval <= 0 {
		interval = 3 * time.Second
	}
	for {
		confirmation, err := client.Confirm(ctx, expected)
		if err == nil {
			return confirmation, nil
		}
		if !errors.Is(err, ErrNotConfirmed) {
			return Confirmation{}, err
		}
		timer := time.NewTimer(interval)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return Confirmation{}, ctx.Err()
		}
	}
}

func (client *Client) get(ctx context.Context, path string, query url.Values) ([]byte, error) {
	if !strings.HasPrefix(path, "/library/") || strings.Contains(path, "..") || strings.Contains(path, "//") {
		return nil, ErrInvalidConfig
	}
	endpoint := *client.base
	endpoint.Path = path
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("X-Plex-Token", client.token)
	request.Header.Set("Accept", "application/xml")
	request.Header.Set("X-Plex-Product", "Media Manager")
	request.Header.Set("X-Plex-Client-Identifier", "media-manager")
	response, err := client.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("Plex returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, client.responseLimit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > client.responseLimit {
		return nil, ErrResponseTooLarge
	}
	return body, nil
}

func matchingGUID(item Item, expected []string) string {
	available := append([]GUID{{ID: item.GUID}}, item.GUIDs...)
	for _, wanted := range expected {
		for _, current := range available {
			if strings.EqualFold(strings.TrimSpace(wanted), strings.TrimSpace(current.ID)) {
				return current.ID
			}
		}
	}
	return ""
}

func numeric(value string) bool {
	number, err := strconv.ParseInt(value, 10, 64)
	return err == nil && number > 0
}
