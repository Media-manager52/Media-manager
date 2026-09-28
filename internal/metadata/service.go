package metadata

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	metadataproviders "github.com/media-manager/media-manager/internal/adapters/metadataproviders"
	"github.com/media-manager/media-manager/internal/adapters/seerr"
	"github.com/media-manager/media-manager/internal/catalog"
)

var imagePath = regexp.MustCompile(`^/[A-Za-z0-9._/-]{1,500}$`)

type Service struct {
	TMDB  metadataproviders.Lookup
	TVDB  metadataproviders.Lookup
	Simkl metadataproviders.Lookup
}

type Result struct {
	Metadata        *catalog.MediaMetadata     `json:"metadata"`
	Identities      []catalog.ExternalIdentity `json:"identities"`
	EnrichmentState string                     `json:"enrichment_state"`
	ProviderStates  map[string]string          `json:"provider_states"`
}

func (service *Service) Build(ctx context.Context, kind catalog.MediaKind, tmdbID int64, value seerr.Metadata, identities []catalog.ExternalIdentity, now time.Time) (Result, error) {
	// A zero runtime from the provider (or a Seerr fallback) means unknown, not
	// invalid; fromTMDB drops it before NormalizeMetadata runs.
	if tmdbID <= 0 || value.ID != 0 && value.ID != tmdbID {
		return Result{}, catalog.ErrInvalidMetadata
	}
	target := fromTMDB(kind, tmdbID, value, now)
	result := Result{Metadata: target, Identities: append([]catalog.ExternalIdentity(nil), identities...), EnrichmentState: "tmdb_via_seerr", ProviderStates: map[string]string{"tmdb": "seerr_fallback", "tvdb": "not_configured", "simkl": "not_configured", "imdb": "identity_only"}}
	configured := []struct {
		name   string
		lookup metadataproviders.Lookup
	}{{"tmdb", nil}, {"tvdb", nil}, {"simkl", nil}}
	if service != nil {
		configured[0].lookup, configured[1].lookup, configured[2].lookup = service.TMDB, service.TVDB, service.Simkl
	}
	direct := 0
	for _, provider := range configured {
		if provider.lookup == nil {
			continue
		}
		contribution, err := provider.lookup.Lookup(ctx, kind, result.Identities, now)
		if err != nil {
			switch err {
			case metadataproviders.ErrIdentityUnavailable:
				result.ProviderStates[provider.name] = "identity_unavailable"
			case metadataproviders.ErrIdentityConflict:
				result.ProviderStates[provider.name] = "identity_conflict"
			default:
				result.ProviderStates[provider.name] = "unavailable"
			}
			continue
		}
		if contribution.Metadata == nil || identitiesConflict(result.Identities, contribution.Identities) {
			result.ProviderStates[provider.name] = "identity_conflict"
			continue
		}
		if provider.name == "tmdb" {
			fallback := *target
			fallback.Sources = metadataSourcesExcept(target.Sources, "tmdb")
			fallback.Ratings = metadataRatingsExcept(target.Ratings, "tmdb")
			mergeMetadata(contribution.Metadata, &fallback, true)
			target = contribution.Metadata
		} else {
			mergeMetadata(target, contribution.Metadata, true)
		}
		result.Identities = mergeIdentities(result.Identities, contribution.Identities)
		result.ProviderStates[provider.name] = "direct"
		direct++
	}
	addIdentitySources(target, result.Identities)
	if result.ProviderStates["simkl"] == "direct" && hasRating(target.Ratings, "imdb") {
		result.ProviderStates["imdb"] = "via_simkl"
	} else if verifiedIdentity(result.Identities, "imdb") == "" {
		result.ProviderStates["imdb"] = "identity_unavailable"
	}
	if direct > 0 {
		result.EnrichmentState = "direct_provider_enriched"
	}
	normalized, err := catalog.NormalizeMetadata(target, now)
	result.Metadata = normalized
	return result, err
}

func fromTMDB(kind catalog.MediaKind, tmdbID int64, value seerr.Metadata, now time.Time) *catalog.MediaMetadata {
	runtime := value.Runtime
	if runtime != nil && *runtime == 0 {
		// Seerr/TMDB report 0 for titles with no known runtime; treat it as
		// unknown instead of rejecting an otherwise valid request.
		runtime = nil
	}
	if kind == catalog.KindSeries && runtime == nil {
		for _, candidate := range value.EpisodeRunTime {
			if candidate > 0 {
				copy := candidate
				runtime = &copy
				break
			}
		}
	}
	originalTitle, releaseDate, format, pathKind := value.OriginalTitle, value.ReleaseDate, "Movie", "movie"
	if kind == catalog.KindSeries {
		originalTitle, releaseDate, format, pathKind = value.OriginalName, value.FirstAirDate, "TV Series", "tv"
	}
	metadata := &catalog.MediaMetadata{
		OriginalTitle: originalTitle, Overview: value.Overview, OriginalLanguage: value.OriginalLanguage, Format: format, Status: value.Status, ReleaseDate: normalizedDate(releaseDate), RuntimeMinutes: runtime,
		PosterURL: tmdbImage("w500", value.PosterPath), BackdropURL: tmdbImage("w780", value.BackdropPath), Genres: []string{}, Keywords: []string{}, Countries: []string{}, Networks: []catalog.MetadataNetwork{}, Companies: []catalog.MetadataCompany{}, Credits: []catalog.MetadataCredit{}, Aliases: []catalog.MetadataAlias{}, Sources: []catalog.MetadataSource{{Provider: "tmdb", ProviderID: strconv.FormatInt(tmdbID, 10), URL: fmt.Sprintf("https://www.themoviedb.org/%s/%d", pathKind, tmdbID), Role: "primary descriptive metadata through Seerr"}}, Ratings: []catalog.MetadataRating{}, Events: []catalog.MetadataEvent{}, ObservedAt: now.UTC(),
	}
	for _, genre := range value.Genres {
		metadata.Genres = append(metadata.Genres, genre.Name)
	}
	if kind == catalog.KindMovie {
		for _, country := range value.ProductionCountries {
			metadata.Countries = append(metadata.Countries, country.Code)
		}
	} else {
		metadata.Countries = append(metadata.Countries, value.OriginCountry...)
		for _, network := range value.Networks {
			metadata.Networks = append(metadata.Networks, catalog.MetadataNetwork{Name: network.Name, Country: network.OriginCountry, Kind: "Network or service", Source: "tmdb"})
		}
	}
	if strings.TrimSpace(originalTitle) != "" {
		metadata.Aliases = append(metadata.Aliases, catalog.MetadataAlias{Name: originalTitle, Language: value.OriginalLanguage, Source: "tmdb"})
	}
	if value.VoteCount > 0 && value.VoteAverage >= 0 && value.VoteAverage <= 10 {
		metadata.Ratings = append(metadata.Ratings, catalog.MetadataRating{Source: "tmdb", Value: value.VoteAverage, Votes: value.VoteCount, Provenance: "TMDB response through Seerr"})
	}
	return metadata
}

func mergeMetadata(target, incoming *catalog.MediaMetadata, supplement bool) {
	if incoming == nil {
		return
	}
	set := func(current *string, value string) {
		if value != "" && (!supplement || *current == "") {
			*current = value
		}
	}
	set(&target.OriginalTitle, incoming.OriginalTitle)
	set(&target.Overview, incoming.Overview)
	set(&target.Tagline, incoming.Tagline)
	set(&target.OriginalLanguage, incoming.OriginalLanguage)
	set(&target.Format, incoming.Format)
	set(&target.Status, incoming.Status)
	set(&target.ReleaseDate, incoming.ReleaseDate)
	set(&target.EndDate, incoming.EndDate)
	set(&target.Certification, incoming.Certification)
	set(&target.PosterURL, incoming.PosterURL)
	set(&target.BackdropURL, incoming.BackdropURL)
	if incoming.RuntimeMinutes != nil && (!supplement || target.RuntimeMinutes == nil) {
		target.RuntimeMinutes = incoming.RuntimeMinutes
	}
	if incoming.SeasonCount != nil && (!supplement || target.SeasonCount == nil) {
		target.SeasonCount = incoming.SeasonCount
	}
	if incoming.EpisodeCount != nil && (!supplement || target.EpisodeCount == nil) {
		target.EpisodeCount = incoming.EpisodeCount
	}
	target.Genres = capped(append(target.Genres, incoming.Genres...), 100)
	target.Keywords = capped(append(target.Keywords, incoming.Keywords...), 200)
	target.Countries = capped(append(target.Countries, incoming.Countries...), 50)
	target.Networks = capped(append(target.Networks, incoming.Networks...), 50)
	target.Companies = capped(append(target.Companies, incoming.Companies...), 100)
	target.Credits = capped(append(target.Credits, incoming.Credits...), 100)
	target.Aliases = capped(append(target.Aliases, incoming.Aliases...), 100)
	target.Sources = capped(append(target.Sources, incoming.Sources...), 20)
	target.Ratings = capped(append(target.Ratings, incoming.Ratings...), 10)
	target.Events = capped(append(target.Events, incoming.Events...), 200)
}

func mergeIdentities(existing, incoming []catalog.ExternalIdentity) []catalog.ExternalIdentity {
	seen := map[string]string{}
	for _, value := range existing {
		seen[strings.ToLower(value.Namespace)] = value.Value
	}
	for _, value := range incoming {
		namespace := strings.ToLower(strings.TrimSpace(value.Namespace))
		if namespace == "" || value.Value == "" || seen[namespace] != "" {
			continue
		}
		seen[namespace] = value.Value
		existing = append(existing, value)
	}
	sort.Slice(existing, func(i, j int) bool {
		return fmt.Sprintf("%s:%s", existing[i].Namespace, existing[i].Value) < fmt.Sprintf("%s:%s", existing[j].Namespace, existing[j].Value)
	})
	return existing
}

func addIdentitySources(metadata *catalog.MediaMetadata, identities []catalog.ExternalIdentity) {
	for _, identity := range identities {
		if identity.State != catalog.IdentityVerified || identity.Namespace != "imdb" || hasSource(metadata.Sources, "imdb", identity.Value) {
			continue
		}
		metadata.Sources = append(metadata.Sources, catalog.MetadataSource{Provider: "imdb", ProviderID: identity.Value, URL: "https://www.imdb.com/title/" + identity.Value + "/", Role: "verified title identity; direct IMDb metadata not configured"})
	}
}

func hasSource(values []catalog.MetadataSource, provider, id string) bool {
	for _, value := range values {
		if value.Provider == provider && value.ProviderID == id {
			return true
		}
	}
	return false
}
func hasRating(values []catalog.MetadataRating, source string) bool {
	for _, value := range values {
		if value.Source == source {
			return true
		}
	}
	return false
}
func metadataSourcesExcept(values []catalog.MetadataSource, provider string) []catalog.MetadataSource {
	result := make([]catalog.MetadataSource, 0, len(values))
	for _, value := range values {
		if value.Provider != provider {
			result = append(result, value)
		}
	}
	return result
}
func metadataRatingsExcept(values []catalog.MetadataRating, provider string) []catalog.MetadataRating {
	result := make([]catalog.MetadataRating, 0, len(values))
	for _, value := range values {
		if value.Source != provider {
			result = append(result, value)
		}
	}
	return result
}
func verifiedIdentity(values []catalog.ExternalIdentity, namespace string) string {
	for _, value := range values {
		if value.State == catalog.IdentityVerified && value.Namespace == namespace {
			return value.Value
		}
	}
	return ""
}
func identitiesConflict(existing, incoming []catalog.ExternalIdentity) bool {
	known := map[string]string{}
	for _, value := range existing {
		if value.State == catalog.IdentityVerified {
			known[value.Namespace] = value.Value
		}
	}
	for _, value := range incoming {
		if previous := known[value.Namespace]; previous != "" && previous != value.Value {
			return true
		}
	}
	return false
}
func capped[T any](values []T, maximum int) []T {
	if len(values) > maximum {
		return values[:maximum]
	}
	return values
}
func tmdbImage(size, path string) string {
	if !imagePath.MatchString(path) {
		return ""
	}
	return "https://image.tmdb.org/t/p/" + size + path
}
func normalizedDate(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 10 {
		value = value[:10]
	}
	if _, err := time.Parse("2006-01-02", value); err == nil {
		return value
	}
	return ""
}
