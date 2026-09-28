package postgres

import (
	"encoding/json"
	"strings"
	"testing"

	"emsim/internal/reporting"
)

func reportingStatus(s string) reporting.AssessmentStatus { return reporting.AssessmentStatus(s) }

// TestIntake112BreakdownRecognizesDescriptionContent is ADR-028's own
// regression test: DESCRIPTION_CONTENT (operator112/rubric-v3) must be
// recognized as a block the same way DESCRIPTION_PRESENT
// (operator112/rubric-v2) already is — same report column, same label
// ("Описание со слов заявителя"), regardless of which rubric version
// actually scored a given lesson. Before this ADR, criterionLabels/
// intake112BlockIDs only knew DESCRIPTION_PRESENT, so a judge-enabled
// lesson's own block would have silently disappeared from the report,
// CSV and PDF instead of showing its points.
func TestIntake112BreakdownRecognizesDescriptionContent(t *testing.T) {
	score := 0.8
	blocks, penaltyTotal := intake112Breakdown([]criterion{
		{ID: "DESCRIPTION_CONTENT", Status: "partial", Score: &score, Weight: 10},
	})
	if len(blocks) != 1 {
		t.Fatalf("blocks = %+v, want exactly one", blocks)
	}
	b := blocks[0]
	if b.CriterionID != "DESCRIPTION_CONTENT" || b.Label != "Описание со слов заявителя" || b.MaxPoints != 10 {
		t.Fatalf("unexpected block: %+v", b)
	}
	if b.Points == nil || *b.Points != 8 {
		t.Fatalf("Points = %v, want 8 (0.8 * 10)", b.Points)
	}
	if penaltyTotal != nil {
		t.Fatalf("penaltyTotal = %v, want nil (no penalty criteria present)", penaltyTotal)
	}
}

// TestIntake112BreakdownStillRecognizesDescriptionPresent guards the
// pre-ADR-028 rubric-v2 case against regressing while the v3 case above
// is added.
func TestIntake112BreakdownStillRecognizesDescriptionPresent(t *testing.T) {
	one := 1.0
	blocks, _ := intake112Breakdown([]criterion{
		{ID: "DESCRIPTION_PRESENT", Status: "met", Score: &one, Weight: 10},
	})
	if len(blocks) != 1 || blocks[0].CriterionID != "DESCRIPTION_PRESENT" || blocks[0].Label != "Описание со слов заявителя" {
		t.Fatalf("unexpected blocks: %+v", blocks)
	}
}

// TestIntake112BreakdownIgnoresDDSCriteria is intake112Breakdown's own
// doc comment, made explicit: a DDS row's criteria ids match neither
// intake112BlockIDs nor intake112PenaltyIDs, so both return values stay
// empty/nil for it.
func TestIntake112BreakdownIgnoresDDSCriteria(t *testing.T) {
	score := 1.0
	blocks, penaltyTotal := intake112Breakdown([]criterion{
		{ID: "T_OPEN", Status: "met", Score: &score, Weight: 20},
	})
	if len(blocks) != 0 || penaltyTotal != nil {
		t.Fatalf("blocks=%+v penaltyTotal=%v, want both empty for a DDS criterion", blocks, penaltyTotal)
	}
}

// TestCommentErrorCount is ДДС-4/ADR-034: the report's "Ошибок в
// комментариях" column reads G_GRAMMAR's own error count from the final
// assessment's criteria — only for an evaluated criterion of a ready
// assessment; nil for a rubric without it, not_applicable, unavailable,
// or an assessment that is not ready.
func TestCommentErrorCount(t *testing.T) {
	const withErrors = `[
	  {"id":"T_OPEN","status":"met","score":1,"weight":10},
	  {"id":"G_GRAMMAR","status":"partial","score":0.5,"weight":5,"details":[
	    {"key":"comment:2","status":"not_met","errors":[{"fragment":"а","correction":"б","kind":"spelling"},{"fragment":"в","correction":"г","kind":"grammar"}]},
	    {"key":"comment:4","status":"met"},
	    {"key":"comment:6","status":"not_met","errors":[{"fragment":"д","correction":"е","kind":"punctuation"}]}]}]`
	got := commentErrorCount([]byte(withErrors), "ready")
	if got == nil || *got != 3 {
		t.Fatalf("commentErrorCount = %v, want 3", got)
	}
	clean := commentErrorCount([]byte(`[{"id":"G_GRAMMAR","status":"met","score":1,"weight":5,"details":[{"key":"comment:2","status":"met"}]}]`), "ready")
	if clean == nil || *clean != 0 {
		t.Fatalf("a clean G_GRAMMAR gave %v, want 0", clean)
	}
	for name, tc := range map[string]struct {
		raw    string
		status string
	}{
		"no grammar criterion (rubric v2)": {`[{"id":"T_OPEN","status":"met","weight":10}]`, "ready"},
		"not applicable":                   {`[{"id":"G_GRAMMAR","status":"not_applicable","weight":5}]`, "ready"},
		"unavailable":                      {`[{"id":"G_GRAMMAR","status":"unavailable","weight":5}]`, "ready"},
		"assessment not ready":             {withErrors, "needs_review"},
		"malformed criteria":               {`not json`, "ready"},
		"expert revision without details":  {`[{"id":"G_GRAMMAR","status":"met","score":1,"weight":5}]`, "ready"},
	} {
		if got := commentErrorCount([]byte(tc.raw), reportingStatus(tc.status)); got != nil {
			t.Fatalf("%s: commentErrorCount = %d, want nil", name, *got)
		}
	}
}

// TestPublicErrorsNameJudgedDDSCriteria: a partial D_COMMENT_CONTENT or
// G_GRAMMAR reaches the trainee as a labelled error with the criterion's
// name only — never as a question or reference text.
func TestPublicErrorsNameJudgedDDSCriteria(t *testing.T) {
	criteria := []byte(`[
	  {"id":"D_COMMENT_CONTENT","status":"partial","score":0.5,"weight":15,"details":[{"key":"event:e3:0","label":"Комментарий к статусу «Проведение работ»","expected":"Комментарий сообщает: вызвана автовышка","actual":"Работы идут"}]},
	  {"id":"G_GRAMMAR","status":"not_met","score":0,"weight":5}]`)
	errs := publicErrors(criteria, []byte(`[]`), "ready")
	if len(errs) != 2 || errs[0].Label != "Содержание комментариев" || errs[1].Label != "Грамотность" {
		t.Fatalf("publicErrors = %+v", errs)
	}
	raw, err := json.Marshal(errs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "автовышка") {
		t.Fatalf("public errors leak the reference fact: %s", raw)
	}
}

func TestApplyLessonSettings(t *testing.T) {
	var lesson reporting.Lesson
	if err := applyLessonSettings(&lesson, []byte(`{"open_s":30,"primary_s":45,"complete_s":180}`), nil); err != nil {
		t.Fatal(err)
	}
	if lesson.Timing == nil || lesson.Timing.PrimaryS != 45 || lesson.PassThreshold != nil || lesson.CustomWeights {
		t.Fatalf("timing only: %+v", lesson)
	}

	lesson = reporting.Lesson{}
	if err := applyLessonSettings(&lesson, []byte(`{"open_s":30,"primary_s":30,"complete_s":180}`), []byte(`{"weights":{"T_OPEN":100},"pass_threshold":85}`)); err != nil {
		t.Fatal(err)
	}
	if lesson.PassThreshold == nil || *lesson.PassThreshold != 85 || !lesson.CustomWeights {
		t.Fatalf("custom scoring: %+v", lesson)
	}

	// A 112 lesson's zero timing is not a norm.
	lesson = reporting.Lesson{}
	if err := applyLessonSettings(&lesson, []byte(`{"open_s":0,"primary_s":0,"complete_s":0}`), nil); err != nil {
		t.Fatal(err)
	}
	if lesson.Timing != nil {
		t.Fatalf("zero timing must be omitted: %+v", lesson.Timing)
	}
}
