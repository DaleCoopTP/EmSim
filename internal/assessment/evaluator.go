package assessment

import (
	"fmt"

	"emsim/internal/content"
	"emsim/internal/training"
)

// RuleEvaluator computes one closed item's deterministic criteria (and,
// until slice 9 adds the LLM judge, always-unavailable llm criteria) from
// its evidence and reference, against the effective rubric merged for its
// scenario version. It is training.Exercise's counterpart for assessment:
// one implementation per exercise_type — internal/assessment/dds.
// Evaluator for dds_processing — selected from a map the same way
// training.Service selects an Exercise, so this package never imports
// internal/assessment/dds directly (that would cycle back here).
//
// Evaluate must return exactly one CriterionResult per criterion in
// effective.Criteria, in that same order — Score relies on both
// (disabled criteria are already absent from effective.Criteria, so
// Evaluate never sees them; the fixed order keeps CriticalErrors
// deterministic, ADR-006).
type RuleEvaluator interface {
	Evaluate(evidence training.EvidenceBody, reference content.Reference, effective Rubric) []CriterionResult
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
