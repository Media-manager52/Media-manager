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
	plans := make([]postgres.PublicationPlan, 0, len(publicationContext.Assets))
	links := make([]library.LinkPlan, 0, len(publicationContext.Assets))
	technical := append([]string(nil), publicationContext.Technical...)
	if publicationContext.ReleaseToken != "" {
		technical = append(technical, publicationContext.ReleaseToken)
	}
	for _, asset := range publicationContext.Assets {
		sourceRelative := asset.RelativePath
		if publicationContext.InventoryRootRelative != "" && publicationContext.InventoryRootRelative != "." {
			sourceRelative = filepath.Join(publicationContext.InventoryRootRelative, asset.RelativePath)
		}
		sourceRelative, err = library.ValidateRelative(sourceRelative)
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
		link := library.LinkPlan{SourceRoot: filepath.Clean(config.LocalReadOnlyRoot), SourceRelative: sourceRelative, DestinationRoot: filepath.Clean(publicationContext.Root.DestinationRoot), DestinationRel: destinationRelative, PlexVisibleSourceRoot: filepath.Clean(publicationContext.Root.SourceVisibleRoot)}
		validated, err := library.ValidatePlan(link)
		if err != nil {
			return nil, nil, err
		}
		plans = append(plans, postgres.PublicationPlan{AssetID: asset.ID, DestinationRelative: destinationRelative, SourceRelative: sourceRelative, PlexVisibleTarget: validated.PlexVisibleTarget})
		links = append(links, validated)
	}
	return plans, links, nil
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
