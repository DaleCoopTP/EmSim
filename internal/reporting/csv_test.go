package reporting

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestCSVBOMSemicolonAndFormulaProtection(t *testing.T) {
	body, err := CSV(LessonReport{Items: []ReportItem{{ItemResult: ItemResult{AssessmentStatus: AssessmentPending}, FullName: "=danger", ScenarioTitle: "Тест", UserID: uuid.New()}}})
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	if !strings.HasPrefix(got, "\ufeffФИО;РМ") || !strings.Contains(got, "'=danger") || !strings.Contains(got, "\r\n") {
		t.Fatalf("csv = %q", got)
	}
}

func TestCSVIncludesOperator112BlocksAndPenalties(t *testing.T) {
	points := 17.5
	penalty := 5.0
	body, err := CSV(LessonReport{Items: []ReportItem{{
		ItemResult: ItemResult{AssessmentStatus: AssessmentReady},
		FullName:   "Петров",
		UserID:     uuid.New(),
		IntakeBlocks: []IntakeBlockScore{
			{CriterionID: "ADDRESS_FIELDS", Label: "Адрес", Points: &points, MaxPoints: 35},
			{CriterionID: "PROFILE_CARDS", Label: "Профильные карты", Points: nil, MaxPoints: 25},
		},
		IntakePenaltyTotal: &penalty,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	if !strings.Contains(got, "Блоки 112;Штрафы 112") {
		t.Fatalf("csv header missing 112 columns: %q", got)
	}
	if !strings.Contains(got, "Адрес: 17.5/35 | Профильные карты: -/25;5") {
		t.Fatalf("csv row missing 112 breakdown: %q", got)
	}
}
