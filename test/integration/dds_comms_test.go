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

type ddsCommsView struct {
	State        string `json:"state"`
	Reaction     string `json:"reaction"`
	Seq          int    `json:"seq"`
	IncomingCall *struct {
		EventKey  string `json:"event_key"`
		From      string `json:"from"`
		RingUntil string `json:"ring_until"`
	} `json:"incoming_call"`
	Events []struct {
		Key      string `json:"key"`
		Delivery string `json:"delivery"`
		From     string `json:"from"`
		Text     string `json:"text"`
		Answered *bool  `json:"answered"`
	} `json:"events"`
	Calls []struct {
		ID         string  `json:"id"`
		ContactKey string  `json:"contact_key"`
		Direction  string  `json:"direction"`
		EventKey   *string `json:"event_key"`
		EndedAt    *string `json:"ended_at"`
	} `json:"calls"`
	Card struct {
		Contacts []struct {
			Key     string `json:"key"`
			Role    string `json:"role"`
			Phrases struct {
				Greeting string `json:"greeting"`
			} `json:"phrases"`
		} `json:"contacts"`
	} `json:"card"`
}

func getCommsItem(t *testing.T, f trainingE2EFixture, client *http.Client, itemID string) ddsCommsView {
	t.Helper()
	var item ddsCommsView
	if response := jsonRequest(t, f.ctx, client, f.baseURL, http.MethodGet, "/api/v1/items/"+itemID, nil, &item); response.StatusCode != http.StatusOK {
		t.Fatalf("GET /items/%s status = %d", itemID, response.StatusCode)
	}
	return item
}

// makeDue pulls the named scheduled events' due_at into the past so the
// api's scheduler delivers them on its next tick — the test's stand-in
// for waiting the scenario's real seconds. anchor_at is left as is.
func makeDue(t *testing.T, ctx context.Context, pool *pgxpool.Pool, itemID string, keys ...string) {
	t.Helper()
	tag, err := pool.Exec(ctx, `UPDATE item_events SET due_at = clock_timestamp() - interval '1 second' WHERE item_id=$1 AND event_key = ANY($2) AND state='scheduled'`, itemID, keys)
	if err != nil {
		t.Fatal(err)
	}
	if int(tag.RowsAffected()) != len(keys) {
		t.Fatalf("makeDue(%v) touched %d scheduled events", keys, tag.RowsAffected())
	}
}

func waitDelivered(t *testing.T, f trainingE2EFixture, client *http.Client, itemID string, keys ...string) ddsCommsView {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		item := getCommsItem(t, f, client, itemID)
		delivered := map[string]bool{}
		for _, e := range item.Events {
			delivered[e.Key] = true
		}
		all := true
		for _, k := range keys {
			all = all && delivered[k]
		}
		if all {
			return item
		}
		if time.Now().After(deadline) {
			t.Fatalf("events %v not delivered: %+v", keys, item.Events)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func eventState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, itemID, key string) string {
	t.Helper()
	var state string
	if err := pool.QueryRow(ctx, `SELECT state FROM item_events WHERE item_id=$1 AND event_key=$2`, itemID, key).Scan(&state); err != nil {
		t.Fatalf("event %s: %v", key, err)
	}
	return state
}

// TestDDSCrewCommunicationThroughAPIAndWorker is ДДС-2's acceptance path
// (ADR-031) over real api and worker processes:
//   - crew reports are scheduled only by the end of the first outgoing
//     call to the crew leader, not by the trainee's statuses;
//   - an incoming call rings, hides its words until answered, is answered
//     with answer_incoming (replay-safe) and ended without a call log;
//   - a call left ringing past 30 s is missed;
//   - the monitor shows each report with its answer/reaction time;
//   - the sealed evidence names each call's direction.
//
// The 112 control department's conditional call (pipe burst) rings only
// while no primary status exists, and a stop while it rings leaves it
// delivered without a call.
func TestDDSCrewCommunicationThroughAPIAndWorker(t *testing.T) {
	f := setupTrainingE2E(t)
	binary := filepath.Join(t.TempDir(), "emsim-worker")
	build := exec.CommandContext(f.ctx, "go", "build", "-o", binary, "./cmd/emsim")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build worker: %v\n%s", err, output)
	}
	worker := startWorkerProcess(t, binary, f.databaseURL, "worker", "dds-comms-worker")
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
	if response := jsonRequest(t, f.ctx, admin, f.baseURL, http.MethodPost, "/api/v1/admin/users", map[string]any{"login": "dds-comms-instructor", "password": "correct-horse-battery-staple", "full_name": "Инструктор ДДС", "role": "instructor"}, nil); response.StatusCode != http.StatusCreated {
		t.Fatalf("create instructor = %d", response.StatusCode)
	}
	instructor := newCookieClient(t)
	if response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodPost, "/api/v1/auth/login", map[string]any{"login": "dds-comms-instructor", "password": "correct-horse-battery-staple"}, nil); response.StatusCode != http.StatusOK {
		t.Fatalf("instructor login = %d", response.StatusCode)
	}
	provisionWorkstations(t, f, admin,
		map[string]any{"number": 1, "label": "dds-comms-tree"},
		map[string]any{"number": 2, "label": "dds-comms-pipe-accept"},
		map[string]any{"number": 3, "label": "dds-comms-pipe-stop"})

	treeV2 := versionIDByNumber(t, f, instructor, "dds-district-tree-cycle-01", 2)
	pipe := versionIDByNumber(t, f, instructor, "dds-district-pipe-burst-01", 1)

	// --- tree v2: reports after the call, incoming calls, a missed one ---
	trainee := createTraineeWithService(t, f, admin, "dds-comms-tree", 1, "dds_district_chertanovo")
	lesson := runLesson(t, f, instructor, "ДДС-2: связь с бригадой", "training", 1, currentUserID(t, f, trainee), treeV2)
	itemID := currentItemID(t, f, trainee)
	seq := 0
	send := func(cmdID, cmdType string, payload map[string]any) (*http.Response, receiptResponse) {
		t.Helper()
		response, receipt := sendCommand(t, f, trainee, itemID, cmdID, seq, cmdType, payload)
		if receipt.Outcome == "applied" && !receipt.Replayed {
			seq = receipt.Seq
		}
		return response, receipt
	}
	apply := func(cmdType string, payload map[string]any) receiptResponse {
		t.Helper()
		response, receipt := send(randomCommandID(), cmdType, payload)
		if response.StatusCode != http.StatusOK || receipt.Outcome != "applied" {
			t.Fatalf("%s %v = %d %+v", cmdType, payload, response.StatusCode, receipt)
		}
		return receipt
	}

	apply("open", map[string]any{})
	opened := getCommsItem(t, f, trainee, itemID)
	roles := map[string]string{}
	for _, c := range opened.Card.Contacts {
		roles[c.Key] = c.Role
	}
	if roles["crew_leader"] != "crew" || roles["control"] != "control_112" || roles["applicant"] != "applicant" || opened.Card.Contacts[0].Phrases.Greeting == "" {
		t.Fatalf("contacts = %+v, want roles and phrase texts", opened.Card.Contacts)
	}
	apply("set_status", map[string]any{"status": "accepted", "comment": "принята, направляем бригаду"})
	var scheduled int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM item_events WHERE item_id=$1`, itemID).Scan(&scheduled); err != nil {
		t.Fatal(err)
	}
	if scheduled != 0 {
		t.Fatalf("%d events scheduled before any call to the crew; reports must wait for the call (ADR-031)", scheduled)
	}

	call := apply("call_start", map[string]any{"contact": "crew_leader"})
	apply("call_end", map[string]any{"call_id": call.CallID, "accepted_by": "Петров", "summary": "Россошанская 7 к.1, дерево на проезде", "recording": nil})
	var anchored int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM item_events e JOIN calls c ON c.item_id=e.item_id
		WHERE e.item_id=$1 AND e.state='scheduled' AND e.anchor_at=c.ended_at AND c.direction='outgoing'`, itemID).Scan(&anchored); err != nil {
		t.Fatal(err)
	}
	if anchored != 4 {
		t.Fatalf("%d events anchored at the end of the crew call, want 4", anchored)
	}

	makeDue(t, ctx, pool, itemID, "e1", "e2")
	item := waitDelivered(t, f, trainee, itemID, "e1", "e2")
	if item.IncomingCall == nil || item.IncomingCall.EventKey != "e2" || item.IncomingCall.From != "crew_leader" {
		t.Fatalf("incoming_call = %+v, want e2 from crew_leader ringing", item.IncomingCall)
	}
	for _, e := range item.Events {
		switch e.Key {
		case "e1":
			if e.Delivery != "notice" || e.Text == "" || e.Answered != nil {
				t.Fatalf("notice e1 = %+v", e)
			}
		case "e2":
			if e.Delivery != "phone_incoming" || e.Text != "" || e.Answered == nil || *e.Answered {
				t.Fatalf("ringing e2 leaked its text or is marked answered: %+v", e)
			}
		}
	}
	apply("set_status", map[string]any{"status": "responding", "comment": "бригада выехала"})

	answerID, answerSeq := randomCommandID(), seq
	response, answered := send(answerID, "answer_incoming", map[string]any{"event_key": "e2"})
	if response.StatusCode != http.StatusOK || answered.Outcome != "applied" || answered.CallID == "" {
		t.Fatalf("answer_incoming e2 = %d %+v", response.StatusCode, answered)
	}
	// A replay resends the identical body, original expected_seq included.
	if response, replay := sendCommand(t, f, trainee, itemID, answerID, answerSeq, "answer_incoming", map[string]any{"event_key": "e2"}); response.StatusCode != http.StatusOK || !replay.Replayed || replay.CallID != answered.CallID {
		t.Fatalf("answer_incoming replay = %d %+v, want the original call", response.StatusCode, replay)
	}
	item = getCommsItem(t, f, trainee, itemID)
	if item.IncomingCall != nil {
		t.Fatalf("answered call still rings: %+v", item.IncomingCall)
	}
	for _, e := range item.Events {
		if e.Key == "e2" && (e.Text == "" || e.Answered == nil || !*e.Answered) {
			t.Fatalf("answered e2 = %+v, want its text", e)
		}
	}
	apply("call_end", map[string]any{"call_id": answered.CallID, "accepted_by": "", "summary": "", "recording": nil})
	apply("set_status", map[string]any{"status": "arrived", "comment": "бригада на месте"})

	makeDue(t, ctx, pool, itemID, "e3", "e4")
	waitDelivered(t, f, trainee, itemID, "e3", "e4")
	apply("set_status", map[string]any{"status": "working", "comment": "распил дерева, вызвана автовышка"})
	// e4 rings unanswered past its window: push its delivery 40 s back.
	if _, err := pool.Exec(ctx, `UPDATE item_events SET delivered_at = delivered_at - interval '40 seconds' WHERE item_id=$1 AND event_key='e4'`, itemID); err != nil {
		t.Fatal(err)
	}
	if response, missed := send(randomCommandID(), "answer_incoming", map[string]any{"event_key": "e4"}); response.StatusCode != http.StatusUnprocessableEntity || missed.ErrorCode == nil || *missed.ErrorCode != "call_missed" {
		t.Fatalf("answer after the ring = %d %+v, want call_missed", response.StatusCode, missed)
	}
	if item := getCommsItem(t, f, trainee, itemID); item.IncomingCall != nil {
		t.Fatalf("missed call still rings: %+v", item.IncomingCall)
	}

	var monitor struct {
		Rows []struct {
			Reports []struct {
				EventKey   string  `json:"event_key"`
				FromLabel  string  `json:"from_label"`
				AnsweredAt *string `json:"answered_at"`
				Missed     bool    `json:"missed"`
				ReactionAt *string `json:"reaction_at"`
			} `json:"reports"`
		} `json:"rows"`
	}
	if response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodGet, "/api/v1/lessons/"+lesson.ID+"/monitor", nil, &monitor); response.StatusCode != http.StatusOK || len(monitor.Rows) != 1 {
		t.Fatalf("monitor = %d %+v", response.StatusCode, monitor)
	}
	reports := map[string]int{}
	for i, r := range monitor.Rows[0].Reports {
		reports[r.EventKey] = i
	}
	if len(reports) != 4 {
		t.Fatalf("monitor reports = %+v, want e1..e4", monitor.Rows[0].Reports)
	}
	rs := monitor.Rows[0].Reports
	if r := rs[reports["e1"]]; r.ReactionAt == nil || r.FromLabel != "Руководитель аварийной бригады" {
		t.Fatalf("e1 report = %+v", r)
	}
	if r := rs[reports["e2"]]; r.AnsweredAt == nil || r.ReactionAt == nil || r.Missed {
		t.Fatalf("e2 report = %+v", r)
	}
	if r := rs[reports["e4"]]; !r.Missed || r.AnsweredAt != nil || r.ReactionAt != nil {
		t.Fatalf("e4 report = %+v, want missed without reaction", r)
	}

	final := apply("set_status", map[string]any{"status": "completed", "comment": "дерево убрано, проезд свободен"})
	if final.ItemState != "closed" {
		t.Fatalf("completed receipt = %+v, want the card closed", final)
	}
	var evidenceJSON []byte
	if err := pool.QueryRow(ctx, `SELECT body FROM evidence WHERE item_id=$1`, itemID).Scan(&evidenceJSON); err != nil {
		t.Fatalf("evidence: %v", err)
	}
	var evidence struct {
		Calls []struct {
			ContactKey string `json:"contact_key"`
			Direction  string `json:"direction"`
			EventKey   string `json:"event_key"`
		} `json:"calls"`
		Events []struct {
			Key   string `json:"key"`
			State string `json:"state"`
		} `json:"events"`
	}
	if err := json.Unmarshal(evidenceJSON, &evidence); err != nil {
		t.Fatal(err)
	}
	if len(evidence.Calls) != 2 ||
		evidence.Calls[0].Direction != "outgoing" || evidence.Calls[0].EventKey != "" ||
		evidence.Calls[1].Direction != "incoming" || evidence.Calls[1].EventKey != "e2" {
		t.Fatalf("evidence calls = %+v, want the crew call then the answered e2", evidence.Calls)
	}
	delivered := 0
	for _, e := range evidence.Events {
		if e.State == "delivered" {
			delivered++
		}
	}
	if delivered != 4 {
		t.Fatalf("evidence events = %+v, want e1..e4 delivered (the missed e4 has no call)", evidence.Events)
	}
	if detail := waitAssessmentDetail(t, f.ctx, instructor, f.baseURL, itemID); detail.Final == nil || detail.Final.Kind != "auto" || detail.Final.Revision != 1 {
		t.Fatalf("assessment = %+v, want auto rev=1", detail.Final)
	}

	// --- pipe burst: the control call waits on a missing primary status ---
	prompt := createTraineeWithService(t, f, admin, "dds-comms-pipe-accept", 2, "dds_district_chertanovo")
	runLesson(t, f, instructor, "ДДС-2: вовремя принята", "training", 2, currentUserID(t, f, prompt), pipe)
	promptItem := currentItemID(t, f, prompt)
	if response, receipt := sendCommand(t, f, prompt, promptItem, randomCommandID(), 0, "open", map[string]any{}); response.StatusCode != http.StatusOK || receipt.Outcome != "applied" {
		t.Fatalf("open = %d %+v", response.StatusCode, receipt)
	}
	if response, receipt := sendCommand(t, f, prompt, promptItem, randomCommandID(), 1, "set_status", map[string]any{"status": "accepted"}); response.StatusCode != http.StatusOK || receipt.Outcome != "applied" {
		t.Fatalf("accept = %d %+v", response.StatusCode, receipt)
	}
	makeDue(t, ctx, pool, promptItem, "e1")
	time.Sleep(2 * time.Second) // several scheduler ticks
	if state := eventState(t, ctx, pool, promptItem, "e1"); state != "scheduled" {
		t.Fatalf("control call after a timely primary status is %q, want still scheduled (status_in [received])", state)
	}

	late := createTraineeWithService(t, f, admin, "dds-comms-pipe-stop", 3, "dds_district_chertanovo")
	lateLesson := runLesson(t, f, instructor, "ДДС-2: нет статуса", "training", 3, currentUserID(t, f, late), pipe)
	lateItem := currentItemID(t, f, late)
	if response, receipt := sendCommand(t, f, late, lateItem, randomCommandID(), 0, "open", map[string]any{}); response.StatusCode != http.StatusOK || receipt.Outcome != "applied" {
		t.Fatalf("open = %d %+v", response.StatusCode, receipt)
	}
	makeDue(t, ctx, pool, lateItem, "e1")
	ringing := waitDelivered(t, f, late, lateItem, "e1")
	if ringing.IncomingCall == nil || ringing.IncomingCall.EventKey != "e1" || ringing.IncomingCall.From != "control" {
		t.Fatalf("incoming_call = %+v, want the control department ringing", ringing.IncomingCall)
	}
	if response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodPost, "/api/v1/lessons/"+lateLesson.ID+"/stop", nil, nil); response.StatusCode != http.StatusOK {
		t.Fatalf("stop = %d", response.StatusCode)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		item := getCommsItem(t, f, late, lateItem)
		if item.State == "interrupted" {
			if item.IncomingCall != nil || len(item.Calls) != 0 {
				t.Fatalf("interrupted item = %+v, want nothing ringing and no call", item)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("item not interrupted after stop: %+v", item)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if state := eventState(t, ctx, pool, lateItem, "e1"); state != "delivered" {
		t.Fatalf("control call after stop is %q, want delivered", state)
	}
}

// versionIDByNumber resolves a seed scenario key and version number to
// the scenario version id through the instructor catalogue endpoints.
func versionIDByNumber(t *testing.T, f trainingE2EFixture, instructorClient *http.Client, scenarioSourceKey string, number int) string {
	t.Helper()
	var scenarioList struct {
		Items []struct {
			ID        string  `json:"id"`
			SourceKey *string `json:"source_key"`
		} `json:"items"`
	}
	if response := jsonRequest(t, f.ctx, instructorClient, f.baseURL, http.MethodGet, "/api/v1/scenarios?page_size=200", nil, &scenarioList); response.StatusCode != http.StatusOK {
		t.Fatalf("GET /scenarios status = %d", response.StatusCode)
	}
	for _, item := range scenarioList.Items {
		if item.SourceKey == nil || *item.SourceKey != scenarioSourceKey {
			continue
		}
		var versions []struct {
			ID      string `json:"id"`
			Version int    `json:"version"`
		}
		if response := jsonRequest(t, f.ctx, instructorClient, f.baseURL, http.MethodGet, "/api/v1/scenarios/"+item.ID+"/versions", nil, &versions); response.StatusCode != http.StatusOK {
			t.Fatalf("GET versions status = %d", response.StatusCode)
		}
		for _, v := range versions {
			if v.Version == number {
				return v.ID
			}
		}
	}
	t.Fatalf("%s@%d not found", scenarioSourceKey, number)
	return ""
}
