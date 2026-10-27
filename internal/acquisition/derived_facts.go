package acquisition

import (
	"fmt"
	"strings"
	"time"

	"github.com/media-manager/media-manager/internal/releases"
)

// replaceDerivedFact replaces every previous evaluator-owned fact with the same
// name. Derived evidence is rebuilt deterministically on every evaluation, so
// an unchanged observation must not accumulate copies of the evaluator's own
// facts.
func replaceDerivedFact(facts []releases.Fact, replacement releases.Fact) []releases.Fact {
	result := make([]releases.Fact, 0, len(facts)+1)
	for _, fact := range facts {
		if strings.EqualFold(strings.TrimSpace(fact.Name), replacement.Name) {
			continue
		}
		result = append(result, fact)
	}
	return append(result, replacement)
}

// dedupeUnmappedFacts collapses exact duplicate provenance aliases created by
// older evaluator runs. Original provider evidence remains under its unmapped_
// alias; the observation row itself remains the historical source. Distinct
// values or evidence indexes are retained.
func dedupeUnmappedFacts(facts []releases.Fact) []releases.Fact {
	seen := map[string]bool{}
	result := make([]releases.Fact, 0, len(facts))
	for _, fact := range facts {
		name := strings.ToLower(strings.TrimSpace(fact.Name))
		if !strings.HasPrefix(name, "unmapped_") {
			result = append(result, fact)
			continue
		}
		key := name + "\x00" + strings.Join(fact.Values, "\x00") + "\x00" + string(fact.State) + "\x00" + fmt.Sprint(fact.EvidenceAt)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, fact)
	}
	return result
}

// derivedFactTime is the observation's source timestamp. Using time.Now here
// would make two evaluations of identical evidence differ and create a new
// immutable observation version on every retry.
func derivedFactTime(observation releases.Observation) time.Time {
	return observation.ObservedAt.UTC()
}
