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
	rootPaths, err := executionRoots(current)
	if err != nil {
		return service.fail(current, nil, executionFailure{phase: "preflight"})
	}
	operations := current.Operations
	if len(operations) == 0 {
		planned, planErr := buildOperations(ctx, current, rootPaths)
		if planErr != nil {
			return service.fail(current, nil, executionFailure{phase: "preflight"})
		}
		operations, err = service.Store.PrepareAdoptionOperations(ctx, current.Batch.ID, planned)
		if err != nil {
			return err
		}
	}
	if err := applyOperations(ctx, service.Store, rootPaths, operations); err != nil {
		latest, _ := service.Store.ListAdoptionOperations(ctx, current.Batch.ID)
		rollbackErr := rollbackOperations(ctx, service.Store, rootPaths, latest)
		phase := "filesystem operation"
		if rollbackErr != nil {
			phase = "filesystem operation with incomplete rollback"
		}
		return service.fail(current, latest, executionFailure{phase: phase})
	}
	if service.MediaInfoProbe == nil {
		latest, _ := service.Store.ListAdoptionOperations(ctx, current.Batch.ID)
		_ = rollbackOperations(ctx, service.Store, rootPaths, latest)
		return service.fail(current, latest, executionFailure{phase: "media-info configuration"})
	}
	if err := captureMediaInfo(ctx, service.Store, service.MediaInfoProbe, rootPaths, operations); err != nil {
		latest, _ := service.Store.ListAdoptionOperations(ctx, current.Batch.ID)
		rollbackErr := rollbackOperations(ctx, service.Store, rootPaths, latest)
		phase := "media-info inspection"
		if rollbackErr != nil {
			phase += " with incomplete rollback"
		}
		return service.fail(current, latest, executionFailure{phase: phase})
	}
	if service.PlexClientFor == nil {
		latest, _ := service.Store.ListAdoptionOperations(ctx, current.Batch.ID)
		_ = rollbackOperations(ctx, service.Store, rootPaths, latest)
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
			_ = rollbackOperations(ctx, service.Store, rootPaths, latest)
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
	rootPaths, rootErr := executionRoots(current)
	rollbackErr := rootErr
	if rootErr == nil {
		rollbackErr = rollbackOperations(ctx, service.Store, rootPaths, current.Operations)
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

func buildOperations(ctx context.Context, current postgres.AdoptionExecutionContext, rootPaths map[uuid.UUID]string) ([]postgres.AdoptionOperation, error) {
	sourceRootPath, ok := rootPaths[current.Batch.DefaultRootID]
	if !ok {
		return nil, errors.New("adoption source root is unavailable")
	}
	sourcePrefix, err := filepath.Rel(sourceRootPath, filepath.Clean(current.Batch.SourceRoot))
	if err != nil || sourcePrefix == ".." || strings.HasPrefix(sourcePrefix, ".."+string(filepath.Separator)) {
		return nil, errors.New("adoption source is outside its configured root")
	}
	operations := []postgres.AdoptionOperation{}
	destinations := map[string]bool{}
	sources := map[string]bool{}
	ordinal := 0
	for _, entry := range current.Items {
		if _, ok := rootPaths[entry.Root.ID]; !ok {
			return nil, errors.New("adoption destination root is unavailable")
		}
		original, err := library.ValidateRelative(filepath.Join(sourcePrefix, filepath.FromSlash(entry.Item.OriginalRelative)))
		if err != nil {
			return nil, err
		}
		proposed, err := library.ValidateRelative(filepath.FromSlash(entry.Item.ProposedRelative))
		if err != nil {
			return nil, err
		}
		destinationKey := entry.Root.ID.String() + "\x00" + proposed
		sourceKey := current.Batch.DefaultRootID.String() + "\x00" + original
		if destinations[destinationKey] || sources[sourceKey] {
			return nil, errors.New("adoption source or destination is duplicated")
		}
		destinations[destinationKey] = true
		sources[sourceKey] = true
		ordinal++
		operation, err := inspectedOperation(ctx, sourceRootPath, current.Batch.ID, entry.Item.ID, ordinal, "primary", current.Batch.DefaultRootID, entry.Root.ID, original, proposed)
		if err != nil {
			return nil, err
		}
		operations = append(operations, operation)
		var recognized, unknown []string
		if err := json.Unmarshal(entry.Item.RecognizedCompanions, &recognized); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(entry.Item.UnknownCompanions, &unknown); err != nil {
			return nil, err
		}
		oldBase := strings.TrimSuffix(filepath.Base(original), filepath.Ext(original))
		newBase := strings.TrimSuffix(filepath.Base(proposed), filepath.Ext(proposed))
		for _, companion := range recognized {
			source, err := library.ValidateRelative(filepath.Join(sourcePrefix, filepath.FromSlash(companion)))
			if err != nil {
				return nil, err
			}
			name := filepath.Base(source)
			if !strings.HasPrefix(name, oldBase+".") {
				return nil, errors.New("recognized companion no longer matches its primary file")
			}
			destination, err := library.ValidateRelative(filepath.Join(filepath.Dir(proposed), newBase+strings.TrimPrefix(name, oldBase)))
			destinationKey := entry.Root.ID.String() + "\x00" + destination
			sourceKey := current.Batch.DefaultRootID.String() + "\x00" + source
			if err != nil || destinations[destinationKey] || sources[sourceKey] {
				return nil, errors.New("recognized companion destination is invalid")
			}
			destinations[destinationKey] = true
			sources[sourceKey] = true
			ordinal++
			operation, err := inspectedOperation(ctx, sourceRootPath, current.Batch.ID, entry.Item.ID, ordinal, "recognized_companion", current.Batch.DefaultRootID, entry.Root.ID, source, destination)
			if err != nil {
				return nil, err
			}
			operations = append(operations, operation)
		}
		for _, companion := range unknown {
			source, err := library.ValidateRelative(filepath.Join(sourcePrefix, filepath.FromSlash(companion)))
			if err != nil {
				return nil, err
			}
			sourceKey := current.Batch.DefaultRootID.String() + "\x00" + source
			if sources[sourceKey] {
				return nil, errors.New("adoption source is duplicated")
			}
			sources[sourceKey] = true
			ordinal++
			operation, err := inspectedOperation(ctx, sourceRootPath, current.Batch.ID, entry.Item.ID, ordinal, "unknown_preserved", current.Batch.DefaultRootID, current.Batch.DefaultRootID, source, source)
			if err != nil {
				return nil, err
			}
			operations = append(operations, operation)
		}
	}
	if len(operations) > 500 {
		return nil, errors.New("adoption operation plan exceeds 500 paths")
	}
	for _, operation := range operations {
		if operation.TransferMode == "preserve" {
			continue
		}
		destinationRoot, err := os.OpenRoot(rootPaths[operation.DestinationRootID])
		if err != nil {
			return nil, err
		}
		_, statErr := destinationRoot.Lstat(operation.DestinationRelative)
		_ = destinationRoot.Close()
		if statErr == nil {
			return nil, errors.New("adoption destination already exists")
		} else if !errors.Is(statErr, fs.ErrNotExist) {
			return nil, errors.New("adoption destination cannot be inspected safely")
		}
	}
	return operations, nil
}

func executionRoots(current postgres.AdoptionExecutionContext) (map[uuid.UUID]string, error) {
	if len(current.Items) == 0 {
		return nil, errors.New("adoption execution has no items")
	}
	if current.SourceRoot.ID != current.Batch.DefaultRootID {
		return nil, errors.New("adoption source root changed")
	}
	result := map[uuid.UUID]string{}
	add := func(root postgres.LibraryRoot) error {
		rootPath := filepath.Clean(root.DestinationRoot)
		if root.ID == (uuid.UUID{}) || !filepath.IsAbs(rootPath) || rootPath != root.DestinationRoot {
			return errors.New("adoption root is not absolute and clean")
		}
		if existing, ok := result[root.ID]; ok && existing != rootPath {
			return errors.New("adoption root changed during execution")
		}
		result[root.ID] = rootPath
		return nil
	}
	if err := add(current.SourceRoot); err != nil {
		return nil, err
	}
	for _, item := range current.Items {
		if err := add(item.Root); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func inspectedOperation(ctx context.Context, sourceRootPath string, batchID, itemID uuid.UUID, ordinal int, kind string, sourceRootID, destinationRootID uuid.UUID, source, destination string) (postgres.AdoptionOperation, error) {
	size, modTimeNS, digest, err := library.InspectRegularFile(ctx, sourceRootPath, source)
	if err != nil {
		return postgres.AdoptionOperation{}, errors.New("adoption source is not a regular file")
	}
	mode := "copy_remove"
	if kind == "unknown_preserved" || sourceRootID == destinationRootID && source == destination {
		mode = "preserve"
	} else if sourceRootID == destinationRootID {
		mode = "rename"
	}
	return postgres.AdoptionOperation{ID: uuid.NewV7(), BatchID: batchID, ItemID: itemID, Ordinal: ordinal, Kind: kind, SourceRootID: sourceRootID, DestinationRootID: destinationRootID, TransferMode: mode, SourceRelative: source, DestinationRelative: destination, SourceSize: size, SourceModTimeNS: modTimeNS, SourceSHA256: digest, State: "planned"}, nil
}

type operationStore interface {
	MarkAdoptionOperation(context.Context, uuid.UUID, string, string, time.Time) error
}

type mediaInfoStore interface {
	RecordAdoptionMediaInfo(context.Context, uuid.UUID, json.RawMessage, time.Time) error
}

func captureMediaInfo(ctx context.Context, store mediaInfoStore, probe MediaInfoProbe, rootPaths map[uuid.UUID]string, operations []postgres.AdoptionOperation) error {
	for _, operation := range operations {
		if operation.Kind != "primary" || len(operation.MediaInfo) != 0 {
			continue
		}
		rootPath, ok := rootPaths[operation.DestinationRootID]
		if !ok {
			return errors.New("adoption destination root is unavailable")
		}
		root, err := os.OpenRoot(rootPath)
		if err != nil {
			return err
		}
		if err := matchesFingerprint(root, operation.DestinationRelative, operation); err != nil {
			_ = root.Close()
			return err
		}
		evidence, err := probe.Inspect(ctx, filepath.Join(rootPath, operation.DestinationRelative))
		if err != nil {
			_ = root.Close()
			return err
		}
		if err := matchesFingerprint(root, operation.DestinationRelative, operation); err != nil {
			_ = root.Close()
			return err
		}
		_ = root.Close()
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

func applyOperations(ctx context.Context, store operationStore, rootPaths map[uuid.UUID]string, operations []postgres.AdoptionOperation) error {
	for index := range operations {
		operation := &operations[index]
		sourcePath, sourceOK := rootPaths[operation.SourceRootID]
		destinationPath, destinationOK := rootPaths[operation.DestinationRootID]
		if !sourceOK || !destinationOK {
			return errors.New("adoption operation root is unavailable")
		}
		sourceRoot, err := os.OpenRoot(sourcePath)
		if err != nil {
			return err
		}
		destinationRoot, err := os.OpenRoot(destinationPath)
		if err != nil {
			_ = sourceRoot.Close()
			return err
		}
		if operation.Kind == "unknown_preserved" || operation.State == "preserved" {
			if err := matchesFingerprint(sourceRoot, operation.SourceRelative, *operation); err != nil {
				_ = sourceRoot.Close()
				_ = destinationRoot.Close()
				return err
			}
			_ = sourceRoot.Close()
			_ = destinationRoot.Close()
			operation.State = "preserved"
			if err := store.MarkAdoptionOperation(ctx, operation.ID, "preserved", "", time.Now().UTC()); err != nil {
				return err
			}
			continue
		}
		if operation.State == "verified" {
			if err := matchesFingerprint(destinationRoot, operation.DestinationRelative, *operation); err != nil {
				_ = sourceRoot.Close()
				_ = destinationRoot.Close()
				return err
			}
			_ = sourceRoot.Close()
			_ = destinationRoot.Close()
			continue
		}
		sourceErr := matchesFingerprint(sourceRoot, operation.SourceRelative, *operation)
		destinationErr := matchesFingerprint(destinationRoot, operation.DestinationRelative, *operation)
		if operation.TransferMode == "preserve" {
			if sourceErr != nil {
				_ = sourceRoot.Close()
				_ = destinationRoot.Close()
				return sourceErr
			}
		} else if operation.TransferMode == "copy_remove" {
			_ = sourceRoot.Close()
			_ = destinationRoot.Close()
			if err := library.TransferRegularFile(ctx, sourcePath, operation.SourceRelative, destinationPath, operation.DestinationRelative, transferTemporary(*operation), operation.SourceSize, operation.SourceModTimeNS, operation.SourceSHA256); err != nil {
				return err
			}
		} else if operation.TransferMode == "rename" && sourceErr == nil && errors.Is(destinationErr, fs.ErrNotExist) {
			if parent := filepath.Dir(operation.DestinationRelative); parent != "." {
				if err := destinationRoot.MkdirAll(parent, 0o755); err != nil {
					_ = sourceRoot.Close()
					_ = destinationRoot.Close()
					return err
				}
			}
			if err := sourceRoot.Rename(operation.SourceRelative, operation.DestinationRelative); err != nil {
				_ = sourceRoot.Close()
				_ = destinationRoot.Close()
				return err
			}
		} else if operation.TransferMode == "rename" && errors.Is(sourceErr, fs.ErrNotExist) && destinationErr == nil {
			// A previous worker moved the file before its journal state committed.
		} else {
			_ = sourceRoot.Close()
			_ = destinationRoot.Close()
			return errors.New("adoption source or destination changed after review")
		}
		_ = sourceRoot.Close()
		_ = destinationRoot.Close()
		operation.State = "applied"
		if err := store.MarkAdoptionOperation(ctx, operation.ID, "applied", "", time.Now().UTC()); err != nil {
			return err
		}
		verificationRoot, err := os.OpenRoot(destinationPath)
		if err != nil {
			return err
		}
		if err := matchesFingerprint(verificationRoot, operation.DestinationRelative, *operation); err != nil {
			_ = verificationRoot.Close()
			return err
		}
		_ = verificationRoot.Close()
		operation.State = "verified"
		if err := store.MarkAdoptionOperation(ctx, operation.ID, "verified", "", time.Now().UTC()); err != nil {
			return err
		}
	}
	return nil
}

func rollbackOperations(ctx context.Context, store operationStore, rootPaths map[uuid.UUID]string, operations []postgres.AdoptionOperation) error {
	var failures []error
	for index := len(operations) - 1; index >= 0; index-- {
		operation := operations[index]
		if operation.TransferMode == "preserve" || !slices.Contains([]string{"planned", "applied", "verified"}, operation.State) {
			continue
		}
		sourcePath, sourceOK := rootPaths[operation.SourceRootID]
		destinationPath, destinationOK := rootPaths[operation.DestinationRootID]
		if !sourceOK || !destinationOK {
			failures = append(failures, errors.New("rollback root is unavailable"))
			continue
		}
		if operation.TransferMode == "copy_remove" {
			if err := library.TransferRegularFile(ctx, destinationPath, operation.DestinationRelative, sourcePath, operation.SourceRelative, rollbackTemporary(operation), operation.SourceSize, operation.SourceModTimeNS, operation.SourceSHA256); err != nil {
				_ = store.MarkAdoptionOperation(ctx, operation.ID, "failed", "rollback transfer failed", time.Now().UTC())
				failures = append(failures, err)
				continue
			}
			if err := store.MarkAdoptionOperation(ctx, operation.ID, "rolled_back", "", time.Now().UTC()); err != nil {
				failures = append(failures, err)
			}
			continue
		}
		sourceRoot, err := os.OpenRoot(sourcePath)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		destinationRoot, err := os.OpenRoot(destinationPath)
		if err != nil {
			_ = sourceRoot.Close()
			failures = append(failures, err)
			continue
		}
		destinationErr := matchesFingerprint(destinationRoot, operation.DestinationRelative, operation)
		sourceFingerprintErr := matchesFingerprint(sourceRoot, operation.SourceRelative, operation)
		if sourceFingerprintErr == nil && errors.Is(destinationErr, fs.ErrNotExist) {
			_ = sourceRoot.Close()
			_ = destinationRoot.Close()
			if operation.State != "planned" {
				_ = store.MarkAdoptionOperation(ctx, operation.ID, "rolled_back", "", time.Now().UTC())
			}
			continue
		}
		if destinationErr != nil || !errors.Is(sourceFingerprintErr, fs.ErrNotExist) {
			_ = sourceRoot.Close()
			_ = destinationRoot.Close()
			_ = store.MarkAdoptionOperation(ctx, operation.ID, "failed", "rollback precondition failed", time.Now().UTC())
			failures = append(failures, errors.New("rollback precondition failed"))
			continue
		}
		_ = sourceRoot.Close()
		_ = destinationRoot.Close()
		root, openErr := os.OpenRoot(sourcePath)
		if openErr != nil {
			err = openErr
		} else {
			if parent := filepath.Dir(operation.SourceRelative); parent != "." {
				err = root.MkdirAll(parent, 0o755)
			}
			if err == nil {
				err = root.Rename(operation.DestinationRelative, operation.SourceRelative)
			}
			_ = root.Close()
		}
		if err != nil {
			_ = store.MarkAdoptionOperation(ctx, operation.ID, "failed", "rollback transfer failed", time.Now().UTC())
			failures = append(failures, err)
			continue
		}
		if err := store.MarkAdoptionOperation(ctx, operation.ID, "rolled_back", "", time.Now().UTC()); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func transferTemporary(operation postgres.AdoptionOperation) string {
	return filepath.Join(filepath.Dir(operation.DestinationRelative), "."+filepath.Base(operation.DestinationRelative)+".media-manager-"+operation.ID.String()+".partial")
}

func rollbackTemporary(operation postgres.AdoptionOperation) string {
	return filepath.Join(filepath.Dir(operation.SourceRelative), "."+filepath.Base(operation.SourceRelative)+".media-manager-"+operation.ID.String()+".rollback-partial")
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
