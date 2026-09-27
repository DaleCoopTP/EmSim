// 112-5a/ADR-024: the free-text caller-chat window's asynchronous
// protocol, driven end to end through the real emsim worker process (not
// a direct call to Service.ApplyCallerReply) — the same pattern
// lesson_close_worker_e2e_test.go uses for training.KindLessonClose.
// caller.reply is this repository's first task kind that calls an
// external adapter with real latency (operator112.StubCallerReplier
// here; a model in 112-5b) rather than pure Go inside its own
// transaction, so this file is also the proof that the async
// enqueue-outside-tx-compute-then-apply protocol actually works through
// PostgreSQL's queue, not just against Service methods called directly.

//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"emsim/internal/auth"
	authpg "emsim/internal/auth/postgres"
	"emsim/internal/content"
	contentpg "emsim/internal/content/postgres"
	pgstore "emsim/internal/platform/postgres"
	"emsim/internal/training"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// freeTextChatScenarioJSON is a minimal caller_mode="free_text" full_case
// scenario — a throwaway fixture for this file only. The curated seed
// scenario trainees/instructors actually see (slice-112-5a-plan.md's own
// step 3) is a separate, later commit; this test only needs *a* valid
// scenario to assign, not that one.
const freeTextChatScenarioJSON = `{
  "schema": "emsim/scenario-file/v1",
  "key": "test-112-free-text-chat",
  "version": 1,
  "title": "Integration test: free-text caller chat",
  "origin": "manual",
  "body": {
    "schema": "emsim/scenario/v1",
    "exercise_type": "operator112_intake",
    "difficulty": 1,
    "intake112": {
      "mode": "full_case",
      "caller_mode": "free_text",
      "call": {"aon": "+79161313131", "local_time": "02:03", "time_zone": "Europe/Moscow"},
      "dialogue": {
        "facts": [
          {"id": "address_city", "label": "Город", "card_path": "/address/city", "knowledge": "initial", "value": "Москва"}
        ]
      },
      "reference": {
        "expected_types": ["gas_explosion"],
        "case_description": "Integration test fixture, not a training case.",
        "expected_services": ["pilot_gas_104"]
      }
    }
  }
}`

type chatFixture struct {
	pool     *pgxpool.Pool
	service  *training.Service
	operator auth.Principal
	itemID   uuid.UUID
	seq      int64
}

func (f *chatFixture) send(t *testing.T, ctx context.Context, kind training.CommandType, body any) training.Receipt {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := f.service.Execute(ctx, f.operator, f.itemID, training.Command{CommandID: uuid.New(), ExpectedSeq: f.seq, Type: kind, Payload: data}, "chat-command")
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Outcome == training.OutcomeApplied {
		f.seq = receipt.Seq
	}
	return receipt
}

// setupFreeTextChatItem builds one full_case/free_text item ready to
// chat: instructor, admin, seeded services/classifier/intake-catalog,
// this file's own throwaway scenario, a lesson started and assigned to
// one trainee.
func setupFreeTextChatItem(t *testing.T, ctx context.Context, databaseURL string) *chatFixture {
	t.Helper()
	return setupChatItemFromScenario(t, ctx, databaseURL, freeTextChatScenarioJSON, "test-112-free-text-chat")
}

// setupChatItemFromScenario is setupFreeTextChatItem for any one-scenario
// fixture file (scenarioJSON, imported under its own key).
func setupChatItemFromScenario(t *testing.T, ctx context.Context, databaseURL, scenarioJSON, key string) *chatFixture {
	t.Helper()
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	authStore := authpg.NewStore(pool)
	actor := insertInstructor(t, ctx, pool, "chat-instructor-"+uuid.NewString())
	adminID, adminRole := createContentAdmin(t, ctx, authStore, "chat-admin-"+uuid.NewString())
	contentService := content.NewService(contentpg.NewStore(pool), mustValidator(t))
	if _, err := contentService.ImportServices(ctx, openSeedFile(t, "../../seed/services.json"), adminID, adminRole, "chat-services"); err != nil {
		t.Fatal(err)
	}
	if _, err := contentService.ImportIntakeCatalog(ctx, openSeedFile(t, "../../seed/intake-catalog.json"), adminID, adminRole, "chat-catalog"); err != nil {
		t.Fatal(err)
	}
	if _, err := contentService.ImportClassifierTypes(ctx, openSeedFile(t, "../../seed/classifier.json"), adminID, adminRole, "chat-classifier"); err != nil {
		t.Fatal(err)
	}
	if _, err := contentService.ImportScenarios(ctx, map[string]io.Reader{"chat-test.json": strings.NewReader(scenarioJSON)}, adminID, adminRole, "chat-scenarios"); err != nil {
		t.Fatal(err)
	}
	trainingService := newTrainingService(pool)

	lesson, err := trainingService.CreateLesson(ctx, principal(actor, uuid.Nil), training.LessonCreate{
		ExerciseType: content.ExerciseTypeOperator112Intake, Title: "Free-text chat", Mode: training.ModeTraining, Level: auth.LevelEasy,
	}, "chat-lesson")
	if err != nil {
		t.Fatal(err)
	}
	traineeData := newTrainee("chat-trainee-"+uuid.NewString(), "")
	traineeData.ServiceCode = nil
	var trainee auth.User
	if err := authStore.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		trainee, err = authStore.InsertUser(ctx, tx, traineeData)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	workstation := insertWorkstation(t, ctx, pool, 1)
	operator := principal(trainee, workstation)

	var versionID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT sv.id FROM scenario_versions sv JOIN scenarios s ON s.id=sv.scenario_id WHERE s.source_key=$1 AND sv.version=1`,
		key).Scan(&versionID); err != nil {
		t.Fatal(err)
	}
	if _, err := trainingService.ReplaceAssignments(ctx, principal(actor, uuid.Nil), lesson.ID, []training.AssignmentInput{
		{WorkstationNo: 1, UserID: trainee.ID, ScenarioVersionIDs: []uuid.UUID{versionID}},
	}, "chat-assign"); err != nil {
		t.Fatal(err)
	}
	if _, err := trainingService.Start(ctx, principal(actor, uuid.Nil), lesson.ID, "chat-start"); err != nil {
		t.Fatal(err)
	}
	items, err := trainingService.MyItems(ctx, operator)
	if err != nil || len(items) != 1 {
		t.Fatalf("items: %+v, %v", items, err)
	}
	return &chatFixture{pool: pool, service: trainingService, operator: operator, itemID: items[0].ID}
}

func buildEmsimBinary(t *testing.T, ctx context.Context) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "emsim")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/emsim")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build emsim: %v\n%s", err, output)
	}
	return binary
}

func waitForCondition(t *testing.T, ctx context.Context, pool *pgxpool.Pool, label, query string, args ...any) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		var ready bool
		if err := pool.QueryRow(ctx, query, args...).Scan(&ready); err != nil {
			t.Fatal(err)
		}
		if ready {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout: %s", label)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// startCallerWorkerProcess is startWorkerProcess's own pattern
// (worker_process_test.go), duplicated here rather than parameterizing
// the shared helper, since this file is the only caller that needs to
// vary CALLER_REPLY_TIMEOUT/CALLER_STUB_DELAY (TestCallerChatFinalizerFailsExhaustedTurn
// deliberately sets a timeout far shorter than the stub's own delay, to
// force every attempt to time out).
func startCallerWorkerProcess(t *testing.T, binary, databaseURL, id, replyTimeout, stubDelay string, extraEnv ...string) *workerProcess {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(t.TempDir(), "worker.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	cmd := exec.Command(binary, "worker", "--role=worker")
	cmd.Env = append(os.Environ(),
		"DATABASE_URL="+databaseURL, "WORKER_ID="+id, "WORKER_ADMIN_LISTEN_ADDR="+addr,
		"WORKER_POLL_INTERVAL=50ms", "WORKER_DRAIN_TIMEOUT=100ms",
		"SHORT_CONCURRENCY=1", "LLM_CONCURRENCY=1", "STT_CONCURRENCY=1", "REPORT_CONCURRENCY=1",
		"CALLER_CONCURRENCY=1", "CALLER_REPLY_TIMEOUT="+replyTimeout, "CALLER_STUB_DELAY="+stubDelay,
		// The stub caller and no judge, explicitly: llm is the default
		// since ADR-029 and these tests run without a model.
		"CALLER_REPLIER=stub", "ASSESSMENT_JUDGE=off",
		"BLOB_ROOT="+t.TempDir(),
		"WORKER_LOCAL_TEST_POLICY=e2e-fast-v1",
	)
	// Later entries win for a duplicated key (os/exec), so a test can
	// switch this worker to the model path.
	cmd.Env = append(cmd.Env, extraEnv...)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &workerProcess{cmd: cmd, done: make(chan error, 1)}
	go func() { p.done <- cmd.Wait() }()
	t.Cleanup(func() {
		if !p.waited {
			_ = p.cmd.Process.Kill()
			select {
			case <-p.done:
			case <-time.After(10 * time.Second):
				t.Error("worker cleanup timeout")
			}
		}
	})
	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case err := <-p.done:
			p.waited = true
			content, _ := os.ReadFile(log.Name())
			t.Fatalf("worker exited before readiness: %v\n%s", err, content)
		default:
		}
		response, err := client.Get("http://" + addr + "/readyz")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return p
			}
		}
		if time.Now().After(deadline) {
			content, _ := os.ReadFile(log.Name())
			t.Fatalf("worker did not become ready\n%s", content)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestCallerChatAsyncReplySequence drives the real worker process
// through six chat turns and a seventh that must repeat the sixth
// phrase (StubCallerReplier's own contract) — each one only after the
// previous reply landed, since send_caller_message rejects a new
// message while a turn is pending.
func TestCallerChatAsyncReplySequence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	databaseURL := openTestDatabase(t, ctx)
	fixture := setupFreeTextChatItem(t, ctx, databaseURL)

	binary := buildEmsimBinary(t, ctx)
	worker := startCallerWorkerProcess(t, binary, databaseURL, "caller-chat-worker", "10s", "1ms")
	defer worker.stop(t, false)

	fixture.send(t, ctx, training.CommandOpen, map[string]any{})
	if r := fixture.send(t, ctx, training.CommandAnswerIncoming, map[string]any{}); r.Outcome != training.OutcomeApplied {
		t.Fatalf("answer_incoming: %+v", r)
	}

	phrases := []string{
		"Я упал... Глаз очень болит, я его открыть не могу. Помогите, пожалуйста!",
		"Москва, улица Космонавтов, дом 1, корпус 4.",
		"На спортивной площадке возле дома, на улице",
		"Произошло примерно четыре минуты назад.",
		"Кудрявцев Алексей Иванович.\nМой телефон: +7 900 000-00-00.",
		"Хорошо, остаюсь на связи. Жду помощи.",
	}
	for turn := 1; turn <= 8; turn++ {
		r := fixture.send(t, ctx, training.CommandSendCallerMessage, map[string]any{"text": "сообщение " + uuid.NewString()})
		if r.Outcome != training.OutcomeApplied {
			t.Fatalf("turn %d send: %+v", turn, r)
		}
		seqAfterOwnCommand := r.Seq
		waitForCondition(t, ctx, fixture.pool, "worker resolves turn",
			`SELECT EXISTS(SELECT 1 FROM tasks WHERE kind='caller.reply' AND status='done' AND terminal_worker='caller-chat-worker'
			  AND (payload->>'turn')::int=$2 AND (payload->>'item_id')::uuid=$1)`,
			fixture.itemID, turn)

		item, _, _, err := fixture.service.ItemForTrainee(ctx, fixture.operator, fixture.itemID)
		if err != nil {
			t.Fatal(err)
		}
		if len(item.IntakeState.CallerTurns) != turn {
			t.Fatalf("turn %d: caller_turns = %+v", turn, item.IntakeState.CallerTurns)
		}
		got := item.IntakeState.CallerTurns[turn-1]
		if got.Status != training.CallerTurnAnswered || got.Adapter != "stub/v1" || got.Source != training.CallerTurnSourceStub || got.ResolvedAt == nil {
			t.Fatalf("turn %d: %+v", turn, got)
		}
		wantPhrase := phrases[len(phrases)-1]
		if turn <= len(phrases) {
			wantPhrase = phrases[turn-1]
		}
		lastLine := item.IntakeState.Transcript[len(item.IntakeState.Transcript)-1]
		if lastLine.Speaker != "caller" || lastLine.Text != wantPhrase {
			t.Fatalf("turn %d: last transcript line = %+v, want caller %q", turn, lastLine, wantPhrase)
		}
		if item.Seq != seqAfterOwnCommand {
			t.Fatalf("turn %d: items.seq changed from %d (after operator's own send_caller_message) to %d — a caller reply must never bump seq (ADR-024)", turn, seqAfterOwnCommand, item.Seq)
		}
	}
}

// TestCallerChatHoldCancelsPendingTurnAndLateReplyIsANoOp exercises
// ADR-024's cancellation and no-op guarantees without any real worker:
// hold_incoming must cancel a still-pending turn immediately (no race
// to win), and a reply arriving for that same turn afterwards — exactly
// what a slow worker's late caller.reply would look like — must change
// nothing.
func TestCallerChatHoldCancelsPendingTurnAndLateReplyIsANoOp(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	databaseURL := openTestDatabase(t, ctx)
	fixture := setupFreeTextChatItem(t, ctx, databaseURL)

	fixture.send(t, ctx, training.CommandOpen, map[string]any{})
	fixture.send(t, ctx, training.CommandAnswerIncoming, map[string]any{})
	if r := fixture.send(t, ctx, training.CommandSendCallerMessage, map[string]any{"text": "Привет"}); r.Outcome != training.OutcomeApplied {
		t.Fatalf("send: %+v", r)
	}
	// No worker process runs in this test — the caller.reply task stays
	// claimable/unclaimed throughout, so hold_incoming is guaranteed to
	// run before any reply could.
	if r := fixture.send(t, ctx, training.CommandHoldIncoming, map[string]any{}); r.Outcome != training.OutcomeApplied {
		t.Fatalf("hold: %+v", r)
	}
	item, _, _, err := fixture.service.ItemForTrainee(ctx, fixture.operator, fixture.itemID)
	if err != nil {
		t.Fatal(err)
	}
	if len(item.IntakeState.CallerTurns) != 1 {
		t.Fatalf("caller_turns: %+v", item.IntakeState.CallerTurns)
	}
	cancelled := item.IntakeState.CallerTurns[0]
	if cancelled.Status != training.CallerTurnCancelled || cancelled.Reason != "held" || cancelled.ResolvedAt == nil {
		t.Fatalf("turn after hold: %+v", cancelled)
	}
	transcriptBefore := len(item.IntakeState.Transcript)

	// Simulate a worker that had already computed a reply before hold
	// landed, now trying to apply it — ApplyCallerReply's own pending
	// check must make this a pure no-op.
	tx, err := fixture.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	outcome := training.CallerReplyOutcome{Text: "поздний ответ", Adapter: "stub/v1", Source: training.CallerTurnSourceStub}
	if err := fixture.service.ApplyCallerReply(ctx, tx, fixture.itemID, 1, outcome, time.Now().UTC()); err != nil {
		t.Fatalf("late ApplyCallerReply: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	item2, _, _, err := fixture.service.ItemForTrainee(ctx, fixture.operator, fixture.itemID)
	if err != nil {
		t.Fatal(err)
	}
	if len(item2.IntakeState.Transcript) != transcriptBefore {
		t.Fatalf("late reply must not append a transcript line: before=%d after=%d", transcriptBefore, len(item2.IntakeState.Transcript))
	}
	if item2.IntakeState.CallerTurns[0].Status != training.CallerTurnCancelled {
		t.Fatalf("late reply must not resurrect a cancelled turn: %+v", item2.IntakeState.CallerTurns[0])
	}
}

// TestCallerChatFinalizerFailsExhaustedTurn drives a real worker whose
// CALLER_REPLY_TIMEOUT is far shorter than its CALLER_STUB_DELAY, so
// every attempt at answering times out; after the task's own
// max_attempts=2 is exhausted, the registered Finalizer
// (callerReplyFinalizer) must turn the pending turn into
// CallerTurnFailed rather than leaving it pending forever, and the chat
// must recover — a new message is accepted right after.
func TestCallerChatFinalizerFailsExhaustedTurn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	databaseURL := openTestDatabase(t, ctx)
	fixture := setupFreeTextChatItem(t, ctx, databaseURL)

	fixture.send(t, ctx, training.CommandOpen, map[string]any{})
	fixture.send(t, ctx, training.CommandAnswerIncoming, map[string]any{})
	if r := fixture.send(t, ctx, training.CommandSendCallerMessage, map[string]any{"text": "Привет"}); r.Outcome != training.OutcomeApplied {
		t.Fatalf("send: %+v", r)
	}

	binary := buildEmsimBinary(t, ctx)
	worker := startCallerWorkerProcess(t, binary, databaseURL, "caller-chat-failing-worker", "1ns", "5s")
	defer worker.stop(t, false)

	// callerReplyHandler classifies Reply's own timeout as
	// tasks.Retryable (it is worth retrying up to Spec.MaxAttempts) —
	// so once attempts are exhausted, finalizeResolvedFailure computes
	// terminalStatus=TaskDeadLetter, not TaskFailed (that status is
	// reserved for a tasks.Permanent classification, which no path in
	// callerReplyHandler currently produces). Either terminal status
	// still runs callerReplyFinalizer, which is what actually turns the
	// IntakeCallerTurn into CallerTurnFailed — the domain state this
	// test cares about — so wait for any terminal tasks status here.
	waitForCondition(t, ctx, fixture.pool, "caller.reply exhausted and finalized",
		`SELECT EXISTS(SELECT 1 FROM tasks WHERE kind='caller.reply' AND scope_id=$1 AND status IN ('failed','dead_letter'))`,
		fixture.itemID)

	item, _, _, err := fixture.service.ItemForTrainee(ctx, fixture.operator, fixture.itemID)
	if err != nil {
		t.Fatal(err)
	}
	if len(item.IntakeState.CallerTurns) != 1 {
		t.Fatalf("caller_turns: %+v", item.IntakeState.CallerTurns)
	}
	failed := item.IntakeState.CallerTurns[0]
	if failed.Status != training.CallerTurnFailed || failed.Reason == "" || failed.ResolvedAt == nil {
		t.Fatalf("turn after exhaustion: %+v", failed)
	}
	if r := fixture.send(t, ctx, training.CommandSendCallerMessage, map[string]any{"text": "ещё раз"}); r.Outcome != training.OutcomeApplied {
		t.Fatalf("retry send after failure: %+v", r)
	}
}

// aiCallerChatScenarioJSON is a free_text scenario with a caller profile,
// so the worker's aicaller.Replier answers it (opening without a model,
// later turns through the model) instead of delegating to the stub.
const aiCallerChatScenarioJSON = `{
  "schema": "emsim/scenario-file/v1",
  "key": "test-112-ai-caller-warmup",
  "version": 1,
  "title": "Integration test: AI caller warm-up",
  "origin": "manual",
  "body": {
    "schema": "emsim/scenario/v1",
    "exercise_type": "operator112_intake",
    "difficulty": 1,
    "intake112": {
      "mode": "full_case",
      "caller_mode": "free_text",
      "call": {"aon": "+79161313131", "local_time": "02:03", "time_zone": "Europe/Moscow"},
      "dialogue": {
        "caller": {
          "persona": "Ты — встревоженный очевидец, говоришь коротко.",
          "opening": {"id": "opening", "text": "Помогите, пахнет газом!", "reveals": []}
        },
        "facts": [
          {"id": "address_city", "label": "Город", "card_path": "/address/city", "knowledge": "initial", "value": "Москва",
           "statement": "Мы в Москве.", "disclosure_patterns": ["москв"]}
        ]
      },
      "reference": {
        "expected_types": ["gas_explosion"],
        "case_description": "Integration test fixture, not a training case.",
        "expected_services": ["pilot_gas_104"]
      }
    }
  }
}`

type recordedChatRequest struct {
	MaxTokens int `json:"max_tokens"`
	Messages  []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

// TestCallerWarmupPrimesModelAtAnswerAndFirstLine drives a real worker
// on the model path (ADR-029): answering the call enqueues a warm-up of
// the system prompt alone, the first operator line a warm-up of that
// line plus the scenario's opening, both with a single-token budget; the
// opening itself is held back by CALLER_OPENING_DELAY; and the first
// real model reply starts with exactly the warmed messages, so a
// prefix-caching server reuses the warm-ups' work.
func TestCallerWarmupPrimesModelAtAnswerAndFirstLine(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	databaseURL := openTestDatabase(t, ctx)
	fixture := setupChatItemFromScenario(t, ctx, databaseURL, aiCallerChatScenarioJSON, "test-112-ai-caller-warmup")
	fixture.service.WithCallerTiming(training.CallerTiming{Warmup: true, OpeningDelay: time.Second})

	var mu sync.Mutex
	var requests []recordedChatRequest
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req recordedChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		mu.Lock()
		requests = append(requests, req)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Мы в Москве."},"finish_reason":"stop"}]}`))
	}))
	defer model.Close()
	recorded := func() []recordedChatRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]recordedChatRequest(nil), requests...)
	}
	warmedStage := func(stage string) {
		waitForCondition(t, ctx, fixture.pool, "warm-up "+stage+" done",
			`SELECT EXISTS(SELECT 1 FROM tasks WHERE kind='caller.warmup' AND status='done' AND scope_id=$1 AND payload->>'stage'=$2 AND result->>'warmed'='true')`,
			fixture.itemID, stage)
	}

	binary := buildEmsimBinary(t, ctx)
	worker := startCallerWorkerProcess(t, binary, databaseURL, "caller-warmup-worker", "10s", "1ms",
		"CALLER_REPLIER=llm", "CALLER_LLM_URL="+model.URL+"/v1", "CALLER_LLM_MODEL=test-model")
	defer worker.stop(t, false)

	fixture.send(t, ctx, training.CommandOpen, map[string]any{})
	if r := fixture.send(t, ctx, training.CommandAnswerIncoming, map[string]any{}); r.Outcome != training.OutcomeApplied {
		t.Fatalf("answer_incoming: %+v", r)
	}
	warmedStage(training.CallerWarmupStageSystem)
	got := recorded()
	if len(got) != 1 {
		t.Fatalf("model requests after answering = %d, want exactly the system warm-up", len(got))
	}
	system := got[0]
	if system.MaxTokens != 1 || len(system.Messages) != 2 || system.Messages[0].Role != "system" ||
		system.Messages[1].Role != "user" || system.Messages[1].Content != "" {
		t.Fatalf("unexpected system warm-up request: %+v", system)
	}

	fixture.send(t, ctx, training.CommandSendCallerMessage, map[string]any{"text": "112, что у вас случилось?"})
	warmedStage(training.CallerWarmupStageOpening)
	waitForCondition(t, ctx, fixture.pool, "opening applied",
		`SELECT EXISTS(SELECT 1 FROM tasks WHERE kind='caller.reply' AND status='done' AND (payload->>'item_id')::uuid=$1 AND (payload->>'turn')::int=1)`,
		fixture.itemID)
	var delayed float64
	if err := fixture.pool.QueryRow(ctx,
		`SELECT extract(epoch FROM (t->>'resolved_at')::timestamptz - (t->>'requested_at')::timestamptz)::float8
		   FROM items, jsonb_array_elements(intake_state->'caller_turns') t WHERE items.id=$1 AND (t->>'turn')::int=1`,
		fixture.itemID).Scan(&delayed); err != nil {
		t.Fatal(err)
	}
	if delayed < 0.9 {
		t.Fatalf("opening answered %.3fs after the first line, want CALLER_OPENING_DELAY (1s)", delayed)
	}
	got = recorded()
	if len(got) != 2 {
		t.Fatalf("model requests after the opening = %d, want the two warm-ups only", len(got))
	}
	opening := got[1]
	if opening.MaxTokens != 1 || len(opening.Messages) != 4 || opening.Messages[0] != system.Messages[0] ||
		opening.Messages[1].Content != "112, что у вас случилось?" || opening.Messages[2].Content != "Помогите, пахнет газом!" ||
		opening.Messages[3].Role != "user" || opening.Messages[3].Content != "" {
		t.Fatalf("unexpected opening warm-up request: %+v", opening)
	}

	fixture.send(t, ctx, training.CommandSendCallerMessage, map[string]any{"text": "Назовите город, пожалуйста"})
	waitForCondition(t, ctx, fixture.pool, "model reply done",
		`SELECT EXISTS(SELECT 1 FROM tasks WHERE kind='caller.reply' AND status='done' AND (payload->>'item_id')::uuid=$1 AND (payload->>'turn')::int=2)`,
		fixture.itemID)
	got = recorded()
	if len(got) != 3 {
		t.Fatalf("model requests = %d, want two warm-ups + one reply", len(got))
	}
	reply := got[2]
	if reply.MaxTokens == 1 || len(reply.Messages) < 4 {
		t.Fatalf("unexpected reply request: %+v", reply)
	}
	for i := 0; i < 3; i++ {
		if reply.Messages[i] != opening.Messages[i] {
			t.Fatalf("reply message %d differs from the warm-up prefix:\n%+v\n%+v", i, reply.Messages[i], opening.Messages[i])
		}
	}
	var warmups int
	if err := fixture.pool.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE kind='caller.warmup' AND scope_id=$1`, fixture.itemID).Scan(&warmups); err != nil {
		t.Fatal(err)
	}
	if warmups != 2 {
		t.Fatalf("caller.warmup tasks = %d, want two per dialogue", warmups)
	}
}
