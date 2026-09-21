//go:build integration

package integration_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestReportingThroughAPIAndWorker covers slice 7's actual process
// boundary: an instructor reads a finished lesson, asks the API for a PDF,
// and the separate report worker materializes the immutable blob. It also
// locks the trainee-facing projection to safe result fields only.
func TestReportingThroughAPIAndWorker(t *testing.T) {
	f := setupTrainingE2E(t)
	binary := filepath.Join(t.TempDir(), "emsim-reporting-worker")
	build := exec.CommandContext(f.ctx, "go", "build", "-o", binary, "./cmd/emsim")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build worker: %v\n%s", err, output)
	}
	worker := startWorkerProcess(t, binary, f.databaseURL, "worker", "reporting-e2e-worker", f.blobRoot)
	defer worker.stop(t, false)

	admin := newCookieClient(t)
	loginAdmin(t, f, admin)
	if response := jsonRequest(t, f.ctx, admin, f.baseURL, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"login": "reporting-e2e-instructor", "password": "correct-horse-battery-staple", "full_name": "Инструктор отчётов", "role": "instructor",
	}, nil); response.StatusCode != http.StatusCreated {
		t.Fatalf("create instructor = %d", response.StatusCode)
	}
	instructor := newCookieClient(t)
	if response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"login": "reporting-e2e-instructor", "password": "correct-horse-battery-staple",
	}, nil); response.StatusCode != http.StatusOK {
		t.Fatalf("instructor login = %d", response.StatusCode)
	}
	versionID := approvedVersionID(t, f, instructor, "pilot-tree-01")
	provisionWorkstations(t, f, admin, map[string]any{"number": 1, "label": "reporting-e2e"})
	trainee := createTrainee(t, f, admin, "reporting-e2e-trainee", 1)
	traineeID := currentUserID(t, f, trainee)
	lesson := runLesson(t, f, instructor, "E2E отчёт", "training", 1, traineeID, versionID)
	itemID := currentItemID(t, f, trainee)
	for seq, command := range []struct {
		typ     string
		payload map[string]any
	}{{"open", map[string]any{}}, {"set_status", map[string]any{"status": "accepted"}}, {"close", map[string]any{}}} {
		response, receipt := sendCommand(t, f, trainee, itemID, randomCommandID(), seq, command.typ, command.payload)
		if response.StatusCode != http.StatusOK || receipt.Outcome != "applied" {
			t.Fatalf("%s = %d %+v", command.typ, response.StatusCode, receipt)
		}
	}

	// The automatic result can request expert review. Give the report a
	// final, numeric assessment so aggregation and a frozen provenance are
	// both exercised rather than treating an absent score as zero.
	detail := waitAssessmentDetail(t, f.ctx, instructor, f.baseURL, itemID)
	criteria := make([]map[string]any, 0, len(detail.Final.Criteria))
	for _, criterion := range detail.Final.Criteria {
		criteria = append(criteria, map[string]any{"id": criterion.ID, "status": "met"})
	}
	var expert assessmentDetailRevision
	response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodPost, "/api/v1/items/"+itemID+"/assessment/revisions", map[string]any{
		"reason": "Итоговая проверка для отчёта", "base_revision": detail.Final.Revision, "criteria": criteria,
	}, &expert)
	if response.StatusCode != http.StatusCreated || expert.Status != "ready" || expert.Score == nil {
		t.Fatalf("expert assessment = status %d, body %+v", response.StatusCode, expert)
	}

	var report reportResponse
	response = waitLessonReport(t, f, instructor, lesson.ID, &report)
	if response.StatusCode != http.StatusOK || report.Lesson.Title != "E2E отчёт" || len(report.Items) != 1 || report.Items[0].Score == nil {
		t.Fatalf("lesson report = status %d, body %+v", response.StatusCode, report)
	}
	if report.Aggregates.ReadyAssessments != 1 || report.Aggregates.AvgScore == nil {
		t.Fatalf("report aggregates = %+v", report.Aggregates)
	}

	// CSV is a synchronous view of the same report and must remain Excel
	// friendly even when a value happens to begin like a formula.
	req, err := http.NewRequestWithContext(f.ctx, http.MethodGet, f.baseURL+"/api/v1/lessons/"+lesson.ID+"/report.csv", nil)
	if err != nil {
		t.Fatal(err)
	}
	csvResponse, err := instructor.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	csvBody, err := io.ReadAll(csvResponse.Body)
	_ = csvResponse.Body.Close()
	if err != nil || csvResponse.StatusCode != http.StatusOK || !bytes.HasPrefix(csvBody, []byte{0xef, 0xbb, 0xbf}) || !strings.Contains(string(csvBody), "ФИО;") {
		t.Fatalf("csv = status %d body %q err %v", csvResponse.StatusCode, csvBody, err)
	}

	var requested reportFileResponse
	response = jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodPost, "/api/v1/lessons/"+lesson.ID+"/report.pdf", nil, &requested)
	if response.StatusCode != http.StatusAccepted || requested.ID == "" {
		t.Fatalf("request PDF = status %d body %+v", response.StatusCode, requested)
	}
	ready := waitReportFile(t, f, instructor, lesson.ID, requested.ID)
	if ready.Status != "ready" || ready.DownloadURL == "" || ready.GeneratedAt == "" {
		t.Fatalf("ready PDF = %+v", ready)
	}
	req, err = http.NewRequestWithContext(f.ctx, http.MethodGet, f.baseURL+ready.DownloadURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	pdfResponse, err := instructor.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	pdfBody, err := io.ReadAll(pdfResponse.Body)
	_ = pdfResponse.Body.Close()
	if err != nil || pdfResponse.StatusCode != http.StatusOK || !bytes.HasPrefix(pdfBody, []byte("%PDF-")) {
		t.Fatalf("download PDF = status %d bytes %d err %v", pdfResponse.StatusCode, len(pdfBody), err)
	}

	var rawResults []map[string]json.RawMessage
	response = jsonRequest(t, f.ctx, trainee, f.baseURL, http.MethodGet, "/api/v1/my/results", nil, &rawResults)
	if response.StatusCode != http.StatusOK || len(rawResults) != 1 {
		t.Fatalf("my results = status %d body %+v", response.StatusCode, rawResults)
	}
	for _, forbidden := range []string{"reference", "evidence", "rubric", "reason", "explanation"} {
		if _, found := rawResults[0][forbidden]; found {
			t.Fatalf("my results leaked %q: %s", forbidden, rawResults[0][forbidden])
		}
	}
	var progress struct {
		CompletedItems int      `json:"completed_items"`
		AvgScore       *float64 `json:"avg_score"`
	}
	response = jsonRequest(t, f.ctx, trainee, f.baseURL, http.MethodGet, "/api/v1/my/progress", nil, &progress)
	if response.StatusCode != http.StatusOK || progress.CompletedItems != 1 || progress.AvgScore == nil {
		t.Fatalf("my progress = status %d body %+v", response.StatusCode, progress)
	}
}

type reportResponse struct {
	Lesson struct {
		Title string `json:"title"`
	} `json:"lesson"`
	Items []struct {
		Score *float64 `json:"score"`
	} `json:"items"`
	Aggregates struct {
		ReadyAssessments int      `json:"ready_assessments"`
		AvgScore         *float64 `json:"avg_score"`
	} `json:"aggregates"`
}

type reportFileResponse struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	GeneratedAt string `json:"generated_at"`
	DownloadURL string `json:"download_url"`
}

func waitLessonReport(t *testing.T, f trainingE2EFixture, client *http.Client, lessonID string, report *reportResponse) *http.Response {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		response := jsonRequest(t, f.ctx, client, f.baseURL, http.MethodGet, "/api/v1/lessons/"+lessonID+"/report", nil, report)
		if response.StatusCode == http.StatusOK {
			return response
		}
		if time.Now().After(deadline) {
			t.Fatalf("lesson report did not become readable: status %d", response.StatusCode)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func waitReportFile(t *testing.T, f trainingE2EFixture, client *http.Client, lessonID, reportID string) reportFileResponse {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		var files []reportFileResponse
		response := jsonRequest(t, f.ctx, client, f.baseURL, http.MethodGet, "/api/v1/lessons/"+lessonID+"/report-files", nil, &files)
		if response.StatusCode == http.StatusOK {
			for _, file := range files {
				if file.ID == reportID && file.Status == "ready" {
					return file
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("report file %s did not become ready", reportID)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
