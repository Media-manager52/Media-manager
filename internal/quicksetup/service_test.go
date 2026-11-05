package quicksetup

import (
	"context"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"uuid"

	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/postgres"
)

type memoryStore struct {
	integrations []postgres.Integration
	roots        []postgres.LibraryRoot
	profiles     []postgres.QualityProfile
}

func (store *memoryStore) ListIntegrations(context.Context) ([]postgres.Integration, error) {
	return append([]postgres.Integration(nil), store.integrations...), nil
}
func (store *memoryStore) ListLibraryRoots(context.Context) ([]postgres.LibraryRoot, error) {
	return append([]postgres.LibraryRoot(nil), store.roots...), nil
}
func (store *memoryStore) ListQualityProfiles(context.Context) ([]postgres.QualityProfile, error) {
	return append([]postgres.QualityProfile(nil), store.profiles...), nil
}
func (store *memoryStore) CreateLibraryRoot(_ context.Context, root postgres.LibraryRoot) (postgres.LibraryRoot, error) {
	root.ID = uuid.NewV7()
	store.roots = append(store.roots, root)
	return root, nil
}
func (store *memoryStore) CreateQualityProfile(_ context.Context, profile postgres.QualityProfile) (postgres.QualityProfile, error) {
	profile.ID = uuid.NewV7()
	store.profiles = append(store.profiles, profile)
	return profile, nil
}

// viewPlex is a Plex that sees host folders under its own paths, the way a
// Plex container with different mounts does.
type viewPlex struct {
	sections []PlexSection
	mounts   map[string]string // Plex path -> host folder
	browsed  []string
}

func (fake *viewPlex) Sections(context.Context) ([]PlexSection, error) { return fake.sections, nil }

func (fake *viewPlex) Browse(_ context.Context, plexPath string) ([]string, error) {
	fake.browsed = append(fake.browsed, plexPath)
	for mount, host := range fake.mounts {
		if plexPath != mount && !strings.HasPrefix(plexPath, mount+"/") {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(host, strings.TrimPrefix(plexPath, mount)))
		if err != nil {
			return []string{}, nil // Plex lists nothing for a missing folder
		}
		var folders []string
		for _, entry := range entries {
			if entry.IsDir() {
				folders = append(folders, path.Join(plexPath, entry.Name()))
			}
		}
		return folders, nil
	}
	return []string{}, nil
}

type fixture struct {
	host    string
	store   *memoryStore
	plex    *viewPlex
	service *Service
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	host := t.TempDir()
	for _, folder := range []string{"movies", "tv", "media/Serier", "altmount/content", "altmount/complete"} {
		if err := os.MkdirAll(filepath.Join(host, folder), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	plexID, altID := uuid.NewV7(), uuid.NewV7()
	altConfig, _ := json.Marshal(map[string]any{"local_read_only_root": filepath.Join(host, "altmount")})
	store := &memoryStore{integrations: []postgres.Integration{
		{ID: plexID, Kind: "plex", Name: "Plex", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://plex:32400"}`)},
		{ID: altID, Kind: "altmount", Name: "AltMount", Enabled: true, Default: true, Config: altConfig},
	}}
	plex := &viewPlex{
		sections: []PlexSection{
			{Key: "3", Title: "Film 4K", Type: "movie", Locations: []string{"/data/movies4k"}},
			{Key: "1", Title: "Film", Type: "movie", Locations: []string{"/data/movies"}},
			{Key: "2", Title: "Serier", Type: "show", Locations: []string{"/tvshows/Serier"}},
			{Key: "9", Title: "Musik", Type: "artist", Locations: []string{"/music"}},
		},
		mounts: map[string]string{
			"/data/movies":  filepath.Join(host, "movies"),
			"/tvshows":      filepath.Join(host, "media"),
			"/mnt/altmount": filepath.Join(host, "altmount"),
		},
	}
	service := &Service{
		Store:    store,
		OpenPlex: func(context.Context, postgres.Integration) (Plex, error) { return plex, nil },
		// host/tv exists and is writable, but Plex's series library is not
		// there: its marker must not be taken as proof.
		Bases:            map[catalog.MediaKind][]string{catalog.KindMovie: {filepath.Join(host, "movies")}, catalog.KindSeries: {filepath.Join(host, "tv")}, "": {filepath.Join(host, "media")}},
		SourceCandidates: []string{"/mnt/altmount"},
	}
	return fixture{host: host, store: store, plex: plex, service: service}
}

func stepByID(t *testing.T, report Report, id string) Step {
	t.Helper()
	for _, step := range report.Steps {
		if step.ID == id {
			return step
		}
	}
	t.Fatalf("no step %s in %+v", id, report.Steps)
	return Step{}
}

func assertNoMarkers(t *testing.T, root string) {
	t.Helper()
	_ = filepath.WalkDir(root, func(current string, entry os.DirEntry, err error) error {
		if err == nil && strings.HasPrefix(entry.Name(), markerPrefix) {
			t.Errorf("marker left behind: %s", current)
		}
		return nil
	})
}

func TestRecommendedSetupProvesFoldersAndCreatesWhatIsMissing(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()

	preview, err := fx.service.Run(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(fx.store.roots) != 0 || len(fx.store.profiles) != 0 {
		t.Fatal("the preview created settings")
	}
	assertNoMarkers(t, fx.host)
	movie := stepByID(t, preview, "movie_root")
	if movie.State != StateCreate || movie.Root == nil || movie.Name != "Film" {
		t.Fatalf("movie preview = %+v", movie)
	}
	if movie.Root.DestinationRoot != filepath.Join(fx.host, "movies") || movie.Root.PlexVisibleRoot != "/data/movies" || movie.Root.PlexLibraryID != "1" || movie.Root.SourceVisibleRoot != "/mnt/altmount" || movie.SourceProven == nil || !*movie.SourceProven {
		t.Fatalf("movie root = %+v proven=%v", movie.Root, movie.SourceProven)
	}
	series := stepByID(t, preview, "series_root")
	if series.State != StateCreate || series.Root.DestinationRoot != filepath.Join(fx.host, "media", "Serier") || series.Root.PlexVisibleRoot != "/tvshows/Serier" {
		t.Fatalf("series preview = %+v root=%+v", series, series.Root)
	}
	if stepByID(t, preview, "movie_profile").State != StateCreate || stepByID(t, preview, "series_profile").State != StateCreate {
		t.Fatalf("profiles preview = %+v", preview.Steps)
	}

	applied, err := fx.service.Run(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	assertNoMarkers(t, fx.host)
	for _, step := range applied.Steps {
		if step.State != StateCreated {
			t.Fatalf("step %s = %+v", step.ID, step)
		}
	}
	if len(fx.store.roots) != 2 || len(fx.store.profiles) != 2 {
		t.Fatalf("created roots=%d profiles=%d", len(fx.store.roots), len(fx.store.profiles))
	}
	for _, root := range fx.store.roots {
		if !root.Default || !root.Enabled || root.Edition != postgres.EditionStandard || root.PlexInstanceID == nil || *root.PlexInstanceID != fx.store.integrations[0].ID {
			t.Fatalf("root = %+v", root)
		}
	}
	for _, profile := range fx.store.profiles {
		var policy struct {
			Rules    []json.RawMessage `json:"rules"`
			Required []string          `json:"required_subtitle_languages"`
		}
		if err := json.Unmarshal(profile.Policy, &policy); err != nil {
			t.Fatal(err)
		}
		if profile.Name != ProfileName || !profile.Default || !profile.UpgradesEnabled || len(policy.Rules) == 0 || len(policy.Required) != 1 || policy.Required[0] != "da" {
			t.Fatalf("profile = %+v policy=%+v", profile, policy)
		}
	}

	again, err := fx.service.Run(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range again.Steps {
		if step.State != StateExists {
			t.Fatalf("second run step %s = %+v", step.ID, step)
		}
	}
	if len(fx.store.roots) != 2 || len(fx.store.profiles) != 2 {
		t.Fatal("the second run created more settings")
	}
}

func TestRecommendedSetupRefusesWhatItCannotProve(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	// Plex sees the movie folder somewhere Media Manager has no access to.
	fx.plex.mounts["/data/movies"] = t.TempDir()
	// Plex cannot see AltMount.
	delete(fx.plex.mounts, "/mnt/altmount")
	fx.store.profiles = []postgres.QualityProfile{{ID: uuid.NewV7(), MediaKind: catalog.KindSeries, Name: ProfileName, Enabled: false}}

	report, err := fx.service.Run(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	assertNoMarkers(t, fx.host)
	if step := stepByID(t, report, "movie_root"); step.State != StateBlocked || step.Reason != ReasonFolderNotShared || step.Detail != "/data/movies" {
		t.Fatalf("movie root = %+v", step)
	}
	if step := stepByID(t, report, "series_root"); step.State != StateBlocked || step.Reason != ReasonSourceNotVisible {
		t.Fatalf("series root = %+v", step)
	}
	if step := stepByID(t, report, "series_profile"); step.State != StateBlocked || step.Reason != ReasonProfileNameTaken {
		t.Fatalf("an existing profile with the name was not respected: %+v", step)
	}
	if step := stepByID(t, report, "movie_profile"); step.State != StateCreated {
		t.Fatalf("movie profile = %+v", step)
	}
	if len(fx.store.roots) != 0 {
		t.Fatal("an unproven root was created")
	}
}

func TestRecommendedSetupNeedsPlexAndAltMount(t *testing.T) {
	fx := newFixture(t)
	fx.store.integrations = fx.store.integrations[1:]
	report, _ := fx.service.Run(context.Background(), false)
	if step := stepByID(t, report, "movie_root"); step.Reason != ReasonNoPlex {
		t.Fatalf("without Plex = %+v", step)
	}
	fx = newFixture(t)
	fx.store.integrations = fx.store.integrations[:1]
	report, _ = fx.service.Run(context.Background(), false)
	if step := stepByID(t, report, "series_root"); step.Reason != ReasonNoAltMount {
		t.Fatalf("without AltMount = %+v", step)
	}
	fx = newFixture(t)
	fx.plex.sections = fx.plex.sections[:1] // only a 4K movie library
	report, _ = fx.service.Run(context.Background(), false)
	if step := stepByID(t, report, "movie_root"); step.Reason != ReasonNoPlexLibrary {
		t.Fatalf("only a 4K library = %+v", step)
	}
}

func TestRecommendedSetupLearnsFromExistingRoots(t *testing.T) {
	fx := newFixture(t)
	// An existing (non-default) movie root shows Plex's /tvshows is
	// Media Manager's host/media; the series default is still missing.
	plexID := fx.store.integrations[0].ID
	fx.store.roots = []postgres.LibraryRoot{{ID: uuid.NewV7(), MediaKind: catalog.KindMovie, Name: "Film", DestinationRoot: filepath.Join(fx.host, "movies"), PlexVisibleRoot: "/data/movies", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &plexID, PlexLibraryID: "1", Default: true, Enabled: true}}
	fx.service.Bases = map[catalog.MediaKind][]string{}
	if err := os.MkdirAll(filepath.Join(fx.host, "media", "Serier"), 0o755); err != nil {
		t.Fatal(err)
	}
	fx.store.roots = append(fx.store.roots, postgres.LibraryRoot{ID: uuid.NewV7(), MediaKind: catalog.KindSeries, Name: "Old", DestinationRoot: filepath.Join(fx.host, "media", "Old"), PlexVisibleRoot: "/tvshows/Old", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &plexID, PlexLibraryID: "5", Enabled: true})
	report, err := fx.service.Run(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if step := stepByID(t, report, "movie_root"); step.State != StateExists {
		t.Fatalf("movie root = %+v", step)
	}
	if step := stepByID(t, report, "series_root"); step.State != StateCreate || step.Root.DestinationRoot != filepath.Join(fx.host, "media", "Serier") {
		t.Fatalf("series root = %+v", step)
	}
}

func TestRecommendedSetupDoesNotDuplicateANonDefaultRoot(t *testing.T) {
	fx := newFixture(t)
	plexID := fx.store.integrations[0].ID
	fx.store.roots = []postgres.LibraryRoot{{ID: uuid.NewV7(), MediaKind: catalog.KindMovie, Name: "Mine film", DestinationRoot: filepath.Join(fx.host, "movies"), PlexVisibleRoot: "/data/movies", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &plexID, PlexLibraryID: "1", Enabled: true}}
	report, err := fx.service.Run(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if step := stepByID(t, report, "movie_root"); step.State != StateBlocked || step.Reason != ReasonRootNotDefault || step.Name != "Mine film" {
		t.Fatalf("movie root = %+v", step)
	}
	movieRoots := 0
	for _, root := range fx.store.roots {
		if root.MediaKind == catalog.KindMovie {
			movieRoots++
		}
	}
	if movieRoots != 1 {
		t.Fatalf("movie roots = %d, want the existing one only", movieRoots)
	}
}
