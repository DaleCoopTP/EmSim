package assessment

import (
	"context"
	"time"

	"emsim/internal/platform/tasks"
)

// Coordinator is RFC-001 §7.4's "worker dependency coordinator" for
// assessment.evaluate specifically: a plain ticking loop, not a distinct
// task kind or a LISTEN subscriber (RFC-001 §7.4: "Координатор — цикл
// внутри worker, не отдельный kind/сервис и не LISTEN-подписчик").
// Reuses platform/tasks's own Ticker/TickerFactory rather than declaring
// a duplicate pair, the same generic timer abstraction Reaper already
// uses — pulling in that type carries no table-ownership weight (it is
// not platform/tasks-table-shaped data, just a ticker interface).
type Coordinator struct {
	service  *Service
	tickers  tasks.TickerFactory
	interval time.Duration
	workerID string
	batch    int
}

// NewCoordinator builds a Coordinator ticking every interval, sealing up
// to batch due tasks per tick, identified as workerID in any input it
// fails to prepare (FailWaitingTx's terminal_worker column).
func NewCoordinator(service *Service, tickers tasks.TickerFactory, interval time.Duration, workerID string, batch int) (*Coordinator, error) {
	if service == nil || tickers == nil || interval <= 0 || workerID == "" || batch < 1 {
		return nil, tasks.ErrInvalidPolicy
	}
	return &Coordinator{service: service, tickers: tickers, interval: interval, workerID: workerID, batch: batch}, nil
}

// Run implements tasks.Supervisor.
func (c *Coordinator) Run(ctx context.Context) error {
	ticker := c.tickers.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C():
			if ctx.Err() != nil {
				return nil
			}
			if err := c.service.RunCoordinatorTick(ctx, c.workerID, time.Now().UTC(), c.batch); err != nil {
				// ctx can be cancelled between the check above and this
				// call actually reaching PostgreSQL (SIGTERM can land in
				// that window) — a query aborted by that cancellation is
				// a graceful shutdown, not an operational failure, the
				// same distinction Runner.Run's own poll/claim loop
				// draws for the identical race.
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
		}
	}
}
