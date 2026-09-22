package assessment

import (
	"context"
	"encoding/json"
	"time"

	"emsim/internal/content"
	"emsim/internal/platform/tasks"
	"emsim/internal/training"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ItemReader is assessment's read of training's own items (CLAUDE.md:
// "небольшие интерфейсы-порты объявляются со стороны потребителя" —
// assessment never writes items, only reads under whatever lock its own
// transaction needs). The method signature matches *trainingpg.Store's
// own ItemByID exactly, so that concrete type satisfies this
// structurally — the same pattern internal/training/ports.go's own
// ScenarioReader/ServiceReader already use for content.
type ItemReader interface {
	ItemByID(ctx context.Context, tx pgx.Tx, id uuid.UUID, lock training.Lock) (training.Item, error)
}

// EvidenceReader is assessment's read of training's sealed evidence — the
// RuleEvaluator's own input, decoded and digest-checked against the
// value assessment_inputs.body already recorded (ADR-006's
// reproducibility guarantee).
type EvidenceReader interface {
	EvidenceByItem(ctx context.Context, tx pgx.Tx, itemID uuid.UUID) (training.EvidenceBody, [32]byte, error)
	EvidenceDocumentByItem(ctx context.Context, tx pgx.Tx, itemID uuid.UUID) (json.RawMessage, [32]byte, error)
}

// ScenarioReader is assessment's read of content's approved scenario
// version — the reference (primary_decision, call, field_corrections,
// ...) and reference.scoring the coordinator merges into the item's
// effective rubric (ADR-013). Method name/signature matches
// *contentpg.Store's own VersionByID exactly.
type ScenarioReader interface {
	VersionByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (content.ScenarioVersionRecord, error)
}

// TaskStore is assessment's own use of platform's technical queue
// (CLAUDE.md: "platform/tasks — a module writes only its own tables";
// assessment only ever calls these, never writes a tasks row itself).
// Every method matches *tasks.Store's own signature exactly, so that
// concrete type satisfies this structurally.
type TaskStore interface {
	WaitingDue(ctx context.Context, kind tasks.Kind, now time.Time, limit int) ([]tasks.WaitingTask, error)
	PromoteWaitingTx(ctx context.Context, tx pgx.Tx, taskID uuid.UUID, payload []byte, nextAttemptAt time.Time) error
	FailWaitingTx(ctx context.Context, tx pgx.Tx, taskID uuid.UUID, workerID string, code tasks.ErrorCode) error
	ByDedupKey(ctx context.Context, tx pgx.Tx, dedupKey string) (tasks.TaskSummary, error)
	// PeekPayload reads a task's scope_id/payload without any lock —
	// FinalizeExpired's own way to discover an exhausted task's item_id
	// before it acquires that item's own domain lock (RFC-001 §8: domain
	// locks come before the task row's).
	PeekPayload(ctx context.Context, taskID uuid.UUID) (scopeID *uuid.UUID, payload []byte, err error)
	CancelTx(ctx context.Context, tx pgx.Tx, request tasks.CancelRequest) (bool, error)
	Terminal(ctx context.Context, tx pgx.Tx, request tasks.TerminalRequest) (tasks.TerminalResult, error)
	// FinalizeExpiredTx is platform/tasks's own exhaustion write, exposed
	// for a Finalizer implementation (this package's own Service) to call
	// from inside its domain-first transaction — see Finalizer's doc
	// comment in internal/platform/tasks/finalizer.go for the lock-order
	// reasoning this method's fencing depends on.
	FinalizeExpiredTx(ctx context.Context, tx pgx.Tx, taskID uuid.UUID, workerID string, token uint64, status tasks.TaskStatus, code tasks.ErrorCode) (bool, error)
}
