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

// TestRenderPDFIncludesDDSCardStatus is ДДС-3/ADR-032: a DDS row's own
// card status renders without breaking the document (RenderPDF doesn't
// expose text content to assert on directly, matching this package's
// other PDF tests).
func TestRenderPDFIncludesDDSCardStatus(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	score := 82.0
	snapshot := Snapshot{Version: 1, CapturedAt: now, Report: LessonReport{Lesson: Lesson{ID: uuid.New(), Title: "ДДС", FinishedAt: now}, Items: []ReportItem{{
		ItemResult: ItemResult{ItemID: uuid.New(), AssessmentStatus: AssessmentReady, Score: &score, ItemState: "closed", CardNumber: "К-1", CardStatus: CardCompleted},
		UserID:     uuid.New(), FullName: "Петров", WorkstationNo: 1, ScenarioTitle: "Упавшее дерево", Ordinal: 1, Level: "easy",
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

// TestRenderPDFIncludesCommentErrorCount is ДДС-4/ADR-034: an item with
// a grammar verdict renders its error count without breaking the
// document (RenderPDF exposes no text to assert on, like the other PDF
// tests here).
func TestRenderPDFIncludesCommentErrorCount(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	score := 91.0
	errs := 2
	snapshot := Snapshot{Version: 1, CapturedAt: now, Report: LessonReport{Lesson: Lesson{ID: uuid.New(), Title: "ДДС", FinishedAt: now}, Items: []ReportItem{{
		ItemResult: ItemResult{ItemID: uuid.New(), AssessmentStatus: AssessmentReady, Score: &score, ItemState: "closed", CardNumber: "К-1", CardStatus: CardCompleted},
		UserID:     uuid.New(), FullName: "Иванов Иван", WorkstationNo: 2, ScenarioTitle: "Дерево", Ordinal: 1, Level: "easy", CommentErrors: &errs,
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

func TestLessonSettingsLine(t *testing.T) {
	threshold := 85.0
	for name, tc := range map[string]struct {
		lesson Lesson
		want   string
	}{
		"no settings": {Lesson{}, ""},
		"timing only": {Lesson{Timing: &LessonTiming{OpenS: 30, PrimaryS: 45, CompleteS: 180}}, "Нормативы: открытие 30 с, решение 45 с, отработка 180 с"},
		"custom scoring": {
			Lesson{Timing: &LessonTiming{OpenS: 30, PrimaryS: 30, CompleteS: 180}, PassThreshold: &threshold, CustomWeights: true},
			"Нормативы: открытие 30 с, решение 30 с, отработка 180 с; порог зачёта 85 (задан преподавателем); веса критериев изменены преподавателем",
		},
	} {
		if got := lessonSettingsLine(tc.lesson); got != tc.want {
			t.Errorf("%s: got %q want %q", name, got, tc.want)
		}
	}
}

func TestRenderPDFWithLessonSettings(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	threshold := 90.0
	snapshot := Snapshot{Version: 1, CapturedAt: now, Report: LessonReport{Lesson: Lesson{ID: uuid.New(), Title: "ДДС", FinishedAt: now,
		Timing: &LessonTiming{OpenS: 30, PrimaryS: 45, CompleteS: 240}, PassThreshold: &threshold, CustomWeights: true}}}
	body, err := RenderPDF(snapshot, now)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(body, []byte("%PDF-")) {
		t.Fatalf("not a PDF: %q", body[:min(len(body), 12)])
	}
}
