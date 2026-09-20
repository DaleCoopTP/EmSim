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
