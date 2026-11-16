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
func ProviderReservationsSince(limits ProviderLimits) time.Time {
	return limits.ObservedAt.UTC().Add(-providerReservationMargin)
}

// Valid reports whether the reading states anything usable.
func (limits ProviderLimits) Valid() bool {
	if limits.ObservedAt.IsZero() {
		return false
	}
	_, _, queryOK := limits.family("query")
	_, _, grabOK := limits.family("grab")
	return queryOK || grabOK
}

// family returns the stated count and maximum of one request family. A stated
// maximum alone (no count) is returned with used -1.
func (limits ProviderLimits) family(family string) (used, maximum int, ok bool) {
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
// since ProviderReservationsSince(limits). It returns the limit and count to
// reserve against (never looser than the own ones) and, when the request must
// wait, the time to try again.
func ProviderLimitDecision(limits ProviderLimits, family string, ownLimit, ownUsed, reservedSince int, window time.Duration, now time.Time) (limit, used int, retryAt time.Time) {
	limit, used = ownLimit, ownUsed
	now = now.UTC()
	statedUsed, statedMax, ok := limits.family(family)
	observed := limits.ObservedAt.UTC()
	if !ok || window <= 0 || observed.IsZero() || !now.Before(observed.Add(window)) {
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
	if statedUsed < 0 {
		return limit, used, time.Time{}
	}
	if reservedSince < 0 {
		reservedSince = 0
	}
	expires := observed.Add(window)
	estimate := statedUsed + reservedSince
	if statedUsed >= statedMax {
		// Used up at the reading. Probes are the only requests let through,
		// and each one counts in reservedSince.
		probeFrom := observed.Add(providerProbeHold)
		if release := limits.oldest(family).UTC().Add(window); !limits.oldest(family).IsZero() && release.After(probeFrom) {
			probeFrom = release
		}
		if probeFrom.After(expires) {
			probeFrom = expires
		}
		allowed := 0
		if !now.Before(probeFrom) {
			allowed = 1 + int(now.Sub(probeFrom)/providerProbeInterval)
		}
		if reservedSince >= allowed {
			next := probeFrom.Add(time.Duration(allowed) * providerProbeInterval)
			if next.After(expires) {
				next = expires
			}
			return limit, statedMax, next
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
