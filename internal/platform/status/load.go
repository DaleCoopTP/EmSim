package status

import (
	"context"

	"emsim/internal/platform/observability"
)

// LoadSources is everything the load panel reads, each as a function so
// this package never imports the auth or training modules (their stores
// satisfy these in cmd/emsim). A nil source leaves its field out.
type LoadSources struct {
	Window         *observability.Window
	Streams        func() int
	ActiveSessions func(ctx context.Context) (int, error)
	Activity       func(ctx context.Context) (Activity, error)
}

// Activity is how much training is going on right now.
type Activity struct {
	RunningLessons int
	OpenItems      int
}

type latencyJSON struct {
	Requests  int      `json:"requests"`
	Errors5xx int      `json:"errors_5xx"`
	P50MS     *float64 `json:"p50_ms"`
	P95MS     *float64 `json:"p95_ms"`
}

type hostJSON struct {
	CPUs              int      `json:"cpus"`
	Load1             *float64 `json:"load1"`
	Load5             *float64 `json:"load5"`
	Load15            *float64 `json:"load15"`
	MemTotalBytes     *int64   `json:"mem_total_bytes"`
	MemAvailableBytes *int64   `json:"mem_available_bytes"`
}

type loadJSON struct {
	WindowSeconds  int         `json:"window_seconds"`
	Requests       latencyJSON `json:"requests"`
	Commands       latencyJSON `json:"commands"`
	SSEConnections *int        `json:"sse_connections"`
	ActiveSessions *int        `json:"active_sessions"`
	RunningLessons *int        `json:"running_lessons"`
	OpenItems      *int        `json:"open_items"`
	Host           hostJSON    `json:"host"`
}

// WithLoad sets the load panel's sources. Without it /admin/status has no
// meaningful load figures and reports a zeroed panel.
func (h *Handlers) WithLoad(sources LoadSources) *Handlers {
	h.load = sources
	return h
}

func (h *Handlers) loadSnapshot(ctx context.Context) loadJSON {
	stats := h.load.Window.Snapshot()
	out := loadJSON{
		WindowSeconds: stats.Seconds,
		Requests:      toLatencyJSON(stats.All),
		Commands:      toLatencyJSON(stats.Commands),
	}
	if h.load.Streams != nil {
		n := h.load.Streams()
		out.SSEConnections = &n
	}
	if h.load.ActiveSessions != nil {
		if n, err := h.load.ActiveSessions(ctx); err == nil {
			out.ActiveSessions = &n
		}
	}
	if h.load.Activity != nil {
		if activity, err := h.load.Activity(ctx); err == nil {
			lessons, items := activity.RunningLessons, activity.OpenItems
			out.RunningLessons, out.OpenItems = &lessons, &items
		}
	}
	host := ReadHost()
	out.Host = hostJSON{
		CPUs: host.CPUs, Load1: host.Load1, Load5: host.Load5, Load15: host.Load15,
		MemTotalBytes: host.MemTotalBytes, MemAvailableBytes: host.MemAvailableBytes,
	}
	return out
}

func toLatencyJSON(s observability.LatencyStats) latencyJSON {
	return latencyJSON{Requests: s.Requests, Errors5xx: s.Errors5xx, P50MS: s.P50MS, P95MS: s.P95MS}
}
