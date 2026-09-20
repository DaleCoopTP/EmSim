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
	Evidence(item Item, actions []Action, cutoffLogSeq int64, closedAt time.Time) (Evidence, error)
}

// Action is one actions row as Evidence needs it: already restricted to
// [1, cutoffLogSeq] and sorted by LogSeq ascending. Producing that slice
// requires reading the actions table, which only the application service
// does — Action itself is a plain value, not a query result.
type Action struct {
	ID        uuid.UUID
	Seq       int64
	LogSeq    int64
	ActorID   uuid.UUID
	Type      CommandType
	Payload   json.RawMessage
	Effect    map[string]any
	Accepted  bool
	Rejection Rejection
	ServerAt  time.Time
	// ReactionAfter is the item's reaction immediately after this action
	// was decided — "" for a rejected action or one that does not change
	// reaction (open, add_comment, set_card_field).
	ReactionAfter content.Reaction
}
