package acquisition

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/adapters/altmount"
	"github.com/media-manager/media-manager/internal/adapters/plex"
	"github.com/media-manager/media-manager/internal/adapters/seerr"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/languages"
	"github.com/media-manager/media-manager/internal/library"
	metadataservice "github.com/media-manager/media-manager/internal/metadata"
	"github.com/media-manager/media-manager/internal/postgres"
)

// AltMountRepairService turns AltMount's corrupted-file reports into repairs.
// AltMount's own repair asks Radarr/Sonarr to replace a broken file; Media
// Manager is the acquisition system here, so it acquires the replacement
// itself (see Store.PlanAltMountRepair). AltMount content is never modified.
type AltMountRepairService struct {
	Store         *postgres.Store
	ResolveSecret SecretResolver
	Transport     http.RoundTripper
	// ListCorrupted overrides the AltMount client in tests.
	ListCorrupted func(ctx context.Context, integration postgres.Integration) ([]altmount.HealthItem, error)
	// Older content (library symlinks Media Manager did not publish) is
	// identified through Plex and, when the title is not catalogued yet,
	// TMDB through Seerr. Without these, only Media Manager publications are
	// repaired.
	PlexClientFor func(postgres.Integration) (*plex.Client, error)
	Seerr         SeerrMetadata
	Metadata      *metadataservice.Service
	// CheckMount, StatFile, ListPublications and PlanMissing override the
	// mount and store calls of the missing-file sweep in tests.
	CheckMount       func(root string) error
	StatFile         func(path string) error
	ListPublications func(ctx context.Context, altMountID uuid.UUID) ([]string, error)
	PlanMissing      func(ctx context.Context, altMountID uuid.UUID, filePath string, now time.Time) (string, error)

	missingMu sync.Mutex
	// missingSeen holds, per AltMount client, the publications whose file was
	// missing on the previous sweep.
	missingSeen map[uuid.UUID]map[string]bool
}

// SeerrMetadata is the Seerr lookup unmanaged repairs need.
type SeerrMetadata interface {
	Metadata(ctx context.Context, mediaType string, tmdbID int64) (seerr.Metadata, error)
	SeasonMetadata(ctx context.Context, tmdbID int64, season int16) (seerr.SeasonMetadata, error)
}

// HandleSweep plans a repair for every corrupted file AltMount reports for
// one AltMount client. Files outside Media Manager's AltMount category are
// not Media Manager publications and are skipped.
func (service *AltMountRepairService) HandleSweep(ctx context.Context, event postgres.OutboxEvent) error {
	var payload struct {
		IntegrationID string `json:"integration_id"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return err
	}
	integrationID, err := uuid.Parse(payload.IntegrationID)
	if err != nil {
		return errors.New("AltMount repair sweep has no valid integration")
	}
	integration, err := service.Store.Integration(ctx, integrationID)
	if errors.Is(err, postgres.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if integration.Kind != "altmount" || !integration.Enabled {
		return nil
	}
	var config altMountConfig
	if err := json.Unmarshal(integration.Config, &config); err != nil {
		return err
	}
	list := service.ListCorrupted
	if list == nil {
		list = service.listCorrupted
	}
	items, err := list(ctx, integration)
	if err != nil {
		return err
	}
	prefix := strings.Trim(strings.TrimSpace(config.Category), "/")
	counts := map[string]int{}
	var links map[string][]unmanagedLink
	for _, item := range items {
		path := strings.Trim(strings.TrimSpace(item.FilePath), "/")
		if prefix != "" && !strings.HasPrefix(path, prefix+"/") {
			if service.PlexClientFor == nil {
				counts["unmanaged_skipped"]++
				continue
			}
			if links == nil {
				if links, err = service.unmanagedLinkIndex(ctx); err != nil {
					return err
				}
			}
			for _, link := range links[path] {
				outcome, planErr := service.planUnmanaged(ctx, integration.ID, item.ID, path, link, nil)
				if planErr != nil {
					// One title's Plex or metadata lookup failing must not stop
					// the sweep; it is retried next sweep.
					slog.Warn("AltMount repair of an unmanaged file failed", "file", path, "error", planErr)
					counts["unmanaged_retry"]++
					continue
				}
				counts["unmanaged_"+outcome]++
			}
			if len(links[path]) == 0 {
				counts["unmanaged_unlinked"]++
			}
			continue
		}
		outcome, planErr := service.Store.PlanAltMountRepair(ctx, integration.ID, item.ID, path, time.Now().UTC())
		if errors.Is(planErr, postgres.ErrConcurrentModification) {
			counts["retry_next_sweep"]++
			continue
		}
		if planErr != nil {
			return planErr
		}
		counts[outcome]++
	}
	slog.Info("AltMount repair sweep", "integration", integration.Name, "corrupted", len(items), "outcomes", counts)
	if missing, err := service.missingFileSweep(ctx, integration.ID, config.LocalReadOnlyRoot); err != nil {
		return err
	} else if len(missing) > 0 {
		slog.Info("AltMount missing-file sweep", "integration", integration.Name, "outcomes", missing)
	}
	if integration.Default {
		upgrades, err := service.audioUpgradeSweep(ctx, integration.ID)
		if err != nil {
			return err
		}
		if len(upgrades) > 0 {
			slog.Info("Audio upgrade sweep", "outcomes", upgrades)
		}
	}
	return nil
}

// Missing-file repairs. A publication's file must be missing on two sweeps
// in a row (sweeps run every half hour), so a file AltMount is briefly not
// listing is not replaced. When more than missingFileLimit files and more
// than missingFileShare of all publications are missing at once, that is a
// mount or AltMount fault rather than lost releases, and nothing is repaired.
// Both must be exceeded: a large library legitimately loses dozens of files
// after a corruption sweep, and a small one would otherwise stop at a few.
const (
	missingFileLimit       = 25
	missingFileShare       = 0.02
	missingFileStatTimeout = 10 * time.Second
)

// missingFileSweep opens a repair for every current publication whose
// AltMount file is gone from the read-only mount. AltMount removes a file
// whose Usenet articles went missing and stops reporting it as corrupted, so
// without this check the Plex item would point at nothing indefinitely.
func (service *AltMountRepairService) missingFileSweep(ctx context.Context, altMountID uuid.UUID, root string) (map[string]int, error) {
	counts := map[string]int{}
	root = strings.TrimSpace(root)
	if root == "" || !filepath.IsAbs(root) {
		return counts, nil
	}
	check := service.CheckMount
	if check == nil {
		check = altmount.CheckMount
	}
	if err := check(root); err != nil {
		slog.Warn("AltMount missing-file sweep skipped: the mount is unavailable", "root", root, "error", err)
		service.rememberMissing(altMountID, nil)
		counts["mount_unavailable"]++
		return counts, nil
	}
	listPublications, planMissing := service.ListPublications, service.PlanMissing
	if listPublications == nil {
		listPublications = service.Store.CurrentAltMountPublications
	}
	if planMissing == nil {
		planMissing = service.Store.PlanMissingAltMountFile
	}
	paths, err := listPublications(ctx, altMountID)
	if err != nil {
		return counts, err
	}
	missing := map[string]bool{}
	for _, path := range paths {
		if ctx.Err() != nil {
			return counts, ctx.Err()
		}
		relative, err := library.ValidateRelative(filepath.Clean(path))
		if err != nil || relative != path {
			counts["unsafe_path"]++
			continue
		}
		err = service.statFile(filepath.Join(root, relative))
		switch {
		case err == nil:
		case errors.Is(err, fs.ErrNotExist):
			missing[path] = true
		case errors.Is(err, errStatTimeout):
			// A file that does not answer says nothing about whether it exists,
			// and the next stat would likely block too.
			slog.Warn("AltMount missing-file sweep stopped: a file did not answer", "file", path)
			counts["unresponsive"]++
			return counts, nil
		default:
			// Other errors (for example EIO on a corrupted file) belong to
			// AltMount's corruption reports, not to this check.
			counts["stat_error"]++
		}
	}
	if len(missing) > missingFileLimit && float64(len(missing)) > missingFileShare*float64(len(paths)) {
		slog.Warn("AltMount missing-file sweep skipped: too many files are missing at once", "missing", len(missing), "publications", len(paths))
		service.rememberMissing(altMountID, nil)
		counts["too_many_missing"] = len(missing)
		return counts, nil
	}
	previous := service.rememberMissing(altMountID, missing)
	now := time.Now().UTC()
	for _, path := range sortedKeys(missing) {
		if !previous[path] {
			counts["missing_once"]++
			continue
		}
		outcome, err := planMissing(ctx, altMountID, path, now)
		if errors.Is(err, postgres.ErrConcurrentModification) {
			counts["retry_next_sweep"]++
			continue
		}
		if err != nil {
			return counts, err
		}
		counts[outcome]++
	}
	return counts, nil
}

// rememberMissing stores this sweep's missing files for one AltMount client
// and returns the previous sweep's.
func (service *AltMountRepairService) rememberMissing(altMountID uuid.UUID, missing map[string]bool) map[string]bool {
	service.missingMu.Lock()
	defer service.missingMu.Unlock()
	if service.missingSeen == nil {
		service.missingSeen = map[uuid.UUID]map[string]bool{}
	}
	previous := service.missingSeen[altMountID]
	service.missingSeen[altMountID] = missing
	return previous
}

var errStatTimeout = errors.New("the file did not answer in time")

// statFile reports whether a file exists on the mount, bounded in time: a
// FUSE call can block indefinitely when AltMount hangs.
func (service *AltMountRepairService) statFile(path string) error {
	if service.StatFile != nil {
		return service.StatFile(path)
	}
	result := make(chan error, 1)
	go func() {
		_, err := os.Lstat(path)
		result <- err
	}()
	select {
	case err := <-result:
		return err
	case <-time.After(missingFileStatTimeout):
		return errStatTimeout
	}
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Audio upgrades are paced: at most maxOpenAudioUpgrades search at a time,
// each sweep opens at most maxNewAudioUpgrades and identifies at most
// maxAudioUpgradeLookups older files through Plex and TMDB. Every open
// workflow searches every indexer, and many titles have no dub at all.
const (
	maxOpenAudioUpgrades   = 40
	maxNewAudioUpgrades    = 10
	maxAudioUpgradeLookups = 100
)

// plexAudioLanguage maps a canonical language to the ISO 639-2 code Plex
// filters audio tracks by.
var plexAudioLanguage = map[string]string{"da": "dan", "sv": "swe", "no": "nor", "nb": "nob", "en": "eng", "de": "ger", "fi": "fin"}

// audioUpgradeSweep opens upgrades for published files in library roots whose
// profile requires an audio language (the kids libraries require Danish) but
// that Plex reports without a track in that language. Movies go first. Only
// library symlinks into AltMount are upgraded; the old file plays until Plex
// confirms the replacement. Upgrades that found nothing are closed first so
// other files get their turn.
func (service *AltMountRepairService) audioUpgradeSweep(ctx context.Context, altMountID uuid.UUID) (map[string]int, error) {
	counts := map[string]int{}
	if service.PlexClientFor == nil {
		return counts, nil
	}
	now := time.Now().UTC()
	closed, err := service.Store.ExpireAudioUpgrades(ctx, now)
	if err != nil {
		return counts, err
	}
	if closed > 0 {
		counts["closed_not_found"] = closed
	}
	required, err := service.Store.RequiredAudioByRoot(ctx)
	if err != nil || len(required) == 0 {
		return counts, err
	}
	open, err := service.Store.OpenAudioUpgrades(ctx)
	if err != nil {
		return counts, err
	}
	budget := min(maxNewAudioUpgrades, maxOpenAudioUpgrades-open)
	if budget <= 0 {
		counts["at_capacity"] = open
		return counts, nil
	}
	lookups := maxAudioUpgradeLookups
	roots, err := service.Store.ListLibraryRoots(ctx)
	if err != nil {
		return counts, err
	}
	pairs := rootPathPairs(roots)
	sort.SliceStable(roots, func(i, j int) bool {
		return roots[i].MediaKind == catalog.KindMovie && roots[j].MediaKind != catalog.KindMovie
	})
	for _, root := range roots {
		target, ok := required[root.ID]
		code := plexAudioLanguage[target.Language]
		if !ok || code == "" || !root.Enabled || root.PlexLibraryID == "" || root.PlexInstanceID == nil || !filepath.IsAbs(root.SourceVisibleRoot) {
			continue
		}
		integration, err := service.Store.Integration(ctx, *root.PlexInstanceID)
		if err != nil {
			return counts, err
		}
		client, err := service.PlexClientFor(integration)
		if err != nil {
			return counts, err
		}
		locations, err := sectionLocations(ctx, client, root, pairs)
		if err != nil {
			return counts, err
		}
		items, err := client.ItemsWithoutAudioLanguage(ctx, root.PlexLibraryID, root.MediaKind == catalog.KindSeries, code)
		if err != nil {
			return counts, err
		}
		upgrade := postgres.AudioUpgrade(target)
		sourcePrefix := filepath.Clean(root.SourceVisibleRoot) + string(filepath.Separator)
		for _, item := range items {
			for _, media := range item.Media {
				for _, part := range media.Parts {
					if budget <= 0 {
						return counts, nil
					}
					link, ok := locateLibraryFile(locations, root, part.File)
					if !ok {
						counts["outside_library_folders"]++
						continue
					}
					if otherRootOwns(roots, root, filepath.Join(link.Location, link.Relative)) {
						// The other version of a merged Plex item (for example
						// the 4K file next to this root's 1080p file).
						counts["other_root_folder"]++
						continue
					}
					linkTarget, readErr := os.Readlink(filepath.Join(link.Location, link.Relative))
					if readErr != nil || !strings.HasPrefix(linkTarget, sourcePrefix) {
						// Only AltMount symlinks are replaced; a real file is never removed.
						counts["not_altmount_symlink"]++
						continue
					}
					link.Target = linkTarget
					path := strings.TrimPrefix(linkTarget, sourcePrefix)
					outcome, planErr := service.Store.PlanAudioUpgrade(ctx, altMountID, path, upgrade, now)
					if errors.Is(planErr, postgres.ErrConcurrentModification) {
						counts["retry_next_sweep"]++
						continue
					}
					if planErr != nil {
						return counts, planErr
					}
					if outcome == postgres.RepairUnmanaged {
						tracked, trackErr := service.Store.UnmanagedRepairTracked(ctx, link.Location, link.Relative, true, now)
						if trackErr != nil {
							return counts, trackErr
						}
						if tracked {
							counts["unmanaged_"+postgres.RepairAlreadyOpen]++
							continue
						}
						if lookups <= 0 {
							counts["unmanaged_next_sweep"]++
							continue
						}
						lookups--
						outcome, planErr = service.planUnmanaged(ctx, altMountID, 0, path, link, &target)
						if planErr != nil {
							slog.Warn("Audio upgrade of an unmanaged file failed", "file", path, "error", planErr)
							counts["unmanaged_retry"]++
							continue
						}
						outcome = "unmanaged_" + outcome
					}
					counts[outcome]++
					if strings.HasSuffix(outcome, postgres.RepairOpened) || strings.HasSuffix(outcome, postgres.RepairAttached) {
						budget--
					}
				}
			}
		}
	}
	return counts, nil
}

// sectionLocations maps every folder of a root's Plex library (as Plex sees
// it) to the same folder in this container.
func sectionLocations(ctx context.Context, client *plex.Client, root postgres.LibraryRoot, pairs []pathPair) (map[string]string, error) {
	locations := map[string]string{filepath.Clean(root.PlexVisibleRoot): filepath.Clean(root.DestinationRoot)}
	sections, err := client.Sections(ctx)
	if err != nil {
		return nil, err
	}
	for _, section := range sections {
		if section.Key != root.PlexLibraryID {
			continue
		}
		for _, location := range section.Locations {
			if container, ok := translatePlexPath(pairs, location.Path); ok {
				locations[filepath.Clean(location.Path)] = container
			}
		}
	}
	return locations, nil
}

// locateLibraryFile finds the library folder holding a file Plex reports and
// returns the file as a library link (without its target).
func locateLibraryFile(locations map[string]string, root postgres.LibraryRoot, plexFile string) (unmanagedLink, bool) {
	plexFile = filepath.Clean(plexFile)
	best := ""
	for plexLocation := range locations {
		if strings.HasPrefix(plexFile, strings.TrimSuffix(plexLocation, "/")+"/") && len(plexLocation) > len(best) {
			best = plexLocation
		}
	}
	if best == "" {
		return unmanagedLink{}, false
	}
	relative, err := filepath.Rel(best, plexFile)
	if err != nil || relative == "." || strings.HasPrefix(relative, "..") {
		return unmanagedLink{}, false
	}
	return unmanagedLink{Root: root, Location: locations[best], PlexLocation: best, Relative: relative}, true
}

func (service *AltMountRepairService) listCorrupted(ctx context.Context, integration postgres.Integration) ([]altmount.HealthItem, error) {
	var config altMountConfig
	if err := json.Unmarshal(integration.Config, &config); err != nil {
		return nil, err
	}
	client, err := newAltMountClient(config, integration.SecretRef, service.ResolveSecret, service.Transport)
	if err != nil {
		return nil, err
	}
	if _, err := client.Login(ctx); err != nil {
		return nil, err
	}
	return client.ListCorruptedFiles(ctx)
}

// unmanagedLink is a library symlink that points into AltMount, found in one
// folder of a library root's Plex library.
type unmanagedLink struct {
	Root         postgres.LibraryRoot
	Location     string // the library folder as this container sees it
	PlexLocation string // the same folder as Plex sees it
	Relative     string
	Target       string
}

// pathPair translates a Plex-visible folder prefix to the container prefix.
type pathPair struct{ plex, container string }

// rootPathPairs derives Plex-to-container prefix translations from library
// roots whose Plex-visible and container folders share their final
// components (for example /media/Serier and /srv/media/Serier).
func rootPathPairs(roots []postgres.LibraryRoot) []pathPair {
	pairs := []pathPair{}
	for _, root := range roots {
		plexPath, containerPath := filepath.Clean(root.PlexVisibleRoot), filepath.Clean(root.DestinationRoot)
		for filepath.Base(plexPath) == filepath.Base(containerPath) && plexPath != "/" && containerPath != "/" {
			plexPath, containerPath = filepath.Dir(plexPath), filepath.Dir(containerPath)
		}
		if plexPath != filepath.Clean(root.PlexVisibleRoot) {
			pairs = append(pairs, pathPair{plex: plexPath, container: containerPath})
		}
	}
	return pairs
}

func translatePlexPath(pairs []pathPair, plexPath string) (string, bool) {
	plexPath = filepath.Clean(plexPath)
	best, found := "", false
	bestLength := -1
	for _, pair := range pairs {
		if plexPath == pair.plex || strings.HasPrefix(plexPath, strings.TrimSuffix(pair.plex, "/")+"/") {
			if len(pair.plex) > bestLength {
				best, found, bestLength = filepath.Join(pair.container, strings.TrimPrefix(plexPath, pair.plex)), true, len(pair.plex)
			}
		}
	}
	return best, found
}

// unmanagedLinkIndex maps an AltMount-relative file path to the library
// symlinks that point at it. It walks every folder of every enabled library
// root's Plex library (a Plex library can list folders that are not Media
// Manager roots), translated to this container's paths.
func (service *AltMountRepairService) unmanagedLinkIndex(ctx context.Context) (map[string][]unmanagedLink, error) {
	roots, err := service.Store.ListLibraryRoots(ctx)
	if err != nil {
		return nil, err
	}
	pairs := rootPathPairs(roots)
	sections := map[string][]plex.Section{}
	index := map[string][]unmanagedLink{}
	walked := map[string]bool{}
	for _, root := range roots {
		if !root.Enabled || root.PlexLibraryID == "" || root.PlexInstanceID == nil || !filepath.IsAbs(root.SourceVisibleRoot) {
			continue
		}
		plexKey := root.PlexInstanceID.String()
		if _, cached := sections[plexKey]; !cached {
			integration, integrationErr := service.Store.Integration(ctx, *root.PlexInstanceID)
			if integrationErr != nil {
				return nil, integrationErr
			}
			client, clientErr := service.PlexClientFor(integration)
			if clientErr != nil {
				return nil, clientErr
			}
			if sections[plexKey], err = client.Sections(ctx); err != nil {
				return nil, err
			}
		}
		locations := map[string]string{filepath.Clean(root.PlexVisibleRoot): filepath.Clean(root.DestinationRoot)}
		for _, section := range sections[plexKey] {
			if section.Key != root.PlexLibraryID {
				continue
			}
			for _, location := range section.Locations {
				if container, ok := translatePlexPath(pairs, location.Path); ok {
					locations[filepath.Clean(location.Path)] = container
				}
			}
		}
		if err := collectRootLinks(index, walked, roots, root, locations); err != nil {
			return nil, err
		}
	}
	return index, nil
}

// collectRootLinks adds every AltMount symlink in a root's library folders to
// the index. One Plex library can hold several Media Manager roots, for
// example movies/1080p (standard) and movies/2160p (4K), which Plex shows as
// two versions of one film. A folder that belongs to another root is left to
// that root, so a broken 4K file is never repaired as the standard edition.
func collectRootLinks(index map[string][]unmanagedLink, walked map[string]bool, roots []postgres.LibraryRoot, root postgres.LibraryRoot, locations map[string]string) error {
	sourcePrefix := filepath.Clean(root.SourceVisibleRoot) + string(filepath.Separator)
	for plexLocation, location := range locations {
		if walked[location] || otherRootOwns(roots, root, location) {
			continue
		}
		walked[location] = true
		walkErr := filepath.WalkDir(location, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if entry.IsDir() && path != location && otherRootOwns(roots, root, path) {
				return filepath.SkipDir
			}
			if entry.Type()&fs.ModeSymlink == 0 {
				return nil
			}
			target, readErr := os.Readlink(path)
			if readErr != nil || !strings.HasPrefix(target, sourcePrefix) {
				return nil
			}
			relative, relErr := filepath.Rel(location, path)
			if relErr != nil {
				return nil
			}
			key := strings.TrimPrefix(target, sourcePrefix)
			index[key] = append(index[key], unmanagedLink{Root: root, Location: location, PlexLocation: plexLocation, Relative: relative, Target: target})
			return nil
		})
		if walkErr != nil && !errors.Is(walkErr, fs.ErrNotExist) {
			return walkErr
		}
	}
	return nil
}

// otherRootOwns reports whether path (as this container sees it) lies in the
// folder of a different enabled library root: the root whose folder is the
// closest parent of path owns it. A path outside every root's folder (another
// folder of the same Plex library) belongs to no other root.
func otherRootOwns(roots []postgres.LibraryRoot, root postgres.LibraryRoot, path string) bool {
	path = filepath.Clean(path)
	owner, best := root.ID, -1
	for _, candidate := range roots {
		if !candidate.Enabled || !filepath.IsAbs(candidate.DestinationRoot) {
			continue
		}
		folder := filepath.Clean(candidate.DestinationRoot)
		if (path == folder || strings.HasPrefix(path, strings.TrimSuffix(folder, string(filepath.Separator))+string(filepath.Separator))) && len(folder) > best {
			owner, best = candidate.ID, len(folder)
		}
	}
	return best >= 0 && owner != root.ID
}

var trailingTag = regexp.MustCompile(`(?:[ ._-]*\[[^\]]*\])+$`)

// releaseTitleFromPath returns the release name of an AltMount file: its
// folder name without local tags such as "[DRI-PROOF-AUDIO]".
func releaseTitleFromPath(path string) string {
	folder := filepath.Base(filepath.Dir(path))
	if folder == "." || folder == "/" {
		folder = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	return strings.TrimSpace(trailingTag.ReplaceAllString(folder, ""))
}

// planUnmanaged identifies one broken unmanaged symlink through Plex and opens
// a repair for it, or records why it cannot be repaired automatically. With
// audio set, the file is not broken but lacks the audio language its library
// requires, and the repair is an upgrade to a dubbed release.
func (service *AltMountRepairService) planUnmanaged(ctx context.Context, altMountID uuid.UUID, healthID int64, path string, link unmanagedLink, audio *postgres.AudioUpgradeRoot) (string, error) {
	tracked, err := service.Store.UnmanagedRepairTracked(ctx, link.Location, link.Relative, audio != nil, time.Now().UTC())
	if err != nil {
		return "", err
	}
	if tracked {
		return postgres.RepairAlreadyOpen, nil
	}
	repair := postgres.UnmanagedRepair{AltMountID: altMountID, HealthID: healthID, FilePath: path, RootID: link.Root.ID, Location: link.Location, PlexLocation: link.PlexLocation, Relative: link.Relative, Target: link.Target, Kind: link.Root.MediaKind, ReleaseTitle: releaseTitleFromPath(path)}
	if audio != nil {
		repair.AudioLanguage = audio.Language
	}
	attention := func(detail string) (string, error) {
		if err := service.Store.RecordUnmanagedRepairAttention(ctx, repair, detail, time.Now().UTC()); err != nil {
			return "", err
		}
		return postgres.RepairNeedsAttention, nil
	}
	if link.Root.PlexInstanceID == nil {
		return attention("the library root has no Plex server to identify the title")
	}
	integration, err := service.Store.Integration(ctx, *link.Root.PlexInstanceID)
	if err != nil {
		return "", err
	}
	client, err := service.PlexClientFor(integration)
	if err != nil {
		return "", err
	}
	plexPath := filepath.Join(link.PlexLocation, link.Relative)
	item, err := client.ItemAtPath(ctx, link.Root.PlexLibraryID, plexPath)
	if errors.Is(err, plex.ErrNotConfirmed) {
		return attention("Plex has no single item at " + plexPath)
	}
	if err != nil {
		return "", err
	}
	identityItem := item
	if repair.Kind == catalog.KindSeries {
		season, seasonErr := strconv.Atoi(item.Season)
		episode, episodeErr := strconv.Atoi(item.Episode)
		if seasonErr != nil || episodeErr != nil || season < 0 || season > 999 || episode <= 0 || episode > 999 {
			return attention("Plex reports no season and episode number for the file")
		}
		repair.Season, repair.Episode = uint16(season), uint16(episode)
		if identityItem, err = client.Metadata(ctx, item.GrandparentRatingKey); err != nil {
			return "", err
		}
	} else if identityItem, err = client.Metadata(ctx, item.RatingKey); err != nil {
		return "", err
	}
	identities, tmdbID := plexIdentities(identityItem)
	if len(identities) == 0 {
		return attention("Plex has no TMDB, TVDB or IMDb identity for the title")
	}
	mediaID, found, archived, err := service.Store.MediaIDByIdentities(ctx, repair.Kind, identities)
	if err != nil {
		return "", err
	}
	if archived {
		repair.MediaID = mediaID
		return attention("the title is archived in Media Manager")
	}
	var language string
	if found {
		media, mediaErr := service.Store.GetMedia(ctx, mediaID)
		if mediaErr != nil {
			return "", mediaErr
		}
		if media.Metadata != nil {
			language = media.Metadata.OriginalLanguage
		}
		for _, identity := range media.Identities {
			if identity.Namespace == "tmdb" && identity.State == catalog.IdentityVerified {
				if parsed, parseErr := strconv.ParseInt(identity.Value, 10, 64); parseErr == nil {
					tmdbID = parsed
				}
			}
		}
	}
	if !found || repair.Kind == catalog.KindSeries {
		if service.Seerr == nil || tmdbID <= 0 {
			return attention("the title has no TMDB identity to look up its metadata")
		}
	}
	if repair.Kind == catalog.KindSeries {
		// The file follows Plex's episode numbering; Media Manager acquires by
		// TMDB numbering. Only repair when both agree on the air date.
		season, seasonErr := service.Seerr.SeasonMetadata(ctx, tmdbID, int16(repair.Season))
		if seasonErr != nil {
			return attention(fmt.Sprintf("TMDB has no season %d for the title", repair.Season))
		}
		var snapshot *seerr.EpisodeMetadata
		for index := range season.Episodes {
			if season.Episodes[index].EpisodeNumber == int16(repair.Episode) {
				snapshot = &season.Episodes[index]
			}
		}
		aired := strings.TrimSpace(item.AiredAt)
		if snapshot == nil || aired == "" || strings.TrimSpace(snapshot.AirDate) != aired {
			return attention(fmt.Sprintf("season %d episode %d could not be verified: Plex and TMDB disagree on the episode (numbering differs)", repair.Season, repair.Episode))
		}
		repair.Snapshot = postgres.ManualEpisode{Number: repair.Episode, ProviderID: "tmdb:" + strconv.FormatInt(snapshot.ID, 10), Title: snapshot.Name, AirDate: snapshot.AirDate}
	}
	if !found {
		mediaType := "movie"
		if repair.Kind == catalog.KindSeries {
			mediaType = "tv"
		}
		discovered, discoverErr := service.Metadata.Discover(ctx, service.Seerr, mediaType, tmdbID, time.Now().UTC())
		if discoverErr != nil {
			return "", discoverErr
		}
		if discovered.Year == nil {
			return attention("TMDB has no release year for the title")
		}
		item, itemErr := catalog.New(repair.Kind, discovered.Title, discovered.Year, discovered.Identities, time.Now().UTC())
		if itemErr != nil {
			return attention("the TMDB title could not be catalogued: " + itemErr.Error())
		}
		if discovered.Result.Metadata != nil {
			if item.Metadata, itemErr = catalog.NormalizeMetadata(discovered.Result.Metadata, time.Now().UTC()); itemErr != nil {
				return attention("the TMDB metadata could not be normalized")
			}
			language = item.Metadata.OriginalLanguage
		}
		// Catalogued together with the repair workflow in one transaction.
		repair.NewMedia = &item
	} else {
		repair.MediaID = mediaID
	}
	if audio != nil {
		if canonical, _ := languages.Canonical(language); canonical == audio.Language {
			// A title made in the required language: its own audio is that
			// language even when the track is untagged.
			if err := service.Store.RecordUnmanagedRepairSkipped(ctx, repair, "the title's original language is "+audio.Language+"; its own audio needs no upgrade", time.Now().UTC()); err != nil {
				return "", err
			}
			return postgres.RepairNotNeeded, nil
		}
		repair.ProfileID = audio.ProfileID
		return service.Store.PlanUnmanagedRepair(ctx, repair, time.Now().UTC())
	}
	if repair.ProfileID, err = service.Store.ResolveRepairProfile(ctx, repair.Kind, link.Root.ID, language); err != nil {
		return "", err
	}
	return service.Store.PlanUnmanagedRepair(ctx, repair, time.Now().UTC())
}

// plexIdentities converts Plex provider GUIDs to verified catalogue
// identities and returns the TMDB id when present.
func plexIdentities(item plex.Item) ([]catalog.ExternalIdentity, int64) {
	identities := []catalog.ExternalIdentity{}
	var tmdbID int64
	for _, guid := range item.GUIDs {
		namespace, value, ok := strings.Cut(guid.ID, "://")
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		switch namespace {
		case "tmdb", "tvdb", "imdb":
			identities = append(identities, catalog.ExternalIdentity{Namespace: namespace, Value: value, State: catalog.IdentityVerified, Source: "plex"})
			if namespace == "tmdb" {
				tmdbID, _ = strconv.ParseInt(value, 10, 64)
			}
		}
	}
	return identities, tmdbID
}

// HandleUnmanagedRemoval removes a broken unmanaged symlink after Plex
// confirmed its replacement, and asks Plex to rescan the folder. A link that
// no longer points at the corrupted file is left alone.
func (service *AltMountRepairService) HandleUnmanagedRemoval(ctx context.Context, event postgres.OutboxEvent) error {
	removal, err := service.Store.LoadUnmanagedRemoval(ctx, event.AggregateID)
	if errors.Is(err, postgres.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := library.RemoveSymlink(removal.DestinationRoot, removal.Relative, removal.Target); err != nil && !errors.Is(err, library.ErrCollision) {
		return err
	}
	if service.PlexClientFor != nil && removal.PlexLibraryID != "" {
		if integration, integrationErr := service.Store.Integration(ctx, removal.PlexInstanceID); integrationErr == nil {
			if client, clientErr := service.PlexClientFor(integration); clientErr == nil {
				_ = client.Refresh(ctx, removal.PlexLibraryID, filepath.Dir(filepath.Join(removal.PlexVisibleRoot, removal.Relative)))
			}
		}
	}
	return service.Store.CompleteUnmanagedRemoval(ctx, removal.RepairID, time.Now().UTC())
}
