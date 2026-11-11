package intake

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"strconv"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/adapters/mdblist"
	"github.com/media-manager/media-manager/internal/catalog"
	metadataservice "github.com/media-manager/media-manager/internal/metadata"
	"github.com/media-manager/media-manager/internal/postgres"
)

// ListFetcher reads a public MDBList list.
type ListFetcher interface {
	Fetch(ctx context.Context, ref mdblist.ListRef) ([]mdblist.Item, error)
}

// ImportListService follows MDBList lists like Radarr's and Sonarr's list
// imports: every new title on a list is added through the same path as
// "Add" (exact TMDB identity, verified year, every season's exact episode
// list) with the list's folder and quality profile, and searched at once.
// Language and quality requirements come from that profile as for any other
// title. Titles already in the library are left alone, titles the owner
// removed are never added again, and a title that leaves the list is kept.
type ImportListService struct {
	Store            *postgres.Store
	Lists            ListFetcher
	Discovery        func(context.Context) (metadataservice.DiscoveryProvider, bool)
	MetadataEnricher *metadataservice.Service
	Now              func() time.Time
}

// Entry details are stable codes; the owner console turns them into words.
const (
	listDetailNotFound            = "list_not_found"
	listDetailTooLong             = "list_too_long"
	listDetailUnavailable         = "mdblist_unavailable"
	listDetailMetadataUnavailable = "metadata_unavailable"
	entryDetailMetadataFailed     = "metadata_failed"
	entryDetailIdentityMissing    = "identity_missing"
	entryDetailNoEpisodes         = "no_episodes"
	entryDetailDefaultsMissing    = "defaults_missing"
	entryDetailAddFailed          = "add_failed"
)

// listRetryAfterFailure is how soon a list that could not be read is tried
// again.
const listRetryAfterFailure = time.Hour

var mediaIDInHint = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

func (service *ImportListService) now() time.Time {
	if service.Now != nil {
		return service.Now().UTC()
	}
	return time.Now().UTC()
}

func (service *ImportListService) Handle(ctx context.Context, event postgres.OutboxEvent) error {
	var payload struct {
		ImportListID string `json:"import_list_id"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return err
	}
	listID, err := uuid.Parse(payload.ImportListID)
	if err != nil {
		return errors.New("invalid import-list ID")
	}
	list, err := service.Store.ImportList(ctx, listID)
	if errors.Is(err, postgres.ErrNotFound) {
		return nil // The list was deleted while the read waited.
	}
	if err != nil {
		return err
	}
	if list.Provider != "mdblist" || !list.Enabled {
		return nil
	}
	items, err := service.Lists.Fetch(ctx, mdblist.ListRef{Owner: list.SourceOwner, Slug: list.SourceSlug})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		detail := listDetailUnavailable
		switch {
		case errors.Is(err, mdblist.ErrListNotFound), errors.Is(err, mdblist.ErrInvalidListURL):
			detail = listDetailNotFound
		case errors.Is(err, mdblist.ErrListTooLong):
			detail = listDetailTooLong
		}
		now := service.now()
		return service.Store.FinishImportListSync(ctx, listID, "failed", detail, false, now.Add(listRetryAfterFailure), now)
	}
	seen := make([]postgres.ImportListSeen, 0, len(items))
	for _, item := range items {
		seen = append(seen, postgres.ImportListSeen{MediaKind: item.Kind, TMDBID: item.TMDBID, IMDbID: item.IMDbID, TVDBID: item.TVDBID, Title: item.Title, Year: item.Year, Rank: item.Rank})
	}
	if _, err := service.Store.RecordImportListRead(ctx, listID, seen, service.now()); errors.Is(err, postgres.ErrNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	open, err := service.Store.OpenImportListEntries(ctx, listID, mdblist.MaxItems)
	if err != nil {
		return err
	}
	var provider metadataservice.DiscoveryProvider
	if len(open) > 0 && service.Discovery != nil {
		provider, _ = service.Discovery(ctx)
	}
	if len(open) > 0 && provider == nil {
		now := service.now()
		return service.Store.FinishImportListSync(ctx, listID, "needs_configuration", listDetailMetadataUnavailable, true, now.Add(listRetryAfterFailure), now)
	}
	for _, entry := range open {
		if err := ctx.Err(); err != nil {
			return err
		}
		state, detail, mediaID, err := service.addEntry(ctx, list, entry, provider)
		if err != nil {
			return err
		}
		if err := service.Store.SetImportListEntry(ctx, listID, entry.MediaKind, entry.TMDBID, state, detail, mediaID, service.now()); err != nil {
			return err
		}
	}
	now := service.now()
	return service.Store.FinishImportListSync(ctx, listID, "ok", "", true, now.Add(postgres.MDBListSyncInterval), now)
}

// addEntry adds one title. It returns an error only when the whole read must
// stop (the read was cancelled or the database failed); a title that cannot
// be added now is reported as failed and tried again on the next read.
func (service *ImportListService) addEntry(ctx context.Context, list postgres.ImportList, entry postgres.ImportListEntry, provider metadataservice.DiscoveryProvider) (string, string, *uuid.UUID, error) {
	identities := map[string]string{"tmdb": strconv.FormatInt(entry.TMDBID, 10), "imdb": entry.IMDbID}
	if entry.MediaKind == "series" && entry.TVDBID > 0 {
		// MDBList also gives films a TVDB number; only a series' one is used.
		identities["tvdb"] = strconv.FormatInt(entry.TVDBID, 10)
	}
	known, deleted, err := service.Store.KnownMedia(ctx, entry.MediaKind, identities)
	if err != nil {
		return "", "", nil, err
	}
	if known != nil {
		if deleted {
			return "removed_earlier", "", known, nil
		}
		return "in_library", "", known, nil
	}
	now := service.now()
	kind, mediaType, rootID, profileID := catalog.KindMovie, "movie", list.MovieRootID, list.MovieQualityProfileID
	if entry.MediaKind == "series" {
		kind, mediaType, rootID, profileID = catalog.KindSeries, "tv", list.SeriesRootID, list.SeriesQualityProfileID
	}
	enricher := service.MetadataEnricher
	if enricher == nil {
		enricher = &metadataservice.Service{}
	}
	discovered, err := enricher.Discover(ctx, provider, mediaType, entry.TMDBID, now)
	if err != nil {
		if ctx.Err() != nil {
			return "", "", nil, ctx.Err()
		}
		return "failed", entryDetailMetadataFailed, nil, nil
	}
	item, err := catalog.New(kind, discovered.Title, discovered.Year, discovered.Identities, now)
	if err != nil || item.Year == nil || !hasPublicationIdentity(item.Kind, item.Identities) {
		return "failed", entryDetailIdentityMissing, nil, nil
	}
	if item.Metadata, err = catalog.NormalizeMetadata(discovered.Result.Metadata, now); err != nil {
		return "failed", entryDetailMetadataFailed, nil, nil
	}
	var seasons []postgres.ManualSeason
	if kind == catalog.KindSeries {
		for _, number := range discovered.Seasons {
			// Specials (season 0) are not part of "every season", as in Sonarr.
			if number < 1 || number > 999 {
				continue
			}
			details, seasonErr := provider.SeasonMetadata(ctx, entry.TMDBID, int16(number))
			if seasonErr != nil {
				if ctx.Err() != nil {
					return "", "", nil, ctx.Err()
				}
				return "failed", entryDetailMetadataFailed, nil, nil
			}
			// Each season is frozen from TMDB's exact episode list, never a
			// guessed count, exactly as "Add" does.
			episodes := []postgres.ManualEpisode{}
			for _, episode := range details.Episodes {
				if episode.EpisodeNumber <= 0 {
					continue
				}
				episodes = append(episodes, postgres.ManualEpisode{Number: uint16(episode.EpisodeNumber), ProviderID: "tmdb:" + strconv.FormatInt(episode.ID, 10), Title: episode.Name, AirDate: episode.AirDate})
			}
			if len(episodes) == 0 {
				continue
			}
			seasons = append(seasons, postgres.ManualSeason{Number: uint16(number), Episodes: episodes, Monitored: true, SearchImmediately: true, PreferSeasonPack: true})
		}
		if len(seasons) == 0 {
			return "failed", entryDetailNoEpisodes, nil, nil
		}
	}
	root, profile, client, err := service.Store.ResolveMediaConfiguration(ctx, kind, rootID, profileID, nil)
	if errors.Is(err, postgres.ErrDefaultsMissing) {
		return "failed", entryDetailDefaultsMissing, nil, nil
	}
	if err != nil {
		return "", "", nil, err
	}
	_, err = service.Store.CreateManualMedia(ctx, item, root, profile, client, seasons, true, "import_list")
	var refusal postgres.ActionRefusal
	switch {
	case err == nil:
		mediaID := item.ID
		return "added", "", &mediaID, nil
	case errors.As(err, &refusal) && refusal.Code == "media_exists":
		return "in_library", "", hintMediaID(refusal.Hint), nil
	case errors.As(err, &refusal) && refusal.Code == "media_removed":
		return "removed_earlier", "", hintMediaID(refusal.Hint), nil
	case errors.As(err, &refusal):
		return "failed", refusal.Code, nil, nil
	case ctx.Err() != nil:
		return "", "", nil, ctx.Err()
	default:
		slog.Warn("import list could not add a title", "list", list.ID, "kind", entry.MediaKind, "tmdb", entry.TMDBID, "error", err)
		return "failed", entryDetailAddFailed, nil, nil
	}
}

func hintMediaID(hint string) *uuid.UUID {
	parsed, err := uuid.Parse(mediaIDInHint.FindString(hint))
	if err != nil {
		return nil
	}
	return &parsed
}
