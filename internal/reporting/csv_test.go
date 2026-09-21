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
