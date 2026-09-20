package realtime

import (
	"testing"

	"github.com/google/uuid"
)

func TestHubSinceReplaysEventsAfterCursor(t *testing.T) {
	h := NewHub()
	start := h.Cursor()
	lessonID := uuid.New()
	h.Publish(Event{LessonID: &lessonID})
	h.Publish(Event{LessonID: &lessonID})

	events, cursor, resync := h.Since(start)
	if resync {
		t.Fatal("resync=true for a freshly issued cursor")
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	if cursor != h.Cursor() {
		t.Fatalf("cursor = %q, want current head %q", cursor, h.Cursor())
	}

	// The head cursor itself has nothing new after it.
	events, _, resync = h.Since(cursor)
	if resync || len(events) != 0 {
		t.Fatalf("Since(head) = %d events, resync=%v, want 0 events, no resync", len(events), resync)
	}
}

func TestHubSinceResyncsOnUnknownOrStaleEpoch(t *testing.T) {
	h := NewHub()
	stale := h.Cursor()
	h.Reset() // simulates a lost/reestablished LISTEN session

	if _, _, resync := h.Since(stale); !resync {
		t.Fatal("cursor from a superseded epoch must resync")
	}
	if _, _, resync := h.Since("not-a-cursor"); !resync {
		t.Fatal("malformed cursor must resync")
	}
	if _, _, resync := h.Since(""); !resync {
		t.Fatal("empty cursor must resync")
	}
}

func TestHubSinceResyncsWhenBufferEvictsThePosition(t *testing.T) {
	h := NewHub()
	start := h.Cursor()
	for i := 0; i < bufferCap+5; i++ {
		h.Publish(Event{})
	}
	if _, _, resync := h.Since(start); !resync {
		t.Fatal("a cursor older than the buffer's coverage must resync")
	}

	// A cursor at the buffer's own oldest-minus-one boundary (no gap)
	// replays cleanly instead of resyncing.
	within, cursor, resync := h.Since(h.Cursor())
	if resync || len(within) != 0 {
		t.Fatalf("Since(head) = %d events, resync=%v", len(within), resync)
	}
	h.Publish(Event{})
	within, _, resync = h.Since(cursor)
	if resync || len(within) != 1 {
		t.Fatalf("Since(head-before-publish) = %d events, resync=%v, want 1 event, no resync", len(within), resync)
	}
}

func TestHubWaitWakesOnPublishAndReset(t *testing.T) {
	h := NewHub()
	waiter := h.Wait()
	select {
	case <-waiter:
		t.Fatal("Wait channel closed before any Publish/Reset")
	default:
	}
	h.Publish(Event{})
	select {
	case <-waiter:
	default:
		t.Fatal("Wait channel did not close after Publish")
	}

	waiter2 := h.Wait()
	h.Reset()
	select {
	case <-waiter2:
	default:
		t.Fatal("Wait channel did not close after Reset")
	}
}
