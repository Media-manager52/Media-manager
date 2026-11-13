// Package quicksetup is "Brug anbefalet opsætning": with Plex (and AltMount)
// connected, it creates the library folders and quality profiles a new
// install needs, from what Media Manager can prove instead of from guesses.
//
// A library root needs three views of the disk: where Media Manager writes
// (destination_root), where Plex sees that same folder (plex_visible_root)
// and where Plex sees AltMount's files (source_visible_root). Plex's own
// folder browser answers both questions:
//
//   - destination: Media Manager creates an empty marker folder in each folder
//     it could write to, asks Plex to list its library's folder, and keeps the
//     folder whose marker Plex shows; every marker is removed again.
//   - source: the folders Media Manager sees in AltMount's root must be the
//     folders Plex lists at the candidate path.
//
// Nothing existing is changed: a root or profile is only created for a media
// kind that has no default one yet.
package quicksetup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
)

// ProfileName is the quality profile created for both kinds.
const ProfileName = "Dansk tekst eller tale"

const markerPrefix = "mm-opsaetning-tjek-"

// Plex is the part of a Plex connection the setup uses.
type Plex interface {
	Sections(ctx context.Context) ([]PlexSection, error)
	Browse(ctx context.Context, path string) ([]string, error)
}

// PlexLibraryCreator is a Plex connection that can create a library.
type PlexLibraryCreator interface {
	CreateSection(ctx context.Context, kind, name, language, folder string) error
}

// Names and language of the Plex libraries the setup creates.
var newLibraryNames = map[string]string{"movie": "Film", "show": "Serier"}

const newLibraryLanguage = "da-DK"

// PlexSection is one Plex library.
type PlexSection struct {
	Key       string
	Title     string
	Type      string
	Locations []string
}

// Store is the storage the setup reads and writes.
type Store interface {
	ListIntegrations(ctx context.Context) ([]postgres.Integration, error)
	ListLibraryRoots(ctx context.Context) ([]postgres.LibraryRoot, error)
	ListQualityProfiles(ctx context.Context) ([]postgres.QualityProfile, error)
	CreateLibraryRoot(ctx context.Context, root postgres.LibraryRoot) (postgres.LibraryRoot, error)
	CreateQualityProfile(ctx context.Context, profile postgres.QualityProfile) (postgres.QualityProfile, error)
}

// Service runs the recommended setup.
type Service struct {
	Store Store
	// OpenPlex opens a Plex connection with its saved token.
	OpenPlex func(ctx context.Context, integration postgres.Integration) (Plex, error)
	// Bases are the folders Media Manager is given for libraries, by kind
	// ("" for both). Tests replace them.
	Bases map[catalog.MediaKind][]string
	// SourceCandidates are extra paths where Plex may see AltMount.
	SourceCandidates []string

	// mutex runs one setup at a time, so two clicks cannot both create a
	// default root.
	mutex sync.Mutex
}

var defaultBases = map[catalog.MediaKind][]string{
	catalog.KindMovie:  {"/libraries/movies"},
	catalog.KindSeries: {"/libraries/tv"},
	"":                 {"/srv/media"},
}

var defaultSourceCandidates = []string{"/mnt/altmount", "/altmount", "/mnt/remotes/altmount"}

// Step states.
const (
	StateExists  = "exists"  // already set up; left as it is
	StateCreate  = "create"  // would be created (preview)
	StateCreated = "created" // created now
	StateBlocked = "blocked" // cannot be done; Reason says why
)

// Step is one thing the setup looked at.
type Step struct {
	// ID is movie_root, series_root, movie_profile or series_profile.
	ID     string `json:"id"`
	State  string `json:"state"`
	Name   string `json:"name,omitempty"`
	Reason string `json:"reason,omitempty"`
	// Detail carries the reason's facts (a folder, a library name).
	Detail string `json:"detail,omitempty"`
	// Root is the library root created or proposed.
	Root *postgres.LibraryRoot `json:"root,omitempty"`
	// SourceProven is false when AltMount's folder could not be compared
	// (it is empty) and the path Media Manager uses is assumed for Plex.
	SourceProven *bool `json:"source_proven,omitempty"`
	// CreatesPlexLibrary is true when Plex has no library of the kind but
	// sees Media Manager's folder, so the Plex library Name is created too.
	CreatesPlexLibrary bool `json:"creates_plex_library,omitempty"`
}

// Report is what the setup did or would do.
type Report struct {
	Applied bool   `json:"applied"`
	Steps   []Step `json:"steps"`
}

// Blocking reasons (the website words them in Danish).
const (
	ReasonNoPlex           = "no_plex"            // no enabled default Plex connection
	ReasonPlexUnreachable  = "plex_unreachable"   // Plex did not list its libraries
	ReasonNoAltMount       = "no_altmount"        // no AltMount connection with a local folder
	ReasonNoPlexLibrary    = "no_plex_library"    // Plex has no library of this kind
	ReasonFolderNotShared  = "folder_not_shared"  // Media Manager cannot write the library's folder
	ReasonSourceNotVisible = "source_not_visible" // Plex cannot see AltMount's files
	ReasonProfileNameTaken = "profile_name_taken" // a profile with the name exists but is not default
	ReasonRootNotDefault   = "root_not_default"   // the Plex library has a root, but it is not the default
)

type kindPlan struct {
	kind      catalog.MediaKind
	rootID    string
	profileID string
	plexType  string
}

var kinds = []kindPlan{
	{catalog.KindMovie, "movie_root", "movie_profile", "movie"},
	{catalog.KindSeries, "series_root", "series_profile", "show"},
}

// Run looks at the current setup and, when apply is true, creates what is
// missing and provable. Run never changes or removes existing settings.
func (service *Service) Run(ctx context.Context, apply bool) (Report, error) {
	service.mutex.Lock()
	defer service.mutex.Unlock()
	report := Report{Applied: apply, Steps: []Step{}}
	integrations, err := service.Store.ListIntegrations(ctx)
	if err != nil {
		return Report{}, err
	}
	roots, err := service.Store.ListLibraryRoots(ctx)
	if err != nil {
		return Report{}, err
	}
	profiles, err := service.Store.ListQualityProfiles(ctx)
	if err != nil {
		return Report{}, err
	}

	rootSteps := service.rootSteps(ctx, integrations, roots, apply)
	for index, plan := range kinds {
		report.Steps = append(report.Steps, rootSteps[index])
		report.Steps = append(report.Steps, service.profileStep(ctx, plan, profiles, apply))
	}
	return report, nil
}

func hasDefaultRoot(roots []postgres.LibraryRoot, kind catalog.MediaKind) *postgres.LibraryRoot {
	for index := range roots {
		root := roots[index]
		if root.MediaKind == kind && root.Default && root.Enabled {
			return &roots[index]
		}
	}
	return nil
}

func rootForLibrary(roots []postgres.LibraryRoot, kind catalog.MediaKind, plexID, libraryKey string) *postgres.LibraryRoot {
	for index := range roots {
		root := &roots[index]
		// A 4K root may share the library (movies/2160p next to
		// movies/1080p); it is not the standard folder this step looks for.
		if root.MediaKind == kind && root.Edition != postgres.Edition4K && root.PlexInstanceID != nil && root.PlexInstanceID.String() == plexID && root.PlexLibraryID == libraryKey {
			return root
		}
	}
	return nil
}

func (service *Service) rootSteps(ctx context.Context, integrations []postgres.Integration, roots []postgres.LibraryRoot, apply bool) []Step {
	steps := make([]Step, len(kinds))
	pending := []int{}
	for index, plan := range kinds {
		steps[index] = Step{ID: plan.rootID}
		if existing := hasDefaultRoot(roots, plan.kind); existing != nil {
			steps[index].State, steps[index].Name = StateExists, existing.Name
			continue
		}
		pending = append(pending, index)
	}
	if len(pending) == 0 {
		return steps
	}
	block := func(reason, detail string) []Step {
		for _, index := range pending {
			steps[index].State, steps[index].Reason, steps[index].Detail = StateBlocked, reason, detail
		}
		return steps
	}
	plexIntegration := defaultIntegration(integrations, "plex")
	if plexIntegration == nil {
		return block(ReasonNoPlex, "")
	}
	plex, err := service.OpenPlex(ctx, *plexIntegration)
	if err != nil {
		return block(ReasonPlexUnreachable, err.Error())
	}
	sections, err := plex.Sections(ctx)
	if err != nil {
		return block(ReasonPlexUnreachable, err.Error())
	}
	altMount := defaultIntegration(integrations, "altmount")
	localSource := ""
	if altMount != nil {
		var config struct {
			LocalReadOnlyRoot string `json:"local_read_only_root"`
		}
		_ = json.Unmarshal(altMount.Config, &config)
		localSource = filepath.Clean(strings.TrimSpace(config.LocalReadOnlyRoot))
		if !filepath.IsAbs(localSource) {
			localSource = ""
		}
	}
	if localSource == "" {
		return block(ReasonNoAltMount, "")
	}
	source, proven, sourceErr := service.sourceVisibleRoot(ctx, plex, localSource, roots)

	for _, index := range pending {
		plan := kinds[index]
		step := &steps[index]
		section := chooseSection(sections, plan.plexType)
		if section == nil {
			service.newLibraryStep(ctx, plex, plexIntegration.ID, plan, step, localSource, roots, source, proven, sourceErr, apply)
			continue
		}
		step.Name = section.Title
		if existing := rootForLibrary(roots, plan.kind, plexIntegration.ID.String(), section.Key); existing != nil {
			// The library already has a folder that is just not the default;
			// a second one would duplicate it.
			step.State, step.Reason, step.Name = StateBlocked, ReasonRootNotDefault, existing.Name
			continue
		}
		// Plex lists a library's folders alphabetically, so the folder Media
		// Manager can write may be any of them.
		var plexPath, destination string
		err := errors.New("no standard folder")
		for _, location := range standardLocations(*section) {
			plexPath = path.Clean(location)
			if destination, err = service.proveDestination(ctx, plex, plan.kind, plexPath, localSource, roots); err == nil {
				break
			}
		}
		if err != nil {
			step.State, step.Reason, step.Detail = StateBlocked, ReasonFolderNotShared, plexPath
			continue
		}
		if sourceErr != nil {
			step.State, step.Reason, step.Detail = StateBlocked, ReasonSourceNotVisible, localSource
			continue
		}
		plexID := plexIntegration.ID
		root := postgres.LibraryRoot{MediaKind: plan.kind, Name: section.Title, DestinationRoot: destination, PlexVisibleRoot: plexPath, SourceVisibleRoot: source, PlexInstanceID: &plexID, PlexLibraryID: section.Key, Default: true, Enabled: true, Edition: postgres.EditionStandard}
		provenCopy := proven
		step.SourceProven = &provenCopy
		if !apply {
			step.State, step.Root = StateCreate, &root
			continue
		}
		created, err := service.Store.CreateLibraryRoot(ctx, root)
		if err != nil {
			step.State, step.Reason, step.Detail = StateBlocked, "create_failed", err.Error()
			continue
		}
		step.State, step.Root = StateCreated, &created
	}
	return steps
}

// newLibraryStep handles a kind Plex has no library for. When Plex sees
// Media Manager's own library folder at the same path (as a Plex installed
// with Media Manager does), the Plex library is created with that folder;
// otherwise the step is blocked.
func (service *Service) newLibraryStep(ctx context.Context, plex Plex, plexID uuid.UUID, plan kindPlan, step *Step, localSource string, roots []postgres.LibraryRoot, source string, proven bool, sourceErr error, apply bool) {
	step.State, step.Reason = StateBlocked, ReasonNoPlexLibrary
	creator, canCreate := plex.(PlexLibraryCreator)
	folder := service.LibraryFolder(plan.kind)
	if !canCreate || folder == "" {
		return
	}
	plexPath := filepath.ToSlash(folder)
	destination, err := service.proveDestination(ctx, plex, plan.kind, plexPath, localSource, roots)
	if err != nil || destination != folder {
		return
	}
	if sourceErr != nil {
		step.Reason, step.Detail = ReasonSourceNotVisible, localSource
		return
	}
	name := newLibraryNames[plan.plexType]
	root := postgres.LibraryRoot{MediaKind: plan.kind, Name: name, DestinationRoot: destination, PlexVisibleRoot: plexPath, SourceVisibleRoot: source, PlexInstanceID: &plexID, Default: true, Enabled: true, Edition: postgres.EditionStandard}
	provenCopy := proven
	step.Name, step.SourceProven, step.CreatesPlexLibrary, step.Reason = name, &provenCopy, true, ""
	if !apply {
		step.State, step.Root = StateCreate, &root
		return
	}
	step.State = StateBlocked
	if err := creator.CreateSection(ctx, plan.plexType, name, newLibraryLanguage, plexPath); err != nil {
		step.Reason, step.Detail = "create_failed", err.Error()
		return
	}
	sections, err := plex.Sections(ctx)
	if err != nil {
		step.Reason, step.Detail = "create_failed", err.Error()
		return
	}
	for _, section := range sections {
		if section.Type != plan.plexType {
			continue
		}
		for _, location := range section.Locations {
			if path.Clean(location) == plexPath {
				root.PlexLibraryID = section.Key
			}
		}
	}
	if root.PlexLibraryID == "" {
		step.Reason, step.Detail = "create_failed", "Plex did not list the new library"
		return
	}
	created, err := service.Store.CreateLibraryRoot(ctx, root)
	if err != nil {
		step.Reason, step.Detail = "create_failed", err.Error()
		return
	}
	step.State, step.Root = StateCreated, &created
}

func (service *Service) profileStep(ctx context.Context, plan kindPlan, profiles []postgres.QualityProfile, apply bool) Step {
	step := Step{ID: plan.profileID, Name: ProfileName}
	for _, profile := range profiles {
		if profile.MediaKind == plan.kind && profile.Default && profile.Enabled {
			step.State, step.Name = StateExists, profile.Name
			return step
		}
	}
	for _, profile := range profiles {
		if profile.MediaKind == plan.kind && strings.EqualFold(strings.TrimSpace(profile.Name), ProfileName) {
			step.State, step.Reason = StateBlocked, ReasonProfileNameTaken
			return step
		}
	}
	if !apply {
		step.State = StateCreate
		return step
	}
	policy, err := danishProfilePolicy()
	if err != nil {
		step.State, step.Reason, step.Detail = StateBlocked, "create_failed", err.Error()
		return step
	}
	if _, err := service.Store.CreateQualityProfile(ctx, postgres.QualityProfile{MediaKind: plan.kind, Name: ProfileName, PolicyVersion: 1, Policy: policy, UpgradesEnabled: true, Default: true, Enabled: true}); err != nil {
		step.State, step.Reason, step.Detail = StateBlocked, "create_failed", err.Error()
		return step
	}
	step.State = StateCreated
	return step
}

// danishProfilePolicy is the "Balanced" preset with Danish subtitles
// required; a Danish audio track satisfies the requirement as well.
func danishProfilePolicy() (json.RawMessage, error) {
	var rules []releases.Rule
	for _, preset := range releases.Presets() {
		if preset.Preset == releases.PresetBalanced {
			rules = preset.Rules
		}
	}
	if len(rules) == 0 {
		return nil, errors.New("the Balanced preset is missing")
	}
	return json.Marshal(map[string]any{
		"rules":                        rules,
		"required_subtitle_languages":  []string{"da"},
		"preferred_subtitle_languages": []string{},
		"required_audio_languages":     []string{},
	})
}

func defaultIntegration(integrations []postgres.Integration, kind string) *postgres.Integration {
	var fallback *postgres.Integration
	for index := range integrations {
		item := &integrations[index]
		if item.Kind != kind || !item.Enabled {
			continue
		}
		if item.Default {
			return item
		}
		if fallback == nil {
			fallback = item
		}
	}
	return fallback
}

// standardLocation picks the library folder for standard copies: a Plex
// library can list a 1080p and a 2160p folder that Plex merges into versions
// of one title, and the 2160p one belongs to a 4K root, never the default.
// A library whose only folders are 4K folders has no standard location ("").
func standardLocation(section PlexSection) string {
	if locations := standardLocations(section); len(locations) > 0 {
		return locations[0]
	}
	return ""
}

// standardLocations is every standard (non-4K) folder of a library.
func standardLocations(section PlexSection) []string {
	var locations []string
	for _, location := range section.Locations {
		lower := strings.ToLower(location)
		if strings.HasPrefix(location, "/") && !strings.Contains(lower, "4k") && !strings.Contains(lower, "2160") {
			locations = append(locations, location)
		}
	}
	return locations
}

// chooseSection picks the first ordinary (non-4K) library of the type that
// has a standard (non-4K) folder; a 4K library is never made the default.
func chooseSection(sections []PlexSection, plexType string) *PlexSection {
	for index := range sections {
		section := &sections[index]
		if section.Type != plexType || standardLocation(*section) == "" || strings.Contains(strings.ToLower(section.Title), "4k") {
			continue
		}
		return section
	}
	return nil
}

// destinationCandidates lists folders Media Manager might know Plex's
// library folder as, best first.
func (service *Service) destinationCandidates(kind catalog.MediaKind, plexPath, localSource string, roots []postgres.LibraryRoot) []string {
	bases := service.Bases
	if bases == nil {
		bases = defaultBases
	}
	var candidates []string
	add := func(candidate string) {
		candidate = filepath.Clean(candidate)
		if !filepath.IsAbs(candidate) || within(candidate, localSource) {
			return
		}
		for _, existing := range candidates {
			if existing == candidate {
				return
			}
		}
		info, err := os.Stat(candidate)
		if err != nil || !info.IsDir() {
			return
		}
		candidates = append(candidates, candidate)
	}
	// Existing roots show how the two views differ.
	for _, root := range roots {
		if mapped, ok := mapPrefix(plexPath, root.PlexVisibleRoot, root.DestinationRoot); ok {
			add(mapped)
		}
	}
	parts := strings.Split(strings.Trim(plexPath, "/"), "/")
	for _, base := range append(append([]string{}, bases[kind]...), bases[""]...) {
		add(base)
		for take := 1; take <= 2 && take <= len(parts); take++ {
			add(filepath.Join(append([]string{base}, parts[len(parts)-take:]...)...))
		}
	}
	add(plexPath)
	return candidates
}

// mapPrefix maps path through the longest shared suffix of a known pair.
func mapPrefix(plexPath, knownPlex, knownDestination string) (string, bool) {
	plexParts := strings.Split(strings.Trim(path.Clean(knownPlex), "/"), "/")
	destinationParts := strings.Split(strings.Trim(filepath.Clean(knownDestination), "/"), "/")
	shared := 0
	for shared < len(plexParts)-1 && shared < len(destinationParts)-1 && plexParts[len(plexParts)-1-shared] == destinationParts[len(destinationParts)-1-shared] {
		shared++
	}
	if shared == 0 {
		return "", false
	}
	plexPrefix := "/" + strings.Join(plexParts[:len(plexParts)-shared], "/")
	destinationPrefix := "/" + strings.Join(destinationParts[:len(destinationParts)-shared], "/")
	if plexPath != plexPrefix && !strings.HasPrefix(plexPath, plexPrefix+"/") {
		return "", false
	}
	return filepath.Join(destinationPrefix, strings.TrimPrefix(plexPath, plexPrefix)), true
}

func within(candidate, root string) bool {
	if root == "" {
		return false
	}
	return candidate == root || strings.HasPrefix(candidate, strings.TrimSuffix(root, "/")+"/")
}

// LibraryFolder is the folder Media Manager is given for a kind's library,
// "" when it has none.
func (service *Service) LibraryFolder(kind catalog.MediaKind) string {
	bases := service.Bases
	if bases == nil {
		bases = defaultBases
	}
	for _, base := range bases[kind] {
		if info, err := os.Stat(base); err == nil && info.IsDir() {
			return base
		}
	}
	return ""
}

// proveDestination finds the folder Media Manager writes that Plex sees at
// plexPath, by marker folders. Every marker is removed before it returns.
func (service *Service) proveDestination(ctx context.Context, plex Plex, kind catalog.MediaKind, plexPath, localSource string, roots []postgres.LibraryRoot) (string, error) {
	candidates := service.destinationCandidates(kind, plexPath, localSource, roots)
	if len(candidates) == 0 {
		return "", errors.New("no candidate folder")
	}
	token, err := randomHex()
	if err != nil {
		return "", err
	}
	type marker struct{ folder, name string }
	var markers []marker
	defer func() {
		for _, item := range markers {
			_ = os.Remove(filepath.Join(item.folder, item.name))
		}
	}()
	for index, candidate := range candidates {
		name := markerPrefix + token + "-" + strconv.Itoa(index)
		if err := os.Mkdir(filepath.Join(candidate, name), 0o755); err != nil {
			continue
		}
		markers = append(markers, marker{candidate, name})
	}
	if len(markers) == 0 {
		return "", errors.New("Media Manager cannot write any candidate folder")
	}
	browseCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	listed, err := plex.Browse(browseCtx, plexPath)
	if err != nil {
		return "", err
	}
	seen := map[string]bool{}
	for _, entry := range listed {
		seen[path.Base(entry)] = true
	}
	for _, item := range markers {
		if seen[item.name] {
			return item.folder, nil
		}
	}
	return "", errors.New("Plex does not see any folder Media Manager can write")
}

// sourceVisibleRoot finds where Plex sees AltMount's files: the folders
// Media Manager sees in localSource must be the folders Plex lists there.
// proven is false when localSource is empty and the path could only be
// confirmed to exist for Plex.
func (service *Service) sourceVisibleRoot(ctx context.Context, plex Plex, localSource string, roots []postgres.LibraryRoot) (string, bool, error) {
	entries, err := os.ReadDir(localSource)
	if err != nil {
		return "", false, err
	}
	local := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			local[entry.Name()] = true
		}
	}
	extra := service.SourceCandidates
	if extra == nil {
		extra = defaultSourceCandidates
	}
	candidates := []string{}
	add := func(candidate string) {
		candidate = path.Clean(candidate)
		for _, existing := range candidates {
			if existing == candidate {
				return
			}
		}
		candidates = append(candidates, candidate)
	}
	counts := map[string]int{}
	for _, root := range roots {
		counts[root.SourceVisibleRoot]++
	}
	known := make([]string, 0, len(counts))
	for value := range counts {
		known = append(known, value)
	}
	sort.SliceStable(known, func(i, j int) bool { return counts[known[i]] > counts[known[j]] })
	for _, value := range known {
		add(value)
	}
	add(filepath.ToSlash(localSource))
	// Where "Ret Plex" puts the folder when Plex uses the same place for
	// something else.
	add(path.Join("/media-manager", filepath.ToSlash(localSource)))
	for _, candidate := range extra {
		add(candidate)
	}
	for _, candidate := range candidates {
		browseCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		listed, err := plex.Browse(browseCtx, candidate)
		cancel()
		if err != nil {
			continue
		}
		names := map[string]bool{}
		for _, entry := range listed {
			names[path.Base(entry)] = true
		}
		if len(local) == 0 {
			continue
		}
		matched := true
		for name := range local {
			if !names[name] {
				matched = false
				break
			}
		}
		if matched {
			return candidate, true, nil
		}
	}
	if len(local) == 0 {
		// AltMount has no folders yet, so there is nothing to compare. Plex
		// must at least see a folder at the path Media Manager uses: its
		// parent lists it.
		candidate := filepath.ToSlash(localSource)
		browseCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		listed, err := plex.Browse(browseCtx, path.Dir(candidate))
		if err == nil {
			for _, entry := range listed {
				if path.Clean(entry) == candidate {
					return candidate, false, nil
				}
			}
		}
	}
	return "", false, errors.New("Plex does not see AltMount's folders")
}

func randomHex() (string, error) {
	var buffer [6]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer[:]), nil
}
