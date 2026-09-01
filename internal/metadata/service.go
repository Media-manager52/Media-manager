package metadata

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/gmd"
	"github.com/media-manager/media-manager/internal/adapters/seerr"
	"github.com/media-manager/media-manager/internal/catalog"
)

var imagePath = regexp.MustCompile(`^/[A-Za-z0-9._/-]{1,500}$`)

type GMDLookup interface {
	Lookup(context.Context, string, []catalog.ExternalIdentity) (gmd.Detail, bool, error)
}

type Service struct {
	GMD GMDLookup
}

type Result struct {
	Metadata        *catalog.MediaMetadata
	Identities      []catalog.ExternalIdentity
	EnrichmentState string
}

func (service *Service) Build(ctx context.Context, kind catalog.MediaKind, tmdbID int64, value seerr.Metadata, identities []catalog.ExternalIdentity, now time.Time) (Result, error) {
	if tmdbID <= 0 || value.ID != 0 && value.ID != tmdbID {
		return Result{}, catalog.ErrInvalidMetadata
	}
	metadata := fromTMDB(kind, tmdbID, value, now)
	result := Result{Metadata: metadata, Identities: append([]catalog.ExternalIdentity(nil), identities...), EnrichmentState: "tmdb_only"}
	if kind != catalog.KindSeries {
		normalized, err := catalog.NormalizeMetadata(metadata, now)
		result.Metadata = normalized
		return result, err
	}
	if service == nil || service.GMD == nil {
		result.EnrichmentState = "gmd_not_configured"
		normalized, err := catalog.NormalizeMetadata(metadata, now)
		result.Metadata = normalized
		return result, err
	}
	title := strings.TrimSpace(value.Name)
	detail, matched, err := service.GMD.Lookup(ctx, title, identities)
	if err != nil {
		result.EnrichmentState = "gmd_unavailable"
		normalized, normalizeErr := catalog.NormalizeMetadata(metadata, now)
		result.Metadata = normalized
		return result, normalizeErr
	}
	if !matched {
		result.EnrichmentState = "gmd_no_exact_identity_match"
		normalized, normalizeErr := catalog.NormalizeMetadata(metadata, now)
		result.Metadata = normalized
		return result, normalizeErr
	}
	mergeGMD(metadata, detail)
	result.Identities = mergeIdentities(result.Identities, detail.ExternalIDs)
	result.EnrichmentState = "gmd_exact_identity_match"
	normalized, err := catalog.NormalizeMetadata(metadata, now)
	result.Metadata = normalized
	return result, err
}

func fromTMDB(kind catalog.MediaKind, tmdbID int64, value seerr.Metadata, now time.Time) *catalog.MediaMetadata {
	runtime := value.Runtime
	if kind == catalog.KindSeries && runtime == nil {
		for _, candidate := range value.EpisodeRunTime {
			if candidate > 0 {
				copy := candidate
				runtime = &copy
				break
			}
		}
	}
	originalTitle := value.OriginalTitle
	if kind == catalog.KindSeries {
		originalTitle = value.OriginalName
	}
	metadata := &catalog.MediaMetadata{
		OriginalTitle:    originalTitle,
		Overview:         value.Overview,
		OriginalLanguage: value.OriginalLanguage,
		Format:           map[catalog.MediaKind]string{catalog.KindMovie: "Movie", catalog.KindSeries: "TV Series"}[kind],
		Status:           value.Status,
		RuntimeMinutes:   runtime,
		PosterURL:        tmdbImage("w500", value.PosterPath),
		BackdropURL:      tmdbImage("w780", value.BackdropPath),
		Genres:           make([]string, 0, len(value.Genres)),
		Countries:        []string{},
		Networks:         []catalog.MetadataNetwork{},
		Aliases:          []catalog.MetadataAlias{},
		Sources: []catalog.MetadataSource{{
			Provider:   "tmdb",
			ProviderID: strconv.FormatInt(tmdbID, 10),
			URL:        tmdbURL(kind, tmdbID),
			Role:       "primary descriptive metadata",
		}},
		Ratings:    []catalog.MetadataRating{},
		Events:     []catalog.MetadataEvent{},
		ObservedAt: now.UTC(),
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
		metadata.Ratings = append(metadata.Ratings, catalog.MetadataRating{Source: "tmdb", Value: value.VoteAverage, Votes: value.VoteCount})
	}
	return metadata
}

func mergeGMD(target *catalog.MediaMetadata, detail gmd.Detail) {
	if target.OriginalTitle == "" {
		target.OriginalTitle = detail.Title.OriginalName
	}
	if target.Overview == "" {
		target.Overview = detail.Title.Overview
	}
	if target.OriginalLanguage == "" {
		target.OriginalLanguage = detail.Title.Language
	}
	if detail.Title.Format != "" {
		target.Format = detail.Title.Format
	}
	if target.Status == "" {
		target.Status = detail.Title.Status
	}
	if target.RuntimeMinutes == nil {
		target.RuntimeMinutes = detail.Title.RuntimeMinutes
	}
	if target.PosterURL == "" {
		target.PosterURL = detail.Title.PosterURL
	}
	if target.BackdropURL == "" {
		target.BackdropURL = detail.Title.BackdropURL
	}
	target.Genres = append(target.Genres, detail.Genres...)
	target.Countries = append(target.Countries, detail.Countries...)
	for _, network := range detail.Networks {
		target.Networks = append(target.Networks, catalog.MetadataNetwork{Name: network.Name, Country: network.Country, Kind: network.Type, Source: network.Source})
	}
	for _, alias := range detail.Aliases {
		target.Aliases = append(target.Aliases, catalog.MetadataAlias{Name: alias.Name, Language: alias.Language, Source: alias.Source})
	}
	for _, identity := range detail.ExternalIDs {
		role := "identity evidence"
		if identity.Source == "tvdb" {
			role = "supplementary TV metadata"
		} else if identity.Source == "simkl" {
			role = "schedule evidence"
		} else if identity.Source != "tmdb" {
			continue
		}
		target.Sources = append(target.Sources, catalog.MetadataSource{Provider: identity.Source, ProviderID: identity.ID, URL: identity.URL, Role: role})
	}
	evidenceSources := map[string][]string{}
	for _, evidence := range detail.Evidence {
		if strings.TrimSpace(evidence.Source) != "" {
			evidenceSources[detail.EventID] = append(evidenceSources[detail.EventID], evidence.Source)
		}
	}
	for _, event := range detail.Events {
		sources := evidenceSources[event.ID]
		if len(sources) == 0 {
			sources = []string{"gmd"}
		}
		target.Events = append(target.Events, catalog.MetadataEvent{Type: event.Type, Date: event.Date, SeasonNumber: optionalEventNumber(event.SeasonNumber), EpisodeNumber: optionalEventNumber(event.EpisodeNumber), Country: event.Country, Network: event.Network, Confidence: event.Confidence, DateConflict: event.DateConflict == 1, Sources: sources})
	}
}

func optionalEventNumber(value int32) *uint16 {
	if value < 0 || value > 65_535 {
		return nil
	}
	converted := uint16(value)
	return &converted
}

func mergeIdentities(existing []catalog.ExternalIdentity, values []gmd.ExternalID) []catalog.ExternalIdentity {
	byNamespace := map[string]string{}
	for _, identity := range existing {
		byNamespace[identity.Namespace] = identity.Value
	}
	for _, identity := range values {
		namespace := strings.ToLower(strings.TrimSpace(identity.Source))
		value := strings.TrimSpace(identity.ID)
		if namespace != "tmdb" && namespace != "tvdb" && namespace != "simkl" || value == "" {
			continue
		}
		if previous, present := byNamespace[namespace]; present {
			if previous != value {
				continue
			}
			continue
		}
		byNamespace[namespace] = value
		existing = append(existing, catalog.ExternalIdentity{Namespace: namespace, Value: value, State: catalog.IdentityVerified, Source: "gmd_identity"})
	}
	sort.Slice(existing, func(i, j int) bool {
		return fmt.Sprintf("%s:%s", existing[i].Namespace, existing[i].Value) < fmt.Sprintf("%s:%s", existing[j].Namespace, existing[j].Value)
	})
	return existing
}

func tmdbImage(size, path string) string {
	if !imagePath.MatchString(path) {
		return ""
	}
	return "https://image.tmdb.org/t/p/" + size + path
}

func tmdbURL(kind catalog.MediaKind, id int64) string {
	mediaType := "movie"
	if kind == catalog.KindSeries {
		mediaType = "tv"
	}
	return fmt.Sprintf("https://www.themoviedb.org/%s/%d", mediaType, id)
}
