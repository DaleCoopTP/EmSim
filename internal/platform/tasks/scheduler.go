package tasks

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var ErrInvalidScheduler = errors.New("invalid task scheduler")

// Schedule is one daily task (ADR-033): once the local time of day At
// has passed in the Scheduler's location, the Scheduler enqueues Kind for
// that day. The dedup key "<DedupPrefix>:daily:<YYYY-MM-DD>" makes the
// enqueue idempotent across ticks, restarts and several maintenance
// processes (tasks.dedup_key is unique). A day the process never ran
// after At is not caught up later.
type Schedule struct {
	Kind        Kind
	At          time.Duration // offset from local midnight, [0, 24h)
	DedupPrefix string        // defaults to Kind
	Payload     []byte
}

// ScheduleEnqueuer is what Scheduler needs from the queue; *Store
// satisfies it.
type ScheduleEnqueuer interface {
	Enqueue(context.Context, EnqueueRequest) (uuid.UUID, bool, error)
}

// Scheduler is a maintenance Supervisor that enqueues daily tasks. It
// never stops its process over a failed enqueue: OnError is told and the
// next tick tries again.
type Scheduler struct {
	schedules []Schedule
	location  *time.Location
	interval  time.Duration
	clock     Clock
	tickers   TickerFactory
	store     ScheduleEnqueuer
	onError   func(context.Context, Kind, error)
	// enqueued remembers the last local day each schedule was enqueued
	// for, so an ordinary tick does not touch the database at all.
	enqueued map[int]string
}

func NewScheduler(
	schedules []Schedule, location *time.Location, interval time.Duration,
	clock Clock, tickers TickerFactory, store ScheduleEnqueuer, onError func(context.Context, Kind, error),
) (*Scheduler, error) {
	if location == nil || interval <= 0 || clock == nil || tickers == nil || store == nil {
		return nil, ErrInvalidScheduler
	}
	for i := range schedules {
		if !schedules[i].Kind.Valid() || schedules[i].At < 0 || schedules[i].At >= 24*time.Hour {
			return nil, ErrInvalidScheduler
		}
		if schedules[i].DedupPrefix == "" {
			schedules[i].DedupPrefix = string(schedules[i].Kind)
		}
	}
	if onError == nil {
		onError = func(context.Context, Kind, error) {}
	}
	return &Scheduler{
		schedules: schedules, location: location, interval: interval, clock: clock,
		tickers: tickers, store: store, onError: onError, enqueued: map[int]string{},
	}, nil
}

func (s *Scheduler) Run(ctx context.Context) error {
	ticker := s.tickers.NewTicker(s.interval)
	defer ticker.Stop()
	s.Tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C():
			s.Tick(ctx)
		}
	}
}

// Tick enqueues every schedule whose slot for the current local day has
// passed and has not been enqueued yet.
func (s *Scheduler) Tick(ctx context.Context) {
	now := s.clock.Now().In(s.location)
	year, month, day := now.Date()
	midnight := time.Date(year, month, day, 0, 0, 0, 0, s.location)
	date := now.Format("2006-01-02")
	for i, schedule := range s.schedules {
		if now.Before(midnight.Add(schedule.At)) || s.enqueued[i] == date {
			continue
		}
		_, _, err := s.store.Enqueue(ctx, EnqueueRequest{
			TaskID: uuid.New(), Kind: schedule.Kind, ScopeType: "system",
			DedupKey: fmt.Sprintf("%s:daily:%s", schedule.DedupPrefix, date),
			Payload:  schedule.Payload, NextAttemptAt: now.UTC(),
		})
		if err != nil {
			s.onError(ctx, schedule.Kind, err)
			continue
		}
		s.enqueued[i] = date
	}
}

// ParseTimeOfDay parses "HH:MM" into an offset from midnight.
func ParseTimeOfDay(value string) (time.Duration, error) {
	parsed, err := time.Parse("15:04", value)
	if err != nil {
		return 0, ErrInvalidScheduler
	}
	return time.Duration(parsed.Hour())*time.Hour + time.Duration(parsed.Minute())*time.Minute, nil
}
