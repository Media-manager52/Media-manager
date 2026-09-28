package publication

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/adapters/plex"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/postgres"
)

type SecretResolver func(string) (string, error)

type PlexService struct {
	Store         *postgres.Store
	ResolveSecret SecretResolver
	Transport     http.RoundTripper
}

type plexConfig struct {
	BaseURL          string `json:"base_url"`
	AllowHTTP        bool   `json:"allow_http"`
	RequestTimeoutMS int64  `json:"request_timeout_ms"`
}

type plexSecret struct {
	Token string `json:"token"`
}

func (service *PlexService) HandleScan(ctx context.Context, event postgres.OutboxEvent) error {
	scan, err := service.Store.LoadPlexScan(ctx, event.AggregateID)
	if err != nil {
		return err
	}
	if scan.State == "confirmed" || scan.State == "polling" {
		return nil
	}
	if event.AggregateRevision != scan.Revision {
		return nil
	}
	if now := time.Now().UTC(); !now.Before(scan.DeadlineAt) {
		return service.Store.TimeoutPlexScan(ctx, scan, plexTimeoutDetail(nil), now)
	}
	if scan.State != "planned" {
		return nil
	}
	client, err := service.ClientFor(scan.Integration)
	if err != nil {
		return err
	}
	refreshPath := filepath.Dir(scan.Versions[0].PlexVisiblePath)
	for _, version := range scan.Versions[1:] {
		if filepath.Dir(version.PlexVisiblePath) != refreshPath {
			refreshPath = scan.Root.PlexVisibleRoot
			break
		}
	}
	if err := client.Refresh(ctx, scan.Root.PlexLibraryID, refreshPath); err != nil {
		return err
	}
	return service.Store.MarkPlexRefreshRequested(ctx, scan, time.Now().UTC().Add(5*time.Second))
}

func (service *PlexService) HandleConfirm(ctx context.Context, event postgres.OutboxEvent) error {
	scan, err := service.Store.LoadPlexScan(ctx, event.AggregateID)
	if err != nil {
		return err
	}
	if scan.State == "confirmed" || scan.State == "timed_out" || scan.State == "failed" {
		return nil
	}
	if scan.State != "polling" {
		return nil
	}
	if event.AggregateRevision != scan.Revision {
		return nil
	}
	now := time.Now().UTC()
	if !now.Before(scan.DeadlineAt) {
		return service.Store.TimeoutPlexScan(ctx, scan, plexTimeoutDetail(plex.ErrNotConfirmed), now)
	}
	client, err := service.ClientFor(scan.Integration)
	if err != nil {
		return err
	}
	guids := plexGUIDs(scan.Media)
	if len(guids) == 0 {
		return errors.New("media has no verified Plex-supported identity")
	}
	confirmations := make([]plex.Confirmation, 0, len(scan.Versions))
	if scan.Media.Kind == catalog.KindMovie {
		if len(scan.Versions) != 1 {
			return errors.New("movie publication contains more than one primary path")
		}
		confirmation, confirmErr := client.Confirm(ctx, plex.Expected{LibraryKey: scan.Root.PlexLibraryID, Title: scan.Media.Title, PlexVisiblePath: scan.Versions[0].PlexVisiblePath, ProviderGUIDs: guids})
		if confirmErr == nil {
			confirmations = append(confirmations, confirmation)
		}
		err = confirmErr
	} else {
		paths := make([]string, 0, len(scan.Versions))
		expectedEpisodes := make(map[string][]plex.EpisodeLeaf, len(scan.Versions))
		for _, version := range scan.Versions {
			paths = append(paths, version.PlexVisiblePath)
			clean := filepath.Clean(version.PlexVisiblePath)
			for _, episode := range version.Episodes {
				expectedEpisodes[clean] = append(expectedEpisodes[clean], plex.EpisodeLeaf{Season: episode.Season, Episode: episode.Episode})
			}
		}
		confirmations, err = client.ConfirmSeries(ctx, plex.ExpectedSeries{LibraryKey: scan.Root.PlexLibraryID, Title: scan.Media.Title, PlexVisiblePaths: paths, ExpectedEpisodes: expectedEpisodes, ProviderGUIDs: guids})
	}
	now = time.Now().UTC()
	if !now.Before(scan.DeadlineAt) {
		lastErr := err
		if lastErr == nil {
			lastErr = errConfirmedAfterDeadline
		}
		return service.Store.TimeoutPlexScan(ctx, scan, plexTimeoutDetail(lastErr), now)
	}
	if errors.Is(err, plex.ErrNotConfirmed) {
		delay := 5*time.Second + time.Duration(scan.PollAttempts)*2*time.Second
		if delay > 30*time.Second {
			delay = 30 * time.Second
		}
		if next := now.Add(delay); next.Before(scan.DeadlineAt) {
			return service.Store.ReschedulePlexConfirmation(ctx, scan, next)
		}
		return service.Store.TimeoutPlexScan(ctx, scan, plexTimeoutDetail(err), now)
	}
	if err != nil {
		return err
	}
	return service.Store.ConfirmPlexScan(ctx, scan, confirmations, now)
}

// plexTimeoutDetail explains, from the last poll outcome, why a scan timed out
// so the owner sees whether Plex never showed the path or matched it to the
// wrong identity. nil means Plex was never polled for this scan.
var errConfirmedAfterDeadline = errors.New("Plex confirmed only after the deadline")

func plexTimeoutDetail(lastErr error) string {
	switch {
	case lastErr == nil:
		return "Plex confirmation was not polled before the deadline"
	case errors.Is(lastErr, errConfirmedAfterDeadline):
		return "Plex confirmed the expected paths only after the deadline; retry confirmation"
	case errors.Is(lastErr, plex.ErrIdentityMismatch):
		return "Plex shows the exact expected path, but matched to a different provider identity than the verified one; fix the Plex match, then retry confirmation"
	case errors.Is(lastErr, plex.ErrNotConfirmed):
		return "Plex did not show every expected path before the deadline"
	default:
		return "Plex confirmation requests failed until the deadline"
	}
}

func (service *PlexService) HandleRecycleRefresh(ctx context.Context, event postgres.OutboxEvent) error {
	entry, root, integration, err := service.Store.PlexForRecycleEntry(ctx, event.AggregateID)
	if err != nil {
		return err
	}
	client, err := service.ClientFor(integration)
	if err != nil {
		return err
	}
	return client.Refresh(ctx, root.PlexLibraryID, filepath.Dir(filepath.Join(root.PlexVisibleRoot, entry.OriginalRelative)))
}

func (service *PlexService) HandleDisplacedRefresh(ctx context.Context, event postgres.OutboxEvent) error {
	link, root, integration, err := service.Store.PlexForDisplacedLink(ctx, event.AggregateID)
	if err != nil {
		return err
	}
	if !link.Removed {
		return postgres.ErrConcurrentModification
	}
	client, err := service.ClientFor(integration)
	if err != nil {
		return err
	}
	return client.Refresh(ctx, root.PlexLibraryID, filepath.Dir(filepath.Join(root.PlexVisibleRoot, link.DestinationRelative)))
}

func (service *PlexService) HandleAdoptedRestoreConfirm(ctx context.Context, event postgres.OutboxEvent) error {
	restore, err := service.Store.LoadAdoptedRestoreContext(ctx, event.AggregateID)
	if err != nil {
		return err
	}
	if restore.Leader.State == "restored" || restore.Leader.State == "failed" {
		return nil
	}
	if restore.Leader.State != "awaiting_confirmation" || event.AggregateRevision != restore.Leader.RestoreRevision {
		return nil
	}
	now := time.Now().UTC()
	if restore.Leader.RestoreDeadlineAt == nil || !now.Before(*restore.Leader.RestoreDeadlineAt) {
		return service.Store.FailAdoptedRestore(ctx, restore.Leader.ID, "Plex did not confirm every restored adopted path before the deadline", now)
	}
	client, err := service.ClientFor(restore.Integration)
	if err != nil {
		return err
	}
	paths := make([]string, 0, len(restore.Entries))
	entryByPath := make(map[string]uuid.UUID, len(restore.Entries))
	for _, entry := range restore.Entries {
		if entry.State != "awaiting_confirmation" {
			return postgres.ErrConcurrentModification
		}
		path := filepath.Clean(filepath.Join(restore.Root.PlexVisibleRoot, entry.OriginalRelative))
		paths = append(paths, path)
		entryByPath[path] = entry.ID
	}
	if restore.Leader.RestoreRefreshAt == nil {
		refreshPath := filepath.Dir(paths[0])
		for _, path := range paths[1:] {
			if filepath.Dir(path) != refreshPath {
				refreshPath = restore.Root.PlexVisibleRoot
				break
			}
		}
		if err := client.Refresh(ctx, restore.Root.PlexLibraryID, refreshPath); err != nil {
			return err
		}
		return service.Store.MarkAdoptedRestoreRefreshed(ctx, restore.Leader.ID, restore.Leader.RestoreRevision, now, now.Add(5*time.Second))
	}
	guids := plexGUIDs(restore.Media)
	if len(guids) == 0 {
		return errors.New("adopted restore media has no verified Plex-supported identity")
	}
	var observed []plex.Confirmation
	if restore.Media.Kind == catalog.KindMovie {
		if len(paths) != 1 {
			return errors.New("movie adopted restore contains more than one primary path")
		}
		confirmation, confirmErr := client.Confirm(ctx, plex.Expected{LibraryKey: restore.Root.PlexLibraryID, Title: restore.Media.Title, PlexVisiblePath: paths[0], ProviderGUIDs: guids})
		if confirmErr == nil {
			observed = append(observed, confirmation)
		}
		err = confirmErr
	} else {
		observed, err = client.ConfirmSeries(ctx, plex.ExpectedSeries{LibraryKey: restore.Root.PlexLibraryID, Title: restore.Media.Title, PlexVisiblePaths: paths, ExpectedEpisodes: map[string][]plex.EpisodeLeaf{}, ProviderGUIDs: guids})
	}
	now = time.Now().UTC()
	if !now.Before(*restore.Leader.RestoreDeadlineAt) {
		return service.Store.FailAdoptedRestore(ctx, restore.Leader.ID, "Plex did not confirm every restored adopted path before the deadline", now)
	}
	if errors.Is(err, plex.ErrNotConfirmed) {
		delay := 5*time.Second + time.Duration(restore.Leader.RestoreAttempts)*2*time.Second
		if delay > 30*time.Second {
			delay = 30 * time.Second
		}
		if next := now.Add(delay); next.Before(*restore.Leader.RestoreDeadlineAt) {
			return service.Store.RescheduleAdoptedRestoreConfirmation(ctx, restore.Leader.ID, restore.Leader.RestoreRevision, now, next)
		}
		return service.Store.FailAdoptedRestore(ctx, restore.Leader.ID, "Plex did not confirm every restored adopted path before the deadline", now)
	}
	if err != nil {
		return err
	}
	confirmations := make([]postgres.AdoptedRestoreConfirmation, 0, len(observed))
	for _, confirmation := range observed {
		entryID, exists := entryByPath[filepath.Clean(confirmation.Path)]
		if !exists {
			return errors.New("Plex returned an unplanned adopted restore path")
		}
		confirmations = append(confirmations, postgres.AdoptedRestoreConfirmation{RecycleEntryID: entryID, ObservedPath: confirmation.Path, RatingKey: confirmation.RatingKey, ProviderGUID: confirmation.ProviderGUID, ConfirmedAt: confirmation.ConfirmedAt})
	}
	return service.Store.CompleteAdoptedRestore(ctx, restore.Leader.ID, confirmations, now)
}

func (service *PlexService) ClientFor(integration postgres.Integration) (*plex.Client, error) {
	if integration.Kind != "plex" || !integration.Enabled {
		return nil, errors.New("Plex integration is disabled")
	}
	var config plexConfig
	if err := json.Unmarshal(integration.Config, &config); err != nil {
		return nil, err
	}
	secret, err := service.ResolveSecret(integration.SecretRef)
	if err != nil {
		return nil, err
	}
	token := strings.TrimSpace(secret)
	var wrapped plexSecret
	if json.Unmarshal([]byte(secret), &wrapped) == nil && strings.TrimSpace(wrapped.Token) != "" {
		token = strings.TrimSpace(wrapped.Token)
	}
	client, err := plex.New(plex.Config{BaseURL: config.BaseURL, Token: token, AllowHTTP: config.AllowHTTP, Timeout: time.Duration(config.RequestTimeoutMS) * time.Millisecond}, service.Transport)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func plexGUIDs(item catalog.MediaItem) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(item.Identities))
	for _, identity := range item.Identities {
		namespace := strings.ToLower(strings.TrimSpace(identity.Namespace))
		value := strings.TrimSpace(identity.Value)
		if identity.State != catalog.IdentityVerified || value == "" || namespace != "tmdb" && namespace != "tvdb" && namespace != "imdb" {
			continue
		}
		guid := namespace + "://" + value
		if _, exists := seen[guid]; exists {
			continue
		}
		seen[guid] = struct{}{}
		result = append(result, guid)
	}
	return result
}
