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
	Key       string     `xml:"key,attr" json:"key"`
	Title     string     `xml:"title,attr" json:"title"`
	Type      string     `xml:"type,attr" json:"type"`
	Locations []Location `xml:"Location" json:"locations,omitempty"`
}
type Location struct {
	Path string `xml:"path,attr" json:"path"`
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
	RatingKey            string  `xml:"ratingKey,attr"`
	GrandparentRatingKey string  `xml:"grandparentRatingKey,attr"`
	Title                string  `xml:"title,attr"`
	GUID                 string  `xml:"guid,attr"`
	Season               string  `xml:"parentIndex,attr"`
	Episode              string  `xml:"index,attr"`
	Type                 string  `xml:"type,attr"`
	AiredAt              string  `xml:"originallyAvailableAt,attr"`
	GUIDs                []GUID  `xml:"Guid"`
	Media                []Media `xml:"Media"`
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
	// ErrIdentityMismatch means Plex shows the exact expected path but under a
	// provider identity other than the verified one. It is still a
	// not-confirmed outcome (errors.Is ErrNotConfirmed), with a precise reason.
	ErrIdentityMismatch = fmt.Errorf("%w: exact path is matched to a different provider identity", ErrNotConfirmed)
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
	confirmation, titleErr := client.confirmMovieItems(ctx, document.Items, expected)
	if !errors.Is(titleErr, ErrNotConfirmed) {
		return confirmation, titleErr
	}
	// The title filter only narrows the listing. Plex matches it against its
	// own (possibly localized) metadata title, so a correctly published file
	// can be invisible to it. Look the exact path up through the section's
	// folder tree instead; path and provider identity remain the only proof.
	items, err := client.itemsAtPath(ctx, expected.LibraryKey, expected.PlexVisiblePath)
	if err != nil {
		return Confirmation{}, err
	}
	confirmation, err = client.confirmMovieItems(ctx, items, expected)
	if errors.Is(titleErr, ErrIdentityMismatch) && errors.Is(err, ErrNotConfirmed) {
		return Confirmation{}, ErrIdentityMismatch
	}
	return confirmation, err
}

func (client *Client) confirmMovieItems(ctx context.Context, items []Item, expected Expected) (Confirmation, error) {
	expectedPath := filepath.Clean(expected.PlexVisiblePath)
	pathMatched := false
	for _, item := range items {
		matchedPath := matchingPartPath(item, expectedPath)
		if matchedPath == "" {
			continue
		}
		pathMatched = true
		guid := matchingGUID(item, expected.ProviderGUIDs)
		if guid == "" && numeric(item.RatingKey) {
			detailed, detailErr := client.metadataItem(ctx, item.RatingKey)
			if detailErr != nil && !errors.Is(detailErr, ErrNotConfirmed) {
				return Confirmation{}, detailErr
			}
			guid = matchingGUID(detailed, expected.ProviderGUIDs)
		}
		if guid == "" && numeric(item.RatingKey) {
			// See confirmShow: an item Plex never identified is proven by its
			// exact path alone; a differently identified one is not.
			unidentifiedGUID, err := client.unidentifiedItemGUID(ctx, item.RatingKey)
			if err != nil {
				return Confirmation{}, err
			}
			guid = unidentifiedGUID
		}
		if guid != "" {
			return Confirmation{RatingKey: item.RatingKey, Path: matchedPath, ProviderGUID: guid, ConfirmedAt: time.Now().UTC()}, nil
		}
	}
	if pathMatched {
		return Confirmation{}, ErrIdentityMismatch
	}
	return Confirmation{}, ErrNotConfirmed
}

const (
	maxFolderDepth    = 8
	maxFolderBranches = 4
)

// itemsAtPath returns the Plex items whose media part is exactly path, found by
// walking the library section's folder tree from the section location that
// contains path. Each level is matched by exact folder name, so the lookup
// never depends on Plex's metadata title and stays a handful of small requests.
func (client *Client) itemsAtPath(ctx context.Context, libraryKey, path string) ([]Item, error) {
	sections, err := client.Sections(ctx)
	if err != nil {
		return nil, err
	}
	var items []Item
	for _, section := range sections {
		if section.Key != libraryKey {
			continue
		}
		for _, location := range section.Locations {
			components, ok := relativeComponents(location.Path, path)
			if !ok {
				continue
			}
			found, err := client.walkFolder(ctx, libraryKey, "", components, filepath.Clean(path))
			if err != nil {
				return nil, err
			}
			items = append(items, found...)
		}
	}
	return items, nil
}

func (client *Client) walkFolder(ctx context.Context, libraryKey, parent string, components []string, expectedPath string) ([]Item, error) {
	query := url.Values{}
	if parent != "" {
		query.Set("parent", parent)
	}
	body, err := client.get(ctx, "/library/sections/"+libraryKey+"/folder", query)
	if err != nil {
		return nil, err
	}
	var document struct {
		Directories []struct {
			Key   string `xml:"key,attr"`
			Title string `xml:"title,attr"`
		} `xml:"Directory"`
		Videos []Item `xml:"Video"`
	}
	if err := xml.Unmarshal(body, &document); err != nil {
		return nil, err
	}
	if len(components) == 1 {
		var items []Item
		for _, video := range document.Videos {
			if matchingPartPath(video, expectedPath) != "" {
				items = append(items, video)
			}
		}
		return items, nil
	}
	var items []Item
	branches := 0
	for _, directory := range document.Directories {
		if directory.Title != components[0] {
			continue
		}
		child, ok := folderParent(directory.Key, libraryKey)
		if !ok {
			continue
		}
		// A multi-location section merges its locations' children, so the
		// same folder name may appear more than once. The exact part path is
		// still required at the leaf, so exploring each branch is safe.
		if branches++; branches > maxFolderBranches {
			break
		}
		found, err := client.walkFolder(ctx, libraryKey, child, components[1:], expectedPath)
		if err != nil {
			return nil, err
		}
		items = append(items, found...)
	}
	return items, nil
}

// relativeComponents splits path below root into folder/file names. It fails
// closed for paths outside root, the root itself, and over-deep paths.
func relativeComponents(root, path string) ([]string, bool) {
	if !filepath.IsAbs(root) || !filepath.IsAbs(path) {
		return nil, false
	}
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, "../") {
		return nil, false
	}
	components := strings.Split(relative, "/")
	if len(components) > maxFolderDepth {
		return nil, false
	}
	for _, component := range components {
		if component == "" || component == "." || component == ".." {
			return nil, false
		}
	}
	return components, true
}

// folderParent accepts only this section's own folder-browse key and returns
// its numeric parent ID; any other key Plex reports is ignored.
func folderParent(key, libraryKey string) (string, bool) {
	parsed, err := url.Parse(key)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.Path != "/library/sections/"+libraryKey+"/folder" {
		return "", false
	}
	query := parsed.Query()
	parent := query.Get("parent")
	if len(query) != 1 || !numeric(parent) {
		return "", false
	}
	return parent, true
}

// ItemAtPath returns the one Plex item whose media part is exactly path in a
// library section, found by walking the section's folders by exact name.
func (client *Client) ItemAtPath(ctx context.Context, libraryKey, path string) (Item, error) {
	items, err := client.itemsAtPath(ctx, libraryKey, path)
	if err != nil {
		return Item{}, err
	}
	if len(items) != 1 {
		return Item{}, ErrNotConfirmed
	}
	return items[0], nil
}

// ItemsWithoutAudioLanguage lists the movies (or episodes) of a library
// section that have no audio track tagged with language, an ISO 639-2 code
// such as "dan", using Plex's audio-language filter. An untagged track does
// not count as the language. Items carry their media file paths.
func (client *Client) ItemsWithoutAudioLanguage(ctx context.Context, libraryKey string, episodes bool, language string) ([]Item, error) {
	if !numeric(libraryKey) || language == "" {
		return nil, ErrInvalidConfig
	}
	itemType := "1"
	if episodes {
		itemType = "4"
	}
	body, err := client.get(ctx, "/library/sections/"+libraryKey+"/all", url.Values{"type": {itemType}, "audioLanguage!": {language}})
	if err != nil {
		return nil, err
	}
	var document struct {
		Videos []Item `xml:"Video"`
	}
	if err := xml.Unmarshal(body, &document); err != nil {
		return nil, err
	}
	return document.Videos, nil
}

// Metadata returns one library item (movie, show or episode) with its
// provider GUIDs.
func (client *Client) Metadata(ctx context.Context, ratingKey string) (Item, error) {
	return client.metadataItem(ctx, ratingKey)
}

func (client *Client) metadataItem(ctx context.Context, ratingKey string) (Item, error) {
	if !numeric(ratingKey) {
		return Item{}, ErrInvalidConfig
	}
	body, err := client.get(ctx, "/library/metadata/"+ratingKey, nil)
	if err != nil {
		return Item{}, err
	}
	var document struct {
		Videos      []Item `xml:"Video"`
		Directories []Item `xml:"Directory"`
	}
	if err := xml.Unmarshal(body, &document); err != nil {
		return Item{}, err
	}
	for _, item := range append(document.Videos, document.Directories...) {
		if item.RatingKey == ratingKey {
			return item, nil
		}
	}
	return Item{}, ErrNotConfirmed
}

func matchingPartPath(item Item, expectedPath string) string {
	for _, media := range item.Media {
		for _, part := range media.Parts {
			if filepath.Clean(part.File) == expectedPath {
				return part.File
			}
		}
	}
	return ""
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
	tried := make(map[string]bool, len(shows.Items))
	leafShowMismatch := make(map[string]bool)
	for _, show := range shows.Items {
		tried[show.RatingKey] = true
		confirmations, err := client.confirmShow(ctx, show, expected)
		// A same-titled show with another identity is not evidence by itself;
		// it only matters if the folder lookup proves it owns the path.
		leafShowMismatch[show.RatingKey] = errors.Is(err, ErrIdentityMismatch)
		if !errors.Is(err, ErrNotConfirmed) {
			return confirmations, err
		}
	}
	// Plex may list the show under a localized title the filter cannot match.
	// Find the show that owns the first expected episode path through the
	// folder tree, then apply the same identity and every-leaf checks.
	leaves, err := client.itemsAtPath(ctx, expected.LibraryKey, expected.PlexVisiblePaths[0])
	if err != nil {
		return nil, err
	}
	result := ErrNotConfirmed
	for _, leaf := range leaves {
		key := leaf.GrandparentRatingKey
		if !numeric(key) {
			continue
		}
		if tried[key] {
			// The title pass already evaluated this show; the path proves it
			// owns the expected episode, so its identity result is precise.
			if leafShowMismatch[key] {
				result = ErrIdentityMismatch
			}
			continue
		}
		tried[key] = true
		confirmations, err := client.confirmShow(ctx, Item{RatingKey: key}, expected)
		if errors.Is(err, ErrIdentityMismatch) {
			result = ErrIdentityMismatch
		}
		if !errors.Is(err, ErrNotConfirmed) {
			return confirmations, err
		}
	}
	return nil, result
}

func (client *Client) confirmShow(ctx context.Context, show Item, expected ExpectedSeries) ([]Confirmation, error) {
	guid := matchingGUID(show, expected.ProviderGUIDs)
	if guid == "" && numeric(show.RatingKey) {
		detailed, detailErr := client.metadataItem(ctx, show.RatingKey)
		if detailErr != nil && !errors.Is(detailErr, ErrNotConfirmed) {
			return nil, detailErr
		}
		guid = matchingGUID(detailed, expected.ProviderGUIDs)
	}
	if !numeric(show.RatingKey) {
		return nil, ErrNotConfirmed
	}
	if guid == "" {
		// Plex has not identified this show at all (common for small Danish
		// titles). Then the exact paths below are the only identity evidence,
		// and they are sufficient: every expected path and episode must be in
		// this one show. A show Plex identified differently is still rejected.
		unidentifiedGUID, err := client.unidentifiedItemGUID(ctx, show.RatingKey)
		if err != nil {
			return nil, err
		}
		if unidentifiedGUID == "" {
			return nil, ErrIdentityMismatch
		}
		guid = unidentifiedGUID
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
			return nil, ErrNotConfirmed
		}
		for _, wantedEpisode := range expectedForPath {
			candidate, exists := available[clean][wantedEpisode]
			if !exists {
				return nil, ErrNotConfirmed
			}
			itemKey = candidate
		}
		confirmations = append(confirmations, Confirmation{RatingKey: itemKey, Path: clean, ProviderGUID: guid, ConfirmedAt: time.Now().UTC()})
	}
	return confirmations, nil
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

// unidentifiedItemGUID returns the item's own Plex GUID (plex://, local://)
// when Plex lists no external provider identity for it at all, and "" when it
// has one. External IDs are requested explicitly so an omitted list is never
// mistaken for an unidentified item.
func (client *Client) unidentifiedItemGUID(ctx context.Context, ratingKey string) (string, error) {
	if !numeric(ratingKey) {
		return "", ErrNotConfirmed
	}
	body, err := client.get(ctx, "/library/metadata/"+ratingKey, url.Values{"includeGuids": []string{"1"}})
	if err != nil {
		return "", err
	}
	var document struct {
		Videos      []Item `xml:"Video"`
		Directories []Item `xml:"Directory"`
	}
	if err := xml.Unmarshal(body, &document); err != nil {
		return "", err
	}
	for _, item := range append(document.Videos, document.Directories...) {
		if item.RatingKey != ratingKey {
			continue
		}
		for _, guid := range append(item.GUIDs, GUID{ID: item.GUID}) {
			id := strings.ToLower(strings.TrimSpace(guid.ID))
			if strings.HasPrefix(id, "imdb://") || strings.HasPrefix(id, "tmdb://") || strings.HasPrefix(id, "tvdb://") || strings.Contains(id, "agents.") {
				return "", nil
			}
		}
		if own := strings.TrimSpace(item.GUID); own != "" {
			return own, nil
		}
		return "plex-item://" + ratingKey, nil
	}
	return "", ErrNotConfirmed
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
