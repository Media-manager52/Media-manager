// Package mdblist reads public MDBList lists (https://mdblist.com), the same
// list URLs Radarr and Sonarr import. A public list needs no account or key:
// its JSON form lists each title with its TMDB, IMDb and TVDB numbers.
package mdblist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/media-manager/media-manager/internal/importlists"
)

// MaxItems bounds one list. A longer list is refused rather than cut short,
// so a list is never silently half-followed.
const MaxItems = 5000

const (
	defaultBaseURL = "https://mdblist.com"
	responseLimit  = 8 << 20
)

// ListRef names one public list; see importlists.ParseMDBListURL.
type ListRef = importlists.MDBListRef

// ErrInvalidListURL is returned for a list reference that is not valid.
var ErrInvalidListURL = importlists.ErrInvalidMDBListURL

var (
	ErrListNotFound = errors.New("the MDBList list was not found or is not public")
	ErrListTooLong  = fmt.Errorf("the MDBList list has more than %d titles", MaxItems)
	ErrUnavailable  = errors.New("MDBList could not be reached or answered unexpectedly")
)

// Item is one title on a list. Kind is "movie" or "series"; titles of any
// other kind are left out by Fetch.
type Item struct {
	Rank   int
	Kind   string
	TMDBID int64
	IMDbID string
	TVDBID int64
	Title  string
	Year   int
}

type Client struct {
	base *url.URL
	http *http.Client
}

// New returns a client for mdblist.com. transport may be nil. baseURL is for
// tests; production passes "".
func New(baseURL string, transport http.RoundTripper) (*Client, error) {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultBaseURL
	}
	base, err := url.Parse(baseURL)
	if err != nil || base.Host == "" || base.Scheme != "https" && base.Scheme != "http" {
		return nil, errors.New("invalid MDBList base URL")
	}
	if transport == nil {
		transport = http.DefaultTransport
	}
	// Redirects are not followed: the list must come from mdblist.com itself.
	return &Client{base: base, http: &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

type rawItem struct {
	ID          int64           `json:"id"`
	Rank        int             `json:"rank"`
	Title       string          `json:"title"`
	IMDbID      string          `json:"imdb_id"`
	TVDBID      json.RawMessage `json:"tvdbid"`
	MediaType   string          `json:"mediatype"`
	ReleaseYear json.RawMessage `json:"release_year"`
}

// Fetch reads every title of a public list.
func (client *Client) Fetch(ctx context.Context, ref ListRef) ([]Item, error) {
	if !ref.Valid() {
		return nil, ErrInvalidListURL
	}
	endpoint := *client.base
	// Path holds the plain segments and RawPath the escaped ones, so æ, ø
	// and å are sent percent-encoded exactly once.
	prefix := strings.TrimSuffix(client.base.Path, "/") + "/lists/"
	endpoint.Path = prefix + ref.Owner + "/" + ref.Slug + "/json"
	endpoint.RawPath = prefix + url.PathEscape(ref.Owner) + "/" + url.PathEscape(ref.Slug) + "/json"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, ErrUnavailable
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Media Manager")
	response, err := client.http.Do(request)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, ErrListNotFound
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, ErrUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	if err != nil || len(body) > responseLimit {
		return nil, ErrUnavailable
	}
	return parseItems(body)
}

func parseItems(body []byte) ([]Item, error) {
	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "{") {
		// MDBList answers an unknown or private list with an error object.
		return nil, ErrListNotFound
	}
	var raw []rawItem
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return nil, ErrUnavailable
	}
	if len(raw) > MaxItems {
		return nil, ErrListTooLong
	}
	items := make([]Item, 0, len(raw))
	seen := map[string]bool{}
	for index, entry := range raw {
		kind := ""
		switch strings.ToLower(strings.TrimSpace(entry.MediaType)) {
		case "movie":
			kind = "movie"
		case "show", "tv", "series":
			kind = "series"
		default:
			continue
		}
		title := strings.TrimSpace(entry.Title)
		if entry.ID <= 0 || title == "" {
			continue
		}
		if len(title) > 500 {
			title = title[:500]
		}
		key := fmt.Sprintf("%s:%d", kind, entry.ID)
		if seen[key] {
			continue
		}
		seen[key] = true
		item := Item{Rank: entry.Rank, Kind: kind, TMDBID: entry.ID, Title: title}
		if item.Rank <= 0 {
			item.Rank = index + 1
		}
		if imdb := strings.ToLower(strings.TrimSpace(entry.IMDbID)); validIMDbID(imdb) {
			item.IMDbID = imdb
		}
		item.TVDBID = positiveNumber(entry.TVDBID)
		if year := positiveNumber(entry.ReleaseYear); year >= 1800 && year <= 9999 {
			item.Year = int(year)
		}
		items = append(items, item)
	}
	return items, nil
}

// positiveNumber reads a JSON number that MDBList may also send as null, a
// string or not at all; anything but a positive whole number is 0.
func positiveNumber(raw json.RawMessage) int64 {
	text := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	var value int64
	if _, err := fmt.Sscan(text, &value); err != nil || value <= 0 {
		return 0
	}
	return value
}

func validIMDbID(value string) bool {
	if len(value) < 9 || len(value) > 14 || !strings.HasPrefix(value, "tt") {
		return false
	}
	for _, character := range value[2:] {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}
