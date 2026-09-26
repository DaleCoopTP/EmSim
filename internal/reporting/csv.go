package reporting

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
)

func CSV(report LessonReport) ([]byte, error) {
	var out bytes.Buffer
	out.Write([]byte{0xef, 0xbb, 0xbf})
	w := csv.NewWriter(&out)
	w.Comma = ';'
	w.UseCRLF = true
	// 112-6/ADR-026: two extra trailing columns beyond DDS's own
	// unchanged set, empty for every non-operator112_intake row.
	if err := w.Write([]string{"ФИО", "РМ", "Сценарий", "Карточка", "Порядок", "Состояние карточки", "Состояние оценки", "Оценщик", "Балл", "Зачёт", "Уровень", "Открытие, с", "Работа, с", "Всего, с", "Ошибки", "Блоки 112", "Штрафы 112"}); err != nil {
		return nil, err
	}
	for _, item := range report.Items {
		if err := w.Write([]string{cell(item.FullName), strconv.Itoa(item.WorkstationNo), cell(item.ScenarioTitle), cell(item.CardNumber), strconv.Itoa(item.Ordinal), item.ItemState, string(item.AssessmentStatus), nullable(item.AssessmentKind), number(item.Score), boolValue(item.Passed), item.Level, number(item.OpenSeconds), number(item.WorkSeconds), number(item.TotalSeconds), cell(errorText(item.Errors)), cell(intakeBlocksText(item.IntakeBlocks)), number(item.IntakePenaltyTotal)}); err != nil {
			return nil, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func cell(value string) string {
	if value != "" && strings.ContainsRune("=+-@", []rune(value)[0]) {
		return "'" + value
	}
	return value
}
func nullable(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
func number(value *float64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatFloat(*value, 'f', -1, 64)
}
func boolValue(value *bool) string {
	if value == nil {
		return ""
	}
	if *value {
		return "да"
	}
	return "нет"
}
func errorText(values []PublicError) string {
	texts := make([]string, 0, len(values))
	for _, value := range values {
		texts = append(texts, value.Label)
	}
	return strings.Join(texts, " | ")
}
func intakeBlocksText(blocks []IntakeBlockScore) string {
	texts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		points := "-"
		if block.Points != nil {
			points = strconv.FormatFloat(*block.Points, 'f', -1, 64)
		}
		texts = append(texts, fmt.Sprintf("%s: %s/%s", block.Label, points, strconv.FormatFloat(block.MaxPoints, 'f', -1, 64)))
	}
	return strings.Join(texts, " | ")
}
