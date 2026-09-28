package reporting

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"codeberg.org/go-pdf/fpdf"
	"golang.org/x/image/font/gofont/goregular"
)

// RenderPDF emits an A4 landscape report. The caller supplies generatedAt so
// a retry never silently changes the rendered document's visible timestamp.
func RenderPDF(snapshot Snapshot, generatedAt time.Time) ([]byte, error) {
	pdf := fpdf.New("L", "mm", "A4", "")
	pdf.SetCreationDate(generatedAt.UTC())
	pdf.SetCatalogSort(true)
	// FPDF restores Y to the top margin after invoking the header callback.
	// Keep a dedicated 20 mm body clearance, and place the header itself at
	// 10 mm, so the summary cannot overlap the document title.
	pdf.SetMargins(10, 30, 10)
	pdf.SetAutoPageBreak(true, 14)
	pdf.AddUTF8FontFromBytes("Go", "", goregular.TTF)
	pdf.SetHeaderFuncMode(func() {
		pdf.SetY(10)
		pdf.SetFont("Go", "", 13)
		pdf.CellFormat(0, 7, "EmSim - отчёт занятия: "+snapshot.Report.Lesson.Title, "", 1, "L", false, 0, "")
		pdf.SetFont("Go", "", 7)
		pdf.CellFormat(0, 4, "Основание зафиксировано: "+snapshot.CapturedAt.Local().Format("02.01.2006 15:04:05 MST"), "", 1, "L", false, 0, "")
		pdf.Ln(2)
	}, true)
	pdf.SetFooterFunc(func() {
		pdf.SetY(-10)
		pdf.SetFont("Go", "", 7)
		pdf.CellFormat(0, 4, fmt.Sprintf("Сформировано: %s   Страница %d/{nb}", generatedAt.Local().Format("02.01.2006 15:04:05 MST"), pdf.PageNo()), "T", 0, "R", false, 0, "")
	})
	pdf.AliasNbPages("{nb}")
	pdf.AddPage()

	pdf.SetFont("Go", "", 9)
	pdf.CellFormat(0, 5, fmt.Sprintf("Карточек: %d   Готовых оценок: %d   Требуют проверки: %d   Прервано: %d", len(snapshot.Report.Items), snapshot.Report.Aggregates.ReadyAssessments, snapshot.Report.Aggregates.PendingAssessments, snapshot.Report.Aggregates.InterruptedItems), "", 1, "L", false, 0, "")
	if snapshot.Report.Aggregates.AvgScore != nil {
		pdf.CellFormat(0, 5, fmt.Sprintf("Средний балл по готовым оценкам: %.2f", *snapshot.Report.Aggregates.AvgScore), "", 1, "L", false, 0, "")
	}
	pdf.Ln(2)
	section(pdf, "Участники")
	for _, p := range snapshot.Report.Participants {
		line := fmt.Sprintf("%s | РМ %d | уровень %s | карточек %d | готово %d | проверка %d | прервано %d", p.FullName, p.WorkstationNo, p.Level, p.Items, p.ReadyAssessments, p.PendingAssessments, p.InterruptedItems)
		if p.AvgScore != nil {
			line += fmt.Sprintf(" | средний балл %.2f", *p.AvgScore)
		}
		paragraph(pdf, line)
	}
	pdf.Ln(1)
	section(pdf, "Карточки")
	for _, item := range snapshot.Report.Items {
		status := string(item.AssessmentStatus)
		if item.AssessmentKind != nil {
			status += " / " + *item.AssessmentKind
		}
		score := "-"
		if item.Score != nil {
			score = fmt.Sprintf("%.2f", *item.Score)
		}
		first := fmt.Sprintf("%s, РМ %d - %s, карточка %s (№%d)", item.FullName, item.WorkstationNo, item.ScenarioTitle, item.CardNumber, item.Ordinal)
		second := fmt.Sprintf("Карточка: %s | Оценка: %s | Балл: %s | Уровень: %s | Открытие: %s | Работа: %s | Всего: %s", item.ItemState, status, score, item.Level, seconds(item.OpenSeconds), seconds(item.WorkSeconds), seconds(item.TotalSeconds))
		// ДДС-3/ADR-032: empty for operator112_intake (no equivalent).
		if item.CardStatus != "" {
			second += " | Статус карточки: " + string(item.CardStatus)
		}
		paragraph(pdf, first)
		paragraph(pdf, second)
		if len(item.Errors) > 0 {
			paragraph(pdf, "Ошибки: "+errorText(item.Errors))
		}
		if len(item.IntakeBlocks) > 0 {
			paragraph(pdf, "Блоки 112: "+intakeBlocksText(item.IntakeBlocks))
		}
		if item.IntakePenaltyTotal != nil {
			paragraph(pdf, fmt.Sprintf("Штрафы 112: -%.2f", *item.IntakePenaltyTotal))
		}
		pdf.Ln(1)
	}
	if len(snapshot.Report.Aggregates.TopErrors) > 0 {
		section(pdf, "Частые ошибки")
		for _, e := range snapshot.Report.Aggregates.TopErrors {
			paragraph(pdf, fmt.Sprintf("%s: %d", e.CriterionID, e.Count))
		}
	}
	var out bytes.Buffer
	if err := pdf.Output(&out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func section(pdf *fpdf.Fpdf, title string) {
	pdf.SetFont("Go", "", 10)
	pdf.CellFormat(0, 5, title, "B", 1, "L", false, 0, "")
	pdf.SetFont("Go", "", 8)
}
func paragraph(pdf *fpdf.Fpdf, value string) {
	pdf.MultiCell(0, 4.2, strings.TrimSpace(value), "", "L", false)
}
func seconds(value *float64) string {
	if value == nil {
		return "-"
	}
	return fmt.Sprintf("%.1f с", *value)
}
