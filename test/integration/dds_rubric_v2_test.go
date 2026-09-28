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

	pgstore "emsim/internal/platform/postgres"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestDDSRubricV2ThroughAPIAndWorker is ДДС-3's own acceptance path
// (ADR-032) over real api and worker processes: a newly created DDS
// lesson freezes dds/rubric-v2 and its auto assessment actually scores
// T_PROGRESS/S_SEQUENCE/C_CALLS from the crew reports the trainee
// reacted to (met after makeDue delivers them, so the timing pointers
// have something real to judge) — while a lesson whose own rubric_
// version was already frozen on dds/rubric-v1 before this slice (a
// direct DB update stands in for "created before ДДС-3 shipped") keeps
// scoring against v1: C_CALL_MADE/C_CALL_LOG appear, T_PROGRESS does not.
func TestDDSRubricV2ThroughAPIAndWorker(t *testing.T) {
	f := setupTrainingE2E(t)
	binary := filepath.Join(t.TempDir(), "emsim-worker")
	build := exec.CommandContext(f.ctx, "go", "build", "-o", binary, "./cmd/emsim")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build worker: %v\n%s", err, output)
	}
	worker := startWorkerProcess(t, binary, f.databaseURL, "worker", "dds-rubric-v2-worker")
	defer worker.stop(t, false)

	ctx, cancel := context.WithTimeout(f.ctx, 2*time.Minute)
	defer cancel()
	pool, err := pgstore.Open(ctx, f.databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	admin := newCookieClient(t)
	loginAdmin(t, f, admin)
	if response := jsonRequest(t, f.ctx, admin, f.baseURL, http.MethodPost, "/api/v1/admin/users", map[string]any{"login": "dds-rubric-v2-instructor", "password": "correct-horse-battery-staple", "full_name": "Инструктор ДДС", "role": "instructor"}, nil); response.StatusCode != http.StatusCreated {
		t.Fatalf("create instructor = %d", response.StatusCode)
	}
	instructor := newCookieClient(t)
	if response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodPost, "/api/v1/auth/login", map[string]any{"login": "dds-rubric-v2-instructor", "password": "correct-horse-battery-staple"}, nil); response.StatusCode != http.StatusOK {
		t.Fatalf("instructor login = %d", response.StatusCode)
	}
	provisionWorkstations(t, f, admin,
		map[string]any{"number": 1, "label": "dds-rubric-v2"},
		map[string]any{"number": 2, "label": "dds-rubric-v1-frozen"})

	// version 2 of this scenario key (ДДС-2/ADR-031): crew reports
	// anchored on since="call_ended", e2/e4 delivered as phone_incoming —
	// the only version still "approved" (version 1 is superseded once a
	// newer version of the same scenario key ships, so it can no longer
	// be assigned to a lesson).
	v2 := versionIDByNumber(t, f, instructor, "dds-district-tree-cycle-01", 2)

	runDistrictCycle := func(label string, workstation int) (itemID string) {
		trainee := createTraineeWithService(t, f, admin, label, workstation, "dds_district_chertanovo")
		runLesson(t, f, instructor, "ДДС "+label, "training", workstation, currentUserID(t, f, trainee), v2)
		itemID = currentItemID(t, f, trainee)
		seq := 0
		send := func(cmdType string, payload map[string]any) (*http.Response, receiptResponse) {
			t.Helper()
			response, receipt := sendCommand(t, f, trainee, itemID, randomCommandID(), seq, cmdType, payload)
			if receipt.Outcome == "applied" && !receipt.Replayed {
				seq = receipt.Seq
			}
			return response, receipt
		}
		apply := func(cmdType string, payload map[string]any) receiptResponse {
			t.Helper()
			response, receipt := send(cmdType, payload)
			if response.StatusCode != http.StatusOK || receipt.Outcome != "applied" {
				t.Fatalf("%s %s %v = %d %+v", label, cmdType, payload, response.StatusCode, receipt)
			}
			return receipt
		}
		answer := func(eventKey string) receiptResponse {
			t.Helper()
			response, receipt := send("answer_incoming", map[string]any{"event_key": eventKey})
			if response.StatusCode != http.StatusOK || receipt.Outcome != "applied" {
				t.Fatalf("%s answer_incoming %s = %d %+v", label, eventKey, response.StatusCode, receipt)
			}
			return receipt
		}

		apply("open", map[string]any{})
		apply("set_status", map[string]any{"status": "accepted", "comment": "принята, направляем аварийную бригаду"})
		call := apply("call_start", map[string]any{"contact": "crew_leader"})
		apply("call_end", map[string]any{"call_id": call.CallID, "accepted_by": "Петров", "summary": "Россошанская 7 к.1, дерево на проезде", "recording": nil})

		makeDue(t, ctx, pool, itemID, "e1", "e2")
		waitDelivered(t, f, trainee, itemID, "e1", "e2")
		apply("set_status", map[string]any{"status": "responding", "comment": "бригада выехала, прибытие через 15 минут"})
		answered := answer("e2")
		apply("call_end", map[string]any{"call_id": answered.CallID, "accepted_by": "", "summary": "", "recording": nil})
		apply("set_status", map[string]any{"status": "arrived", "comment": "бригада на месте"})

		makeDue(t, ctx, pool, itemID, "e3", "e4")
		waitDelivered(t, f, trainee, itemID, "e3", "e4")
		apply("set_status", map[string]any{"status": "working", "comment": "распил дерева, вызвана автовышка"})
		answered = answer("e4")
		apply("call_end", map[string]any{"call_id": answered.CallID, "accepted_by": "", "summary": "", "recording": nil})

		final := apply("set_status", map[string]any{"status": "completed", "comment": "дерево убрано, проезд свободен"})
		if final.ItemState != "closed" {
			t.Fatalf("%s: completed receipt = %+v, want the card closed", label, final)
		}
		return itemID
	}

	// --- a freshly created lesson freezes dds/rubric-v2 ---
	v2ItemID := runDistrictCycle("rubric-v2", 1)
	if detail := waitAssessmentDetail(t, f.ctx, instructor, f.baseURL, v2ItemID); detail.Final == nil || detail.Final.Kind != "auto" || detail.Final.Revision != 1 {
		t.Fatalf("v2 assessment = %+v, want auto rev=1", detail.Final)
	}
	rubricVersion, criteria := rubricVersionAndCriteria(t, ctx, pool, v2ItemID)
	if rubricVersion != "dds/rubric-v2" {
		t.Fatalf("v2 lesson rubric_version = %q, want dds/rubric-v2", rubricVersion)
	}
	byID := criteriaByID(criteria)
	for _, id := range []string{"T_PROGRESS", "S_SEQUENCE", "C_CALLS"} {
		c, ok := byID[id]
		if !ok {
			t.Fatalf("v2 criteria missing %s: %+v", id, criteria)
		}
		if c.Status != "met" && c.Status != "partial" {
			t.Fatalf("%s = %+v, want met or partial (crew reports were actually delivered and reacted to)", id, c)
		}
	}
	for _, id := range []string{"T_COMPLETE", "D_FIELD_CORRECTIONS", "C_CALL_MADE", "C_CALL_LOG", "G_ADDRESS"} {
		if _, ok := byID[id]; ok {
			t.Fatalf("v2 criteria still has v1-only %s: %+v", id, criteria)
		}
	}

	// --- a lesson whose rubric_version was already frozen on v1 before
	// this slice (simulated by a direct update right after creation, the
	// same way an in-progress pre-ДДС-3 lesson would already carry it)
	// keeps scoring against dds/rubric-v1. ---
	frozenTrainee := createTraineeWithService(t, f, admin, "rubric-v1-frozen", 2, "dds_district_chertanovo")
	frozenLesson := runLesson(t, f, instructor, "ДДС уже на v1", "training", 2, currentUserID(t, f, frozenTrainee), v2)
	if tag, err := pool.Exec(ctx, `UPDATE lessons SET rubric_version='dds/rubric-v1' WHERE id=$1`, frozenLesson.ID); err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("freeze lesson on dds/rubric-v1: %v (%d rows)", err, tag.RowsAffected())
	}
	frozenItemID := currentItemID(t, f, frozenTrainee)
	seq := 0
	sendFrozen := func(cmdType string, payload map[string]any) (*http.Response, receiptResponse) {
		t.Helper()
		response, receipt := sendCommand(t, f, frozenTrainee, frozenItemID, randomCommandID(), seq, cmdType, payload)
		if receipt.Outcome == "applied" && !receipt.Replayed {
			seq = receipt.Seq
		}
		return response, receipt
	}
	applyFrozen := func(cmdType string, payload map[string]any) receiptResponse {
		t.Helper()
		response, receipt := sendFrozen(cmdType, payload)
		if response.StatusCode != http.StatusOK || receipt.Outcome != "applied" {
			t.Fatalf("frozen %s %v = %d %+v", cmdType, payload, response.StatusCode, receipt)
		}
		return receipt
	}
	answerFrozen := func(eventKey string) receiptResponse {
		t.Helper()
		response, receipt := sendFrozen("answer_incoming", map[string]any{"event_key": eventKey})
		if response.StatusCode != http.StatusOK || receipt.Outcome != "applied" {
			t.Fatalf("frozen answer_incoming %s = %d %+v", eventKey, response.StatusCode, receipt)
		}
		return receipt
	}
	applyFrozen("open", map[string]any{})
	applyFrozen("set_status", map[string]any{"status": "accepted", "comment": "принята"})
	call := applyFrozen("call_start", map[string]any{"contact": "crew_leader"})
	applyFrozen("call_end", map[string]any{"call_id": call.CallID, "accepted_by": "Петров", "summary": "Россошанская 7 к.1, дерево на проезде", "recording": nil})
	makeDue(t, ctx, pool, frozenItemID, "e1", "e2")
	waitDelivered(t, f, frozenTrainee, frozenItemID, "e1", "e2")
	applyFrozen("set_status", map[string]any{"status": "responding", "comment": "бригада выехала"})
	answered := answerFrozen("e2")
	applyFrozen("call_end", map[string]any{"call_id": answered.CallID, "accepted_by": "Петров", "summary": "на месте", "recording": nil})
	applyFrozen("set_status", map[string]any{"status": "arrived", "comment": "бригада на месте"})
	makeDue(t, ctx, pool, frozenItemID, "e3", "e4")
	waitDelivered(t, f, frozenTrainee, frozenItemID, "e3", "e4")
	applyFrozen("set_status", map[string]any{"status": "working", "comment": "распил дерева"})
	answered = answerFrozen("e4")
	applyFrozen("call_end", map[string]any{"call_id": answered.CallID, "accepted_by": "Петров", "summary": "работы завершены", "recording": nil})
	if final := applyFrozen("set_status", map[string]any{"status": "completed", "comment": "дерево убрано"}); final.ItemState != "closed" {
		t.Fatalf("frozen: completed receipt = %+v, want the card closed", final)
	}
	if detail := waitAssessmentDetail(t, f.ctx, instructor, f.baseURL, frozenItemID); detail.Final == nil || detail.Final.Kind != "auto" || detail.Final.Revision != 1 {
		t.Fatalf("frozen assessment = %+v, want auto rev=1", detail.Final)
	}
	frozenRubricVersion, frozenCriteria := rubricVersionAndCriteria(t, ctx, pool, frozenItemID)
	if frozenRubricVersion != "dds/rubric-v1" {
		t.Fatalf("frozen lesson rubric_version = %q, want dds/rubric-v1 (must not be re-derived after close)", frozenRubricVersion)
	}
	frozenByID := criteriaByID(frozenCriteria)
	if _, ok := frozenByID["C_CALL_MADE"]; !ok {
		t.Fatalf("frozen criteria missing v1's own C_CALL_MADE: %+v", frozenCriteria)
	}
	if _, ok := frozenByID["T_PROGRESS"]; ok {
		t.Fatalf("frozen criteria must not have v2's own T_PROGRESS: %+v", frozenCriteria)
	}
}

type assessmentCriterionDetail struct {
	ID     string   `json:"id"`
	Status string   `json:"status"`
	Score  *float64 `json:"score"`
}

func criteriaByID(criteria []assessmentCriterionDetail) map[string]assessmentCriterionDetail {
	out := make(map[string]assessmentCriterionDetail, len(criteria))
	for _, c := range criteria {
		out[c.ID] = c
	}
	return out
}

// rubricVersionAndCriteria reads the sealed auto assessment's own rubric_version and
// criteria straight from the database — the HTTP API's own assessment
// detail response does not project rubric_version, and this test needs
// to tell dds/rubric-v1 and dds/rubric-v2 apart precisely.
func rubricVersionAndCriteria(t *testing.T, ctx context.Context, pool *pgxpool.Pool, itemID string) (string, []assessmentCriterionDetail) {
	t.Helper()
	var rubricVersion string
	var criteriaRaw []byte
	if err := pool.QueryRow(ctx, `SELECT rubric_version, criteria FROM assessments WHERE item_id=$1 AND kind='auto'`, itemID).Scan(&rubricVersion, &criteriaRaw); err != nil {
		t.Fatalf("assessments row for %s: %v", itemID, err)
	}
	var criteria []assessmentCriterionDetail
	if err := json.Unmarshal(criteriaRaw, &criteria); err != nil {
		t.Fatalf("unmarshal criteria: %v", err)
	}
	return rubricVersion, criteria
}
