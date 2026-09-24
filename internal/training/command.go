package training

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"emsim/internal/content"

	"github.com/google/uuid"
)

// Command is one POST /items/{id}/actions request, decoded down to what
// Exercise.Decide needs — ADR-004's {command_id,expected_seq,type,
// payload,client_at}. Payload stays raw JSON here rather than a typed
// per-command-type struct: RequestDigest needs the client's exact bytes
// (ADR-004 "клиент хранит исходное тело без изменений"), and each
// Exercise implementation decodes Payload into its own shape at the
// point it needs it, so there is exactly one place (the exercise's own
// rule for that command type) that defines what a valid payload for it
// looks like.
type Command struct {
	CommandID   uuid.UUID
	ExpectedSeq int64
	Type        CommandType
	Payload     json.RawMessage
	ClientAt    *time.Time
}

// Decision is what Exercise.Decide computes for one Command against one
// Item — before the application service assigns log_seq/action_id/
// server_at and persists the result under the item's row lock, none of
// which Decide (a pure function) has access to.
type Decision struct {
	Accepted  bool
	Rejection Rejection // zero value ("") when Accepted

	// Reaction/State/Card are the item's resulting values. A caller
	// applies them unconditionally when Accepted; when not Accepted they
	// equal the Item's own current values (Decide never reports a change
	// alongside a rejection).
	Reaction           content.Reaction
	State              ItemState
	Card               content.CardPreview
	IntakeCard         *IntakeCard
	IntakeState        *IntakeState
	IntakeDispatch     *IntakeDispatch
	IntakeNotification *IntakeNotification

	// Effect is the server-side fact to record in actions.effect,
	// distinct from the command's own payload (ADR-017) — e.g.
	// set_card_field's {path,old,new}. nil for every other command type.
	Effect map[string]any

	// PrimaryAt/CompleteAt are non-nil only on the decision that fixes
	// them for the first time (Item.PrimaryAt was nil); the caller
	// writes them to items.primary_at/deadlines.complete_at. A repeat
	// decision leaves both nil, meaning "unchanged".
	PrimaryAt  *time.Time
	CompleteAt *time.Time

	// OpenedAt is non-nil only on an accepted `open` command.
	OpenedAt *time.Time

	// Close is non-nil when this decision closes the item; the caller
	// sets items.closed_at to the same `now` it passed into Decide.
	Close     *CloseReason
	StartCall *Call
	EndCall   *CallEnd
}

type CallEnd struct {
	CallID     uuid.UUID
	AcceptedBy string
	Summary    string
	Recording  *RecordingManifest
}

// requestDigestFields is the canonical JSON RequestDigest hashes —
// ADR-004: "request_digest — sha256 канонического
// {actor_id,item_id,type,payload,expected_seq,client_at}".
func requestDigestFields(actorID, itemID uuid.UUID, cmdType CommandType, payload json.RawMessage, expectedSeq int64, clientAt *time.Time) (map[string]any, error) {
	var payloadTree any
	if len(bytes.TrimSpace(payload)) == 0 {
		payloadTree = map[string]any{}
	} else {
		dec := json.NewDecoder(bytes.NewReader(payload))
		dec.UseNumber()
		if err := dec.Decode(&payloadTree); err != nil {
			return nil, fmt.Errorf("decode payload for request digest: %w", err)
		}
	}
	var clientAtValue any
	if clientAt != nil {
		clientAtValue = clientAt.UTC().Format(time.RFC3339Nano)
	}
	return map[string]any{
		"actor_id":     actorID.String(),
		"item_id":      itemID.String(),
		"type":         string(cmdType),
		"payload":      payloadTree,
		"expected_seq": json.Number(strconv.FormatInt(expectedSeq, 10)),
		"client_at":    clientAtValue,
	}, nil
}

// RequestDigest is actions.request_digest (ADR-004/RFC-001 §7.1): sha256
// of the canonical form of {actor_id,item_id,type,payload,expected_seq,
// client_at}, with payload taken from the client's raw JSON bytes (not a
// re-marshaled typed struct) so two payloads that differ in any byte the
// server does not itself normalize away never collide. It reuses
// content.Canonical/Digest — jsonb-equivalent canonicalization is exactly
// what this digest needs, and duplicating it here would risk the two
// diverging.
func RequestDigest(actorID, itemID uuid.UUID, cmdType CommandType, payload json.RawMessage, expectedSeq int64, clientAt *time.Time) ([32]byte, error) {
	fields, err := requestDigestFields(actorID, itemID, cmdType, payload, expectedSeq, clientAt)
	if err != nil {
		return [32]byte{}, err
	}
	return content.Digest(fields), nil
}
