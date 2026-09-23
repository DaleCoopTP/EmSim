package training

import (
	"encoding/json"
	"time"

	"emsim/internal/content"

	"github.com/google/uuid"
)

// Exercise decides one command's effect for one exercise_type's process
// (ADR-015) and assembles the immutable evidence RFC-001 §6/§7.4
// requires at close. Both methods are pure: no PostgreSQL, no HTTP, no
// wall-clock reads beyond the `now`/`closedAt` they are given, so rule
// tests are deterministic and the caller controls exactly which clock
// reading is also used for actions.server_at/evidence.closed_at.
//
// internal/training/dds is the one implementation this slice supports;
// selecting an implementation by exercise_type is the application
// service's job (slice 3's C4), not this package's — Exercise itself
// says nothing about registration, so training never has to import dds
// and risk an import cycle (dds necessarily imports training for these
// very types).
type Exercise interface {
	Decide(item Item, cmd Command, now time.Time) (Decision, error)
	// Evidence assembles the immutable close-time snapshot. events is the
	// item's own item_events rows (any state — scheduled ones only occur
	// here if the caller has a bug, since close/stop cancel them first),
	// already the application service's job to load; Evidence itself
	// stays pure and does not touch the database.
	Evidence(item Item, actions []Action, events []ItemEvent, cutoffLogSeq int64, closedAt time.Time) (Evidence, error)
}

// IntakeQuestionProjector is an optional read-only projection for exercises
// with a caller dialogue. It receives the immutable scenario already loaded
// by Service and returns only questions currently safe to show a trainee.
type IntakeQuestionProjector interface {
	AvailableQuestions(item Item) []IntakeQuestionOption
}

// Action is one actions row — both what Evidence needs (already
// restricted to [1, cutoffLogSeq] and sorted by LogSeq ascending, a
// filter and order only the application service can produce, since only
// it reads the actions table) and the full storage/replay shape the
// application service's Store persists and looks up by CommandID
// (ADR-004 §7.1). ItemID/RequestDigest/CommandID/Receipt/HTTPStatus/
// ClientAt are only meaningful for the latter use, not for Evidence.
type Action struct {
	ID            uuid.UUID
	ItemID        uuid.UUID
	Seq           int64
	LogSeq        int64
	ActorID       uuid.UUID
	RequestDigest [32]byte
	CommandID     uuid.UUID
	Type          CommandType
	Payload       json.RawMessage
	Effect        map[string]any
	Accepted      bool
	Rejection     Rejection
	// Receipt is the exact quittance returned to the client on this
	// action's first (non-replay) response — ADR-004: a replay must
	// return this unchanged, not a recomputed one.
	Receipt    Receipt
	HTTPStatus int
	ClientAt   *time.Time
	ServerAt   time.Time
	// ReactionAfter is the item's reaction immediately after this action
	// was decided — "" for a rejected action or one that does not change
	// reaction (open, add_comment, set_card_field).
	ReactionAfter content.Reaction
}
