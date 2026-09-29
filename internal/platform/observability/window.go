package observability

import (
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Window is the api's own short memory of recent traffic for the admin
// status screen (ADR-038): request count, 5xx count and p50/p95 latency
// over the last five minutes, overall and for trainee commands
// (POST /items/{id}/actions — the hot path RFC-001 §11 sets a p95 target
// for). Prometheus on :8081 keeps the long history; this exists so an
// administrator without a Prometheus can see "is it slow right now".
//
// It is a ring of 30-second slots, each holding counts and a fixed-bucket
// latency histogram, so memory is bounded whatever the traffic. Percentiles
// are the upper bound of the histogram bucket that holds the rank — an
// estimate, exact to the bucket width, never lower than the truth.
type Window struct {
	now func() time.Time
	// since and total5xx outlive the ring: server errors since this
	// process started, for the failures report.
	since    time.Time
	total5xx atomic.Int64

	mu    sync.Mutex
	slots [windowSlots]windowSlot
}

const (
	windowSlots    = 10
	windowSlotSize = 30 * time.Second
	// WindowSeconds is how far back Snapshot looks.
	WindowSeconds = int(windowSlots * windowSlotSize / time.Second)
)

// latencyBoundsMS are the histogram's upper bounds; the last bucket holds
// everything slower than the final bound.
var latencyBoundsMS = [...]float64{5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000}

type histogram struct {
	counts [len(latencyBoundsMS) + 1]int
	total  int
	fives  int
}

func (h *histogram) add(ms float64, is5xx bool) {
	i := 0
	for i < len(latencyBoundsMS) && ms > latencyBoundsMS[i] {
		i++
	}
	h.counts[i]++
	h.total++
	if is5xx {
		h.fives++
	}
}

func (h *histogram) merge(o histogram) {
	for i := range h.counts {
		h.counts[i] += o.counts[i]
	}
	h.total += o.total
	h.fives += o.fives
}

// quantile returns the bucket upper bound holding rank q of the samples;
// the overflow bucket reports the last bound (a floor: "at least this").
func (h histogram) quantile(q float64) *float64 {
	if h.total == 0 {
		return nil
	}
	rank := int(q*float64(h.total) + 0.999999)
	if rank < 1 {
		rank = 1
	}
	seen := 0
	for i, c := range h.counts {
		seen += c
		if seen >= rank {
			bound := latencyBoundsMS[len(latencyBoundsMS)-1]
			if i < len(latencyBoundsMS) {
				bound = latencyBoundsMS[i]
			}
			return &bound
		}
	}
	return nil
}

type windowSlot struct {
	start    int64 // slot number (unix seconds / slot size); 0 = unused
	all      histogram
	commands histogram
}

// NewWindow returns an empty window; now defaults to time.Now.
func NewWindow(now func() time.Time) *Window {
	if now == nil {
		now = time.Now
	}
	return &Window{now: now, since: now()}
}

// IsCommandRoute reports whether route (a metrics route label) is the
// trainee command endpoint.
func IsCommandRoute(route string) bool {
	return strings.HasSuffix(route, "items_itemid_actions")
}

// Observe records one finished request.
func (w *Window) Observe(route, method string, status int, elapsed time.Duration) {
	// A stream lasts for minutes by design: counting it as one slow
	// request would make p95 meaningless.
	if w == nil || isProbeRoute(route) || strings.HasSuffix(route, "_stream") {
		return
	}
	ms := float64(elapsed) / float64(time.Millisecond)
	is5xx := status >= http.StatusInternalServerError
	if is5xx {
		w.total5xx.Add(1)
	}
	number := w.now().Unix() / int64(windowSlotSize/time.Second)
	w.mu.Lock()
	defer w.mu.Unlock()
	slot := &w.slots[number%windowSlots]
	if slot.start != number {
		*slot = windowSlot{start: number}
	}
	slot.all.add(ms, is5xx)
	if method == http.MethodPost && IsCommandRoute(route) {
		slot.commands.add(ms, is5xx)
	}
}

// LatencyStats is one traffic class over the window. The percentiles are
// null while there were no requests.
type LatencyStats struct {
	Requests  int
	Errors5xx int
	P50MS     *float64
	P95MS     *float64
}

// WindowStats is what Snapshot returns.
type WindowStats struct {
	Seconds  int
	All      LatencyStats
	Commands LatencyStats
	// Errors5xxSinceStart counts server errors since the window was
	// created (the process started); Since is that moment.
	Errors5xxSinceStart int
	Since               time.Time
}

// Snapshot merges the slots that still fall inside the window.
func (w *Window) Snapshot() WindowStats {
	if w == nil {
		return WindowStats{Seconds: WindowSeconds}
	}
	number := w.now().Unix() / int64(windowSlotSize/time.Second)
	var all, commands histogram
	w.mu.Lock()
	for _, slot := range w.slots {
		if slot.start != 0 && number-slot.start < windowSlots {
			all.merge(slot.all)
			commands.merge(slot.commands)
		}
	}
	w.mu.Unlock()
	stats := func(h histogram) LatencyStats {
		return LatencyStats{Requests: h.total, Errors5xx: h.fives, P50MS: h.quantile(0.5), P95MS: h.quantile(0.95)}
	}
	return WindowStats{Seconds: WindowSeconds, All: stats(all), Commands: stats(commands),
		Errors5xxSinceStart: int(w.total5xx.Load()), Since: w.since}
}
