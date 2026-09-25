package assessment

import (
	"encoding/json"
	"fmt"

	"emsim/internal/content"
	"emsim/internal/platform/tasks"
)

// RuleEvaluator computes one closed item's deterministic criteria (and,
// until slice 9 adds the LLM judge, always-unavailable llm criteria) from
// its evidence and scenario body, against the effective rubric merged
// for its scenario version. It is training.Exercise's counterpart for
// assessment: one implementation per exercise_type —
// internal/assessment/dds for dds_processing,
// internal/assessment/operator112 for operator112_intake (112-6/
// ADR-026) — selected from a map the same way training.Service selects
// an Exercise, so this package never imports either directly (that would
// cycle back here).
//
// evidence is the item's sealed evidence document exactly as stored
// (evidence.schema.json for DDS, evidence.operator112.schema.json for
// 112) — raw rather than typed, since assessment itself has no reason to
// know either exercise's evidence shape (ADR-026's c3: this package used
// to decode DDS's own training.EvidenceBody directly, which made adding
// a second exercise_type here impossible without either a union type or
// a second interface). Each RuleEvaluator decodes its own shape; a
// decode failure is a programming error (evidence.digest already proved
// this document matches what training sealed), not a data problem to
// recover from.
//
// Evaluate must return exactly one CriterionResult per criterion in
// effective.Criteria, in that same order — Score relies on both
// (disabled criteria are already absent from effective.Criteria, so
// Evaluate never sees them; the fixed order keeps CriticalErrors
// deterministic, ADR-006). A non-nil error aborts sealing the same way
// a recognized preparation failure already does (sealInputForItem's own
// doc comment): return a *TerminalEvaluationError for a specific,
// expected condition an evaluator wants its own tasks.ErrorCode for
// (112-6/ADR-026's operator112_legacy_route — an item whose evidence has
// no ADR-023 notify-services snapshot to score at all); any other error
// is treated as an unexpected bug and simply propagated.
type RuleEvaluator interface {
	Evaluate(evidence json.RawMessage, body content.Body, effective Rubric) ([]CriterionResult, error)
}

// TerminalEvaluationError lets a RuleEvaluator name the specific
// tasks.ErrorCode sealInputForItem should fail the waiting task with,
// instead of the generic "evaluator_failed" — without either package
// importing the other's evaluator package directly (that would cycle:
// internal/assessment/operator112 already imports this package to
// implement RuleEvaluator).
type TerminalEvaluationError struct {
	Code tasks.ErrorCode
}

func (e *TerminalEvaluationError) Error() string {
	return fmt.Sprintf("assessment: terminal evaluation error: %s", e.Code)
}

// Registry maps exercise_type to its RuleEvaluator — a plain map, not a
// type of its own, the same convention
// cmd/emsim/training_composition.go's map[content.ExerciseType]training.
// Exercise already uses.
type Registry map[content.ExerciseType]RuleEvaluator

// EvaluatorFor looks up et's RuleEvaluator, or an error naming the
// unsupported type (ADR-015: only dds_processing exists today).
func (r Registry) EvaluatorFor(et content.ExerciseType) (RuleEvaluator, error) {
	evaluator, ok := r[et]
	if !ok {
		return nil, fmt.Errorf("assessment: no RuleEvaluator registered for exercise_type %q", et)
	}
	return evaluator, nil
}
