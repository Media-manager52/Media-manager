package indexers

import (
	"context"
	"time"
)

// Provider limits: the usage counters an indexer states in its own answers.
//
// Media Manager counts its own requests (reservations) and paces them. Some
// indexers also say how many requests and downloads their side has counted
// and what their maximum is. Those numbers are a second opinion that can only
// make Media Manager more careful:
//
//   - the indexer's maximum replaces the configured limit only when lower;
//   - the indexer's count replaces Media Manager's own count only when higher;
//   - requests reserved after the reading are added to the indexer's count,
//     because the indexer had not seen them yet;
//   - a reading is kept until a newer one replaces it or until a whole limit
//     window has passed since it was taken. It never expires earlier, however
//     old it is: every request it counted has only left the window by then.
//
// A reading that shows the allowance used up blocks requests until a newer
// reading shows room again. Query answers carry a new reading, so while
// queries flow a download block lifts by itself. When nothing flows (queries
// themselves are used up) no new reading can arrive, so one request an hour is
// let through as a probe from the later of the indexer's own stated release
// time and providerProbeHold after the reading. The stated release time is
// not trusted further than that: indexers were measured stating local time as
// UTC.

// ProviderLimits is one reading. A negative number means "not stated".
type ProviderLimits struct {
	ObservedAt  time.Time
	QueryUsed   int
	QueryMax    int
	QueryOldest time.Time
	GrabUsed    int
	GrabMax     int
	GrabOldest  time.Time
	// Stored counter and maximum ages are independent. Raw answers use
	// ObservedAt for both; a partial answer never refreshes an absent counter.
	QueryObservedAt      time.Time
	GrabObservedAt       time.Time
	QueryLimitObservedAt time.Time
	GrabLimitObservedAt  time.Time
	QueryLastProbeAt     time.Time
	GrabLastProbeAt      time.Time
	// The maximum paired with a measured count keeps a known exhausted
	// reading blocked even if a later maximum-only answer raises the ceiling.
	QueryCounterMax int
	GrabCounterMax  int
}

// maxProviderLimit bounds a stated count or maximum; anything outside is
// treated as not stated.
const maxProviderLimit = 10_000_000

// providerProbeHold is the least time a used-up reading blocks every request
// before a probe may be let through; providerProbeInterval spaces the probes.
const (
	providerProbeHold     = time.Hour
	providerProbeInterval = time.Hour
	// providerReservationMargin widens "reserved after the reading" backwards:
	// a request reserved shortly before the reading may have reached the
	// indexer after it. Counting a few requests twice only tightens.
	providerReservationMargin = time.Minute
)

// ProviderReservationsSince is the time from which own reservations are added
// to a reading's count.
func ProviderReservationsSince(limits ProviderLimits, family string) time.Time {
	return limits.CounterObservedAt(family).Add(-providerReservationMargin)
}

func (limits ProviderLimits) CounterObservedAt(family string) time.Time {
	at := limits.QueryObservedAt
	if family == "grab" {
		at = limits.GrabObservedAt
	}
	if at.IsZero() {
		at = limits.ObservedAt
	}
	return at.UTC()
}

func (limits ProviderLimits) MaximumObservedAt(family string) time.Time {
	at := limits.QueryLimitObservedAt
	if family == "grab" {
		at = limits.GrabLimitObservedAt
	}
	if at.IsZero() {
		at = limits.ObservedAt
	}
	return at.UTC()
}

func (limits ProviderLimits) LastProbeAt(family string) time.Time {
	if family == "grab" {
		return limits.GrabLastProbeAt
	}
	return limits.QueryLastProbeAt
}

// Valid reports whether the reading states anything usable.
func (limits ProviderLimits) Valid() bool {
	if limits.ObservedAt.IsZero() {
		return false
	}
	_, _, queryOK := limits.Family("query")
	_, _, grabOK := limits.Family("grab")
	return queryOK || grabOK
}

// Family returns the stated count and maximum of one request family. A stated
// maximum alone (no count) is returned with used -1.
func (limits ProviderLimits) Family(family string) (used, maximum int, ok bool) {
	used, maximum = limits.QueryUsed, limits.QueryMax
	if family == "grab" {
		used, maximum = limits.GrabUsed, limits.GrabMax
	}
	if maximum <= 0 || maximum > maxProviderLimit {
		return -1, -1, false
	}
	if used < 0 || used > maxProviderLimit {
		used = -1
	}
	return used, maximum, true
}

func (limits ProviderLimits) oldest(family string) time.Time {
	if family == "grab" {
		return limits.GrabOldest
	}
	return limits.QueryOldest
}

// ProviderLimitDecision says whether a request of one family may be reserved.
// ownLimit and ownUsed are Media Manager's limit for this request and its own
// reservations inside window; reservedSince counts own reservations made
// since ProviderReservationsSince(limits, family). It returns the limit and count to
// reserve against (never looser than the own ones) and, when the request must
// wait, the time to try again.
func ProviderLimitDecision(limits ProviderLimits, family string, ownLimit, ownUsed, reservedSince int, window time.Duration, now time.Time) (limit, used int, retryAt time.Time) {
	limit, used = ownLimit, ownUsed
	now = now.UTC()
	statedUsed, statedMax, ok := limits.Family(family)
	observed, maximumAt := limits.CounterObservedAt(family), limits.MaximumObservedAt(family)
	if !ok || window <= 0 || maximumAt.IsZero() || !now.Before(maximumAt.Add(window)) {
		// Nothing stated, or the reading is a whole window old.
		return limit, used, time.Time{}
	}
	if observed.After(now) {
		// A clock that went backwards: count from now, never from the future.
		observed = now
	}
	if statedMax < limit {
		limit = statedMax
	}
	if statedUsed < 0 || observed.IsZero() || !now.Before(observed.Add(window)) {
		return limit, used, time.Time{}
	}
	if reservedSince < 0 {
		reservedSince = 0
	}
	expires := observed.Add(window)
	estimate := statedUsed + reservedSince
	if limits.counterExhausted(family, statedUsed, statedMax) {
		// Unused probe opportunities never accumulate. The reservation store
		// records the last actual probe under the same lock as the request.
		probeFrom := observed.Add(providerProbeHold)
		if release := limits.oldest(family).UTC().Add(window); !limits.oldest(family).IsZero() && release.After(probeFrom) {
			probeFrom = release
		}
		if probeFrom.After(expires) {
			probeFrom = expires
		}
		if next := limits.LastProbeAt(family).Add(providerProbeInterval); !limits.LastProbeAt(family).IsZero() && next.After(probeFrom) {
			probeFrom = next
		}
		if probeFrom.After(expires) {
			probeFrom = expires
		}
		if now.Before(probeFrom) {
			return limit, max(ownUsed, statedMax), probeFrom
		}
		// A probe: leave exactly one request of room.
		estimate = statedMax - 1
	}
	if estimate > used {
		used = estimate
	}
	if used >= limit && used > ownUsed {
		// Blocked by the indexer's count, not by the own one: the own retry
		// time does not apply. Try again when the indexer's oldest counted
		// request leaves the window, at the latest when the reading expires.
		retryAt = expires
		if release := limits.oldest(family).UTC().Add(window); !limits.oldest(family).IsZero() && release.After(now) && release.Before(expires) {
			retryAt = release
		}
		if floor := now.Add(time.Minute); retryAt.Before(floor) {
			retryAt = floor
		}
	}
	return limit, used, retryAt
}

// Exhausted reports a still-current counter that permits only recovery probes.
func (limits ProviderLimits) Exhausted(family string, window time.Duration, now time.Time) bool {
	used, maximum, ok := limits.Family(family)
	return ok && limits.counterExhausted(family, used, maximum) && window > 0 && now.Before(limits.CounterObservedAt(family).Add(window)) && now.Before(limits.MaximumObservedAt(family).Add(window))
}

func (limits ProviderLimits) counterExhausted(family string, used, maximum int) bool {
	paired := limits.QueryCounterMax
	if family == "grab" {
		paired = limits.GrabCounterMax
	}
	return used >= maximum || paired > 0 && used >= paired
}

// ProviderLimitStore keeps the newest reading per indexer. The reservation
// store applies it inside the same lock as the reservation itself.
type ProviderLimitStore interface {
	SaveProviderLimits(context.Context, string, ProviderLimits) error
}

// ObserveProviderLimits records a reading taken from an indexer's answer. It
// is best effort: a reading that cannot be saved changes nothing, and Media
// Manager's own reservations and pacing apply either way.
func (scheduler *Scheduler) ObserveProviderLimits(ctx context.Context, indexerID string, limits ProviderLimits) {
	if scheduler == nil || !limits.Valid() {
		return
	}
	if store, ok := scheduler.store.(ProviderLimitStore); ok {
		_ = store.SaveProviderLimits(ctx, indexerID, limits)
	}
}
