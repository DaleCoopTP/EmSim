// Ported from orchestration-core@34290d74656bc594f8968ae17871dc997559b497
// internal/recovery/policy.go; adapted: TaskLease and the Dialogue/Judge
// retry bases are removed — lease and retry base are now per-kind
// (Spec.Lease, Spec.RetryBase in kinds.go), looked up from the Registry
// instead of living on Policy. PoolSize is likewise gone: pool size is a
// property of a worker pool (cmd/emsim/worker.go), not the shared recovery
// policy. RetryDelay takes a retry base directly instead of a fixed Kind
// enum.
package tasks

import (
	"errors"
	"time"
)

var ErrInvalidPolicy = errors.New("invalid recovery policy")

type Policy struct {
	HeartbeatInterval time.Duration
	HeartbeatJitter   float64
	HeartbeatTimeout  time.Duration
	SafetyMargin      time.Duration
	ReaperInterval    time.Duration
	ReclaimGrace      time.Duration
	ReaperBatch       int
	RetryCap          time.Duration
}

func DefaultPolicy() Policy {
	return Policy{
		HeartbeatInterval: 30 * time.Second,
		HeartbeatJitter:   0.10,
		HeartbeatTimeout:  5 * time.Second,
		SafetyMargin:      15 * time.Second,
		ReaperInterval:    15 * time.Second,
		ReclaimGrace:      10 * time.Second,
		ReaperBatch:       32,
		RetryCap:          5 * time.Minute,
	}
}

func (p Policy) Validate() error {
	if p.HeartbeatInterval <= 0 || p.HeartbeatJitter < 0 || p.HeartbeatJitter > 0.10 ||
		p.HeartbeatTimeout <= 0 || p.HeartbeatTimeout >= p.HeartbeatInterval || p.SafetyMargin <= 0 ||
		p.ReaperInterval <= 0 || p.ReclaimGrace < 0 || p.ReaperBatch < 1 || p.ReaperBatch > 32 ||
		p.RetryCap <= 0 {
		return ErrInvalidPolicy
	}
	return nil
}

// minimumLease is the shortest lease a kind can safely declare: a lease at
// or below the worst-case heartbeat interval (plus jitter and the safety
// margin) could expire between two heartbeats and let the reaper reclaim a
// task that is still being worked on.
func (p Policy) minimumLease() time.Duration {
	return p.HeartbeatInterval + time.Duration(float64(p.HeartbeatInterval)*p.HeartbeatJitter) + p.SafetyMargin
}

type JitterSource interface {
	Apply(time.Duration, float64) time.Duration
}

type NoJitter struct{}

func (NoJitter) Apply(value time.Duration, _ float64) time.Duration { return value }

// RetryDelay computes the backoff before the next attempt: retryBase,
// doubled once per prior attempt, capped at RetryCap, then jittered.
// retryBase comes from the failing task's registered Spec.RetryBase.
func (p Policy) RetryDelay(retryBase time.Duration, attempt int, jitter JitterSource) (time.Duration, error) {
	if err := p.Validate(); err != nil || retryBase <= 0 || retryBase > p.RetryCap || attempt < 1 || jitter == nil {
		return 0, ErrInvalidPolicy
	}
	delay := retryBase
	for current := 1; current < attempt && delay < p.RetryCap; current++ {
		if delay > p.RetryCap/2 {
			delay = p.RetryCap
			break
		}
		delay *= 2
	}
	if delay > p.RetryCap {
		delay = p.RetryCap
	}
	delay = jitter.Apply(delay, p.HeartbeatJitter)
	if delay < 0 {
		return 0, ErrInvalidPolicy
	}
	if delay > p.RetryCap {
		delay = p.RetryCap
	}
	return delay, nil
}

func (p Policy) HeartbeatDelay(jitter JitterSource) (time.Duration, error) {
	if err := p.Validate(); err != nil || jitter == nil {
		return 0, ErrInvalidPolicy
	}
	delay := jitter.Apply(p.HeartbeatInterval, p.HeartbeatJitter)
	minimum := time.Duration(float64(p.HeartbeatInterval) * (1 - p.HeartbeatJitter))
	maximum := time.Duration(float64(p.HeartbeatInterval) * (1 + p.HeartbeatJitter))
	if delay < minimum || delay > maximum {
		return 0, ErrInvalidPolicy
	}
	return delay, nil
}
