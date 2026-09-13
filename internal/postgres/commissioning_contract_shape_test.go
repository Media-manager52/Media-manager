package postgres

import (
	"reflect"
	"testing"

	"github.com/media-manager/media-manager/internal/indexers"
)

func stringTypeOfStore() reflect.Type { return reflect.TypeOf(Store{}) }

func storeMethodByName(t *testing.T, name string) reflect.Method {
	t.Helper()
	method, ok := reflect.TypeOf(&Store{}).MethodByName(name)
	if !ok {
		t.Fatalf("Store method %q not found", name)
	}
	return method
}

func assertSnapshotParamNoBudget(t *testing.T, method reflect.Method) {
	t.Helper()
	snapshotType := reflect.TypeOf(indexers.CommissioningBudgetSnapshot{})
	budgetType := reflect.TypeOf(indexers.Budget{})
	hasSnapshot, hasBudget := false, false
	for arg := 1; arg < method.Type.NumIn(); arg++ {
		parameter := method.Type.In(arg)
		if parameter == snapshotType {
			hasSnapshot = true
		}
		if parameter == budgetType {
			hasBudget = true
		}
	}
	if !hasSnapshot {
		t.Fatalf("Store.%s has no CommissioningBudgetSnapshot parameter", method.Name)
	}
	if hasBudget {
		t.Fatalf("Store.%s takes a raw indexers.Budget", method.Name)
	}
}
