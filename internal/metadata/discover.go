package metadata

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/seerr"
	"github.com/media-manager/media-manager/internal/catalog"
)

// SeerrSource is the Seerr metadata lookup Discover needs.
type SeerrSource interface {
	Metadata(ctx context.Context, mediaType string, tmdbID int64) (seerr.Metadata, error)
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
	identities := []catalog.ExternalIdentity{{Namespace: "tmdb", Value: strconv.FormatInt(tmdbID, 10), State: catalog.IdentityVerified, Source: "seerr_metadata"}}
	if value.ExternalIDs.TVDBID != nil && *value.ExternalIDs.TVDBID > 0 {
		identities = append(identities, catalog.ExternalIdentity{Namespace: "tvdb", Value: strconv.FormatInt(*value.ExternalIDs.TVDBID, 10), State: catalog.IdentityVerified, Source: "seerr_metadata"})
	}
	if strings.TrimSpace(value.ExternalIDs.IMDbID) != "" {
		identities = append(identities, catalog.ExternalIdentity{Namespace: "imdb", Value: value.ExternalIDs.IMDbID, State: catalog.IdentityVerified, Source: "seerr_metadata"})
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
