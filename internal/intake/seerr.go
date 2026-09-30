package intake

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
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
	intake := postgres.SeerrIntake{InstanceID: service.InstanceID, RequestID: payload.RequestID, UpdatedAt: request.UpdatedAt, RequestedAt: request.CreatedAt, RequestState: strconv.Itoa(request.Status), DeliveryID: deliveryID, ServerID: request.ServerID, ProfileID: request.ProfileID, RootFolder: strings.TrimSpace(request.RootFolder), Is4K: request.Is4K}
	if request.RequestedBy != nil {
		intake.Requester = strings.TrimSpace(request.RequestedBy.DisplayName)
	}
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
		intake.PosterURL = enriched.Metadata.PosterURL
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
		return service.Store.MarkSeerrDelivery(ctx, deliveryID, "needs_attention", "Seerr request is missing its update cursor")
	}
	if err != nil {
		return err
	}
	if !accepted {
		storedUpdated, storedHash, cursorErr := service.Store.SeerrSnapshotCursor(ctx, service.InstanceID, payload.RequestID)
		if cursorErr != nil {
			return cursorErr
		}
		if !storedUpdated.Equal(request.UpdatedAt.UTC()) || !bytes.Equal(storedHash, intake.PayloadHash) {
			return service.Store.MarkSeerrDelivery(ctx, deliveryID, "stale_ignored", "an unchanged or newer Seerr snapshot was already reconciled")
		}
		hasWorkflow, workflowErr := service.Store.SeerrRequestHasAnyWorkflow(ctx, service.InstanceID, payload.RequestID)
		if workflowErr != nil {
			return workflowErr
		}
		if hasWorkflow {
			return service.Store.MarkSeerrDelivery(ctx, deliveryID, "stale_ignored", "an unchanged or newer Seerr snapshot was already reconciled")
		}
		// The stored snapshot is current but no workflow was ever created from
		// it (for example a request stranded by an earlier intake conflict), so
		// evaluate it again instead of ignoring it forever.
	}
	switch seerrRequestDisposition(request.Status) {
	case seerrRequestCompleted:
		// Seerr completes a request itself once it considers the media available
		// (its own Plex or *arr scan, or Media Manager's availability sync). That
		// is not a revocation: linked workflows keep their state and no new work
		// is created.
		return service.Store.MarkSeerrDelivery(ctx, deliveryID, "already_available", "Seerr reports the request completed")
	case seerrRequestWithdrawn:
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
	result, err := service.Store.EnsureMediaFromSeerr(ctx, intake, time.Now().UTC())
	if errors.Is(err, postgres.ErrDefaultsMissing) {
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
	} else if errors.Is(err, postgres.ErrProfileRoutingAmbiguous) {
		return service.Store.MarkSeerrDelivery(ctx, deliveryID, "needs_configuration", "multiple enabled quality profiles route this original language")
	} else if errors.Is(err, postgres.ErrSeerrRouteUnmapped) {
		return service.Store.MarkSeerrDelivery(ctx, deliveryID, "needs_configuration", fmt.Sprintf("Seerr chose root folder %q; map it to a Media Manager library root in Settings → Connections", intake.RootFolder))
	} else if errors.Is(err, postgres.ErrSeerrRouteTargetUnavailable) {
		return service.Store.MarkSeerrDelivery(ctx, deliveryID, "needs_configuration", fmt.Sprintf("the Media Manager root or quality profile mapped to Seerr folder %q is disabled or unavailable", intake.RootFolder))
	} else if err != nil {
		return err
	}
	if result.AlreadyAvailable {
		return service.Store.MarkSeerrDelivery(ctx, deliveryID, "already_available", "matching Plex-confirmed media already exists")
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
	// Routing fields are excluded so adding routing provenance does not change
	// the stored payload hash of existing Seerr snapshots. A folder cannot
	// change after a Seerr request is created.
	hashable := request
	hashable.ServerID, hashable.ProfileID, hashable.RootFolder = 0, 0, ""
	canonical, _ := json.Marshal(struct {
		Request    seerradapter.Request       `json:"request"`
		Title      string                     `json:"title"`
		Year       *uint16                    `json:"year"`
		Identities []catalog.ExternalIdentity `json:"identities"`
		Seasons    []postgres.SeerrSeason     `json:"seasons"`
	}{Request: hashable, Title: intake.Title, Year: intake.Year, Identities: identities, Seasons: seasons})
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

type seerrRequestAction int

const (
	seerrRequestAdmit seerrRequestAction = iota
	seerrRequestCompleted
	seerrRequestWithdrawn
)

// seerrRequestDisposition classifies a Seerr request status for intake:
// approved requests are admitted, completed ones are already available, and
// every other status (pending, declined, failed, unknown) withdraws the work.
func seerrRequestDisposition(status int) seerrRequestAction {
	switch status {
	case seerradapter.RequestStatusApproved:
		return seerrRequestAdmit
	case seerradapter.RequestStatusCompleted:
		return seerrRequestCompleted
	}
	return seerrRequestWithdrawn
}
