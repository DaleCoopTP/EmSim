// New test: internal/platform/realtime end to end against real
// PostgreSQL — LISTEN/NOTIFY actually carries an event from a training
// domain transaction into a Hub a stream handler would read from.
//
//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"emsim/internal/auth"
	"emsim/internal/platform/observability"
	pgstore "emsim/internal/platform/postgres"
	"emsim/internal/platform/realtime"
	"emsim/internal/training"

	"github.com/google/uuid"
)

// TestRealtimeListenerDeliversDomainNotifications is slice-4-plan.md's
// C9: a RunListener LISTENing on the real database observes the
// pg_notify calls training.Service's own command path issues inside its
// own transactions (internal/training/service.go's notify), and the Hub
// it feeds resolves a pre-event cursor to exactly those events.
func TestRealtimeListenerDeliversDomainNotifications(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	service := newTrainingService(pool)

	hub := realtime.NewHub()
	listenerCtx, stopListener := context.WithCancel(ctx)
	defer stopListener()
	ready := make(chan struct{})
	logger := observability.NewLogger(nil, "test", "test")
	go realtime.RunListener(listenerCtx, pool, hub, logger, ready)
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("listener did not start LISTEN in time")
	}

	startCursor := hub.Cursor()

	_, trainee, workstationID, lesson := setupPilotLesson(t, ctx, pool, service, "ЮАО")
	instructorActor := principal(auth.User{ID: lesson.InstructorID, Role: auth.RoleInstructor}, uuid.Nil)
	if _, err := service.Start(ctx, instructorActor, lesson.ID, "req-start"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	actor := principal(trainee, workstationID)
	items, err := service.MyItems(ctx, actor)
	if err != nil || len(items) != 1 {
		t.Fatalf("MyItems = %+v, %v", items, err)
	}
	item := items[0]

	if _, err := service.Execute(ctx, actor, item.ID, training.Command{
		CommandID: uuid.New(), ExpectedSeq: 0, Type: training.CommandOpen, Payload: []byte(`{}`),
	}, "req-open"); err != nil {
		t.Fatalf("Execute(open): %v", err)
	}

	events, _, resync := waitForEvents(t, hub, startCursor, 5*time.Second)
	if resync {
		t.Fatal("unexpected resync while waiting for the open command's notification")
	}
	found := false
	for _, e := range events {
		if e.LessonID != nil && *e.LessonID == lesson.ID &&
			e.UserID != nil && *e.UserID == trainee.ID &&
			e.ItemID != nil && *e.ItemID == item.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("no matching event for lesson=%s user=%s item=%s in %+v", lesson.ID, trainee.ID, item.ID, events)
	}

	// A lost/reestablished LISTEN session must force a resync for a
	// cursor issued before it — simulate that directly rather than
	// actually killing the connection.
	hub.Reset()
	if _, _, resync := hub.Since(startCursor); !resync {
		t.Fatal("a cursor from a superseded epoch must resync")
	}
}

func waitForEvents(t *testing.T, hub *realtime.Hub, cursor string, timeout time.Duration) ([]realtime.Event, string, bool) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		if events, newCursor, resync := hub.Since(cursor); resync || len(events) > 0 {
			return events, newCursor, resync
		}
		select {
		case <-hub.Wait():
		case <-deadline:
			t.Fatal("timed out waiting for a realtime event")
		}
	}
}

// TestRealtimeListenerDeliversStopNotificationToTrainee covers Stop's
// own per-trainee notify (added alongside the lesson-scoped one): before
// this fix, Service.Stop only ever called notify with userID=uuid.Nil,
// which streamLesson's own instructor feed matches on (filters by
// LessonID) but streamMy's own trainee feed never does (filters by
// UserID) — so a trainee's /my/stream never invalidated on stop at all,
// and their workplace only refreshed once the durable lesson.close task
// later interrupted an open item of theirs, or on the next unrelated
// refetch if they had none open.
func TestRealtimeListenerDeliversStopNotificationToTrainee(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	service := newTrainingService(pool)

	hub := realtime.NewHub()
	listenerCtx, stopListener := context.WithCancel(ctx)
	defer stopListener()
	ready := make(chan struct{})
	logger := observability.NewLogger(nil, "test", "test")
	go realtime.RunListener(listenerCtx, pool, hub, logger, ready)
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("listener did not start LISTEN in time")
	}

	startCursor := hub.Cursor()

	_, trainee, _, lesson := setupPilotLesson(t, ctx, pool, service, "ЮАО")
	instructorActor := principal(auth.User{ID: lesson.InstructorID, Role: auth.RoleInstructor}, uuid.Nil)
	if _, err := service.Start(ctx, instructorActor, lesson.ID, "req-start"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Drain Start's own item.offered notification (which also carries
	// this trainee's user_id, plus a real item_id) before capturing the
	// cursor Stop's own events are measured from — otherwise an
	// asynchronous LISTEN delivery still in flight for Start (the
	// domain transaction already committed, but the separate LISTEN
	// connection's delivery into the Hub is not synchronous with that
	// commit) could land after hub.Cursor() is read and be
	// indistinguishable from Stop's own per-trainee event.
	if _, newCursor, resync := waitForEvents(t, hub, startCursor, 5*time.Second); resync {
		t.Fatal("unexpected resync while waiting for Start's own notification")
	} else {
		startCursor = newCursor
	}

	if _, err := service.Stop(ctx, instructorActor, lesson.ID, nil, "req-stop"); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Stop's own per-trainee notify (the fix under test) never carries
	// an item_id (Service.Stop's own barrier touches no single item);
	// requiring ItemID == nil rules out this assertion being satisfied
	// by a leftover item-scoped event instead of the fix itself.
	foundTrainee, foundLessonWide := false, false
	var events []realtime.Event
	deadline := time.Now().Add(5 * time.Second)
	for !foundTrainee || !foundLessonWide {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		batch, nextCursor, resync := waitForEvents(t, hub, startCursor, remaining)
		if resync {
			t.Fatal("unexpected resync while waiting for Stop's own notification")
		}
		startCursor = nextCursor
		events = append(events, batch...)
		for _, e := range batch {
			if e.LessonID == nil || *e.LessonID != lesson.ID {
				continue
			}
			if e.UserID != nil && *e.UserID == trainee.ID && e.ItemID == nil {
				foundTrainee = true
			}
			if e.UserID == nil && e.ItemID == nil {
				foundLessonWide = true
			}
		}
	}
	if !foundTrainee {
		t.Fatalf("no item-less event scoped to the trainee's own user_id=%s after Stop, in %+v (streamMy would never invalidate)", trainee.ID, events)
	}
	if !foundLessonWide {
		t.Fatalf("no lesson-wide event (user_id=nil, item_id=nil) after Stop, in %+v (streamLesson's own instructor feed would never invalidate)", events)
	}
}
