package adoptionexec

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"uuid"

	"github.com/media-manager/media-manager/internal/adapters/plex"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/library"
	"github.com/media-manager/media-manager/internal/mediainfo"
	"github.com/media-manager/media-manager/internal/postgres"
)

type PlexClient interface {
	Refresh(context.Context, string, string) error
	Confirm(context.Context, plex.Expected) (plex.Confirmation, error)
	ConfirmSeries(context.Context, plex.ExpectedSeries) ([]plex.Confirmation, error)
}

type PlexClientFactory func(postgres.Integration) (PlexClient, error)

type MediaInfoProbe interface {
	Inspect(context.Context, string) (mediainfo.Evidence, error)
}

type Service struct {
	Store          *postgres.Store
	PlexClientFor  PlexClientFactory
	MediaInfoProbe MediaInfoProbe
}

type executionFailure struct{ phase string }

func (failure executionFailure) Error() string { return "adoption " + failure.phase + " failed" }

func (service *Service) HandleExecute(ctx context.Context, event postgres.OutboxEvent) error {
	current, err := service.Store.AdoptionExecutionContext(ctx, event.AggregateID)
	if errors.Is(err, postgres.ErrConcurrentModification) || errors.Is(err, postgres.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	rootPath, err := executionRoot(current)
	if err != nil {
		return service.fail(current, nil, executionFailure{phase: "preflight"})
	}
	operations := current.Operations
	if len(operations) == 0 {
		planned, _, planErr := buildOperations(current)
		if planErr != nil {
			return service.fail(current, nil, executionFailure{phase: "preflight"})
		}
		operations, err = service.Store.PrepareAdoptionOperations(ctx, current.Batch.ID, planned)
		if err != nil {
			return err
		}
	}
	if err := applyOperations(ctx, service.Store, rootPath, operations); err != nil {
		latest, _ := service.Store.ListAdoptionOperations(ctx, current.Batch.ID)
		rollbackErr := rollbackOperations(ctx, service.Store, rootPath, latest)
		phase := "filesystem operation"
		if rollbackErr != nil {
			phase = "filesystem operation with incomplete rollback"
		}
		return service.fail(current, latest, executionFailure{phase: phase})
	}
	if service.MediaInfoProbe == nil {
		latest, _ := service.Store.ListAdoptionOperations(ctx, current.Batch.ID)
		_ = rollbackOperations(ctx, service.Store, rootPath, latest)
		return service.fail(current, latest, executionFailure{phase: "media-info configuration"})
	}
	if err := captureMediaInfo(ctx, service.Store, service.MediaInfoProbe, rootPath, operations); err != nil {
		latest, _ := service.Store.ListAdoptionOperations(ctx, current.Batch.ID)
		rollbackErr := rollbackOperations(ctx, service.Store, rootPath, latest)
		phase := "media-info inspection"
		if rollbackErr != nil {
			phase += " with incomplete rollback"
		}
		return service.fail(current, latest, executionFailure{phase: phase})
	}
	if service.PlexClientFor == nil {
		latest, _ := service.Store.ListAdoptionOperations(ctx, current.Batch.ID)
		_ = rollbackOperations(ctx, service.Store, rootPath, latest)
		return service.fail(current, latest, executionFailure{phase: "Plex client configuration"})
	}
	refreshed := map[string]bool{}
	for _, item := range current.Items {
		key := item.Plex.ID.String() + "\x00" + item.Root.PlexLibraryID
		if refreshed[key] {
			continue
		}
		client, err := service.PlexClientFor(item.Plex)
		if err != nil || client.Refresh(ctx, item.Root.PlexLibraryID, item.Root.PlexVisibleRoot) != nil {
			latest, _ := service.Store.ListAdoptionOperations(ctx, current.Batch.ID)
			_ = rollbackOperations(ctx, service.Store, rootPath, latest)
			return service.fail(current, latest, executionFailure{phase: "Plex refresh"})
		}
		refreshed[key] = true
	}
	return service.Store.QueueAdoptionPlexConfirmation(ctx, current.Batch.ID, time.Now().UTC())
}

func (service *Service) HandlePlexConfirm(ctx context.Context, event postgres.OutboxEvent) error {
	current, err := service.Store.AdoptionExecutionContext(ctx, event.AggregateID)
	if errors.Is(err, postgres.ErrConcurrentModification) || errors.Is(err, postgres.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if current.Batch.ExecutionDeadline == nil || !time.Now().UTC().Before(*current.Batch.ExecutionDeadline) {
		return service.rollbackExpired(ctx, current)
	}
	if service.PlexClientFor == nil {
		return executionFailure{phase: "Plex client configuration"}
	}
	for _, item := range current.Items {
		if current.ConfirmedItems[item.Item.ID] {
			continue
		}
		client, err := service.PlexClientFor(item.Plex)
		if err != nil {
			return err
		}
		guids := plexGUIDs(item.Media)
		if len(guids) == 0 {
			return executionFailure{phase: "Plex identity verification"}
		}
		visiblePath := filepath.Join(item.Root.PlexVisibleRoot, item.Item.ProposedRelative)
		if item.Media.Kind == catalog.KindMovie {
			confirmation, err := client.Confirm(ctx, plex.Expected{LibraryKey: item.Root.PlexLibraryID, Title: item.Media.Title, PlexVisiblePath: visiblePath, ProviderGUIDs: guids})
			if err != nil {
				return err
			}
			persisted := postgres.AdoptionPlexConfirmation{ItemID: item.Item.ID, ObservedPath: filepath.Clean(confirmation.Path), RatingKey: confirmation.RatingKey, ProviderGUID: confirmation.ProviderGUID, ConfirmedAt: confirmation.ConfirmedAt}
			if err := service.Store.RecordAdoptionPlexConfirmation(ctx, current.Batch.ID, persisted); err != nil {
				return err
			}
			continue
		}
		seriesConfirmations, err := client.ConfirmSeries(ctx, plex.ExpectedSeries{LibraryKey: item.Root.PlexLibraryID, Title: item.Media.Title, PlexVisiblePaths: []string{visiblePath}, ExpectedEpisodes: map[string][]plex.EpisodeLeaf{}, ProviderGUIDs: guids})
		if err != nil {
			return err
		}
		if len(seriesConfirmations) != 1 {
			return plex.ErrNotConfirmed
		}
		confirmation := seriesConfirmations[0]
		persisted := postgres.AdoptionPlexConfirmation{ItemID: item.Item.ID, ObservedPath: filepath.Clean(confirmation.Path), RatingKey: confirmation.RatingKey, ProviderGUID: confirmation.ProviderGUID, ConfirmedAt: confirmation.ConfirmedAt}
		if err := service.Store.RecordAdoptionPlexConfirmation(ctx, current.Batch.ID, persisted); err != nil {
			return err
		}
	}
	return service.Store.CompleteAdoptionExecution(ctx, current.Batch.ID, nil, time.Now().UTC())
}

func (service *Service) rollbackExpired(ctx context.Context, current postgres.AdoptionExecutionContext) error {
	rootPath, rootErr := executionRoot(current)
	rollbackErr := rootErr
	if rootErr == nil {
		rollbackErr = rollbackOperations(ctx, service.Store, rootPath, current.Operations)
	}
	for _, item := range current.Items {
		if client, err := service.PlexClientFor(item.Plex); err == nil {
			_ = client.Refresh(ctx, item.Root.PlexLibraryID, item.Root.PlexVisibleRoot)
		}
	}
	phase := "Plex confirmation deadline"
	if rollbackErr != nil {
		phase += " with incomplete rollback"
	}
	return service.fail(current, current.Operations, executionFailure{phase: phase})
}

func (service *Service) fail(current postgres.AdoptionExecutionContext, _ []postgres.AdoptionOperation, failure error) error {
	detail := "adoption execution failed"
	var scoped executionFailure
	if errors.As(failure, &scoped) {
		detail = scoped.phase + " failed"
	}
	failContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := service.Store.FailAdoptionExecution(failContext, current.Batch.ID, detail, time.Now().UTC()); err != nil {
		return err
	}
	return nil
}

func buildOperations(current postgres.AdoptionExecutionContext) ([]postgres.AdoptionOperation, string, error) {
	rootPath, err := executionRoot(current)
	if err != nil {
		return nil, "", err
	}
	sourcePrefix, err := filepath.Rel(rootPath, filepath.Clean(current.Batch.SourceRoot))
	if err != nil || sourcePrefix == ".." || strings.HasPrefix(sourcePrefix, ".."+string(filepath.Separator)) {
		return nil, "", errors.New("adoption source is outside its destination root")
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, "", err
	}
	defer root.Close()
	operations := []postgres.AdoptionOperation{}
	destinations := map[string]bool{}
	ordinal := 0
	for _, entry := range current.Items {
		if filepath.Clean(entry.Root.DestinationRoot) != rootPath || entry.Root.ID != current.Batch.DefaultRootID {
			return nil, "", errors.New("cross-root adoption execution is not supported")
		}
		original, err := library.ValidateRelative(filepath.Join(sourcePrefix, filepath.FromSlash(entry.Item.OriginalRelative)))
		if err != nil {
			return nil, "", err
		}
		proposed, err := library.ValidateRelative(filepath.FromSlash(entry.Item.ProposedRelative))
		if err != nil {
			return nil, "", err
		}
		ordinal++
		operation, err := inspectedOperation(root, current.Batch.ID, entry.Item.ID, ordinal, "primary", original, proposed)
		if err != nil {
			return nil, "", err
		}
		if destinations[proposed] {
			return nil, "", errors.New("adoption destination is duplicated")
		}
		destinations[proposed] = true
		operations = append(operations, operation)
		var recognized, unknown []string
		if err := json.Unmarshal(entry.Item.RecognizedCompanions, &recognized); err != nil {
			return nil, "", err
		}
		if err := json.Unmarshal(entry.Item.UnknownCompanions, &unknown); err != nil {
			return nil, "", err
		}
		oldBase := strings.TrimSuffix(filepath.Base(original), filepath.Ext(original))
		newBase := strings.TrimSuffix(filepath.Base(proposed), filepath.Ext(proposed))
		for _, companion := range recognized {
			source, err := library.ValidateRelative(filepath.Join(sourcePrefix, filepath.FromSlash(companion)))
			if err != nil {
				return nil, "", err
			}
			name := filepath.Base(source)
			if !strings.HasPrefix(name, oldBase+".") {
				return nil, "", errors.New("recognized companion no longer matches its primary file")
			}
			destination, err := library.ValidateRelative(filepath.Join(filepath.Dir(proposed), newBase+strings.TrimPrefix(name, oldBase)))
			if err != nil || destinations[destination] {
				return nil, "", errors.New("recognized companion destination is invalid")
			}
			destinations[destination] = true
			ordinal++
			operation, err := inspectedOperation(root, current.Batch.ID, entry.Item.ID, ordinal, "recognized_companion", source, destination)
			if err != nil {
				return nil, "", err
			}
			operations = append(operations, operation)
		}
		for _, companion := range unknown {
			source, err := library.ValidateRelative(filepath.Join(sourcePrefix, filepath.FromSlash(companion)))
			if err != nil {
				return nil, "", err
			}
			ordinal++
			operation, err := inspectedOperation(root, current.Batch.ID, entry.Item.ID, ordinal, "unknown_preserved", source, source)
			if err != nil {
				return nil, "", err
			}
			operations = append(operations, operation)
		}
	}
	if len(operations) > 500 {
		return nil, "", errors.New("adoption operation plan exceeds 500 paths")
	}
	for _, operation := range operations {
		if operation.Kind == "unknown_preserved" || operation.SourceRelative == operation.DestinationRelative {
			continue
		}
		if _, err := root.Lstat(operation.DestinationRelative); err == nil {
			return nil, "", errors.New("adoption destination already exists")
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, "", errors.New("adoption destination cannot be inspected safely")
		}
	}
	return operations, rootPath, nil
}

func executionRoot(current postgres.AdoptionExecutionContext) (string, error) {
	if len(current.Items) == 0 {
		return "", errors.New("adoption execution has no items")
	}
	rootPath := filepath.Clean(current.Items[0].Root.DestinationRoot)
	if !filepath.IsAbs(rootPath) {
		return "", errors.New("adoption root is not absolute")
	}
	for _, item := range current.Items {
		if filepath.Clean(item.Root.DestinationRoot) != rootPath || item.Root.ID != current.Batch.DefaultRootID {
			return "", errors.New("cross-root adoption execution is not supported")
		}
	}
	return rootPath, nil
}

func inspectedOperation(root *os.Root, batchID, itemID uuid.UUID, ordinal int, kind, source, destination string) (postgres.AdoptionOperation, error) {
	info, err := root.Lstat(source)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return postgres.AdoptionOperation{}, errors.New("adoption source is not a regular file")
	}
	return postgres.AdoptionOperation{BatchID: batchID, ItemID: itemID, Ordinal: ordinal, Kind: kind, SourceRelative: source, DestinationRelative: destination, SourceSize: info.Size(), SourceModTimeNS: info.ModTime().UnixNano(), State: "planned"}, nil
}

type operationStore interface {
	MarkAdoptionOperation(context.Context, uuid.UUID, string, string, time.Time) error
}

type mediaInfoStore interface {
	RecordAdoptionMediaInfo(context.Context, uuid.UUID, json.RawMessage, time.Time) error
}

func captureMediaInfo(ctx context.Context, store mediaInfoStore, probe MediaInfoProbe, rootPath string, operations []postgres.AdoptionOperation) error {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, operation := range operations {
		if operation.Kind != "primary" || len(operation.MediaInfo) != 0 {
			continue
		}
		if err := matchesFingerprint(root, operation.DestinationRelative, operation); err != nil {
			return err
		}
		evidence, err := probe.Inspect(ctx, filepath.Join(rootPath, operation.DestinationRelative))
		if err != nil {
			return err
		}
		if err := matchesFingerprint(root, operation.DestinationRelative, operation); err != nil {
			return err
		}
		body, err := json.Marshal(evidence)
		if err != nil {
			return err
		}
		if err := store.RecordAdoptionMediaInfo(ctx, operation.ID, body, time.Now().UTC()); err != nil {
			return err
		}
	}
	return nil
}

func applyOperations(ctx context.Context, store operationStore, rootPath string, operations []postgres.AdoptionOperation) error {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return err
	}
	defer root.Close()
	for index := range operations {
		operation := &operations[index]
		if operation.Kind == "unknown_preserved" || operation.State == "preserved" {
			if err := matchesFingerprint(root, operation.SourceRelative, *operation); err != nil {
				return err
			}
			operation.State = "preserved"
			if err := store.MarkAdoptionOperation(ctx, operation.ID, "preserved", "", time.Now().UTC()); err != nil {
				return err
			}
			continue
		}
		if operation.State == "verified" {
			if err := matchesFingerprint(root, operation.DestinationRelative, *operation); err != nil {
				return err
			}
			continue
		}
		sourceErr := matchesFingerprint(root, operation.SourceRelative, *operation)
		destinationErr := matchesFingerprint(root, operation.DestinationRelative, *operation)
		if operation.SourceRelative == operation.DestinationRelative {
			if sourceErr != nil {
				return sourceErr
			}
		} else if sourceErr == nil && errors.Is(destinationErr, fs.ErrNotExist) {
			if parent := filepath.Dir(operation.DestinationRelative); parent != "." {
				if err := root.MkdirAll(parent, 0o755); err != nil {
					return err
				}
			}
			if err := root.Rename(operation.SourceRelative, operation.DestinationRelative); err != nil {
				return err
			}
		} else if errors.Is(sourceErr, fs.ErrNotExist) && destinationErr == nil {
			// A previous worker moved the file before its journal state committed.
		} else {
			return errors.New("adoption source or destination changed after review")
		}
		operation.State = "applied"
		if err := store.MarkAdoptionOperation(ctx, operation.ID, "applied", "", time.Now().UTC()); err != nil {
			return err
		}
		if err := matchesFingerprint(root, operation.DestinationRelative, *operation); err != nil {
			return err
		}
		operation.State = "verified"
		if err := store.MarkAdoptionOperation(ctx, operation.ID, "verified", "", time.Now().UTC()); err != nil {
			return err
		}
	}
	return nil
}

func rollbackOperations(ctx context.Context, store operationStore, rootPath string, operations []postgres.AdoptionOperation) error {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return err
	}
	defer root.Close()
	var failures []error
	for index := len(operations) - 1; index >= 0; index-- {
		operation := operations[index]
		if operation.Kind == "unknown_preserved" || operation.SourceRelative == operation.DestinationRelative || !slices.Contains([]string{"planned", "applied", "verified"}, operation.State) {
			continue
		}
		destinationErr := matchesFingerprint(root, operation.DestinationRelative, operation)
		sourceFingerprintErr := matchesFingerprint(root, operation.SourceRelative, operation)
		if sourceFingerprintErr == nil && errors.Is(destinationErr, fs.ErrNotExist) {
			if operation.State != "planned" {
				_ = store.MarkAdoptionOperation(ctx, operation.ID, "rolled_back", "", time.Now().UTC())
			}
			continue
		}
		if destinationErr != nil || !errors.Is(sourceFingerprintErr, fs.ErrNotExist) {
			_ = store.MarkAdoptionOperation(ctx, operation.ID, "failed", "rollback precondition failed", time.Now().UTC())
			failures = append(failures, errors.New("rollback precondition failed"))
			continue
		}
		if parent := filepath.Dir(operation.SourceRelative); parent != "." {
			if err := root.MkdirAll(parent, 0o755); err != nil {
				failures = append(failures, err)
				continue
			}
		}
		if err := root.Rename(operation.DestinationRelative, operation.SourceRelative); err != nil {
			_ = store.MarkAdoptionOperation(ctx, operation.ID, "failed", "rollback rename failed", time.Now().UTC())
			failures = append(failures, err)
			continue
		}
		if err := store.MarkAdoptionOperation(ctx, operation.ID, "rolled_back", "", time.Now().UTC()); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func matchesFingerprint(root *os.Root, path string, operation postgres.AdoptionOperation) error {
	info, err := root.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != operation.SourceSize || info.ModTime().UnixNano() != operation.SourceModTimeNS {
		return errors.New("adoption file fingerprint changed")
	}
	return nil
}

func plexGUIDs(item catalog.MediaItem) []string {
	result := []string{}
	for _, identity := range item.Identities {
		namespace, value := strings.ToLower(strings.TrimSpace(identity.Namespace)), strings.TrimSpace(identity.Value)
		if identity.State == catalog.IdentityVerified && value != "" && slices.Contains([]string{"tmdb", "tvdb", "imdb"}, namespace) {
			result = append(result, namespace+"://"+value)
		}
	}
	return result
}
