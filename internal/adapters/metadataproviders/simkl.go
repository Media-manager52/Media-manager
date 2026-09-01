package metadataproviders

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/media-manager/media-manager/internal/catalog"
)

const defaultSimklBaseURL = "https://api.simkl.com"

var simklPath = regexp.MustCompile(`^/(movies|tv|anime)/([1-9][0-9]{0,19})(?:/[A-Za-z0-9_-]+)?/?$`)

type SimklConfig struct {
	ClientID      string
	BaseURL       string
	AllowHTTP     bool
	Timeout       time.Duration
	ResponseLimit int64
	AppName       string
	AppVersion    string
}

type SimklClient struct {
	client     clientConfig
	clientID   string
	appName    string
	appVersion string
}

type simklRating struct {
	Rating float64 `json:"rating"`
	Votes  int64   `json:"votes"`
}

type simklPayload struct {
	Title         string   `json:"title"`
	Year          int      `json:"year"`
	Type          string   `json:"type"`
	Runtime       int64    `json:"runtime"`
	Certification string   `json:"certification"`
	Network       string   `json:"network"`
	Status        string   `json:"status"`
	FirstAired    string   `json:"first_aired"`
	LastAired     string   `json:"last_aired"`
	TotalEpisodes int64    `json:"total_episodes"`
	TotalSeasons  int64    `json:"total_seasons"`
	Genres        []string `json:"genres"`
	IDs           struct {
		Simkl stringID `json:"simkl"`
		IMDb  string   `json:"imdb"`
		TMDB  stringID `json:"tmdb"`
		TVDB  stringID `json:"tvdb"`
	} `json:"ids"`
	Ratings struct {
		Simkl simklRating `json:"simkl"`
		IMDb  simklRating `json:"imdb"`
	} `json:"ratings"`
}

func NewSimkl(config SimklConfig, transport http.RoundTripper) (*SimklClient, error) {
	if strings.TrimSpace(config.ClientID) == "" {
		return nil, ErrInvalidConfig
	}
	if strings.TrimSpace(config.BaseURL) == "" {
		config.BaseURL = defaultSimklBaseURL
	}
	client, err := newClientConfig(config.BaseURL, config.AllowHTTP, config.Timeout, config.ResponseLimit, transport)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(config.AppName) == "" {
		config.AppName = "Media Manager"
	}
	if strings.TrimSpace(config.AppVersion) == "" {
		config.AppVersion = "development"
	}
	return &SimklClient{client: client, clientID: strings.TrimSpace(config.ClientID), appName: strings.TrimSpace(config.AppName), appVersion: strings.TrimSpace(config.AppVersion)}, nil
}

func (client *SimklClient) Lookup(ctx context.Context, kind catalog.MediaKind, identities []catalog.ExternalIdentity, now time.Time) (Contribution, error) {
	resolvedType, simklID, sourceURL, err := client.resolve(ctx, kind, identities)
	if err != nil {
		return Contribution{}, err
	}
	endpoint := client.client.endpoint("/" + resolvedType + "/" + simklID)
	client.addQuery(&endpoint)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return Contribution{}, ErrProviderUnavailable
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Media-Manager/"+client.appVersion)
	response, err := client.client.http.Do(request)
	if err != nil {
		return Contribution{}, ErrProviderUnavailable
	}
	var payload simklPayload
	if err := client.client.decode(response, &payload); err != nil {
		return Contribution{}, err
	}
	if string(payload.IDs.Simkl) != simklID {
		return Contribution{}, ErrIdentityConflict
	}
	return simklContribution(kind, simklID, sourceURL, payload, identities, now)
}

func (client *SimklClient) resolve(ctx context.Context, kind catalog.MediaKind, identities []catalog.ExternalIdentity) (string, string, string, error) {
	endpoint := client.client.endpoint("/redirect")
	query := endpoint.Query()
	query.Set("to", "simkl")
	if value := verifiedIdentity(identities, "imdb"); value != "" {
		query.Set("imdb", value)
	} else if value := verifiedIdentity(identities, "tvdb"); positiveID(value) {
		query.Set("tvdb", value)
	} else if value := verifiedIdentity(identities, "tmdb"); positiveID(value) {
		query.Set("tmdb", value)
	} else {
		return "", "", "", ErrIdentityUnavailable
	}
	if kind == catalog.KindMovie {
		query.Set("type", "movie")
	} else if kind == catalog.KindSeries {
		query.Set("type", "tv")
	} else {
		return "", "", "", ErrIdentityUnavailable
	}
	endpoint.RawQuery = query.Encode()
	client.addQuery(&endpoint)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", "", "", ErrProviderUnavailable
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Media-Manager/"+client.appVersion)
	response, err := client.client.http.Do(request)
	if err != nil {
		return "", "", "", ErrProviderUnavailable
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
	if response.StatusCode != http.StatusMovedPermanently {
		return "", "", "", ErrProviderUnavailable
	}
	location, err := url.Parse(response.Header.Get("Location"))
	if err == nil && location.Scheme == "" && location.Host != "" {
		// Simkl's documented redirect endpoint currently returns a network-path
		// reference. Pin its scheme to HTTPS before applying the normal strict
		// origin and path checks; path-only relative redirects remain rejected.
		location.Scheme = "https"
	}
	// The provider currently echoes API parameters on the human-facing target.
	// Media Manager never follows this redirect and deliberately discards every
	// query parameter before validating or persisting the public item link.
	location.RawQuery = ""
	location.ForceQuery = false
	if err != nil || location.Scheme != "https" || location.User != nil || location.Fragment != "" || location.Port() != "" && location.Port() != "443" || !strings.EqualFold(location.Hostname(), "simkl.com") {
		return "", "", "", ErrProviderUnavailable
	}
	match := simklPath.FindStringSubmatch(location.EscapedPath())
	if len(match) != 3 || kind == catalog.KindMovie && match[1] != "movies" || kind == catalog.KindSeries && match[1] != "tv" && match[1] != "anime" {
		return "", "", "", ErrProviderUnavailable
	}
	return match[1], match[2], location.String(), nil
}

func (client *SimklClient) addQuery(endpoint *url.URL) {
	query := endpoint.Query()
	query.Set("client_id", client.clientID)
	query.Set("app-name", client.appName)
	query.Set("app-version", client.appVersion)
	endpoint.RawQuery = query.Encode()
}

func simklContribution(kind catalog.MediaKind, id, sourceURL string, payload simklPayload, existing []catalog.ExternalIdentity, now time.Time) (Contribution, error) {
	metadata := &catalog.MediaMetadata{Format: payload.Type, Status: payload.Status, ReleaseDate: firstDate(payload.FirstAired), EndDate: firstDate(payload.LastAired), Certification: payload.Certification, RuntimeMinutes: intPointer(payload.Runtime, 1_440), SeasonCount: intPointer(payload.TotalSeasons, 10_000), EpisodeCount: uint32Pointer(payload.TotalEpisodes), Genres: []string{}, Keywords: []string{}, Countries: []string{}, Networks: []catalog.MetadataNetwork{}, Companies: []catalog.MetadataCompany{}, Credits: []catalog.MetadataCredit{}, Aliases: []catalog.MetadataAlias{}, Sources: []catalog.MetadataSource{{Provider: "simkl", ProviderID: id, URL: sourceURL, Role: "ratings and lifecycle evidence"}}, Ratings: []catalog.MetadataRating{}, Events: []catalog.MetadataEvent{}, ObservedAt: now.UTC()}
	if payload.Network != "" {
		metadata.Networks = append(metadata.Networks, catalog.MetadataNetwork{Name: payload.Network, Kind: "Network or service", Source: "simkl"})
	}
	if payload.Ratings.Simkl.Rating > 0 && payload.Ratings.Simkl.Rating <= 10 {
		metadata.Ratings = append(metadata.Ratings, catalog.MetadataRating{Source: "simkl", Value: payload.Ratings.Simkl.Rating, Votes: payload.Ratings.Simkl.Votes, Provenance: "direct Simkl API"})
	}
	if payload.Ratings.IMDb.Rating > 0 && payload.Ratings.IMDb.Rating <= 10 {
		metadata.Ratings = append(metadata.Ratings, catalog.MetadataRating{Source: "imdb", Value: payload.Ratings.IMDb.Rating, Votes: payload.Ratings.IMDb.Votes, Provenance: "Simkl catalog evidence; not a direct IMDb API response"})
	}
	if metadata.ReleaseDate != "" {
		eventType := "movie_release"
		if kind == catalog.KindSeries {
			eventType = "series_premiere"
		}
		metadata.Events = append(metadata.Events, catalog.MetadataEvent{Type: eventType, Date: metadata.ReleaseDate, Confidence: 0.75, Sources: []string{"simkl"}})
	}
	if metadata.EndDate != "" && kind == catalog.KindSeries {
		metadata.Events = append(metadata.Events, catalog.MetadataEvent{Type: "series_finale", Date: metadata.EndDate, Confidence: 0.75, Sources: []string{"simkl"}})
	}
	identities := []catalog.ExternalIdentity{identity("simkl", id, "simkl_api")}
	returned := map[string]string{"imdb": strings.TrimSpace(payload.IDs.IMDb), "tmdb": string(payload.IDs.TMDB), "tvdb": string(payload.IDs.TVDB)}
	for namespace, value := range returned {
		if validIdentity(namespace, value) {
			identities = append(identities, identity(namespace, value, "simkl_api"))
			if namespace == "imdb" {
				metadata.Sources = append(metadata.Sources, catalog.MetadataSource{Provider: "imdb", ProviderID: value, URL: "https://www.imdb.com/title/" + value + "/", Role: "rating evidence through Simkl and verified title identity"})
			}
		}
	}
	if identitiesConflict(existing, identities) {
		return Contribution{}, ErrIdentityConflict
	}
	if !atLeastOneIdentityMatches(existing, identities) {
		return Contribution{}, ErrIdentityConflict
	}
	normalized, err := catalog.NormalizeMetadata(metadata, now)
	if err != nil {
		return Contribution{}, ErrProviderUnavailable
	}
	return Contribution{Metadata: normalized, Identities: identities}, nil
}

func atLeastOneIdentityMatches(existing, returned []catalog.ExternalIdentity) bool {
	for _, left := range existing {
		if left.State != catalog.IdentityVerified {
			continue
		}
		for _, right := range returned {
			if strings.EqualFold(left.Namespace, right.Namespace) && left.Value == right.Value {
				return true
			}
		}
	}
	return false
}
