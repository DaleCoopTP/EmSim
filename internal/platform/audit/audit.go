// Package audit is the platform/audit_log module (CLAUDE.md: "platform" is
// one of the six product modules; it owns audit_log — no other module
// writes that table). Every product module records its effects through
// Record, inside the same pgx.Tx as the domain change it describes
// (RFC-001 §9: "Audit: каждый эффект — строка в audit_log в той же
// транзакции"; CLAUDE.md: "Preserve one database transaction where a
// domain change, audit record, notification, and related background-task
// enqueue must be atomic").
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	// ErrInvalidEntry is returned by Record when the entry fails shape
	// validation before any SQL runs.
	ErrInvalidEntry = errors.New("invalid audit entry")
	// ErrStorage is returned by Record for a database failure unrelated to
	// the entry's own shape.
	ErrStorage = errors.New("audit storage failure")
)

// Outcome is the audit_log.outcome state (migrations/00002_platform_audit_log.sql).
type Outcome string

const (
	OutcomeOK       Outcome = "ok"
	OutcomeRejected Outcome = "rejected"
	OutcomeError    Outcome = "error"
)

func (o Outcome) valid() bool {
	switch o {
	case OutcomeOK, OutcomeRejected, OutcomeError:
		return true
	default:
		return false
	}
}

// action is dot-namespaced, e.g. "auth.login", "admin.user.create" — the
// same shape as tasks.Kind (internal/platform/tasks/queue.go), reused here
// independently since audit and tasks have no reason to share a type.
var actionPattern = regexp.MustCompile(`^[a-z]+(\.[a-z_]+)+$`)

// resource_type is a single lowercase word, e.g. "user", "workstation",
// "session", "lesson", "item".
var resourceTypePattern = regexp.MustCompile(`^[a-z][a-z_]*$`)

// forbiddenDetailKeys names the Details keys RFC-001 §9 and
// migrations/00002_platform_audit_log.sql's own comment rule out —
// "никогда: пароли, тексты обучаемых, ФИО" ("never: passwords, trainee
// texts, full names") — checked case-insensitively so a caller cannot slip
// past it with "Password" or "Login". This is a floor, not the full
// policy: a module must still choose what belongs in Details at all.
var forbiddenDetailKeys = map[string]struct{}{
	"password":  {},
	"full_name": {},
	"login":     {},
}

// Entry is one audit_log row. ActorID is nil for a system action (no
// human actor); ResourceID is nil when the action has no single resource
// (e.g. a list query is never audited, but a bulk replace might record
// none). Details must contain no secrets and none of forbiddenDetailKeys —
// Record rejects an entry that does before writing anything.
type Entry struct {
	ActorID      *uuid.UUID
	ActorRole    string
	Action       string
	ResourceType string
	ResourceID   *uuid.UUID
	Outcome      Outcome
	RequestID    string
	Details      map[string]any
}

func (e Entry) validate() error {
	if !actionPattern.MatchString(e.Action) {
		return ErrInvalidEntry
	}
	if !resourceTypePattern.MatchString(e.ResourceType) {
		return ErrInvalidEntry
	}
	if !e.Outcome.valid() {
		return ErrInvalidEntry
	}
	for key := range e.Details {
		if _, forbidden := forbiddenDetailKeys[lowercase(key)]; forbidden {
			return ErrInvalidEntry
		}
	}
	return nil
}

func lowercase(s string) string {
	out := []byte(s)
	for i, b := range out {
		if b >= 'A' && b <= 'Z' {
			out[i] = b + ('a' - 'A')
		}
	}
	return string(out)
}

// Record validates e and inserts it as one audit_log row using tx — the
// caller's own transaction, which owns commit/rollback (CLAUDE.md: "Use
// pgx.Tx for atomic domain and queue operations. The caller owns commit
// and rollback."). It never opens its own transaction: an audit row with
// no matching committed domain effect would misrepresent what happened.
func Record(ctx context.Context, tx pgx.Tx, e Entry) error {
	if tx == nil {
		return ErrInvalidEntry
	}
	if err := e.validate(); err != nil {
		return err
	}

	details := e.Details
	if details == nil {
		details = map[string]any{}
	}
	detailsJSON, err := json.Marshal(details)
	if err != nil {
		return ErrInvalidEntry
	}

	var actorRole *string
	if e.ActorRole != "" {
		actorRole = &e.ActorRole
	}
	var requestID *string
	if e.RequestID != "" {
		requestID = &e.RequestID
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO audit_log (actor_id, actor_role, action, resource_type, resource_id, outcome, request_id, details)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, e.ActorID, actorRole, e.Action, e.ResourceType, e.ResourceID, string(e.Outcome), requestID, detailsJSON)
	if err != nil {
		return ErrStorage
	}
	return nil
}
