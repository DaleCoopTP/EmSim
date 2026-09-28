package tasks

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type movingClock struct{ now time.Time }

func (c *movingClock) Now() time.Time { return c.now }

// dedupQueue stands in for tasks.dedup_key's uniqueness: a second enqueue
// with the same key is a no-op, as ON CONFLICT DO NOTHING makes it.
type dedupQueue struct {
	keys  []string
	seen  map[string]bool
	fails int
}

func (q *dedupQueue) Enqueue(_ context.Context, request EnqueueRequest) (uuid.UUID, bool, error) {
	if err := request.Validate(); err != nil {
		return uuid.Nil, false, err
	}
	if q.fails > 0 {
		q.fails--
		return uuid.Nil, false, ErrStorage
	}
	if q.seen[request.DedupKey] {
		return uuid.Nil, false, nil
	}
	q.seen[request.DedupKey] = true
	q.keys = append(q.keys, request.DedupKey)
	return request.TaskID, true, nil
}

func TestSchedulerEnqueuesEachDailySlotOnce(t *testing.T) {
	moscow, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	clock := &movingClock{now: time.Date(2026, 9, 28, 2, 59, 0, 0, moscow)}
	queue := &dedupQueue{seen: map[string]bool{}}
	var failures []Kind
	newScheduler := func() *Scheduler {
		scheduler, err := NewScheduler([]Schedule{
			{Kind: "backup.run", At: 3 * time.Hour},
			{Kind: "audit.prune", At: 4 * time.Hour},
		}, moscow, time.Minute, clock, newFakeTickerFactory(), queue, func(_ context.Context, kind Kind, _ error) {
			failures = append(failures, kind)
		})
		if err != nil {
			t.Fatal(err)
		}
		return scheduler
	}
	scheduler := newScheduler()
	ctx := context.Background()

	scheduler.Tick(ctx) // 02:59 — no slot has come yet
	if len(queue.keys) != 0 {
		t.Fatalf("before the slot: %v", queue.keys)
	}
	clock.now = clock.now.Add(time.Minute) // 03:00
	scheduler.Tick(ctx)
	scheduler.Tick(ctx) // a second tick the same day changes nothing
	if want := []string{"backup.run:daily:2026-09-28"}; !equalStrings(queue.keys, want) {
		t.Fatalf("at the backup slot: %v, want %v", queue.keys, want)
	}

	// A restart the same day, after both slots: the backup is not
	// enqueued twice, the prune is enqueued once.
	clock.now = time.Date(2026, 9, 28, 23, 30, 0, 0, moscow)
	restarted := newScheduler()
	restarted.Tick(ctx)
	restarted.Tick(ctx)
	if want := []string{"backup.run:daily:2026-09-28", "audit.prune:daily:2026-09-28"}; !equalStrings(queue.keys, want) {
		t.Fatalf("after restart: %v, want %v", queue.keys, want)
	}

	// The next local day is a new slot, dated in Moscow time even though
	// it is still 28 September in UTC.
	clock.now = time.Date(2026, 9, 29, 3, 5, 0, 0, moscow)
	restarted.Tick(ctx)
	if got := queue.keys[len(queue.keys)-1]; got != "backup.run:daily:2026-09-29" || len(queue.keys) != 3 {
		t.Fatalf("next day: %v", queue.keys)
	}
	if len(failures) != 0 {
		t.Fatalf("unexpected failures: %v", failures)
	}
}

func TestSchedulerRetriesAFailedEnqueueOnTheNextTick(t *testing.T) {
	clock := &movingClock{now: time.Date(2026, 9, 28, 5, 0, 0, 0, time.UTC)}
	queue := &dedupQueue{seen: map[string]bool{}, fails: 1}
	var failures int
	scheduler, err := NewScheduler([]Schedule{{Kind: "audit.prune", At: 4 * time.Hour}}, time.UTC, time.Minute,
		clock, newFakeTickerFactory(), queue, func(context.Context, Kind, error) { failures++ })
	if err != nil {
		t.Fatal(err)
	}
	scheduler.Tick(context.Background())
	scheduler.Tick(context.Background())
	if failures != 1 || !equalStrings(queue.keys, []string{"audit.prune:daily:2026-09-28"}) {
		t.Fatalf("failures=%d keys=%v", failures, queue.keys)
	}
}

func TestSchedulerRejectsInvalidSchedules(t *testing.T) {
	clock := &movingClock{}
	queue := &dedupQueue{seen: map[string]bool{}}
	for _, schedule := range []Schedule{{Kind: "Bad", At: 0}, {Kind: "audit.prune", At: 24 * time.Hour}, {Kind: "audit.prune", At: -time.Minute}} {
		if _, err := NewScheduler([]Schedule{schedule}, time.UTC, time.Minute, clock, newFakeTickerFactory(), queue, nil); !errors.Is(err, ErrInvalidScheduler) {
			t.Fatalf("schedule %+v: err = %v", schedule, err)
		}
	}
	if at, err := ParseTimeOfDay("03:30"); err != nil || at != 3*time.Hour+30*time.Minute {
		t.Fatalf("ParseTimeOfDay = %v, %v", at, err)
	}
	if _, err := ParseTimeOfDay("25:00"); err == nil {
		t.Fatal("25:00 must be rejected")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
