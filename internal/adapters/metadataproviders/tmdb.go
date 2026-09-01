package metadataproviders

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/media-manager/media-manager/internal/catalog"
)

const defaultTMDBBaseURL = "https://api.themoviedb.org"

type TMDBConfig struct {
	AccessToken   string
	BaseURL       string
	AllowHTTP     bool
	Timeout       time.Duration
	ResponseLimit int64
}

type TMDBClient struct {
	client clientConfig
	token  string
}

type tmdbName struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type tmdbCountry struct {
	Code string `json:"iso_3166_1"`
	Name string `json:"name"`
}

type tmdbNetwork struct {
	Name          string `json:"name"`
	OriginCountry string `json:"origin_country"`
}

type tmdbCompany struct {
	Name          string `json:"name"`
	OriginCountry string `json:"origin_country"`
}

type tmdbPerson struct {
	Name        string `json:"name"`
	Job         string `json:"job"`
	Department  string `json:"known_for_department"`
	Character   string `json:"character"`
	ProfilePath string `json:"profile_path"`
	Roles       []struct {
		Character string `json:"character"`
	} `json:"roles"`
	Jobs []struct {
		Job string `json:"job"`
	} `json:"jobs"`
}

type tmdbAlias struct {
	Code  string `json:"iso_3166_1"`
	Title string `json:"title"`
	Name  string `json:"name"`
}

type tmdbPayload struct {
	ID                  int64         `json:"id"`
	Title               string        `json:"title"`
	Name                string        `json:"name"`
	OriginalTitle       string        `json:"original_title"`
	OriginalName        string        `json:"original_name"`
	Overview            string        `json:"overview"`
	Tagline             string        `json:"tagline"`
	OriginalLanguage    string        `json:"original_language"`
	Status              string        `json:"status"`
	ReleaseDate         string        `json:"release_date"`
	FirstAirDate        string        `json:"first_air_date"`
	LastAirDate         string        `json:"last_air_date"`
	Runtime             int64         `json:"runtime"`
	EpisodeRunTime      []int64       `json:"episode_run_time"`
	PosterPath          string        `json:"poster_path"`
	BackdropPath        string        `json:"backdrop_path"`
	VoteAverage         float64       `json:"vote_average"`
	VoteCount           int64         `json:"vote_count"`
	NumberOfSeasons     int64         `json:"number_of_seasons"`
	NumberOfEpisodes    int64         `json:"number_of_episodes"`
	Genres              []tmdbName    `json:"genres"`
	ProductionCountries []tmdbCountry `json:"production_countries"`
	OriginCountry       []string      `json:"origin_country"`
	Networks            []tmdbNetwork `json:"networks"`
	ProductionCompanies []tmdbCompany `json:"production_companies"`
	CreatedBy           []tmdbPerson  `json:"created_by"`
	ExternalIDs         struct {
		IMDbID string   `json:"imdb_id"`
		TVDBID stringID `json:"tvdb_id"`
	} `json:"external_ids"`
	Credits struct {
		Cast []tmdbPerson `json:"cast"`
		Crew []tmdbPerson `json:"crew"`
	} `json:"credits"`
	AggregateCredits struct {
		Cast []tmdbPerson `json:"cast"`
		Crew []tmdbPerson `json:"crew"`
	} `json:"aggregate_credits"`
	AlternativeTitles struct {
		Titles  []tmdbAlias `json:"titles"`
		Results []tmdbAlias `json:"results"`
	} `json:"alternative_titles"`
	Keywords struct {
		Keywords []tmdbName `json:"keywords"`
		Results  []tmdbName `json:"results"`
	} `json:"keywords"`
	ReleaseDates struct {
		Results []struct {
			Country string `json:"iso_3166_1"`
			Dates   []struct {
				Certification string `json:"certification"`
				Type          int    `json:"type"`
			} `json:"release_dates"`
		} `json:"results"`
	} `json:"release_dates"`
	ContentRatings struct {
		Results []struct {
			Country string `json:"iso_3166_1"`
			Rating  string `json:"rating"`
		} `json:"results"`
	} `json:"content_ratings"`
}

func NewTMDB(config TMDBConfig, transport http.RoundTripper) (*TMDBClient, error) {
	if strings.TrimSpace(config.AccessToken) == "" {
		return nil, ErrInvalidConfig
	}
	if strings.TrimSpace(config.BaseURL) == "" {
		config.BaseURL = defaultTMDBBaseURL
	}
	client, err := newClientConfig(config.BaseURL, config.AllowHTTP, config.Timeout, config.ResponseLimit, transport)
	if err != nil {
		return nil, err
	}
	return &TMDBClient{client: client, token: strings.TrimSpace(config.AccessToken)}, nil
}

func (client *TMDBClient) Lookup(ctx context.Context, kind catalog.MediaKind, identities []catalog.ExternalIdentity, now time.Time) (Contribution, error) {
	id := verifiedIdentity(identities, "tmdb")
	if !positiveID(id) || kind != catalog.KindMovie && kind != catalog.KindSeries {
		return Contribution{}, ErrIdentityUnavailable
	}
	mediaType, appendFields := "movie", "credits,release_dates,alternative_titles,external_ids,keywords"
	if kind == catalog.KindSeries {
		mediaType, appendFields = "tv", "aggregate_credits,content_ratings,alternative_titles,external_ids,keywords"
	}
	endpoint := client.client.endpoint("/3/" + mediaType + "/" + id)
	query := endpoint.Query()
	query.Set("language", "en-US")
	query.Set("append_to_response", appendFields)
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return Contribution{}, ErrProviderUnavailable
	}
	request.Header.Set("Authorization", "Bearer "+client.token)
	request.Header.Set("Accept", "application/json")
	response, err := client.client.http.Do(request)
	if err != nil {
		return Contribution{}, ErrProviderUnavailable
	}
	var payload tmdbPayload
	if err := client.client.decode(response, &payload); err != nil {
		return Contribution{}, err
	}
	if strconv.FormatInt(payload.ID, 10) != id {
		return Contribution{}, ErrIdentityConflict
	}
	return tmdbContribution(kind, id, payload, identities, now)
}

func tmdbContribution(kind catalog.MediaKind, id string, payload tmdbPayload, existing []catalog.ExternalIdentity, now time.Time) (Contribution, error) {
	runtime := payload.Runtime
	if kind == catalog.KindSeries && runtime == 0 {
		for _, value := range payload.EpisodeRunTime {
			if value > 0 {
				runtime = value
				break
			}
		}
	}
	originalTitle, releaseDate, endDate := payload.OriginalTitle, payload.ReleaseDate, ""
	format, pathKind := "Movie", "movie"
	if kind == catalog.KindSeries {
		originalTitle, releaseDate, endDate, format, pathKind = payload.OriginalName, payload.FirstAirDate, payload.LastAirDate, "TV Series", "tv"
	}
	metadata := &catalog.MediaMetadata{
		OriginalTitle: originalTitle, Overview: payload.Overview, Tagline: payload.Tagline,
		OriginalLanguage: payload.OriginalLanguage, Format: format, Status: payload.Status,
		ReleaseDate: firstDate(releaseDate), EndDate: firstDate(endDate), Certification: tmdbCertification(kind, payload),
		RuntimeMinutes: intPointer(runtime, 1_440), SeasonCount: intPointer(payload.NumberOfSeasons, 10_000), EpisodeCount: uint32Pointer(payload.NumberOfEpisodes),
		PosterURL: tmdbImage("w500", payload.PosterPath), BackdropURL: tmdbImage("w780", payload.BackdropPath),
		Genres: []string{}, Keywords: []string{}, Countries: []string{}, Networks: []catalog.MetadataNetwork{}, Companies: []catalog.MetadataCompany{}, Credits: []catalog.MetadataCredit{}, Aliases: []catalog.MetadataAlias{}, Ratings: []catalog.MetadataRating{}, Events: []catalog.MetadataEvent{},
		Sources: []catalog.MetadataSource{{Provider: "tmdb", ProviderID: id, URL: "https://www.themoviedb.org/" + pathKind + "/" + id, Role: "primary descriptive metadata"}}, ObservedAt: now.UTC(),
	}
	for _, genre := range payload.Genres {
		metadata.Genres = append(metadata.Genres, genre.Name)
	}
	for _, keyword := range append(payload.Keywords.Keywords, payload.Keywords.Results...) {
		metadata.Keywords = append(metadata.Keywords, keyword.Name)
	}
	for _, country := range payload.ProductionCountries {
		metadata.Countries = append(metadata.Countries, country.Code)
	}
	metadata.Countries = append(metadata.Countries, payload.OriginCountry...)
	for _, network := range payload.Networks {
		metadata.Networks = append(metadata.Networks, catalog.MetadataNetwork{Name: network.Name, Country: network.OriginCountry, Kind: "Network or service", Source: "tmdb"})
	}
	for _, company := range payload.ProductionCompanies {
		metadata.Companies = append(metadata.Companies, catalog.MetadataCompany{Name: company.Name, Country: company.OriginCountry, Kind: "Production company", Source: "tmdb"})
	}
	if strings.TrimSpace(originalTitle) != "" {
		metadata.Aliases = append(metadata.Aliases, catalog.MetadataAlias{Name: originalTitle, Language: payload.OriginalLanguage, Source: "tmdb"})
	}
	for _, alias := range append(payload.AlternativeTitles.Titles, payload.AlternativeTitles.Results...) {
		name := alias.Title
		if name == "" {
			name = alias.Name
		}
		metadata.Aliases = append(metadata.Aliases, catalog.MetadataAlias{Name: name, Language: alias.Code, Source: "tmdb"})
	}
	if payload.VoteCount > 0 && payload.VoteAverage >= 0 && payload.VoteAverage <= 10 {
		metadata.Ratings = append(metadata.Ratings, catalog.MetadataRating{Source: "tmdb", Value: payload.VoteAverage, Votes: payload.VoteCount, Provenance: "direct TMDB API"})
	}
	cast, crew := payload.Credits.Cast, payload.Credits.Crew
	if kind == catalog.KindSeries {
		cast, crew = payload.AggregateCredits.Cast, payload.AggregateCredits.Crew
		for _, creator := range payload.CreatedBy {
			creator.Job = "Creator"
			crew = append(crew, creator)
		}
	}
	for _, person := range cast {
		character := person.Character
		if character == "" && len(person.Roles) > 0 {
			character = person.Roles[0].Character
		}
		metadata.Credits = append(metadata.Credits, catalog.MetadataCredit{Name: person.Name, Role: "Cast", Character: character, ProfileURL: tmdbImage("w185", person.ProfilePath), Source: "tmdb"})
		if len(metadata.Credits) >= 30 {
			break
		}
	}
	for _, person := range crew {
		job := person.Job
		if job == "" && len(person.Jobs) > 0 {
			job = person.Jobs[0].Job
		}
		if job == "" {
			job = person.Department
		}
		if !importantCredit(job) {
			continue
		}
		metadata.Credits = append(metadata.Credits, catalog.MetadataCredit{Name: person.Name, Role: job, ProfileURL: tmdbImage("w185", person.ProfilePath), Source: "tmdb"})
		if len(metadata.Credits) >= 50 {
			break
		}
	}
	identities := []catalog.ExternalIdentity{identity("tmdb", id, "tmdb_api")}
	if value := strings.TrimSpace(payload.ExternalIDs.IMDbID); validIdentity("imdb", value) {
		identities = append(identities, identity("imdb", value, "tmdb_api"))
		metadata.Sources = append(metadata.Sources, catalog.MetadataSource{Provider: "imdb", ProviderID: value, URL: "https://www.imdb.com/title/" + value + "/", Role: "verified title identity"})
	}
	if value := string(payload.ExternalIDs.TVDBID); positiveID(value) {
		identities = append(identities, identity("tvdb", value, "tmdb_api"))
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

func tmdbImage(size, path string) string {
	path = strings.TrimSpace(path)
	if path == "" || !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "?#\\") {
		return ""
	}
	return "https://image.tmdb.org/t/p/" + size + path
}

func importantCredit(job string) bool {
	switch strings.ToLower(strings.TrimSpace(job)) {
	case "director", "writer", "screenplay", "creator", "executive producer", "producer", "original music composer":
		return true
	default:
		return false
	}
}

func tmdbCertification(kind catalog.MediaKind, payload tmdbPayload) string {
	if kind == catalog.KindSeries {
		for _, country := range []string{"DK", "US"} {
			for _, item := range payload.ContentRatings.Results {
				if item.Country == country && strings.TrimSpace(item.Rating) != "" {
					return item.Rating
				}
			}
		}
		return ""
	}
	type release struct {
		country, value string
		kind           int
	}
	values := []release{}
	for _, result := range payload.ReleaseDates.Results {
		for _, date := range result.Dates {
			if strings.TrimSpace(date.Certification) != "" {
				values = append(values, release{result.Country, date.Certification, date.Type})
			}
		}
	}
	sort.SliceStable(values, func(i, j int) bool {
		priority := func(v release) int {
			p := 2
			if v.country == "DK" {
				p = 0
			} else if v.country == "US" {
				p = 1
			}
			return p*10 + v.kind
		}
		return priority(values[i]) < priority(values[j])
	})
	if len(values) > 0 {
		return values[0].value
	}
	return ""
}

func identitiesConflict(existing, returned []catalog.ExternalIdentity) bool {
	known := map[string]string{}
	for _, value := range existing {
		if value.State == catalog.IdentityVerified {
			known[strings.ToLower(value.Namespace)] = strings.TrimSpace(value.Value)
		}
	}
	for _, value := range returned {
		if previous := known[strings.ToLower(value.Namespace)]; previous != "" && previous != strings.TrimSpace(value.Value) {
			return true
		}
	}
	return false
}
