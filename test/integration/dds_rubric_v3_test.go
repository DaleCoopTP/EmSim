//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"emsim/internal/assessment/dds/commentjudge"

	pgstore "emsim/internal/platform/postgres"

	"github.com/jackc/pgx/v5/pgxpool"
)

// fakeDDSJudge is an OpenAI-compatible /chat/completions endpoint that
// plays the DDS comment judge (ADR-034): it tells the two prompts apart
// by their system message, answers from a per-request mode, and records
// every user message it was sent so a test can inspect exactly what the
// model would have seen. The real worker process talks to it over HTTP
// through the real llm.Client and commentjudge handlers.
type fakeDDSJudge struct {
	mu       sync.Mutex
	mode     string // "partial" | "needs_review" | "down"
	facts    []commentjudge.FactsRequest
	grammar  []commentjudge.GrammarRequest
	rawUsers []string
}

func (f *fakeDDSJudge) setMode(mode string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mode = mode
	f.facts, f.grammar, f.rawUsers = nil, nil, nil
}

func (f *fakeDDSJudge) snapshot() (facts []commentjudge.FactsRequest, grammar []commentjudge.GrammarRequest, raw []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]commentjudge.FactsRequest(nil), f.facts...), append([]commentjudge.GrammarRequest(nil), f.grammar...), append([]string(nil), f.rawUsers...)
}

func (f *fakeDDSJudge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Messages) != 2 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	system, user := body.Messages[0].Content, body.Messages[1].Content
	f.mu.Lock()
	mode := f.mode
	f.rawUsers = append(f.rawUsers, user)
	f.mu.Unlock()
	if mode == "down" {
		http.Error(w, "model unavailable", http.StatusInternalServerError)
		return
	}
	var answer any
	switch {
	case strings.HasPrefix(system, "Ты проверяешь письменные комментарии"):
		var req commentjudge.FactsRequest
		if err := json.Unmarshal([]byte(user), &req); err != nil {
			http.Error(w, "bad facts request", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.facts = append(f.facts, req)
		f.mu.Unlock()
		answers := map[string]commentjudge.Answer{}
		for _, q := range req.Questions {
			answers[q.ID] = commentjudge.AnswerYes
		}
		switch mode {
		case "partial":
			answers["event:e3:0"] = commentjudge.AnswerNo // the autovышка fact is "not found"
		case "needs_review":
			answers["event:e3:0"] = commentjudge.AnswerNeedsReview
		}
		answer = answers
	case strings.HasPrefix(system, "Ты проверяешь орфографию"):
		var req commentjudge.GrammarRequest
		if err := json.Unmarshal([]byte(user), &req); err != nil {
			http.Error(w, "bad grammar request", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.grammar = append(f.grammar, req)
		f.mu.Unlock()
		answers := map[string][]commentjudge.GrammarError{}
		for _, c := range req.Comments {
			answers[c.ID] = []commentjudge.GrammarError{}
		}
		if len(req.Comments) > 0 {
			first := req.Comments[0]
			word := strings.Trim(strings.Fields(first.Text)[0], ",.")
			answers[first.ID] = []commentjudge.GrammarError{
				{Fragment: word, Correction: word, Kind: commentjudge.KindSpelling},
				{Fragment: word, Correction: word, Kind: commentjudge.KindPunctuation},
			}
		}
		answer = answers
	default:
		http.Error(w, "unknown prompt", http.StatusBadRequest)
		return
	}
	content, _ := json.Marshal(answer)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": string(content)}, "finish_reason": "stop"}}})
}

type ddsAutoAssessment struct {
	Status        string
	Score         *float64
	Model         *string
	RubricVersion string
	Criteria      map[string]ddsCriterion
}

type ddsCriterion struct {
	ID      string   `json:"id"`
	Status  string   `json:"status"`
	Score   *float64 `json:"score"`
	Details []struct {
		Key    string `json:"key"`
		Errors []struct {
			Fragment string `json:"fragment"`
		} `json:"errors"`
	} `json:"details"`
}

func readDDSAuto(t *testing.T, ctx context.Context, pool *pgxpool.Pool, itemID string) ddsAutoAssessment {
	t.Helper()
	var a ddsAutoAssessment
	var raw []byte
	if err := pool.QueryRow(ctx, `SELECT status, score::float8, model, rubric_version, criteria FROM assessments WHERE item_id=$1 AND kind='auto'`, itemID).Scan(&a.Status, &a.Score, &a.Model, &a.RubricVersion, &raw); err != nil {
		t.Fatalf("auto assessment for %s: %v", itemID, err)
	}
	var criteria []ddsCriterion
	if err := json.Unmarshal(raw, &criteria); err != nil {
		t.Fatal(err)
	}
	a.Criteria = make(map[string]ddsCriterion, len(criteria))
	for _, c := range criteria {
		a.Criteria[c.ID] = c
	}
	return a
}

// TestDDSRubricV3ThroughAPIAndWorker is ДДС-4's own acceptance path
// (ADR-034) over real api and worker processes with ASSESSMENT_JUDGE=llm
// and a fake OpenAI-compatible model: a new DDS lesson freezes
// dds/rubric-v3, its close seals both judge requests into the assessment
// input, and the worker's answers become a partial D_COMMENT_CONTENT and
// G_GRAMMAR with the error list; a judge that is unsure makes the whole
// assessment needs_review with no score; a model that is down exhausts
// the task's retries into needs_review through the finalizer, never a
// zero. (ASSESSMENT_JUDGE=off — v2 and no model call — is covered by
// TestDDSRubricV2ThroughAPIAndWorker, which runs its api and worker with
// the judge off.)
func TestDDSRubricV3ThroughAPIAndWorker(t *testing.T) {
	judge := &fakeDDSJudge{}
	model := httptest.NewServer(judge)
	defer model.Close()

	f := setupTrainingE2EWithAPIEnv(t, []string{"ASSESSMENT_JUDGE=llm"})
	binary := filepath.Join(t.TempDir(), "emsim-worker")
	build := exec.CommandContext(f.ctx, "go", "build", "-o", binary, "./cmd/emsim")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build worker: %v\n%s", err, output)
	}
	worker := startWorkerProcessWithEnv(t, binary, f.databaseURL, "worker", "dds-rubric-v3-worker", []string{
		"ASSESSMENT_JUDGE=llm", "JUDGE_LLM_URL=" + model.URL + "/v1", "JUDGE_LLM_MODEL=fake-dds-judge", "JUDGE_TIMEOUT=5s",
	})
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
	if response := jsonRequest(t, f.ctx, admin, f.baseURL, http.MethodPost, "/api/v1/admin/users", map[string]any{"login": "dds-rubric-v3-instructor", "password": "correct-horse-battery-staple", "full_name": "Инструктор ДДС", "role": "instructor"}, nil); response.StatusCode != http.StatusCreated {
		t.Fatalf("create instructor = %d", response.StatusCode)
	}
	instructor := newCookieClient(t)
	if response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodPost, "/api/v1/auth/login", map[string]any{"login": "dds-rubric-v3-instructor", "password": "correct-horse-battery-staple"}, nil); response.StatusCode != http.StatusOK {
		t.Fatalf("instructor login = %d", response.StatusCode)
	}
	provisionWorkstations(t, f, admin,
		map[string]any{"number": 1, "label": "dds-v3-partial"},
		map[string]any{"number": 2, "label": "dds-v3-review"},
		map[string]any{"number": 3, "label": "dds-v3-down"})
	versionID := versionIDByNumber(t, f, instructor, "dds-district-tree-cycle-01", 2)

	// --- the judge answers: partial content, two grammar errors ---
	judge.setMode("partial")
	partialItem := runDDSTreeCycle(t, f, ctx, pool, admin, instructor, versionID, "v3-partial", 1)
	if detail := waitAssessmentDetail(t, f.ctx, instructor, f.baseURL, partialItem); detail.Final == nil || detail.Final.Kind != "auto" || detail.Final.Revision != 1 || detail.Final.Status != "ready" {
		t.Fatalf("partial assessment = %+v, want auto rev=1 ready", detail.Final)
	}
	auto := readDDSAuto(t, ctx, pool, partialItem)
	if auto.RubricVersion != "dds/rubric-v3" || auto.Model == nil || *auto.Model != "fake-dds-judge" || auto.Score == nil {
		t.Fatalf("auto = %+v, want dds/rubric-v3 scored by fake-dds-judge", auto)
	}
	content := auto.Criteria["D_COMMENT_CONTENT"]
	// 3 fact questions (1 on working, 2 on completed) + 1 aggregate
	// contradiction question; one "no" -> 3/4.
	if content.Status != "partial" || content.Score == nil || *content.Score != 0.75 {
		t.Fatalf("D_COMMENT_CONTENT = %+v, want partial 0.75", content)
	}
	grammar := auto.Criteria["G_GRAMMAR"]
	errorCount := 0
	for _, d := range grammar.Details {
		errorCount += len(d.Errors)
	}
	if grammar.Status != "partial" || grammar.Score == nil || *grammar.Score != 0.5 || errorCount != 2 {
		t.Fatalf("G_GRAMMAR = %+v (errors %d), want partial 0.5 with 2 errors", grammar, errorCount)
	}
	var semanticInput, judgeInfo []byte
	if err := pool.QueryRow(ctx, `SELECT body->'semantic_input', body->'judge' FROM assessment_inputs WHERE item_id=$1`, partialItem).Scan(&semanticInput, &judgeInfo); err != nil {
		t.Fatal(err)
	}
	var sealed map[string]json.RawMessage
	if err := json.Unmarshal(semanticInput, &sealed); err != nil {
		t.Fatal(err)
	}
	if _, ok := sealed["D_COMMENT_CONTENT"]; !ok {
		t.Fatalf("sealed semantic_input has no D_COMMENT_CONTENT: %s", semanticInput)
	}
	if _, ok := sealed["G_GRAMMAR"]; !ok {
		t.Fatalf("sealed semantic_input has no G_GRAMMAR: %s", semanticInput)
	}
	var sealedJudge struct {
		Model          string            `json:"model"`
		PromptVersions map[string]string `json:"prompt_versions"`
	}
	if err := json.Unmarshal(judgeInfo, &sealedJudge); err != nil {
		t.Fatal(err)
	}
	if sealedJudge.Model != "fake-dds-judge" || sealedJudge.PromptVersions["D_COMMENT_CONTENT"] != commentjudge.FactsPromptVersion || sealedJudge.PromptVersions["G_GRAMMAR"] != commentjudge.GrammarPromptVersion {
		t.Fatalf("sealed judge = %+v", sealedJudge)
	}
	// One request per judged criterion, and the model only ever saw the
	// trainee's comments with statuses and closed questions — never the
	// crew reports' own text or anything about weights and scores.
	facts, grammarRequests, raw := judge.snapshot()
	if len(facts) != 1 || len(grammarRequests) != 1 {
		t.Fatalf("judge saw %d facts and %d grammar requests, want 1 and 1", len(facts), len(grammarRequests))
	}
	for _, user := range raw {
		for _, forbidden := range []string{"Приступили к распилу", "для крупных веток", "weight", "score", "expected_chain"} {
			if strings.Contains(user, forbidden) {
				t.Fatalf("judge request leaks %q: %s", forbidden, user)
			}
		}
	}
	if _, ok := questionByID(facts[0], "event:e3:0"); !ok {
		t.Fatalf("facts request has no event:e3:0 question: %+v", facts[0].Questions)
	}

	// --- the judge is unsure: needs_review, no score ---
	judge.setMode("needs_review")
	reviewItem := runDDSTreeCycle(t, f, ctx, pool, admin, instructor, versionID, "v3-review", 2)
	if detail := waitAssessmentDetail(t, f.ctx, instructor, f.baseURL, reviewItem); detail.Final == nil || detail.Final.Status != "needs_review" {
		t.Fatalf("needs_review assessment = %+v", detail.Final)
	}
	review := readDDSAuto(t, ctx, pool, reviewItem)
	if review.Score != nil || review.Criteria["D_COMMENT_CONTENT"].Status != "unavailable" {
		t.Fatalf("review = %+v, want no score and D_COMMENT_CONTENT unavailable", review)
	}

	// --- the model is down: retries are exhausted into needs_review ---
	judge.setMode("down")
	downItem := runDDSTreeCycle(t, f, ctx, pool, admin, instructor, versionID, "v3-down", 3)
	if detail := waitAssessmentDetail(t, f.ctx, instructor, f.baseURL, downItem); detail.Final == nil || detail.Final.Kind != "auto" || detail.Final.Status != "needs_review" {
		t.Fatalf("down assessment = %+v, want auto needs_review through the finalizer", detail.Final)
	}
	down := readDDSAuto(t, ctx, pool, downItem)
	if down.Score != nil || down.Criteria["D_COMMENT_CONTENT"].Status != "unavailable" || down.Criteria["G_GRAMMAR"].Status != "unavailable" {
		t.Fatalf("down = %+v, want no score and both judged criteria unavailable", down)
	}
	if _, _, raw := judge.snapshot(); len(raw) < 2 {
		t.Fatalf("the worker asked the model %d times, want it to retry", len(raw))
	}
	// The deterministic criteria keep their real results in every case.
	for name, a := range map[string]ddsAutoAssessment{"partial": auto, "review": review, "down": down} {
		if c := a.Criteria["T_PROGRESS"]; c.Status != "met" && c.Status != "partial" {
			t.Fatalf("%s: T_PROGRESS = %+v, want a real deterministic result", name, c)
		}
	}
}

func questionByID(req commentjudge.FactsRequest, id string) (commentjudge.Question, bool) {
	for _, q := range req.Questions {
		if q.ID == id {
			return q, true
		}
	}
	return commentjudge.Question{}, false
}

// runDDSTreeCycle plays dds-district-tree-cycle-01 v2 end to end as a new
// trainee on workstation and returns the closed item's id: the same
// command sequence TestDDSRubricV2ThroughAPIAndWorker uses, with a
// comment on every status (the working one names the autovышка, the last
// one the outcome).
func runDDSTreeCycle(t *testing.T, f trainingE2EFixture, ctx context.Context, pool *pgxpool.Pool, admin, instructor *http.Client, versionID, label string, workstation int) string {
	t.Helper()
	trainee := createTraineeWithService(t, f, admin, label, workstation, "dds_district_chertanovo")
	runLesson(t, f, instructor, "ДДС "+label, "training", workstation, currentUserID(t, f, trainee), versionID)
	itemID := currentItemID(t, f, trainee)
	seq := 0
	send := func(cmdType string, payload map[string]any) receiptResponse {
		t.Helper()
		response, receipt := sendCommand(t, f, trainee, itemID, randomCommandID(), seq, cmdType, payload)
		if response.StatusCode != http.StatusOK || receipt.Outcome != "applied" {
			t.Fatalf("%s %s %v = %d %+v", label, cmdType, payload, response.StatusCode, receipt)
		}
		if !receipt.Replayed {
			seq = receipt.Seq
		}
		return receipt
	}
	send("open", map[string]any{})
	send("set_status", map[string]any{"status": "accepted", "comment": "принята, направляем аварийную бригаду"})
	call := send("call_start", map[string]any{"contact": "crew_leader"})
	send("call_end", map[string]any{"call_id": call.CallID, "accepted_by": "Петров", "summary": "Россошанская 7 к.1, дерево на проезде", "recording": nil})

	makeDue(t, ctx, pool, itemID, "e1", "e2")
	waitDelivered(t, f, trainee, itemID, "e1", "e2")
	send("set_status", map[string]any{"status": "responding", "comment": "бригада выехала, прибытие через 15 минут"})
	answered := send("answer_incoming", map[string]any{"event_key": "e2"})
	send("call_end", map[string]any{"call_id": answered.CallID, "accepted_by": "", "summary": "", "recording": nil})
	send("set_status", map[string]any{"status": "arrived", "comment": "бригада на месте"})

	makeDue(t, ctx, pool, itemID, "e3", "e4")
	waitDelivered(t, f, trainee, itemID, "e3", "e4")
	send("set_status", map[string]any{"status": "working", "comment": "распил дерева, вызвана автовышка"})
	answered = send("answer_incoming", map[string]any{"event_key": "e4"})
	send("call_end", map[string]any{"call_id": answered.CallID, "accepted_by": "", "summary": "", "recording": nil})
	if final := send("set_status", map[string]any{"status": "completed", "comment": "дерево убрано, проезд свободен"}); final.ItemState != "closed" {
		t.Fatalf("%s: completed receipt = %+v, want the card closed", label, final)
	}
	return itemID
}
