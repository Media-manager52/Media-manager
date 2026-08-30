package altmount

import "testing"

func TestCompletedNeedsVerifiedInventory(t *testing.T) {
	item := QueueItem{ID: 1, Status: StatusCompleted}
	if phase := Reconcile(ReconcileInput{Queue: &item, Output: OutputObservation{Mapped: true, Exists: true, Readable: true}}); phase != PhaseNeedsAttention {
		t.Fatalf("completed was trusted without inventory: %s", phase)
	}
	if phase := Reconcile(ReconcileInput{Queue: &item, Output: OutputObservation{Mapped: true, Exists: true, Readable: true, InventoryComplete: true}}); phase != PhaseImportComplete {
		t.Fatalf("verified inventory did not complete import: %s", phase)
	}
}

func TestStreamingReadyRequiresSnapshotEvidence(t *testing.T) {
	item := QueueItem{ID: 1, Status: StatusProcessing}
	if phase := Reconcile(ReconcileInput{Queue: &item}); phase != PhaseProcessing {
		t.Fatalf("processing became streamable without output: %s", phase)
	}
}
