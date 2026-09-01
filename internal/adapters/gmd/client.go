package gmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/media-manager/media-manager/internal/catalog"
)

const (
	defaultResponseLimit = int64(2 << 20)
	maximumCandidates    = 12
)

var ErrInvalidConfig = errors.New("invalid GMD configuration")

type Config struct {
	BaseURL       string
	AllowHTTP     bool
	Timeout       time.Duration
	ResponseLimit int64
}

type Client struct {
	base          *url.URL
	http          *http.Client
	responseLimit int64
}

type SearchItem struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	OriginalName string `json:"original_name"`
}

type searchResponse struct {
	Items []SearchItem `json:"items"`
}

type Title struct {
	Name           string  `json:"name"`
	OriginalName   string  `json:"original_name"`
	Overview       string  `json:"overview"`
	Language       string  `json:"language"`
	Format         string  `json:"format"`
	Status         string  `json:"status"`
	RuntimeMinutes *uint16 `json:"runtime_minutes"`
	PosterURL      string  `json:"poster_url"`
	BackdropURL    string  `json:"backdrop_url"`
	Confidence     float64 `json:"confidence"`
}

type ExternalID struct {
	Source string `json:"source"`
	ID     string `json:"id"`
	URL    string `json:"url"`
}

type Alias struct {
	Name     string `json:"name"`
	Language string `json:"language"`
	Source   string `json:"source"`
}

type Network struct {
	Name    string `json:"name"`
	Country string `json:"country"`
	Type    string `json:"type"`
	Source  string `json:"source"`
}

type Evidence struct {
	Source string `json:"source"`
}

type Event struct {
	ID            string  `json:"id"`
	Type          string  `json:"type"`
	Date          string  `json:"date"`
	SeasonNumber  *uint16 `json:"season_number"`
	EpisodeNumber *uint16 `json:"episode_number"`
	Country       string  `json:"country"`
	Network       string  `json:"network"`
	Confidence    float64 `json:"confidence"`
	DateConflict  bool    `json:"date_conflict"`
}

type Detail struct {
	EventID     string       `json:"event_id"`
	Title       Title        `json:"title"`
	Countries   []string     `json:"countries"`
	Genres      []string     `json:"genres"`
	Networks    []Network    `json:"networks"`
	ExternalIDs []ExternalID `json:"external_ids"`
	Evidence    []Evidence   `json:"evidence"`
	Aliases     []Alias      `json:"aliases"`
	Events      []Event      `json:"events"`
}

func New(config Config, transport http.RoundTripper) (*Client, error) {
	base, err := url.Parse(strings.TrimSpace(config.BaseURL))
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || base.Scheme != "https" && !(config.AllowHTTP && base.Scheme == "http") {
		return nil, ErrInvalidConfig
	}
	if config.Timeout <= 0 {
		config.Timeout = 10 * time.Second
	}
	if config.Timeout > 30*time.Second {
		return nil, ErrInvalidConfig
	}
	if config.ResponseLimit <= 0 {
		config.ResponseLimit = defaultResponseLimit
	}
	if config.ResponseLimit > 8<<20 {
		return nil, ErrInvalidConfig
	}
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &Client{
		base: base,
		http: &http.Client{
			Transport: transport,
			Timeout:   config.Timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		responseLimit: config.ResponseLimit,
	}, nil
}

func (client *Client) Lookup(ctx context.Context, title string, identities []catalog.ExternalIdentity) (Detail, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	title = strings.TrimSpace(title)
	targets := map[string]string{}
	for _, identity := range identities {
		if identity.State != catalog.IdentityVerified {
			continue
		}
		namespace := strings.ToLower(strings.TrimSpace(identity.Namespace))
		if namespace == "tmdb" || namespace == "tvdb" || namespace == "imdb" {
			targets[namespace] = strings.TrimSpace(identity.Value)
		}
	}
	if title == "" || len(title) > 500 || len(targets) == 0 {
		return Detail{}, false, ErrInvalidConfig
	}
	endpoint := client.endpoint("/api/v1/search")
	query := endpoint.Query()
	query.Set("q", title)
	query.Set("limit", "40")
	endpoint.RawQuery = query.Encode()
	var search searchResponse
	if err := client.get(ctx, endpoint, &search); err != nil {
		return Detail{}, false, err
	}
	if len(search.Items) > 40 {
		return Detail{}, false, errors.New("GMD search response exceeded its requested bound")
	}
	normalizedTitle := normalized(title)
	sort.SliceStable(search.Items, func(i, j int) bool {
		left := normalized(search.Items[i].Name) == normalizedTitle || normalized(search.Items[i].OriginalName) == normalizedTitle
		right := normalized(search.Items[j].Name) == normalizedTitle || normalized(search.Items[j].OriginalName) == normalizedTitle
		return left && !right
	})
	var matched *Detail
	inspected := 0
	for _, candidate := range search.Items {
		if inspected >= maximumCandidates {
			break
		}
		if !validTitleID(candidate.ID) {
			continue
		}
		inspected++
		endpoint = client.endpoint("/api/v1/titles/" + url.PathEscape(candidate.ID))
		var detail Detail
		if err := client.get(ctx, endpoint, &detail); err != nil {
			return Detail{}, false, err
		}
		if !matchesExactIdentity(detail.ExternalIDs, targets) {
			continue
		}
		if matched != nil {
			return Detail{}, false, errors.New("GMD returned more than one exact external-identity match")
		}
		copy := detail
		matched = &copy
	}
	if matched == nil {
		return Detail{}, false, nil
	}
	return *matched, true, nil
}

func (client *Client) endpoint(path string) url.URL {
	endpoint := *client.base
	endpoint.Path = strings.TrimSuffix(client.base.Path, "/") + path
	endpoint.RawPath = ""
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	return endpoint
}

func (client *Client) get(ctx context.Context, endpoint url.URL, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.http.Do(request)
	if err != nil {
		return errors.New("GMD request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("GMD returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, client.responseLimit+1))
	if err != nil {
		return errors.New("GMD response could not be read")
	}
	if int64(len(body)) > client.responseLimit {
		return errors.New("GMD response exceeded its size limit")
	}
	if err := json.Unmarshal(body, target); err != nil {
		return errors.New("GMD returned invalid JSON")
	}
	return nil
}

func matchesExactIdentity(values []ExternalID, targets map[string]string) bool {
	matched := false
	for _, identity := range values {
		source := strings.ToLower(strings.TrimSpace(identity.Source))
		expected, relevant := targets[source]
		if !relevant {
			continue
		}
		if strings.TrimSpace(identity.ID) != expected {
			return false
		}
		matched = true
	}
	return matched
}

func normalized(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(value)), " ")
}

func validTitleID(value string) bool {
	if len(value) < 8 || len(value) > 80 || !strings.HasPrefix(value, "title_") {
		return false
	}
	for _, character := range value {
		if character != '_' && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}
