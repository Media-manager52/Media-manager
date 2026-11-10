package indexers

const (
	// DefaultSearchPageLimit is the page size asked for when neither the
	// provider's capabilities nor the owner state another one.
	DefaultSearchPageLimit = 100
	// MaxSearchPageLimit is the largest page size ever asked for.
	MaxSearchPageLimit = 500
)

// SearchPageLimit is the page size one request asks an indexer for. The
// owner's stated page size wins; otherwise the maximum the provider itself
// advertises in its capabilities is used. Both are bounded by
// MaxSearchPageLimit, and an indexer that states nothing is asked for
// DefaultSearchPageLimit.
func SearchPageLimit(ownerStated, advertised int) int {
	limit := DefaultSearchPageLimit
	if advertised > 0 {
		limit = advertised
	}
	if ownerStated > 0 {
		limit = ownerStated
	}
	if limit > MaxSearchPageLimit {
		limit = MaxSearchPageLimit
	}
	return limit
}
