package postgres

import (
	"testing"
	"time"
	"uuid"
)

// An event that was planned for later, waited in the queue, failed once, was
// retried after a scheduled delay and then finished reports each of those
// four parts separately.
func TestOutboxTimingSeparatesPlannedQueueWorkAndRetry(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	if _, err := f.store.Pool.Exec(f.ctx, `UPDATE schema_migrations SET applied_at=now()-interval '1 day' WHERE version=71`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Pool.Exec(f.ctx, `UPDATE outbox_events SET completed_at=now() WHERE completed_at IS NULL`); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewV7()
	// Created 10 minutes ago, planned for 4 minutes ago.
	if _, err := f.store.Pool.Exec(f.ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at)
		VALUES($1,'timing_test',$2,1,'timing.test','{}',now()-interval '10 minutes',now()-interval '4 minutes')`, id.String(), uuid.NewV7().String()); err != nil {
		t.Fatal(err)
	}
	lane := OutboxLane{Only: []string{"timing.test"}}
	claim := func() {
		t.Helper()
		events, err := f.store.ClaimOutboxLane(f.ctx, "timing-worker", 1, time.Minute, false, lane)
		if err != nil || len(events) != 1 || events[0].ID != id {
			t.Fatalf("claim: events=%v err=%v", events, err)
		}
	}
	// "Time passes" by moving the event's own timestamps back.
	elapse := func(interval string) {
		t.Helper()
		if _, err := f.store.Pool.Exec(f.ctx, `UPDATE outbox_events SET occurred_at=occurred_at-$2::interval,available_at=available_at-$2::interval,first_ready_at=first_ready_at-$2::interval,
			first_claimed_at=first_claimed_at-$2::interval,last_claimed_at=last_claimed_at-$2::interval,lease_until=lease_until-$2::interval WHERE id=$1`, id.String(), interval); err != nil {
			t.Fatal(err)
		}
	}
	claim()
	elapse("30 seconds") // the first attempt works for 30 seconds and fails
	if err := f.store.FailOutbox(f.ctx, id, "timing-worker", "test failure", time.Now().Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	elapse("12 minutes") // the retry was planned 10 minutes on; it is taken 2 minutes late
	claim()
	elapse("90 seconds") // the second attempt works for 90 seconds
	if err := f.store.CompleteOutbox(f.ctx, id, "timing-worker"); err != nil {
		t.Fatal(err)
	}
	timings, err := f.store.OutboxTimings(f.ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var timing *OutboxTiming
	for index := range timings {
		if timings[index].EventType == "timing.test" {
			timing = &timings[index]
		}
	}
	if timing == nil {
		t.Fatalf("no timing for the test event in %+v", timings)
	}
	near := func(name string, gotMS int64, want time.Duration) {
		t.Helper()
		if difference := time.Duration(gotMS)*time.Millisecond - want; difference < -5*time.Second || difference > 5*time.Second {
			t.Errorf("%s = %s, want about %s", name, time.Duration(gotMS)*time.Millisecond, want)
		}
	}
	if timing.Events != 1 || timing.Retried != 1 || timing.Attempts != 2 {
		t.Fatalf("events=%d retried=%d attempts=%d, want 1 1 2", timing.Events, timing.Retried, timing.Attempts)
	}
	near("planned wait", timing.PlannedP50MS, 6*time.Minute)   // created 10, due 4 minutes before the first claim
	near("queue wait", timing.ReadyWaitP50MS, 6*time.Minute)   // 4 minutes before the first claim, 2 before the second
	near("work", timing.ActiveP50MS, 2*time.Minute)            // 30 + 90 seconds
	near("retry delay", timing.RetryP50MS, 10*time.Minute)     // the planned delay before the second attempt
	near("work in total", timing.ActiveTotalMS, 2*time.Minute) // the sums agree for one event
	queue, err := f.store.QueueSnapshot(f.ctx, time.Now().UTC())
	if err != nil || len(queue.TimingsLast24h) == 0 {
		t.Fatalf("queue snapshot timings=%+v err=%v", queue.TimingsLast24h, err)
	}
}

// A worker that takes an event and never reports back adds no work time: the
// wait for the next worker is counted from the end of its lease.
func TestOutboxTimingDoesNotCountAVanishedWorkerAsWork(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	if _, err := f.store.Pool.Exec(f.ctx, `UPDATE schema_migrations SET applied_at=now()-interval '1 day' WHERE version=71`); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewV7()
	if _, err := f.store.Pool.Exec(f.ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at)
		VALUES($1,'timing_test',$2,1,'timing.vanished','{}',now(),now())`, id.String(), uuid.NewV7().String()); err != nil {
		t.Fatal(err)
	}
	lane := OutboxLane{Only: []string{"timing.vanished"}}
	if events, err := f.store.ClaimOutboxLane(f.ctx, "gone", 1, time.Minute, false, lane); err != nil || len(events) != 1 {
		t.Fatalf("first claim: %v %v", events, err)
	}
	// Ten minutes later the lease ran out three minutes ago.
	if _, err := f.store.Pool.Exec(f.ctx, `UPDATE outbox_events SET occurred_at=occurred_at-interval '10 minutes',available_at=available_at-interval '10 minutes',first_ready_at=first_ready_at-interval '10 minutes',
		first_claimed_at=first_claimed_at-interval '10 minutes',last_claimed_at=last_claimed_at-interval '10 minutes',lease_until=now()-interval '3 minutes' WHERE id=$1`, id.String()); err != nil {
		t.Fatal(err)
	}
	if events, err := f.store.ClaimOutboxLane(f.ctx, "next", 1, time.Minute, false, lane); err != nil || len(events) != 1 {
		t.Fatalf("second claim: %v %v", events, err)
	}
	if err := f.store.CompleteOutbox(f.ctx, id, "next"); err != nil {
		t.Fatal(err)
	}
	var claims int
	var readyWait, active int64
	if err := f.store.Pool.QueryRow(f.ctx, `SELECT claim_count,ready_wait_ms,active_ms FROM outbox_events WHERE id=$1`, id.String()).Scan(&claims, &readyWait, &active); err != nil {
		t.Fatal(err)
	}
	if claims != 2 || readyWait < 170_000 || readyWait > 190_000 || active > 5_000 {
		t.Fatalf("claims=%d ready_wait=%dms active=%dms, want 2 claims, about 3 minutes waiting and no work", claims, readyWait, active)
	}
}

func TestQueueTimingExcludesEventsCreatedBeforeUpgrade(t *testing.T) {
	f := newFailureRecoveryFixture(t, 70)
	id := uuid.NewV7()
	if _, err := f.store.Pool.Exec(f.ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'review',$2,1,'review.legacy','{}',now()-interval '3 days',now()-interval '1 day')`, id.String(), uuid.NewV7().String()); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(f.ctx, f.store.Pool); err != nil {
		t.Fatal(err)
	}
	events, err := f.store.ClaimOutboxLane(f.ctx, "review", 1, time.Minute, false, OutboxLane{Only: []string{"review.legacy"}})
	if err != nil || len(events) != 1 {
		t.Fatalf("claim: %v %v", events, err)
	}
	if err := f.store.CompleteOutbox(f.ctx, id, "review"); err != nil {
		t.Fatal(err)
	}
	timings, err := f.store.OutboxTimings(f.ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, timing := range timings {
		if timing.EventType == "review.legacy" {
			t.Fatalf("pre-upgrade event included: events=%d queue_wait_ms=%d", timing.Events, timing.ReadyWaitP50MS)
		}
	}
	for _, age := range []string{"1 day", "0 seconds"} {
		if _, err := f.store.Pool.Exec(f.ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,aggregate_revision,event_type,payload,occurred_at,available_at) VALUES($1,'timing',$2,1,'timing.pending','{}',now()-$3::interval,now())`, uuid.NewV7().String(), uuid.NewV7().String(), age); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := f.store.QueueSnapshot(f.ctx, time.Now().UTC())
	if err != nil || snapshot.LegacyPending != 1 || snapshot.Due != 2 || snapshot.TimingStartedAt.IsZero() {
		t.Fatalf("pending cohorts=%+v err=%v", snapshot, err)
	}
}
