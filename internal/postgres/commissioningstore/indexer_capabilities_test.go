package commissioningstore

import (
	"testing"
	"time"
	"uuid"
)

func TestIndexerOperationStatusValidationRejectsItemScopeAndUnsafeFields(t *testing.T) {
	base := IndexerOperationStatus{
		IndexerInstanceID:   uuid.NewV7(),
		CapabilityProfileID: uuid.NewV7(),
		OperationKey:        "search",
		RecipeKey:           "movie-imdb",
		Availability:        "degraded",
		LastOutcome:         "failed",
		SafeCode:            "malformed-envelope",
		LastFailureScope:    "operation",
		ConsecutiveFailures: 1,
		UpdatedAt:           time.Now().UTC(),
	}
	if !validIndexerOperationStatus(base) {
		t.Fatal("valid operation-scoped status was rejected")
	}
	itemScoped := base
	itemScoped.LastFailureScope = "item"
	if validIndexerOperationStatus(itemScoped) {
		t.Fatal("item failure was accepted as operation status")
	}
	wrongOperation := base
	wrongOperation.OperationKey = "details"
	if validIndexerOperationStatus(wrongOperation) {
		t.Fatal("non-search operation was accepted with a recipe key")
	}
	wrongOperation.RecipeKey = ""
	wrongOperation.OperationKey = "provider_custom"
	if validIndexerOperationStatus(wrongOperation) {
		t.Fatal("unknown operation key was accepted")
	}
	unsafe := base
	unsafe.SafeCode = "https://provider.invalid/?apikey=secret"
	if validIndexerOperationStatus(unsafe) {
		t.Fatal("unsafe diagnostic text was accepted")
	}
}
