package seerr

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	BaseURL       string
	APIKey        string
	AllowHTTP     bool
	Timeout       time.Duration
	ResponseLimit int64
}
type Client struct {
	base          *url.URL
	apiKey        string
	http          *http.Client
	responseLimit int64
}

type Identity struct {
	Namespace string `json:"namespace"`
	Value     string `json:"value"`
}
type Media struct {
	ID        int64  `json:"id"`
	TMDBID    *int64 `json:"tmdbId"`
	TVDBID    *int64 `json:"tvdbId"`
	IMDbID    string `json:"imdbId"`
	MediaType string `json:"mediaType"`
	Status    int    `json:"status"`
}
type Season struct {
	ID           int64 `json:"id"`
	SeasonNumber int16 `json:"seasonNumber"`
	Status       int   `json:"status"`
}
type Request struct {
	ID        int64     `json:"id"`
	Status    int       `json:"status"`
	Type      string    `json:"type"`
	Is4K      bool      `json:"is4k"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Media     Media     `json:"media"`
	Seasons   []Season  `json:"seasons"`
}
type ExternalIDs struct {
	IMDbID string `json:"imdbId"`
	TVDBID *int64 `json:"tvdbId"`
}
type Metadata struct {
	ID           int64       `json:"id"`
	Title        string      `json:"title"`
	Name         string      `json:"name"`
	ReleaseDate  string      `json:"releaseDate"`
	FirstAirDate string      `json:"firstAirDate"`
	ExternalIDs  ExternalIDs `json:"externalIds"`
}
type EpisodeMetadata struct {
	ID            int64  `json:"id"`
	EpisodeNumber int16  `json:"episodeNumber"`
	Name          string `json:"name"`
	AirDate       string `json:"airDate"`
}
type SeasonMetadata struct {
	ID           int64             `json:"id"`
	SeasonNumber int16             `json:"seasonNumber"`
	Episodes     []EpisodeMetadata `json:"episodes"`
}
type SearchResult struct {
	ID           int64  `json:"id"`
	MediaType    string `json:"mediaType"`
	Title        string `json:"title"`
	Name         string `json:"name"`
	ReleaseDate  string `json:"releaseDate"`
	FirstAirDate string `json:"firstAirDate"`
	Overview     string `json:"overview"`
	PosterPath   string `json:"posterPath"`
}
type SearchPage struct {
	Page         int            `json:"page"`
	TotalPages   int            `json:"totalPages"`
	TotalResults int            `json:"totalResults"`
	Results      []SearchResult `json:"results"`
}

type Identifier string

func (identifier *Identifier) UnmarshalJSON(value []byte) error {
	trimmed := strings.TrimSpace(string(value))
	if trimmed == "" || trimmed == "null" {
		*identifier = ""
		return nil
	}
	if strings.HasPrefix(trimmed, `"`) {
		var decoded string
		if err := json.Unmarshal(value, &decoded); err != nil {
			return err
		}
		trimmed = decoded
	}
	if _, err := strconv.ParseInt(trimmed, 10, 64); err != nil {
		return err
	}
	*identifier = Identifier(trimmed)
	return nil
}
func (identifier Identifier) String() string { return string(identifier) }

type Webhook struct {
	NotificationType string     `json:"notification_type"`
	RequestID        Identifier `json:"request_id"`
	MediaType        string     `json:"media_type"`
	MediaTMDBID      Identifier `json:"media_tmdbid"`
	MediaTVDBID      Identifier `json:"media_tvdbid"`
}

var (
	ErrInvalidConfig       = errors.New("invalid Seerr configuration")
	ErrUnauthorizedWebhook = errors.New("invalid Seerr webhook authorization")
	ErrInvalidWebhook      = errors.New("invalid Seerr webhook payload")
	ErrResponseTooLarge    = errors.New("Seerr response exceeds configured limit")
	ErrRequestNotFound     = errors.New("Seerr request was not found")
)

func New(config Config, transport http.RoundTripper) (*Client, error) {
	base, err := url.Parse(strings.TrimSpace(config.BaseURL))
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || strings.TrimSpace(config.APIKey) == "" || (base.Scheme != "https" && !(config.AllowHTTP && base.Scheme == "http")) {
		return nil, ErrInvalidConfig
	}
	if config.Timeout <= 0 {
		config.Timeout = 20 * time.Second
	}
	if config.ResponseLimit <= 0 {
		config.ResponseLimit = 4 << 20
	}
	if transport == nil {
		transport = http.DefaultTransport
	}
	base.Path = strings.TrimSuffix(base.Path, "/")
	return &Client{base: base, apiKey: config.APIKey, http: &http.Client{Transport: transport, Timeout: config.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("Seerr redirects are disabled") }}, responseLimit: config.ResponseLimit}, nil
}

func (client *Client) Request(ctx context.Context, id int64) (Request, error) {
	if id <= 0 {
		return Request{}, ErrInvalidConfig
	}
	endpoint := *client.base
	endpoint.Path = strings.TrimSuffix(client.base.Path, "/") + "/api/v1/request/" + strconv.FormatInt(id, 10)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return Request{}, err
	}
	request.Header.Set("X-Api-Key", client.apiKey)
	request.Header.Set("Accept", "application/json")
	response, err := client.http.Do(request)
	if err != nil {
		return Request{}, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return Request{}, ErrRequestNotFound
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Request{}, fmt.Errorf("Seerr returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, client.responseLimit+1))
	if err != nil {
		return Request{}, err
	}
	if int64(len(body)) > client.responseLimit {
		return Request{}, ErrResponseTooLarge
	}
	var result Request
	if err := json.Unmarshal(body, &result); err != nil {
		return Request{}, err
	}
	return result, nil
}

func (client *Client) Search(ctx context.Context, query string, page int) (SearchPage, error) {
	query = strings.TrimSpace(query)
	if len(query) < 2 || len(query) > 200 || page < 1 || page > 100 {
		return SearchPage{}, ErrInvalidConfig
	}
	endpoint := *client.base
	endpoint.Path = strings.TrimSuffix(client.base.Path, "/") + "/api/v1/search"
	values := endpoint.Query()
	values.Set("query", query)
	values.Set("page", strconv.Itoa(page))
	endpoint.RawQuery = values.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return SearchPage{}, err
	}
	request.Header.Set("X-Api-Key", client.apiKey)
	request.Header.Set("Accept", "application/json")
	response, err := client.http.Do(request)
	if err != nil {
		return SearchPage{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return SearchPage{}, fmt.Errorf("Seerr returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, client.responseLimit+1))
	if err != nil {
		return SearchPage{}, err
	}
	if int64(len(body)) > client.responseLimit {
		return SearchPage{}, ErrResponseTooLarge
	}
	var result SearchPage
	if err := json.Unmarshal(body, &result); err != nil {
		return SearchPage{}, err
	}
	return result, nil
}

func (client *Client) Metadata(ctx context.Context, mediaType string, tmdbID int64) (Metadata, error) {
	if tmdbID <= 0 || (mediaType != "movie" && mediaType != "tv") {
		return Metadata{}, ErrInvalidConfig
	}
	endpoint := *client.base
	endpoint.Path = strings.TrimSuffix(client.base.Path, "/") + "/api/v1/" + mediaType + "/" + strconv.FormatInt(tmdbID, 10)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return Metadata{}, err
	}
	request.Header.Set("X-Api-Key", client.apiKey)
	request.Header.Set("Accept", "application/json")
	response, err := client.http.Do(request)
	if err != nil {
		return Metadata{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Metadata{}, fmt.Errorf("Seerr returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, client.responseLimit+1))
	if err != nil {
		return Metadata{}, err
	}
	if int64(len(body)) > client.responseLimit {
		return Metadata{}, ErrResponseTooLarge
	}
	var result Metadata
	if err := json.Unmarshal(body, &result); err != nil {
		return Metadata{}, err
	}
	return result, nil
}

func (client *Client) SeasonMetadata(ctx context.Context, tmdbID int64, season int16) (SeasonMetadata, error) {
	if tmdbID <= 0 || season < 0 {
		return SeasonMetadata{}, ErrInvalidConfig
	}
	endpoint := *client.base
	endpoint.Path = strings.TrimSuffix(client.base.Path, "/") + "/api/v1/tv/" + strconv.FormatInt(tmdbID, 10) + "/season/" + strconv.Itoa(int(season))
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return SeasonMetadata{}, err
	}
	request.Header.Set("X-Api-Key", client.apiKey)
	request.Header.Set("Accept", "application/json")
	response, err := client.http.Do(request)
	if err != nil {
		return SeasonMetadata{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return SeasonMetadata{}, fmt.Errorf("Seerr returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, client.responseLimit+1))
	if err != nil {
		return SeasonMetadata{}, err
	}
	if int64(len(body)) > client.responseLimit {
		return SeasonMetadata{}, ErrResponseTooLarge
	}
	var result SeasonMetadata
	if err := json.Unmarshal(body, &result); err != nil {
		return SeasonMetadata{}, err
	}
	return result, nil
}

func (request Request) Identities() []Identity {
	var identities []Identity
	if request.Media.TMDBID != nil && *request.Media.TMDBID > 0 {
		identities = append(identities, Identity{Namespace: "tmdb", Value: strconv.FormatInt(*request.Media.TMDBID, 10)})
	}
	if request.Media.TVDBID != nil && *request.Media.TVDBID > 0 {
		identities = append(identities, Identity{Namespace: "tvdb", Value: strconv.FormatInt(*request.Media.TVDBID, 10)})
	}
	if strings.TrimSpace(request.Media.IMDbID) != "" {
		identities = append(identities, Identity{Namespace: "imdb", Value: request.Media.IMDbID})
	}
	return identities
}

func AuthenticateWebhook(authorization, secret string) error {
	const prefix = "Bearer "
	if !strings.HasPrefix(authorization, prefix) || len(secret) < 32 {
		return ErrUnauthorizedWebhook
	}
	actual, expected := sha256.Sum256([]byte(strings.TrimPrefix(authorization, prefix))), sha256.Sum256([]byte(secret))
	if subtle.ConstantTimeCompare(actual[:], expected[:]) != 1 {
		return ErrUnauthorizedWebhook
	}
	return nil
}

func ParseWebhook(body []byte) (Webhook, error) {
	if len(body) == 0 || len(body) > 256<<10 {
		return Webhook{}, ErrInvalidWebhook
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	var event Webhook
	if err := decoder.Decode(&event); err != nil {
		return Webhook{}, ErrInvalidWebhook
	}
	requestID, err := strconv.ParseInt(event.RequestID.String(), 10, 64)
	if err != nil || requestID <= 0 || strings.TrimSpace(event.NotificationType) == "" || (event.MediaType != "movie" && event.MediaType != "tv") {
		return Webhook{}, ErrInvalidWebhook
	}
	return event, nil
}

func ReceiptKey(body []byte) string {
	digest := sha256.Sum256(body)
	return fmt.Sprintf("%x", digest[:])
}
