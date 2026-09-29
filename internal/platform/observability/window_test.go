package observability

import (
	"net/http"
	"testing"
	"time"
)

func TestWindowPercentilesAndErrors(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	w := NewWindow(func() time.Time { return now })
	if got := w.Snapshot(); got.All.Requests != 0 || got.All.P95MS != nil {
		t.Fatalf("empty window = %+v", got)
	}
	// 95 fast requests, 5 slow ones: p50 is the fast bucket, p95 still fast,
	// but 5 samples above p95 rank would move it — put 6 slow to cross.
	for i := 0; i < 94; i++ {
		w.Observe("api_v1_lessons", http.MethodGet, 200, 8*time.Millisecond)
	}
	for i := 0; i < 6; i++ {
		w.Observe("api_v1_lessons", http.MethodGet, 500, 700*time.Millisecond)
	}
	got := w.Snapshot()
	if got.All.Requests != 100 || got.All.Errors5xx != 6 {
		t.Fatalf("counts = %+v", got.All)
	}
	if got.All.P50MS == nil || *got.All.P50MS != 10 {
		t.Fatalf("p50 = %v, want 10", got.All.P50MS)
	}
	if got.All.P95MS == nil || *got.All.P95MS != 1000 {
		t.Fatalf("p95 = %v, want 1000", got.All.P95MS)
	}
	if got.Commands.Requests != 0 {
		t.Fatalf("commands = %+v", got.Commands)
	}
}

func TestWindowSeparatesCommandsAndIgnoresProbesAndStreams(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	w := NewWindow(func() time.Time { return now })
	w.Observe("api_v1_items_itemid_actions", http.MethodPost, 200, 20*time.Millisecond)
	w.Observe("api_v1_items_itemid_actions", http.MethodGet, 200, 20*time.Millisecond)
	w.Observe("health", http.MethodGet, 200, time.Millisecond)
	w.Observe("api_v1_my_stream", http.MethodGet, 200, 10*time.Minute)
	w.Observe("api_v1_lessons_lessonid_stream", http.MethodGet, 200, 10*time.Minute)
	got := w.Snapshot()
	if got.All.Requests != 2 || got.Commands.Requests != 1 {
		t.Fatalf("stats = %+v", got)
	}
	if got.Commands.P95MS == nil || *got.Commands.P95MS != 25 {
		t.Fatalf("command p95 = %v, want 25", got.Commands.P95MS)
	}
}

func TestWindowForgetsOldSlots(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	w := NewWindow(func() time.Time { return now })
	w.Observe("api_v1_lessons", http.MethodGet, 200, 10*time.Millisecond)
	now = now.Add(4 * time.Minute)
	w.Observe("api_v1_lessons", http.MethodGet, 200, 10*time.Millisecond)
	if got := w.Snapshot().All.Requests; got != 2 {
		t.Fatalf("within window = %d, want 2", got)
	}
	now = now.Add(2 * time.Minute) // the first sample is now 6 minutes old
	if got := w.Snapshot().All.Requests; got != 1 {
		t.Fatalf("after expiry = %d, want 1", got)
	}
	now = now.Add(10 * time.Minute)
	if got := w.Snapshot().All.Requests; got != 0 {
		t.Fatalf("after all expired = %d, want 0", got)
	}
}

func TestWindowNilIsSafe(t *testing.T) {
	var w *Window
	w.Observe("x", http.MethodGet, 200, time.Millisecond)
	if got := w.Snapshot(); got.Seconds != WindowSeconds || got.All.Requests != 0 {
		t.Fatalf("nil snapshot = %+v", got)
	}
}

func TestWindowCountsServerErrorsSinceStartBeyondTheRing(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	w := NewWindow(func() time.Time { return now })
	w.Observe("api_v1_lessons", http.MethodGet, 500, time.Millisecond)
	now = now.Add(time.Hour)
	got := w.Snapshot()
	if got.All.Errors5xx != 0 || got.Errors5xxSinceStart != 1 || !got.Since.Equal(time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("snapshot = %+v", got)
	}
}
