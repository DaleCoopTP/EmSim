package status

import (
	"context"
	"net/http"
	"syscall"
	"time"
)

// Probe checks one component. Check returns a heartbeat status and a
// detail object; it must bound its own work by ctx.
type Probe struct {
	Component string
	Check     func(context.Context) (string, map[string]any)
}

// HeartbeatWriter is what Prober needs from storage; *Store satisfies it.
type HeartbeatWriter interface {
	UpsertHeartbeat(ctx context.Context, component, status string, detail map[string]any) error
}

// Ticker matches internal/platform/tasks.Ticker, so the worker hands in
// tasks.SystemTickerFactory's tickers unchanged.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

// Prober is a maintenance Supervisor recording every probe's result each
// interval. A failed check or write never stops the process: the status
// screen shows how old the last heartbeat is instead.
type Prober struct {
	probes   []Probe
	store    HeartbeatWriter
	interval time.Duration
	timeout  time.Duration
	newTick  func(time.Duration) Ticker
}

func NewProber(probes []Probe, store HeartbeatWriter, interval, timeout time.Duration, newTicker func(time.Duration) Ticker) *Prober {
	return &Prober{probes: probes, store: store, interval: interval, timeout: timeout, newTick: newTicker}
}

func (p *Prober) Run(ctx context.Context) error {
	ticker := p.newTick(p.interval)
	defer ticker.Stop()
	p.ProbeAll(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C():
			p.ProbeAll(ctx)
		}
	}
}

// ProbeAll runs every probe once and records the results.
func (p *Prober) ProbeAll(ctx context.Context) {
	for _, probe := range p.probes {
		if ctx.Err() != nil {
			return
		}
		checkCtx, cancel := context.WithTimeout(ctx, p.timeout)
		status, detail := probe.Check(checkCtx)
		cancel()
		_ = p.store.UpsertHeartbeat(ctx, probe.Component, status, detail)
	}
}

// HTTPCheck is a probe that GETs url (with an optional bearer key) and
// reports ok on a 2xx answer. The key is never part of the detail.
func HTTPCheck(url, bearer string, detail map[string]any) func(context.Context) (string, map[string]any) {
	client := &http.Client{}
	return func(ctx context.Context) (string, map[string]any) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return StatusUnavailable, detail
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := client.Do(req)
		if err != nil {
			return StatusUnavailable, detail
		}
		_ = resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return StatusUnavailable, detail
		}
		return StatusOK, detail
	}
}

// FreeBytes is the space available to an unprivileged user on the file
// system holding path.
func FreeBytes(path string) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return int64(uint64(stat.Bavail) * uint64(stat.Bsize)), nil
}
