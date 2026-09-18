// Ported from orchestration-core@34290d74656bc594f8968ae17871dc997559b497
// internal/recovery/supervisor_test.go; renamed with the type (Supervisor
// -> Reaper).
package tasks

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestReaperRunsStoreAndStopsCleanly(t *testing.T) {
	ticker := &reaperFakeTicker{ticks: make(chan time.Time, 1), stopped: make(chan struct{})}
	factory := reaperTickerFactory{ticker: ticker}
	store := &reaperFakeStore{called: make(chan struct{}, 1)}
	reaper, err := NewReaper(DefaultPolicy(), factory, store)
	if err != nil {
		t.Fatalf("NewReaper: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- reaper.Run(ctx) }()
	ticker.ticks <- time.Now()
	<-store.called
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("reaper shutdown: %v", err)
	}
	select {
	case <-ticker.stopped:
	default:
		t.Fatal("reaper ticker was not stopped")
	}
}

type reaperFakeTicker struct {
	ticks   chan time.Time
	stopped chan struct{}
	once    sync.Once
}

func (t *reaperFakeTicker) C() <-chan time.Time { return t.ticks }
func (t *reaperFakeTicker) Stop()               { t.once.Do(func() { close(t.stopped) }) }

type reaperTickerFactory struct{ ticker *reaperFakeTicker }

func (f reaperTickerFactory) NewTicker(time.Duration) Ticker { return f.ticker }

type reaperFakeStore struct{ called chan struct{} }

func (r *reaperFakeStore) ReapExpired(context.Context) (ReapSummary, error) {
	r.called <- struct{}{}
	return ReapSummary{}, nil
}
