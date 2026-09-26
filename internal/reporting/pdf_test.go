package reporting

import (
	"bytes"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRenderPDFProducesPDFWithCyrillicAndPages(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	score := 87.5
	snapshot := Snapshot{Version: 1, CapturedAt: now, Report: LessonReport{Lesson: Lesson{ID: uuid.New(), Title: "Проверка дерева", FinishedAt: now}, Items: []ReportItem{{ItemResult: ItemResult{ItemID: uuid.New(), AssessmentStatus: AssessmentReady, Score: &score, ItemState: "closed", CardNumber: "К-1"}, UserID: uuid.New(), FullName: "Иванов Иван", WorkstationNo: 2, ScenarioTitle: "Упавшее дерево", Ordinal: 1, Level: "easy"}}}}
	snapshot.Report.Participants, snapshot.Report.Aggregates = Enrich(snapshot.Report.Items)
	body, err := RenderPDF(snapshot, now)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(body, []byte("%PDF-")) || !bytes.Contains(body, []byte("%%EOF")) {
		t.Fatalf("not a PDF: %q", body[:min(len(body), 12)])
	}
}

func TestRenderPDFIncludesOperator112BlocksAndPenalties(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	score := 78.65
	points := 17.5
	penalty := 5.0
	snapshot := Snapshot{Version: 1, CapturedAt: now, Report: LessonReport{Lesson: Lesson{ID: uuid.New(), Title: "112", FinishedAt: now}, Items: []ReportItem{{
		ItemResult: ItemResult{ItemID: uuid.New(), AssessmentStatus: AssessmentReady, Score: &score, ItemState: "closed", CardNumber: "К-1"},
		UserID:     uuid.New(), FullName: "Сидоров", WorkstationNo: 3, ScenarioTitle: "Оператор 112", Ordinal: 1, Level: "easy",
		IntakeBlocks:       []IntakeBlockScore{{CriterionID: "ADDRESS_FIELDS", Label: "Адрес", Points: &points, MaxPoints: 35}},
		IntakePenaltyTotal: &penalty,
	}}}}
	snapshot.Report.Participants, snapshot.Report.Aggregates = Enrich(snapshot.Report.Items)
	body, err := RenderPDF(snapshot, now)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(body, []byte("%PDF-")) || !bytes.Contains(body, []byte("%%EOF")) {
		t.Fatalf("not a PDF: %q", body[:min(len(body), 12)])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
