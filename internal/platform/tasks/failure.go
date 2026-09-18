// Ported from orchestration-core@34290d74656bc594f8968ae17871dc997559b497
// internal/recovery/failure.go and (the ErrorCode shape) internal/domain/
// failure.go; adapted: domain.SafeFailure{stage, code} loses its stage —
// migrations/00001_platform_tasks.sql has only last_error_code, no stage
// column (docs/technical-discovery.md §3.5 keeps the "safe, whitelisted
// error code" idea but drops the stage enum, which was specific to core's
// dialogue/judge/finalization pipeline). FailureDigest is dropped: the
// platform tasks table has no terminal_digest column, so terminal replay
// is classified without a digest (see queue.go).
package tasks

import (
	"errors"
)

var ErrInvalidRequest = errors.New("invalid task request")

// ErrorCode is a safe, whitelisted failure code: never free text, so a
// failure can be logged and reported without risking a leak of request
// payloads, prompts, or other unsafe content into logs or metrics.
type ErrorCode string

func (c ErrorCode) Valid() bool {
	if len(c) == 0 || len(c) > 64 {
		return false
	}
	for _, char := range c {
		if !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '_') {
			return false
		}
	}
	return true
}

type Retryability string

const (
	Retryable Retryability = "retryable"
	Permanent Retryability = "permanent"
)

// HandlerFailure is what a task Handler returns to signal a classified
// failure (as opposed to an unexpected error, which the Runner treats as
// an operational failure and stops on). Only errors.As-matched
// *HandlerFailure values reach the recovery resolver.
type HandlerFailure struct {
	retryability Retryability
	code         ErrorCode
}

func NewHandlerFailure(retryability Retryability, code ErrorCode) (*HandlerFailure, error) {
	if retryability != Retryable && retryability != Permanent || !code.Valid() {
		return nil, ErrInvalidRequest
	}
	return &HandlerFailure{retryability: retryability, code: code}, nil
}

func (f *HandlerFailure) Error() string {
	if f == nil {
		return "task handler failure"
	}
	return string(f.retryability) + " task handler failure"
}

func (f *HandlerFailure) Retryability() Retryability {
	if f == nil {
		return ""
	}
	return f.retryability
}

func (f *HandlerFailure) Code() ErrorCode {
	if f == nil {
		return ""
	}
	return f.code
}

func AsHandlerFailure(err error) (*HandlerFailure, bool) {
	var failure *HandlerFailure
	ok := errors.As(err, &failure)
	return failure, ok
}
