// Ported from orchestration-core@34290d74656bc594f8968ae17871dc997559b497
// internal/worker/runner_test.go; adapted for NewRunner's new signature
// (pool name/size, a Kinds set, and a Registry instead of one fixed Kind
// pulling its lease from Policy).
package tasks

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

const testKind Kind = "test.kind"

func TestRunnerBoundsCapacityWithoutPrefetchAndDrains(t *testing.T) {
	store := &fakeStore{leases: []Lease{testLease(1), testLease(2), testLease(3)}}
	tickers := newFakeTickerFactory()
	started := make(chan struct{}, 3)
	release := make(chan struct{}, 3)
	handler := HandlerFunc(func(context.Context, Lease) error {
		started <- struct{}{}
		<-release
		return nil
	})
	runner := newTestRunner(t, 2, tickers, store, handler)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	poll := <-tickers.created
	poll.tick <- time.Now()
	<-started
	<-started
	if got := store.claimCount(); got != 2 {
		t.Fatalf("claims at capacity = %d, want 2", got)
	}
	cancel()
	poll.tick <- time.Now()
	release <- struct{}{}
	release <- struct{}{}
	if err := <-done; err != nil {
		t.Fatalf("drain runner: %v", err)
	}
	if got := store.claimCount(); got != 2 {
		t.Fatalf("claims after shutdown = %d, want 2", got)
	}
	if !poll.wasStopped() {
		t.Fatal("poll ticker was not stopped")
	}
}

func TestRunnerHeartbeatLeaseLossCancelsHandler(t *testing.T) {
	store := &fakeStore{leases: []Lease{testLease(1)}, heartbeatErr: ErrLeaseLost}
	tickers := newFakeTickerFactory()
	started := make(chan struct{})
	canceled := make(chan struct{})
	handler := HandlerFunc(func(ctx context.Context, _ Lease) error {
		close(started)
		<-ctx.Done()
		close(canceled)
		return ctx.Err()
	})
	runner := newTestRunner(t, 1, tickers, store, handler)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	poll := <-tickers.created
	poll.tick <- time.Now()
	<-started
	heartbeat := <-tickers.created
	heartbeat.tick <- time.Now()
	<-canceled
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("runner after lease loss: %v", err)
	}
	if store.heartbeatCount() != 1 || !heartbeat.wasStopped() {
		t.Fatalf("heartbeat calls/stopped = %d/%t", store.heartbeatCount(), heartbeat.wasStopped())
	}
}

func TestRunnerStopsHeartbeatWhenHandlerCompletes(t *testing.T) {
	store := &fakeStore{leases: []Lease{testLease(1)}}
	tickers := newFakeTickerFactory()
	release := make(chan struct{})
	handler := HandlerFunc(func(context.Context, Lease) error {
		<-release
		return nil
	})
	runner := newTestRunner(t, 1, tickers, store, handler)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	poll := <-tickers.created
	poll.tick <- time.Now()
	heartbeat := <-tickers.created
	close(release)
	<-heartbeat.stopped
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("runner after handler completion: %v", err)
	}
}

func TestRunnerDrainTimeoutCancelsActiveHandler(t *testing.T) {
	store := &fakeStore{leases: []Lease{testLease(1)}}
	tickers := newFakeTickerFactory()
	started := make(chan struct{})
	canceled := make(chan struct{})
	handler := HandlerFunc(func(ctx context.Context, _ Lease) error {
		close(started)
		<-ctx.Done()
		close(canceled)
		return ctx.Err()
	})
	runner := newTestRunner(t, 1, tickers, store, handler)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	poll := <-tickers.created
	poll.tick <- time.Now()
	<-started
	heartbeat := <-tickers.created
	cancel()
	drain := <-tickers.created
	drain.tick <- time.Now()
	<-canceled
	if err := <-done; err != nil {
		t.Fatalf("bounded shutdown error = %v", err)
	}
	if !poll.wasStopped() || !heartbeat.wasStopped() || !drain.wasStopped() {
		t.Fatal("poll, heartbeat, or drain timer leaked")
	}
}

func TestRunnerTreatsHandlerLeaseLossAsCompletedAttempt(t *testing.T) {
	tickers := newFakeTickerFactory()
	runner := newTestRunner(t, 1, tickers, &fakeStore{}, HandlerFunc(func(context.Context, Lease) error {
		return ErrLeaseLost
	}))
	if err := runner.runLease(context.Background(), testLease(1)); err != nil {
		t.Fatalf("runLease() error = %v", err)
	}
}

func newTestRunner(t *testing.T, poolSize int, tickers *fakeTickerFactory, store *fakeStore, handler Handler) *Runner {
	t.Helper()
	registry, err := NewRegistry(DefaultPolicy())
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	if err := registry.Register(Spec{Name: testKind, Pool: "test", MaxAttempts: 3, Lease: time.Minute, RetryBase: time.Second, Priority: 10}); err != nil {
		t.Fatalf("register test kind: %v", err)
	}
	runner, err := NewRunner(
		"test", poolSize, []Kind{testKind}, "test-worker", DefaultPolicy(), NoJitter{}, fixedClock{now: time.Now()},
		tickers, tickers, store, fakeResolver{}, handler, registry, time.Second, 10*time.Second,
	)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	return runner
}

func testLease(index int) Lease {
	return Lease{
		TaskID: uuid.MustParse(fmt.Sprintf("00000000-0000-0000-0000-%012d", index)),
		Kind:   testKind, WorkerID: "test-worker", Token: 1, Attempt: 1,
	}
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type fakeTicker struct {
	tick    chan time.Time
	stopped chan struct{}
	once    sync.Once
}

func newFakeTicker() *fakeTicker {
	return &fakeTicker{tick: make(chan time.Time, 8), stopped: make(chan struct{})}
}

func (t *fakeTicker) C() <-chan time.Time { return t.tick }
func (t *fakeTicker) Stop()               { t.once.Do(func() { close(t.stopped) }) }
func (t *fakeTicker) wasStopped() bool {
	select {
	case <-t.stopped:
		return true
	default:
		return false
	}
}

type fakeTickerFactory struct{ created chan *fakeTicker }

func newFakeTickerFactory() *fakeTickerFactory {
	return &fakeTickerFactory{created: make(chan *fakeTicker, 16)}
}

func (f *fakeTickerFactory) NewTicker(time.Duration) Ticker {
	ticker := newFakeTicker()
	f.created <- ticker
	return ticker
}

func (f *fakeTickerFactory) NewTimer(time.Duration) Timer {
	timer := newFakeTicker()
	f.created <- timer
	return timer
}

type fakeStore struct {
	mu           sync.Mutex
	leases       []Lease
	claims       int
	heartbeats   int
	heartbeatErr error
}

func (s *fakeStore) Claim(context.Context, ClaimRequest) (Lease, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claims++
	if len(s.leases) == 0 {
		return Lease{}, false, nil
	}
	lease := s.leases[0]
	s.leases = s.leases[1:]
	return lease, true, nil
}

func (s *fakeStore) Heartbeat(context.Context, HeartbeatRequest) (time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.heartbeats++
	return time.Time{}, s.heartbeatErr
}

func (s *fakeStore) claimCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.claims
}

func (s *fakeStore) heartbeatCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.heartbeats
}

type fakeResolver struct{}

func (fakeResolver) ResolveFailure(context.Context, FailureRequest) (Resolution, error) {
	return ResolutionRequeued, nil
}
