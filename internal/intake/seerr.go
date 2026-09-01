package intake

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"
	"uuid"

	seerradapter "github.com/media-manager/media-manager/internal/adapters/seerr"
	"github.com/media-manager/media-manager/internal/catalog"
	metadataservice "github.com/media-manager/media-manager/internal/metadata"
	"github.com/media-manager/media-manager/internal/postgres"
)

type SeerrService struct {
	Client           *seerradapter.Client
	Store            *postgres.Store
	InstanceID       string
	MetadataEnricher *metadataservice.Service
}

func (service *SeerrService) Handle(ctx context.Context, event postgres.OutboxEvent) error {
	var payload struct {
		DeliveryID string `json:"delivery_id"`
		RequestID  string `json:"request_id"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return err
	}
	deliveryID, err := uuid.Parse(payload.DeliveryID)
	if err != nil {
		return err
	}
	requestID, err := strconv.ParseInt(payload.RequestID, 10, 64)
	if err != nil || requestID <= 0 {
		return errors.New("invalid Seerr request ID")
	}
	request, err := service.Client.Request(ctx, requestID)
	if errors.Is(err, seerradapter.ErrRequestNotFound) {
		now := time.Now().UTC()
		if markErr := service.Store.MarkSeerrRequestMissing(ctx, service.InstanceID, payload.RequestID, deliveryID, now); markErr != nil {
			return markErr
		}
		needsAttention, revokeErr := service.Store.ReconcileSeerrRevocation(ctx, service.InstanceID, payload.RequestID, now)
		if revokeErr != nil {
			return revokeErr
		}
		if needsAttention {
			return service.Store.MarkSeerrDelivery(ctx, deliveryID, "needs_attention", "the deleted Seerr request had already started retrieval")
		}
		return service.Store.MarkSeerrDelivery(ctx, deliveryID, "observed_not_approved", "the Seerr request no longer exists")
	}
	if err != nil {
		return err
	}
	intake := postgres.SeerrIntake{InstanceID: service.InstanceID, RequestID: payload.RequestID, UpdatedAt: request.UpdatedAt, RequestState: strconv.Itoa(request.Status), DeliveryID: deliveryID}
	if request.Type == "movie" || request.Media.MediaType == "movie" {
		intake.Kind = catalog.KindMovie
	} else {
		intake.Kind = catalog.KindSeries
	}
	if request.Media.TMDBID != nil && *request.Media.TMDBID > 0 {
		intake.Identities = append(intake.Identities, catalog.ExternalIdentity{Namespace: "tmdb", Value: strconv.FormatInt(*request.Media.TMDBID, 10), State: catalog.IdentityVerified, Source: "seerr"})
	}
	if request.Media.TVDBID != nil && *request.Media.TVDBID > 0 {
		intake.Identities = append(intake.Identities, catalog.ExternalIdentity{Namespace: "tvdb", Value: strconv.FormatInt(*request.Media.TVDBID, 10), State: catalog.IdentityVerified, Source: "seerr"})
	}
	if strings.TrimSpace(request.Media.IMDbID) != "" {
		intake.Identities = append(intake.Identities, catalog.ExternalIdentity{Namespace: "imdb", Value: request.Media.IMDbID, State: catalog.IdentityVerified, Source: "seerr"})
	}
	for _, season := range request.Seasons {
		intake.Seasons = append(intake.Seasons, postgres.SeerrSeason{Number: season.SeasonNumber, Availability: season.Status})
	}
	if request.Media.TMDBID != nil && *request.Media.TMDBID > 0 {
		mediaType := "tv"
		if intake.Kind == catalog.KindMovie {
			mediaType = "movie"
		}
		metadata, metadataErr := service.Client.Metadata(ctx, mediaType, *request.Media.TMDBID)
		if metadataErr != nil {
			return metadataErr
		}
		intake.Title = metadata.Title
		date := metadata.ReleaseDate
		if intake.Kind == catalog.KindSeries {
			intake.Title, date = metadata.Name, metadata.FirstAirDate
		}
		if parsed, parseErr := time.Parse("2006-01-02", date); parseErr == nil {
			year := uint16(parsed.Year())
			intake.Year = &year
		}
		if metadata.ExternalIDs.TVDBID != nil && !hasIdentity(intake.Identities, "tvdb") {
			intake.Identities = append(intake.Identities, catalog.ExternalIdentity{Namespace: "tvdb", Value: strconv.FormatInt(*metadata.ExternalIDs.TVDBID, 10), State: catalog.IdentityVerified, Source: "seerr_metadata"})
		}
		if metadata.ExternalIDs.IMDbID != "" && !hasIdentity(intake.Identities, "imdb") {
			intake.Identities = append(intake.Identities, catalog.ExternalIdentity{Namespace: "imdb", Value: metadata.ExternalIDs.IMDbID, State: catalog.IdentityVerified, Source: "seerr_metadata"})
		}
		enricher := service.MetadataEnricher
		if enricher == nil {
			enricher = &metadataservice.Service{}
		}
		enriched, enrichErr := enricher.Build(ctx, intake.Kind, *request.Media.TMDBID, metadata, intake.Identities, time.Now().UTC())
		if enrichErr != nil {
			return enrichErr
		}
		intake.Metadata = enriched.Metadata
		intake.Identities = enriched.Identities
		if intake.Kind == catalog.KindSeries {
			for seasonIndex := range intake.Seasons {
				details, detailsErr := service.Client.SeasonMetadata(ctx, *request.Media.TMDBID, intake.Seasons[seasonIndex].Number)
				if detailsErr != nil {
					return detailsErr
				}
				for _, episode := range details.Episodes {
					if episode.EpisodeNumber <= 0 {
						continue
					}
					intake.Seasons[seasonIndex].Episodes = append(intake.Seasons[seasonIndex].Episodes, postgres.SeerrEpisode{Number: episode.EpisodeNumber, ProviderID: "tmdb:" + strconv.FormatInt(episode.ID, 10), Title: episode.Name, AirDate: episode.AirDate})
				}
			}
		}
	}
	intake.PayloadHash = seerrIntakeHash(request, intake)
	accepted, err := service.Store.UpsertSeerrSnapshot(ctx, intake)
	if errors.Is(err, postgres.ErrSeerrSnapshotConflict) {
		return service.Store.MarkSeerrDelivery(ctx, deliveryID, "needs_attention", "Seerr changed a request without a newer update cursor")
	}
	if err != nil {
		return err
	}
	if !accepted {
		return service.Store.MarkSeerrDelivery(ctx, deliveryID, "stale_ignored", "an unchanged or newer Seerr snapshot was already reconciled")
	}
	if request.Status != 2 {
		needsAttention, revokeErr := service.Store.ReconcileSeerrRevocation(ctx, intake.InstanceID, intake.RequestID, time.Now().UTC())
		if revokeErr != nil {
			return revokeErr
		}
		if needsAttention {
			return service.Store.MarkSeerrDelivery(ctx, deliveryID, "needs_attention", "the request changed after retrieval started")
		}
		return service.Store.MarkSeerrDelivery(ctx, deliveryID, "observed_not_approved", "")
	}
	if request.Media.Status == 5 {
		return service.Store.MarkSeerrDelivery(ctx, deliveryID, "already_available", "")
	}
	if intake.Kind == catalog.KindSeries {
		actionable := intake.Seasons[:0]
		for _, season := range intake.Seasons {
			if season.Availability != 5 {
				actionable = append(actionable, season)
			}
		}
		intake.Seasons = actionable
		if len(intake.Seasons) == 0 {
			return service.Store.MarkSeerrDelivery(ctx, deliveryID, "already_available", "all requested seasons are available")
		}
		for _, season := range intake.Seasons {
			if len(season.Episodes) == 0 {
				return service.Store.MarkSeerrDelivery(ctx, deliveryID, "needs_attention", "an actionable season has no exact episode snapshot")
			}
		}
	}
	if intake.Title == "" || intake.Year == nil || !hasPublicationIdentity(intake.Kind, intake.Identities) {
		return service.Store.MarkSeerrDelivery(ctx, deliveryID, "needs_attention", "title, year, or verified Plex-supported identity is missing")
	}
	if _, err := service.Store.EnsureMediaFromSeerr(ctx, intake, time.Now().UTC()); errors.Is(err, postgres.ErrDefaultsMissing) {
		return service.Store.MarkSeerrDelivery(ctx, deliveryID, "needs_configuration", "default root or quality profile is missing")
	} else if errors.Is(err, postgres.ErrIdentityNeedsAttention) {
		return service.Store.MarkSeerrDelivery(ctx, deliveryID, "needs_attention", "verified identities resolve to different media")
	} else if errors.Is(err, postgres.ErrMediaRecycled) {
		return service.Store.MarkSeerrDelivery(ctx, deliveryID, "needs_attention", "matching media is recycled; restore it before requesting again")
	} else if errors.Is(err, postgres.ErrSeerrSnapshotDrift) {
		return service.Store.MarkSeerrDelivery(ctx, deliveryID, "needs_attention", "Seerr changed the exact episode snapshot; review the active season workflow")
	} else if errors.Is(err, postgres.ErrAdoptedReplacementConfirmationRequired) {
		return service.Store.MarkSeerrDelivery(ctx, deliveryID, "needs_attention", "adopted current files require the owner-confirmed replacement workflow in Media Manager")
	} else if errors.Is(err, postgres.ErrAdoptionMonitoringReserved) {
		return service.Store.MarkSeerrDelivery(ctx, deliveryID, "needs_attention", "reviewed adoption monitoring is waiting for Plex verification")
	} else if err != nil {
		return err
	}
	return service.Store.MarkSeerrDelivery(ctx, deliveryID, "media_manager_added", "")
}

func seerrIntakeHash(request seerradapter.Request, intake postgres.SeerrIntake) []byte {
	identities := append([]catalog.ExternalIdentity(nil), intake.Identities...)
	sort.Slice(identities, func(i, j int) bool {
		if identities[i].Namespace == identities[j].Namespace {
			return identities[i].Value < identities[j].Value
		}
		return identities[i].Namespace < identities[j].Namespace
	})
	seasons := append([]postgres.SeerrSeason(nil), intake.Seasons...)
	for index := range seasons {
		seasons[index].Episodes = append([]postgres.SeerrEpisode(nil), seasons[index].Episodes...)
		sort.Slice(seasons[index].Episodes, func(i, j int) bool { return seasons[index].Episodes[i].Number < seasons[index].Episodes[j].Number })
	}
	sort.Slice(seasons, func(i, j int) bool { return seasons[i].Number < seasons[j].Number })
	canonical, _ := json.Marshal(struct {
		Request    seerradapter.Request       `json:"request"`
		Title      string                     `json:"title"`
		Year       *uint16                    `json:"year"`
		Identities []catalog.ExternalIdentity `json:"identities"`
		Seasons    []postgres.SeerrSeason     `json:"seasons"`
	}{Request: request, Title: intake.Title, Year: intake.Year, Identities: identities, Seasons: seasons})
	digest := sha256.Sum256(canonical)
	return digest[:]
}

func hasPublicationIdentity(kind catalog.MediaKind, identities []catalog.ExternalIdentity) bool {
	for _, identity := range identities {
		if identity.State != catalog.IdentityVerified {
			continue
		}
		if kind == catalog.KindMovie && (identity.Namespace == "tmdb" || identity.Namespace == "imdb") {
			return true
		}
		if kind == catalog.KindSeries && (identity.Namespace == "tvdb" || identity.Namespace == "tmdb") {
			return true
		}
	}
	return false
}

func hasIdentity(identities []catalog.ExternalIdentity, namespace string) bool {
	for _, identity := range identities {
		if identity.Namespace == namespace {
			return true
		}
	}
	return false
}
