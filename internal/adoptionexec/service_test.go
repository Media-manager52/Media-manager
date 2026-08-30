package adoptionexec

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/mediainfo"
	"github.com/media-manager/media-manager/internal/postgres"
)

type memoryOperationStore struct{ states map[uuid.UUID]string }

func (store *memoryOperationStore) MarkAdoptionOperation(_ context.Context, id uuid.UUID, state, _ string, _ time.Time) error {
	store.states[id] = state
	return nil
}

type memoryMediaInfoStore struct{ evidence map[uuid.UUID]json.RawMessage }

func (store *memoryMediaInfoStore) RecordAdoptionMediaInfo(_ context.Context, id uuid.UUID, evidence json.RawMessage, _ time.Time) error {
	store.evidence[id] = append(json.RawMessage(nil), evidence...)
	return nil
}

type staticMediaInfoProbe struct {
	evidence mediainfo.Evidence
	paths    []string
}

func (probe *staticMediaInfoProbe) Inspect(_ context.Context, path string) (mediainfo.Evidence, error) {
	probe.paths = append(probe.paths, path)
	return probe.evidence, nil
}

func TestBuildApplyAndRollbackAdoptionOperations(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(rootPath, "Existing"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"Old.mkv": "video", "Old.en.srt": "subtitle", "Old.notes.txt": "preserve"} {
		if err := os.WriteFile(filepath.Join(rootPath, "Existing", name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	recognized, _ := json.Marshal([]string{"Old.en.srt"})
	unknown, _ := json.Marshal([]string{"Old.notes.txt"})
	rootID, batchID, itemID, mediaID := uuid.NewV7(), uuid.NewV7(), uuid.NewV7(), uuid.NewV7()
	current := postgres.AdoptionExecutionContext{
		Batch:      postgres.AdoptionBatch{ID: batchID, SourceRoot: filepath.Join(rootPath, "Existing"), DefaultRootID: rootID},
		SourceRoot: postgres.LibraryRoot{ID: rootID, DestinationRoot: rootPath},
		Items: []postgres.AdoptionExecutionItem{{
			Item:  postgres.AdoptionItem{ID: itemID, OriginalRelative: "Old.mkv", ProposedRelative: "Renamed/New.mkv", RecognizedCompanions: recognized, UnknownCompanions: unknown},
			Root:  postgres.LibraryRoot{ID: rootID, DestinationRoot: rootPath},
			Media: catalog.MediaItem{ID: mediaID, Kind: catalog.KindMovie},
		}},
	}
	rootPaths, err := executionRoots(current)
	if err != nil {
		t.Fatal(err)
	}
	operations, err := buildOperations(context.Background(), current, rootPaths)
	if err != nil {
		t.Fatal(err)
	}
	if len(operations) != 3 || operations[0].DestinationRelative != "Renamed/New.mkv" || operations[1].DestinationRelative != "Renamed/New.en.srt" || operations[2].Kind != "unknown_preserved" {
		t.Fatalf("operations=%+v", operations)
	}
	for index := range operations {
		operations[index].ID = uuid.NewV7()
	}
	store := &memoryOperationStore{states: map[uuid.UUID]string{}}
	if err := applyOperations(context.Background(), store, rootPaths, operations); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(rootPath, "Renamed", "New.mkv")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(rootPath, "Renamed", "New.en.srt")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(rootPath, "Existing", "Old.notes.txt")); err != nil {
		t.Fatal(err)
	}
	for index := range operations {
		operations[index].State = store.states[operations[index].ID]
	}
	operations[0].State = "planned" // Simulate a crash after rename but before journal update.
	if err := rollbackOperations(context.Background(), store, rootPaths, operations); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(rootPath, "Existing", "Old.mkv")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(rootPath, "Existing", "Old.en.srt")); err != nil {
		t.Fatal(err)
	}
}

func TestBuildOperationsRejectsDestinationCollision(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "Old.mkv"), []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, "New.mkv"), []byte("collision"), 0o644); err != nil {
		t.Fatal(err)
	}
	rootID := uuid.NewV7()
	current := postgres.AdoptionExecutionContext{Batch: postgres.AdoptionBatch{ID: uuid.NewV7(), SourceRoot: rootPath, DefaultRootID: rootID}, SourceRoot: postgres.LibraryRoot{ID: rootID, DestinationRoot: rootPath}, Items: []postgres.AdoptionExecutionItem{{Item: postgres.AdoptionItem{ID: uuid.NewV7(), OriginalRelative: "Old.mkv", ProposedRelative: "New.mkv", RecognizedCompanions: json.RawMessage(`[]`), UnknownCompanions: json.RawMessage(`[]`)}, Root: postgres.LibraryRoot{ID: rootID, DestinationRoot: rootPath}}}}
	rootPaths, err := executionRoots(current)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := buildOperations(context.Background(), current, rootPaths); err == nil {
		t.Fatal("destination collision was accepted")
	}
}

func TestCrossRootAdoptionTransfersAndRollsBackVerifiedFiles(t *testing.T) {
	t.Parallel()
	temp := t.TempDir()
	sourcePath, destinationPath := filepath.Join(temp, "source-root"), filepath.Join(temp, "destination-root")
	if err := os.MkdirAll(filepath.Join(sourcePath, "incoming"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destinationPath, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"Old.mkv": "synthetic video", "Old.en.srt": "synthetic subtitle", "Old.notes.txt": "preserved note"} {
		if err := os.WriteFile(filepath.Join(sourcePath, "incoming", name), []byte(body), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	recognized, _ := json.Marshal([]string{"Old.en.srt"})
	unknown, _ := json.Marshal([]string{"Old.notes.txt"})
	sourceRootID, destinationRootID := uuid.NewV7(), uuid.NewV7()
	current := postgres.AdoptionExecutionContext{
		Batch:      postgres.AdoptionBatch{ID: uuid.NewV7(), SourceRoot: filepath.Join(sourcePath, "incoming"), DefaultRootID: sourceRootID},
		SourceRoot: postgres.LibraryRoot{ID: sourceRootID, DestinationRoot: sourcePath},
		Items: []postgres.AdoptionExecutionItem{{
			Item: postgres.AdoptionItem{ID: uuid.NewV7(), OriginalRelative: "Old.mkv", ProposedRelative: "Movie (2026)/Movie (2026).mkv", RecognizedCompanions: recognized, UnknownCompanions: unknown},
			Root: postgres.LibraryRoot{ID: destinationRootID, DestinationRoot: destinationPath},
		}},
	}
	rootPaths, err := executionRoots(current)
	if err != nil {
		t.Fatal(err)
	}
	operations, err := buildOperations(context.Background(), current, rootPaths)
	if err != nil {
		t.Fatal(err)
	}
	if len(operations) != 3 || operations[0].TransferMode != "copy_remove" || operations[1].TransferMode != "copy_remove" || operations[2].TransferMode != "preserve" || len(operations[0].SourceSHA256) != 64 {
		t.Fatalf("unexpected cross-root plan: %+v", operations)
	}
	store := &memoryOperationStore{states: map[uuid.UUID]string{}}
	if err := applyOperations(context.Background(), store, rootPaths, operations); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destinationPath, "Movie (2026)", "Movie (2026).mkv")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(sourcePath, "incoming", "Old.mkv")); !os.IsNotExist(err) {
		t.Fatalf("cross-root source was not removed after verification: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sourcePath, "incoming", "Old.notes.txt")); err != nil {
		t.Fatal(err)
	}
	for index := range operations {
		operations[index].State = store.states[operations[index].ID]
	}
	operations[0].State = "planned" // Unknown outcome after transfer, before the journal write.
	if err := rollbackOperations(context.Background(), store, rootPaths, operations); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Old.mkv", "Old.en.srt", "Old.notes.txt"} {
		if _, err := os.Stat(filepath.Join(sourcePath, "incoming", name)); err != nil {
			t.Fatalf("source %s was not restored: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(destinationPath, "Movie (2026)", "Movie (2026).mkv")); !os.IsNotExist(err) {
		t.Fatalf("rolled-back destination still exists: %v", err)
	}
}

func TestCaptureMediaInfoPersistsVerifiedPrimaryEvidenceOnce(t *testing.T) {
	rootPath := t.TempDir()
	path := filepath.Join(rootPath, "Current.mkv")
	if err := os.WriteFile(path, []byte("synthetic video fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	rootID := uuid.NewV7()
	operation := postgres.AdoptionOperation{ID: uuid.NewV7(), Kind: "primary", DestinationRootID: rootID, DestinationRelative: "Current.mkv", SourceSize: info.Size(), SourceModTimeNS: info.ModTime().UnixNano(), State: "verified"}
	probe := &staticMediaInfoProbe{evidence: mediainfo.Evidence{SchemaVersion: 1, ContainerNames: []string{"matroska"}, VideoTracks: []mediainfo.Track{{Index: 0, Codec: "hevc", Width: 3840, Height: 2160}}, AudioTracks: []mediainfo.Track{}, SubtitleTracks: []mediainfo.Track{}, Facts: map[string]any{"resolution": "2160p"}}}
	store := &memoryMediaInfoStore{evidence: map[uuid.UUID]json.RawMessage{}}
	if err := captureMediaInfo(context.Background(), store, probe, map[uuid.UUID]string{rootID: rootPath}, []postgres.AdoptionOperation{operation}); err != nil {
		t.Fatal(err)
	}
	if len(probe.paths) != 1 || probe.paths[0] != path || !strings.Contains(string(store.evidence[operation.ID]), `"resolution":"2160p"`) {
		t.Fatalf("paths=%v evidence=%s", probe.paths, store.evidence[operation.ID])
	}
	operation.MediaInfo = store.evidence[operation.ID]
	if err := captureMediaInfo(context.Background(), store, probe, map[uuid.UUID]string{rootID: rootPath}, []postgres.AdoptionOperation{operation}); err != nil {
		t.Fatal(err)
	}
	if len(probe.paths) != 1 {
		t.Fatalf("persisted media info was probed again: %v", probe.paths)
	}
}

func TestVerifiedOperationRechecksFingerprint(t *testing.T) {
	rootPath := t.TempDir()
	path := filepath.Join(rootPath, "Current.mkv")
	if err := os.WriteFile(path, []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	rootID := uuid.NewV7()
	operation := postgres.AdoptionOperation{ID: uuid.NewV7(), Kind: "primary", SourceRootID: rootID, DestinationRootID: rootID, TransferMode: "preserve", DestinationRelative: "Current.mkv", SourceRelative: "Current.mkv", SourceSize: 999, SourceModTimeNS: 1, State: "verified"}
	if err := applyOperations(context.Background(), &memoryOperationStore{states: map[uuid.UUID]string{}}, map[uuid.UUID]string{rootID: rootPath}, []postgres.AdoptionOperation{operation}); err == nil {
		t.Fatal("changed verified file fingerprint was accepted")
	}
}
