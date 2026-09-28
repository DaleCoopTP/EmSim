// Ported from orchestration-core@34290d74656bc594f8968ae17871dc997559b497
// internal/recovery/supervisor.go; as-is except renamed (Supervisor ->
// Reaper, SupervisorTicker(Factory) -> Ticker(Factory)): core's queue,
// worker, and recovery were three packages, each free to define its own
// Ticker/TickerFactory pair; this port folds them into one tasks package
// (ADR-010), so Ticker/TickerFactory are defined once here and reused by
// Runner in the next commit instead of being redeclared per file.
package tasks

import (
	"context"
	"errors"
	"time"
)

var ErrReaperOperational = errors.New("reaper operational failure")

type Ticker interface {
	C() <-chan time.Time
	Stop()
}

type TickerFactory interface {
	NewTicker(time.Duration) Ticker
}

// ReaperStore is what Reaper needs from storage: *Recovery satisfies it.
type ReaperStore interface {
	ReapExpired(context.Context) (ReapSummary, error)
}

// Reaper periodically reclaims tasks whose lease expired without a
// heartbeat — the worker holding them is presumed dead, partitioned, or
// killed mid-handler.
type Reaper struct {
	policy  Policy
	tickers TickerFactory
	store   ReaperStore
}

func NewReaper(policy Policy, tickers TickerFactory, store ReaperStore) (*Reaper, error) {
	if policy.Validate() != nil || tickers == nil || store == nil {
		return nil, ErrInvalidPolicy
	}
	return &Reaper{policy: policy, tickers: tickers, store: store}, nil
}

func (r *Reaper) Run(ctx context.Context) error {
	ticker := r.tickers.NewTicker(r.policy.ReaperInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C():
			if ctx.Err() != nil {
				return nil
			}
			if _, err := r.store.ReapExpired(ctx); err != nil {
				// A stop that lands mid-reap cancels the query; that is a
				// graceful shutdown, not an operational failure.
				if ctx.Err() != nil {
					return nil
				}
				return ErrReaperOperational
			}
		}
	}
}

type systemTicker struct{ ticker *time.Ticker }

func (t systemTicker) C() <-chan time.Time { return t.ticker.C }
func (t systemTicker) Stop()               { t.ticker.Stop() }

type SystemTickerFactory struct{}

func (SystemTickerFactory) NewTicker(interval time.Duration) Ticker {
	return systemTicker{ticker: time.NewTicker(interval)}
}
