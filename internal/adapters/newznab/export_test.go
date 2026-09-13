package newznab

import (
	"context"
	"time"
)

// fetchCommissioningRawForTest is the test-only raw guarded-transmission
// helper. It exists ONLY in this _test.go file, so production-compiled code
// exposes no raw commissioning sender that bypasses snapshot -> durable
// authorizer -> attempt consumption -> guarded transport. Commissioning live
// sends go through FetchCommissioningWithTransmission.
func (client *Client) fetchCommissioningRawForTest(ctx context.Context, probe CommissioningProbe, dest CommissioningDestination, guard CommissioningGuard, deadline time.Time) (CommissioningResult, error) {
	return client.fetchCommissioning(ctx, probe, dest, guard, deadline)
}
