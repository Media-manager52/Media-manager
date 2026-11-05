package metadata

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/metadataproviders"
	"github.com/media-manager/media-manager/internal/adapters/seerr"
	"github.com/media-manager/media-manager/internal/catalog"
)

// SeerrSource is the Seerr metadata lookup Discover needs.
type SeerrSource interface {
	Metadata(ctx context.Context, mediaType string, tmdbID int64) (seerr.Metadata, error)
}

// DiscoveryProvider finds titles on TMDB: Seerr's TMDB proxy, or TMDB itself.
type DiscoveryProvider interface {
	Search(ctx context.Context, query string, page int) (seerr.SearchPage, error)
	Metadata(ctx context.Context, mediaType string, tmdbID int64) (seerr.Metadata, error)
	SeasonMetadata(ctx context.Context, tmdbID int64, season int16) (seerr.SeasonMetadata, error)
}

// OptionalDiscovery is a DiscoveryProvider whose credential can be missing.
type OptionalDiscovery interface {
	DiscoveryProvider
	Configured(ctx context.Context) bool
}

// CheckTMDBToken asks TMDB whether token is a valid read access token.
func CheckTMDBToken(ctx context.Context, token string) error {
	client, err := metadataproviders.NewTMDB(metadataproviders.TMDBConfig{AccessToken: token}, nil)
	if err != nil {
		return err
	}
	return client.Check(ctx)
}

// SourceLabel names where discovery metadata came from, for the identities
// it proves: "seerr_metadata" for Seerr's TMDB proxy, or the source's own
// label (TMDB queried directly says "tmdb_metadata").
func SourceLabel(source any) string {
	if labelled, ok := source.(interface{ MetadataSourceLabel() string }); ok {
		return labelled.MetadataSourceLabel()
	}
	return "seerr_metadata"
}

// Discovered is a movie or series identified on TMDB through Seerr, with
// verified identities and normalized metadata, ready to add to the catalogue.
type Discovered struct {
	Kind       catalog.MediaKind
	Title      string
	Year       *uint16
	TMDBID     int64
	Identities []catalog.ExternalIdentity
	Result     Result
}

// Discover looks a TMDB id up through Seerr and builds the verified identity
// set and normalized metadata the owner console's "Add media" uses. mediaType
// is "movie" or "tv".
func (service *Service) Discover(ctx context.Context, source SeerrSource, mediaType string, tmdbID int64, now time.Time) (Discovered, error) {
	if mediaType != "movie" && mediaType != "tv" {
		return Discovered{}, errors.New("metadata kind must be movie or tv")
	}
	if source == nil || tmdbID <= 0 {
		return Discovered{}, errors.New("Seerr metadata discovery is not configured")
	}
	value, err := source.Metadata(ctx, mediaType, tmdbID)
	if err != nil {
		return Discovered{}, err
	}
	title, date, kind := value.Title, value.ReleaseDate, catalog.KindMovie
	if mediaType == "tv" {
		title, date, kind = value.Name, value.FirstAirDate, catalog.KindSeries
	}
	label := SourceLabel(source)
	identities := []catalog.ExternalIdentity{{Namespace: "tmdb", Value: strconv.FormatInt(tmdbID, 10), State: catalog.IdentityVerified, Source: label}}
	if value.ExternalIDs.TVDBID != nil && *value.ExternalIDs.TVDBID > 0 {
		identities = append(identities, catalog.ExternalIdentity{Namespace: "tvdb", Value: strconv.FormatInt(*value.ExternalIDs.TVDBID, 10), State: catalog.IdentityVerified, Source: label})
	}
	if strings.TrimSpace(value.ExternalIDs.IMDbID) != "" {
		identities = append(identities, catalog.ExternalIdentity{Namespace: "imdb", Value: value.ExternalIDs.IMDbID, State: catalog.IdentityVerified, Source: label})
	}
	enricher := service
	if enricher == nil {
		enricher = &Service{}
	}
	result, err := enricher.Build(ctx, kind, tmdbID, value, identities, now)
	if err != nil {
		return Discovered{}, err
	}
	discovered := Discovered{Kind: kind, Title: title, TMDBID: tmdbID, Identities: result.Identities, Result: result}
	if parsed, parseErr := time.Parse("2006-01-02", date); parseErr == nil && parsed.Year() >= 1800 && parsed.Year() <= 9999 {
		year := uint16(parsed.Year())
		discovered.Year = &year
	}
	return discovered, nil
}
