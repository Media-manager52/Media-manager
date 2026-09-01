package commissioning

import (
	"errors"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/indexers"
)

func TestCommissionPacingDelayAllowsOnlyBoundedOrdinaryPacing(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	if delay, ok := commissionPacingDelay(indexers.CooldownError{Until: now.Add(3 * time.Second)}, now); !ok || delay != 3*time.Second {
		t.Fatalf("delay=%v ok=%v", delay, ok)
	}
	if _, ok := commissionPacingDelay(indexers.CooldownError{Until: now.Add(11 * time.Second)}, now); ok {
		t.Fatal("long cooldown was treated as an ordinary commissioning wait")
	}
	if _, ok := commissionPacingDelay(indexers.RequestBudgetError{Until: now.Add(3 * time.Second)}, now); ok {
		t.Fatal("rolling budget deferral was treated as ordinary pacing")
	}
	if _, ok := commissionPacingDelay(errors.New("network failed"), now); ok {
		t.Fatal("unrelated error was treated as pacing")
	}
}
