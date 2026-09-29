// Package maintenance is the platform's maintenance-mode switch (ADR-038):
// while it is on, no new lesson can be started, so an administrator can
// update, restore or move the installation without new work arriving.
// Running lessons are untouched. It owns platform_maintenance (one row).
//
// The switch and every start check meet on that one row: Set takes it FOR
// UPDATE, a start takes it FOR SHARE inside its own transaction (ActiveTx).
// A start therefore either commits before the switch flips — and the
// lesson simply keeps running — or sees the switch on and is refused; it
// can never slip through half-way.
package maintenance

import (
	"context"
	"errors"
	"strings"
	"time"

	"emsim/internal/platform/audit"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrInvalid = errors.New("invalid maintenance request")
	ErrStorage = errors.New("maintenance storage failure")
)

// MaxReasonRunes bounds the note shown to everyone while the mode is on.
const MaxReasonRunes = 200

// State is the switch as everyone sees it.
type State struct {
	Enabled bool
	Reason  string
	SetAt   time.Time
}

// Querier is the part of a pool or transaction Get needs.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Get reads the switch without locking it (the status screen, the banner).
func Get(ctx context.Context, db Querier) (State, error) {
	var s State
	if err := db.QueryRow(ctx, `SELECT enabled, reason, set_at FROM platform_maintenance WHERE id`).Scan(&s.Enabled, &s.Reason, &s.SetAt); err != nil {
		return State{}, ErrStorage
	}
	return s, nil
}

// Gate is the consumer-side check a lesson start makes; the zero value is
// ready to use.
type Gate struct{}

// ActiveTx reports whether maintenance mode is on, holding the row FOR
// SHARE until tx ends.
func (Gate) ActiveTx(ctx context.Context, tx pgx.Tx) (bool, error) {
	var enabled bool
	if err := tx.QueryRow(ctx, `SELECT enabled FROM platform_maintenance WHERE id FOR SHARE`).Scan(&enabled); err != nil {
		return false, ErrStorage
	}
	return enabled, nil
}

// SetTx switches the mode and audits the change in the caller's own
// transaction. Setting the state it already has is allowed and audited
// again (it updates the reason).
func SetTx(ctx context.Context, tx pgx.Tx, enabled bool, reason string, actorID uuid.UUID, actorRole, requestID string) (State, error) {
	reason = strings.TrimSpace(reason)
	if len([]rune(reason)) > MaxReasonRunes {
		return State{}, ErrInvalid
	}
	if !enabled {
		reason = ""
	}
	var s State
	// The row lock waits for every start currently holding it FOR SHARE.
	if err := tx.QueryRow(ctx, `
		UPDATE platform_maintenance SET enabled = $1, reason = $2, set_by = $3, set_at = clock_timestamp()
		WHERE id RETURNING enabled, reason, set_at`, enabled, reason, actorID).Scan(&s.Enabled, &s.Reason, &s.SetAt); err != nil {
		return State{}, ErrStorage
	}
	details := map[string]any{"enabled": enabled}
	if reason != "" {
		details["reason"] = reason
	}
	if err := audit.Record(ctx, tx, audit.Entry{
		ActorID: &actorID, ActorRole: actorRole, Action: "admin.maintenance.set", ResourceType: "maintenance",
		Outcome: audit.OutcomeOK, RequestID: requestID, Details: details,
	}); err != nil {
		return State{}, ErrStorage
	}
	return s, nil
}
