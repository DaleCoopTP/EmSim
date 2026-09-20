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
	"sync"
	"testing"
	"time"

	"emsim/internal/auth"
	authpg "emsim/internal/auth/postgres"
	"emsim/internal/content"
	contentpg "emsim/internal/content/postgres"
	pgstore "emsim/internal/platform/postgres"
	"emsim/internal/training"
	"emsim/internal/training/dds"
	trainingpg "emsim/internal/training/postgres"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func newTrainingService(pool *pgxpool.Pool) *training.Service {
	authStore := authpg.NewStore(pool)
	contentStore := contentpg.NewStore(pool)
	return training.NewService(
		trainingpg.NewStore(pool),
		authStore, authStore,
		contentStore, contentStore,
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
		Difficulty:   1,
		ExerciseType: content.ExerciseTypeDDSProcessing,
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal fixture body: %v", err)
	}
	digest := sha256.Sum256(bodyJSON)
	if _, err := pool.Exec(ctx, `
		INSERT INTO scenarios (id, title, target_service, difficulty, origin, status, created_by)
		VALUES ($1, 'Fixture', $2, 1, 'manual', 'approved', $3)
	`, scenarioID, targetService, createdBy); err != nil {
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
	run, _, err := service.MyRun(ctx, actor)
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
	if err := pool.QueryRow(ctx, `SELECT state FROM runs WHERE id = $1`, run.ID).Scan(&runState); err != nil {
		t.Fatalf("read run state: %v", err)
	}
	if runState != "finished" {
		t.Fatalf("run state = %s, want finished (queue exhausted)", runState)
	}

	// A closed run frees the user/workstation for a new active run
	// (slice-planning.md §4 DoD).
	if _, _, err := service.MyRun(ctx, actor); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("MyRun after close = %v, want ErrNotFound (no active run)", err)
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

func (failingEvidenceExercise) Evidence(training.Item, []training.Action, int64, time.Time) (training.Evidence, error) {
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
		trainingpg.NewStore(pool), authStore, authStore, contentStore, contentStore,
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
