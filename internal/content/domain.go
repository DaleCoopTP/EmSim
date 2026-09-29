package content

import (
	"errors"
	"fmt"
)

// ExerciseType is scenario.schema.json's exercise_type — ADR-015's
// extension point, fixed at const "dds_processing" until the operator-112
// stage adds its own contract (slice-planning.md §1).
type ExerciseType string

const (
	ExerciseTypeDDSProcessing     ExerciseType = "dds_processing"
	ExerciseTypeOperator112Intake ExerciseType = "operator112_intake"
)

func (e ExerciseType) Valid() bool {
	return e == ExerciseTypeDDSProcessing || e == ExerciseTypeOperator112Intake
}

// Reaction is scenario.schema.json's $defs.reaction — the card status
// vocabulary shared by notification_list, events and reference. The
// transition rules a status machine would enforce belong to
// training/dds (RFC-001 §4.5, slice 3); content only carries the
// vocabulary and, per service, the workflow graph (Workflow) that a
// prepared scenario's reference must stay consistent with.
type Reaction string

const (
	ReactionAdded                Reaction = "added"
	ReactionReceived             Reaction = "received"
	ReactionAccepted             Reaction = "accepted"
	ReactionNotAccepted          Reaction = "not_accepted"
	ReactionResponding           Reaction = "responding"
	ReactionArrived              Reaction = "arrived"
	ReactionWorking              Reaction = "working"
	ReactionCompleted            Reaction = "completed"
	ReactionRefused              Reaction = "refused"
	ReactionCompletedWithoutTeam Reaction = "completed_without_team"
)

func (r Reaction) Valid() bool {
	switch r {
	case ReactionAdded, ReactionReceived, ReactionAccepted, ReactionNotAccepted,
		ReactionResponding, ReactionArrived, ReactionWorking, ReactionCompleted,
		ReactionRefused, ReactionCompletedWithoutTeam:
		return true
	default:
		return false
	}
}

var (
	// ErrDuplicateKey is DecodeFile's rejection of a JSON object with a
	// repeated key — encoding/json silently keeps the last occurrence,
	// which would let a crafted or corrupted file mean something
	// different than what a reviewer read (slice-2-plan.md's "loosen
	// nothing" import posture).
	ErrDuplicateKey = errors.New("duplicate object key")

	// ErrSchemaInvalid is DecodeFile/Validate's JSON-Schema-level
	// rejection — see internal/content/schema.Validator.
	ErrSchemaInvalid = errors.New("scenario file does not match its schema")

	// ErrValidation is Validate's semantic-level rejection (target
	// service/classifier unknown, workflow-inconsistent reference, and
	// so on) — always wrapped by a *ValidationError.
	ErrValidation = errors.New("scenario validation failed")
)

// ValidationError names the one JSON Pointer-ish field that failed and
// why, mirroring internal/auth's ValidationError. Reason is a short
// machine code, not user-facing text.
type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("scenario validation failed: field=%s reason=%s", e.Field, e.Reason)
}

func (e *ValidationError) Unwrap() error { return ErrValidation }

func invalid(field, reason string) error {
	return &ValidationError{Field: field, Reason: reason}
}
