package intake

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/media-manager/media-manager/internal/adapters/mdblist"
	"github.com/media-manager/media-manager/internal/adapters/seerr"
	"github.com/media-manager/media-manager/internal/catalog"
	metadataservice "github.com/media-manager/media-manager/internal/metadata"
	"github.com/media-manager/media-manager/internal/postgres"
)

type importListFetcher func(context.Context, mdblist.ListRef) ([]mdblist.Item, error)

func (fetch importListFetcher) Fetch(ctx context.Context, ref mdblist.ListRef) ([]mdblist.Item, error) {
	return fetch(ctx, ref)
}

type importListMetadata struct {
	metadata    seerr.Metadata
	metadataErr error
	seasonErr   error
	seasons     []int16
}

func (*importListMetadata) Search(context.Context, string, int) (seerr.SearchPage, error) {
	return seerr.SearchPage{}, errors.New("list intake must use exact identities, not search")
}

func (provider *importListMetadata) Metadata(context.Context, string, int64) (seerr.Metadata, error) {
	return provider.metadata, provider.metadataErr
}

func (provider *importListMetadata) SeasonMetadata(_ context.Context, _ int64, season int16) (seerr.SeasonMetadata, error) {
	provider.seasons = append(provider.seasons, season)
	if season == 2 {
		return seerr.SeasonMetadata{}, provider.seasonErr
	}
	return seerr.SeasonMetadata{SeasonNumber: season, Episodes: []seerr.EpisodeMetadata{{ID: 880001, EpisodeNumber: 1, Name: "First Light", AirDate: "2024-01-01"}}}, nil
}

// Use the same isolated-schema lifecycle as the acquisition integration fixtures.
func openImportListTestStore(t *testing.T) *postgres.Store {
	t.Helper()
	databaseURL := os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx := t.Context()
	schema := "mm_import_list_" + strings.ReplaceAll(uuid.New().String(), "-", "")
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(cleanup, `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
		admin.Close()
	})
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	store, err := postgres.Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	if err := postgres.Migrate(ctx, store.Pool); err != nil {
		t.Fatal(err)
	}
	return store
}

func newImportListTestService(t *testing.T, store *postgres.Store, now time.Time, slug string) (*ImportListService, postgres.ImportList, postgres.OutboxEvent) {
	t.Helper()
	list, err := store.CreateMDBList(t.Context(), postgres.MDBListSettings{Owner: "example-owner", Slug: slug, Enabled: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	service := &ImportListService{Store: store, Now: func() time.Time { return now }}
	event := postgres.OutboxEvent{Payload: json.RawMessage(`{"import_list_id":"` + list.ID.String() + `"}`)}
	return service, list, event
}

func assertImportListCreatedNoWork(t *testing.T, store *postgres.Store) {
	t.Helper()
	var media, workflows, searches int
	if err := store.Pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM media_items), (SELECT count(*) FROM workflows), (SELECT count(*) FROM outbox_events WHERE event_type='workflow.search_requested')`).Scan(&media, &workflows, &searches); err != nil {
		t.Fatal(err)
	}
	if media != 0 || workflows != 0 || searches != 0 {
		t.Fatalf("unexpected acquisition work: media=%d workflows=%d searches=%d", media, workflows, searches)
	}
}

// Missing metadata configuration keeps titles pending and retries without queuing downloads.
func TestImportListKeepsPendingTitlesWhenMetadataIsUnconfigured(t *testing.T) {
	store := openImportListTestStore(t)
	now := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	service, list, event := newImportListTestService(t, store, now, "needs-metadata")
	service.Lists = importListFetcher(func(context.Context, mdblist.ListRef) ([]mdblist.Item, error) {
		return []mdblist.Item{{Kind: "movie", TMDBID: 880001, Title: "The Copper Moon", Rank: 1}}, nil
	})
	service.PlexTitles = func(context.Context, catalog.MediaKind) (map[string]bool, error) {
		t.Fatal("Plex was queried before metadata was configured")
		return nil, nil
	}
	for range 2 {
		if err := service.Handle(t.Context(), event); err != nil {
			t.Fatal(err)
		}
	}
	stored, err := store.ImportList(t.Context(), list.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.LastSyncState != "needs_configuration" || stored.LastSyncDetail != "metadata_unavailable" || stored.LastSyncedAt == nil || !stored.LastSyncedAt.Equal(now) || stored.NextSyncAt == nil || !stored.NextSyncAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("configuration failure was not recorded truthfully: %+v", stored)
	}
	entries, err := store.ImportListEntries(t.Context(), list.ID)
	if err != nil || len(entries) != 1 || entries[0].State != "pending" || entries[0].Attempts != 0 {
		t.Fatalf("pending titles = %+v, %v", entries, err)
	}
	assertImportListCreatedNoWork(t, store)
}

// Incomplete identity or season metadata cannot create a partial title or start a search.
func TestImportListRejectsIncompleteTitlesBeforeCreatingWork(t *testing.T) {
	store := openImportListTestStore(t)
	now := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	for _, test := range []struct {
		name    string
		kind    string
		data    seerr.Metadata
		err     error
		detail  string
		seasons []int16
	}{
		{name: "missing-year", kind: "movie", data: seerr.Metadata{Title: "The Copper Moon"}, detail: "identity_missing"},
		{name: "metadata-outage", kind: "movie", err: errors.New("metadata unavailable"), detail: "metadata_failed"},
		{name: "missing-defaults", kind: "movie", data: seerr.Metadata{Title: "The Copper Moon", ReleaseDate: "2024-01-01"}, detail: "defaults_missing"},
		{name: "specials-only", kind: "series", data: seerr.Metadata{Name: "Lantern Harbour", FirstAirDate: "2024-01-01", Seasons: []seerr.SeasonSummary{{SeasonNumber: 0}, {SeasonNumber: 1000}}}, detail: "no_episodes"},
		{name: "later-season-fails", kind: "series", data: seerr.Metadata{Name: "Lantern Harbour", FirstAirDate: "2024-01-01", Seasons: []seerr.SeasonSummary{{SeasonNumber: 0}, {SeasonNumber: 1}, {SeasonNumber: 2}}}, detail: "metadata_failed", seasons: []int16{1, 2}},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, list, event := newImportListTestService(t, store, now, test.name)
			provider := &importListMetadata{metadata: test.data, metadataErr: test.err, seasonErr: errors.New("season metadata unavailable")}
			service.Discovery = func(context.Context) (metadataservice.DiscoveryProvider, bool) { return provider, true }
			service.Lists = importListFetcher(func(context.Context, mdblist.ListRef) ([]mdblist.Item, error) {
				return []mdblist.Item{{Kind: test.kind, TMDBID: 880001, Title: "List Claim", Year: 2024, Rank: 1}}, nil
			})
			if err := service.Handle(t.Context(), event); err != nil {
				t.Fatal(err)
			}
			entries, err := store.ImportListEntries(t.Context(), list.ID)
			if err != nil || len(entries) != 1 || entries[0].State != "failed" || entries[0].Detail != test.detail || entries[0].Attempts != 1 || entries[0].MediaID != nil {
				t.Fatalf("incomplete title = %+v, %v; want %s", entries, err, test.detail)
			}
			if !reflect.DeepEqual(provider.seasons, test.seasons) {
				t.Fatalf("season lookups = %v, want %v", provider.seasons, test.seasons)
			}
			assertImportListCreatedNoWork(t, store)
		})
	}
}

// Cancellation during a list fetch is retriable work, not a persisted provider failure.
func TestImportListCancellationPreservesTheLastSuccessfulRead(t *testing.T) {
	store := openImportListTestStore(t)
	now := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	service, list, event := newImportListTestService(t, store, now, "cancelled-read")
	prior, next := now.Add(-time.Hour), now.Add(5*time.Hour)
	if err := store.FinishImportListSync(t.Context(), list.ID, "ok", "", true, next, prior); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	service.Lists = importListFetcher(func(context.Context, mdblist.ListRef) ([]mdblist.Item, error) {
		cancel()
		return nil, mdblist.ErrUnavailable
	})
	if err := service.Handle(ctx, event); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled fetch = %v, want context cancellation", err)
	}
	stored, err := store.ImportList(t.Context(), list.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.LastSyncState != "ok" || stored.LastSyncDetail != "" || stored.LastSyncedAt == nil || !stored.LastSyncedAt.Equal(prior) || stored.NextSyncAt == nil || !stored.NextSyncAt.Equal(next) {
		t.Fatalf("cancellation overwrote the last read: %+v", stored)
	}
	assertImportListCreatedNoWork(t, store)
}

// A film's TVDB number and another media kind's TMDB number cannot hide a missing film.
func TestImportListDoesNotMistakeUnrelatedPlexIdentitiesForItsTitle(t *testing.T) {
	store := openImportListTestStore(t)
	now := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	service, list, event := newImportListTestService(t, store, now, "identity-collision")
	provider := &importListMetadata{metadataErr: errors.New("exact title metadata unavailable")}
	service.Discovery = func(context.Context) (metadataservice.DiscoveryProvider, bool) { return provider, true }
	service.Lists = importListFetcher(func(context.Context, mdblist.ListRef) ([]mdblist.Item, error) {
		return []mdblist.Item{{Kind: "movie", TMDBID: 880001, TVDBID: 770001, Title: "The Copper Moon", Rank: 1}}, nil
	})
	service.PlexTitles = func(_ context.Context, kind catalog.MediaKind) (map[string]bool, error) {
		if kind == catalog.KindMovie {
			return map[string]bool{"tvdb://770001": true}, nil
		}
		return map[string]bool{"tmdb://880001": true}, nil
	}
	if err := service.Handle(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	entries, err := store.ImportListEntries(t.Context(), list.ID)
	if err != nil || len(entries) != 1 || entries[0].State != "failed" || entries[0].Detail != "metadata_failed" {
		t.Fatalf("unrelated Plex identity suppressed a missing title: %+v, %v", entries, err)
	}
	assertImportListCreatedNoWork(t, store)
}
