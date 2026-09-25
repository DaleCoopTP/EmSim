// Package assessment implements RFC-001 §7.4's deterministic assessment
// pipeline (slice 6, ADR-006/013/016/019): rubric merging and scoring,
// sealing evidence + rules into an immutable assessment_inputs snapshot,
// the single auto rev=1 a worker produces from it, the shared finalizer
// for exhausted retries, and instructor expert revisions. STT and the LLM
// judge (rubric criteria of kind=llm) are slice 9 — every llm-kind
// criterion here always resolves to CriterionUnavailable.
//
// Like internal/training, assessment keeps its domain rules
// (rubric.go/input.go/revision.go, and the DDS rule set in
// internal/assessment/dds) independent of HTTP/PostgreSQL: Service
// (service.go) coordinates use cases and transactions, Store/ports.go
// declare the narrow persistence and cross-module read ports it needs.
package assessment

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrNotFound is a missing item/assessment/input a caller looked up
	// directly by id.
	ErrNotFound = errors.New("assessment: not found")
	// ErrNotClosed is returned for an item that has not reached
	// closed/interrupted yet — there is no evidence to assess.
	ErrNotClosed = errors.New("assessment: item is not closed")
	// ErrStaleRevision is a manual revision whose base_revision no longer
	// matches the item's current final revision (RFC-001 §7.4: "Под
	// блокировкой item проверяется base_revision; stale → 409").
	ErrStaleRevision = errors.New("assessment: stale base revision")
	// ErrValidation wraps a rejected AssessmentRevision request — see
	// ValidationError for the specific field/reason.
	ErrValidation = errors.New("assessment: validation failed")
)

// ValidationError names the offending field of a rejected
// AssessmentRevision, the same convention internal/training.ValidationError
// uses for command payloads.
type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string { return "assessment: " + e.Field + ": " + e.Reason }
func (e *ValidationError) Unwrap() error { return ErrValidation }

func validationErr(field, reason string) error {
	return &ValidationError{Field: field, Reason: reason}
}

// Kind is assessments.kind.
type Kind string

const (
	KindAuto   Kind = "auto"
	KindExpert Kind = "expert"
)

// Status is assessments.status — RFC-001 §7.4/ADR-013's three outcomes.
// needs_review and unavailable both carry a NULL score (ADR-016 A3): the
// distinction is administrative (an auto pipeline that ran vs one that
// never got sealed input), not a different scoring rule.
type Status string

const (
	StatusReady       Status = "ready"
	StatusNeedsReview Status = "needs_review"
	StatusUnavailable Status = "unavailable"
)

// CriterionStatus is one criterion's outcome — rubric.schema.json's
// implicit vocabulary, made explicit here (also assessment-inputs.schema.
// json's rule_results.status).
type CriterionStatus string

const (
	CriterionMet           CriterionStatus = "met"
	CriterionPartial       CriterionStatus = "partial"
	CriterionNotMet        CriterionStatus = "not_met"
	CriterionNotApplicable CriterionStatus = "not_applicable"
	CriterionUnavailable   CriterionStatus = "unavailable"
)

// CriterionResult is one row of assessments.criteria (openapi.yaml's
// CriterionResult). Weight is the criterion's raw rubric_effective
// weight (before disabled/not_applicable normalization) — Score.Compute
// normalizes on the fly from RubricEffective, so this field stays a
// stable, auditable number independent of which other criteria happened
// to apply to this particular item.
type CriterionResult struct {
	ID           string
	Status       CriterionStatus
	Score        *float64 // 0..1; nil for not_applicable/unavailable
	Weight       float64
	Critical     bool
	EvidenceRefs []string
	Explanation  string
	// PenaltyPoints (112-6/ADR-026) is set only for a kind=penalty
	// criterion — the actual points charged (0 if the reference is
	// absent or nothing was wrong), always >= 0. Score.Compute excludes
	// penalty criteria from weight normalization entirely and instead
	// subtracts their summed PenaltyPoints from the normalized 0..100
	// score, clamped to [0, 100].
	PenaltyPoints *float64
	// Details (112-6/ADR-026) is a penalty or block criterion's own
	// line-by-line breakdown (one address field, one profile card, one
	// service, ...) for the instructor review UI — never shown to a
	// trainee (assessment.StripExpected clears Expected/Actual from the
	// trainee-facing projection, same as it clears reference values
	// elsewhere).
	Details []CriterionDetail
}

// CriterionDetail is one row of CriterionResult.Details — a single
// scored field/card/service within a block or penalty criterion
// (openapi.yaml's CriterionDetail, 112-6/ADR-026).
type CriterionDetail struct {
	Key       string
	Label     string
	Points    float64
	MaxPoints float64
	Status    CriterionStatus // met | partial | not_met | not_applicable
	Actual    *string
	Expected  *string
}

// StripExpected returns a copy of criteria with every CriterionDetail's
// Expected value cleared (112-6/ADR-026) — the per-field/per-card/
// per-service detail breakdown can otherwise leak the scenario's closed
// reference (a street name, a card field's correct answer) straight to
// the trainee it was scored against, the same leak RFC-001 already
// forbids for the raw scenario reference itself. Actual (the trainee's
// own filled value) and every other field are left as-is — only the
// answer key half of a detail row is instructor-only.
func StripExpected(criteria []CriterionResult) []CriterionResult {
	stripped := make([]CriterionResult, len(criteria))
	for i, c := range criteria {
		stripped[i] = c
		if len(c.Details) == 0 {
			continue
		}
		details := make([]CriterionDetail, len(c.Details))
		for j, d := range c.Details {
			d.Expected = nil
			details[j] = d
		}
		stripped[i].Details = details
	}
	return stripped
}

// Feedback is one assessments.feedback entry (openapi.yaml).
type Feedback struct {
	CriterionID string
	Severity    string // info | warning | critical
	Text        string
	GuideRef    string
}

// Assessment is one assessments row (domain shape; Store's postgres
// adapter marshals Criteria/Feedback/RubricEffective to/from jsonb).
type Assessment struct {
	ID              uuid.UUID
	ItemID          uuid.UUID
	Revision        int
	Kind            Kind
	Status          Status
	EvidenceDigest  [32]byte
	InputID         *uuid.UUID
	SourceTaskID    *uuid.UUID
	BaseRevision    *int
	RubricVersion   string
	RubricEffective Rubric
	Score           *float64 // 0..100
	Passed          *bool
	Criteria        []CriterionResult
	CriticalErrors  []string
	Feedback        []Feedback
	Model           *string
	CreatedBy       *uuid.UUID
	Reason          *string
	CreatedAt       time.Time
}

// TraineeAssessmentState is trainee_assessment_state — assessment's own
// basis-versioning row (ADR-016 B5). Advice is slice 10; only Version is
// meaningful in slice 6.
type TraineeAssessmentState struct {
	UserID       uuid.UUID
	ExerciseType string
	Version      int64
}

// Detail is the instructor-facing assessment projection.  Evidence and the
// effective rubric intentionally travel with it: the review UI must be able
// to explain a result without reassembling an immutable snapshot from mutable
// training tables.
type Detail struct {
	AutomaticState  *string
	Final           *Assessment
	Revisions       []Assessment
	RubricEffective Rubric
	Evidence        any
}

// LessonAssessmentItem is the closed-card projection used by the instructor
// queue.  It deliberately contains only the trainee fields the public User
// projection needs, rather than exposing an auth-store implementation here.
type LessonAssessmentItem struct {
	ItemID         uuid.UUID
	UserID         uuid.UUID
	Login          string
	FullName       string
	ServiceCode    *string
	Level          string
	Active         bool
	WorkstationNo  int
	Ordinal        int
	CardNumber     string
	ItemState      string
	CloseReason    *string
	ClosedAt       time.Time
	AutomaticState *string
	Final          *Assessment
}
