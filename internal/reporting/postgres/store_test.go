package postgres

import "testing"

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
