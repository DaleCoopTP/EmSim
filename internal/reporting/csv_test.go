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

// TestCSVIncludesDDSCardStatus is ДДС-3/ADR-032: a DDS row's own derived
// card status is a dedicated column, empty for a non-DDS row (checked by
// TestCSVIncludesOperator112BlocksAndPenalties below, which never sets
// CardStatus).
func TestCSVIncludesDDSCardStatus(t *testing.T) {
	body, err := CSV(LessonReport{Items: []ReportItem{{
		ItemResult: ItemResult{AssessmentStatus: AssessmentReady, CardStatus: CardCompleted},
		FullName:   "Сидоров",
		UserID:     uuid.New(),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	if !strings.Contains(got, "Состояние карточки;Статус карточки;Состояние оценки") {
		t.Fatalf("csv header missing card status column: %q", got)
	}
	if !strings.Contains(got, ";completed;") {
		t.Fatalf("csv row missing card status value: %q", got)
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

// TestCSVIncludesCommentErrorCount is ДДС-4/ADR-034: G_GRAMMAR's error
// count is the last column; empty when the item was not judged on it.
func TestCSVIncludesCommentErrorCount(t *testing.T) {
	errs := 3
	body, err := CSV(LessonReport{Items: []ReportItem{
		{ItemResult: ItemResult{AssessmentStatus: AssessmentReady, CardStatus: CardCompleted}, FullName: "Сидоров", UserID: uuid.New(), CommentErrors: &errs},
		{ItemResult: ItemResult{AssessmentStatus: AssessmentReady, CardStatus: CardCompleted}, FullName: "Кузнецов", UserID: uuid.New()},
	}})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(body), "\r\n"), "\r\n")
	if len(lines) != 3 {
		t.Fatalf("csv has %d lines, want header + 2 rows: %q", len(lines), body)
	}
	if !strings.HasSuffix(lines[0], ";Штрафы 112;Ошибок в комментариях") {
		t.Fatalf("header does not end with the comment errors column: %q", lines[0])
	}
	if !strings.HasSuffix(lines[1], ";3") {
		t.Fatalf("row with errors does not end with 3: %q", lines[1])
	}
	if !strings.HasSuffix(lines[2], ";;") {
		t.Fatalf("row without a grammar verdict must leave the column empty: %q", lines[2])
	}
	columns := len(strings.Split(lines[0], ";"))
	for _, line := range lines[1:] {
		if got := len(strings.Split(line, ";")); got != columns {
			t.Fatalf("row has %d columns, header has %d: %q", got, columns, line)
		}
	}
}
