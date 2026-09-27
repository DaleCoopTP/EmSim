//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	pgstore "emsim/internal/platform/postgres"
)

type ddsItemView struct {
	State              string   `json:"state"`
	Reaction           string   `json:"reaction"`
	Seq                int      `json:"seq"`
	CardStatus         string   `json:"card_status"`
	TerminalStatuses   []string `json:"terminal_statuses"`
	AllowedTransitions []string `json:"allowed_transitions"`
}

func getDDSItem(t *testing.T, f trainingE2EFixture, client *http.Client, itemID string) ddsItemView {
	t.Helper()
	var item ddsItemView
	if response := jsonRequest(t, f.ctx, client, f.baseURL, http.MethodGet, "/api/v1/items/"+itemID, nil, &item); response.StatusCode != http.StatusOK {
		t.Fatalf("GET /items/%s status = %d", itemID, response.StatusCode)
	}
	return item
}

// TestDDSResponseCycleThroughAPIAndWorker is DDS-1's acceptance path
// (ADR-030) over real api and worker processes: a district dispatcher
// works the whole reaction cycle with the status pencil, the terminal
// status itself closes the card, the legacy close/add_comment/
// set_card_field commands are rejected, pending crew reports are skipped
// at close and the worker still produces the automatic assessment. A
// 03 dispatcher never gets not_accepted, and a stop mid-cycle leaves the
// card «Не завершено».
func TestDDSResponseCycleThroughAPIAndWorker(t *testing.T) {
	f := setupTrainingE2E(t)
	binary := filepath.Join(t.TempDir(), "emsim-worker")
	build := exec.CommandContext(f.ctx, "go", "build", "-o", binary, "./cmd/emsim")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build worker: %v\n%s", err, output)
	}
	worker := startWorkerProcess(t, binary, f.databaseURL, "worker", "dds-cycle-worker")
	defer worker.stop(t, false)

	admin := newCookieClient(t)
	loginAdmin(t, f, admin)
	if response := jsonRequest(t, f.ctx, admin, f.baseURL, http.MethodPost, "/api/v1/admin/users", map[string]any{"login": "dds-cycle-instructor", "password": "correct-horse-battery-staple", "full_name": "Инструктор ДДС", "role": "instructor"}, nil); response.StatusCode != http.StatusCreated {
		t.Fatalf("create instructor = %d", response.StatusCode)
	}
	instructor := newCookieClient(t)
	if response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodPost, "/api/v1/auth/login", map[string]any{"login": "dds-cycle-instructor", "password": "correct-horse-battery-staple"}, nil); response.StatusCode != http.StatusOK {
		t.Fatalf("instructor login = %d", response.StatusCode)
	}
	provisionWorkstations(t, f, admin, map[string]any{"number": 1, "label": "dds-district"}, map[string]any{"number": 2, "label": "dds-ambulance"})

	// --- district: the full cycle ---
	district := createTraineeWithService(t, f, admin, "dds-cycle-district", 1, "dds_district_chertanovo")
	runLesson(t, f, instructor, "ДДС района: цикл", "training", 1, currentUserID(t, f, district), approvedVersionID(t, f, instructor, "dds-district-tree-cycle-01"))
	itemID := currentItemID(t, f, district)

	item := getDDSItem(t, f, district, itemID)
	if !reflect.DeepEqual(item.TerminalStatuses, []string{"completed", "refused"}) || item.CardStatus != "registered" {
		t.Fatalf("offered district item = %+v, want terminal [completed refused] and card_status registered", item)
	}

	seq := 0
	apply := func(cmdType string, payload map[string]any) receiptResponse {
		t.Helper()
		response, receipt := sendCommand(t, f, district, itemID, randomCommandID(), seq, cmdType, payload)
		if response.StatusCode != http.StatusOK || receipt.Outcome != "applied" {
			t.Fatalf("%s %v = %d %+v", cmdType, payload, response.StatusCode, receipt)
		}
		seq = receipt.Seq
		return receipt
	}
	reject := func(cmdType string, payload map[string]any, wantCode string) {
		t.Helper()
		response, receipt := sendCommand(t, f, district, itemID, randomCommandID(), seq, cmdType, payload)
		if response.StatusCode != http.StatusUnprocessableEntity || receipt.Outcome != "rejected" || receipt.ErrorCode == nil || *receipt.ErrorCode != wantCode {
			t.Fatalf("%s %v = %d %+v, want rejected %s", cmdType, payload, response.StatusCode, receipt, wantCode)
		}
	}

	apply("open", map[string]any{})
	reject("set_status", map[string]any{"status": "not_accepted"}, "comment_required")
	reject("add_comment", map[string]any{"text": "комментарий без статуса"}, "transition_not_allowed")
	reject("set_card_field", map[string]any{"path": "/card/address/okrug", "value": "ЮАО"}, "transition_not_allowed")
	apply("set_status", map[string]any{"status": "accepted", "comment": "принята, направляем аварийную бригаду"})
	reject("close", map[string]any{}, "transition_not_allowed")
	reject("set_status", map[string]any{"status": "responding", "comment": "бригада выехала"}, "call_required")

	call := apply("call_start", map[string]any{"contact": "crew_leader"})
	apply("call_end", map[string]any{"call_id": call.CallID, "accepted_by": "Петров", "summary": "Россошанская 7 к.1, дерево на проезде", "recording": nil})
	for _, step := range []struct{ status, comment string }{
		{"responding", "бригада выехала, прибытие через 15 минут"},
		{"arrived", "бригада на месте"},
		{"working", "распил дерева, вызвана автовышка"},
	} {
		if receipt := apply("set_status", map[string]any{"status": step.status, "comment": step.comment}); receipt.ItemState != "in_progress" {
			t.Fatalf("%s left item_state %q, want in_progress", step.status, receipt.ItemState)
		}
	}
	if item := getDDSItem(t, f, district, itemID); item.CardStatus != "in_progress" {
		t.Fatalf("card_status mid-cycle = %q, want in_progress", item.CardStatus)
	}
	final := apply("set_status", map[string]any{"status": "completed", "comment": "дерево убрано, проезд свободен"})
	if final.ItemState != "closed" || final.Reaction != "completed" {
		t.Fatalf("completed receipt = %+v, want the card closed", final)
	}
	if item := getDDSItem(t, f, district, itemID); item.CardStatus != "completed" || len(item.AllowedTransitions) != 0 {
		t.Fatalf("closed item = %+v, want card_status completed and no transitions", item)
	}

	ctx, cancel := context.WithTimeout(f.ctx, 30*time.Second)
	defer cancel()
	pool, err := pgstore.Open(ctx, f.databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var closeReason string
	var scheduled int
	if err := pool.QueryRow(ctx, `SELECT close_reason, (SELECT count(*) FROM item_events WHERE item_id=$1 AND state='scheduled') FROM items WHERE id=$1`, itemID).Scan(&closeReason, &scheduled); err != nil {
		t.Fatal(err)
	}
	if closeReason != "completed" || scheduled != 0 {
		t.Fatalf("close_reason=%q scheduled events=%d, want completed and none left scheduled", closeReason, scheduled)
	}
	var evidenceJSON []byte
	if err := pool.QueryRow(ctx, `SELECT body FROM evidence WHERE item_id=$1`, itemID).Scan(&evidenceJSON); err != nil {
		t.Fatalf("evidence: %v", err)
	}
	var evidence struct {
		Actions []struct {
			Type     string          `json:"type"`
			Accepted bool            `json:"accepted"`
			Payload  json.RawMessage `json:"payload"`
		} `json:"actions"`
		CloseReason string `json:"close_reason"`
	}
	if err := json.Unmarshal(evidenceJSON, &evidence); err != nil {
		t.Fatal(err)
	}
	var statuses []string
	for _, action := range evidence.Actions {
		if action.Type != "set_status" || !action.Accepted {
			continue
		}
		var payload struct{ Status, Comment string }
		if err := json.Unmarshal(action.Payload, &payload); err != nil || payload.Comment == "" {
			t.Fatalf("set_status evidence payload %s: comment missing (%v)", action.Payload, err)
		}
		statuses = append(statuses, payload.Status)
	}
	if want := []string{"accepted", "responding", "arrived", "working", "completed"}; !reflect.DeepEqual(statuses, want) {
		t.Fatalf("evidence statuses = %v, want %v", statuses, want)
	}
	if detail := waitAssessmentDetail(t, f.ctx, instructor, f.baseURL, itemID); detail.Final == nil || detail.Final.Kind != "auto" || detail.Final.Revision != 1 {
		t.Fatalf("assessment = %+v, want auto rev=1", detail.Final)
	}

	// --- ambulance 03: no not_accepted; a stop mid-cycle ---
	ambulance := createTraineeWithService(t, f, admin, "dds-cycle-ambulance", 2, "dds_ambulance_03")
	lesson := runLesson(t, f, instructor, "ДДС 03: stop", "training", 2, currentUserID(t, f, ambulance), approvedVersionID(t, f, instructor, "dds-ambulance-cycle-01"))
	ambulanceItemID := currentItemID(t, f, ambulance)
	if response, receipt := sendCommand(t, f, ambulance, ambulanceItemID, randomCommandID(), 0, "open", map[string]any{}); response.StatusCode != http.StatusOK || receipt.Outcome != "applied" {
		t.Fatalf("03 open = %d %+v", response.StatusCode, receipt)
	}
	opened := getDDSItem(t, f, ambulance, ambulanceItemID)
	if !reflect.DeepEqual(opened.AllowedTransitions, []string{"accepted", "completed_without_team"}) {
		t.Fatalf("03 allowed transitions = %v, want [accepted completed_without_team]", opened.AllowedTransitions)
	}
	if response, receipt := sendCommand(t, f, ambulance, ambulanceItemID, randomCommandID(), 1, "set_status", map[string]any{"status": "accepted"}); response.StatusCode != http.StatusOK || receipt.Outcome != "applied" {
		t.Fatalf("03 accept = %d %+v", response.StatusCode, receipt)
	}
	if response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodPost, "/api/v1/lessons/"+lesson.ID+"/stop", nil, nil); response.StatusCode != http.StatusOK {
		t.Fatalf("stop = %d", response.StatusCode)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		item := getDDSItem(t, f, ambulance, ambulanceItemID)
		if item.State == "interrupted" {
			if item.CardStatus != "not_completed" {
				t.Fatalf("interrupted 03 card_status = %q, want not_completed", item.CardStatus)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("03 item not interrupted after stop: %+v", item)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
