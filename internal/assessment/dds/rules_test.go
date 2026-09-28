package dds

import (
	"encoding/json"
	"testing"
	"time"

	"emsim/internal/assessment"
	"emsim/internal/content"
	"emsim/internal/training"

	"github.com/google/uuid"
)

// effectiveRubric merges scoring onto the real embedded default rubric —
// the same call training/service.go's coordinator will make — so these
// tests exercise the actual rubric.default.json wiring, not a synthetic
// stand-in.
func effectiveRubric(t *testing.T, scoring *content.Scoring) assessment.Rubric {
	t.Helper()
	base, err := assessment.LoadDefault()
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	return assessment.Merge(base, scoring)
}

func findResult(t *testing.T, results []assessment.CriterionResult, id string) assessment.CriterionResult {
	t.Helper()
	for _, r := range results {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("no criterion result for %q among %d results", id, len(results))
	return assessment.CriterionResult{}
}

// evaluate adapts the tests' own typed (training.EvidenceBody,
// content.Reference) fixtures onto Evaluate's real signature (112-6/
// ADR-026's c3 — raw evidence + the whole scenario body, so one
// RuleEvaluator interface serves every exercise_type). t.Helper's
// t.Fatal on a marshal error would never actually fire: baseEvidence's
// fixtures always marshal.
func evaluate(t *testing.T, ev training.EvidenceBody, ref content.Reference, effective assessment.Rubric) []assessment.CriterionResult {
	t.Helper()
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal evidence fixture: %v", err)
	}
	results, err := Evaluator.Evaluate(raw, content.Body{Reference: ref}, effective, nil)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	return results
}

// baseEvidence is a minimal, valid closed-item evidence.schema.json body
// (dds_processing, pilot-shaped) with every timing milestone reached
// comfortably inside norm — individual tests override just the fields
// their rule cares about.
func baseEvidence() training.EvidenceBody {
	offeredAt := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	openedAt := offeredAt.Add(5 * time.Second)
	primaryAt := offeredAt.Add(10 * time.Second)
	closedAt := primaryAt.Add(20 * time.Second)
	openSeconds := 5.0
	primarySeconds := 10.0
	workSeconds := 20.0
	primaryStatus := content.ReactionAccepted
	return training.EvidenceBody{
		Schema:            "emsim/evidence/v1",
		ItemID:            uuid.New(),
		RunID:             uuid.New(),
		LessonID:          uuid.New(),
		TraineeID:         uuid.New(),
		ScenarioVersionID: uuid.New(),
		TargetService:     "dds_district",
		Timing:            training.EvidenceTiming{OpenS: 30, PrimaryS: 30, CompleteS: 180, OpenAnchor: "offered_at", PrimaryAnchor: "offered_at", CompleteAnchor: "primary_at"},
		OfferedAt:         offeredAt,
		OpenedAt:          &openedAt,
		ClosedAt:          closedAt,
		CloseReason:       training.ClosePilotCompleted,
		FinalReaction:     content.ReactionAccepted,
		Mode:              training.ModeTraining,
		FinalCard: content.CardPreview{
			Address: content.Address{Street: "Чертановская улица", House: "58"},
		},
		PrimaryAt: &primaryAt,
		Deadlines: training.EvidenceDeadlines{OpenAt: offeredAt.Add(30 * time.Second), PrimaryAt: offeredAt.Add(30 * time.Second)},
		Derived: training.EvidenceDerived{
			OpenSeconds: &openSeconds, PrimarySeconds: &primarySeconds, WorkSeconds: &workSeconds,
			TotalSeconds: 25, PrimaryStatus: &primaryStatus, Chain: []content.Reaction{content.ReactionAccepted},
		},
		ExerciseType: content.ExerciseTypeDDSProcessing,
	}
}

func acceptedReference() content.Reference {
	return content.Reference{PrimaryDecision: content.PrimaryDecision{Status: content.ReactionAccepted}}
}

func TestTimingRules(t *testing.T) {
	rubric := effectiveRubric(t, nil)

	t.Run("within norm is met", func(t *testing.T) {
		ev := baseEvidence()
		results := evaluate(t, ev, acceptedReference(), rubric)
		for _, id := range []string{"T_OPEN", "T_PRIMARY", "T_COMPLETE"} {
			if r := findResult(t, results, id); r.Status != assessment.CriterionMet {
				t.Fatalf("%s = %+v, want met", id, r)
			}
		}
	})

	t.Run("beyond norm but within partial allowance is partial", func(t *testing.T) {
		ev := baseEvidence()
		open := 45.0 // > 30s norm, <= 60s partial_until_s
		ev.Derived.OpenSeconds = &open
		r := findResult(t, evaluate(t, ev, acceptedReference(), rubric), "T_OPEN")
		if r.Status != assessment.CriterionPartial || r.Score == nil || *r.Score != 0.5 {
			t.Fatalf("T_OPEN = %+v, want partial score 0.5", r)
		}
	})

	t.Run("beyond partial allowance is not_met", func(t *testing.T) {
		ev := baseEvidence()
		open := 120.0
		ev.Derived.OpenSeconds = &open
		r := findResult(t, evaluate(t, ev, acceptedReference(), rubric), "T_OPEN")
		if r.Status != assessment.CriterionNotMet {
			t.Fatalf("T_OPEN = %+v, want not_met", r)
		}
	})

	t.Run("never opened is not_met when not interrupted", func(t *testing.T) {
		ev := baseEvidence()
		ev.Derived.OpenSeconds = nil
		r := findResult(t, evaluate(t, ev, acceptedReference(), rubric), "T_OPEN")
		if r.Status != assessment.CriterionNotMet {
			t.Fatalf("T_OPEN = %+v, want not_met", r)
		}
	})

	t.Run("server restart marks every timing criterion not_applicable", func(t *testing.T) {
		ev := baseEvidence()
		ev.Interruptions = []training.Interruption{{RecoveryID: uuid.New(), Cause: "server_restart", DetectedAt: ev.ClosedAt}}
		results := evaluate(t, ev, acceptedReference(), rubric)
		for _, id := range []string{"T_OPEN", "T_PRIMARY", "T_COMPLETE"} {
			if r := findResult(t, results, id); r.Status != assessment.CriterionNotApplicable {
				t.Fatalf("%s = %+v, want not_applicable after server restart", id, r)
			}
		}
	})

	t.Run("stop before a milestone is not_applicable, reached milestones still judged", func(t *testing.T) {
		ev := baseEvidence()
		ev.CloseReason = training.CloseInterrupted
		ev.Derived.WorkSeconds = nil // never reached complete before stop
		results := evaluate(t, ev, acceptedReference(), rubric)
		if r := findResult(t, results, "T_OPEN"); r.Status != assessment.CriterionMet {
			t.Fatalf("T_OPEN = %+v, want met (reached before stop)", r)
		}
		if r := findResult(t, results, "T_COMPLETE"); r.Status != assessment.CriterionNotApplicable {
			t.Fatalf("T_COMPLETE = %+v, want not_applicable (never reached before stop)", r)
		}
	})
}

func TestPrimaryDecisionRule(t *testing.T) {
	rubric := effectiveRubric(t, nil)

	t.Run("matches reference", func(t *testing.T) {
		ev := baseEvidence()
		r := findResult(t, evaluate(t, ev, acceptedReference(), rubric), "D_PRIMARY")
		if r.Status != assessment.CriterionMet || r.Critical {
			t.Fatalf("D_PRIMARY = %+v, want met, not critical", r)
		}
	})

	t.Run("wrong decision on a profile-mismatch case is critical", func(t *testing.T) {
		ev := baseEvidence()
		refused := content.ReactionRefused
		ev.Derived.PrimaryStatus = &refused
		r := findResult(t, evaluate(t, ev, acceptedReference(), rubric), "D_PRIMARY")
		if r.Status != assessment.CriterionNotMet || !r.Critical {
			t.Fatalf("D_PRIMARY = %+v, want not_met and critical (refused_profile_incident)", r)
		}
	})

	t.Run("never decided and not interrupted is not_met", func(t *testing.T) {
		ev := baseEvidence()
		ev.Derived.PrimaryStatus = nil
		r := findResult(t, evaluate(t, ev, acceptedReference(), rubric), "D_PRIMARY")
		if r.Status != assessment.CriterionNotMet {
			t.Fatalf("D_PRIMARY = %+v, want not_met", r)
		}
	})

	t.Run("never decided but interrupted is not_applicable", func(t *testing.T) {
		ev := baseEvidence()
		ev.Derived.PrimaryStatus = nil
		ev.CloseReason = training.CloseInterrupted
		r := findResult(t, evaluate(t, ev, acceptedReference(), rubric), "D_PRIMARY")
		if r.Status != assessment.CriterionNotApplicable {
			t.Fatalf("D_PRIMARY = %+v, want not_applicable", r)
		}
	})
}

func TestCommentRequiredRule(t *testing.T) {
	rubric := effectiveRubric(t, nil)

	t.Run("not required is not_applicable", func(t *testing.T) {
		ev := baseEvidence()
		r := findResult(t, evaluate(t, ev, acceptedReference(), rubric), "D_COMMENT_REQUIRED")
		if r.Status != assessment.CriterionNotApplicable {
			t.Fatalf("D_COMMENT_REQUIRED = %+v, want not_applicable", r)
		}
	})

	t.Run("required and present is met", func(t *testing.T) {
		ev := baseEvidence()
		ev.Comments = []training.EvidenceComment{{Seq: 1, Text: "не наша территория"}}
		ref := acceptedReference()
		ref.PrimaryDecision.CommentRequired = true
		r := findResult(t, evaluate(t, ev, ref, rubric), "D_COMMENT_REQUIRED")
		if r.Status != assessment.CriterionMet {
			t.Fatalf("D_COMMENT_REQUIRED = %+v, want met", r)
		}
	})

	t.Run("required and absent is not_met", func(t *testing.T) {
		ev := baseEvidence()
		ref := acceptedReference()
		ref.PrimaryDecision.CommentRequired = true
		r := findResult(t, evaluate(t, ev, ref, rubric), "D_COMMENT_REQUIRED")
		if r.Status != assessment.CriterionNotMet {
			t.Fatalf("D_COMMENT_REQUIRED = %+v, want not_met", r)
		}
	})
}

func TestFieldCorrectionsRule(t *testing.T) {
	rubric := effectiveRubric(t, nil)

	t.Run("no corrections required is not_applicable", func(t *testing.T) {
		ev := baseEvidence()
		r := findResult(t, evaluate(t, ev, acceptedReference(), rubric), "D_FIELD_CORRECTIONS")
		if r.Status != assessment.CriterionNotApplicable {
			t.Fatalf("D_FIELD_CORRECTIONS = %+v, want not_applicable", r)
		}
	})

	t.Run("corrected value and confirmed action is met", func(t *testing.T) {
		ev := baseEvidence()
		ev.FinalCard.Address.Okrug = "ЮАО"
		ev.Actions = []training.EvidenceAction{
			{ActionID: uuid.New(), Accepted: true, Type: training.CommandSetCardField, Effect: map[string]any{"path": "/card/address/okrug", "old": "ЮАР", "new": "ЮАО"}},
		}
		ref := acceptedReference()
		ref.FieldCorrections = []content.FieldCorrection{{Path: "/card/address/okrug", ExpectedValue: "ЮАО", BeforeStatus: content.ReactionAccepted}}
		r := findResult(t, evaluate(t, ev, ref, rubric), "D_FIELD_CORRECTIONS")
		if r.Status != assessment.CriterionMet || len(r.EvidenceRefs) != 1 {
			t.Fatalf("D_FIELD_CORRECTIONS = %+v, want met with one evidence ref", r)
		}
	})

	t.Run("uncorrected value is not_met", func(t *testing.T) {
		ev := baseEvidence()
		ev.FinalCard.Address.Okrug = "ЮАР"
		ref := acceptedReference()
		ref.FieldCorrections = []content.FieldCorrection{{Path: "/card/address/okrug", ExpectedValue: "ЮАО", BeforeStatus: content.ReactionAccepted}}
		r := findResult(t, evaluate(t, ev, ref, rubric), "D_FIELD_CORRECTIONS")
		if r.Status != assessment.CriterionNotMet {
			t.Fatalf("D_FIELD_CORRECTIONS = %+v, want not_met", r)
		}
	})

	t.Run("correct final value without a confirming action is not_met", func(t *testing.T) {
		ev := baseEvidence()
		ev.FinalCard.Address.Okrug = "ЮАО" // e.g. authored correctly from the start, never actually corrected
		ref := acceptedReference()
		ref.FieldCorrections = []content.FieldCorrection{{Path: "/card/address/okrug", ExpectedValue: "ЮАО", BeforeStatus: content.ReactionAccepted}}
		r := findResult(t, evaluate(t, ev, ref, rubric), "D_FIELD_CORRECTIONS")
		if r.Status != assessment.CriterionNotMet {
			t.Fatalf("D_FIELD_CORRECTIONS = %+v, want not_met (no set_card_field action)", r)
		}
	})
}

func TestSequenceRule(t *testing.T) {
	rubric := effectiveRubric(t, nil)

	t.Run("empty expected chain is not_applicable", func(t *testing.T) {
		ev := baseEvidence()
		r := findResult(t, evaluate(t, ev, acceptedReference(), rubric), "S_SEQUENCE")
		if r.Status != assessment.CriterionNotApplicable {
			t.Fatalf("S_SEQUENCE = %+v, want not_applicable", r)
		}
	})

	t.Run("full expected chain observed in order is met", func(t *testing.T) {
		ev := baseEvidence()
		ev.Derived.Chain = []content.Reaction{content.ReactionAccepted, content.ReactionResponding, content.ReactionArrived, content.ReactionCompleted}
		ref := acceptedReference()
		ref.ExpectedChain = []content.Reaction{content.ReactionResponding, content.ReactionArrived, content.ReactionCompleted}
		r := findResult(t, evaluate(t, ev, ref, rubric), "S_SEQUENCE")
		if r.Status != assessment.CriterionMet {
			t.Fatalf("S_SEQUENCE = %+v, want met", r)
		}
	})

	t.Run("partial chain is partial with a fractional score", func(t *testing.T) {
		ev := baseEvidence()
		ev.Derived.Chain = []content.Reaction{content.ReactionAccepted, content.ReactionResponding}
		ref := acceptedReference()
		ref.ExpectedChain = []content.Reaction{content.ReactionResponding, content.ReactionArrived, content.ReactionCompleted}
		r := findResult(t, evaluate(t, ev, ref, rubric), "S_SEQUENCE")
		if r.Status != assessment.CriterionPartial || r.Score == nil || *r.Score < 0.32 || *r.Score > 0.34 {
			t.Fatalf("S_SEQUENCE = %+v, want partial ~1/3", r)
		}
	})

	t.Run("no expected transition observed is not_met", func(t *testing.T) {
		ev := baseEvidence()
		ev.Derived.Chain = []content.Reaction{content.ReactionAccepted}
		ref := acceptedReference()
		ref.ExpectedChain = []content.Reaction{content.ReactionResponding}
		r := findResult(t, evaluate(t, ev, ref, rubric), "S_SEQUENCE")
		if r.Status != assessment.CriterionNotMet {
			t.Fatalf("S_SEQUENCE = %+v, want not_met", r)
		}
	})
}

func requiredCallEvidence(acceptedBy, summary *string) training.EvidenceBody {
	ev := baseEvidence()
	ev.Calls = []training.EvidenceCall{{
		CallID: uuid.New(), ContactKey: "crew_leader", StartedAt: ev.OfferedAt,
		EndedAt: &ev.ClosedAt, AcceptedBy: acceptedBy, Summary: summary,
	}}
	return ev
}

func requiredCallReference() content.Reference {
	ref := acceptedReference()
	ref.Call = content.Call{Required: true, To: "crew_leader", BeforeStatus: content.ReactionAccepted}
	return ref
}

func strPtr(s string) *string { return &s }

func TestCallRules(t *testing.T) {
	rubric := effectiveRubric(t, nil)

	t.Run("not required is not_applicable for both call criteria", func(t *testing.T) {
		ev := baseEvidence()
		results := evaluate(t, ev, acceptedReference(), rubric)
		for _, id := range []string{"C_CALL_MADE", "C_CALL_LOG"} {
			if r := findResult(t, results, id); r.Status != assessment.CriterionNotApplicable {
				t.Fatalf("%s = %+v, want not_applicable", id, r)
			}
		}
	})

	t.Run("required, completed, and logged is met for both", func(t *testing.T) {
		ev := requiredCallEvidence(strPtr("дежурный по бригаде"), strPtr("адрес и тип происшествия переданы"))
		results := evaluate(t, ev, requiredCallReference(), rubric)
		for _, id := range []string{"C_CALL_MADE", "C_CALL_LOG"} {
			if r := findResult(t, results, id); r.Status != assessment.CriterionMet {
				t.Fatalf("%s = %+v, want met", id, r)
			}
		}
	})

	t.Run("required but never completed is not_met for both", func(t *testing.T) {
		ev := baseEvidence()
		results := evaluate(t, ev, requiredCallReference(), rubric)
		for _, id := range []string{"C_CALL_MADE", "C_CALL_LOG"} {
			if r := findResult(t, results, id); r.Status != assessment.CriterionNotMet {
				t.Fatalf("%s = %+v, want not_met", id, r)
			}
		}
	})

	t.Run("an incoming call from the required contact is not the required call (ADR-031)", func(t *testing.T) {
		ev := requiredCallEvidence(strPtr("дежурный"), strPtr("адрес передан"))
		ev.Calls[0].Direction, ev.Calls[0].EventKey = training.CallIncoming, "e2"
		results := evaluate(t, ev, requiredCallReference(), rubric)
		if r := findResult(t, results, "C_CALL_MADE"); r.Status != assessment.CriterionNotMet {
			t.Fatalf("C_CALL_MADE = %+v, want not_met", r)
		}
	})

	t.Run("completed but log incomplete is met for made, not_met for log", func(t *testing.T) {
		ev := requiredCallEvidence(strPtr("дежурный"), nil)
		results := evaluate(t, ev, requiredCallReference(), rubric)
		if r := findResult(t, results, "C_CALL_MADE"); r.Status != assessment.CriterionMet {
			t.Fatalf("C_CALL_MADE = %+v, want met", r)
		}
		if r := findResult(t, results, "C_CALL_LOG"); r.Status != assessment.CriterionNotMet {
			t.Fatalf("C_CALL_LOG = %+v, want not_met", r)
		}
	})
}

func TestAddressRule(t *testing.T) {
	rubric := effectiveRubric(t, nil)

	t.Run("no mention at all is not_applicable", func(t *testing.T) {
		ev := baseEvidence()
		r := findResult(t, evaluate(t, ev, acceptedReference(), rubric), "G_ADDRESS")
		if r.Status != assessment.CriterionNotApplicable {
			t.Fatalf("G_ADDRESS = %+v, want not_applicable", r)
		}
	})

	t.Run("matching street and house is met", func(t *testing.T) {
		ev := baseEvidence()
		ev.Comments = []training.EvidenceComment{{Seq: 1, Text: "Чертановская улица, дом 58, дерево упало"}}
		r := findResult(t, evaluate(t, ev, acceptedReference(), rubric), "G_ADDRESS")
		if r.Status != assessment.CriterionMet {
			t.Fatalf("G_ADDRESS = %+v, want met", r)
		}
	})

	t.Run("street matches but a different house number is not_met", func(t *testing.T) {
		ev := baseEvidence()
		ev.Comments = []training.EvidenceComment{{Seq: 1, Text: "Чертановская улица, дом 12"}}
		r := findResult(t, evaluate(t, ev, acceptedReference(), rubric), "G_ADDRESS")
		if r.Status != assessment.CriterionNotMet {
			t.Fatalf("G_ADDRESS = %+v, want not_met", r)
		}
	})

	t.Run("a bare number with no street context is unavailable, never a guess", func(t *testing.T) {
		ev := baseEvidence()
		ev.Comments = []training.EvidenceComment{{Seq: 1, Text: "2 пострадавших, бригада выехала"}}
		r := findResult(t, evaluate(t, ev, acceptedReference(), rubric), "G_ADDRESS")
		if r.Status != assessment.CriterionUnavailable {
			t.Fatalf("G_ADDRESS = %+v, want unavailable", r)
		}
	})
}

func TestLLMCriteriaAreUnavailableOrNotApplicable(t *testing.T) {
	rubric := effectiveRubric(t, nil)

	t.Run("no predicate set is not_applicable", func(t *testing.T) {
		ev := baseEvidence()
		results := evaluate(t, ev, acceptedReference(), rubric)
		if r := findResult(t, results, "D_COMMENT_CONTENT"); r.Status != assessment.CriterionNotApplicable {
			t.Fatalf("D_COMMENT_CONTENT = %+v, want not_applicable", r)
		}
		if r := findResult(t, results, "C_CALL_CONTENT"); r.Status != assessment.CriterionNotApplicable {
			t.Fatalf("C_CALL_CONTENT = %+v, want not_applicable", r)
		}
	})

	t.Run("predicate set is unavailable (JUDGE=off)", func(t *testing.T) {
		ev := baseEvidence()
		ref := acceptedReference()
		ref.PrimaryDecision.CommentMustMention = []string{"адрес"}
		ref.Call = content.Call{Required: true, To: "crew_leader"}
		results := evaluate(t, ev, ref, rubric)
		for _, id := range []string{"D_COMMENT_CONTENT", "C_CALL_CONTENT", "C_CALL_LOG_CONTENT", "G_GRAMMAR"} {
			if r := findResult(t, results, id); r.Status != assessment.CriterionUnavailable {
				t.Fatalf("%s = %+v, want unavailable", id, r)
			}
		}
	})
}

func TestEvaluateReturnsOneResultPerEffectiveCriterion(t *testing.T) {
	base, err := assessment.LoadDefault()
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	scoring := &content.Scoring{Disabled: []string{"G_GRAMMAR"}}
	rubric := assessment.Merge(base, scoring)
	results := evaluate(t, baseEvidence(), acceptedReference(), rubric)
	if len(results) != len(rubric.Criteria) {
		t.Fatalf("results = %d, want %d (one per effective criterion)", len(results), len(rubric.Criteria))
	}
	for _, r := range results {
		if r.ID == "G_GRAMMAR" {
			t.Fatal("disabled criterion must not appear in results at all")
		}
	}
}
