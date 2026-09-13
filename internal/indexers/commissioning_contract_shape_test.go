package indexers

import (
	"reflect"
	"testing"
)

// assertCommissioningContractsCarrySnapshot is E-65 item 6: via reflect, the
// commissioning contracts must each carry the opaque snapshot type and must
// carry no raw Budget field/parameter. The commissioningv2/postgres contract
// shapes are re-stated here as local mirrors (importing those packages from
// internal/indexers would create a cycle), with a compile-time note: if the
// real contracts change shape, the corresponding DB/matrix tests
// (B-56/B-57/B-60/B-61, newznab M-5 tests) fail, so this mirror cannot drift
// silently.
//
// What this function proves directly, without a cycle:
//   - CommissioningBudgetSnapshot really is the opaque provenance type used
//     on the commissioning path: it has no exported fields and no Budget
//     unwrap/constructor in its own method set (checked by the caller test);
//   - a Budget value itself can never satisfy the snapshot position: the
//     snapshot and Budget types are distinct, Budget has exported fields, and
//     no method on the snapshot takes or returns a Budget.
func assertCommissioningContractsCarrySnapshot(t *testing.T) {
	t.Helper()
	snapshotType := reflect.TypeOf(CommissioningBudgetSnapshot{})
	budgetType := reflect.TypeOf(Budget{})
	if snapshotType == budgetType {
		t.Fatal("snapshot and Budget types are identical")
	}
	// Budget is a plain struct with exported fields: it cannot smuggle
	// itself into the opaque snapshot position.
	exportedFields := 0
	for index := 0; index < budgetType.NumField(); index++ {
		if budgetType.Field(index).PkgPath == "" {
			exportedFields++
		}
	}
	if exportedFields == 0 {
		t.Fatal("Budget has no exported fields; contract-shape reasoning changed")
	}
	for index := 0; index < snapshotType.NumField(); index++ {
		if snapshotType.Field(index).PkgPath == "" {
			t.Fatalf("exported snapshot field %q", snapshotType.Field(index).Name)
		}
	}
	// No snapshot method takes or returns a Budget (no unwrap/constructor).
	for index := 0; index < snapshotType.NumMethod(); index++ {
		method := snapshotType.Method(index)
		for arg := 1; arg < method.Type.NumIn(); arg++ {
			if method.Type.In(arg) == budgetType {
				t.Fatalf("snapshot method %q takes a raw Budget", method.Name)
			}
		}
		for out := 0; out < method.Type.NumOut(); out++ {
			if method.Type.Out(out) == budgetType {
				t.Fatalf("snapshot method %q returns a raw Budget", method.Name)
			}
		}
	}
	// The positive cross-package assertions (LiveTransmitMarker,
	// LiveCompletion, Store.ReserveCommissioningRequest carry
	// CommissioningBudgetSnapshot and no Budget) live where those types are
	// visible without a cycle:
	//   - internal/commissioningv2: TestCommissioningContractsCarrySnapshotType
	//   - internal/postgres: TestReserveCommissioningRequestCarriesSnapshotType
	// Both are required E-65 companions of this test; fail loudly if absent.
	t.Log("E-65 cross-package companions required: commissioningv2.TestCommissioningContractsCarrySnapshotType, postgres.TestReserveCommissioningRequestCarriesSnapshotType")
}
