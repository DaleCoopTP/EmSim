package assessment

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

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
//
// semantic (ADR-028) carries a llm-kind criterion's own judge answer,
// keyed by criterion id, when one has already been obtained — nil at
// seal time (sealInputForItem calls Evaluate before any judge ever
// runs, purely to build assessment_inputs.rule_results' informational
// snapshot) and again whenever no judge is configured at all; non-nil
// only inside the same worker attempt that just called the judge
// (Service.Handle), immediately before computeAndInsertAuto's own call.
// An evaluator with no llm criteria (dds.Evaluator) simply ignores it.
// Unlike evidence/body/effective, Evaluate is not required to be
// deterministic with respect to semantic across separate calls — a
// judge's own answer may differ call to call the way any model call
// can; ADR-006's reproducibility guarantee is about the rule-based
// three other parameters, not about replaying a past model response.
type RuleEvaluator interface {
	Evaluate(evidence json.RawMessage, body content.Body, effective Rubric, semantic SemanticAnswers) ([]CriterionResult, error)
}

// SemanticAnswers is one llm-kind criterion's raw judge answer, keyed by
// criterion id — see RuleEvaluator.Evaluate's own doc comment. The value
// shape is whatever that criterion's own SemanticJudge implementation
// returns; only the criterion's own RuleEvaluator ever decodes it.
type SemanticAnswers map[string]json.RawMessage

// SemanticRequest is one llm-kind criterion's own prepared model call —
// SemanticPreparer.PrepareSemantic's return value for that criterion id.
// PromptVersion is sealed into assessment_inputs.judge.prompt_versions
// (openapi.yaml/assessment-inputs.schema.json's existing field, ADR-006)
// and doubles as the key Service.judge.Registry dispatches Handle's own
// judge call by; Payload is sealed verbatim into assessment_inputs.
// semantic_input[id] and handed back to that same SemanticJudge as its
// own payload.
type SemanticRequest struct {
	PromptVersion string
	Payload       any
}

// SemanticPreparer is an optional RuleEvaluator extension (ADR-028):
// implemented by an exercise_type that has at least one llm-kind
// criterion needing a model call prepared once, at seal time, before any
// transaction that could block on it — internal/assessment/operator112
// is the only implementation so far (DESCRIPTION_CONTENT). Called only
// when a judge is actually configured (Service.judge != nil); its own
// returned map is empty, not nil, for a criterion with nothing to ask
// (no reference questions, or an empty description) — Evaluate's own
// ordinary "no reference"/"empty description" rule still produces that
// criterion's CriterionResult without the judge ever being called for
// it. sealInputForItem is the only caller.
type SemanticPreparer interface {
	PrepareSemantic(evidence json.RawMessage, body content.Body, effective Rubric) (map[string]SemanticRequest, error)
}

// SemanticJudge answers one sealed SemanticRequest.Payload outside any
// transaction (Service.Handle's own job, ADR-003/025/028) — dispatched
// by PromptVersion, never by exercise_type: assessment itself stays
// exercise-agnostic, and the actual prompt/schema/parsing logic lives in
// the exercise's own package (internal/assessment/operator112/descjudge
// for "description-questions-v1"). model/parameters come from the same
// JudgeConfig Service.Handle already holds, not from the sealed input
// (ADR-006 seals the request, not the deployment's own current model
// choice — a retried attempt after a config change would otherwise be
// unable to tell which model actually answered).
type SemanticJudge interface {
	Answer(ctx context.Context, model string, parameters map[string]any, payload json.RawMessage) (json.RawMessage, error)
}

// SemanticJudgeRegistry maps a SemanticRequest.PromptVersion to the
// SemanticJudge that can answer it — cmd/emsim's own composition
// registers internal/assessment/operator112/descjudge.Handler under
// "description-questions-v1", mirroring Registry's own exercise_type ->
// RuleEvaluator convention one level down (prompt version, not exercise
// type, since one exercise_type's rubric can eventually grow more than
// one llm criterion with different prompts).
type SemanticJudgeRegistry map[string]SemanticJudge

// JudgeConfig is Service's own judge wiring (ADR-028) — nil in the api
// process (which never calls Handle/sealInputForItem's judge branch,
// only the worker's Coordinator/Runner do) and, even in the worker,
// nil whenever ASSESSMENT_JUDGE is off (config.Worker.AssessmentJudge),
// so a stock deployment with no judge configured takes none of this
// code's new paths at all — sealInputForItem's SemanticPreparer branch
// and Handle's own judge call both short-circuit on Service.judge==nil.
type JudgeConfig struct {
	// Model is sealed into assessment_inputs.judge.model verbatim
	// (openapi.yaml's existing field) — the deployment's own configured
	// model name, not a value SemanticJudge returns.
	Model string
	// Parameters is sealed into assessment_inputs.judge.parameters and
	// handed to SemanticJudge.Answer's own parameters argument — a
	// generic map since each PromptVersion's own SemanticJudge
	// interprets whichever keys it cares about (ADR-028's descjudge
	// reads none yet beyond what its own defaults already fix, but the
	// shape stays open for a future prompt version that does).
	Parameters map[string]any
	// Registry dispatches Handle's own judge call by PromptVersion.
	Registry SemanticJudgeRegistry
	// Timeout bounds one SemanticJudge.Answer call the same way
	// CallerReplyTimeout bounds one CallerReplier.Reply call.
	Timeout time.Duration
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
