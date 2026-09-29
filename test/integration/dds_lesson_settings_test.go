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
)

// TestDDSLessonSettingsThroughAPIAndWorker is ДДС-6's acceptance path
// (ADR-035) over real api and worker processes: an instructor sets a draft
// DDS lesson's time norm and its own weights and pass threshold, draws a
// random queue, saves and starts it; the frozen norm reaches the card's
// deadlines, the lesson's weights and threshold reach the sealed input and
// the final assessment, and the finished lesson's report shows them.
func TestDDSLessonSettingsThroughAPIAndWorker(t *testing.T) {
	f := setupTrainingE2E(t)
	binary := filepath.Join(t.TempDir(), "emsim-worker")
	build := exec.CommandContext(f.ctx, "go", "build", "-o", binary, "./cmd/emsim")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build worker: %v\n%s", err, output)
	}
	worker := startWorkerProcess(t, binary, f.databaseURL, "worker", "dds-lesson-settings-worker")
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
	if response := jsonRequest(t, f.ctx, admin, f.baseURL, http.MethodPost, "/api/v1/admin/users", map[string]any{"login": "dds-settings-instructor", "password": "correct-horse-battery-staple", "full_name": "Инструктор ДДС", "role": "instructor"}, nil); response.StatusCode != http.StatusCreated {
		t.Fatalf("create instructor = %d", response.StatusCode)
	}
	instructor := newCookieClient(t)
	if response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodPost, "/api/v1/auth/login", map[string]any{"login": "dds-settings-instructor", "password": "correct-horse-battery-staple"}, nil); response.StatusCode != http.StatusOK {
		t.Fatalf("instructor login = %d", response.StatusCode)
	}
	provisionWorkstations(t, f, admin, map[string]any{"number": 1, "label": "dds-settings"})
	trainee := createTraineeWithService(t, f, admin, "dds-settings", 1, "dds_district_chertanovo")
	traineeID := currentUserID(t, f, trainee)
	tree := versionIDByNumber(t, f, instructor, "dds-district-tree-cycle-01", 2)

	type lessonView struct {
		ID            string `json:"id"`
		State         string `json:"state"`
		RubricVersion string `json:"rubric_version"`
		Timing        struct {
			OpenS     int `json:"open_s"`
			PrimaryS  int `json:"primary_s"`
			CompleteS int `json:"complete_s"`
		} `json:"timing"`
		Scoring *struct {
			Weights       map[string]float64 `json:"weights"`
			PassThreshold float64            `json:"pass_threshold"`
		} `json:"scoring"`
	}
	var lesson lessonView
	if response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodPost, "/api/v1/lessons", map[string]any{
		"exercise_type": "dds_processing", "title": "Настройки ДДС", "mode": "training", "level": "easy",
	}, &lesson); response.StatusCode != http.StatusCreated {
		t.Fatalf("create lesson = %d", response.StatusCode)
	}
	lessonPath := "/api/v1/lessons/" + lesson.ID
	if lesson.Scoring != nil || lesson.Timing.PrimaryS != 30 {
		t.Fatalf("a new lesson starts on the defaults: %+v", lesson)
	}

	// --- time norm ---
	if response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodPatch, lessonPath, map[string]any{
		"timing": map[string]any{"open_s": 30, "primary_s": 9, "complete_s": 240},
	}, nil); response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("primary_s below 10 = %d, want 422", response.StatusCode)
	}
	if response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodPatch, lessonPath, map[string]any{
		"timing": map[string]any{"open_s": 30, "primary_s": 45, "complete_s": 240},
	}, &lesson); response.StatusCode != http.StatusOK || lesson.Timing.PrimaryS != 45 || lesson.Timing.CompleteS != 240 {
		t.Fatalf("patch timing = %d %+v", response.StatusCode, lesson.Timing)
	}

	// --- weights and threshold ---
	var rubric struct {
		RubricVersion        string  `json:"rubric_version"`
		PassThreshold        float64 `json:"pass_threshold"`
		DefaultPassThreshold float64 `json:"default_pass_threshold"`
		Criteria             []struct {
			ID            string  `json:"id"`
			Weight        float64 `json:"weight"`
			DefaultWeight float64 `json:"default_weight"`
		} `json:"criteria"`
	}
	if response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodGet, lessonPath+"/rubric", nil, &rubric); response.StatusCode != http.StatusOK || len(rubric.Criteria) == 0 {
		t.Fatalf("GET rubric = %d %+v", response.StatusCode, rubric)
	}
	if rubric.RubricVersion != "dds/rubric-v2" || rubric.PassThreshold != rubric.DefaultPassThreshold {
		t.Fatalf("a lesson without scoring shows the rubric's own values: %+v", rubric)
	}
	weights := map[string]float64{}
	for _, c := range rubric.Criteria {
		weights[c.ID] = 0
	}
	weights["T_OPEN"], weights["D_PRIMARY"] = 40, 60
	badWeights := map[string]float64{"T_OPEN": 100}
	if response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodPatch, lessonPath, map[string]any{
		"scoring": map[string]any{"weights": badWeights, "pass_threshold": 90},
	}, nil); response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("incomplete weights = %d, want 422", response.StatusCode)
	}
	if response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodPatch, lessonPath, map[string]any{
		"scoring": map[string]any{"weights": weights, "pass_threshold": 90},
	}, &lesson); response.StatusCode != http.StatusOK || lesson.Scoring == nil || lesson.Scoring.PassThreshold != 90 || lesson.Scoring.Weights["D_PRIMARY"] != 60 {
		t.Fatalf("patch scoring = %d %+v", response.StatusCode, lesson.Scoring)
	}
	// Patching only the timing must not touch the scoring.
	if response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodPatch, lessonPath, map[string]any{
		"timing": map[string]any{"open_s": 30, "primary_s": 45, "complete_s": 240},
	}, &lesson); response.StatusCode != http.StatusOK || lesson.Scoring == nil {
		t.Fatalf("timing-only patch dropped the scoring: %d %+v", response.StatusCode, lesson.Scoring)
	}
	if response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodGet, lessonPath+"/rubric", nil, &rubric); response.StatusCode != http.StatusOK || rubric.PassThreshold != 90 {
		t.Fatalf("GET rubric after patch = %d %+v", response.StatusCode, rubric)
	}
	for _, c := range rubric.Criteria {
		if c.Weight != weights[c.ID] || c.DefaultWeight == c.Weight && (c.ID == "T_OPEN" || c.ID == "D_PRIMARY") {
			t.Fatalf("criterion %s: weight %v (default %v), want the lesson's %v", c.ID, c.Weight, c.DefaultWeight, weights[c.ID])
		}
	}

	// --- random fill: a proposal, saved by nothing ---
	var drawn []struct {
		WorkstationNo      int      `json:"workstation_no"`
		UserID             string   `json:"user_id"`
		ScenarioVersionIDs []string `json:"scenario_version_ids"`
	}
	row := map[string]any{"workstation_no": 1, "user_id": traineeID}
	if response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodPost, lessonPath+"/assignments/draw", map[string]any{
		"categories": []string{"14"}, "count": 1, "rows": []any{row},
	}, &drawn); response.StatusCode != http.StatusOK || len(drawn) != 1 || len(drawn[0].ScenarioVersionIDs) != 1 || drawn[0].UserID != traineeID {
		t.Fatalf("draw = %d %+v", response.StatusCode, drawn)
	}
	var notEnough struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodPost, lessonPath+"/assignments/draw", map[string]any{
		"categories": []string{"14"}, "count": 20, "rows": []any{row},
	}, &notEnough); response.StatusCode != http.StatusUnprocessableEntity || notEnough.Error.Code != "not_enough_scenarios" || notEnough.Error.Details["available"] == nil {
		t.Fatalf("draw of 20 = %d %+v, want 422 not_enough_scenarios with details.available", response.StatusCode, notEnough)
	}
	var categories []struct {
		Code         string `json:"code"`
		CountByLevel struct {
			Easy int `json:"easy"`
		} `json:"count_by_level"`
	}
	if response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodGet, "/api/v1/scenarios/categories?service=dds_district_chertanovo", nil, &categories); response.StatusCode != http.StatusOK || len(categories) == 0 || categories[0].Code != "14" || categories[0].CountByLevel.Easy < 1 {
		t.Fatalf("categories = %d %+v", response.StatusCode, categories)
	}

	// The drawn queue is saved like any other; the cycle below then plays a
	// fixed scenario so its assessment is predictable.
	if response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodPut, lessonPath+"/assignments",
		[]map[string]any{{"workstation_no": 1, "user_id": traineeID, "scenario_version_ids": drawn[0].ScenarioVersionIDs}}, nil); response.StatusCode != http.StatusOK {
		t.Fatalf("saving the drawn queue = %d", response.StatusCode)
	}
	if response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodPut, lessonPath+"/assignments",
		[]map[string]any{{"workstation_no": 1, "user_id": traineeID, "scenario_version_ids": []string{tree}}}, nil); response.StatusCode != http.StatusOK {
		t.Fatalf("saving the tree scenario = %d", response.StatusCode)
	}
	if response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodPost, lessonPath+"/start", nil, &lesson); response.StatusCode != http.StatusOK || lesson.State != "running" {
		t.Fatalf("start = %d %+v", response.StatusCode, lesson)
	}

	// --- started: settings are frozen ---
	for name, body := range map[string]map[string]any{
		"timing":  {"timing": map[string]any{"open_s": 30, "primary_s": 60, "complete_s": 240}},
		"scoring": {"scoring": nil},
	} {
		if response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodPatch, lessonPath, body, nil); response.StatusCode != http.StatusConflict {
			t.Fatalf("PATCH %s after start = %d, want 409", name, response.StatusCode)
		}
	}
	if response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodPost, lessonPath+"/assignments/draw", map[string]any{
		"categories": []string{"14"}, "count": 1, "rows": []any{row},
	}, nil); response.StatusCode != http.StatusConflict {
		t.Fatalf("draw after start = %d, want 409", response.StatusCode)
	}

	// --- the frozen norm reaches the card's own deadlines ---
	itemID := currentItemID(t, f, trainee)
	var open, primary time.Time
	if err := pool.QueryRow(ctx, `SELECT (deadlines->>'open_at')::timestamptz, (deadlines->>'primary_at')::timestamptz FROM items WHERE id=$1`, itemID).Scan(&open, &primary); err != nil {
		t.Fatalf("read deadlines: %v", err)
	}
	var offered time.Time
	if err := pool.QueryRow(ctx, `SELECT offered_at FROM items WHERE id=$1`, itemID).Scan(&offered); err != nil {
		t.Fatal(err)
	}
	if got := open.Sub(offered); got != 30*time.Second {
		t.Fatalf("open deadline = offered+%v, want 30s", got)
	}
	// Before opening the primary deadline is provisional: open_at + primary_s.
	if got := primary.Sub(offered); got != 75*time.Second {
		t.Fatalf("provisional primary deadline = offered+%v, want 30s+45s", got)
	}

	// --- play the district cycle to its end ---
	seq := 0
	apply := func(cmdType string, payload map[string]any) receiptResponse {
		t.Helper()
		response, receipt := sendCommand(t, f, trainee, itemID, randomCommandID(), seq, cmdType, payload)
		if response.StatusCode != http.StatusOK || receipt.Outcome != "applied" {
			t.Fatalf("%s %v = %d %+v", cmdType, payload, response.StatusCode, receipt)
		}
		if !receipt.Replayed {
			seq = receipt.Seq
		}
		return receipt
	}
	opened := apply("open", map[string]any{})
	// Opening re-anchors the primary norm at the opening itself (ADR-035 amendment).
	var openedAt time.Time
	if err := pool.QueryRow(ctx, `SELECT opened_at, (deadlines->>'primary_at')::timestamptz FROM items WHERE id=$1`, itemID).Scan(&openedAt, &primary); err != nil {
		t.Fatal(err)
	}
	if got := primary.Sub(openedAt); got != 45*time.Second {
		t.Fatalf("primary deadline = opened+%v, want 45s", got)
	}
	if opened.Deadlines == nil || !opened.Deadlines.PrimaryAt.Equal(primary) {
		t.Fatalf("open receipt deadlines = %+v, want primary_at %v", opened.Deadlines, primary)
	}
	apply("set_status", map[string]any{"status": "accepted", "comment": "принята, направляем аварийную бригаду"})
	call := apply("call_start", map[string]any{"contact": "crew_leader"})
	apply("call_end", map[string]any{"call_id": call.CallID, "accepted_by": "Петров", "summary": "Россошанская 7 к.1, дерево на проезде", "recording": nil})
	makeDue(t, ctx, pool, itemID, "e1", "e2")
	waitDelivered(t, f, trainee, itemID, "e1", "e2")
	apply("set_status", map[string]any{"status": "responding", "comment": "бригада выехала, прибытие через 15 минут"})
	answered := apply("answer_incoming", map[string]any{"event_key": "e2"})
	apply("call_end", map[string]any{"call_id": answered.CallID, "accepted_by": "", "summary": "", "recording": nil})
	apply("set_status", map[string]any{"status": "arrived", "comment": "бригада на месте"})
	makeDue(t, ctx, pool, itemID, "e3", "e4")
	waitDelivered(t, f, trainee, itemID, "e3", "e4")
	apply("set_status", map[string]any{"status": "working", "comment": "распил дерева, вызвана автовышка"})
	answered = apply("answer_incoming", map[string]any{"event_key": "e4"})
	apply("call_end", map[string]any{"call_id": answered.CallID, "accepted_by": "", "summary": "", "recording": nil})
	if final := apply("set_status", map[string]any{"status": "completed", "comment": "дерево убрано, проезд свободен"}); final.ItemState != "closed" {
		t.Fatalf("completed receipt = %+v, want the card closed", final)
	}

	// --- the lesson's weights and threshold reach the assessment ---
	if detail := waitAssessmentDetail(t, f.ctx, instructor, f.baseURL, itemID); detail.Final == nil || detail.Final.Kind != "auto" {
		t.Fatalf("assessment = %+v, want an auto one", detail.Final)
	}
	type effectiveRubric struct {
		PassThreshold float64 `json:"pass_threshold"`
		Criteria      []struct {
			ID     string  `json:"id"`
			Weight float64 `json:"weight"`
		} `json:"criteria"`
	}
	checkEffective := func(where string, raw []byte) {
		t.Helper()
		var e effectiveRubric
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatalf("%s: %v", where, err)
		}
		if e.PassThreshold != 90 {
			t.Fatalf("%s: pass_threshold=%v, want the lesson's 90", where, e.PassThreshold)
		}
		for _, c := range e.Criteria {
			if c.Weight != weights[c.ID] {
				t.Fatalf("%s: %s weight=%v, want the lesson's %v", where, c.ID, c.Weight, weights[c.ID])
			}
		}
	}
	var sealed, effective []byte
	if err := pool.QueryRow(ctx, `SELECT body->'rubric_effective' FROM assessment_inputs WHERE item_id=$1`, itemID).Scan(&sealed); err != nil {
		t.Fatalf("sealed input: %v", err)
	}
	checkEffective("assessment_inputs.rubric_effective", sealed)
	var score *float64
	var passed *bool
	if err := pool.QueryRow(ctx, `SELECT rubric_effective, score, passed FROM assessments WHERE item_id=$1 AND kind='auto'`, itemID).Scan(&effective, &score, &passed); err != nil {
		t.Fatalf("auto assessment: %v", err)
	}
	checkEffective("assessments.rubric_effective", effective)
	if score == nil || passed == nil {
		t.Fatalf("auto assessment is not ready: score=%v passed=%v", score, passed)
	}
	if want := *score >= 90; *passed != want {
		t.Fatalf("passed=%v with score %v, want %v against the lesson's threshold 90", *passed, *score, want)
	}

	// --- the finished lesson's report shows the settings ---
	var report struct {
		Lesson struct {
			Timing struct {
				PrimaryS  int `json:"primary_s"`
				CompleteS int `json:"complete_s"`
			} `json:"timing"`
			PassThreshold float64 `json:"pass_threshold"`
			CustomWeights bool    `json:"custom_weights"`
		} `json:"lesson"`
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodGet, lessonPath+"/report", nil, &report)
		if response.StatusCode == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("report status = %d, want 200 once the lesson finished", response.StatusCode)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if report.Lesson.Timing.PrimaryS != 45 || report.Lesson.Timing.CompleteS != 240 || report.Lesson.PassThreshold != 90 || !report.Lesson.CustomWeights {
		t.Fatalf("report lesson header = %+v, want the lesson's settings", report.Lesson)
	}
}
