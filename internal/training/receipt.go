package training

import (
	"time"

	"emsim/internal/content"

	"github.com/google/uuid"
)

// Outcome is Receipt.Outcome — ADR-004's "исходный outcome=applied|
// rejected и HTTP-статус сохраняются и при replay".
type Outcome string

const (
	OutcomeApplied  Outcome = "applied"
	OutcomeRejected Outcome = "rejected"
)

// Receipt is openapi.yaml's Receipt schema, field for field — what
// POST /items/{id}/actions returns, and what actions.receipt stores
// verbatim for replay (ADR-004 §7.1: a replay returns this same value
// with only Replayed flipped to true, never a recomputed one). json tags
// let the HTTP layer (slice 3's C5) marshal a Receipt directly.
type Receipt struct {
	CommandID  uuid.UUID        `json:"command_id"`
	Outcome    Outcome          `json:"outcome"`
	Seq        int64            `json:"seq"`
	Reaction   content.Reaction `json:"reaction"`
	ItemState  ItemState        `json:"item_state"`
	ServerAt   time.Time        `json:"server_at"`
	Deadlines  *Deadlines       `json:"deadlines,omitempty"`
	CallID     *uuid.UUID       `json:"call_id,omitempty"`      // not used until slice 5
	NextItemID *uuid.UUID       `json:"next_item_id,omitempty"` // set after a close that issues the next queued item
	ErrorCode  *Rejection       `json:"error_code,omitempty"`
	Replayed   bool             `json:"replayed"`
	ActionID   uuid.UUID        `json:"action_id"`
	LogSeq     int64            `json:"log_seq"`
}
