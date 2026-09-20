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
		map[content.ExerciseType]training.Exercise{content.ExerciseTypeDDSProcessing: dds.Exercise},
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
	const svc = "training_pilot_svc"
	pilotWorkflowService(t, ctx, pool, svc)

	instructor = insertInstructor(t, ctx, pool, "training-instructor-"+uuid.NewString())
	trainee = insertActiveTrainee(t, ctx, pool, "training-trainee-"+uuid.NewString(), svc)
	workstationID = insertWorkstation(t, ctx, pool, 1)
	versionID := pilotScenarioVersion(t, ctx, pool, svc, okrug, instructor.ID)

	instructorPrincipal := principal(instructor, uuid.Nil)
	created, err := service.CreateLesson(ctx, instructorPrincipal, training.LessonCreate{
		ExerciseType: content.ExerciseTypeDDSProcessing, Title: "Fixture lesson",
		Mode: training.ModeTraining, Level: auth.LevelEasy,
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
