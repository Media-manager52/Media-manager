package metadataproviders

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/media-manager/media-manager/internal/catalog"
)

const defaultTVDBBaseURL = "https://api4.thetvdb.com"

var safeSlug = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,199}$`)

type TVDBConfig struct {
	APIKey        string
	SubscriberPIN string
	BaseURL       string
	AllowHTTP     bool
	Timeout       time.Duration
	ResponseLimit int64
}

type TVDBClient struct {
	client clientConfig
	key    string
	pin    string
	mu     sync.Mutex
	token  string
}

type tvdbAlias struct {
	Name     string `json:"name"`
	Language string `json:"language"`
}

type tvdbNamed struct {
	Name    string `json:"name"`
	Country string `json:"country"`
	Type    string `json:"type"`
}

type tvdbRemoteID struct {
	ID         stringID `json:"id"`
	SourceName string   `json:"sourceName"`
}

type tvdbData struct {
	ID               stringID       `json:"id"`
	Name             string         `json:"name"`
	Slug             string         `json:"slug"`
	Overview         string         `json:"overview"`
	Image            string         `json:"image"`
	OriginalCountry  string         `json:"originalCountry"`
	OriginalLanguage string         `json:"originalLanguage"`
	FirstAired       string         `json:"firstAired"`
	LastAired        string         `json:"lastAired"`
	Year             string         `json:"year"`
	AverageRuntime   int64          `json:"averageRuntime"`
	Runtime          int64          `json:"runtime"`
	Score            float64        `json:"score"`
	Status           tvdbNamed      `json:"status"`
	Genres           []tvdbNamed    `json:"genres"`
	Aliases          []tvdbAlias    `json:"aliases"`
	RemoteIDs        []tvdbRemoteID `json:"remoteIds"`
	OriginalNetwork  *tvdbNamed     `json:"originalNetwork"`
	Companies        []tvdbNamed    `json:"companies"`
	Characters       []struct {
		Name       string `json:"name"`
		PersonName string `json:"personName"`
		PeopleType string `json:"peopleType"`
		Image      string `json:"image"`
	} `json:"characters"`
	ContentRatings []struct {
		Name    string `json:"name"`
		Country string `json:"country"`
	} `json:"contentRatings"`
}

func NewTVDB(config TVDBConfig, transport http.RoundTripper) (*TVDBClient, error) {
	if strings.TrimSpace(config.APIKey) == "" {
		return nil, ErrInvalidConfig
	}
	if strings.TrimSpace(config.BaseURL) == "" {
		config.BaseURL = defaultTVDBBaseURL
	}
	client, err := newClientConfig(config.BaseURL, config.AllowHTTP, config.Timeout, config.ResponseLimit, transport)
	if err != nil {
		return nil, err
	}
	return &TVDBClient{client: client, key: strings.TrimSpace(config.APIKey), pin: strings.TrimSpace(config.SubscriberPIN)}, nil
}

func (client *TVDBClient) Lookup(ctx context.Context, kind catalog.MediaKind, identities []catalog.ExternalIdentity, now time.Time) (Contribution, error) {
	id := verifiedIdentity(identities, "tvdb")
	if !positiveID(id) || kind != catalog.KindMovie && kind != catalog.KindSeries {
		return Contribution{}, ErrIdentityUnavailable
	}
	recordType := "movies"
	if kind == catalog.KindSeries {
		recordType = "series"
	}
	response, err := client.authorizedGet(ctx, "/v4/"+recordType+"/"+id+"/extended", false)
	if err != nil {
		return Contribution{}, err
	}
	if response.StatusCode == http.StatusUnauthorized {
		response.Body.Close()
		response, err = client.authorizedGet(ctx, "/v4/"+recordType+"/"+id+"/extended", true)
		if err != nil {
			return Contribution{}, err
		}
	}
	var envelope struct {
		Data tvdbData `json:"data"`
	}
	if err := client.client.decode(response, &envelope); err != nil {
		return Contribution{}, err
	}
	if string(envelope.Data.ID) != id {
		return Contribution{}, ErrIdentityConflict
	}
	return tvdbContribution(kind, id, envelope.Data, identities, now)
}

func (client *TVDBClient) authorizedGet(ctx context.Context, path string, forceLogin bool) (*http.Response, error) {
	token, err := client.authToken(ctx, forceLogin)
	if err != nil {
		return nil, err
	}
	endpoint := client.client.endpoint(path)
	query := endpoint.Query()
	query.Set("meta", "translations")
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, ErrProviderUnavailable
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	response, err := client.client.http.Do(request)
	if err != nil {
		return nil, ErrProviderUnavailable
	}
	return response, nil
}

func (client *TVDBClient) authToken(ctx context.Context, force bool) (string, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if !force && client.token != "" {
		return client.token, nil
	}
	credentials := map[string]string{"apikey": client.key}
	if client.pin != "" {
		credentials["pin"] = client.pin
	}
	body, err := json.Marshal(credentials)
	if err != nil {
		return "", ErrProviderUnavailable
	}
	endpoint := client.client.endpoint("/v4/login")
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return "", ErrProviderUnavailable
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := client.client.http.Do(request)
	if err != nil {
		return "", ErrProviderUnavailable
	}
	var envelope struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := client.client.decode(response, &envelope); err != nil {
		return "", err
	}
	client.token = strings.TrimSpace(envelope.Data.Token)
	if client.token == "" {
		return "", ErrProviderUnavailable
	}
	return client.token, nil
}

func tvdbContribution(kind catalog.MediaKind, id string, payload tvdbData, existing []catalog.ExternalIdentity, now time.Time) (Contribution, error) {
	runtime := payload.AverageRuntime
	if runtime == 0 {
		runtime = payload.Runtime
	}
	format, role, urlKind := "Movie", "supplementary movie metadata", "movies"
	if kind == catalog.KindSeries {
		format, role, urlKind = "TV Series", "supplementary TV metadata", "series"
	}
	sourceURL := "https://thetvdb.com/dereferrer/" + urlKind + "/" + id
	if safeSlug.MatchString(payload.Slug) {
		sourceURL = "https://thetvdb.com/" + urlKind + "/" + payload.Slug
	}
	metadata := &catalog.MediaMetadata{OriginalTitle: payload.Name, Overview: payload.Overview, OriginalLanguage: payload.OriginalLanguage, Format: format, Status: payload.Status.Name, ReleaseDate: firstDate(payload.FirstAired), EndDate: firstDate(payload.LastAired), RuntimeMinutes: intPointer(runtime, 1_440), PosterURL: tvdbImage(payload.Image), Genres: []string{}, Keywords: []string{}, Countries: []string{payload.OriginalCountry}, Networks: []catalog.MetadataNetwork{}, Companies: []catalog.MetadataCompany{}, Credits: []catalog.MetadataCredit{}, Aliases: []catalog.MetadataAlias{}, Sources: []catalog.MetadataSource{{Provider: "tvdb", ProviderID: id, URL: sourceURL, Role: role}}, Ratings: []catalog.MetadataRating{}, Events: []catalog.MetadataEvent{}, ObservedAt: now.UTC()}
	for _, genre := range payload.Genres {
		metadata.Genres = append(metadata.Genres, genre.Name)
	}
	for _, alias := range payload.Aliases {
		metadata.Aliases = append(metadata.Aliases, catalog.MetadataAlias{Name: alias.Name, Language: alias.Language, Source: "tvdb"})
	}
	if payload.OriginalNetwork != nil && payload.OriginalNetwork.Name != "" {
		metadata.Networks = append(metadata.Networks, catalog.MetadataNetwork{Name: payload.OriginalNetwork.Name, Country: payload.OriginalNetwork.Country, Kind: "Original network", Source: "tvdb"})
	}
	for _, company := range payload.Companies {
		metadata.Companies = append(metadata.Companies, catalog.MetadataCompany{Name: company.Name, Country: company.Country, Kind: company.Type, Source: "tvdb"})
	}
	for _, character := range payload.Characters {
		if len(metadata.Credits) >= 30 {
			break
		}
		role := character.PeopleType
		if role == "" {
			role = "Cast"
		}
		metadata.Credits = append(metadata.Credits, catalog.MetadataCredit{Name: character.PersonName, Role: role, Character: character.Name, ProfileURL: tvdbImage(character.Image), Source: "tvdb"})
	}
	if payload.Score > 0 && payload.Score <= 10 {
		metadata.Ratings = append(metadata.Ratings, catalog.MetadataRating{Source: "tvdb", Value: payload.Score, Provenance: "direct TheTVDB API"})
	}
	for _, country := range []string{"DK", "US"} {
		for _, rating := range payload.ContentRatings {
			if strings.EqualFold(rating.Country, country) && rating.Name != "" {
				metadata.Certification = rating.Name
				break
			}
		}
		if metadata.Certification != "" {
			break
		}
	}
	if metadata.ReleaseDate != "" {
		eventType := "movie_release"
		if kind == catalog.KindSeries {
			eventType = "series_premiere"
		}
		metadata.Events = append(metadata.Events, catalog.MetadataEvent{Type: eventType, Date: metadata.ReleaseDate, Confidence: 0.8, Sources: []string{"tvdb"}})
	}
	identities := []catalog.ExternalIdentity{identity("tvdb", id, "tvdb_api")}
	for _, remote := range payload.RemoteIDs {
		namespace := tvdbRemoteNamespace(remote.SourceName)
		value := string(remote.ID)
		if namespace == "" || !validIdentity(namespace, value) {
			continue
		}
		identities = append(identities, identity(namespace, value, "tvdb_api"))
		if namespace == "imdb" {
			metadata.Sources = append(metadata.Sources, catalog.MetadataSource{Provider: "imdb", ProviderID: value, URL: "https://www.imdb.com/title/" + value + "/", Role: "verified title identity"})
		}
	}
	if identitiesConflict(existing, identities) {
		return Contribution{}, ErrIdentityConflict
	}
	normalized, err := catalog.NormalizeMetadata(metadata, now)
	if err != nil {
		return Contribution{}, ErrProviderUnavailable
	}
	return Contribution{Metadata: normalized, Identities: identities}, nil
}

func tvdbRemoteNamespace(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "themoviedb.com", "the movie db", "tmdb":
		return "tmdb"
	case "imdb", "imdb.com":
		return "imdb"
	default:
		return ""
	}
}

func tvdbImage(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "?#") {
		return ""
	}
	if strings.HasPrefix(value, "/") {
		return "https://artworks.thetvdb.com" + value
	}
	if strings.HasPrefix(value, "https://artworks.thetvdb.com/") {
		return value
	}
	return ""
}
