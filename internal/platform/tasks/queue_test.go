// Ported from orchestration-core@34290d74656bc594f8968ae17871dc997559b497
// internal/queue/queue_test.go; adapted for the new Kind/Lease/
// TerminalOutcome shapes; added coverage for EnqueueRequest/CancelRequest,
// which core did not have.
package tasks

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRequestsRejectInvalidLeaseInputs(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	if err := (ClaimRequest{Kinds: []Kind{"bad kind"}, WorkerID: "worker", Now: now}).Validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid kind error = %v", err)
	}
	if err := (ClaimRequest{WorkerID: "worker", Now: now}).Validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("empty kinds error = %v", err)
	}
	lease := Lease{TaskID: uuid.New(), WorkerID: "worker", Token: 1}
	if err := (HeartbeatRequest{Lease: lease, Now: now, LeaseDuration: 0}).Validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid heartbeat error = %v", err)
	}
}

func TestTerminalOutcomesValidateErrorCode(t *testing.T) {
	if _, err := Failed("invalid_response", nil); err != nil {
		t.Fatalf("valid failed outcome: %v", err)
	}
	if err := (TerminalOutcome{Status: TaskFailed}).Validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("missing error code error = %v", err)
	}
	if err := (TerminalOutcome{Status: TaskDone, Code: "unexpected"}).Validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("done outcome carrying a code error = %v", err)
	}
	if err := (TerminalOutcome{Status: TaskCancelled}).Validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("non-handler terminal status error = %v", err)
	}
}

func TestTerminalConflictErrorIsTypedAndSentinelCompatible(t *testing.T) {
	err := error(&TerminalConflictError{})
	if !errors.Is(err, ErrTerminalConflict) {
		t.Fatalf("errors.Is conflict = false")
	}
	var conflict *TerminalConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("errors.As conflict = false")
	}
}

func TestEnqueueRequestValidatesScopeAndDedupKey(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	valid := EnqueueRequest{
		TaskID: uuid.New(), Kind: "system.noop", ScopeType: "system",
		DedupKey: "system.noop:1", NextAttemptAt: now,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid enqueue request: %v", err)
	}

	badScope := valid
	badScope.ScopeType = "unknown"
	if err := badScope.Validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unknown scope type error = %v", err)
	}

	blankDedup := valid
	blankDedup.DedupKey = "   "
	if err := blankDedup.Validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("blank dedup key error = %v", err)
	}

	badKind := valid
	badKind.Kind = "noop"
	if err := badKind.Validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("undotted kind error = %v", err)
	}
}

func TestCancelRequestRequiresTaskIDAndNow(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	if err := (CancelRequest{TaskID: uuid.New(), Now: now}).Validate(); err != nil {
		t.Fatalf("valid cancel request: %v", err)
	}
	if err := (CancelRequest{Now: now}).Validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("missing task id error = %v", err)
	}
	if err := (CancelRequest{TaskID: uuid.New()}).Validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("missing now error = %v", err)
	}
}
