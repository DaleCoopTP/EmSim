// Ported from orchestration-core@34290d74656bc594f8968ae17871dc997559b497
// internal/recovery/lifecycle.go; as-is except ErrStorage, which is
// declared once in queue.go for the whole package instead of being
// redeclared per ported source file.
package tasks

import (
	"time"
)

type FailureRequest struct {
	Lease         Lease
	Now           time.Time
	Failure       *HandlerFailure
	NextAttemptAt time.Time
}

func (r FailureRequest) Validate() error {
	if r.Lease.ValidateIdentity() != nil || r.Now.IsZero() || r.Failure == nil {
		return ErrInvalidRequest
	}
	if r.Failure.retryability != Retryable && r.Failure.retryability != Permanent || !r.Failure.code.Valid() {
		return ErrInvalidRequest
	}
	if r.Failure.retryability == Retryable && !r.NextAttemptAt.After(r.Now) {
		return ErrInvalidRequest
	}
	return nil
}

type Resolution string

const (
	ResolutionRequeued   Resolution = "requeued"
	ResolutionFailed     Resolution = "failed"
	ResolutionDeadLetter Resolution = "dead_letter"
)

type ReapSummary struct {
	Requeued   int
	DeadLetter int
}

func (s ReapSummary) Count() int { return s.Requeued + s.DeadLetter }
