package postgres

import "github.com/media-manager/media-manager/internal/postgres/commissioningstore"

// IgnoreIndexerStatedLimits switches off the usage counters indexers state in
// their answers for this process: none is saved and none is applied to a
// request reservation. Call it once at start-up, before any request.
func IgnoreIndexerStatedLimits() { commissioningstore.IgnoreProviderLimits.Store(true) }
