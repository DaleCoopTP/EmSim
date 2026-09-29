// New test: slice 6's C6 — internal/assessment's own coordinator, auto
// revision, exhaustion finalizer and expert revisions, exercised against
// real PostgreSQL through the same training.Service pilot fixtures
// training_service_test.go already uses to close an item. Unlike
// TestWorkerProcessDrivesLessonCloseThroughRealQueue (a real compiled
// binary), these drive assessment.Service and platform/tasks.Store/
// Recovery directly — the same "package-level integration test, no
// separate process" convention task_finalizer_test.go/task_waiting_test.go
// already use for platform/tasks itself, since what is under test here is
// assessment's own transactional logic, not process boundaries.
//
//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"emsim/internal/assessment"
	assessmentdds "emsim/internal/assessment/dds"
	assessmentpg "emsim/internal/assessment/postgres"
	"emsim/internal/auth"
	"emsim/internal/content"
	contentpg "emsim/internal/content/postgres"
	pgstore "emsim/internal/platform/postgres"
	"emsim/internal/platform/tasks"
	"emsim/internal/training"
	trainingpg "emsim/internal/training/postgres"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func newAssessmentServiceForTest(pool *pgxpool.Pool, taskStore *tasks.Store) *assessment.Service {
	trainingStore := trainingpg.NewStore(pool)
	contentStore := contentpg.NewStore(pool)
	return assessment.NewService(
		assessmentpg.NewStore(pool), trainingStore, trainingStore, trainingStore, contentStore, taskStore,
		assessment.Registry{content.ExerciseTypeDDSProcessing: assessmentdds.Evaluator}, nil,
	)
}

// closePilotItemForAssessment creates, starts and closes one training-
// mode pilot item (open -> accepted -> close, ADR-017's pilot_completed
// path — the same fixture setupPilotLesson/closePilotItem already build
// for training's own tests) and returns its id.
func closePilotItemForAssessment(t *testing.T, ctx context.Context, pool *pgxpool.Pool, trainingService *training.Service) uuid.UUID {
	t.Helper()
	_, trainee, workstationID, lesson := setupPilotLesson(t, ctx, pool, trainingService, "ЮАО")
	if _, err := trainingService.Start(ctx, principal(auth.User{ID: lesson.InstructorID, Role: auth.RoleInstructor}, uuid.Nil), lesson.ID, "req-start"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	actor := principal(trainee, workstationID)
	items, err := trainingService.MyItems(ctx, actor)
	if err != nil || len(items) != 1 {
		t.Fatalf("MyItems = %+v, %v, want exactly one item", items, err)
	}
	closePilotItem(t, ctx, trainingService, actor, items[0].ID)
	return items[0].ID
}

func claimAndHandle(t *testing.T, ctx context.Context, pool *pgxpool.Pool, taskStore *tasks.Store, service *assessment.Service, workerID string) (handleErr error, ok bool) {
	t.Helper()
	lease, claimed, err := taskStore.Claim(ctx, tasks.ClaimRequest{
		Kinds: []tasks.Kind{training.KindAssessmentEvaluate}, WorkerID: workerID, Now: databaseTime(t, ctx, pool),
	})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if !claimed {
		return nil, false
	}
	return service.Handle(ctx, lease), true
}

func mustRunCoordinatorTick(t *testing.T, ctx context.Context, pool *pgxpool.Pool, service *assessment.Service) {
	t.Helper()
	if err := service.RunCoordinatorTick(ctx, "test-coordinator", databaseTime(t, ctx, pool), 10); err != nil {
		t.Fatalf("RunCoordinatorTick: %v", err)
	}
}

type assessmentRow struct {
	Kind           string
	Status         string
	Revision       int
	Score          *float64
	Passed         *bool
	BaseRevision   *int
	InputID        *uuid.UUID
	Criteria       []assessment.CriterionResult
	CriticalErrors []string
}

func readAssessments(t *testing.T, ctx context.Context, pool *pgxpool.Pool, itemID uuid.UUID) []assessmentRow {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT kind, status, revision, score, passed, base_revision, input_id, criteria, critical_errors FROM assessments WHERE item_id = $1 ORDER BY revision`, itemID)
	if err != nil {
		t.Fatalf("query assessments: %v", err)
	}
	defer rows.Close()
	var out []assessmentRow
	for rows.Next() {
		var row assessmentRow
		var criteriaRaw []byte
		if err := rows.Scan(&row.Kind, &row.Status, &row.Revision, &row.Score, &row.Passed, &row.BaseRevision, &row.InputID, &criteriaRaw, &row.CriticalErrors); err != nil {
			t.Fatalf("scan assessment row: %v", err)
		}
		var decoded []struct {
			ID     string   `json:"id"`
			Status string   `json:"status"`
			Score  *float64 `json:"score"`
		}
		if err := json.Unmarshal(criteriaRaw, &decoded); err != nil {
			t.Fatalf("decode criteria: %v", err)
		}
		for _, d := range decoded {
			row.Criteria = append(row.Criteria, assessment.CriterionResult{ID: d.ID, Status: assessment.CriterionStatus(d.Status), Score: d.Score})
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate assessments: %v", err)
	}
	return out
}

func criterionByID(criteria []assessment.CriterionResult, id string) (assessment.CriterionResult, bool) {
	for _, c := range criteria {
		if c.ID == id {
			return c, true
		}
	}
	return assessment.CriterionResult{}, false
}

// resolveAllCriteria copies auto's own criteria and replaces every
// unavailable one with an arbitrary resolved value (met) — an
// instructor resolving every JUDGE=off llm criterion by hand, the only
// way ValidateRevision's base_revision=0 full-set rule can ever be
// satisfied while JUDGE is off (slice 6 has no LLM judge at all).
func resolveAllCriteria(criteria []assessment.CriterionResult) []assessment.CriterionResult {
	resolved := make([]assessment.CriterionResult, 0, len(criteria))
	for _, c := range criteria {
		if c.Status == assessment.CriterionUnavailable {
			c.Status = assessment.CriterionMet
			c.Score = nil
		}
		resolved = append(resolved, c)
	}
	return resolved
}

func evaluateTaskStatus(t *testing.T, ctx context.Context, pool *pgxpool.Pool, itemID uuid.UUID) (status string, found bool) {
	t.Helper()
	err := pool.QueryRow(ctx, `SELECT status FROM tasks WHERE dedup_key = $1`, training.EvaluateDedupKey(itemID)).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false
	}
	if err != nil {
		t.Fatalf("read evaluate task status: %v", err)
	}
	return status, true
}

// mustAssessmentTaskRegistry builds the same task Registry
// mustTaskEnqueuer (training_service_test.go) registers — duplicated
// here because tasks.NewRecovery needs the *tasks.Registry value itself,
// which mustTaskEnqueuer does not expose (it only returns the *tasks.
// Store built from it).
func mustAssessmentTaskRegistry(t *testing.T) *tasks.Registry {
	t.Helper()
	registry, err := tasks.NewRegistry(tasks.DefaultPolicy())
	if err != nil {
		t.Fatalf("tasks.NewRegistry: %v", err)
	}
	if err := registry.Register(tasks.Spec{
		Name: training.KindLessonClose, Pool: "short", MaxAttempts: 5,
		Lease: 2 * time.Minute, RetryBase: 200 * time.Millisecond, Priority: 100,
	}); err != nil {
		t.Fatalf("register lesson.close: %v", err)
	}
	if err := registry.Register(tasks.Spec{
		Name: training.KindAssessmentEvaluate, Pool: "llm", MaxAttempts: 3,
		Lease: 5 * time.Minute, RetryBase: 5 * time.Second, Priority: 100,
	}); err != nil {
		t.Fatalf("register assessment.evaluate: %v", err)
	}
	return registry
}

// TestAssessmentAutoPipelineEndToEnd covers plan item (a): close ->
// waiting -> sealed input -> pending -> a single claimed worker handler
// run produces auto rev=1. Under dds/rubric-v2 (ДДС-3, ADR-032) the pilot
// fixture's applicable criteria (T_OPEN/T_PRIMARY/D_PRIMARY) are all met
// and the rest not_applicable, so the auto is ready with a full score —
// unlike dds/rubric-v1 where G_GRAMMAR was unconditionally unavailable
// under JUDGE=off and forced needs_review by construction — and the
// recorded criteria ids/statuses match the sealed input's own rule_results.
func TestAssessmentAutoPipelineEndToEnd(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	taskStore := mustTaskEnqueuer(pool)
	trainingService := newTrainingService(pool)
	assessmentService := newAssessmentServiceForTest(pool, taskStore)

	itemID := closePilotItemForAssessment(t, ctx, pool, trainingService)

	status, found := evaluateTaskStatus(t, ctx, pool, itemID)
	if !found || status != "waiting" {
		t.Fatalf("evaluate task status = %q, found=%v, want waiting", status, found)
	}

	mustRunCoordinatorTick(t, ctx, pool, assessmentService)
	status, found = evaluateTaskStatus(t, ctx, pool, itemID)
	if !found || status != "pending" {
		t.Fatalf("evaluate task status after coordinator tick = %q, found=%v, want pending", status, found)
	}

	var inputID uuid.UUID
	var inputBodyRaw []byte
	if err := pool.QueryRow(ctx, `SELECT id, body FROM assessment_inputs WHERE item_id = $1`, itemID).Scan(&inputID, &inputBodyRaw); err != nil {
		t.Fatalf("read assessment_inputs: %v", err)
	}
	var inputBody assessment.InputBody
	if err := json.Unmarshal(inputBodyRaw, &inputBody); err != nil {
		t.Fatalf("decode input body: %v", err)
	}

	handleErr, claimed := claimAndHandle(t, ctx, pool, taskStore, assessmentService, "test-worker-1")
	if !claimed {
		t.Fatal("no assessment.evaluate task was claimable after promotion")
	}
	if handleErr != nil {
		t.Fatalf("Handle: %v", handleErr)
	}

	status, found = evaluateTaskStatus(t, ctx, pool, itemID)
	if !found || status != "done" {
		t.Fatalf("evaluate task status after Handle = %q, found=%v, want done", status, found)
	}

	rows := readAssessments(t, ctx, pool, itemID)
	if len(rows) != 1 {
		t.Fatalf("assessments for item = %d rows, want exactly 1 auto", len(rows))
	}
	auto := rows[0]
	if auto.Kind != "auto" || auto.Revision != 1 || auto.Status != "ready" || auto.Score == nil || *auto.Score != 100 || auto.Passed == nil || !*auto.Passed {
		t.Fatalf("auto assessment = %+v, want kind=auto revision=1 status=ready score=100 passed=true", auto)
	}
	if auto.InputID == nil || *auto.InputID != inputID {
		t.Fatalf("auto.input_id = %v, want %s", auto.InputID, inputID)
	}
	primary, ok := criterionByID(auto.Criteria, "D_PRIMARY")
	if !ok || primary.Status != assessment.CriterionMet {
		t.Fatalf("D_PRIMARY = %+v, want met (pilot's reference primary_decision is accepted, and the item was accepted)", primary)
	}
	progress, ok := criterionByID(auto.Criteria, "T_PROGRESS")
	if !ok || progress.Status != assessment.CriterionNotApplicable {
		t.Fatalf("T_PROGRESS = %+v, want not_applicable (no crew reports on this pilot fixture)", progress)
	}
	for _, rr := range inputBody.RuleResults {
		got, ok := criterionByID(auto.Criteria, rr.ID)
		if !ok {
			t.Fatalf("auto assessment is missing criterion %q present in the sealed input's rule_results", rr.ID)
		}
		if got.Status != rr.Status {
			t.Fatalf("criterion %q: assessment status=%q, sealed rule_results status=%q, want equal (ADR-006 reproducibility)", rr.ID, got.Status, rr.Status)
		}
	}

	var version int64
	if err := pool.QueryRow(ctx, `SELECT version FROM trainee_assessment_state WHERE user_id = (SELECT user_id FROM runs WHERE id = (SELECT run_id FROM items WHERE id = $1))`, itemID).Scan(&version); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("read trainee_assessment_state: %v", err)
	}
	if version != 0 {
		t.Fatalf("trainee_assessment_state.version = %d, want 0 (an auto alone never bumps it)", version)
	}
}

// TestAssessmentEvaluateRetryDoesNotDuplicateAuto covers plan item (b):
// a second delivery of the same claimed task (simulating the at-least-
// once redelivery a crash-before-terminal retry produces) must not
// create a second auto row — AutoByItem's own idempotent-retry guard.
func TestAssessmentEvaluateRetryDoesNotDuplicateAuto(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	taskStore := mustTaskEnqueuer(pool)
	trainingService := newTrainingService(pool)
	assessmentService := newAssessmentServiceForTest(pool, taskStore)

	itemID := closePilotItemForAssessment(t, ctx, pool, trainingService)
	mustRunCoordinatorTick(t, ctx, pool, assessmentService)

	handleErr, claimed := claimAndHandle(t, ctx, pool, taskStore, assessmentService, "test-worker-1")
	if !claimed || handleErr != nil {
		t.Fatalf("first Handle: claimed=%v err=%v", claimed, handleErr)
	}
	if rows := readAssessments(t, ctx, pool, itemID); len(rows) != 1 {
		t.Fatalf("assessments after first handle = %d, want 1", len(rows))
	}

	// Simulate the task's at-least-once redelivery (e.g. the worker
	// crashed between InsertAssessment/Terminal and a reaper requeued
	// it) by resetting it to 'pending' by hand, then claiming it again.
	var taskID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM tasks WHERE dedup_key = $1`, training.EvaluateDedupKey(itemID)).Scan(&taskID); err != nil {
		t.Fatalf("read task id: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE tasks SET status = 'pending', next_attempt_at = now(), terminal_worker = NULL, terminal_at = NULL, result = NULL
		WHERE id = $1
	`, taskID); err != nil {
		t.Fatalf("reset task to pending: %v", err)
	}

	handleErr, claimed = claimAndHandle(t, ctx, pool, taskStore, assessmentService, "test-worker-2")
	if !claimed {
		t.Fatal("redelivered task was not claimable")
	}
	if handleErr != nil {
		t.Fatalf("second Handle: %v", handleErr)
	}
	rows := readAssessments(t, ctx, pool, itemID)
	if len(rows) != 1 {
		t.Fatalf("assessments after redelivered handle = %d, want still 1 (no duplicate auto)", len(rows))
	}
}

// TestAssessmentFinalizerRecordsAutoOnExhaustion covers plan item (c):
// a task whose lease keeps expiring without ever being handled must,
// once its retry budget (max_attempts=3) is exhausted, get an auto rev=1
// recorded atomically with the task's own dead_letter write — the shared
// assessment-finalizer's exhaustion path (RFC-001 §7.4/ADR-019), driven
// here through the real Recovery.ReapExpired the same way
// test/integration/task_finalizer_test.go exercises a generic finalizer
// kind. Under dds/rubric-v2 (ДДС-3) every criterion is deterministic and
// already resolved at seal time (mustRunCoordinatorTick), so the
// exhaustion path — which never runs Service.Handle's own semantic
// step, because there is none to run — legitimately produces the same
// ready result a normal Handle would have; what this test actually
// guards is that exactly one auto gets recorded, atomically with
// dead_letter, however the task got there.
func TestAssessmentFinalizerRecordsAutoOnExhaustion(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	taskStore := mustTaskEnqueuer(pool)
	trainingService := newTrainingService(pool)
	assessmentService := newAssessmentServiceForTest(pool, taskStore)

	registry := mustAssessmentTaskRegistry(t)
	recovery, err := tasks.NewRecovery(pool, tasks.DefaultPolicy(), tasks.NoJitter{}, registry)
	if err != nil {
		t.Fatalf("NewRecovery: %v", err)
	}
	if err := recovery.RegisterFinalizer(training.KindAssessmentEvaluate, assessmentService); err != nil {
		t.Fatalf("RegisterFinalizer: %v", err)
	}

	itemID := closePilotItemForAssessment(t, ctx, pool, trainingService)
	mustRunCoordinatorTick(t, ctx, pool, assessmentService)

	// Claim and let the lease expire, three times (max_attempts=3),
	// requeuing between each via the real reaper — the same
	// claim-then-expire-then-reap cycle task_recovery_test.go's own
	// crash-recovery tests use.
	for attempt := 1; attempt <= 3; attempt++ {
		lease, claimed, err := taskStore.Claim(ctx, tasks.ClaimRequest{
			Kinds: []tasks.Kind{training.KindAssessmentEvaluate}, WorkerID: "test-crashing-worker", Now: databaseTime(t, ctx, pool),
		})
		if err != nil || !claimed {
			t.Fatalf("Claim attempt %d: claimed=%v err=%v", attempt, claimed, err)
		}
		// Both moved well into the past (not just lease_expires_at) so the
		// reaper's own ReclaimGrace buffer (DefaultPolicy: 10s) is cleared
		// too — tasks_lease_shape still requires lease_expires_at >
		// lease_started_at.
		if _, err := pool.Exec(ctx, `
			UPDATE tasks SET lease_started_at = now() - interval '1 hour', lease_expires_at = now() - interval '55 minutes'
			WHERE id = $1
		`, lease.TaskID); err != nil {
			t.Fatalf("expire lease: %v", err)
		}
		if _, err := recovery.ReapExpired(ctx); err != nil {
			t.Fatalf("ReapExpired attempt %d: %v", attempt, err)
		}
		// A requeued task's next_attempt_at is the registered RetryBase
		// (5s) in the future — fast-forward it so the next loop iteration
		// can claim it immediately instead of the test sleeping for real.
		if _, err := pool.Exec(ctx, `UPDATE tasks SET next_attempt_at = now() WHERE dedup_key = $1 AND status = 'pending'`, training.EvaluateDedupKey(itemID)); err != nil {
			t.Fatalf("fast-forward next_attempt_at: %v", err)
		}
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM tasks WHERE dedup_key = $1`, training.EvaluateDedupKey(itemID)).Scan(&status); err != nil {
		t.Fatalf("read task status: %v", err)
	}
	if status != "dead_letter" {
		t.Fatalf("task status = %q, want dead_letter after exhausting max_attempts=3", status)
	}
	rows := readAssessments(t, ctx, pool, itemID)
	if len(rows) != 1 || rows[0].Kind != "auto" || rows[0].Revision != 1 || rows[0].Status != "ready" || rows[0].Score == nil || *rows[0].Score != 100 {
		t.Fatalf("assessments after exhaustion = %+v, want exactly one ready auto rev=1 score=100", rows)
	}
}

// TestAssessmentExpertRevisionWithoutAutoCancelsEvaluate covers plan
// items (d) and (e): an instructor can fully hand-assess a closed item
// before any auto ever ran — revision=2, base_revision=0, input_id=nil —
// and doing so cancels the still-waiting evaluate task so a worker that
// claims it later (were it still claimable, which it no longer is) would
// only ever observe ErrLeaseLost, never write a late auto.
func TestAssessmentExpertRevisionWithoutAutoCancelsEvaluate(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	taskStore := mustTaskEnqueuer(pool)
	trainingService := newTrainingService(pool)
	assessmentService := newAssessmentServiceForTest(pool, taskStore)

	itemID := closePilotItemForAssessment(t, ctx, pool, trainingService)
	mustRunCoordinatorTick(t, ctx, pool, assessmentService)

	// Claim the task (as if a worker were about to process it) before
	// the expert revision preempts it — exercising (e)'s race under a
	// held lease, sequenced rather than concurrent.
	lease, claimed, err := taskStore.Claim(ctx, tasks.ClaimRequest{
		Kinds: []tasks.Kind{training.KindAssessmentEvaluate}, WorkerID: "test-racing-worker", Now: databaseTime(t, ctx, pool),
	})
	if err != nil || !claimed {
		t.Fatalf("Claim: claimed=%v err=%v", claimed, err)
	}

	var inputBodyRaw []byte
	if err := pool.QueryRow(ctx, `SELECT body FROM assessment_inputs WHERE item_id = $1`, itemID).Scan(&inputBodyRaw); err != nil {
		t.Fatalf("read assessment_inputs: %v", err)
	}
	var inputBody assessment.InputBody
	if err := json.Unmarshal(inputBodyRaw, &inputBody); err != nil {
		t.Fatalf("decode input body: %v", err)
	}
	criteria := make([]assessment.CriterionResult, 0, len(inputBody.RuleResults))
	for _, rr := range inputBody.RuleResults {
		criteria = append(criteria, assessment.CriterionResult{ID: rr.ID, Status: rr.Status})
	}
	criteria = resolveAllCriteria(criteria)

	instructorID := insertInstructor(t, ctx, pool, "assessment-instructor-"+uuid.NewString()).ID
	created, err := assessmentService.CreateExpertRevision(ctx, itemID, instructorID, assessment.RevisionInput{
		Reason: "полный ручной разбор до готовности авто-оценки", BaseRevision: 0, Criteria: criteria,
	}, "req-expert-1")
	if err != nil {
		t.Fatalf("CreateExpertRevision: %v", err)
	}
	if created.Revision != 2 || created.Kind != assessment.KindExpert || created.BaseRevision == nil || *created.BaseRevision != 0 || created.InputID != nil {
		t.Fatalf("expert assessment = %+v, want revision=2 kind=expert base_revision=0 input_id=nil", created)
	}
	if created.Status != assessment.StatusReady || created.Score == nil || created.Passed == nil {
		t.Fatalf("expert assessment = %+v, want ready with a computed score", created)
	}

	var taskStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM tasks WHERE dedup_key = $1`, training.EvaluateDedupKey(itemID)).Scan(&taskStatus); err != nil {
		t.Fatalf("read task status: %v", err)
	}
	if taskStatus != "cancelled" {
		t.Fatalf("evaluate task status = %q, want cancelled", taskStatus)
	}

	// The worker that was already holding the (now-cancelled) lease must
	// see ErrLeaseLost, never write a second/late auto.
	if err := assessmentService.Handle(ctx, lease); !errors.Is(err, tasks.ErrLeaseLost) {
		t.Fatalf("late Handle on a cancelled lease = %v, want ErrLeaseLost", err)
	}
	rows := readAssessments(t, ctx, pool, itemID)
	if len(rows) != 1 || rows[0].Kind != "expert" {
		t.Fatalf("assessments after the late handle = %+v, want only the one expert revision (no late auto)", rows)
	}

	var trainingExampleCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM training_examples WHERE assessment_id = $1`, created.ID).Scan(&trainingExampleCount); err != nil {
		t.Fatalf("count training_examples: %v", err)
	}
	if trainingExampleCount != 0 {
		t.Fatalf("training_examples for an expert-without-auto revision = %d, want 0 (plan item h: only ever recorded when an auto exists)", trainingExampleCount)
	}

	// (f) stale_revision: a second attempt with a base_revision that no
	// longer matches the item's current final revision (2) must be
	// rejected, not silently accepted as a new correction.
	_, err = assessmentService.CreateExpertRevision(ctx, itemID, instructorID, assessment.RevisionInput{
		Reason: "устаревшая правка", BaseRevision: 0, Criteria: criteria,
	}, "req-expert-stale")
	if !errors.Is(err, assessment.ErrStaleRevision) {
		t.Fatalf("stale CreateExpertRevision = %v, want ErrStaleRevision", err)
	}

	// (g) trainee_assessment_state.version grows by exactly one per new
	// final revision — one so far (the single expert revision above).
	var version int64
	if err := pool.QueryRow(ctx, `SELECT version FROM trainee_assessment_state WHERE user_id = (SELECT user_id FROM runs WHERE id = (SELECT run_id FROM items WHERE id = $1))`, itemID).Scan(&version); err != nil {
		t.Fatalf("read trainee_assessment_state: %v", err)
	}
	if version != 1 {
		t.Fatalf("trainee_assessment_state.version = %d, want 1 after the one expert revision", version)
	}

	// A correcting revision (base_revision=2, the current final) succeeds
	// and bumps version again.
	corrected, err := assessmentService.CreateExpertRevision(ctx, itemID, instructorID, assessment.RevisionInput{
		Reason: "исправление после разбора", BaseRevision: 2, Criteria: []assessment.CriterionResult{{ID: "T_OPEN", Status: assessment.CriterionNotMet}},
	}, "req-expert-2")
	if err != nil {
		t.Fatalf("correcting CreateExpertRevision: %v", err)
	}
	if corrected.Revision != 3 {
		t.Fatalf("corrected revision = %d, want 3", corrected.Revision)
	}
	if err := pool.QueryRow(ctx, `SELECT version FROM trainee_assessment_state WHERE user_id = (SELECT user_id FROM runs WHERE id = (SELECT run_id FROM items WHERE id = $1))`, itemID).Scan(&version); err != nil {
		t.Fatalf("read trainee_assessment_state: %v", err)
	}
	if version != 2 {
		t.Fatalf("trainee_assessment_state.version = %d, want 2 after a second final revision", version)
	}
}

// TestAssessmentExpertRevisionAfterAutoRecordsTrainingExamples covers
// plan item (h)'s positive case: correcting a criterion an existing auto
// got a different answer for must record a training_examples pair.
func TestAssessmentExpertRevisionAfterAutoRecordsTrainingExamples(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	taskStore := mustTaskEnqueuer(pool)
	trainingService := newTrainingService(pool)
	assessmentService := newAssessmentServiceForTest(pool, taskStore)

	itemID := closePilotItemForAssessment(t, ctx, pool, trainingService)
	mustRunCoordinatorTick(t, ctx, pool, assessmentService)
	if handleErr, claimed := claimAndHandle(t, ctx, pool, taskStore, assessmentService, "test-worker-1"); !claimed || handleErr != nil {
		t.Fatalf("Handle: claimed=%v err=%v", claimed, handleErr)
	}
	auto := readAssessments(t, ctx, pool, itemID)[0]

	criteria := resolveAllCriteria(auto.Criteria)
	// Deliberately disagree with auto's own D_PRIMARY resolution (met,
	// dds/rubric-v2 has no llm criterion left to disagree with, unlike
	// v1's G_GRAMMAR) by instead marking it not_met, so at least one
	// criterion differs.
	for i := range criteria {
		if criteria[i].ID == "D_PRIMARY" {
			criteria[i].Status = assessment.CriterionNotMet
		}
	}

	instructorID := insertInstructor(t, ctx, pool, "assessment-instructor-"+uuid.NewString()).ID
	expert, err := assessmentService.CreateExpertRevision(ctx, itemID, instructorID, assessment.RevisionInput{
		Reason: "не согласен с автооценкой по первичному решению", BaseRevision: auto.Revision, Criteria: criteria,
	}, "req-expert-disagree")
	if err != nil {
		t.Fatalf("CreateExpertRevision: %v", err)
	}
	if expert.InputID == nil {
		t.Fatal("expert.InputID = nil, want the auto's own input_id copied forward")
	}

	var exampleCount int
	var criterionID string
	if err := pool.QueryRow(ctx, `SELECT count(*), max(criterion_id) FROM training_examples WHERE assessment_id = $1`, expert.ID).Scan(&exampleCount, &criterionID); err != nil {
		t.Fatalf("count training_examples: %v", err)
	}
	if exampleCount < 1 {
		t.Fatal("training_examples count = 0, want at least one (G_GRAMMAR differs from auto)")
	}
}

// TestAssessmentInputPreparationFailureAllowsManualAssessment covers
// plan item (i): when the coordinator cannot prepare an item's input at
// all — here, no RuleEvaluator registered for its exercise_type, the
// same failure shape an unrecognized/future exercise_type would produce
// (ADR-015) — it fails the waiting task outright (no assessment_inputs
// row, no auto ever), and a fully manual expert assessment must still be
// possible for that item.
func TestAssessmentInputPreparationFailureAllowsManualAssessment(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	taskStore := mustTaskEnqueuer(pool)
	trainingService := newTrainingService(pool)
	// An assessment.Service with no RuleEvaluator registered for
	// dds_processing at all — sealInputForItem's own EvaluatorFor lookup
	// fails, which is exactly RFC-001 §7.4's "ошибка подготовки" path,
	// without needing to mutate any immutable row (scenario_versions and
	// evidence both reject UPDATE at the trigger level).
	assessmentService := assessment.NewService(
		assessmentpg.NewStore(pool), trainingpg.NewStore(pool), trainingpg.NewStore(pool), trainingpg.NewStore(pool), contentpg.NewStore(pool), taskStore,
		assessment.Registry{}, nil,
	)

	itemID := closePilotItemForAssessment(t, ctx, pool, trainingService)

	mustRunCoordinatorTick(t, ctx, pool, assessmentService)

	var taskStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM tasks WHERE dedup_key = $1`, training.EvaluateDedupKey(itemID)).Scan(&taskStatus); err != nil {
		t.Fatalf("read task status: %v", err)
	}
	if taskStatus != "failed" {
		t.Fatalf("task status = %q, want failed (input preparation failure)", taskStatus)
	}
	var inputCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM assessment_inputs WHERE item_id = $1`, itemID).Scan(&inputCount); err != nil {
		t.Fatalf("count assessment_inputs: %v", err)
	}
	if inputCount != 0 {
		t.Fatalf("assessment_inputs count = %d, want 0 (preparation never sealed)", inputCount)
	}

	// Manual assessment does not depend on rubric_effective coming from
	// a sealed input at all — the reviewer supplies every applicable
	// criterion directly, matching whatever rubric this item's own
	// lesson actually froze (dds/rubric-v2 since ДДС-3/c3, not
	// LoadDefault's fixed dds/rubric-v1).
	base, err := assessment.LoadDefaultFor(content.ExerciseTypeDDSProcessing)
	if err != nil {
		t.Fatalf("LoadDefaultFor: %v", err)
	}
	criteria := make([]assessment.CriterionResult, 0, len(base.Criteria))
	for _, c := range base.Criteria {
		criteria = append(criteria, assessment.CriterionResult{ID: c.ID, Status: assessment.CriterionMet})
	}
	instructorID := insertInstructor(t, ctx, pool, "assessment-instructor-"+uuid.NewString()).ID
	expert, err := assessmentService.CreateExpertRevision(ctx, itemID, instructorID, assessment.RevisionInput{
		Reason: "полностью ручная оценка после сбоя подготовки", BaseRevision: 0, Criteria: criteria,
	}, "req-expert-manual")
	if err != nil {
		t.Fatalf("CreateExpertRevision after preparation failure: %v", err)
	}
	if expert.Status != assessment.StatusReady || expert.InputID != nil {
		t.Fatalf("manual expert assessment = %+v, want ready with input_id=nil (RFC-001 §7.4: ручная оценка не требует успешной подготовки входа)", expert)
	}
}
