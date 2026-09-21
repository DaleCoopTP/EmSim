// Ported from orchestration-core@34290d74656bc594f8968ae17871dc997559b497
// internal/worker/runner.go; adapted: a Runner claims across a set of Kinds
// (a worker pool, e.g. "short"/"llm"/"stt" — see cmd/emsim/worker.go, added
// in a later commit) instead of one fixed Kind, so per-lease heartbeat
// renewal and retry backoff look up that lease's Kind in the Registry
// instead of using one Policy-wide lease/retry base. Handler dispatch is
// still a single Handler value; HandlerRegistry below routes a claimed
// Lease to the sub-handler registered for its Kind, so cmd/emsim/worker.go
// does not need its own dispatch logic. Observer's first argument is now
// the pool name rather than a single Kind string, to keep worker metrics
// low-cardinality (docs/architecture/02-core-reuse.md) regardless of how
// many kinds a pool ends up handling.
package tasks

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrOperational = errors.New("worker operational failure")

type Clock interface {
	Now() time.Time
}

type Timer interface {
	C() <-chan time.Time
	Stop()
}

type TimerFactory interface {
	NewTimer(time.Duration) Timer
}

type ClaimHeartbeatStore interface {
	Claim(context.Context, ClaimRequest) (Lease, bool, error)
	Heartbeat(context.Context, HeartbeatRequest) (time.Time, error)
}

type FailureResolver interface {
	ResolveFailure(context.Context, FailureRequest) (Resolution, error)
}

type Handler interface {
	Handle(context.Context, Lease) error
}

type HandlerFunc func(context.Context, Lease) error

func (f HandlerFunc) Handle(ctx context.Context, lease Lease) error { return f(ctx, lease) }

// HandlerRegistry routes a claimed Lease to the Handler registered for its
// Kind. It is itself a Handler, so it plugs directly into NewRunner: build
// one HandlerRegistry per pool, register each of that pool's kinds against
// its handler, and pass the registry as the Runner's handler.
type HandlerRegistry struct {
	mu       sync.RWMutex
	handlers map[Kind]Handler
}

func NewHandlerRegistry() *HandlerRegistry {
	return &HandlerRegistry{handlers: make(map[Kind]Handler)}
}

func (r *HandlerRegistry) Register(kind Kind, handler Handler) error {
	if !kind.Valid() || handler == nil {
		return ErrInvalidSpec
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.handlers[kind]; exists {
		return ErrDuplicateKind
	}
	r.handlers[kind] = handler
	return nil
}

func (r *HandlerRegistry) Handle(ctx context.Context, lease Lease) error {
	r.mu.RLock()
	handler, ok := r.handlers[lease.Kind]
	r.mu.RUnlock()
	if !ok {
		return ErrUnknownKind
	}
	return handler.Handle(ctx, lease)
}

type Observer interface {
	SetActiveHandlers(string, int)
	ObserveClaim(string, string)
	ObserveHandler(string, string)
	ObserveHeartbeat(string, string)
}

type Runner struct {
	poolName     string
	poolSize     int
	kinds        []Kind
	workerID     string
	policy       Policy
	jitter       JitterSource
	clock        Clock
	tickers      TickerFactory
	timers       TimerFactory
	store        ClaimHeartbeatStore
	resolver     FailureResolver
	handler      Handler
	registry     *Registry
	pollPeriod   time.Duration
	drainTimeout time.Duration
	observer     Observer
}

func (r *Runner) SetObserver(observer Observer) { r.observer = observer }

// poolSize is how many leases this Runner works concurrently — a property
// of the worker pool it belongs to (cmd/emsim/worker.go picks it per pool,
// e.g. more for "short" than for "llm"), not of the shared recovery Policy
// core used PoolSize from.
func NewRunner(
	poolName string,
	poolSize int,
	kinds []Kind,
	workerID string,
	policy Policy,
	jitter JitterSource,
	clock Clock,
	tickers TickerFactory,
	timers TimerFactory,
	store ClaimHeartbeatStore,
	resolver FailureResolver,
	handler Handler,
	registry *Registry,
	pollPeriod time.Duration,
	drainTimeout time.Duration,
) (*Runner, error) {
	if poolName == "" || poolSize < 1 {
		return nil, ErrInvalidPolicy
	}
	identityProbe := ClaimRequest{Kinds: kinds, WorkerID: workerID, Now: time.Unix(1, 0)}
	if identityProbe.Validate() != nil || policy.Validate() != nil || jitter == nil || clock == nil ||
		tickers == nil || timers == nil || store == nil || resolver == nil || handler == nil || registry == nil ||
		pollPeriod <= 0 || drainTimeout <= 0 {
		return nil, ErrInvalidPolicy
	}
	return &Runner{
		poolName: poolName, poolSize: poolSize, kinds: kinds, workerID: workerID, policy: policy, jitter: jitter,
		clock: clock, tickers: tickers, timers: timers, store: store, resolver: resolver, handler: handler,
		registry: registry, pollPeriod: pollPeriod, drainTimeout: drainTimeout,
	}, nil
}

func (r *Runner) Run(ctx context.Context) error {
	poll := r.tickers.NewTicker(r.pollPeriod)
	defer poll.Stop()
	completed := make(chan error, r.poolSize)
	handlerCtx, cancelHandlers := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelHandlers()
	active := 0
	if r.observer != nil {
		r.observer.SetActiveHandlers(r.poolName, active)
	}
	stopping := false
	var operationalErr error
	var drain Timer
	var drainC <-chan time.Time
	ctxDone := ctx.Done()
	beginStopping := func() {
		if stopping {
			return
		}
		stopping = true
		ctxDone = nil
		poll.Stop()
		if active > 0 {
			drain = r.timers.NewTimer(r.drainTimeout)
			drainC = drain.C()
		}
	}
	defer func() {
		if drain != nil {
			drain.Stop()
		}
	}()

	for {
		if stopping && active == 0 {
			return operationalErr
		}
		select {
		case <-ctxDone:
			beginStopping()
		case <-drainC:
			drainC = nil
			cancelHandlers()
		case err := <-completed:
			active--
			if r.observer != nil {
				r.observer.SetActiveHandlers(r.poolName, active)
			}
			if err != nil && operationalErr == nil {
				operationalErr = ErrOperational
				beginStopping()
			}
		case <-poll.C():
			if stopping || ctx.Err() != nil {
				beginStopping()
				continue
			}
			for active < r.poolSize && ctx.Err() == nil {
				now := r.clock.Now()
				lease, found, err := r.store.Claim(ctx, ClaimRequest{
					Kinds: r.kinds, WorkerID: r.workerID, Now: now,
				})
				if err != nil {
					if r.observer != nil {
						r.observer.ObserveClaim(r.poolName, "failed")
					}
					// ctx can be cancelled between this loop's own
					// ctx.Err()==nil check and Claim actually reaching
					// PostgreSQL (shutdown can land in that window,
					// especially for a pool that polls frequently with
					// nothing to claim) — a claim aborted by that
					// cancellation is a graceful shutdown, not an
					// operational failure to report.
					if ctx.Err() == nil {
						operationalErr = ErrOperational
					}
					beginStopping()
					break
				}
				if !found {
					if r.observer != nil {
						r.observer.ObserveClaim(r.poolName, "not_claimed")
					}
					break
				}
				if r.observer != nil {
					r.observer.ObserveClaim(r.poolName, "claimed")
				}
				active++
				if r.observer != nil {
					r.observer.SetActiveHandlers(r.poolName, active)
				}
				go func() { completed <- r.runLease(handlerCtx, lease) }()
			}
		}
	}
}

func (r *Runner) runLease(ctx context.Context, lease Lease) error {
	spec, ok := r.registry.Lookup(lease.Kind)
	if !ok {
		// The kinds this Runner claims all come from the same Registry
		// it was built with, so a claimed lease whose kind is no longer
		// registered means the registry and the database have drifted.
		if r.observer != nil {
			r.observer.ObserveHandler(r.poolName, "failed")
		}
		return ErrOperational
	}
	heartbeatDelay, err := r.policy.HeartbeatDelay(r.jitter)
	if err != nil {
		return ErrOperational
	}
	heartbeat := r.tickers.NewTicker(heartbeatDelay)
	defer heartbeat.Stop()
	handlerCtx, cancelHandler := context.WithCancel(ctx)
	defer cancelHandler()
	handlerDone := make(chan error, 1)
	go func() { handlerDone <- r.handler.Handle(handlerCtx, lease) }()

	for {
		select {
		case handlerErr := <-handlerDone:
			if handlerErr == nil {
				if r.observer != nil {
					r.observer.ObserveHandler(r.poolName, "success")
				}
				return nil
			}
			if ctx.Err() != nil {
				if r.observer != nil {
					r.observer.ObserveHandler(r.poolName, "failed")
				}
				return nil
			}
			if errors.Is(handlerErr, ErrLeaseLost) {
				if r.observer != nil {
					r.observer.ObserveHandler(r.poolName, "lease_lost")
				}
				return nil
			}
			failure, ok := AsHandlerFailure(handlerErr)
			if !ok {
				if r.observer != nil {
					r.observer.ObserveHandler(r.poolName, "failed")
				}
				return ErrOperational
			}
			now := r.clock.Now()
			request := FailureRequest{Lease: lease, Now: now, Failure: failure}
			if failure.Retryability() == Retryable {
				delay, err := r.policy.RetryDelay(spec.RetryBase, lease.Attempt, r.jitter)
				if err != nil {
					return ErrOperational
				}
				request.NextAttemptAt = now.Add(delay)
			}
			if _, err := r.resolver.ResolveFailure(ctx, request); err != nil && !errors.Is(err, ErrLeaseLost) {
				if r.observer != nil {
					r.observer.ObserveHandler(r.poolName, "failed")
				}
				return ErrOperational
			}
			if r.observer != nil {
				r.observer.ObserveHandler(r.poolName, "failed")
			}
			return nil
		case <-heartbeat.C():
			now := r.clock.Now()
			heartbeatCtx, cancel := context.WithTimeout(ctx, r.policy.HeartbeatTimeout)
			_, err := r.store.Heartbeat(heartbeatCtx, HeartbeatRequest{
				Lease: lease, Now: now, LeaseDuration: spec.Lease,
			})
			cancel()
			if err == nil {
				if r.observer != nil {
					r.observer.ObserveHeartbeat(r.poolName, "success")
				}
				continue
			}
			cancelHandler()
			<-handlerDone
			if errors.Is(err, ErrLeaseLost) {
				if r.observer != nil {
					r.observer.ObserveHeartbeat(r.poolName, "lease_lost")
				}
				return nil
			}
			if r.observer != nil {
				r.observer.ObserveHeartbeat(r.poolName, "failed")
			}
			return ErrOperational
		}
	}
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

type systemTimer struct{ timer *time.Timer }

func (t systemTimer) C() <-chan time.Time { return t.timer.C }
func (t systemTimer) Stop()               { t.timer.Stop() }

type SystemTimerFactory struct{}

func (SystemTimerFactory) NewTimer(interval time.Duration) Timer {
	return systemTimer{timer: time.NewTimer(interval)}
}
