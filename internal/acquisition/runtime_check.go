package acquisition

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/mediainfo"
	"github.com/media-manager/media-manager/internal/packs"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/subtitles"
	"github.com/media-manager/media-manager/internal/workflows"
)

// outputTooShortDetail returns a release-attributable reason when a selected
// video is far shorter than the movie or episode it claims to be. The probe
// already runs for subtitle and playable-media evidence, so the duration is
// free; a fake, sample or cut-off download must not be published as a success.
//
// The expected runtime is TMDB's runtime for the title (a series runtime is
// its usual episode length, which can be far off for seasons with short
// segments or for a show whose format changed); a shorter runtime TMDB gives
// for the exact episode (episodeMinutes) replaces it. When neither is known,
// only an implausibly short file is refused. Specials (season 0) and short
// subjects are never judged by runtime because their lengths vary legitimately.
func outputTooShortDetail(job postgres.AltMountJobContext, verification subtitles.Verification, episodeMinutes uint16) (string, bool) {
	kind, ok := runtimeLengthKind(job)
	if !ok {
		return "", false
	}
	expectedMinutes := 0.0
	if job.Media.Metadata != nil && job.Media.Metadata.RuntimeMinutes != nil {
		expectedMinutes = float64(*job.Media.Metadata.RuntimeMinutes)
	}
	// The episode's own runtime only ever lowers the expectation: release
	// numbering can differ from TMDB's (a 48-minute TMDB episode can be four
	// 12-minute releases), so it must never refuse a file the series runtime
	// accepts.
	if kind == mediainfo.LengthEpisode && episodeMinutes > 0 && float64(episodeMinutes) < expectedMinutes {
		expectedMinutes = float64(episodeMinutes)
	}
	for _, video := range verification.Videos {
		if video.MediaInfo.DurationSeconds == nil {
			continue
		}
		if !mediainfo.TooShort(*video.MediaInfo.DurationSeconds, expectedMinutes, kind) {
			continue
		}
		actual := formatLength(*video.MediaInfo.DurationSeconds)
		label := "movie"
		if kind == mediainfo.LengthEpisode {
			label = "episode"
		}
		if expectedMinutes > 0 {
			return fmt.Sprintf("The file %s is only %s long; the %s is about %s. This looks like a fake, sample or truncated release.", video.RelativePath, actual, label, formatLength(expectedMinutes*60)), true
		}
		return fmt.Sprintf("The file %s is only %s long, too short to be a %s. This looks like a fake, sample or truncated release.", video.RelativePath, actual, label), true
	}
	return "", false
}

func runtimeLengthKind(job postgres.AltMountJobContext) (mediainfo.LengthKind, bool) {
	switch job.Media.Kind {
	case catalog.KindMovie:
		return mediainfo.LengthMovie, true
	case catalog.KindSeries:
		scope, ok := workflows.ParseVariant(job.Variant)
		if !ok || scope.Kind == workflows.VariantDefault || scope.Season == 0 {
			return 0, false
		}
		return mediainfo.LengthEpisode, true
	default:
		return 0, false
	}
}

func formatLength(seconds float64) string {
	if seconds < 60 {
		return fmt.Sprintf("%.0f s", seconds)
	}
	return fmt.Sprintf("%.0f min", math.Round(seconds/60))
}

// EpisodeRuntimeSource reads one season's per-episode runtimes, keyed by the
// provider episode ID the frozen episode snapshot stores ("tmdb:<id>").
type EpisodeRuntimeSource interface {
	SeasonEpisodeRuntimes(ctx context.Context, seriesID string, season uint16) (map[string]uint16, error)
}

const (
	episodeRuntimeCacheTTL = 6 * time.Hour
	episodeRuntimeTimeout  = 15 * time.Second
)

type episodeRuntimeCache struct {
	mu      sync.Mutex
	seasons map[string]episodeRuntimeSeason
}

type episodeRuntimeSeason struct {
	runtimes map[string]uint16
	fetched  time.Time
}

// expectedEpisodeRuntime is the provider's runtime for the single episode a
// workflow requests, matched by the frozen snapshot's provider episode ID so
// a numbering difference can never pick another episode's length. Zero means
// unknown; the caller then uses the series runtime as before.
func (service *AltMountReconcileService) expectedEpisodeRuntime(ctx context.Context, job postgres.AltMountJobContext) uint16 {
	if service.EpisodeRuntimes == nil || job.Media.Kind != catalog.KindSeries {
		return 0
	}
	scope, ok := workflows.ParseVariant(job.Variant)
	if !ok || scope.Kind != workflows.VariantEpisode || scope.Season == 0 || scope.Episode == 0 {
		return 0
	}
	seriesID := ""
	for _, identity := range job.Media.Identities {
		if identity.State == catalog.IdentityVerified && identity.Namespace == "tmdb" {
			seriesID = strings.TrimSpace(identity.Value)
		}
	}
	if seriesID == "" {
		return 0
	}
	_, _, _, expected, err := service.Store.PackExpectation(ctx, job.Workflow.ID)
	if err != nil {
		return 0
	}
	return episodeRuntimeFromSnapshot(scope, seriesID, expected, func(seriesID string, season uint16) map[string]uint16 {
		return service.seasonRuntimes(ctx, seriesID, season)
	})
}

func episodeRuntimeFromSnapshot(scope workflows.VariantScope, seriesID string, expected []packs.ExpectedEpisode, season func(string, uint16) map[string]uint16) uint16 {
	episodeID := ""
	for _, item := range expected {
		if item.Key.Season == scope.Season && item.Key.Episode == scope.Episode {
			episodeID = strings.TrimSpace(item.ProviderEpisodeID)
		}
	}
	if !strings.HasPrefix(episodeID, "tmdb:") {
		return 0
	}
	return season(seriesID, scope.Season)[episodeID]
}

func (service *AltMountReconcileService) seasonRuntimes(ctx context.Context, seriesID string, season uint16) map[string]uint16 {
	key := fmt.Sprintf("%s:%d", seriesID, season)
	now := time.Now()
	service.runtimeCache.mu.Lock()
	cached, ok := service.runtimeCache.seasons[key]
	service.runtimeCache.mu.Unlock()
	if ok && now.Sub(cached.fetched) < episodeRuntimeCacheTTL {
		return cached.runtimes
	}
	lookupCtx, cancel := context.WithTimeout(ctx, episodeRuntimeTimeout)
	defer cancel()
	runtimes, err := service.EpisodeRuntimes.SeasonEpisodeRuntimes(lookupCtx, seriesID, season)
	if err != nil {
		return nil
	}
	service.runtimeCache.mu.Lock()
	if service.runtimeCache.seasons == nil {
		service.runtimeCache.seasons = map[string]episodeRuntimeSeason{}
	}
	service.runtimeCache.seasons[key] = episodeRuntimeSeason{runtimes: runtimes, fetched: now}
	service.runtimeCache.mu.Unlock()
	return runtimes
}
