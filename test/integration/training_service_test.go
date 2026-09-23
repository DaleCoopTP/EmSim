// New test: internal/training.Service against real PostgreSQL, wired
// exactly as cmd/emsim's composition (slice 3's C5) will wire it —
// trainingpg.Store for training's own tables, *authpg.Store/*contentpg.
// Store passed directly as the UserDirectory/WorkstationDirectory/
// ScenarioReader/ServiceReader ports (they satisfy them structurally, no
// adapter type needed), and dds.Exercise as the one registered exercise
// type. Covers slice-3-plan.md's C4 DoD: concurrent start, concurrent
// commands on one item (one applied, one stale_seq), replay of an
// applied and a rejected action, replay after close, a different body
// under the same command_id (command_id_conflict), and two independent
// runs on the same scenario version.
//
//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"emsim/internal/auth"
	authpg "emsim/internal/auth/postgres"
	"emsim/internal/content"
	contentpg "emsim/internal/content/postgres"
	pgstore "emsim/internal/platform/postgres"
	"emsim/internal/platform/tasks"
	"emsim/internal/training"
	"emsim/internal/training/dds"
	"emsim/internal/training/operator112"
	trainingpg "emsim/internal/training/postgres"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// mustTaskEnqueuer builds a *tasks.Store whose Registry has
// training.KindLessonClose registered — the same Spec cmd/emsim's
// registerKinds (worker_composition.go) uses, duplicated here rather
// than imported since that lives in package main. Only the Spec's shape
// matters for what these tests assert (EnqueueTx needs a registered
// Kind to look up priority/max_attempts from); the exact numbers are not
// under test here. A failure can only mean the literal Spec below is
// malformed — a compile-time-equivalent invariant, not a runtime
// condition — so this panics rather than taking a *testing.T, keeping
// every existing newTrainingService(pool) call site unchanged.
func mustTaskEnqueuer(pool *pgxpool.Pool) *tasks.Store {
	registry, err := tasks.NewRegistry(tasks.DefaultPolicy())
	if err != nil {
		panic("mustTaskEnqueuer: " + err.Error())
	}
	if err := registry.Register(tasks.Spec{
		Name: training.KindLessonClose, Pool: "short", MaxAttempts: 5,
		Lease: 2 * time.Minute, RetryBase: 200 * time.Millisecond, Priority: 100,
	}); err != nil {
		panic("mustTaskEnqueuer: " + err.Error())
	}
	// assessment.evaluate: training only ever enqueues it (straight into
	// waiting), never claims it, but EnqueueWaitingTx still looks up the
	// Spec by Kind to fill in max_attempts — this Spec's shape is a
	// placeholder for that lookup, not the one assessment's own
	// composition (slice 6's C6) will register.
	if err := registry.Register(tasks.Spec{
		Name: training.KindAssessmentEvaluate, Pool: "llm", MaxAttempts: 3,
		Lease: 5 * time.Minute, RetryBase: 5 * time.Second, Priority: 100,
	}); err != nil {
		panic("mustTaskEnqueuer: " + err.Error())
	}
	return tasks.NewStore(pool, registry)
}

func newTrainingService(pool *pgxpool.Pool) *training.Service {
	authStore := authpg.NewStore(pool)
	contentStore := contentpg.NewStore(pool)
	return training.NewService(
		trainingpg.NewStore(pool),
		authStore, authStore,
		contentStore, contentStore,
		mustTaskEnqueuer(pool),
		map[content.ExerciseType]training.Exercise{content.ExerciseTypeDDSProcessing: dds.Exercise,
			content.ExerciseTypeOperator112Intake: operator112.New()},
	)
}

func insertWorkstation(t *testing.T, ctx context.Context, pool *pgxpool.Pool, number int) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO workstations (id, number, active) VALUES ($1, $2, true)`, id, number); err != nil {
		t.Fatalf("insert workstation: %v", err)
	}
	return id
}

func insertInstructor(t *testing.T, ctx context.Context, pool *pgxpool.Pool, login string) auth.User {
	t.Helper()
	store := authpg.NewStore(pool)
	var created auth.User
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		u := newAdmin(login)
		u.Role = auth.RoleInstructor
		var err error
		created, err = store.InsertUser(ctx, tx, u)
		return err
	}); err != nil {
		t.Fatalf("insert instructor: %v", err)
	}
	return created
}

func insertActiveTrainee(t *testing.T, ctx context.Context, pool *pgxpool.Pool, login, serviceCode string) auth.User {
	t.Helper()
	store := authpg.NewStore(pool)
	var created auth.User
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		created, err = store.InsertUser(ctx, tx, newTrainee(login, serviceCode))
		return err
	}); err != nil {
		t.Fatalf("insert trainee: %v", err)
	}
	return created
}

// pilotScenarioVersion inserts an approved scenario version shaped like
// seed/scenarios/pilot-tree-0{1,2}.json (ADR-017: primary_decision
// accepted, pilot_goal=accept_card, no events, no required call) with
// the given okrug value, and returns its id.
func pilotScenarioVersion(t *testing.T, ctx context.Context, pool *pgxpool.Pool, targetService, okrug string, createdBy uuid.UUID) uuid.UUID {
	return pilotScenarioVersionWithEvents(t, ctx, pool, targetService, okrug, createdBy, "", nil)
}

func pilotScenarioVersionWithEvents(t *testing.T, ctx context.Context, pool *pgxpool.Pool, targetService, okrug string, createdBy uuid.UUID, sourceKey string, events []content.Event) uuid.UUID {
	t.Helper()
	scenarioID, versionID := uuid.New(), uuid.New()
	body := content.Body{
		Schema:        "emsim/scenario/v1",
		TargetService: targetService,
		Card: content.Card{
			Number:              "881412",
			RegisteredAtOffsetS: -60,
			Applicant:           content.Applicant{Name: "Иванов", Phone: "+70000000000", Status: "witness"},
			Address: content.Address{
				Country: "Россия", City: "Москва", Okrug: okrug, District: "Чертаново Южное",
				Street: "Чертановская улица", House: "58", Building: "2", Entrance: "2",
			},
			Incident:         content.Incident{TypeCode: "14080106", TypeName: "Дерево упало во дворе"},
			NotificationList: []content.NotificationEntry{{Service: targetService, Status: content.ReactionAdded, Mine: true}},
		},
		Reference: content.Reference{
			PrimaryDecision: content.PrimaryDecision{Status: content.ReactionAccepted, CommentRequired: false},
			PilotGoal:       "accept_card",
		},
		Events:       events,
		Difficulty:   1,
		ExerciseType: content.ExerciseTypeDDSProcessing,
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal fixture body: %v", err)
	}
	digest := sha256.Sum256(bodyJSON)
	if _, err := pool.Exec(ctx, `
		INSERT INTO scenarios (id, source_key, title, target_service, difficulty, origin, status, created_by)
		VALUES ($1, NULLIF($2, ''), 'Fixture', $3, 1, 'manual', 'approved', $4)
	`, scenarioID, sourceKey, targetService, createdBy); err != nil {
		t.Fatalf("insert scenario: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO scenario_versions (id, scenario_id, version, status, body, digest, difficulty, created_by, approved_by, approved_at)
		VALUES ($1, $2, 1, 'approved', $3, $4, 1, $5, $5, now())
	`, versionID, scenarioID, bodyJSON, digest[:], createdBy); err != nil {
		t.Fatalf("insert scenario version: %v", err)
	}
	return versionID
}

func pilotWorkflowService(t *testing.T, ctx context.Context, pool *pgxpool.Pool, code string) {
	t.Helper()
	workflow := content.Workflow{
		Transitions: map[content.Reaction][]content.Reaction{
			content.ReactionAdded:       {content.ReactionReceived},
			content.ReactionReceived:    {content.ReactionAccepted, content.ReactionNotAccepted},
			content.ReactionNotAccepted: {content.ReactionAccepted},
		},
		CommentRequired: []content.Reaction{content.ReactionNotAccepted},
	}
	workflowJSON, err := json.Marshal(workflow)
	if err != nil {
		t.Fatalf("marshal workflow: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO services (code, name, workflow) VALUES ($1, $1, $2)`, code, workflowJSON); err != nil {
		t.Fatalf("insert service: %v", err)
	}
}

func principal(u auth.User, workstationID uuid.UUID) auth.Principal {
	return auth.Principal{UserID: u.ID, Role: u.Role, WorkstationID: &workstationID}
}

// setupPilotLesson creates one draft lesson, one workstation, one active
// trainee and one approved pilot scenario version, assigns them, and
// returns everything a test needs to Start and Execute against it.
func setupPilotLesson(t *testing.T, ctx context.Context, pool *pgxpool.Pool, service *training.Service, okrug string) (instructor, trainee auth.User, workstationID uuid.UUID, lesson training.Lesson) {
	t.Helper()
	return setupPilotLessonWithMode(t, ctx, pool, service, okrug, training.ModeTraining)
}

// setupPilotLessonWithMode is setupPilotLesson with an explicit lesson
// mode — slice 6's C5 needs an intro-mode lesson to assert that closing
// an intro item never enqueues assessment.evaluate (slice-planning.md
// §9: "intro не ставит задачу оценки").
func setupPilotLessonWithMode(t *testing.T, ctx context.Context, pool *pgxpool.Pool, service *training.Service, okrug string, mode training.Mode) (instructor, trainee auth.User, workstationID uuid.UUID, lesson training.Lesson) {
	t.Helper()
	const svc = "training_pilot_svc"
	pilotWorkflowService(t, ctx, pool, svc)

	instructor = insertInstructor(t, ctx, pool, "training-instructor-"+uuid.NewString())
	trainee = insertActiveTrainee(t, ctx, pool, "training-trainee-"+uuid.NewString(), svc)
	workstationID = insertWorkstation(t, ctx, pool, 1)
	versionID := pilotScenarioVersion(t, ctx, pool, svc, okrug, instructor.ID)

	instructorPrincipal := principal(instructor, uuid.Nil)
	created, err := service.CreateLesson(ctx, instructorPrincipal, training.LessonCreate{
		ExerciseType: content.ExerciseTypeDDSProcessing, Title: "Fixture lesson",
		Mode: mode, Level: auth.LevelEasy,
	}, "req-create")
	if err != nil {
		t.Fatalf("CreateLesson: %v", err)
	}

	lesson, err = service.ReplaceAssignments(ctx, instructorPrincipal, created.ID, []training.AssignmentInput{
		{WorkstationNo: 1, UserID: trainee.ID, ScenarioVersionIDs: []uuid.UUID{versionID}},
	}, "req-assign")
	if err != nil {
		t.Fatalf("ReplaceAssignments: %v", err)
	}
	return instructor, trainee, workstationID, lesson
}

func TestTrainingPilotOneEndToEnd(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	service := newTrainingService(pool)

	_, trainee, workstationID, lesson := setupPilotLesson(t, ctx, pool, service, "ЮАО")

	started, err := service.Start(ctx, principal(auth.User{ID: lesson.InstructorID, Role: auth.RoleInstructor}, uuid.Nil), lesson.ID, "req-start")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if started.State != training.LessonRunning {
		t.Fatalf("lesson state = %s, want running", started.State)
	}

	actor := principal(trainee, workstationID)
	run, _, _, err := service.MyRun(ctx, actor)
	if err != nil {
		t.Fatalf("MyRun: %v", err)
	}
	items, err := service.MyItems(ctx, actor)
	if err != nil || len(items) != 1 {
		t.Fatalf("MyItems = %+v, %v, want exactly one item", items, err)
	}
	item := items[0]

	openReceipt, err := service.Execute(ctx, actor, item.ID, training.Command{
		CommandID: uuid.New(), ExpectedSeq: 0, Type: training.CommandOpen, Payload: []byte(`{}`),
	}, "req-open")
	if err != nil {
		t.Fatalf("Execute(open): %v", err)
	}
	if openReceipt.Outcome != training.OutcomeApplied || openReceipt.Seq != 1 {
		t.Fatalf("open receipt = %+v", openReceipt)
	}

	acceptReceipt, err := service.Execute(ctx, actor, item.ID, training.Command{
		CommandID: uuid.New(), ExpectedSeq: 1, Type: training.CommandSetStatus,
		Payload: []byte(`{"status":"accepted"}`),
	}, "req-accept")
	if err != nil {
		t.Fatalf("Execute(set_status accepted): %v", err)
	}
	if acceptReceipt.Outcome != training.OutcomeApplied || acceptReceipt.Seq != 2 {
		t.Fatalf("accept receipt = %+v", acceptReceipt)
	}

	closeReceipt, err := service.Execute(ctx, actor, item.ID, training.Command{
		CommandID: uuid.New(), ExpectedSeq: 2, Type: training.CommandClose, Payload: []byte(`{}`),
	}, "req-close")
	if err != nil {
		t.Fatalf("Execute(close): %v", err)
	}
	if closeReceipt.Outcome != training.OutcomeApplied || closeReceipt.ItemState != training.ItemClosed {
		t.Fatalf("close receipt = %+v", closeReceipt)
	}

	var evidenceCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM evidence WHERE item_id = $1`, item.ID).Scan(&evidenceCount); err != nil {
		t.Fatalf("count evidence: %v", err)
	}
	if evidenceCount != 1 {
		t.Fatalf("evidence rows = %d, want 1", evidenceCount)
	}

	var runState string
	var runFinishedAt time.Time
	if err := pool.QueryRow(ctx, `SELECT state, finished_at FROM runs WHERE id = $1`, run.ID).Scan(&runState, &runFinishedAt); err != nil {
		t.Fatalf("read run state: %v", err)
	}
	if runState != "finished" {
		t.Fatalf("run state = %s, want finished (queue exhausted)", runState)
	}
	var lessonState string
	var lessonFinishedAt time.Time
	if err := pool.QueryRow(ctx, `SELECT state, finished_at FROM lessons WHERE id = $1`, lesson.ID).Scan(&lessonState, &lessonFinishedAt); err != nil {
		t.Fatalf("read lesson state: %v", err)
	}
	if lessonState != "finished" {
		t.Fatalf("lesson state = %s, want finished (only run exhausted)", lessonState)
	}
	if !lessonFinishedAt.Equal(runFinishedAt) {
		t.Fatalf("lesson finished_at = %s, run finished_at = %s, want same close timestamp", lessonFinishedAt, runFinishedAt)
	}

	// A closed run frees the user/workstation for a new active run
	// (slice-planning.md §4 DoD).
	if _, _, _, err := service.MyRun(ctx, actor); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("MyRun after close = %v, want ErrNotFound (no active run)", err)
	}
}

func closePilotItem(t *testing.T, ctx context.Context, service *training.Service, actor auth.Principal, itemID uuid.UUID) {
	t.Helper()
	for seq, command := range []training.Command{
		{CommandID: uuid.New(), Type: training.CommandOpen, Payload: []byte(`{}`)},
		{CommandID: uuid.New(), Type: training.CommandSetStatus, Payload: []byte(`{"status":"accepted"}`)},
		{CommandID: uuid.New(), Type: training.CommandClose, Payload: []byte(`{}`)},
	} {
		command.ExpectedSeq = int64(seq)
		receipt, err := service.Execute(ctx, actor, itemID, command, "req-queue-close")
		if err != nil || receipt.Outcome != training.OutcomeApplied {
			t.Fatalf("close item command %s = %+v, %v", command.Type, receipt, err)
		}
	}
}

// assessmentEvaluateTaskRow reads the single tasks row (if any) matching
// training.EvaluateDedupKey(itemID); ok is false if no such row exists.
func assessmentEvaluateTaskRow(t *testing.T, ctx context.Context, pool *pgxpool.Pool, itemID uuid.UUID) (status, waitReason string, payload []byte, digest []byte, ok bool) {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT status, coalesce(wait_reason, ''), payload FROM tasks WHERE dedup_key = $1`, training.EvaluateDedupKey(itemID))
	if err != nil {
		t.Fatalf("query assessment.evaluate task: %v", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
		if err := rows.Scan(&status, &waitReason, &payload); err != nil {
			t.Fatalf("scan assessment.evaluate task: %v", err)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate assessment.evaluate task rows: %v", err)
	}
	if count > 1 {
		t.Fatalf("assessment.evaluate tasks for item %s = %d, want at most 1 (dedup_key must prevent duplicates)", itemID, count)
	}
	if count == 0 {
		return "", "", nil, nil, false
	}
	if err := pool.QueryRow(ctx, `SELECT digest FROM evidence WHERE item_id = $1`, itemID).Scan(&digest); err != nil {
		t.Fatalf("read evidence digest: %v", err)
	}
	return status, waitReason, payload, digest, true
}

// TestTrainingCloseEnqueuesAssessmentEvaluateWaiting covers slice 6's C5
// (slice-6-plan.md): closing a training-mode item must enqueue exactly
// one assessment.evaluate task, straight into waiting with input_id=nil,
// atomically with the evidence it will score.
func TestTrainingCloseEnqueuesAssessmentEvaluateWaiting(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	service := newTrainingService(pool)

	_, trainee, workstationID, lesson := setupPilotLesson(t, ctx, pool, service, "ЮАО")
	if _, err := service.Start(ctx, principal(auth.User{ID: lesson.InstructorID, Role: auth.RoleInstructor}, uuid.Nil), lesson.ID, "req-start"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	actor := principal(trainee, workstationID)
	items, err := service.MyItems(ctx, actor)
	if err != nil || len(items) != 1 {
		t.Fatalf("MyItems = %+v, %v, want exactly one item", items, err)
	}
	itemID := items[0].ID

	closePilotItem(t, ctx, service, actor, itemID)

	status, waitReason, payload, digest, ok := assessmentEvaluateTaskRow(t, ctx, pool, itemID)
	if !ok {
		t.Fatal("no assessment.evaluate task was enqueued for the closed training item")
	}
	if status != "waiting" || waitReason != "awaiting_input" {
		t.Fatalf("status/wait_reason = %q/%q, want waiting/awaiting_input", status, waitReason)
	}
	var body struct {
		ItemID         uuid.UUID `json:"item_id"`
		EvidenceDigest string    `json:"evidence_digest"`
		RubricVersion  string    `json:"rubric_version"`
		InputID        *string   `json:"input_id"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if body.ItemID != itemID {
		t.Fatalf("payload.item_id = %s, want %s", body.ItemID, itemID)
	}
	if body.EvidenceDigest != fmt.Sprintf("%x", digest) {
		t.Fatalf("payload.evidence_digest = %s, want %x (the item's own sealed evidence)", body.EvidenceDigest, digest)
	}
	if body.RubricVersion != lesson.RubricVersion {
		t.Fatalf("payload.rubric_version = %s, want %s (the lesson's frozen rubric_version)", body.RubricVersion, lesson.RubricVersion)
	}
	if body.InputID != nil {
		t.Fatalf("payload.input_id = %v, want null (sealed only by the coordinator, per RFC-001 §7.4)", *body.InputID)
	}
}

// TestTrainingIntroCloseDoesNotEnqueueAssessmentEvaluate covers
// slice-planning.md §9: an intro-mode item's close must not create an
// assessment.evaluate task at all.
func TestTrainingIntroCloseDoesNotEnqueueAssessmentEvaluate(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	service := newTrainingService(pool)

	_, trainee, workstationID, lesson := setupPilotLessonWithMode(t, ctx, pool, service, "ЮАО", training.ModeIntro)
	if _, err := service.Start(ctx, principal(auth.User{ID: lesson.InstructorID, Role: auth.RoleInstructor}, uuid.Nil), lesson.ID, "req-start"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	actor := principal(trainee, workstationID)
	items, err := service.MyItems(ctx, actor)
	if err != nil || len(items) != 1 {
		t.Fatalf("MyItems = %+v, %v, want exactly one item", items, err)
	}
	itemID := items[0].ID

	closePilotItem(t, ctx, service, actor, itemID)

	if _, _, _, _, ok := assessmentEvaluateTaskRow(t, ctx, pool, itemID); ok {
		t.Fatal("an intro item's close enqueued an assessment.evaluate task, want none")
	}
}

// TestTrainingCloseReplayDoesNotDuplicateAssessmentEvaluate closes the
// same item twice with the identical close command_id (the client's own
// retry-until-receipt behaviour, RFC-001 §7.1) and asserts the second
// call is a replay that does not enqueue a second assessment.evaluate
// task.
func TestTrainingCloseReplayDoesNotDuplicateAssessmentEvaluate(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	service := newTrainingService(pool)

	_, trainee, workstationID, lesson := setupPilotLesson(t, ctx, pool, service, "ЮАО")
	if _, err := service.Start(ctx, principal(auth.User{ID: lesson.InstructorID, Role: auth.RoleInstructor}, uuid.Nil), lesson.ID, "req-start"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	actor := principal(trainee, workstationID)
	items, err := service.MyItems(ctx, actor)
	if err != nil || len(items) != 1 {
		t.Fatalf("MyItems = %+v, %v, want exactly one item", items, err)
	}
	itemID := items[0].ID

	if _, err := service.Execute(ctx, actor, itemID, training.Command{
		CommandID: uuid.New(), ExpectedSeq: 0, Type: training.CommandOpen, Payload: []byte(`{}`),
	}, "req-open"); err != nil {
		t.Fatalf("Execute(open): %v", err)
	}
	if _, err := service.Execute(ctx, actor, itemID, training.Command{
		CommandID: uuid.New(), ExpectedSeq: 1, Type: training.CommandSetStatus, Payload: []byte(`{"status":"accepted"}`),
	}, "req-accept"); err != nil {
		t.Fatalf("Execute(set_status accepted): %v", err)
	}

	closeCommandID := uuid.New()
	closeCommand := training.Command{CommandID: closeCommandID, ExpectedSeq: 2, Type: training.CommandClose, Payload: []byte(`{}`)}
	first, err := service.Execute(ctx, actor, itemID, closeCommand, "req-close")
	if err != nil || first.Outcome != training.OutcomeApplied || first.Replayed {
		t.Fatalf("first close = %+v, %v, want applied, not replayed", first, err)
	}
	_, _, firstPayload, _, ok := assessmentEvaluateTaskRow(t, ctx, pool, itemID)
	if !ok {
		t.Fatal("first close did not enqueue assessment.evaluate")
	}

	second, err := service.Execute(ctx, actor, itemID, closeCommand, "req-close-retry")
	if err != nil || second.Outcome != training.OutcomeApplied || !second.Replayed {
		t.Fatalf("second close = %+v, %v, want applied and replayed=true", second, err)
	}
	_, _, secondPayload, _, ok := assessmentEvaluateTaskRow(t, ctx, pool, itemID)
	if !ok {
		t.Fatal("assessment.evaluate task disappeared after a replayed close")
	}
	if string(firstPayload) != string(secondPayload) {
		t.Fatalf("payload changed across replay: %s -> %s, want the same row untouched", firstPayload, secondPayload)
	}
}

func TestTrainingGroupAssignmentsAdvanceIndependentQueues(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	service := newTrainingService(pool)

	const svc = "training_group_queue_svc"
	pilotWorkflowService(t, ctx, pool, svc)
	instructor := insertInstructor(t, ctx, pool, "training-group-instructor-"+uuid.NewString())
	traineeA := insertActiveTrainee(t, ctx, pool, "training-group-a-"+uuid.NewString(), svc)
	traineeB := insertActiveTrainee(t, ctx, pool, "training-group-b-"+uuid.NewString(), svc)
	workstationA := insertWorkstation(t, ctx, pool, 211)
	workstationB := insertWorkstation(t, ctx, pool, 212)
	versionA := pilotScenarioVersion(t, ctx, pool, svc, "ЮАО", instructor.ID)
	versionB := pilotScenarioVersion(t, ctx, pool, svc, "ЮЗАО", instructor.ID)
	instructorActor := principal(instructor, uuid.Nil)

	lesson, err := service.CreateLesson(ctx, instructorActor, training.LessonCreate{
		ExerciseType: content.ExerciseTypeDDSProcessing, Title: "Group queues", Mode: training.ModeTraining, Level: auth.LevelEasy,
	}, "req-group-create")
	if err != nil {
		t.Fatalf("CreateLesson: %v", err)
	}
	if _, err := service.ReplaceAssignments(ctx, instructorActor, lesson.ID, []training.AssignmentInput{
		{WorkstationNo: 211, UserID: traineeA.ID, ScenarioVersionIDs: []uuid.UUID{versionA, versionB}},
		{WorkstationNo: 212, UserID: traineeB.ID, ScenarioVersionIDs: []uuid.UUID{versionB, versionA}},
	}, "req-group-assign"); err != nil {
		t.Fatalf("ReplaceAssignments: %v", err)
	}
	if _, err := service.Start(ctx, instructorActor, lesson.ID, "req-group-start"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	actorA, actorB := principal(traineeA, workstationA), principal(traineeB, workstationB)
	itemsA, err := service.MyItems(ctx, actorA)
	if err != nil || len(itemsA) != 1 || itemsA[0].ScenarioVersionID != versionA {
		t.Fatalf("initial A items = %+v, %v", itemsA, err)
	}
	itemsB, err := service.MyItems(ctx, actorB)
	if err != nil || len(itemsB) != 1 || itemsB[0].ScenarioVersionID != versionB {
		t.Fatalf("initial B items = %+v, %v", itemsB, err)
	}

	closePilotItem(t, ctx, service, actorA, itemsA[0].ID)
	itemsA, err = service.MyItems(ctx, actorA)
	if err != nil || len(itemsA) != 2 || itemsA[1].ScenarioVersionID != versionB || itemsA[1].State != training.ItemOffered {
		t.Fatalf("A after first close = %+v, %v", itemsA, err)
	}
	if _, _, _, err := service.MyRun(ctx, actorA); err != nil {
		t.Fatalf("A run ended before queue exhausted: %v", err)
	}
	closePilotItem(t, ctx, service, actorB, itemsB[0].ID)
	itemsB, err = service.MyItems(ctx, actorB)
	if err != nil || len(itemsB) != 2 || itemsB[1].ScenarioVersionID != versionA || itemsB[1].State != training.ItemOffered {
		t.Fatalf("B after first close = %+v, %v", itemsB, err)
	}

	closePilotItem(t, ctx, service, actorA, itemsA[1].ID)
	if _, _, _, err := service.MyRun(ctx, actorA); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("A run after queue exhausted = %v, want ErrNotFound", err)
	}
	closePilotItem(t, ctx, service, actorB, itemsB[1].ID)
	var state string
	if err := pool.QueryRow(ctx, `SELECT state FROM lessons WHERE id=$1`, lesson.ID).Scan(&state); err != nil {
		t.Fatalf("read lesson state: %v", err)
	}
	if state != string(training.LessonFinished) {
		t.Fatalf("lesson state = %q, want finished after both queues", state)
	}
}

func TestTrainingHardQueueRequiresPositiveSpawnInterval(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	service := newTrainingService(pool)

	const svc = "training_hard_queue_svc"
	pilotWorkflowService(t, ctx, pool, svc)
	instructor := insertInstructor(t, ctx, pool, "training-hard-instructor-"+uuid.NewString())
	trainee := insertActiveTrainee(t, ctx, pool, "training-hard-trainee-"+uuid.NewString(), svc)
	insertWorkstation(t, ctx, pool, 221)
	versionA := pilotScenarioVersion(t, ctx, pool, svc, "ЮАО", instructor.ID)
	versionB := pilotScenarioVersion(t, ctx, pool, svc, "ЮЗАО", instructor.ID)
	actor := principal(instructor, uuid.Nil)

	lesson, err := service.CreateLesson(ctx, actor, training.LessonCreate{
		ExerciseType: content.ExerciseTypeDDSProcessing, Title: "Hard queue", Mode: training.ModeTraining, Level: auth.LevelHard,
	}, "req-hard-create")
	if err != nil {
		t.Fatalf("CreateLesson: %v", err)
	}
	_, err = service.ReplaceAssignments(ctx, actor, lesson.ID, []training.AssignmentInput{
		{WorkstationNo: 221, UserID: trainee.ID, ScenarioVersionIDs: []uuid.UUID{versionA, versionB}},
	}, "req-hard-assign")
	var validation *training.ValidationError
	if !errors.As(err, &validation) || validation.Field != "timing.spawn_every_s" {
		t.Fatalf("hard queue without interval = %v, want timing.spawn_every_s validation", err)
	}
}

func TestTrainingTickDeliversSpawnAndHardOffer(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	service := newTrainingService(pool)

	const svc = "training_tick_svc"
	pilotWorkflowService(t, ctx, pool, svc)
	instructor := insertInstructor(t, ctx, pool, "training-tick-instructor-"+uuid.NewString())
	target := pilotScenarioVersionWithEvents(t, ctx, pool, svc, "ЮАО", instructor.ID, "tick-target", nil)
	spawn := pilotScenarioVersionWithEvents(t, ctx, pool, svc, "ЮАО", instructor.ID, "tick-spawn", []content.Event{{
		Key: "e1", AtS: 0, Since: "offered", Delivery: "spawn_card",
		Spawn: &content.EventSpawn{Kind: "scenario", ScenarioKey: "tick-target", Version: 1},
	}})
	trainee := insertActiveTrainee(t, ctx, pool, "training-tick-trainee-"+uuid.NewString(), svc)
	workstation := insertWorkstation(t, ctx, pool, 231)
	actor := principal(instructor, uuid.Nil)
	lesson, err := service.CreateLesson(ctx, actor, training.LessonCreate{
		ExerciseType: content.ExerciseTypeDDSProcessing, Title: "Event tick", Mode: training.ModeTraining, Level: auth.LevelEasy,
	}, "req-event-create")
	if err != nil {
		t.Fatalf("CreateLesson: %v", err)
	}
	if _, err := service.ReplaceAssignments(ctx, actor, lesson.ID, []training.AssignmentInput{
		{WorkstationNo: 231, UserID: trainee.ID, ScenarioVersionIDs: []uuid.UUID{spawn, target}},
	}, "req-event-assign"); err != nil {
		t.Fatalf("ReplaceAssignments: %v", err)
	}
	if _, err := service.Start(ctx, actor, lesson.ID, "req-event-start"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := service.Tick(ctx); err != nil {
		t.Fatalf("Tick spawn event: %v", err)
	}
	items, err := service.MyItems(ctx, principal(trainee, workstation))
	if err != nil || len(items) != 2 || items[1].ScenarioVersionID != target {
		t.Fatalf("spawn tick items = %+v, %v", items, err)
	}
	var eventState string
	if err := pool.QueryRow(ctx, `SELECT state FROM item_events WHERE item_id=$1 AND event_key='e1'`, items[0].ID).Scan(&eventState); err != nil {
		t.Fatalf("read event state: %v", err)
	}
	if eventState != string(training.EventDelivered) {
		t.Fatalf("event state = %q, want delivered", eventState)
	}

	// Hard queues are driven by next_offer_at. Make that timestamp due
	// without sleeping; one Tick must add exactly one new parallel item.
	hardTrainee := insertActiveTrainee(t, ctx, pool, "training-hard-tick-"+uuid.NewString(), svc)
	hardWS := insertWorkstation(t, ctx, pool, 232)
	interval := 5
	hardLesson, err := service.CreateLesson(ctx, actor, training.LessonCreate{
		ExerciseType: content.ExerciseTypeDDSProcessing, Title: "Hard tick", Mode: training.ModeTraining, Level: auth.LevelHard,
		Timing: &training.Timing{OpenS: 30, PrimaryS: 30, CompleteS: 180, SpawnEveryS: &interval},
	}, "req-hard-tick-create")
	if err != nil {
		t.Fatalf("Create hard lesson: %v", err)
	}
	if _, err := service.ReplaceAssignments(ctx, actor, hardLesson.ID, []training.AssignmentInput{
		{WorkstationNo: 232, UserID: hardTrainee.ID, ScenarioVersionIDs: []uuid.UUID{target, target}},
	}, "req-hard-tick-assign"); err != nil {
		t.Fatalf("Replace hard assignments: %v", err)
	}
	if _, err := service.Start(ctx, actor, hardLesson.ID, "req-hard-tick-start"); err != nil {
		t.Fatalf("Start hard lesson: %v", err)
	}
	hardActor := principal(hardTrainee, hardWS)
	hardRun, _, _, err := service.MyRun(ctx, hardActor)
	if err != nil {
		t.Fatalf("MyRun hard: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE runs SET next_offer_at=clock_timestamp()-interval '1 second' WHERE id=$1`, hardRun.ID); err != nil {
		t.Fatalf("make hard run due: %v", err)
	}
	if err := service.Tick(ctx); err != nil {
		t.Fatalf("Tick hard offer: %v", err)
	}
	hardItems, err := service.MyItems(ctx, hardActor)
	if err != nil || len(hardItems) != 2 || hardItems[1].ScenarioVersionID != target {
		t.Fatalf("hard tick items = %+v, %v", hardItems, err)
	}
}

func TestTrainingTwoIndependentRunsOnSameVersion(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	service := newTrainingService(pool)

	const svc = "training_shared_svc"
	pilotWorkflowService(t, ctx, pool, svc)
	instructor := insertInstructor(t, ctx, pool, "training-instructor-shared-"+uuid.NewString())
	versionID := pilotScenarioVersion(t, ctx, pool, svc, "ЮАО", instructor.ID)
	instructorPrincipal := principal(instructor, uuid.Nil)

	var items []training.Item
	for i, ws := range []int{101, 102} {
		trainee := insertActiveTrainee(t, ctx, pool, "training-shared-trainee-"+uuid.NewString(), svc)
		wsID := insertWorkstation(t, ctx, pool, ws)
		lesson, err := service.CreateLesson(ctx, instructorPrincipal, training.LessonCreate{
			ExerciseType: content.ExerciseTypeDDSProcessing, Title: "Shared", Mode: training.ModeTraining, Level: auth.LevelEasy,
		}, "req")
		if err != nil {
			t.Fatalf("CreateLesson %d: %v", i, err)
		}
		if _, err := service.ReplaceAssignments(ctx, instructorPrincipal, lesson.ID, []training.AssignmentInput{
			{WorkstationNo: ws, UserID: trainee.ID, ScenarioVersionIDs: []uuid.UUID{versionID}},
		}, "req"); err != nil {
			t.Fatalf("ReplaceAssignments %d: %v", i, err)
		}
		if _, err := service.Start(ctx, instructorPrincipal, lesson.ID, "req"); err != nil {
			t.Fatalf("Start %d: %v", i, err)
		}
		its, err := service.MyItems(ctx, principal(trainee, wsID))
		if err != nil || len(its) != 1 {
			t.Fatalf("MyItems %d = %+v, %v", i, its, err)
		}
		items = append(items, its[0])
	}

	if items[0].ID == items[1].ID {
		t.Fatal("two independent assignments produced the same item")
	}
	if items[0].RunID == items[1].RunID {
		t.Fatal("two independent assignments share a run")
	}
	if items[0].ScenarioVersionID != items[1].ScenarioVersionID {
		t.Fatal("expected both items to reference the same shared scenario version")
	}
}

func TestTrainingConcurrentCommandsOneApplied(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	service := newTrainingService(pool)

	_, trainee, workstationID, lesson := setupPilotLesson(t, ctx, pool, service, "ЮАО")
	if _, err := service.Start(ctx, principal(auth.User{ID: lesson.InstructorID, Role: auth.RoleInstructor}, uuid.Nil), lesson.ID, "req"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	actor := principal(trainee, workstationID)
	items, err := service.MyItems(ctx, actor)
	if err != nil || len(items) != 1 {
		t.Fatalf("MyItems: %+v, %v", items, err)
	}
	item := items[0]

	// Both commands target expected_seq=0 (the item's current seq) with
	// two different command_ids — exactly one may apply.
	results := make(chan training.Receipt, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := service.Execute(ctx, actor, item.ID, training.Command{
				CommandID: uuid.New(), ExpectedSeq: 0, Type: training.CommandOpen, Payload: []byte(`{}`),
			}, "req-concurrent")
			if err != nil {
				errs <- err
				return
			}
			results <- r
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		t.Fatalf("Execute returned an error instead of a rejected Receipt: %v", e)
	}

	var applied, staleRejected int
	for r := range results {
		switch {
		case r.Outcome == training.OutcomeApplied:
			applied++
		case r.Outcome == training.OutcomeRejected && r.ErrorCode != nil && *r.ErrorCode == training.RejectStaleSeq:
			staleRejected++
		default:
			t.Fatalf("unexpected receipt: %+v", r)
		}
	}
	if applied != 1 || staleRejected != 1 {
		t.Fatalf("applied=%d staleRejected=%d, want 1 and 1", applied, staleRejected)
	}
}

func TestTrainingReplayAndCommandIDConflict(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	service := newTrainingService(pool)

	_, trainee, workstationID, lesson := setupPilotLesson(t, ctx, pool, service, "ЮАО")
	if _, err := service.Start(ctx, principal(auth.User{ID: lesson.InstructorID, Role: auth.RoleInstructor}, uuid.Nil), lesson.ID, "req"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	actor := principal(trainee, workstationID)
	items, err := service.MyItems(ctx, actor)
	if err != nil || len(items) != 1 {
		t.Fatalf("MyItems: %+v, %v", items, err)
	}
	item := items[0]

	openCommandID := uuid.New()
	first, err := service.Execute(ctx, actor, item.ID, training.Command{
		CommandID: openCommandID, ExpectedSeq: 0, Type: training.CommandOpen, Payload: []byte(`{}`),
	}, "req")
	if err != nil || first.Outcome != training.OutcomeApplied {
		t.Fatalf("first open = %+v, %v", first, err)
	}

	// Replay: identical command_id and body -> same receipt, replayed=true,
	// no second effect (seq stays 1, not 2).
	replay, err := service.Execute(ctx, actor, item.ID, training.Command{
		CommandID: openCommandID, ExpectedSeq: 0, Type: training.CommandOpen, Payload: []byte(`{}`),
	}, "req")
	if err != nil {
		t.Fatalf("replay open: %v", err)
	}
	if !replay.Replayed || replay.Seq != first.Seq || replay.Outcome != first.Outcome {
		t.Fatalf("replay = %+v, want same outcome/seq as %+v with Replayed=true", replay, first)
	}

	// A different body under the same command_id is a conflict, not a
	// second effect and not the original receipt.
	_, err = service.Execute(ctx, actor, item.ID, training.Command{
		CommandID: openCommandID, ExpectedSeq: 1, Type: training.CommandAddComment, Payload: []byte(`{"text":"x"}`),
	}, "req")
	if !errors.Is(err, training.ErrCommandIDConflict) {
		t.Fatalf("different body under same command_id = %v, want ErrCommandIDConflict", err)
	}

	// A rejected command's replay must also return the same rejection,
	// not silently succeed or error.
	rejectCommandID := uuid.New()
	rejected, err := service.Execute(ctx, actor, item.ID, training.Command{
		CommandID: rejectCommandID, ExpectedSeq: 0, Type: training.CommandOpen, Payload: []byte(`{}`),
	}, "req")
	if err != nil || rejected.Outcome != training.OutcomeRejected || rejected.ErrorCode == nil || *rejected.ErrorCode != training.RejectStaleSeq {
		t.Fatalf("second open (stale) = %+v, %v", rejected, err)
	}
	rejectedReplay, err := service.Execute(ctx, actor, item.ID, training.Command{
		CommandID: rejectCommandID, ExpectedSeq: 0, Type: training.CommandOpen, Payload: []byte(`{}`),
	}, "req")
	if err != nil || !rejectedReplay.Replayed || rejectedReplay.Outcome != training.OutcomeRejected {
		t.Fatalf("replay of rejected command = %+v, %v", rejectedReplay, err)
	}

	// Close the item, then replay the original open command_id again —
	// ADR-004: a replay of an applied command still works after close.
	acceptID := uuid.New()
	if _, err := service.Execute(ctx, actor, item.ID, training.Command{
		CommandID: acceptID, ExpectedSeq: 1, Type: training.CommandSetStatus, Payload: []byte(`{"status":"accepted"}`),
	}, "req"); err != nil {
		t.Fatalf("accept: %v", err)
	}
	closeID := uuid.New()
	closeReceipt, err := service.Execute(ctx, actor, item.ID, training.Command{
		CommandID: closeID, ExpectedSeq: 2, Type: training.CommandClose, Payload: []byte(`{}`),
	}, "req")
	if err != nil || closeReceipt.ItemState != training.ItemClosed {
		t.Fatalf("close = %+v, %v", closeReceipt, err)
	}

	replayAfterClose, err := service.Execute(ctx, actor, item.ID, training.Command{
		CommandID: openCommandID, ExpectedSeq: 0, Type: training.CommandOpen, Payload: []byte(`{}`),
	}, "req")
	if err != nil {
		t.Fatalf("replay after close: %v", err)
	}
	if !replayAfterClose.Replayed || replayAfterClose.Outcome != training.OutcomeApplied {
		t.Fatalf("replay after close = %+v, want the original applied outcome", replayAfterClose)
	}

	var evidenceCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM evidence WHERE item_id = $1`, item.ID).Scan(&evidenceCount); err != nil {
		t.Fatalf("count evidence: %v", err)
	}
	if evidenceCount != 1 {
		t.Fatalf("evidence rows = %d, want 1 (replay must not create a second snapshot)", evidenceCount)
	}
}

func TestTrainingConcurrentStartIsIdempotent(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	service := newTrainingService(pool)

	_, _, _, lesson := setupPilotLesson(t, ctx, pool, service, "ЮАО")
	actorPrincipal := principal(auth.User{ID: lesson.InstructorID, Role: auth.RoleInstructor}, uuid.Nil)

	var wg sync.WaitGroup
	results := make(chan training.Lesson, 3)
	errs := make(chan error, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := service.Start(ctx, actorPrincipal, lesson.ID, "req-concurrent-start")
			if err != nil {
				errs <- err
				return
			}
			results <- l
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		t.Fatalf("concurrent Start returned an error: %v", e)
	}
	for l := range results {
		if l.State != training.LessonRunning {
			t.Fatalf("lesson state = %s, want running", l.State)
		}
	}

	var runCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM runs WHERE lesson_id = $1`, lesson.ID).Scan(&runCount); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if runCount != 1 {
		t.Fatalf("runs for lesson = %d, want exactly 1 (no duplicate from concurrent start)", runCount)
	}
}

// failingEvidenceExercise wraps dds.Exercise but always fails at the
// Evidence step — injecting a break at exactly the point
// slice-3-plan.md's C4 DoD asks for ("откат при ошибке evidence"),
// without needing a real broken digest/constraint.
type failingEvidenceExercise struct {
	training.Exercise
}

func (failingEvidenceExercise) Evidence(training.Item, []training.Action, []training.ItemEvent, int64, time.Time) (training.Evidence, error) {
	return training.Evidence{}, errors.New("injected evidence failure")
}

// TestTrainingCloseRollsBackOnEvidenceFailure: if assembling evidence
// fails, the whole close transaction must roll back — the action row,
// the item's closed state and the run's finished state all commit
// together or not at all (CLAUDE.md: "Preserve one database transaction
// where a domain change, audit record ... must be atomic").
func TestTrainingCloseRollsBackOnEvidenceFailure(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)

	authStore := authpg.NewStore(pool)
	contentStore := contentpg.NewStore(pool)
	service := training.NewService(
		trainingpg.NewStore(pool), authStore, authStore, contentStore, contentStore, mustTaskEnqueuer(pool),
		map[content.ExerciseType]training.Exercise{
			content.ExerciseTypeDDSProcessing: failingEvidenceExercise{Exercise: dds.Exercise},
		},
	)

	_, trainee, workstationID, lesson := setupPilotLesson(t, ctx, pool, service, "ЮАО")
	if _, err := service.Start(ctx, principal(auth.User{ID: lesson.InstructorID, Role: auth.RoleInstructor}, uuid.Nil), lesson.ID, "req"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	actor := principal(trainee, workstationID)
	items, err := service.MyItems(ctx, actor)
	if err != nil || len(items) != 1 {
		t.Fatalf("MyItems: %+v, %v", items, err)
	}
	item := items[0]

	if _, err := service.Execute(ctx, actor, item.ID, training.Command{
		CommandID: uuid.New(), ExpectedSeq: 0, Type: training.CommandOpen, Payload: []byte(`{}`),
	}, "req"); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := service.Execute(ctx, actor, item.ID, training.Command{
		CommandID: uuid.New(), ExpectedSeq: 1, Type: training.CommandSetStatus, Payload: []byte(`{"status":"accepted"}`),
	}, "req"); err != nil {
		t.Fatalf("accept: %v", err)
	}

	closeCommandID := uuid.New()
	_, err = service.Execute(ctx, actor, item.ID, training.Command{
		CommandID: closeCommandID, ExpectedSeq: 2, Type: training.CommandClose, Payload: []byte(`{}`),
	}, "req")
	if err == nil || err.Error() != "injected evidence failure" {
		t.Fatalf("Execute(close) = %v, want the injected evidence failure to propagate", err)
	}

	var itemState, runState string
	var evidenceCount, actionCount int
	if err := pool.QueryRow(ctx, `SELECT state FROM items WHERE id = $1`, item.ID).Scan(&itemState); err != nil {
		t.Fatalf("read item state: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT state FROM runs WHERE id = $1`, item.RunID).Scan(&runState); err != nil {
		t.Fatalf("read run state: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM evidence WHERE item_id = $1`, item.ID).Scan(&evidenceCount); err != nil {
		t.Fatalf("count evidence: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM actions WHERE item_id = $1 AND command_id = $2`, item.ID, closeCommandID).Scan(&actionCount); err != nil {
		t.Fatalf("count close action: %v", err)
	}
	if itemState != string(training.ItemInProgress) {
		t.Fatalf("item state = %s, want in_progress (close must not have partially applied)", itemState)
	}
	if runState != "active" {
		t.Fatalf("run state = %s, want active (close must not have partially applied)", runState)
	}
	if evidenceCount != 0 {
		t.Fatalf("evidence rows = %d, want 0", evidenceCount)
	}
	if actionCount != 0 {
		t.Fatalf("close action rows = %d, want 0 (the whole attempt must roll back)", actionCount)
	}

	// The item is still usable after the failed close — a retry (a new
	// command_id, since the failed attempt never committed a receipt to
	// replay) succeeds once the injected failure is gone.
	realService := newTrainingService(pool)
	retryReceipt, err := realService.Execute(ctx, actor, item.ID, training.Command{
		CommandID: uuid.New(), ExpectedSeq: 2, Type: training.CommandClose, Payload: []byte(`{}`),
	}, "req")
	if err != nil || retryReceipt.ItemState != training.ItemClosed {
		t.Fatalf("retry close = %+v, %v", retryReceipt, err)
	}
}

// TestTrainingRecoverMarksOpenItemsIdempotently is slice-4-plan.md's C6:
// Service.Recover marks a running lesson's still-open item with one
// interruption per distinct recovery_id, never touches offered_at/
// deadlines, is a no-op on a repeated recovery_id, and the accumulated
// markers end up in the item's own evidence once it closes.
func TestTrainingRecoverMarksOpenItemsIdempotently(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	service := newTrainingService(pool)

	_, trainee, workstationID, lesson := setupPilotLesson(t, ctx, pool, service, "ЮАО")
	if _, err := service.Start(ctx, principal(auth.User{ID: lesson.InstructorID, Role: auth.RoleInstructor}, uuid.Nil), lesson.ID, "req-start"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	actor := principal(trainee, workstationID)
	items, err := service.MyItems(ctx, actor)
	if err != nil || len(items) != 1 {
		t.Fatalf("MyItems = %+v, %v", items, err)
	}
	item := items[0]

	var offeredBefore time.Time
	var deadlinesBefore []byte
	if err := pool.QueryRow(ctx, `SELECT offered_at, deadlines FROM items WHERE id=$1`, item.ID).Scan(&offeredBefore, &deadlinesBefore); err != nil {
		t.Fatalf("read item before recovery: %v", err)
	}

	recoveryID := uuid.New()
	affected, err := service.Recover(ctx, recoveryID, "server_restart")
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if len(affected) != 1 || affected[0] != item.ID {
		t.Fatalf("Recover affected = %+v, want [%s]", affected, item.ID)
	}

	// A retried call with the same recovery_id must append nothing more.
	if affected2, err := service.Recover(ctx, recoveryID, "server_restart"); err != nil || len(affected2) != 0 {
		t.Fatalf("repeat Recover = %+v, %v, want no affected items", affected2, err)
	}

	var offeredAfter time.Time
	var deadlinesAfter, interruptionsJSON []byte
	if err := pool.QueryRow(ctx, `SELECT offered_at, deadlines, interruptions FROM items WHERE id=$1`, item.ID).Scan(&offeredAfter, &deadlinesAfter, &interruptionsJSON); err != nil {
		t.Fatalf("read item after recovery: %v", err)
	}
	if !offeredBefore.Equal(offeredAfter) {
		t.Fatalf("offered_at changed: %s -> %s", offeredBefore, offeredAfter)
	}
	if string(deadlinesBefore) != string(deadlinesAfter) {
		t.Fatalf("deadlines changed: %s -> %s", deadlinesBefore, deadlinesAfter)
	}
	var interruptions []training.Interruption
	if err := json.Unmarshal(interruptionsJSON, &interruptions); err != nil {
		t.Fatalf("unmarshal interruptions: %v", err)
	}
	if len(interruptions) != 1 || interruptions[0].RecoveryID != recoveryID || interruptions[0].Cause != "server_restart" {
		t.Fatalf("interruptions = %+v", interruptions)
	}

	// A genuinely different restart appends a second, distinct marker.
	recoveryID2 := uuid.New()
	if affected3, err := service.Recover(ctx, recoveryID2, "server_restart"); err != nil || len(affected3) != 1 {
		t.Fatalf("second Recover = %+v, %v", affected3, err)
	}

	closePilotItem(t, ctx, service, actor, item.ID)
	var evidenceBody []byte
	if err := pool.QueryRow(ctx, `SELECT body FROM evidence WHERE item_id=$1`, item.ID).Scan(&evidenceBody); err != nil {
		t.Fatalf("read evidence: %v", err)
	}
	var body training.EvidenceBody
	if err := json.Unmarshal(evidenceBody, &body); err != nil {
		t.Fatalf("unmarshal evidence: %v", err)
	}
	if len(body.Interruptions) != 2 {
		t.Fatalf("evidence interruptions = %+v, want 2", body.Interruptions)
	}
}

// TestTrainingControlReportAfterClose is slice-4-plan.md's C7: a post-
// close message from the trainee is rejected before the item closes,
// accepted afterward through the same idempotent command endpoint, never
// changes reaction/closed_at/evidence, and replays like any other action.
func TestTrainingControlReportAfterClose(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	service := newTrainingService(pool)

	_, trainee, workstationID, lesson := setupPilotLesson(t, ctx, pool, service, "ЮАО")
	if _, err := service.Start(ctx, principal(auth.User{ID: lesson.InstructorID, Role: auth.RoleInstructor}, uuid.Nil), lesson.ID, "req-start"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	actor := principal(trainee, workstationID)
	items, err := service.MyItems(ctx, actor)
	if err != nil || len(items) != 1 {
		t.Fatalf("MyItems = %+v, %v", items, err)
	}
	item := items[0]

	tooEarly, err := service.Execute(ctx, actor, item.ID, training.Command{
		CommandID: uuid.New(), ExpectedSeq: 0, Type: training.CommandControlReport,
		Payload: []byte(`{"text":"слишком рано"}`),
	}, "req-early")
	if err != nil {
		t.Fatalf("Execute(control_report, early): %v", err)
	}
	if tooEarly.Outcome != training.OutcomeRejected || tooEarly.ErrorCode == nil || *tooEarly.ErrorCode != training.RejectTransitionNotAllowed {
		t.Fatalf("early control_report receipt = %+v", tooEarly)
	}

	// open(seq 0->1), accept(1->2), close(2->3): the item's seq is 3 once
	// closePilotItem returns.
	closePilotItem(t, ctx, service, actor, item.ID)

	var evidenceBefore []byte
	if err := pool.QueryRow(ctx, `SELECT body FROM evidence WHERE item_id=$1`, item.ID).Scan(&evidenceBefore); err != nil {
		t.Fatalf("read evidence before: %v", err)
	}

	commandID := uuid.New()
	reportReceipt, err := service.Execute(ctx, actor, item.ID, training.Command{
		CommandID: commandID, ExpectedSeq: 3, Type: training.CommandControlReport,
		Payload: []byte(`{"text":"по факту пропустил статус"}`),
	}, "req-report")
	if err != nil {
		t.Fatalf("Execute(control_report): %v", err)
	}
	if reportReceipt.Outcome != training.OutcomeApplied || reportReceipt.Seq != 4 || reportReceipt.ItemState != training.ItemClosed {
		t.Fatalf("control_report receipt = %+v", reportReceipt)
	}

	var evidenceAfter []byte
	if err := pool.QueryRow(ctx, `SELECT body FROM evidence WHERE item_id=$1`, item.ID).Scan(&evidenceAfter); err != nil {
		t.Fatalf("read evidence after: %v", err)
	}
	if string(evidenceBefore) != string(evidenceAfter) {
		t.Fatalf("evidence changed by control_report")
	}
	var reaction string
	if err := pool.QueryRow(ctx, `SELECT reaction FROM items WHERE id=$1`, item.ID).Scan(&reaction); err != nil {
		t.Fatalf("read item: %v", err)
	}
	if reaction != "accepted" {
		t.Fatalf("reaction = %s, want unchanged (accepted)", reaction)
	}

	var reportCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM control_reports
		WHERE item_id=$1 AND action_id=(SELECT id FROM actions WHERE command_id=$2)
	`, item.ID, commandID).Scan(&reportCount); err != nil {
		t.Fatalf("count control_reports: %v", err)
	}
	if reportCount != 1 {
		t.Fatalf("control_reports rows = %d, want 1", reportCount)
	}

	replay, err := service.Execute(ctx, actor, item.ID, training.Command{
		CommandID: commandID, ExpectedSeq: 3, Type: training.CommandControlReport,
		Payload: []byte(`{"text":"по факту пропустил статус"}`),
	}, "req-report-replay")
	if err != nil {
		t.Fatalf("Execute(control_report replay): %v", err)
	}
	if !replay.Replayed || replay.Seq != reportReceipt.Seq {
		t.Fatalf("replay receipt = %+v", replay)
	}

	var reportCountAfterReplay int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM control_reports WHERE item_id=$1`, item.ID).Scan(&reportCountAfterReplay); err != nil {
		t.Fatalf("count control_reports after replay: %v", err)
	}
	if reportCountAfterReplay != 1 {
		t.Fatalf("control_reports rows after replay = %d, want 1 (replay must not insert again)", reportCountAfterReplay)
	}
}

// TestTrainingStopBarrierAndDurableClose is slice-4-plan.md's C8: Stop
// sets the barrier (epoch, stopped_at, per-item stop_cutoff_log_seq),
// rejects a late command as lesson_stopped, is idempotent on repeat, and
// enqueues exactly one lesson.close task; CloseStoppedLesson (the task's
// domain half, run here directly rather than through package main's
// lessonCloseHandler) then interrupts the still-open item at the
// lesson's own stopped_at, is safe to retry, finishes the run/lesson,
// and produces evidence with a populated singular "interruption".
func TestTrainingStopBarrierAndDurableClose(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	service := newTrainingService(pool)

	_, trainee, workstationID, lesson := setupPilotLesson(t, ctx, pool, service, "ЮАО")
	instructorActor := principal(auth.User{ID: lesson.InstructorID, Role: auth.RoleInstructor}, uuid.Nil)
	if _, err := service.Start(ctx, instructorActor, lesson.ID, "req-start"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	actor := principal(trainee, workstationID)
	items, err := service.MyItems(ctx, actor)
	if err != nil || len(items) != 1 {
		t.Fatalf("MyItems = %+v, %v", items, err)
	}
	item := items[0]

	if _, err := service.Execute(ctx, actor, item.ID, training.Command{
		CommandID: uuid.New(), ExpectedSeq: 0, Type: training.CommandOpen, Payload: []byte(`{}`),
	}, "req-open"); err != nil {
		t.Fatalf("Execute(open): %v", err)
	}
	acceptReceipt, err := service.Execute(ctx, actor, item.ID, training.Command{
		CommandID: uuid.New(), ExpectedSeq: 1, Type: training.CommandSetStatus, Payload: []byte(`{"status":"accepted"}`),
	}, "req-accept")
	if err != nil || acceptReceipt.Outcome != training.OutcomeApplied {
		t.Fatalf("Execute(accept): %+v, %v", acceptReceipt, err)
	}

	reason := "2 минуты до звонка"
	stopped, err := service.Stop(ctx, instructorActor, lesson.ID, &reason, "req-stop")
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if stopped.State != training.LessonStopped || stopped.Epoch != 1 || stopped.StopReason == nil || *stopped.StopReason != reason {
		t.Fatalf("stopped lesson = %+v", stopped)
	}
	if stopped.StoppedAt == nil {
		t.Fatal("stopped.StoppedAt is nil")
	}

	var cutoff *int64
	if err := pool.QueryRow(ctx, `SELECT stop_cutoff_log_seq FROM items WHERE id=$1`, item.ID).Scan(&cutoff); err != nil {
		t.Fatalf("read cutoff: %v", err)
	}
	if cutoff == nil || *cutoff != 2 {
		t.Fatalf("stop_cutoff_log_seq = %v, want 2 (open+accept)", cutoff)
	}

	var taskCount int
	var dedupKey string
	if err := pool.QueryRow(ctx, `SELECT count(*), max(dedup_key) FROM tasks WHERE kind='lesson.close' AND scope_id=$1`, lesson.ID).Scan(&taskCount, &dedupKey); err != nil {
		t.Fatalf("read task: %v", err)
	}
	if taskCount != 1 || dedupKey != fmt.Sprintf("lesson.close:%s:1", lesson.ID) {
		t.Fatalf("task count=%d dedup_key=%q", taskCount, dedupKey)
	}

	rejected, err := service.Execute(ctx, actor, item.ID, training.Command{
		CommandID: uuid.New(), ExpectedSeq: 2, Type: training.CommandSetStatus, Payload: []byte(`{"status":"responding"}`),
	}, "req-late")
	if err != nil {
		t.Fatalf("Execute(late): %v", err)
	}
	if rejected.Outcome != training.OutcomeRejected || rejected.ErrorCode == nil || *rejected.ErrorCode != training.RejectLessonStopped {
		t.Fatalf("late command receipt = %+v", rejected)
	}

	stoppedAgain, err := service.Stop(ctx, instructorActor, lesson.ID, nil, "req-stop-2")
	if err != nil {
		t.Fatalf("repeat Stop: %v", err)
	}
	if stoppedAgain.Epoch != 1 || stoppedAgain.StoppedAt == nil || !stoppedAgain.StoppedAt.Equal(*stopped.StoppedAt) {
		t.Fatalf("repeat stop changed state: %+v", stoppedAgain)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE kind='lesson.close' AND scope_id=$1`, lesson.ID).Scan(&taskCount); err != nil {
		t.Fatalf("recount tasks: %v", err)
	}
	if taskCount != 1 {
		t.Fatalf("repeat stop created %d tasks, want 1", taskCount)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := service.CloseStoppedLesson(ctx, tx, lesson.ID); err != nil {
		t.Fatalf("CloseStoppedLesson: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// A second, retried attempt (this task's own at-least-once delivery,
	// or a worker crash between commit and tasks.done) must be a no-op.
	tx2, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin retry: %v", err)
	}
	defer func() { _ = tx2.Rollback(ctx) }()
	if err := service.CloseStoppedLesson(ctx, tx2, lesson.ID); err != nil {
		t.Fatalf("CloseStoppedLesson (retry): %v", err)
	}
	if err := tx2.Commit(ctx); err != nil {
		t.Fatalf("commit retry: %v", err)
	}

	var itemState, closeReason string
	var closedAt time.Time
	if err := pool.QueryRow(ctx, `SELECT state, close_reason, closed_at FROM items WHERE id=$1`, item.ID).Scan(&itemState, &closeReason, &closedAt); err != nil {
		t.Fatalf("read item: %v", err)
	}
	if itemState != "interrupted" || closeReason != "interrupted" {
		t.Fatalf("item state=%s close_reason=%s, want interrupted/interrupted", itemState, closeReason)
	}
	if !closedAt.Equal(*stopped.StoppedAt) {
		t.Fatalf("closed_at=%s, want stopped_at=%s (worker delay must not count)", closedAt, *stopped.StoppedAt)
	}

	var runState, lessonState string
	if err := pool.QueryRow(ctx, `SELECT state FROM runs WHERE id=$1`, item.RunID).Scan(&runState); err != nil {
		t.Fatalf("read run: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT state FROM lessons WHERE id=$1`, lesson.ID).Scan(&lessonState); err != nil {
		t.Fatalf("read lesson: %v", err)
	}
	if runState != "finished" || lessonState != "finished" {
		t.Fatalf("run=%s lesson=%s, want both finished", runState, lessonState)
	}

	var evidenceCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM evidence WHERE item_id=$1`, item.ID).Scan(&evidenceCount); err != nil {
		t.Fatalf("count evidence: %v", err)
	}
	if evidenceCount != 1 {
		t.Fatalf("evidence rows = %d, want 1 (the retry must not insert a second)", evidenceCount)
	}

	// slice 6's C5: the stop-triggered closeInterruptedItem path must
	// also enqueue assessment.evaluate exactly once, even though
	// CloseStoppedLesson ran twice above (worker at-least-once retry).
	status, waitReason, _, _, ok := assessmentEvaluateTaskRow(t, ctx, pool, item.ID)
	if !ok {
		t.Fatal("no assessment.evaluate task was enqueued for the interrupted item")
	}
	if status != "waiting" || waitReason != "awaiting_input" {
		t.Fatalf("interrupted item's assessment.evaluate status/wait_reason = %q/%q, want waiting/awaiting_input", status, waitReason)
	}

	var evidenceBody []byte
	if err := pool.QueryRow(ctx, `SELECT body FROM evidence WHERE item_id=$1`, item.ID).Scan(&evidenceBody); err != nil {
		t.Fatalf("read evidence: %v", err)
	}
	var body training.EvidenceBody
	if err := json.Unmarshal(evidenceBody, &body); err != nil {
		t.Fatalf("unmarshal evidence: %v", err)
	}
	if body.CloseReason != training.CloseInterrupted {
		t.Fatalf("evidence close_reason = %q, want interrupted", body.CloseReason)
	}
	if body.Interruption == nil || body.Interruption.Reason != "stop" || !body.Interruption.StoppedAt.Equal(*stopped.StoppedAt) {
		t.Fatalf("evidence interruption = %+v", body.Interruption)
	}
	if body.CutoffLogSeq != 2 {
		t.Fatalf("evidence cutoff_log_seq = %d, want 2", body.CutoffLogSeq)
	}
	if body.Derived.PrimaryStatus == nil || *body.Derived.PrimaryStatus != content.ReactionAccepted {
		t.Fatalf("evidence primary_status = %v, want accepted", body.Derived.PrimaryStatus)
	}

	// control_report remains available on the now-interrupted item, and
	// the worker's own close never wrote a new action row (seq/log_seq
	// are unchanged from before it ran).
	crReceipt, err := service.Execute(ctx, actor, item.ID, training.Command{
		CommandID: uuid.New(), ExpectedSeq: 2, Type: training.CommandControlReport,
		Payload: []byte(`{"text":"не успел закрыть до стопа"}`),
	}, "req-cr")
	if err != nil || crReceipt.Outcome != training.OutcomeApplied {
		t.Fatalf("control_report after interrupt = %+v, %v", crReceipt, err)
	}

	// Stop on the now-finished lesson stays idempotent.
	stoppedAfterFinish, err := service.Stop(ctx, instructorActor, lesson.ID, nil, "req-stop-3")
	if err != nil {
		t.Fatalf("Stop after finish: %v", err)
	}
	if stoppedAfterFinish.State != training.LessonFinished {
		t.Fatalf("Stop after finish state = %s, want finished", stoppedAfterFinish.State)
	}
}

// TestTrainingMonitorSnapshotReflectsLiveState is slice-4-plan.md's C9:
// Service.Monitor reads the instructor's live view straight from
// PostgreSQL — one row per assignment with a run, active_items/queue_
// left/done tracking each trainee's own progress independently, and
// last_action reflecting the most recent command across the run's
// items (not just its currently open one).
func TestTrainingMonitorSnapshotReflectsLiveState(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	service := newTrainingService(pool)

	const svc = "training_monitor_svc"
	pilotWorkflowService(t, ctx, pool, svc)
	instructor := insertInstructor(t, ctx, pool, "training-monitor-instructor-"+uuid.NewString())
	traineeA := insertActiveTrainee(t, ctx, pool, "training-monitor-a-"+uuid.NewString(), svc)
	traineeB := insertActiveTrainee(t, ctx, pool, "training-monitor-b-"+uuid.NewString(), svc)
	workstationA := insertWorkstation(t, ctx, pool, 221)
	insertWorkstation(t, ctx, pool, 222)
	versionA := pilotScenarioVersion(t, ctx, pool, svc, "ЮАО", instructor.ID)
	versionB := pilotScenarioVersion(t, ctx, pool, svc, "ЮЗАО", instructor.ID)
	instructorActor := principal(instructor, uuid.Nil)

	lesson, err := service.CreateLesson(ctx, instructorActor, training.LessonCreate{
		ExerciseType: content.ExerciseTypeDDSProcessing, Title: "Monitor", Mode: training.ModeTraining, Level: auth.LevelEasy,
	}, "req-monitor-create")
	if err != nil {
		t.Fatalf("CreateLesson: %v", err)
	}
	if _, err := service.ReplaceAssignments(ctx, instructorActor, lesson.ID, []training.AssignmentInput{
		{WorkstationNo: 221, UserID: traineeA.ID, ScenarioVersionIDs: []uuid.UUID{versionA, versionB}},
		{WorkstationNo: 222, UserID: traineeB.ID, ScenarioVersionIDs: []uuid.UUID{versionB}},
	}, "req-monitor-assign"); err != nil {
		t.Fatalf("ReplaceAssignments: %v", err)
	}

	// Before start: no runs exist yet, so no rows (Monitor.rows[].run_id
	// is required — an assignment with no run is omitted, not emitted
	// with a zero id).
	before, err := service.Monitor(ctx, instructorActor, lesson.ID)
	if err != nil {
		t.Fatalf("Monitor before start: %v", err)
	}
	if len(before.Rows) != 0 {
		t.Fatalf("Monitor before start rows = %+v, want none", before.Rows)
	}

	if _, err := service.Start(ctx, instructorActor, lesson.ID, "req-monitor-start"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	actorA := principal(traineeA, workstationA)
	itemsA, err := service.MyItems(ctx, actorA)
	if err != nil || len(itemsA) != 1 {
		t.Fatalf("MyItems A = %+v, %v", itemsA, err)
	}
	if _, err := service.Execute(ctx, actorA, itemsA[0].ID, training.Command{
		CommandID: uuid.New(), ExpectedSeq: 0, Type: training.CommandOpen, Payload: []byte(`{}`),
	}, "req-monitor-open"); err != nil {
		t.Fatalf("Execute(open): %v", err)
	}

	result, err := service.Monitor(ctx, instructorActor, lesson.ID)
	if err != nil {
		t.Fatalf("Monitor: %v", err)
	}
	if len(result.Rows) != 2 {
		t.Fatalf("Monitor rows = %d, want 2", len(result.Rows))
	}
	var rowA, rowB *training.MonitorRow
	for i := range result.Rows {
		switch result.Rows[i].User.ID {
		case traineeA.ID:
			rowA = &result.Rows[i]
		case traineeB.ID:
			rowB = &result.Rows[i]
		}
	}
	if rowA == nil || rowB == nil {
		t.Fatalf("Monitor rows missing a trainee: %+v", result.Rows)
	}
	if len(rowA.ActiveItems) != 1 || rowA.Done != 0 || rowA.QueueLeft != 1 {
		t.Fatalf("rowA = %+v, want 1 active item, 0 done, queue_left=1", rowA)
	}
	if rowA.LastAction == nil || rowA.LastAction.Type != training.CommandOpen {
		t.Fatalf("rowA.LastAction = %+v, want the open command", rowA.LastAction)
	}
	if len(rowB.ActiveItems) != 1 || rowB.Done != 0 || rowB.QueueLeft != 0 {
		t.Fatalf("rowB = %+v, want 1 active item, 0 done, queue_left=0 (single-item queue)", rowB)
	}
	if rowB.LastAction != nil {
		t.Fatalf("rowB.LastAction = %+v, want nil (nothing executed yet)", rowB.LastAction)
	}

	// Closing A's item advances it into Done and offers the next queued
	// version, and Monitor reflects that on the very next read. The item
	// is already open (seq 1) from above, so continue the sequence
	// rather than reusing closePilotItem's own open-from-scratch flow.
	for seq, command := range []training.Command{
		{CommandID: uuid.New(), Type: training.CommandSetStatus, Payload: []byte(`{"status":"accepted"}`)},
		{CommandID: uuid.New(), Type: training.CommandClose, Payload: []byte(`{}`)},
	} {
		command.ExpectedSeq = int64(seq + 1)
		receipt, err := service.Execute(ctx, actorA, itemsA[0].ID, command, "req-monitor-close")
		if err != nil || receipt.Outcome != training.OutcomeApplied {
			t.Fatalf("close item command %s = %+v, %v", command.Type, receipt, err)
		}
	}
	afterClose, err := service.Monitor(ctx, instructorActor, lesson.ID)
	if err != nil {
		t.Fatalf("Monitor after close: %v", err)
	}
	for i := range afterClose.Rows {
		if afterClose.Rows[i].User.ID != traineeA.ID {
			continue
		}
		if afterClose.Rows[i].Done != 1 || len(afterClose.Rows[i].ActiveItems) != 1 || afterClose.Rows[i].QueueLeft != 0 {
			t.Fatalf("rowA after close = %+v, want done=1, 1 active item, queue_left=0", afterClose.Rows[i])
		}
	}
}

// TestTrainingHardQueueOffersOneCardPerTickAfterLongOutage is slice-4-
// plan.md's C11: RFC-001 §7.2 requires that "после задержки или
// рестарта за tick выдаётся не более одной карточки на run, пропущенные
// интервалы не воспроизводятся пачкой" and that next_offer_at is always
// computed from the real offer time, never by catching up to now(). A
// run's next_offer_at is pushed an hour into the past (many multiples of
// a 5s spawn interval), simulating a long scheduler outage; one Tick
// must offer exactly one new card, and a second, immediate Tick must not
// offer a third.
func TestTrainingHardQueueOffersOneCardPerTickAfterLongOutage(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	service := newTrainingService(pool)

	const svc = "training_hard_outage_svc"
	pilotWorkflowService(t, ctx, pool, svc)
	instructor := insertInstructor(t, ctx, pool, "training-outage-instr-"+uuid.NewString())
	target := pilotScenarioVersion(t, ctx, pool, svc, "ЮАО", instructor.ID)
	trainee := insertActiveTrainee(t, ctx, pool, "training-outage-trainee-"+uuid.NewString(), svc)
	workstation := insertWorkstation(t, ctx, pool, 251)
	actor := principal(instructor, uuid.Nil)

	interval := 5
	lesson, err := service.CreateLesson(ctx, actor, training.LessonCreate{
		ExerciseType: content.ExerciseTypeDDSProcessing, Title: "Hard outage", Mode: training.ModeTraining, Level: auth.LevelHard,
		Timing: &training.Timing{OpenS: 30, PrimaryS: 30, CompleteS: 180, SpawnEveryS: &interval},
	}, "req-outage-create")
	if err != nil {
		t.Fatalf("CreateLesson: %v", err)
	}
	if _, err := service.ReplaceAssignments(ctx, actor, lesson.ID, []training.AssignmentInput{
		{WorkstationNo: 251, UserID: trainee.ID, ScenarioVersionIDs: []uuid.UUID{target, target, target}},
	}, "req-outage-assign"); err != nil {
		t.Fatalf("ReplaceAssignments: %v", err)
	}
	if _, err := service.Start(ctx, actor, lesson.ID, "req-outage-start"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	traineeActor := principal(trainee, workstation)
	run, _, _, err := service.MyRun(ctx, traineeActor)
	if err != nil {
		t.Fatalf("MyRun: %v", err)
	}
	if run.NextOfferAt == nil {
		t.Fatal("run.NextOfferAt is nil right after starting a 3-card hard queue")
	}

	if _, err := pool.Exec(ctx, `UPDATE runs SET next_offer_at = clock_timestamp() - interval '1 hour' WHERE id=$1`, run.ID); err != nil {
		t.Fatalf("simulate outage: %v", err)
	}

	if err := service.Tick(ctx); err != nil {
		t.Fatalf("Tick (first, after outage): %v", err)
	}
	itemsAfterFirstTick, err := service.MyItems(ctx, traineeActor)
	if err != nil {
		t.Fatalf("MyItems after first tick: %v", err)
	}
	if len(itemsAfterFirstTick) != 2 {
		t.Fatalf("items after one tick following a long outage = %d, want exactly 2 (one new offer, not a batch catch-up)", len(itemsAfterFirstTick))
	}

	runAfterFirstTick, _, _, err := service.MyRun(ctx, traineeActor)
	if err != nil {
		t.Fatalf("MyRun after first tick: %v", err)
	}
	if runAfterFirstTick.NextOfferAt == nil {
		t.Fatal("run.NextOfferAt is nil after the second offer, want a third card still pending")
	}
	var databaseNow time.Time
	if err := pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&databaseNow); err != nil {
		t.Fatalf("read PostgreSQL time: %v", err)
	}
	if !runAfterFirstTick.NextOfferAt.After(databaseNow.Add(-time.Second)) {
		t.Fatalf("next_offer_at = %s is already due, want it computed from the real offer time (~now+%ds), not the stale pre-outage deadline", runAfterFirstTick.NextOfferAt, interval)
	}

	// A second tick immediately afterward, with next_offer_at still in
	// the future, must not add a third item.
	if err := service.Tick(ctx); err != nil {
		t.Fatalf("Tick (second, immediately after): %v", err)
	}
	itemsAfterSecondTick, err := service.MyItems(ctx, traineeActor)
	if err != nil {
		t.Fatalf("MyItems after second tick: %v", err)
	}
	if len(itemsAfterSecondTick) != 2 {
		t.Fatalf("items after an immediate second tick = %d, want still 2 (next_offer_at not yet due)", len(itemsAfterSecondTick))
	}
}

// TestTrainingRecoverDeliversOverdueEventAsLate is slice-4-plan.md's
// C11: RFC-001 §7.2's restart recovery leaves item_events' own anchor_at/
// due_at untouched ("дедлайны не сдвигаются") — Recover only marks the
// open item's interruption — and a scheduled event that has since become
// more than 5s overdue (missed during the simulated outage) is delivered
// with late=true on the next Tick, visible in the trainee's own
// DeliveredEvent view.
func TestTrainingRecoverDeliversOverdueEventAsLate(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	service := newTrainingService(pool)

	const svc = "training_late_event_svc"
	pilotWorkflowService(t, ctx, pool, svc)
	instructor := insertInstructor(t, ctx, pool, "training-late-instructor-"+uuid.NewString())
	versionID := pilotScenarioVersionWithEvents(t, ctx, pool, svc, "ЮАО", instructor.ID, "late-event-target", []content.Event{{
		Key: "notice1", AtS: 0, Since: "offered", Delivery: "notice", From: "team", Text: "Бригада на месте",
	}})
	trainee := insertActiveTrainee(t, ctx, pool, "training-late-trainee-"+uuid.NewString(), svc)
	workstation := insertWorkstation(t, ctx, pool, 241)
	actor := principal(instructor, uuid.Nil)

	lesson, err := service.CreateLesson(ctx, actor, training.LessonCreate{
		ExerciseType: content.ExerciseTypeDDSProcessing, Title: "Late event", Mode: training.ModeTraining, Level: auth.LevelEasy,
	}, "req-late-create")
	if err != nil {
		t.Fatalf("CreateLesson: %v", err)
	}
	if _, err := service.ReplaceAssignments(ctx, actor, lesson.ID, []training.AssignmentInput{
		{WorkstationNo: 241, UserID: trainee.ID, ScenarioVersionIDs: []uuid.UUID{versionID}},
	}, "req-late-assign"); err != nil {
		t.Fatalf("ReplaceAssignments: %v", err)
	}
	if _, err := service.Start(ctx, actor, lesson.ID, "req-late-start"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	traineeActor := principal(trainee, workstation)
	items, err := service.MyItems(ctx, traineeActor)
	if err != nil || len(items) != 1 {
		t.Fatalf("MyItems = %+v, %v", items, err)
	}
	item := items[0]

	var dueBefore time.Time
	if err := pool.QueryRow(ctx, `SELECT due_at FROM item_events WHERE item_id=$1 AND event_key='notice1'`, item.ID).Scan(&dueBefore); err != nil {
		t.Fatalf("read event before outage: %v", err)
	}

	// Simulate the event having missed its due time during a server
	// outage: push due_at an hour into the past. No clock is moved and
	// nothing recomputes the deadline — only the fact that this event is
	// now far more than 5s overdue.
	if _, err := pool.Exec(ctx, `UPDATE item_events SET due_at = due_at - interval '1 hour' WHERE item_id=$1 AND event_key='notice1'`, item.ID); err != nil {
		t.Fatalf("simulate outage: %v", err)
	}

	recoveryID := uuid.New()
	if _, err := service.Recover(ctx, recoveryID, "server_restart"); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	var dueAfterRecover time.Time
	if err := pool.QueryRow(ctx, `SELECT due_at FROM item_events WHERE item_id=$1 AND event_key='notice1'`, item.ID).Scan(&dueAfterRecover); err != nil {
		t.Fatalf("read event after recovery: %v", err)
	}
	if !dueAfterRecover.Equal(dueBefore.Add(-time.Hour)) {
		t.Fatalf("Recover changed the event's due_at: %s, want %s (deadlines must not shift)", dueAfterRecover, dueBefore.Add(-time.Hour))
	}

	if err := service.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	var state string
	var late bool
	var deliveredAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT state, late, delivered_at FROM item_events WHERE item_id=$1 AND event_key='notice1'`, item.ID).Scan(&state, &late, &deliveredAt); err != nil {
		t.Fatalf("read event after tick: %v", err)
	}
	if state != string(training.EventDelivered) || !late || deliveredAt == nil {
		t.Fatalf("event after tick: state=%s late=%v delivered_at=%v, want delivered with late=true", state, late, deliveredAt)
	}

	_, _, delivered, err := service.ItemForTrainee(ctx, traineeActor, item.ID)
	if err != nil {
		t.Fatalf("ItemForTrainee: %v", err)
	}
	found := false
	for _, e := range delivered {
		if e.Key == "notice1" {
			found = true
			if !e.Late {
				t.Fatalf("delivered event JSON is not marked late: %+v", e)
			}
		}
	}
	if !found {
		t.Fatal("delivered event notice1 missing from the trainee's own DeliveredEvent view")
	}
}

// TestTrainingControlReportRejectsStaleExpectedSeq is slice-4-plan.md's
// C11: control_report goes through the same idempotent command endpoint
// as everything else (RFC-001 §7.5), so a second attempt against an
// already-stale expected_seq must be rejected like any other command,
// not silently accepted or treated as a replay of the first.
func TestTrainingControlReportRejectsStaleExpectedSeq(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	service := newTrainingService(pool)

	_, trainee, workstationID, lesson := setupPilotLesson(t, ctx, pool, service, "ЮАО")
	if _, err := service.Start(ctx, principal(auth.User{ID: lesson.InstructorID, Role: auth.RoleInstructor}, uuid.Nil), lesson.ID, "req-start"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	actor := principal(trainee, workstationID)
	items, err := service.MyItems(ctx, actor)
	if err != nil || len(items) != 1 {
		t.Fatalf("MyItems = %+v, %v", items, err)
	}
	item := items[0]

	// open(0->1), accept(1->2), close(2->3): seq is 3 once closed.
	closePilotItem(t, ctx, service, actor, item.ID)

	first, err := service.Execute(ctx, actor, item.ID, training.Command{
		CommandID: uuid.New(), ExpectedSeq: 3, Type: training.CommandControlReport,
		Payload: []byte(`{"text":"первое сообщение"}`),
	}, "req-cr-1")
	if err != nil || first.Outcome != training.OutcomeApplied || first.Seq != 4 {
		t.Fatalf("first control_report = %+v, %v", first, err)
	}

	stale, err := service.Execute(ctx, actor, item.ID, training.Command{
		CommandID: uuid.New(), ExpectedSeq: 3, Type: training.CommandControlReport,
		Payload: []byte(`{"text":"второе сообщение, устаревший seq"}`),
	}, "req-cr-2")
	if err != nil {
		t.Fatalf("Execute(stale control_report): %v", err)
	}
	if stale.Outcome != training.OutcomeRejected || stale.ErrorCode == nil || *stale.ErrorCode != training.RejectStaleSeq {
		t.Fatalf("stale control_report receipt = %+v", stale)
	}

	var reportCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM control_reports WHERE item_id=$1`, item.ID).Scan(&reportCount); err != nil {
		t.Fatalf("count control_reports: %v", err)
	}
	if reportCount != 1 {
		t.Fatalf("control_reports rows after a rejected stale attempt = %d, want 1", reportCount)
	}

	second, err := service.Execute(ctx, actor, item.ID, training.Command{
		CommandID: uuid.New(), ExpectedSeq: 4, Type: training.CommandControlReport,
		Payload: []byte(`{"text":"второе сообщение, верный seq"}`),
	}, "req-cr-3")
	if err != nil || second.Outcome != training.OutcomeApplied || second.Seq != 5 {
		t.Fatalf("second control_report (correct seq) = %+v, %v", second, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM control_reports WHERE item_id=$1`, item.ID).Scan(&reportCount); err != nil {
		t.Fatalf("recount control_reports: %v", err)
	}
	if reportCount != 2 {
		t.Fatalf("control_reports rows after two accepted reports = %d, want 2", reportCount)
	}
}

// TestTrainingTickSurvivesSpawnPlanMismatchAndDeliversOtherEvents covers
// the fix for a real deadlock class: a hard-level run's own
// next_offer_at scheduler tick can legitimately consume the queue slot
// a spawn_card event was waiting to issue (ADR-018's checkSpawnQueuePlan
// only proves the *static* plan is reachable, not that runtime issuance
// order matches it). Before the fix, the resulting mismatch was
// returned as a plain error on every single Tick forever (the event
// stayed scheduled, so ScheduledItemEventsDue kept re-selecting it
// first by due_at) and Tick aborted its whole batch on the first error
// — starving every other running lesson's due events/offers, not just
// this one. This test reproduces the exact race and asserts: (1) Tick
// itself does not get stuck (an unrelated lesson's own due event is
// still delivered in the very same Tick call the mismatch happens in),
// and (2) the mismatched event reaches a terminal, non-scheduled state
// instead of being retried forever.
func TestTrainingTickSurvivesSpawnPlanMismatchAndDeliversOtherEvents(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	service := newTrainingService(pool)

	const svc = "training_race_svc"
	pilotWorkflowService(t, ctx, pool, svc)
	instructor := insertInstructor(t, ctx, pool, "training-race-instructor-"+uuid.NewString())
	target := pilotScenarioVersionWithEvents(t, ctx, pool, svc, "ЮАО", instructor.ID, "race-target", nil)
	third := pilotScenarioVersionWithEvents(t, ctx, pool, svc, "ЮАО", instructor.ID, "race-third", nil)
	spawn := pilotScenarioVersionWithEvents(t, ctx, pool, svc, "ЮАО", instructor.ID, "race-spawn", []content.Event{{
		Key: "e1", AtS: 0, Since: "accepted", Delivery: "spawn_card",
		Spawn: &content.EventSpawn{Kind: "scenario", ScenarioKey: "race-target", Version: 1},
	}})
	trainee := insertActiveTrainee(t, ctx, pool, "training-race-trainee-"+uuid.NewString(), svc)
	ws := insertWorkstation(t, ctx, pool, 351)
	actor := principal(instructor, uuid.Nil)
	interval := 5
	lesson, err := service.CreateLesson(ctx, actor, training.LessonCreate{
		ExerciseType: content.ExerciseTypeDDSProcessing, Title: "race", Mode: training.ModeTraining, Level: auth.LevelHard,
		Timing: &training.Timing{OpenS: 30, PrimaryS: 30, CompleteS: 180, SpawnEveryS: &interval},
	}, "req-race-create")
	if err != nil {
		t.Fatalf("CreateLesson: %v", err)
	}
	if _, err := service.ReplaceAssignments(ctx, actor, lesson.ID, []training.AssignmentInput{
		{WorkstationNo: 351, UserID: trainee.ID, ScenarioVersionIDs: []uuid.UUID{spawn, target, third}},
	}, "req-race-assign"); err != nil {
		t.Fatalf("ReplaceAssignments: %v", err)
	}
	if _, err := service.Start(ctx, actor, lesson.ID, "req-race-start"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	traineeActor := principal(trainee, ws)
	run, _, _, err := service.MyRun(ctx, traineeActor)
	if err != nil {
		t.Fatalf("MyRun: %v", err)
	}

	// The hard timer fires before the trainee ever accepts the spawn
	// card: it consumes queue[1] (target) itself, one tick ahead of the
	// spawn_card event that also wants it.
	if _, err := pool.Exec(ctx, `UPDATE runs SET next_offer_at=clock_timestamp()-interval '1 second' WHERE id=$1`, run.ID); err != nil {
		t.Fatalf("make hard run due: %v", err)
	}
	if err := service.Tick(ctx); err != nil {
		t.Fatalf("Tick hard offer: %v", err)
	}
	items, err := service.MyItems(ctx, traineeActor)
	if err != nil || len(items) != 2 {
		t.Fatalf("items after hard offer = %+v, %v, want 2", items, err)
	}

	// Now open+accept the spawn card: e1 schedules for "accepted",
	// at_s=0, so it is immediately due — and immediately racing a queue
	// slot the hard offer already took.
	for seq, cmd := range []training.Command{
		{CommandID: uuid.New(), Type: training.CommandOpen, Payload: []byte(`{}`)},
		{CommandID: uuid.New(), Type: training.CommandSetStatus, Payload: []byte(`{"status":"accepted"}`)},
	} {
		cmd.ExpectedSeq = int64(seq)
		receipt, err := service.Execute(ctx, traineeActor, items[0].ID, cmd, "req-race-cmd")
		if err != nil || receipt.Outcome != training.OutcomeApplied {
			t.Fatalf("command %s = %+v, %v", cmd.Type, receipt, err)
		}
	}

	// A second, independent lesson with its own due event (an ordinary
	// "offered" notice, due immediately) — due strictly after e1
	// (created later), so ScheduledItemEventsDue's due_at ordering tries
	// e1 first, exactly reproducing the old starvation.
	other := pilotScenarioVersionWithEvents(t, ctx, pool, svc, "ЮАО", instructor.ID, "race-other", []content.Event{{
		Key: "n1", AtS: 0, Since: "offered", Delivery: "notice", From: "team", Text: "независимое занятие",
	}})
	otherTrainee := insertActiveTrainee(t, ctx, pool, "training-race-other-"+uuid.NewString(), svc)
	insertWorkstation(t, ctx, pool, 352)
	otherLesson, err := service.CreateLesson(ctx, actor, training.LessonCreate{
		ExerciseType: content.ExerciseTypeDDSProcessing, Title: "other", Mode: training.ModeTraining, Level: auth.LevelEasy,
	}, "req-race-other-create")
	if err != nil {
		t.Fatalf("CreateLesson (other): %v", err)
	}
	if _, err := service.ReplaceAssignments(ctx, actor, otherLesson.ID, []training.AssignmentInput{
		{WorkstationNo: 352, UserID: otherTrainee.ID, ScenarioVersionIDs: []uuid.UUID{other}},
	}, "req-race-other-assign"); err != nil {
		t.Fatalf("ReplaceAssignments (other): %v", err)
	}
	if _, err := service.Start(ctx, actor, otherLesson.ID, "req-race-other-start"); err != nil {
		t.Fatalf("Start (other): %v", err)
	}

	// One Tick call must both resolve the mismatched e1 (not leave it
	// scheduled forever) and still deliver the unrelated lesson's n1 —
	// the exact case the old abort-on-first-error Tick could never
	// reach.
	if err := service.Tick(ctx); err != nil {
		t.Fatalf("Tick after race: %v", err)
	}

	var e1State string
	var e1SkipReason *string
	if err := pool.QueryRow(ctx, `SELECT state, skip_reason FROM item_events WHERE event_key='e1'`).Scan(&e1State, &e1SkipReason); err != nil {
		t.Fatalf("read e1: %v", err)
	}
	if e1State != string(training.EventSkipped) {
		t.Fatalf("e1 state = %q, want %q (terminal, not left scheduled)", e1State, training.EventSkipped)
	}
	if e1SkipReason == nil || *e1SkipReason != training.SkipReasonSpawnPlanMismatch {
		t.Fatalf("e1 skip_reason = %v, want %q", e1SkipReason, training.SkipReasonSpawnPlanMismatch)
	}

	var n1State string
	if err := pool.QueryRow(ctx, `SELECT state FROM item_events WHERE event_key='n1'`).Scan(&n1State); err != nil {
		t.Fatalf("read n1: %v", err)
	}
	if n1State != string(training.EventDelivered) {
		t.Fatalf("n1 (unrelated lesson's due event) state = %q, want %q — a poisoned event must not starve other lessons' scheduling", n1State, training.EventDelivered)
	}

	// A further Tick must not keep retrying the now-terminal e1 (no
	// panic/error from re-selecting a skipped event — ScheduledItemEventsDue
	// only selects state='scheduled').
	if err := service.Tick(ctx); err != nil {
		t.Fatalf("Tick after terminal skip: %v", err)
	}
}

// TestTrainingRejectsLegacyDuplicateSpawnCard makes the temporary contract
// boundary explicit for JSON that was persisted before duplicate spawning was
// suspended. The old body remains decodable, but it cannot form a new lesson
// assignment that would otherwise be impossible to make finite.
func TestTrainingRejectsLegacyDuplicateSpawnCard(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	service := newTrainingService(pool)

	const svc = "training_legacy_duplicate_svc"
	pilotWorkflowService(t, ctx, pool, svc)
	instructor := insertInstructor(t, ctx, pool, "legacy-dup-i-"+uuid.NewString())
	target := pilotScenarioVersionWithEvents(t, ctx, pool, svc, "ЮАО", instructor.ID, "legacy-duplicate-target", nil)
	source := pilotScenarioVersionWithEvents(t, ctx, pool, svc, "ЮАО", instructor.ID, "legacy-duplicate-source", []content.Event{{
		Key: "e1", AtS: 0, Since: "offered", Delivery: "spawn_card",
		Spawn: &content.EventSpawn{Kind: "duplicate", Variation: "legacy variation"},
	}})
	trainee := insertActiveTrainee(t, ctx, pool, "legacy-dup-t-"+uuid.NewString(), svc)
	insertWorkstation(t, ctx, pool, 354)
	actor := principal(instructor, uuid.Nil)
	lesson, err := service.CreateLesson(ctx, actor, training.LessonCreate{
		ExerciseType: content.ExerciseTypeDDSProcessing, Title: "legacy duplicate", Mode: training.ModeTraining, Level: auth.LevelEasy,
	}, "req-legacy-duplicate-create")
	if err != nil {
		t.Fatalf("CreateLesson: %v", err)
	}

	_, err = service.ReplaceAssignments(ctx, actor, lesson.ID, []training.AssignmentInput{{
		WorkstationNo: 354, UserID: trainee.ID, ScenarioVersionIDs: []uuid.UUID{source, target},
	}}, "req-legacy-duplicate-assign")
	if err == nil || !strings.Contains(err.Error(), "spawn_card kind is unsupported") {
		t.Fatalf("ReplaceAssignments legacy duplicate = %v, want unsupported spawn_card error", err)
	}
}

// TestTrainingSpawnCardSetsSpawnedFrom ensures an event-created card keeps
// its immutable ancestry in both the item and close-time evidence.
func TestTrainingSpawnCardSetsSpawnedFrom(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	service := newTrainingService(pool)

	const svc = "training_dup_spawn_svc"
	pilotWorkflowService(t, ctx, pool, svc)
	instructor := insertInstructor(t, ctx, pool, "training-dup-instructor-"+uuid.NewString())
	target := pilotScenarioVersionWithEvents(t, ctx, pool, svc, "ЮАО", instructor.ID, "dup-target", nil)
	source := pilotScenarioVersionWithEvents(t, ctx, pool, svc, "ЮАО", instructor.ID, "dup-source", []content.Event{{
		Key: "e1", AtS: 0, Since: "offered", Delivery: "spawn_card",
		Spawn: &content.EventSpawn{Kind: "scenario", ScenarioKey: "dup-target", Version: 1},
	}})
	trainee := insertActiveTrainee(t, ctx, pool, "training-dup-trainee-"+uuid.NewString(), svc)
	ws := insertWorkstation(t, ctx, pool, 353)
	actor := principal(instructor, uuid.Nil)

	lesson, err := service.CreateLesson(ctx, actor, training.LessonCreate{
		ExerciseType: content.ExerciseTypeDDSProcessing, Title: "dup", Mode: training.ModeTraining, Level: auth.LevelEasy,
	}, "req-dup-create")
	if err != nil {
		t.Fatalf("CreateLesson: %v", err)
	}
	if _, err := service.ReplaceAssignments(ctx, actor, lesson.ID, []training.AssignmentInput{
		{WorkstationNo: 353, UserID: trainee.ID, ScenarioVersionIDs: []uuid.UUID{source, target}},
	}, "req-dup-assign"); err != nil {
		t.Fatalf("ReplaceAssignments: %v", err)
	}
	if _, err := service.Start(ctx, actor, lesson.ID, "req-dup-start"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	traineeActor := principal(trainee, ws)
	items, err := service.MyItems(ctx, traineeActor)
	if err != nil || len(items) != 1 {
		t.Fatalf("MyItems before tick = %+v, %v", items, err)
	}
	sourceItemID := items[0].ID

	if err := service.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	items, err = service.MyItems(ctx, traineeActor)
	if err != nil || len(items) != 2 {
		t.Fatalf("MyItems after spawn = %+v, %v, want 2", items, err)
	}
	var spawned training.Item
	for _, it := range items {
		if it.ID != sourceItemID {
			spawned = it
		}
	}
	if spawned.SpawnedFrom == nil || *spawned.SpawnedFrom != sourceItemID {
		t.Fatalf("spawned item's SpawnedFrom = %v, want %s", spawned.SpawnedFrom, sourceItemID)
	}
	// Close the spawned item and confirm evidence carries the same
	// ancestry link (evidence.schema.json's spawned_from_item_id).
	closePilotItem(t, ctx, service, traineeActor, spawned.ID)
	var spawnedFromInEvidence string
	if err := pool.QueryRow(ctx, `SELECT body->>'spawned_from_item_id' FROM evidence WHERE item_id=$1`, spawned.ID).Scan(&spawnedFromInEvidence); err != nil {
		t.Fatalf("read evidence: %v", err)
	}
	if spawnedFromInEvidence != sourceItemID.String() {
		t.Fatalf("evidence spawned_from_item_id = %q, want %q", spawnedFromInEvidence, sourceItemID.String())
	}
}

// TestTrainingStopClearsHardRunNextOfferAt covers Stop's own barrier
// freezing a hard-level run's next_offer_at immediately, rather than
// leaving it for the durable lesson.close task (which may be delayed,
// or never run at all if the worker is unavailable) to eventually
// finish the run and thereby stop RunsDueForOffer from selecting it.
// Before this fix, a stopped hard lesson's run stayed selectable by
// RunsDueForOffer — not corrupting anything (tickHardRun's own
// lesson.State check made it a no-op every time), but re-locking the
// lesson and re-discovering "not running" on every single 500ms tick,
// forever, until a worker eventually closed the run.
func TestTrainingStopClearsHardRunNextOfferAt(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	service := newTrainingService(pool)

	const svc = "training_stop_hard_svc"
	pilotWorkflowService(t, ctx, pool, svc)
	instructor := insertInstructor(t, ctx, pool, "training-stphd-instr-"+uuid.NewString())
	versionA := pilotScenarioVersionWithEvents(t, ctx, pool, svc, "ЮАО", instructor.ID, "stophard-a", nil)
	versionB := pilotScenarioVersionWithEvents(t, ctx, pool, svc, "ЮАО", instructor.ID, "stophard-b", nil)
	trainee := insertActiveTrainee(t, ctx, pool, "training-stphd-trn-"+uuid.NewString(), svc)
	ws := insertWorkstation(t, ctx, pool, 361)
	actor := principal(instructor, uuid.Nil)
	interval := 5

	lesson, err := service.CreateLesson(ctx, actor, training.LessonCreate{
		ExerciseType: content.ExerciseTypeDDSProcessing, Title: "stop-hard", Mode: training.ModeTraining, Level: auth.LevelHard,
		Timing: &training.Timing{OpenS: 30, PrimaryS: 30, CompleteS: 180, SpawnEveryS: &interval},
	}, "req-stophard-create")
	if err != nil {
		t.Fatalf("CreateLesson: %v", err)
	}
	if _, err := service.ReplaceAssignments(ctx, actor, lesson.ID, []training.AssignmentInput{
		{WorkstationNo: 361, UserID: trainee.ID, ScenarioVersionIDs: []uuid.UUID{versionA, versionB}},
	}, "req-stophard-assign"); err != nil {
		t.Fatalf("ReplaceAssignments: %v", err)
	}
	if _, err := service.Start(ctx, actor, lesson.ID, "req-stophard-start"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	traineeActor := principal(trainee, ws)
	run, _, _, err := service.MyRun(ctx, traineeActor)
	if err != nil {
		t.Fatalf("MyRun: %v", err)
	}
	var nextOfferBefore *time.Time
	if err := pool.QueryRow(ctx, `SELECT next_offer_at FROM runs WHERE id=$1`, run.ID).Scan(&nextOfferBefore); err != nil {
		t.Fatalf("read next_offer_at before stop: %v", err)
	}
	if nextOfferBefore == nil {
		t.Fatal("hard run's next_offer_at must be set right after start")
	}

	if _, err := service.Stop(ctx, actor, lesson.ID, nil, "req-stophard-stop"); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	var nextOfferAfter *time.Time
	if err := pool.QueryRow(ctx, `SELECT next_offer_at FROM runs WHERE id=$1`, run.ID).Scan(&nextOfferAfter); err != nil {
		t.Fatalf("read next_offer_at after stop: %v", err)
	}
	if nextOfferAfter != nil {
		t.Fatalf("next_offer_at after Stop = %v, want NULL (Stop's own barrier must clear it)", nextOfferAfter)
	}

	// Make the (now-cleared) offer moment due in the past regardless —
	// RunsDueForOffer must not even select this run any more, so a
	// further Tick offers nothing.
	if _, err := pool.Exec(ctx, `UPDATE runs SET next_offer_at = NULL WHERE id=$1`, run.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.Tick(ctx); err != nil {
		t.Fatalf("Tick after stop: %v", err)
	}
	items, err := service.MyItems(ctx, traineeActor)
	if err != nil {
		t.Fatalf("MyItems: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items after stop+tick = %d, want 1 (no further hard offer once stopped)", len(items))
	}
}

// TestTrainingRecoverAcrossMultipleRunningLessons covers Recover's own
// per-lesson barrier (each running lesson locked and recovered in its
// own transaction) still recovering every running lesson's own open
// items correctly and idempotently, not just a single one — the
// property a single cross-lesson bulk statement had for free and the
// per-lesson loop must not regress.
func TestTrainingRecoverAcrossMultipleRunningLessons(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	service := newTrainingService(pool)

	const svc = "training_recover_multi_svc"
	pilotWorkflowService(t, ctx, pool, svc)
	instructor := insertInstructor(t, ctx, pool, "training-recmulti-instr-"+uuid.NewString())
	actor := principal(instructor, uuid.Nil)

	setupOne := func(wsNo int, sourceKey string) uuid.UUID {
		version := pilotScenarioVersionWithEvents(t, ctx, pool, svc, "ЮАО", instructor.ID, sourceKey, nil)
		trainee := insertActiveTrainee(t, ctx, pool, "training-recmulti-trn-"+uuid.NewString(), svc)
		insertWorkstation(t, ctx, pool, wsNo)
		lesson, err := service.CreateLesson(ctx, actor, training.LessonCreate{
			ExerciseType: content.ExerciseTypeDDSProcessing, Title: sourceKey, Mode: training.ModeTraining, Level: auth.LevelEasy,
		}, "req-recovermulti-create-"+sourceKey)
		if err != nil {
			t.Fatalf("CreateLesson(%s): %v", sourceKey, err)
		}
		if _, err := service.ReplaceAssignments(ctx, actor, lesson.ID, []training.AssignmentInput{
			{WorkstationNo: wsNo, UserID: trainee.ID, ScenarioVersionIDs: []uuid.UUID{version}},
		}, "req-recovermulti-assign-"+sourceKey); err != nil {
			t.Fatalf("ReplaceAssignments(%s): %v", sourceKey, err)
		}
		if _, err := service.Start(ctx, actor, lesson.ID, "req-recovermulti-start-"+sourceKey); err != nil {
			t.Fatalf("Start(%s): %v", sourceKey, err)
		}
		traineeActor := principal(trainee, uuid.Nil)
		items, err := service.MyItems(ctx, traineeActor)
		if err != nil || len(items) != 1 {
			t.Fatalf("MyItems(%s) = %+v, %v", sourceKey, items, err)
		}
		return items[0].ID
	}

	itemA := setupOne(371, "recover-multi-a")
	itemB := setupOne(372, "recover-multi-b")

	interruptionCount := func(itemID uuid.UUID) int {
		t.Helper()
		var count int
		if err := pool.QueryRow(ctx, `SELECT jsonb_array_length(interruptions) FROM items WHERE id=$1`, itemID).Scan(&count); err != nil {
			t.Fatalf("count interruptions for %s: %v", itemID, err)
		}
		return count
	}

	firstRecoveryID := uuid.New()
	affected, err := service.Recover(ctx, firstRecoveryID, "server_restart")
	if err != nil {
		t.Fatalf("Recover (first): %v", err)
	}
	if len(affected) != 2 {
		t.Fatalf("Recover affected = %d items, want 2 (one per running lesson)", len(affected))
	}
	if interruptionCount(itemA) != 1 || interruptionCount(itemB) != 1 {
		t.Fatalf("interruption counts after first Recover = %d, %d, want 1, 1", interruptionCount(itemA), interruptionCount(itemB))
	}

	// A repeat call with the same recovery_id is a no-op for both
	// lessons, not just the first one the per-lesson loop visits.
	if affected, err := service.Recover(ctx, firstRecoveryID, "server_restart"); err != nil || len(affected) != 0 {
		t.Fatalf("Recover (repeat) = %+v, %v, want no items affected", affected, err)
	}
	if interruptionCount(itemA) != 1 || interruptionCount(itemB) != 1 {
		t.Fatalf("interruption counts after repeat Recover = %d, %d, want unchanged 1, 1", interruptionCount(itemA), interruptionCount(itemB))
	}

	// A genuinely new recovery_id adds a second marker to both.
	secondRecoveryID := uuid.New()
	if affected, err := service.Recover(ctx, secondRecoveryID, "server_restart"); err != nil || len(affected) != 2 {
		t.Fatalf("Recover (second, new id) = %+v, %v, want 2 items affected", affected, err)
	}
	if interruptionCount(itemA) != 2 || interruptionCount(itemB) != 2 {
		t.Fatalf("interruption counts after second Recover = %d, %d, want 2, 2", interruptionCount(itemA), interruptionCount(itemB))
	}
}

// TestTrainingConcurrentCommandVsStopRespectsBarrier covers RFC-001
// §7.5's barrier guarantee under real concurrency, not just sequential
// calls: "эффект либо до stop и в evidence, либо отклонён/отменён
// после". A trainee's open command and the instructor's Stop race for
// real, each in its own goroutine; PostgreSQL's own lock order
// (Execute's lockForCommand takes lessons FOR SHARE for a non-close
// command, Stop takes lessons FOR UPDATE) serializes them either way,
// so exactly one of two outcomes must hold, checked against the item's
// own frozen stop_cutoff_log_seq rather than assumed from goroutine
// scheduling:
//   - the open applied before the barrier: stop_cutoff_log_seq is set
//     and is at least the open's own log_seq (its effect is inside the
//     barrier, and will be included once a worker later closes the item);
//   - the open lost the race entirely (lesson already stopped by the
//     time its transaction reached the lock): it is rejected
//     lesson_stopped, and its own log_seq is strictly greater than
//     stop_cutoff_log_seq (the barrier came first; RFC-001 §7.5: "поздние
//     отклонённые попытки остаются... вне этого снимка").
//
// No interleaving may produce anything else — in particular, "applied"
// with a cutoff that excludes it, which would mean the barrier missed
// an effect it should have captured.
func TestTrainingConcurrentCommandVsStopRespectsBarrier(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	service := newTrainingService(pool)

	_, trainee, workstationID, lesson := setupPilotLesson(t, ctx, pool, service, "ЮАО")
	instructorActor := principal(auth.User{ID: lesson.InstructorID, Role: auth.RoleInstructor}, uuid.Nil)
	if _, err := service.Start(ctx, instructorActor, lesson.ID, "req-start"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	traineeActor := principal(trainee, workstationID)
	items, err := service.MyItems(ctx, traineeActor)
	if err != nil || len(items) != 1 {
		t.Fatalf("MyItems: %+v, %v", items, err)
	}
	itemID := items[0].ID

	var wg sync.WaitGroup
	var receipt training.Receipt
	var execErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		receipt, execErr = service.Execute(ctx, traineeActor, itemID, training.Command{
			CommandID: uuid.New(), ExpectedSeq: 0, Type: training.CommandOpen, Payload: []byte(`{}`),
		}, "req-race-open")
	}()
	go func() {
		defer wg.Done()
		if _, err := service.Stop(ctx, instructorActor, lesson.ID, nil, "req-race-stop"); err != nil {
			t.Errorf("Stop: %v", err)
		}
	}()
	wg.Wait()
	if execErr != nil {
		t.Fatalf("Execute returned an error instead of a receipt: %v", execErr)
	}

	var logSeq int64
	var cutoff *int64
	if err := pool.QueryRow(ctx, `SELECT log_seq, stop_cutoff_log_seq FROM items WHERE id=$1`, itemID).Scan(&logSeq, &cutoff); err != nil {
		t.Fatalf("read item after race: %v", err)
	}
	if cutoff == nil {
		t.Fatalf("stop_cutoff_log_seq is NULL after Stop committed — barrier did not freeze this item")
	}

	switch receipt.Outcome {
	case training.OutcomeApplied:
		if *cutoff < receipt.LogSeq {
			t.Fatalf("open applied (log_seq=%d) but stop_cutoff_log_seq=%d excludes it — barrier missed an effect it committed after", receipt.LogSeq, *cutoff)
		}
	case training.OutcomeRejected:
		if receipt.ErrorCode == nil || *receipt.ErrorCode != training.RejectLessonStopped {
			t.Fatalf("rejected receipt = %+v, want lesson_stopped", receipt)
		}
		if *cutoff >= receipt.LogSeq {
			t.Fatalf("open rejected as lesson_stopped (log_seq=%d) but stop_cutoff_log_seq=%d does not precede it — a late rejected attempt must sit strictly outside the barrier's own snapshot", receipt.LogSeq, *cutoff)
		}
	default:
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
}

// TestTrainingConcurrentCloseOffersNextCardExactlyOnce covers "закрытие/
// следующая карточка" under real concurrency: two close attempts on the
// same item, different command_ids, racing at the same expected_seq —
// exactly one may apply (mirrors TestTrainingConcurrentCommandsOneApplied's
// own open-vs-open race, here specifically for close, since a close's
// own accepted branch also offers the run's next queued item and that
// is the invariant actually at risk — runs.queue_cursor and
// UNIQUE(run_id, ordinal), RFC-001 §7.2). Asserts the queue advanced by
// exactly one card, not zero and not two.
func TestTrainingConcurrentCloseOffersNextCardExactlyOnce(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	service := newTrainingService(pool)

	const svc = "training_close_race_svc"
	pilotWorkflowService(t, ctx, pool, svc)
	instructor := insertInstructor(t, ctx, pool, "training-closerace-instr-"+uuid.NewString())
	versionA := pilotScenarioVersionWithEvents(t, ctx, pool, svc, "ЮАО", instructor.ID, "closerace-a", nil)
	versionB := pilotScenarioVersionWithEvents(t, ctx, pool, svc, "ЮАО", instructor.ID, "closerace-b", nil)
	trainee := insertActiveTrainee(t, ctx, pool, "training-closerace-trn-"+uuid.NewString(), svc)
	ws := insertWorkstation(t, ctx, pool, 381)
	actor := principal(instructor, uuid.Nil)

	lesson, err := service.CreateLesson(ctx, actor, training.LessonCreate{
		ExerciseType: content.ExerciseTypeDDSProcessing, Title: "close-race", Mode: training.ModeTraining, Level: auth.LevelEasy,
	}, "req-closerace-create")
	if err != nil {
		t.Fatalf("CreateLesson: %v", err)
	}
	if _, err := service.ReplaceAssignments(ctx, actor, lesson.ID, []training.AssignmentInput{
		{WorkstationNo: 381, UserID: trainee.ID, ScenarioVersionIDs: []uuid.UUID{versionA, versionB}},
	}, "req-closerace-assign"); err != nil {
		t.Fatalf("ReplaceAssignments: %v", err)
	}
	if _, err := service.Start(ctx, actor, lesson.ID, "req-closerace-start"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	traineeActor := principal(trainee, ws)
	items, err := service.MyItems(ctx, traineeActor)
	if err != nil || len(items) != 1 {
		t.Fatalf("MyItems before close: %+v, %v", items, err)
	}
	itemID := items[0].ID

	for seq, cmd := range []training.Command{
		{CommandID: uuid.New(), Type: training.CommandOpen, Payload: []byte(`{}`)},
		{CommandID: uuid.New(), Type: training.CommandSetStatus, Payload: []byte(`{"status":"accepted"}`)},
	} {
		cmd.ExpectedSeq = int64(seq)
		r, err := service.Execute(ctx, traineeActor, itemID, cmd, "req-closerace-prep")
		if err != nil || r.Outcome != training.OutcomeApplied {
			t.Fatalf("prep command %s = %+v, %v", cmd.Type, r, err)
		}
	}

	results := make(chan training.Receipt, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := service.Execute(ctx, traineeActor, itemID, training.Command{
				CommandID: uuid.New(), ExpectedSeq: 2, Type: training.CommandClose, Payload: []byte(`{}`),
			}, "req-closerace")
			if err != nil {
				errs <- err
				return
			}
			results <- r
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		t.Fatalf("Execute returned an error instead of a rejected Receipt: %v", e)
	}

	// Unlike two racing "open" attempts (item.State never becomes Closed
	// in between), the losing close here is decided against an item
	// Execute's own switch already sees as item.State==Closed — that
	// case is checked ahead of expected_seq (internal/training/
	// service.go's Execute: item_closed before stale_seq) — so the
	// loser is rejected item_closed, not stale_seq.
	var applied, lostRace int
	for r := range results {
		switch {
		case r.Outcome == training.OutcomeApplied:
			applied++
		case r.Outcome == training.OutcomeRejected && r.ErrorCode != nil &&
			(*r.ErrorCode == training.RejectStaleSeq || *r.ErrorCode == training.RejectItemClosed):
			lostRace++
		default:
			t.Fatalf("unexpected receipt: %+v", r)
		}
	}
	if applied != 1 || lostRace != 1 {
		t.Fatalf("applied=%d lostRace=%d, want 1 and 1 (exactly one close must win)", applied, lostRace)
	}

	finalItems, err := service.MyItems(ctx, traineeActor)
	if err != nil {
		t.Fatalf("MyItems after race: %v", err)
	}
	if len(finalItems) != 2 {
		t.Fatalf("items after the close race = %d, want 2 (original closed + exactly one next card offered, not zero and not duplicated)", len(finalItems))
	}
	closedCount, offeredCount := 0, 0
	for _, it := range finalItems {
		switch it.State {
		case training.ItemClosed:
			closedCount++
		case training.ItemOffered:
			offeredCount++
		}
	}
	if closedCount != 1 || offeredCount != 1 {
		t.Fatalf("closed=%d offered=%d among %+v, want 1 and 1", closedCount, offeredCount, finalItems)
	}
}

// TestTrainingThreeParticipantsIndependentQueues covers
// slice-planning.md §4's DoD line literally ("преподаватель задаёт
// разные очереди минимум трём участникам") — the existing group test
// (TestTrainingGroupAssignmentsAdvanceIndependentQueues) only ever
// covers two. Three trainees, three distinct queues, closing one
// participant's item must not affect either of the other two's own
// queue position or open item.
func TestTrainingThreeParticipantsIndependentQueues(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	service := newTrainingService(pool)

	const svc = "training_three_participants_svc"
	pilotWorkflowService(t, ctx, pool, svc)
	instructor := insertInstructor(t, ctx, pool, "training-three-instr-"+uuid.NewString())
	actor := principal(instructor, uuid.Nil)

	versionA := pilotScenarioVersionWithEvents(t, ctx, pool, svc, "ЮАО", instructor.ID, "three-a", nil)
	versionB := pilotScenarioVersionWithEvents(t, ctx, pool, svc, "ЮАО", instructor.ID, "three-b", nil)
	versionC := pilotScenarioVersionWithEvents(t, ctx, pool, svc, "ЮАО", instructor.ID, "three-c", nil)

	traineeX := insertActiveTrainee(t, ctx, pool, "training-three-x-"+uuid.NewString(), svc)
	traineeY := insertActiveTrainee(t, ctx, pool, "training-three-y-"+uuid.NewString(), svc)
	traineeZ := insertActiveTrainee(t, ctx, pool, "training-three-z-"+uuid.NewString(), svc)
	wsX, wsY, wsZ := insertWorkstation(t, ctx, pool, 391), insertWorkstation(t, ctx, pool, 392), insertWorkstation(t, ctx, pool, 393)

	lesson, err := service.CreateLesson(ctx, actor, training.LessonCreate{
		ExerciseType: content.ExerciseTypeDDSProcessing, Title: "three", Mode: training.ModeTraining, Level: auth.LevelEasy,
	}, "req-three-create")
	if err != nil {
		t.Fatalf("CreateLesson: %v", err)
	}
	if _, err := service.ReplaceAssignments(ctx, actor, lesson.ID, []training.AssignmentInput{
		{WorkstationNo: 391, UserID: traineeX.ID, ScenarioVersionIDs: []uuid.UUID{versionA, versionB, versionC}},
		{WorkstationNo: 392, UserID: traineeY.ID, ScenarioVersionIDs: []uuid.UUID{versionB, versionC}},
		{WorkstationNo: 393, UserID: traineeZ.ID, ScenarioVersionIDs: []uuid.UUID{versionC}},
	}, "req-three-assign"); err != nil {
		t.Fatalf("ReplaceAssignments: %v", err)
	}
	if _, err := service.Start(ctx, actor, lesson.ID, "req-three-start"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	actorX, actorY, actorZ := principal(traineeX, wsX), principal(traineeY, wsY), principal(traineeZ, wsZ)
	itemsX, err := service.MyItems(ctx, actorX)
	if err != nil || len(itemsX) != 1 || itemsX[0].ScenarioVersionID != versionA {
		t.Fatalf("X's items = %+v, %v, want [A]", itemsX, err)
	}
	itemsY, err := service.MyItems(ctx, actorY)
	if err != nil || len(itemsY) != 1 || itemsY[0].ScenarioVersionID != versionB {
		t.Fatalf("Y's items = %+v, %v, want [B]", itemsY, err)
	}
	itemsZ, err := service.MyItems(ctx, actorZ)
	if err != nil || len(itemsZ) != 1 || itemsZ[0].ScenarioVersionID != versionC {
		t.Fatalf("Z's items = %+v, %v, want [C]", itemsZ, err)
	}

	// Close X's first item — Y and Z, on entirely different queues and
	// runs, must be completely unaffected.
	closePilotItem(t, ctx, service, actorX, itemsX[0].ID)

	itemsX, err = service.MyItems(ctx, actorX)
	if err != nil || len(itemsX) != 2 || itemsX[1].ScenarioVersionID != versionB || itemsX[1].State != training.ItemOffered {
		t.Fatalf("X's items after closing #1 = %+v, %v, want [closed A, offered B]", itemsX, err)
	}
	itemsYAfter, err := service.MyItems(ctx, actorY)
	if err != nil || len(itemsYAfter) != 1 || itemsYAfter[0].ID != itemsY[0].ID || itemsYAfter[0].State != training.ItemOffered {
		t.Fatalf("Y's items after X's close = %+v, %v, want unchanged [offered B]", itemsYAfter, err)
	}
	itemsZAfter, err := service.MyItems(ctx, actorZ)
	if err != nil || len(itemsZAfter) != 1 || itemsZAfter[0].ID != itemsZ[0].ID || itemsZAfter[0].State != training.ItemOffered {
		t.Fatalf("Z's items after X's close = %+v, %v, want unchanged [offered C]", itemsZAfter, err)
	}

	monitor, err := service.Monitor(ctx, actor, lesson.ID)
	if err != nil {
		t.Fatalf("Monitor: %v", err)
	}
	if len(monitor.Rows) != 3 {
		t.Fatalf("monitor rows = %d, want 3", len(monitor.Rows))
	}
	doneByUser := map[uuid.UUID]int{}
	for _, row := range monitor.Rows {
		doneByUser[row.User.ID] = row.Done
	}
	if doneByUser[traineeX.ID] != 1 || doneByUser[traineeY.ID] != 0 || doneByUser[traineeZ.ID] != 0 {
		t.Fatalf("monitor done counts = %+v, want X=1 Y=0 Z=0", doneByUser)
	}
}
