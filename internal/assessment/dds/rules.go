// Package dds implements assessment.RuleEvaluator for exercise_type
// "dds_processing" (ADR-019) — the deterministic half of RFC-001 §7.4's
// scoring pipeline. It has the same name as internal/training/dds (a
// different package, a different import path) for the same reason:
// each is "the DDS rules" for its own module, selected by exercise_type
// the same way training.Service selects a training.Exercise. Callers
// that need both import each under its own alias (see
// cmd/emsim/assessment_composition.go).
package dds

import (
	"encoding/json"
	"fmt"
	"strings"

	"emsim/internal/assessment"
	"emsim/internal/content"
	"emsim/internal/training"

	"github.com/google/uuid"
)

// Evaluator is the assessment.RuleEvaluator value for "dds_processing".
// Like internal/training/dds.Exercise, it has no fields — every rule is
// a pure function of (evidence, reference, criterion).
var Evaluator assessment.RuleEvaluator = evaluator{}

type evaluator struct{}

// Evaluate decodes raw into training.EvidenceBody itself (112-6/ADR-026's
// c3 generalization: assessment.RuleEvaluator now hands every evaluator
// the raw evidence document rather than a shape typed for one exercise —
// operator112's own evaluator decodes internal/training/operator112.
// EvidenceBody the same way). A decode failure here is a programming
// error, not a data problem: raw is always evidence.schema.json's own
// dds_processing document, sealed by training/dds.Exercise.Evidence and
// digest-checked by the caller (assessment.Service.sealInputForItem)
// before Evaluate ever runs.
func (evaluator) Evaluate(raw json.RawMessage, body content.Body, effective assessment.Rubric, _ assessment.SemanticAnswers) ([]assessment.CriterionResult, error) {
	var ev training.EvidenceBody
	if err := json.Unmarshal(raw, &ev); err != nil {
		return nil, fmt.Errorf("assessment/dds: decode evidence: %w", err)
	}
	ref := body.Reference
	results := make([]assessment.CriterionResult, 0, len(effective.Criteria))
	for _, c := range effective.Criteria {
		results = append(results, evaluateCriterion(ev, ref, c))
	}
	return results, nil
}

func evaluateCriterion(ev training.EvidenceBody, ref content.Reference, c assessment.RubricCriterion) assessment.CriterionResult {
	if c.Kind == "llm" {
		return llmCriterionResult(c, ref)
	}
	switch c.Rule {
	case "t_open":
		return timingRule(ev, c, ev.Derived.OpenSeconds, timingLimit(ev.Timing, c.Params))
	case "t_primary":
		return timingRule(ev, c, ev.Derived.PrimarySeconds, timingLimit(ev.Timing, c.Params))
	case "t_complete":
		return timingRule(ev, c, ev.Derived.WorkSeconds, timingLimit(ev.Timing, c.Params))
	case "d_primary":
		return primaryDecisionRule(ev, ref, c)
	case "d_comment_required":
		return commentRequiredRule(ev, ref, c)
	case "d_field_corrections":
		return fieldCorrectionsRule(ev, ref, c)
	case "s_sequence":
		return sequenceRule(ev, ref, c)
	case "c_call_made":
		return callMadeRule(ev, ref, c)
	case "call_log_required":
		return callLogRule(ev, ref, c)
	case "address_components":
		return addressRule(ev, c)
	default:
		return unavailable(c, fmt.Sprintf("unknown deterministic rule %q", c.Rule))
	}
}

// serverInterrupted is RFC-001 §7.2/ADR-019's rule: a server-restart
// marker makes every timing-based criterion not_applicable for this
// item, regardless of how far the trainee actually got.
func serverInterrupted(ev training.EvidenceBody) bool {
	return len(ev.Interruptions) > 0
}

// stopBeforeReached is the other, narrower interruption case (ADR-019):
// a stop-triggered close (close_reason=interrupted) makes a milestone
// not_applicable only when the trainee never actually reached it before
// the barrier — a milestone reached before stop is still judged normally.
func stopBeforeReached(ev training.EvidenceBody, reached bool) bool {
	return ev.CloseReason == training.CloseInterrupted && !reached
}

func timingLimit(timing training.EvidenceTiming, params map[string]any) int {
	key, _ := params["limit_key"].(string)
	switch key {
	case "open_s":
		return timing.OpenS
	case "primary_s":
		return timing.PrimaryS
	case "complete_s":
		return timing.CompleteS
	default:
		return 0
	}
}

func paramInt(params map[string]any, key string, fallback int) int {
	switch n := params[key].(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return fallback
	}
}

// timingRule is T_OPEN/T_PRIMARY/T_COMPLETE (rubric.default.json): met
// within limit, partial until partial_until_s, not_met beyond that —
// unless the milestone is excused by interruption (see
// serverInterrupted/stopBeforeReached above).
func timingRule(ev training.EvidenceBody, c assessment.RubricCriterion, elapsed *float64, limit int) assessment.CriterionResult {
	reached := elapsed != nil
	if serverInterrupted(ev) || stopBeforeReached(ev, reached) {
		return na(c)
	}
	if !reached {
		return notMet(c, "the trainee never reached this milestone")
	}
	partialUntil := paramInt(c.Params, "partial_until_s", limit)
	switch {
	case *elapsed <= float64(limit):
		return met(c, fmt.Sprintf("%.0fs within the %ds norm", *elapsed, limit))
	case *elapsed <= float64(partialUntil):
		return partial(c, 0.5, fmt.Sprintf("%.0fs exceeds the %ds norm but within the %ds partial allowance", *elapsed, limit, partialUntil))
	default:
		return notMet(c, fmt.Sprintf("%.0fs exceeds even the %ds partial allowance", *elapsed, partialUntil))
	}
}

// primaryDecisionRule is D_PRIMARY: the first accepted status compared
// to reference.primary_decision.status. critical_when=refused_profile_
// incident makes this criterion critical for this specific item — on top
// of, never instead of, the rubric's own static critical flag — whenever
// the reference expected acceptance but the trainee refused/rejected it.
func primaryDecisionRule(ev training.EvidenceBody, ref content.Reference, c assessment.RubricCriterion) assessment.CriterionResult {
	reached := ev.Derived.PrimaryStatus != nil
	if serverInterrupted(ev) || stopBeforeReached(ev, reached) {
		return na(c)
	}
	if !reached {
		return notMet(c, "no primary decision was ever made")
	}
	actual := *ev.Derived.PrimaryStatus
	expected := ref.PrimaryDecision.Status
	critical := c.Critical
	if c.CriticalWhen == "refused_profile_incident" && expected == content.ReactionAccepted &&
		(actual == content.ReactionNotAccepted || actual == content.ReactionRefused) {
		critical = true
	}
	refs := primaryDecisionRefs(ev)
	if actual == expected {
		return result(c, assessment.CriterionMet, critical, fmt.Sprintf("decision %q matches the reference", actual), refs)
	}
	return result(c, assessment.CriterionNotMet, critical, fmt.Sprintf("decision %q does not match the reference %q", actual, expected), refs)
}

func primaryDecisionRefs(ev training.EvidenceBody) []string {
	for _, a := range ev.Actions {
		if a.Accepted && a.Type == training.CommandSetStatus {
			return []string{"action:" + a.ActionID.String()}
		}
	}
	return nil
}

// commentRequiredRule is D_COMMENT_REQUIRED: reference.primary_decision.
// comment_required gates applicability; the criterion only checks a
// comment exists, not its content (D_COMMENT_CONTENT, an llm criterion,
// covers that from slice 9).
func commentRequiredRule(ev training.EvidenceBody, ref content.Reference, c assessment.RubricCriterion) assessment.CriterionResult {
	if !ref.PrimaryDecision.CommentRequired {
		return na(c)
	}
	if len(ev.Comments) > 0 {
		return met(c, "a comment was recorded")
	}
	return notMet(c, "comment_required but no comment was recorded")
}

// fieldCorrectionsRule is D_FIELD_CORRECTIONS (ADR-017/019): every
// reference.field_corrections entry needs both the final card value and
// a confirmed set_card_field action for that path. Slice 6 only ever
// sees the one allowlisted path (/card/address/okrug); a future slice
// generalizing set_card_field needs a real JSON-pointer walker here
// instead of fieldCorrectionApplied's direct field access.
func fieldCorrectionsRule(ev training.EvidenceBody, ref content.Reference, c assessment.RubricCriterion) assessment.CriterionResult {
	if len(ref.FieldCorrections) == 0 {
		return na(c)
	}
	total := len(ref.FieldCorrections)
	matched := 0
	var refs []string
	for _, fc := range ref.FieldCorrections {
		actionID, ok := fieldCorrectionApplied(ev, fc)
		if !ok {
			continue
		}
		matched++
		if actionID != nil {
			refs = append(refs, "action:"+actionID.String())
		}
	}
	switch {
	case matched == total:
		return result(c, assessment.CriterionMet, c.Critical, fmt.Sprintf("%d/%d required corrections applied", matched, total), refs)
	case matched > 0:
		return partial(c, float64(matched)/float64(total), fmt.Sprintf("%d/%d required corrections applied", matched, total), refs...)
	default:
		return notMet(c, "no required correction was applied")
	}
}

func fieldCorrectionApplied(ev training.EvidenceBody, fc content.FieldCorrection) (actionID *uuid.UUID, applied bool) {
	if fc.Path != "/card/address/okrug" {
		return nil, false
	}
	if ev.FinalCard.Address.Okrug != fc.ExpectedValue {
		return nil, false
	}
	id := setCardFieldActionID(ev, fc.Path)
	return id, id != nil
}

func setCardFieldActionID(ev training.EvidenceBody, path string) *uuid.UUID {
	for _, a := range ev.Actions {
		if !a.Accepted || a.Type != training.CommandSetCardField || a.Effect == nil {
			continue
		}
		if p, ok := a.Effect["path"].(string); ok && p == path {
			id := a.ActionID
			return &id
		}
	}
	return nil
}

// sequenceRule is S_SEQUENCE: reference.expected_chain must appear, in
// order (not necessarily contiguously), among the statuses set after the
// primary decision (evidence.derived.chain's own first entry is that
// primary decision itself). events[].expects is not checked here —
// ADR-019 defers a per-event reaction check to slice 9/11.
func sequenceRule(ev training.EvidenceBody, ref content.Reference, c assessment.RubricCriterion) assessment.CriterionResult {
	if len(ref.ExpectedChain) == 0 {
		return na(c)
	}
	if serverInterrupted(ev) {
		return na(c)
	}
	chain := ev.Derived.Chain
	if len(chain) > 0 {
		chain = chain[1:]
	}
	pos, matched := 0, 0
	for _, want := range ref.ExpectedChain {
		for pos < len(chain) {
			found := chain[pos] == want
			pos++
			if found {
				matched++
				break
			}
		}
	}
	switch {
	case matched == len(ref.ExpectedChain):
		return met(c, "expected chain observed in order")
	case matched > 0:
		return partial(c, float64(matched)/float64(len(ref.ExpectedChain)), fmt.Sprintf("%d/%d expected transitions observed", matched, len(ref.ExpectedChain)))
	default:
		return notMet(c, "none of the expected transitions were observed")
	}
}

// callMadeRule is C_CALL_MADE: training/dds's own live rule already
// blocks reaching reference.call.before_status without a completed call
// to reference.call.to (RejectCallRequired), so evidence review only
// needs to confirm one exists — ordering was already enforced when it
// mattered.
func callMadeRule(ev training.EvidenceBody, ref content.Reference, c assessment.RubricCriterion) assessment.CriterionResult {
	if !ref.Call.Required {
		return na(c)
	}
	if completedRequiredCall(ev, ref) != nil {
		return met(c, "the required call was completed")
	}
	return notMet(c, "no completed call to the required contact")
}

// callLogRule is C_CALL_LOG (rubric rule name call_log_required):
// "Кто принял"/"Суть сообщения" both filled on the completed required
// call.
func callLogRule(ev training.EvidenceBody, ref content.Reference, c assessment.RubricCriterion) assessment.CriterionResult {
	if !ref.Call.Required {
		return na(c)
	}
	call := completedRequiredCall(ev, ref)
	if call == nil {
		return notMet(c, "no completed call to the required contact")
	}
	if call.AcceptedBy != nil && strings.TrimSpace(*call.AcceptedBy) != "" &&
		call.Summary != nil && strings.TrimSpace(*call.Summary) != "" {
		return met(c, "call log ('кто принял'/'суть сообщения') is filled")
	}
	return notMet(c, "call log is missing 'кто принял' or 'суть сообщения'")
}

func completedRequiredCall(ev training.EvidenceBody, ref content.Reference) *training.EvidenceCall {
	for i := range ev.Calls {
		call := &ev.Calls[i]
		// ADR-031: only a call the trainee placed fulfils the required
		// call; an incoming call from the same contact does not.
		if call.Outgoing() && call.ContactKey == ref.Call.To && call.EndedAt != nil {
			return call
		}
	}
	return nil
}

// llmCriterionResult is every kind=llm criterion's slice-6 outcome
// (JUDGE=off, ADR-013/019): unavailable unless its own applicability
// predicate is empty, in which case not_applicable — the same
// distinction a live judge will make later without changing this
// function's shape.
func llmCriterionResult(c assessment.RubricCriterion, ref content.Reference) assessment.CriterionResult {
	switch c.ID {
	case "D_COMMENT_CONTENT":
		if len(ref.PrimaryDecision.CommentMustMention) == 0 {
			return na(c)
		}
	case "C_CALL_CONTENT", "C_CALL_LOG_CONTENT":
		if !ref.Call.Required {
			return na(c)
		}
	}
	return unavailable(c, "LLM judge is not enabled in this slice (JUDGE=off)")
}

func result(c assessment.RubricCriterion, status assessment.CriterionStatus, critical bool, explanation string, refs []string) assessment.CriterionResult {
	return assessment.CriterionResult{ID: c.ID, Status: status, Weight: c.Weight, Critical: critical, EvidenceRefs: refs, Explanation: explanation}
}

func met(c assessment.RubricCriterion, explanation string, refs ...string) assessment.CriterionResult {
	return result(c, assessment.CriterionMet, c.Critical, explanation, refs)
}

func notMet(c assessment.RubricCriterion, explanation string, refs ...string) assessment.CriterionResult {
	return result(c, assessment.CriterionNotMet, c.Critical, explanation, refs)
}

func na(c assessment.RubricCriterion) assessment.CriterionResult {
	return result(c, assessment.CriterionNotApplicable, c.Critical, "not applicable to this case", nil)
}

func unavailable(c assessment.RubricCriterion, explanation string) assessment.CriterionResult {
	return result(c, assessment.CriterionUnavailable, c.Critical, explanation, nil)
}

func partial(c assessment.RubricCriterion, score float64, explanation string, refs ...string) assessment.CriterionResult {
	r := result(c, assessment.CriterionPartial, c.Critical, explanation, refs)
	r.Score = &score
	return r
}
