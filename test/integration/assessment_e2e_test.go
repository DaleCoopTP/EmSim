//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestAssessmentPipelineThroughAPIAndWorker is C9's acceptance path: the
// browser-facing API closes a training card, a separate real worker process
// seals/promotes/evaluates it, and the instructor reads the review queue then
// creates an expert revision through the public API.  No in-process service
// calls stand in for either process boundary.
func TestAssessmentPipelineThroughAPIAndWorker(t *testing.T) {
	f := setupTrainingE2E(t)
	binary := filepath.Join(t.TempDir(), "emsim-worker")
	build := exec.CommandContext(f.ctx, "go", "build", "-o", binary, "./cmd/emsim")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build worker: %v\n%s", err, output)
	}
	worker := startWorkerProcess(t, binary, f.databaseURL, "worker", "assessment-e2e-worker")
	defer worker.stop(t, false)

	admin := newCookieClient(t)
	loginAdmin(t, f, admin)
	if response := jsonRequest(t, f.ctx, admin, f.baseURL, http.MethodPost, "/api/v1/admin/users", map[string]any{"login": "assessment-e2e-instructor", "password": "correct-horse-battery-staple", "full_name": "Инструктор оценки", "role": "instructor"}, nil); response.StatusCode != http.StatusCreated {
		t.Fatalf("create instructor = %d", response.StatusCode)
	}
	instructor := newCookieClient(t)
	if response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodPost, "/api/v1/auth/login", map[string]any{"login": "assessment-e2e-instructor", "password": "correct-horse-battery-staple"}, nil); response.StatusCode != http.StatusOK {
		t.Fatalf("instructor login = %d", response.StatusCode)
	}
	versionID := approvedVersionID(t, f, instructor, "pilot-tree-01")
	provisionWorkstations(t, f, admin, map[string]any{"number": 1, "label": "assessment-e2e"})
	trainee := createTrainee(t, f, admin, "assessment-e2e-trainee", 1)
	traineeID := currentUserID(t, f, trainee)
	lesson := runLesson(t, f, instructor, "E2E оценка", "training", 1, traineeID, versionID)
	itemID := currentItemID(t, f, trainee)
	if response, receipt := sendCommand(t, f, trainee, itemID, randomCommandID(), 0, "open", map[string]any{}); response.StatusCode != http.StatusOK || receipt.Outcome != "applied" {
		t.Fatalf("open = %d %+v", response.StatusCode, receipt)
	}
	if response, receipt := sendCommand(t, f, trainee, itemID, randomCommandID(), 1, "set_status", map[string]any{"status": "accepted"}); response.StatusCode != http.StatusOK || receipt.Outcome != "applied" {
		t.Fatalf("accept = %d %+v", response.StatusCode, receipt)
	}
	if response, receipt := sendCommand(t, f, trainee, itemID, randomCommandID(), 2, "close", map[string]any{}); response.StatusCode != http.StatusOK || receipt.ItemState != "closed" {
		t.Fatalf("close = %d %+v", response.StatusCode, receipt)
	}

	// pilot-tree-01 closes through the old pilot_completed path with no
	// crew reports/calls, so under dds/rubric-v2 (ДДС-3) T_PROGRESS/
	// S_SEQUENCE/D_COMMENT_REQUIRED/C_CALLS all resolve not_applicable
	// and only T_OPEN/T_PRIMARY/D_PRIMARY (all met) remain — a clean
	// ready with full score, unlike dds/rubric-v1 where G_GRAMMAR was
	// unconditionally unavailable under JUDGE=off and forced needs_review.
	detail := waitAssessmentDetail(t, f.ctx, instructor, f.baseURL, itemID)
	if detail.Final == nil || detail.Final.Kind != "auto" || detail.Final.Revision != 1 || detail.Final.Status != "ready" || detail.Final.Score == nil || *detail.Final.Score != 100 {
		t.Fatalf("automatic assessment = %+v, want auto rev=1 ready with score=100", detail.Final)
	}
	if detail.AutomaticState != "done" || len(detail.Final.Criteria) == 0 {
		t.Fatalf("detail automatic_state=%q criteria=%d, want done and rules", detail.AutomaticState, len(detail.Final.Criteria))
	}
	var rawRows json.RawMessage
	response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodGet, "/api/v1/lessons/"+lesson.ID+"/assessments", nil, &rawRows)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("assessment queue status=%d body=%s", response.StatusCode, rawRows)
	}
	var rows []assessmentListRow
	if err := json.Unmarshal(rawRows, &rows); err != nil || len(rows) != 1 || rows[0].ItemID != itemID || rows[0].Final == nil {
		t.Fatalf("assessment queue decode=%v rows=%+v", err, rows)
	}

	criteria := make([]map[string]any, 0, len(detail.Final.Criteria))
	for _, criterion := range detail.Final.Criteria {
		status := criterion.Status
		if status == "unavailable" {
			status = "met"
		}
		criteria = append(criteria, map[string]any{"id": criterion.ID, "status": status})
	}
	var expert assessmentDetailRevision
	if response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodPost, "/api/v1/items/"+itemID+"/assessment/revisions", map[string]any{"reason": "Эксперт завершил разбор закрытой карточки", "base_revision": 1, "criteria": criteria}, &expert); response.StatusCode != http.StatusCreated || expert.Kind != "expert" || expert.Revision != 2 || expert.Status != "ready" {
		t.Fatalf("create expert status=%d assessment=%+v", response.StatusCode, expert)
	}
}

type assessmentCriterion struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}
type assessmentDetailRevision struct {
	ID       string                `json:"id"`
	Revision int                   `json:"revision"`
	Kind     string                `json:"kind"`
	Status   string                `json:"status"`
	Score    *float64              `json:"score"`
	Criteria []assessmentCriterion `json:"criteria"`
}
type assessmentDetailResponse struct {
	AutomaticState string                    `json:"automatic_state"`
	Final          *assessmentDetailRevision `json:"final"`
}
type assessmentListRow struct {
	ItemID string                    `json:"item_id"`
	Final  *assessmentDetailRevision `json:"final"`
}

func waitAssessmentDetail(t *testing.T, ctx context.Context, client *http.Client, baseURL, itemID string) assessmentDetailResponse {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		var detail assessmentDetailResponse
		response := jsonRequest(t, ctx, client, baseURL, http.MethodGet, "/api/v1/items/"+itemID+"/assessment", nil, &detail)
		if response.StatusCode == http.StatusOK && detail.Final != nil {
			return detail
		}
		if time.Now().After(deadline) {
			t.Fatalf("assessment was not produced: status=%d detail=%+v", response.StatusCode, detail)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
