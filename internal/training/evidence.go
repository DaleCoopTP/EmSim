package training

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"emsim/internal/content"

	"github.com/google/uuid"
)

// EvidenceBody is evidence.schema.json's ItemEvidence v1 — the immutable
// close-time snapshot RFC-001 §6/§7.4 requires. Struct tags mirror the
// schema's property names exactly, so json.Marshal produces a document
// that validates against it as-is (see internal/training/dds's evidence
// tests). Calls are always empty — the phone is slice 5. Interruption
// (singular, the stop-triggered snapshot) is slice 4's C8; Interruptions
// (the server-restart recovery markers, RFC-001 §7.2) is populated from
// the closed item's own accumulated history as of C6.
type EvidenceBody struct {
	Schema            string                `json:"schema"`
	ItemID            uuid.UUID             `json:"item_id"`
	RunID             uuid.UUID             `json:"run_id"`
	LessonID          uuid.UUID             `json:"lesson_id"`
	TraineeID         uuid.UUID             `json:"trainee_id"`
	WorkstationNo     int                   `json:"workstation_no"`
	ScenarioVersionID uuid.UUID             `json:"scenario_version_id"`
	ScenarioDigest    string                `json:"scenario_digest"` // hex sha256, scenario_versions.digest
	TargetService     string                `json:"target_service"`
	SpawnedFromItemID *uuid.UUID            `json:"spawned_from_item_id,omitempty"`
	Timing            EvidenceTiming        `json:"timing"`
	OfferedAt         time.Time             `json:"offered_at"`
	OpenedAt          *time.Time            `json:"opened_at"`
	ClosedAt          time.Time             `json:"closed_at"`
	CloseReason       CloseReason           `json:"close_reason"`
	FinalReaction     content.Reaction      `json:"final_reaction"`
	Mode              Mode                  `json:"mode"`
	FinalCard         content.CardPreview   `json:"final_card"`
	Actions           []EvidenceAction      `json:"actions"`
	Events            []EvidenceEvent       `json:"events"`
	Calls             []any                 `json:"calls"`
	Comments          []EvidenceComment     `json:"comments,omitempty"`
	Derived           EvidenceDerived       `json:"derived"`
	CutoffLogSeq      int64                 `json:"cutoff_log_seq"`
	PrimaryAt         *time.Time            `json:"primary_at"`
	Deadlines         EvidenceDeadlines     `json:"deadlines"`
	Interruption      *EvidenceInterruption `json:"interruption"`
	ExerciseType      content.ExerciseType  `json:"exercise_type"`
	Interruptions     []Interruption        `json:"interruptions"`
}

// EvidenceEvent is one of the item's own scheduled-or-terminal scenario
// events, evidence.schema.json's simplified projection of item_events —
// key/state/timing only, no delivery/text/from (the trainee-facing
// DeliveredEvent projection that needs those lives in the HTTP layer,
// which already has the scenario version body to resolve them from).
type EvidenceEvent struct {
	Key         string     `json:"key"`
	State       EventState `json:"state"`
	AnchorAt    time.Time  `json:"anchor_at"`
	DueAt       time.Time  `json:"due_at"`
	DeliveredAt *time.Time `json:"delivered_at"`
	Late        bool       `json:"late"`
	SkipReason  *string    `json:"skip_reason"`
}

// EvidenceTiming is evidence body's timing — the frozen lesson policy
// plus its fixed anchors (RFC-001 §7.2's "Единая timing policy ДДС").
type EvidenceTiming struct {
	OpenS          int    `json:"open_s"`
	PrimaryS       int    `json:"primary_s"`
	CompleteS      int    `json:"complete_s"`
	OpenAnchor     string `json:"open_anchor"`     // const "offered_at"
	PrimaryAnchor  string `json:"primary_anchor"`  // const "offered_at"
	CompleteAnchor string `json:"complete_anchor"` // const "primary_at"
}

// EvidenceAction is one journal entry in the snapshot, up to
// cutoff_log_seq inclusive — RFC-001 §7.1's "ссылки на отдельные
// действия — action_id, не seq".
type EvidenceAction struct {
	Seq           int64             `json:"seq"`
	Type          CommandType       `json:"type"`
	Payload       json.RawMessage   `json:"payload,omitempty"`
	Accepted      bool              `json:"accepted"`
	Rejection     *Rejection        `json:"rejection"`
	Effect        map[string]any    `json:"effect,omitempty"`
	ServerAt      time.Time         `json:"server_at"`
	ReactionAfter *content.Reaction `json:"reaction_after"`
	ActionID      uuid.UUID         `json:"action_id"`
	LogSeq        int64             `json:"log_seq"`
}

// EvidenceComment is one journalled comment, in input order — a
// convenience duplicate of what add_comment/set_status payloads already
// carry (evidence.schema.json's own description of the field).
type EvidenceComment struct {
	Seq        int64             `json:"seq"`
	Text       string            `json:"text"`
	WithStatus *content.Reaction `json:"with_status,omitempty"`
}

// EvidenceDerived is the item's actual server-measured intervals,
// without pause compensation (RFC-001 §7.2/§8: interruption makes the
// timing-based criteria not_applicable, but the raw seconds stay in the
// report). Slice 3 never sets an interruption, so every derived value
// here reflects the item's real, uninterrupted timeline.
type EvidenceDerived struct {
	OpenSeconds         *float64           `json:"open_seconds"`
	PrimarySeconds      *float64           `json:"primary_seconds"`
	WorkSeconds         *float64           `json:"work_seconds"`
	TotalSeconds        float64            `json:"total_seconds"`
	PrimaryStatus       *content.Reaction  `json:"primary_status,omitempty"`
	Chain               []content.Reaction `json:"chain,omitempty"`
	RejectedTransitions int                `json:"rejected_transitions,omitempty"`
	CommentCount        int                `json:"comment_count,omitempty"`
	CallCount           int                `json:"call_count,omitempty"`
}

// EvidenceDeadlines mirrors items.deadlines at closed_at.
type EvidenceDeadlines struct {
	OpenAt     time.Time  `json:"open_at"`
	PrimaryAt  time.Time  `json:"primary_at"`
	CompleteAt *time.Time `json:"complete_at"`
}

// EvidenceInterruption is not produced by anything in slice 3 (server-
// restart recovery is slice 4); it exists so the struct already matches
// evidence.schema.json's optional interruption property.
type EvidenceInterruption struct {
	Reason          string    `json:"reason"`
	StoppedAt       time.Time `json:"stopped_at"`
	UnreachedEvents []string  `json:"unreached_events,omitempty"`
}

// Evidence is SealEvidence's result: the canonical bytes to store
// verbatim in evidence.body and the digest to store in evidence.digest.
type Evidence struct {
	Body   []byte
	Digest [32]byte
}

// SealEvidence canonicalizes body into the exact bytes evidence.digest
// hashes (ADR-006's immutable snapshot), reusing content.Canonical/
// Digest so this stays byte-for-byte consistent with how scenario
// version digests are computed. Storing these same canonical bytes as
// evidence.body — rather than a second, separate json.Marshal of the
// typed struct at the SQL layer — is what keeps body and digest from
// ever drifting apart, the same reasoning content.InsertScenarioVersion
// follows for scenario bodies (internal/content/import.go).
func SealEvidence(body EvidenceBody) (Evidence, error) {
	canonical, digest, err := canonicalDigest(body)
	if err != nil {
		return Evidence{}, err
	}
	return Evidence{Body: canonical, Digest: digest}, nil
}

// canonicalDigest marshals v (an ordinary Go value) to JSON and re-
// decodes it with UseNumber before handing it to content.Canonical/
// Digest, which require that json.Number-preserving tree shape and panic
// on any other numeric type.
func canonicalDigest(v any) ([]byte, [32]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, [32]byte{}, fmt.Errorf("marshal evidence body: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var tree any
	if err := dec.Decode(&tree); err != nil {
		return nil, [32]byte{}, fmt.Errorf("decode evidence body for canonicalization: %w", err)
	}
	return content.Canonical(tree), content.Digest(tree), nil
}
