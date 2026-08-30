package adoptionexec

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/postgres"
)

type memoryOperationStore struct{ states map[uuid.UUID]string }

func (store *memoryOperationStore) MarkAdoptionOperation(_ context.Context, id uuid.UUID, state, _ string, _ time.Time) error {
	store.states[id] = state
	return nil
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
		Batch: postgres.AdoptionBatch{ID: batchID, SourceRoot: filepath.Join(rootPath, "Existing"), DefaultRootID: rootID},
		Items: []postgres.AdoptionExecutionItem{{
			Item:  postgres.AdoptionItem{ID: itemID, OriginalRelative: "Old.mkv", ProposedRelative: "Renamed/New.mkv", RecognizedCompanions: recognized, UnknownCompanions: unknown},
			Root:  postgres.LibraryRoot{ID: rootID, DestinationRoot: rootPath},
			Media: catalog.MediaItem{ID: mediaID, Kind: catalog.KindMovie},
		}},
	}
	operations, returnedRoot, err := buildOperations(current)
	if err != nil {
		t.Fatal(err)
	}
	if returnedRoot != rootPath || len(operations) != 3 || operations[0].DestinationRelative != "Renamed/New.mkv" || operations[1].DestinationRelative != "Renamed/New.en.srt" || operations[2].Kind != "unknown_preserved" {
		t.Fatalf("operations=%+v root=%q", operations, returnedRoot)
	}
	for index := range operations {
		operations[index].ID = uuid.NewV7()
	}
	store := &memoryOperationStore{states: map[uuid.UUID]string{}}
	if err := applyOperations(context.Background(), store, rootPath, operations); err != nil {
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
	if err := rollbackOperations(context.Background(), store, rootPath, operations); err != nil {
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
	current := postgres.AdoptionExecutionContext{Batch: postgres.AdoptionBatch{ID: uuid.NewV7(), SourceRoot: rootPath, DefaultRootID: rootID}, Items: []postgres.AdoptionExecutionItem{{Item: postgres.AdoptionItem{ID: uuid.NewV7(), OriginalRelative: "Old.mkv", ProposedRelative: "New.mkv", RecognizedCompanions: json.RawMessage(`[]`), UnknownCompanions: json.RawMessage(`[]`)}, Root: postgres.LibraryRoot{ID: rootID, DestinationRoot: rootPath}}}}
	if _, _, err := buildOperations(current); err == nil {
		t.Fatal("destination collision was accepted")
	}
}
