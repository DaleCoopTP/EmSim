// Ported from orchestration-core@34290d74656bc594f8968ae17871dc997559b497
// internal/observability/sampler.go; adapted: Snapshot drops Runs (no runs
// table was ported, see docs/technical-discovery.md §3.5) — it now reports
// only the tasks queue gauge.
package observability

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

var ErrInvalidSampler = errors.New("invalid queue sampler")

type TaskCount struct {
	Kind, Status string
	Count        int
}

type Snapshot struct {
	Tasks []TaskCount
}

type SnapshotReader interface {
	Sample(context.Context) (Snapshot, error)
}
type Ticker interface {
	C() <-chan time.Time
	Stop()
}
type TickerFactory interface{ NewTicker(time.Duration) Ticker }

type Sampler struct {
	reader            SnapshotReader
	metrics           *Metrics
	logger            Logger
	interval, timeout time.Duration
	tickers           TickerFactory
	mu                sync.Mutex
	previousTasks     map[string]TaskCount
}

func NewSampler(reader SnapshotReader, metrics *Metrics, logger Logger, interval, timeout time.Duration, tickers TickerFactory) (*Sampler, error) {
	if reader == nil || metrics == nil || interval <= 0 || timeout <= 0 || tickers == nil {
		return nil, ErrInvalidSampler
	}
	return &Sampler{reader: reader, metrics: metrics, logger: logger, interval: interval, timeout: timeout, tickers: tickers, previousTasks: map[string]TaskCount{}}, nil
}

func (s *Sampler) Run(ctx context.Context) error {
	ticker := s.tickers.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C():
			s.sample(ctx)
		}
	}
}

func (s *Sampler) sample(ctx context.Context) {
	iteration, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	snapshot, err := s.reader.Sample(iteration)
	if err != nil {
		s.metrics.ObserveSampler("failed")
		s.logger.Operation(ctx, slog.LevelWarn, "queue_sampler", "failed", "", "database_unavailable")
		return
	}
	s.metrics.ObserveSampler("success")
	s.mu.Lock()
	defer s.mu.Unlock()
	currentTasks := make(map[string]TaskCount, len(snapshot.Tasks))
	for _, count := range snapshot.Tasks {
		if !validKind(count.Kind) || !validTaskStatus(count.Status) || count.Count < 0 {
			continue
		}
		key := count.Kind + "\x00" + count.Status
		currentTasks[key] = count
		s.metrics.SetQueueTasks(count.Kind, count.Status, count.Count)
	}
	for key, previous := range s.previousTasks {
		if _, present := currentTasks[key]; !present {
			s.metrics.SetQueueTasks(previous.Kind, previous.Status, 0)
		}
	}
	s.previousTasks = currentTasks
}

type SystemTickerFactory struct{}
type systemTicker struct{ ticker *time.Ticker }

func (SystemTickerFactory) NewTicker(interval time.Duration) Ticker {
	return systemTicker{ticker: time.NewTicker(interval)}
}
func (t systemTicker) C() <-chan time.Time { return t.ticker.C }
func (t systemTicker) Stop()               { t.ticker.Stop() }
