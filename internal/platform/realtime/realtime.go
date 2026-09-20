// Package realtime is platform's SSE invalidation bus (ADR-018/RFC-001
// §7.7): a PostgreSQL NOTIFY channel any module's domain transaction can
// publish identifiers to (NotifyTx — a plain SQL call, not a table
// write, so it needs no port of its own), and an in-process Hub that a
// LISTEN loop feeds and any number of HTTP SSE handlers read from. SSE
// is an invalidation layer over PostgreSQL, not a durable delivery
// channel: the buffer is a bounded ring, not a log, and a client that
// falls behind it (or connects with an unknown/stale cursor, or a
// reconnected LISTEN session) is always told to resync and re-read the
// database — never handed a gap silently.
package realtime

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Channel is the one PostgreSQL NOTIFY channel every module publishes
// to and the one Listener subscribes to. A single channel (rather than
// one per module or per lesson) keeps one LISTEN connection sufficient
// for the whole process; Event's own fields are what a subscriber
// filters on.
const Channel = "emsim_events"

// Event is one invalidation — identifiers only (ADR-018: "NOTIFY
// содержит только идентификаторы"), never a domain payload. A nil field
// means "not scoped to this" — e.g. Stop's own notify has no single
// ItemID. Counter/Epoch are filled in by the Hub, not the publisher.
type Event struct {
	Epoch    uint64     `json:"-"`
	Counter  uint64     `json:"-"`
	LessonID *uuid.UUID `json:"lesson_id,omitempty"`
	UserID   *uuid.UUID `json:"user_id,omitempty"`
	ItemID   *uuid.UUID `json:"item_id,omitempty"`
}

// wirePayload is the exact JSON pg_notify carries — Event minus the
// Hub-assigned Epoch/Counter, which a listener elsewhere in a different
// process has no way to agree on anyway (each process's Hub assigns its
// own).
type wirePayload struct {
	LessonID *uuid.UUID `json:"lesson_id,omitempty"`
	UserID   *uuid.UUID `json:"user_id,omitempty"`
	ItemID   *uuid.UUID `json:"item_id,omitempty"`
}

// NotifyTx publishes one Event from inside the caller's own domain
// transaction (CLAUDE.md: "NOTIFY исполняется внутри транзакции
// доменного изменения" — RFC-001 §7.7). It is a plain pg_notify call,
// not a write to any table, so no module "owns" it and no port is
// needed to call it from another module's service.
func NotifyTx(ctx context.Context, tx pgx.Tx, event Event) error {
	payload, err := json.Marshal(wirePayload{LessonID: event.LessonID, UserID: event.UserID, ItemID: event.ItemID})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `SELECT pg_notify($1, $2)`, Channel, string(payload))
	return err
}
