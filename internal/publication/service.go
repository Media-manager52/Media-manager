package publication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/library"
	"github.com/media-manager/media-manager/internal/postgres"
)

type Service struct {
	Store *postgres.Store
}

type altMountConfig struct {
	LocalReadOnlyRoot string `json:"local_read_only_root"`
}

func (service *Service) Handle(ctx context.Context, event postgres.OutboxEvent) error {
	var payload struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return err
	}
	jobID, err := uuid.Parse(payload.JobID)
	if err != nil {
		return errors.New("publication request has no valid AltMount job")
	}
	publicationContext, err := service.Store.LoadPublicationContext(ctx, event.AggregateID, jobID)
	if err != nil {
		if errors.Is(err, postgres.ErrConcurrentModification) {
			return nil
		}
		return err
	}
	if event.AggregateRevision != publicationContext.Workflow.Revision {
		return nil
	}
	if publicationContext.Workflow.State == "plex_scanning" || publicationContext.Workflow.State == "plex_confirmed" {
		return nil
	}
	plans, linkPlans, err := buildPlans(publicationContext)
	if err != nil {
		if attentionErr := service.Store.FailPublication(ctx, publicationContext.Workflow.ID, jobID, err.Error(), time.Now().UTC()); attentionErr != nil {
			return attentionErr
		}
		return nil
	}
	if _, err := library.PreflightBatch(linkPlans); err != nil {
		if attentionErr := service.Store.FailPublication(ctx, publicationContext.Workflow.ID, jobID, "publication preflight failed: "+err.Error(), time.Now().UTC()); attentionErr != nil {
			return attentionErr
		}
		return nil
	}
	if _, err := service.Store.PreparePublication(ctx, publicationContext.Workflow.ID, jobID, plans, time.Now().UTC()); err != nil {
		return err
	}
	if err := service.Store.MarkPublicationPublishing(ctx, publicationContext.Workflow.ID, jobID, time.Now().UTC()); err != nil {
		return err
	}
	if err := library.PublishBatch(linkPlans); err != nil {
		if attentionErr := service.Store.FailPublication(ctx, publicationContext.Workflow.ID, jobID, "symlink publication failed: "+err.Error(), time.Now().UTC()); attentionErr != nil {
			return attentionErr
		}
		return nil
	}
	_, err = service.Store.CompletePublication(ctx, publicationContext.Workflow.ID, jobID, time.Now().UTC())
	return err
}

func buildPlans(publicationContext postgres.PublicationContext) ([]postgres.PublicationPlan, []library.LinkPlan, error) {
	if publicationContext.Media.Year == nil || *publicationContext.Media.Year == 0 {
		return nil, nil, errors.New("media year is required for Plex-safe naming")
	}
	provider, providerID, err := namingIdentity(publicationContext.Media)
	if err != nil {
		return nil, nil, err
	}
	var config altMountConfig
	if err := json.Unmarshal(publicationContext.AltMount.Config, &config); err != nil {
		return nil, nil, errors.New("AltMount path mapping is invalid")
	}
	if !filepath.IsAbs(config.LocalReadOnlyRoot) {
		return nil, nil, errors.New("AltMount read-only root must be absolute")
	}
	jobSourceRoot := filepath.Clean(config.LocalReadOnlyRoot)
	jobPlexSourceRoot := filepath.Clean(publicationContext.Root.SourceVisibleRoot)
	inventoryPrefix := ""
	if publicationContext.InventoryRootRelative != "" && publicationContext.InventoryRootRelative != "." {
		inventoryPrefix, err = library.ValidateRelative(publicationContext.InventoryRootRelative)
		if err != nil {
			return nil, nil, errors.New("AltMount completed output root is unsafe")
		}
		jobSourceRoot = filepath.Join(jobSourceRoot, inventoryPrefix)
		jobPlexSourceRoot = filepath.Join(jobPlexSourceRoot, inventoryPrefix)
	}
	plans := make([]postgres.PublicationPlan, 0, len(publicationContext.Assets))
	links := make([]library.LinkPlan, 0, len(publicationContext.Assets))
	technical := append([]string(nil), publicationContext.Technical...)
	if publicationContext.ReleaseToken != "" {
		technical = append(technical, publicationContext.ReleaseToken)
	}
	for _, asset := range publicationContext.Assets {
		containedSource, sourceErr := library.ValidateRelative(asset.RelativePath)
		if sourceErr != nil {
			return nil, nil, fmt.Errorf("unsafe AltMount source path: %w", sourceErr)
		}
		persistedSource := containedSource
		if inventoryPrefix != "" {
			persistedSource = filepath.Join(inventoryPrefix, containedSource)
		}
		persistedSource, err = library.ValidateRelative(persistedSource)
		if err != nil {
			return nil, nil, fmt.Errorf("unsafe AltMount source path: %w", err)
		}
		extension := strings.TrimPrefix(strings.ToLower(filepath.Ext(asset.RelativePath)), ".")
		var destinationRelative string
		if publicationContext.Media.Kind == catalog.KindMovie {
			folder, file, renderErr := library.RenderMovie(library.MovieName{Title: publicationContext.Media.Title, Year: *publicationContext.Media.Year, Provider: provider, ProviderID: providerID, Technical: technical, Extension: extension})
			if renderErr != nil {
				return nil, nil, renderErr
			}
			destinationRelative, err = library.DestinationRelative(folder, file)
		} else {
			if len(asset.Episodes) == 0 {
				return nil, nil, errors.New("season-pack asset has no verified episode coverage")
			}
			sort.Slice(asset.Episodes, func(i, j int) bool { return asset.Episodes[i].Key.Episode < asset.Episodes[j].Key.Episode })
			season := asset.Episodes[0].Key.Season
			episodes := make([]uint16, 0, len(asset.Episodes))
			for _, episode := range asset.Episodes {
				if episode.Key.Season != season {
					return nil, nil, errors.New("one season-pack asset spans multiple seasons")
				}
				episodes = append(episodes, episode.Key.Episode)
			}
			title := ""
			if len(asset.Episodes) == 1 {
				title = asset.Episodes[0].Title
			}
			folder, seasonFolder, file, renderErr := library.RenderEpisodeSet(publicationContext.Media.Title, *publicationContext.Media.Year, provider, providerID, season, episodes, title, technical, extension)
			if renderErr != nil {
				return nil, nil, renderErr
			}
			destinationRelative, err = library.DestinationRelative(folder, seasonFolder, file)
		}
		if err != nil {
			return nil, nil, err
		}
		link := library.LinkPlan{SourceRoot: jobSourceRoot, SourceRelative: containedSource, DestinationRoot: filepath.Clean(publicationContext.Root.DestinationRoot), DestinationRel: destinationRelative, PlexVisibleSourceRoot: jobPlexSourceRoot}
		validated, err := library.ValidatePlan(link)
		if err != nil {
			return nil, nil, err
		}
		plan := postgres.PublicationPlan{AssetID: asset.ID, DestinationRelative: destinationRelative, SourceRelative: persistedSource, PlexVisibleTarget: validated.PlexVisibleTarget}
		links = append(links, validated)
		for _, companion := range asset.Companions {
			containedCompanion, companionErr := library.ValidateRelative(companion.RelativePath)
			if companionErr != nil {
				return nil, nil, fmt.Errorf("unsafe AltMount subtitle source path: %w", companionErr)
			}
			persistedCompanion := containedCompanion
			if inventoryPrefix != "" {
				persistedCompanion = filepath.Join(inventoryPrefix, containedCompanion)
			}
			persistedCompanion, err = library.ValidateRelative(persistedCompanion)
			if err != nil {
				return nil, nil, fmt.Errorf("unsafe AltMount subtitle source path: %w", err)
			}
			companionDestination, destinationErr := subtitleDestination(destinationRelative, companion)
			if destinationErr != nil {
				return nil, nil, destinationErr
			}
			companionLink, validateErr := library.ValidatePlan(library.LinkPlan{SourceRoot: jobSourceRoot, SourceRelative: containedCompanion, DestinationRoot: filepath.Clean(publicationContext.Root.DestinationRoot), DestinationRel: companionDestination, PlexVisibleSourceRoot: jobPlexSourceRoot})
			if validateErr != nil {
				return nil, nil, validateErr
			}
			plan.Companions = append(plan.Companions, postgres.PublicationCompanionPlan{AssetID: companion.AssetID, Language: companion.Language, Forced: companion.Forced, HearingImpaired: companion.HearingImpaired, DestinationRelative: companionDestination, SourceRelative: persistedCompanion, PlexVisibleTarget: companionLink.PlexVisibleTarget})
			links = append(links, companionLink)
		}
		plans = append(plans, plan)
	}
	return plans, links, nil
}

func subtitleDestination(videoDestination string, companion postgres.PublicationCompanion) (string, error) {
	extension := strings.ToLower(filepath.Ext(companion.RelativePath))
	switch extension {
	case ".ass", ".idx", ".srt", ".ssa", ".sub", ".vtt":
	default:
		return "", errors.New("subtitle companion extension is unsupported")
	}
	if strings.TrimSpace(companion.Language) == "" {
		return "", errors.New("subtitle companion has no verified language")
	}
	suffix := "." + strings.ToLower(strings.TrimSpace(companion.Language))
	if companion.Forced != nil && *companion.Forced {
		suffix += ".forced"
	}
	if companion.HearingImpaired != nil && *companion.HearingImpaired {
		suffix += ".sdh"
	}
	destination := strings.TrimSuffix(videoDestination, filepath.Ext(videoDestination)) + suffix + extension
	return library.ValidateRelative(destination)
}

func namingIdentity(item catalog.MediaItem) (string, string, error) {
	preference := []string{"tmdb", "imdb"}
	if item.Kind == catalog.KindSeries {
		preference = []string{"tvdb", "tmdb"}
	}
	for _, wanted := range preference {
		for _, identity := range item.Identities {
			if strings.EqualFold(strings.TrimSpace(identity.Namespace), wanted) && identity.State == catalog.IdentityVerified && strings.TrimSpace(identity.Value) != "" {
				return wanted, strings.TrimSpace(identity.Value), nil
			}
		}
	}
	return "", "", errors.New("a verified Plex-supported media identity is required")
}
