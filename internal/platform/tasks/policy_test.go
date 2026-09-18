// Ported from orchestration-core@34290d74656bc594f8968ae17871dc997559b497
// internal/recovery/policy_test.go; adapted: RetryDelay takes a retry base
// duration directly instead of a fixed queue.Kind (Dialogue/Judge), and
// HandlerFailure carries an ErrorCode instead of a domain.SafeFailure.
package tasks

import (
	"errors"
	"testing"
	"time"
)

func TestDefaultPolicyAndBackoff(t *testing.T) {
	policy := DefaultPolicy()
	if err := policy.Validate(); err != nil {
		t.Fatalf("default policy: %v", err)
	}
	tests := []struct {
		retryBase time.Duration
		attempt   int
		want      time.Duration
	}{
		{5 * time.Second, 1, 5 * time.Second},
		{5 * time.Second, 3, 20 * time.Second},
		{5 * time.Second, 20, 5 * time.Minute},
		{2 * time.Second, 1, 2 * time.Second},
		{2 * time.Second, 3, 8 * time.Second},
		{2 * time.Second, 20, 5 * time.Minute},
	}
	for _, test := range tests {
		got, err := policy.RetryDelay(test.retryBase, test.attempt, NoJitter{})
		if err != nil || got != test.want {
			t.Fatalf("delay %v/%d = %v/%v, want %v", test.retryBase, test.attempt, got, err, test.want)
		}
	}
}

func TestRetryDelayRejectsInvalidInputs(t *testing.T) {
	policy := DefaultPolicy()
	if _, err := policy.RetryDelay(0, 1, NoJitter{}); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("zero retry base error = %v", err)
	}
	if _, err := policy.RetryDelay(time.Second, 0, NoJitter{}); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("zero attempt error = %v", err)
	}
	if _, err := policy.RetryDelay(policy.RetryCap+time.Second, 1, NoJitter{}); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("retry base above cap error = %v", err)
	}
}

func TestTypedRetryability(t *testing.T) {
	failure, err := NewHandlerFailure(Retryable, "temporary_unavailable")
	if err != nil {
		t.Fatalf("handler failure: %v", err)
	}
	wrapped := errors.Join(errors.New("handler stopped"), failure)
	got, ok := AsHandlerFailure(wrapped)
	if !ok || got.Retryability() != Retryable || got.Code() != "temporary_unavailable" {
		t.Fatalf("typed failure = %#v/%t", got, ok)
	}
	if _, ok := AsHandlerFailure(errors.New("retry me")); ok {
		t.Fatal("untyped error was classified")
	}
}
