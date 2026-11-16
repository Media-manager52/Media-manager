package postgres

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/postgres/commissioningstore"
)

func TestProviderCounterMigrationPreservesAgesAndRecentProbe(t *testing.T) {
	f := newFailureRecoveryFixture(t, 71)
	id := f.indexer.ID.String()
	now := time.Now().UTC().Truncate(time.Second)
	observed, probed := now.Add(-5*time.Hour), now.Add(-30*time.Minute)
	if _, err := f.store.Pool.Exec(f.ctx, `INSERT INTO indexer_provider_limits(indexer_instance_id,observed_at,query_used,query_max,grab_max) VALUES($1,$2,100,100,30)`, id, observed); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Pool.Exec(f.ctx, `INSERT INTO indexer_request_reservations(id,indexer_instance_id,request_family,reserved_at) VALUES($1,$2,'query',$3)`, uuid.NewV7().String(), id, probed); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(f.ctx, f.store.Pool); err != nil {
		t.Fatal(err)
	}
	sample, found, err := f.store.LoadProviderLimits(f.ctx, id)
	if err != nil || !found || !sample.QueryObservedAt.Equal(observed) || !sample.QueryLastProbeAt.Equal(probed) || sample.GrabUsed != -1 {
		t.Fatalf("migrated sample=%+v found=%v err=%v", sample, found, err)
	}
	retry, err := f.store.ReserveIndexerRequest(f.ctx, id, "query", 1000, 24*time.Hour, now)
	if err != nil || !retry.Equal(probed.Add(time.Hour)) {
		t.Fatalf("upgrade allowed an early probe: retry=%v err=%v", retry, err)
	}
}

func stated(observed time.Time, queryUsed, queryMax, grabUsed, grabMax int) indexers.ProviderLimits {
	return indexers.ProviderLimits{ObservedAt: observed, QueryUsed: queryUsed, QueryMax: queryMax, GrabUsed: grabUsed, GrabMax: grabMax}
}

func TestPartialProviderReadingsKeepCounterAgesAndExhaustion(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	id := f.indexer.ID.String()
	at := time.Now().UTC().Truncate(time.Second)
	save := func(sample indexers.ProviderLimits) {
		t.Helper()
		if err := f.store.SaveProviderLimits(f.ctx, id, sample); err != nil {
			t.Fatal(err)
		}
	}
	save(stated(at, 5, 100, 30, 30))
	save(stated(at.Add(20*time.Minute), 6, 100, -1, 300))
	sample, _, err := f.store.LoadProviderLimits(f.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if sample.GrabUsed != 30 || sample.GrabMax != 300 || sample.GrabCounterMax != 30 || !sample.GrabObservedAt.Equal(at) || !sample.GrabLimitObservedAt.Equal(at.Add(20*time.Minute)) {
		t.Fatalf("maximum-only answer erased or refreshed the exhausted count: %+v", sample)
	}
	if retry, err := f.store.ReserveIndexerRequest(f.ctx, id, "grab", 1000, 24*time.Hour, at.Add(21*time.Minute)); err != nil || retry.IsZero() {
		t.Fatalf("maximum-only increase cleared exhaustion: retry=%v err=%v", retry, err)
	}
	save(stated(at.Add(23*time.Hour), 7, 100, -1, -1))
	sample, _, err = f.store.LoadProviderLimits(f.ctx, id)
	if err != nil || !sample.GrabObservedAt.Equal(at) || !sample.GrabLimitObservedAt.Equal(at.Add(20*time.Minute)) {
		t.Fatalf("other family refreshed grab age: %+v %v", sample, err)
	}
	// The old counter expires at its own full window, while the newer maximum
	// is still applied. No absent count can extend that old count indefinitely.
	retry, err := f.store.ReserveIndexerRequest(f.ctx, id, "grab", 100, 24*time.Hour, at.Add(24*time.Hour+time.Second))
	if err != nil || !retry.IsZero() {
		t.Fatalf("expired counter still blocks: %v %v", retry, err)
	}
}

func TestProviderProbesAreHourlyAcrossConcurrentReservations(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	id := f.indexer.ID.String()
	at := time.Now().UTC().Truncate(time.Second)
	if err := f.store.SaveProviderLimits(f.ctx, id, stated(at, 100, 100, -1, -1)); err != nil {
		t.Fatal(err)
	}
	probeAt := at.Add(5 * time.Hour)
	var granted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			retry, err := f.store.ReserveIndexerRequest(f.ctx, id, "query", 1000, 24*time.Hour, probeAt)
			if err != nil {
				t.Error(err)
			} else if retry.IsZero() {
				granted.Add(1)
			}
		}()
	}
	wg.Wait()
	if granted.Load() != 1 {
		t.Fatalf("got %d probes together after five unused hours, want 1", granted.Load())
	}
	sample, _, err := f.store.LoadProviderLimits(f.ctx, id)
	if err != nil || !sample.QueryLastProbeAt.Equal(probeAt) {
		t.Fatalf("probe was not persisted: %+v %v", sample, err)
	}
	for _, after := range []time.Duration{time.Second, 59 * time.Minute, time.Hour} {
		retry, err := f.store.ReserveIndexerRequest(f.ctx, id, "query", 1000, 24*time.Hour, probeAt.Add(after))
		if err != nil || retry.IsZero() != (after == time.Hour) {
			t.Fatalf("after %s retry=%v err=%v", after, retry, err)
		}
	}
}

// The indexer's own stated counters tighten the reservations and never
// loosen them; a late answer cannot put older numbers back.
func TestProviderLimitsTightenReservationsAndNeverLoosenThem(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	id := f.indexer.ID.String()
	now := time.Now().UTC().Truncate(time.Second)
	reserve := func(family string, ownLimit int, at time.Time) bool {
		t.Helper()
		retryAt, err := f.store.ReserveIndexerRequest(f.ctx, id, family, ownLimit, 24*time.Hour, at)
		if err != nil {
			t.Fatal(err)
		}
		if !retryAt.IsZero() && !retryAt.After(at) {
			t.Fatalf("retry at %v is not after %v", retryAt, at)
		}
		return retryAt.IsZero()
	}
	// The indexer has counted 8 of 10; Media Manager's own limit is 100.
	if err := f.store.SaveProviderLimits(f.ctx, id, stated(now, 8, 10, -1, 30)); err != nil {
		t.Fatal(err)
	}
	if !reserve("query", 100, now.Add(time.Second)) || !reserve("query", 100, now.Add(2*time.Second)) {
		t.Fatal("the two requests the indexer still had room for were refused")
	}
	if reserve("query", 100, now.Add(3*time.Second)) {
		t.Fatal("a third request was reserved although the indexer's count plus the own reservations reached its maximum")
	}
	// Downloads: the indexer stated only a maximum (30), lower than the own 100.
	for i := 0; i < 30; i++ {
		if !reserve("grab", 100, now.Add(time.Duration(i+1)*time.Second)) {
			t.Fatalf("download %d was refused below the indexer's maximum", i+1)
		}
	}
	if reserve("grab", 100, now.Add(time.Minute)) {
		t.Fatal("a download beyond the indexer's stated maximum was reserved")
	}
	// An answer that arrives late carries older numbers: they are not saved.
	if err := f.store.SaveProviderLimits(f.ctx, id, stated(now.Add(-time.Minute), 0, 1000, 0, 1000)); err != nil {
		t.Fatal(err)
	}
	saved, found, err := f.store.LoadProviderLimits(f.ctx, id)
	if err != nil || !found || saved.QueryUsed != 8 || saved.QueryMax != 10 || saved.GrabUsed != -1 || saved.GrabMax != 30 {
		t.Fatalf("saved=%+v found=%v err=%v", saved, found, err)
	}
	if reserve("query", 100, now.Add(4*time.Second)) {
		t.Fatal("an older reading loosened the limit")
	}
	// A newer reading with a higher maximum than the own limit loosens
	// nothing either: the own limit and count stay in force.
	if err := f.store.SaveProviderLimits(f.ctx, id, stated(now.Add(time.Hour), 0, 1000, 0, 1000)); err != nil {
		t.Fatal(err)
	}
	if reserve("query", 2, now.Add(time.Hour+time.Second)) {
		t.Fatal("a generous reading let a request through past the own limit of 2 (2 are reserved)")
	}
	if !reserve("query", 100, now.Add(time.Hour+time.Second)) {
		t.Fatal("a newer reading with room did not lift the block")
	}
}

// A reading that shows the allowance used up keeps blocking: not dropped
// after fifteen minutes, one probe an hour later, and only gone when a whole
// window has passed or a newer reading shows room.
func TestUsedUpProviderReadingIsKeptUntilReplacedOrAWindowOld(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	id := f.indexer.ID.String()
	now := time.Now().UTC().Truncate(time.Second)
	allowed := func(family string, at time.Time) bool {
		t.Helper()
		retryAt, err := f.store.ReserveIndexerRequest(f.ctx, id, family, 1000, 24*time.Hour, at)
		if err != nil {
			t.Fatal(err)
		}
		return retryAt.IsZero()
	}
	if err := f.store.SaveProviderLimits(f.ctx, id, stated(now, 50, 1000, 30, 30)); err != nil {
		t.Fatal(err)
	}
	for _, after := range []time.Duration{time.Second, 16 * time.Minute, 59 * time.Minute} {
		if allowed("grab", now.Add(after)) {
			t.Fatalf("a download was reserved %s after a used-up reading", after)
		}
	}
	if !allowed("query", now.Add(time.Minute)) {
		t.Fatal("used-up downloads blocked a query")
	}
	if !allowed("grab", now.Add(time.Hour)) {
		t.Fatal("no probe was allowed an hour after the used-up reading")
	}
	if allowed("grab", now.Add(time.Hour+time.Minute)) {
		t.Fatal("a second download followed the probe within the hour")
	}
	// A newer reading with room (it arrives with any query answer) lifts it.
	if err := f.store.SaveProviderLimits(f.ctx, id, stated(now.Add(90*time.Minute), 51, 1000, 28, 30)); err != nil {
		t.Fatal(err)
	}
	if !allowed("grab", now.Add(91*time.Minute)) {
		t.Fatal("a newer reading with room did not lift the block")
	}
	// Used up again, and no newer reading ever arrives: gone only after a
	// whole window.
	if err := f.store.SaveProviderLimits(f.ctx, id, stated(now.Add(2*time.Hour), 51, 1000, 30, 30)); err != nil {
		t.Fatal(err)
	}
	if allowed("grab", now.Add(2*time.Hour+20*time.Minute)) {
		t.Fatal("the second used-up reading was ignored after twenty minutes")
	}
	if !allowed("grab", now.Add(26*time.Hour+time.Second)) {
		t.Fatal("a reading a whole window old still blocked")
	}
}

// Requests reserved at the same moment cannot together pass the indexer's
// stated room: the decision is taken under the reservation's own lock.
func TestConcurrentReservationsStayInsideTheStatedRoom(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	id := f.indexer.ID.String()
	now := time.Now().UTC().Truncate(time.Second)
	if err := f.store.SaveProviderLimits(f.ctx, id, stated(now, 5, 10, -1, -1)); err != nil {
		t.Fatal(err)
	}
	var granted, failed atomic.Int32
	var wait sync.WaitGroup
	for i := 0; i < 24; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			retryAt, err := f.store.ReserveIndexerRequest(f.ctx, id, "query", 1000, 24*time.Hour, now.Add(time.Second))
			if err != nil {
				failed.Add(1)
			} else if retryAt.IsZero() {
				granted.Add(1)
			}
		}()
	}
	wait.Wait()
	if failed.Load() != 0 || granted.Load() != 5 {
		t.Fatalf("granted=%d failed=%d, want exactly the 5 requests of stated room", granted.Load(), failed.Load())
	}
}

// The scheduler passes a reading on to the store; a reading that states
// nothing is not saved.
func TestSchedulerSavesObservedProviderLimits(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	id := f.indexer.ID.String()
	scheduler := indexers.NewScheduler(1, f.store)
	scheduler.ObserveProviderLimits(f.ctx, id, indexers.ProviderLimits{ObservedAt: time.Now(), QueryUsed: -1, QueryMax: -1, GrabUsed: -1, GrabMax: -1})
	if _, found, err := f.store.LoadProviderLimits(f.ctx, id); err != nil || found {
		t.Fatalf("an empty reading was saved: found=%v err=%v", found, err)
	}
	scheduler.ObserveProviderLimits(f.ctx, id, stated(time.Now(), 3, 10, 1, 5))
	if saved, found, err := f.store.LoadProviderLimits(f.ctx, id); err != nil || !found || saved.QueryUsed != 3 || saved.GrabMax != 5 {
		t.Fatalf("saved=%+v found=%v err=%v", saved, found, err)
	}
}

// INDEXER_STATED_LIMITS=off: no reading is saved, and a saved one is not
// applied. The own limit still is.
func TestIgnoredProviderLimitsAreNeitherSavedNorApplied(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	id := f.indexer.ID.String()
	now := time.Now().UTC().Truncate(time.Second)
	if err := f.store.SaveProviderLimits(f.ctx, id, stated(now, 10, 10, -1, -1)); err != nil {
		t.Fatal(err)
	}
	IgnoreIndexerStatedLimits()
	defer commissioningstore.IgnoreProviderLimits.Store(false)
	if retryAt, err := f.store.ReserveIndexerRequest(f.ctx, id, "query", 1, time.Hour, now.Add(time.Second)); err != nil || !retryAt.IsZero() {
		t.Fatalf("a used-up reading was applied while switched off: retry=%v err=%v", retryAt, err)
	}
	if retryAt, err := f.store.ReserveIndexerRequest(f.ctx, id, "query", 1, time.Hour, now.Add(2*time.Second)); err != nil || retryAt.IsZero() {
		t.Fatalf("the own limit of 1 was not applied: retry=%v err=%v", retryAt, err)
	}
	if err := f.store.SaveProviderLimits(f.ctx, id, stated(now.Add(time.Minute), 1, 99, -1, -1)); err != nil {
		t.Fatal(err)
	}
	if saved, _, err := f.store.LoadProviderLimits(f.ctx, id); err != nil || saved.QueryMax != 10 {
		t.Fatalf("a reading was saved while switched off: %+v err=%v", saved, err)
	}
}

func TestPartialProviderReadingPreservesExhaustedGrabCounter(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	id := f.indexer.ID.String()
	now := time.Now().UTC().Truncate(time.Second)
	if err := f.store.SaveProviderLimits(f.ctx, id, stated(now, 5, 100, 30, 30)); err != nil {
		t.Fatal(err)
	}
	if retry, err := f.store.ReserveIndexerRequest(f.ctx, id, "grab", 100, 24*time.Hour, now.Add(time.Second)); err != nil || retry.IsZero() {
		t.Fatalf("exhausted initial sample did not block: retry=%v err=%v", retry, err)
	}
	partial := indexers.ProviderLimits{ObservedAt: now.Add(time.Minute), QueryUsed: 6, QueryMax: 100, GrabUsed: -1, GrabMax: -1}
	if err := f.store.SaveProviderLimits(f.ctx, id, partial); err != nil {
		t.Fatal(err)
	}
	retry, err := f.store.ReserveIndexerRequest(f.ctx, id, "grab", 100, 24*time.Hour, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if retry.IsZero() {
		t.Fatal("a query-only reading erased an exhausted grab counter and permitted a download two minutes later")
	}
}

func TestIndexerUsageReflectsProviderGrabBlock(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	id := f.indexer.ID.String()
	if err := f.store.SaveProviderLimits(f.ctx, id, stated(now, 5, 100, 30, 30)); err != nil {
		t.Fatal(err)
	}
	retry, err := f.store.ReserveIndexerRequest(f.ctx, id, "grab", 100, 24*time.Hour, now.Add(time.Second))
	if err != nil || retry.IsZero() {
		t.Fatalf("expected provider block: retry=%v err=%v", retry, err)
	}
	items, err := f.store.IndexerUsages(f.ctx, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ID == f.indexer.ID && item.Usage.GrabsRemaining != 0 {
			t.Fatalf("scheduler blocks downloads, but API reports %d remaining (used=%d maximum=%d)", item.Usage.GrabsRemaining, item.Usage.GrabsUsed, item.Budget.GrabLimit)
		}
	}
}
