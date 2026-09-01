package catalog

import (
	"errors"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"uuid"
)

type MediaKind string

const (
	KindMovie  MediaKind = "movie"
	KindSeries MediaKind = "series"
)

type IdentityState string

const (
	IdentityVerified       IdentityState = "verified"
	IdentityUnverified     IdentityState = "unverified"
	IdentityNeedsAttention IdentityState = "needs_attention"
)

type ExternalIdentity struct {
	Namespace string        `json:"namespace"`
	Value     string        `json:"value"`
	State     IdentityState `json:"state"`
	Source    string        `json:"source"`
}

type MetadataAlias struct {
	Name     string `json:"name"`
	Language string `json:"language,omitempty"`
	Source   string `json:"source"`
}

type MetadataNetwork struct {
	Name    string `json:"name"`
	Country string `json:"country,omitempty"`
	Kind    string `json:"kind,omitempty"`
	Source  string `json:"source"`
}

type MetadataSource struct {
	Provider   string `json:"provider"`
	ProviderID string `json:"provider_id,omitempty"`
	URL        string `json:"url,omitempty"`
	Role       string `json:"role"`
}

type MetadataRating struct {
	Source string  `json:"source"`
	Value  float64 `json:"value"`
	Votes  int64   `json:"votes"`
}

type MetadataEvent struct {
	Type          string   `json:"type"`
	Date          string   `json:"date"`
	SeasonNumber  *uint16  `json:"season_number,omitempty"`
	EpisodeNumber *uint16  `json:"episode_number,omitempty"`
	Country       string   `json:"country,omitempty"`
	Network       string   `json:"network,omitempty"`
	Confidence    float64  `json:"confidence,omitempty"`
	DateConflict  bool     `json:"date_conflict"`
	Sources       []string `json:"sources"`
}

type MediaMetadata struct {
	OriginalTitle    string            `json:"original_title,omitempty"`
	Overview         string            `json:"overview,omitempty"`
	OriginalLanguage string            `json:"original_language,omitempty"`
	Format           string            `json:"format,omitempty"`
	Status           string            `json:"status,omitempty"`
	RuntimeMinutes   *uint16           `json:"runtime_minutes,omitempty"`
	PosterURL        string            `json:"poster_url,omitempty"`
	BackdropURL      string            `json:"backdrop_url,omitempty"`
	Genres           []string          `json:"genres"`
	Countries        []string          `json:"countries"`
	Networks         []MetadataNetwork `json:"networks"`
	Aliases          []MetadataAlias   `json:"aliases"`
	Sources          []MetadataSource  `json:"sources"`
	Ratings          []MetadataRating  `json:"ratings"`
	Events           []MetadataEvent   `json:"events"`
	ObservedAt       time.Time         `json:"observed_at"`
}

type MediaItem struct {
	ID         uuid.UUID          `json:"id"`
	Kind       MediaKind          `json:"kind"`
	Title      string             `json:"title"`
	Year       *uint16            `json:"year,omitempty"`
	Identities []ExternalIdentity `json:"identities"`
	Metadata   *MediaMetadata     `json:"metadata,omitempty"`
	Revision   uint64             `json:"revision"`
	CreatedAt  time.Time          `json:"created_at"`
	UpdatedAt  time.Time          `json:"updated_at"`
}

var (
	ErrInvalidMediaKind  = errors.New("invalid media kind")
	ErrTitleRequired     = errors.New("title is required")
	ErrInvalidYear       = errors.New("year is outside the supported range")
	ErrIdentityConflict  = errors.New("conflicting external identities")
	ErrInvalidIdentity   = errors.New("external identity has an invalid value")
	ErrInvalidMetadata   = errors.New("media metadata is invalid")
	numericProviderID    = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)
	imdbProviderID       = regexp.MustCompile(`^tt[0-9]{7,12}$`)
	metadataProviderName = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,31}$`)
)

var metadataSourceHosts = map[string]map[string]bool{
	"tmdb":  {"www.themoviedb.org": true, "themoviedb.org": true},
	"tvdb":  {"thetvdb.com": true, "www.thetvdb.com": true},
	"simkl": {"simkl.com": true, "www.simkl.com": true},
}

func NormalizeMetadata(input *MediaMetadata, now time.Time) (*MediaMetadata, error) {
	if input == nil {
		return nil, nil
	}
	result := *input
	result.OriginalTitle = boundedText(result.OriginalTitle, 500)
	result.Overview = boundedText(result.Overview, 20_000)
	result.OriginalLanguage = boundedText(result.OriginalLanguage, 32)
	result.Format = boundedText(result.Format, 100)
	result.Status = boundedText(result.Status, 100)
	if result.RuntimeMinutes != nil && (*result.RuntimeMinutes == 0 || *result.RuntimeMinutes > 1_440) {
		return nil, ErrInvalidMetadata
	}
	if !validArtworkURL(result.PosterURL) || !validArtworkURL(result.BackdropURL) {
		return nil, ErrInvalidMetadata
	}
	result.Genres = canonicalStrings(result.Genres, 100, 100)
	result.Countries = canonicalStrings(result.Countries, 50, 32)
	if len(input.Genres) > 100 || len(input.Countries) > 50 || len(input.Networks) > 50 || len(input.Aliases) > 100 || len(input.Sources) > 20 || len(input.Ratings) > 10 || len(input.Events) > 200 {
		return nil, ErrInvalidMetadata
	}
	result.Networks = make([]MetadataNetwork, 0, len(input.Networks))
	seenNetworks := map[string]struct{}{}
	for _, network := range input.Networks {
		network.Name, network.Country, network.Kind, network.Source = boundedText(network.Name, 200), boundedText(network.Country, 32), boundedText(network.Kind, 100), normalizedProvider(network.Source)
		if network.Name == "" || network.Source == "" {
			continue
		}
		key := strings.ToLower(network.Name + "\x00" + network.Country + "\x00" + network.Source)
		if _, duplicate := seenNetworks[key]; duplicate {
			continue
		}
		seenNetworks[key] = struct{}{}
		result.Networks = append(result.Networks, network)
	}
	result.Aliases = make([]MetadataAlias, 0, len(input.Aliases))
	seenAliases := map[string]struct{}{}
	for _, alias := range input.Aliases {
		alias.Name, alias.Language, alias.Source = boundedText(alias.Name, 500), boundedText(alias.Language, 32), normalizedProvider(alias.Source)
		if alias.Name == "" || alias.Source == "" {
			continue
		}
		key := strings.ToLower(alias.Name + "\x00" + alias.Language + "\x00" + alias.Source)
		if _, duplicate := seenAliases[key]; duplicate {
			continue
		}
		seenAliases[key] = struct{}{}
		result.Aliases = append(result.Aliases, alias)
	}
	result.Sources = make([]MetadataSource, 0, len(input.Sources))
	seenSources := map[string]struct{}{}
	for _, source := range input.Sources {
		source.Provider, source.ProviderID, source.Role = normalizedProvider(source.Provider), boundedText(source.ProviderID, 100), boundedText(source.Role, 100)
		if source.Provider == "" || source.Role == "" {
			continue
		}
		if !validSourceURL(source.Provider, source.URL) {
			return nil, ErrInvalidMetadata
		}
		key := source.Provider + "\x00" + source.ProviderID + "\x00" + source.Role
		if _, duplicate := seenSources[key]; duplicate {
			continue
		}
		seenSources[key] = struct{}{}
		result.Sources = append(result.Sources, source)
	}
	result.Ratings = make([]MetadataRating, 0, len(input.Ratings))
	seenRatings := map[string]struct{}{}
	for _, rating := range input.Ratings {
		rating.Source = normalizedProvider(rating.Source)
		if rating.Source == "" || rating.Value < 0 || rating.Value > 10 || rating.Votes < 0 {
			continue
		}
		if _, duplicate := seenRatings[rating.Source]; duplicate {
			continue
		}
		seenRatings[rating.Source] = struct{}{}
		result.Ratings = append(result.Ratings, rating)
	}
	result.Events = make([]MetadataEvent, 0, len(input.Events))
	seenEvents := map[string]struct{}{}
	for _, event := range input.Events {
		event.Type, event.Country, event.Network = boundedText(event.Type, 100), boundedText(event.Country, 32), boundedText(event.Network, 200)
		if event.Type == "" || !validDate(event.Date) || event.Confidence < 0 || event.Confidence > 1 {
			continue
		}
		event.Sources = canonicalProviders(event.Sources, 10)
		key := event.Type + "\x00" + event.Date
		if event.SeasonNumber != nil {
			key += "\x00" + strconv.Itoa(int(*event.SeasonNumber))
		}
		if event.EpisodeNumber != nil {
			key += "\x00" + strconv.Itoa(int(*event.EpisodeNumber))
		}
		if _, duplicate := seenEvents[key]; duplicate {
			continue
		}
		seenEvents[key] = struct{}{}
		result.Events = append(result.Events, event)
	}
	result.ObservedAt = input.ObservedAt.UTC()
	if result.ObservedAt.IsZero() {
		result.ObservedAt = now.UTC()
	}
	return &result, nil
}

func boundedText(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}

func canonicalStrings(values []string, maximum, limit int) []string {
	seen := map[string]string{}
	for _, value := range values {
		value = boundedText(value, limit)
		if value != "" {
			seen[strings.ToLower(value)] = value
		}
	}
	result := make([]string, 0, min(maximum, len(seen)))
	for _, value := range seen {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i]) < strings.ToLower(result[j]) })
	if len(result) > maximum {
		result = result[:maximum]
	}
	return result
}

func canonicalProviders(values []string, maximum int) []string {
	providers := make([]string, 0, len(values))
	for _, value := range values {
		if provider := normalizedProvider(value); provider != "" {
			providers = append(providers, provider)
		}
	}
	return canonicalStrings(providers, maximum, 32)
}

func normalizedProvider(value string) string {
	value = strings.ToLower(boundedText(value, 32))
	if !metadataProviderName.MatchString(value) {
		return ""
	}
	return value
}

func validDate(value string) bool {
	_, err := time.Parse("2006-01-02", value)
	return err == nil
}

func validArtworkURL(value string) bool {
	if strings.TrimSpace(value) == "" {
		return true
	}
	if len(value) > 2_048 {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" && parsed.Port() != "443" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "image.tmdb.org" || host == "artworks.thetvdb.com" || strings.HasSuffix(host, ".thetvdb.com")
}

func validSourceURL(provider, value string) bool {
	if strings.TrimSpace(value) == "" {
		return true
	}
	if len(value) > 2_048 {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" && parsed.Port() != "443" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	return metadataSourceHosts[provider][strings.ToLower(parsed.Hostname())]
}

func New(kind MediaKind, title string, year *uint16, identities []ExternalIdentity, now time.Time) (MediaItem, error) {
	if kind != KindMovie && kind != KindSeries {
		return MediaItem{}, ErrInvalidMediaKind
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return MediaItem{}, ErrTitleRequired
	}
	if year != nil && (*year < 1800 || *year > 9999) {
		return MediaItem{}, ErrInvalidYear
	}
	identities, err := canonicalIdentities(kind, identities)
	if err != nil {
		return MediaItem{}, err
	}
	return MediaItem{
		ID:         uuid.NewV7(),
		Kind:       kind,
		Title:      title,
		Year:       year,
		Identities: append([]ExternalIdentity(nil), identities...),
		Revision:   1,
		CreatedAt:  now.UTC(),
		UpdatedAt:  now.UTC(),
	}, nil
}

func ValidateIdentities(kind MediaKind, identities []ExternalIdentity) error {
	_, err := canonicalIdentities(kind, identities)
	return err
}

func canonicalIdentities(kind MediaKind, identities []ExternalIdentity) ([]ExternalIdentity, error) {
	seen := make(map[string]string, len(identities))
	unique := make(map[string]struct{}, len(identities))
	result := make([]ExternalIdentity, 0, len(identities))
	for _, identity := range identities {
		namespace := strings.ToLower(strings.TrimSpace(identity.Namespace))
		value := strings.TrimSpace(identity.Value)
		if namespace == "" || value == "" || identity.State != IdentityVerified && identity.State != IdentityUnverified && identity.State != IdentityNeedsAttention {
			return nil, ErrIdentityConflict
		}
		if identity.State == IdentityVerified && ((namespace == "tmdb" || namespace == "tvdb") && !numericProviderID.MatchString(value) || namespace == "imdb" && !imdbProviderID.MatchString(strings.ToLower(value))) {
			return nil, ErrInvalidIdentity
		}
		if namespace == "imdb" {
			value = strings.ToLower(value)
		}
		key := string(kind) + ":" + namespace
		if previous, exists := seen[key]; exists && previous != value {
			return nil, ErrIdentityConflict
		}
		seen[key] = value
		identity.Namespace = namespace
		identity.Value = value
		identity.Source = strings.TrimSpace(identity.Source)
		exact := key + "\x00" + value
		if _, duplicate := unique[exact]; duplicate {
			continue
		}
		unique[exact] = struct{}{}
		result = append(result, identity)
	}
	return result, nil
}
