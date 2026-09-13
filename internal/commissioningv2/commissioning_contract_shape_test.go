package commissioningv2

import (
	"reflect"
	"testing"

	"github.com/media-manager/media-manager/internal/indexers"
)

// TestCommissioningContractsCarrySnapshotType is the E-65 item 6
// cross-package companion owned by the package that defines the contracts:
// LiveTransmitMarker and LiveCompletion must each carry the opaque
// indexers.CommissioningBudgetSnapshot and must carry no raw indexers.Budget.
func TestCommissioningContractsCarrySnapshotType(t *testing.T) {
	t.Parallel()
	snapshotType := reflect.TypeOf(indexers.CommissioningBudgetSnapshot{})
	budgetType := reflect.TypeOf(indexers.Budget{})
	for name, contract := range map[string]reflect.Type{
		"LiveTransmitMarker": reflect.TypeOf(LiveTransmitMarker{}),
		"LiveCompletion":     reflect.TypeOf(LiveCompletion{}),
	} {
		hasSnapshot, hasBudget := false, false
		for index := 0; index < contract.NumField(); index++ {
			field := contract.Field(index)
			if field.Type == snapshotType {
				hasSnapshot = true
			}
			if field.Type == budgetType {
				hasBudget = true
			}
		}
		if !hasSnapshot {
			t.Fatalf("%s carries no CommissioningBudgetSnapshot", name)
		}
		if hasBudget {
			t.Fatalf("%s carries a raw indexers.Budget", name)
		}
	}
}
