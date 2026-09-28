// ДДС-6/ADR-035: the random queue fill and the category summary against
// real PostgreSQL — which approved scenarios are drawable, per-row
// independent queues, and that a proposal is accepted by ReplaceAssignments.
//
//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"

	"emsim/internal/auth"
	"emsim/internal/content"
	contentpg "emsim/internal/content/postgres"
	pgstore "emsim/internal/platform/postgres"
	"emsim/internal/training"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// drawScenarioVersion inserts an approved DDS scenario of the given service,
// incident type code, difficulty and status (scenarios.status), optionally
// with a spawn_card event, and returns its version id.
func drawScenarioVersion(t *testing.T, ctx context.Context, pool *pgxpool.Pool, targetService, typeCode string, difficulty int, scenarioStatus string, spawn bool, createdBy uuid.UUID) uuid.UUID {
	t.Helper()
	scenarioID, versionID := uuid.New(), uuid.New()
	body := content.Body{
		Schema:        "emsim/scenario/v1",
		TargetService: targetService,
		Card: content.Card{
			Number: "881412", RegisteredAtOffsetS: -60,
			Applicant:        content.Applicant{Name: "Иванов", Phone: "+70000000000", Status: "witness"},
			Address:          content.Address{Country: "Россия", City: "Москва", Okrug: "ЮАО", District: "Чертаново Южное", Street: "Чертановская улица", House: "58"},
			Incident:         content.Incident{TypeCode: typeCode, TypeName: "Тип " + typeCode},
			NotificationList: []content.NotificationEntry{{Service: targetService, Status: content.ReactionAdded, Mine: true}},
		},
		Reference:    content.Reference{PrimaryDecision: content.PrimaryDecision{Status: content.ReactionAccepted}, PilotGoal: "accept_card"},
		Difficulty:   difficulty,
		ExerciseType: content.ExerciseTypeDDSProcessing,
	}
	if spawn {
		body.Events = []content.Event{{Key: "e1", AtS: 5, Since: "offered", Delivery: "spawn_card", Text: "Новая карточка"}}
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(bodyJSON)
	if _, err := pool.Exec(ctx, `
		INSERT INTO scenarios (id, title, target_service, difficulty, origin, status, created_by)
		VALUES ($1, 'Fixture', $2, $3, 'manual', $4, $5)`, scenarioID, targetService, difficulty, scenarioStatus, createdBy); err != nil {
		t.Fatalf("insert scenario: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO scenario_versions (id, scenario_id, version, status, body, digest, difficulty, created_by, approved_by, approved_at)
		VALUES ($1, $2, 1, 'approved', $3, $4, $5, $6, $6, now())`, versionID, scenarioID, bodyJSON, digest[:], difficulty, createdBy); err != nil {
		t.Fatalf("insert scenario version: %v", err)
	}
	return versionID
}

func TestDDSDrawAssignmentsAndCategories(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	service := newTrainingService(pool)

	const svc, otherSvc = "draw_svc", "draw_other_svc"
	pilotWorkflowService(t, ctx, pool, svc)
	pilotWorkflowService(t, ctx, pool, otherSvc)
	instructor := insertInstructor(t, ctx, pool, "draw-instructor-"+uuid.NewString())
	traineeA := insertActiveTrainee(t, ctx, pool, "draw-a-"+uuid.NewString(), svc)
	traineeB := insertActiveTrainee(t, ctx, pool, "draw-b-"+uuid.NewString(), svc)
	insertWorkstation(t, ctx, pool, 231)
	insertWorkstation(t, ctx, pool, 232)
	actor := principal(instructor, uuid.Nil)

	// Easy (1-3): three drawable in section 14, one in 22, plus three that
	// must never be drawn — a spawn_card scenario, an archived one and one
	// of another service.
	easy14 := map[uuid.UUID]bool{
		drawScenarioVersion(t, ctx, pool, svc, "14080106", 1, "approved", false, instructor.ID): true,
		drawScenarioVersion(t, ctx, pool, svc, "14020300", 2, "approved", false, instructor.ID): true,
		drawScenarioVersion(t, ctx, pool, svc, "14080106", 3, "approved", false, instructor.ID): true,
	}
	easy22 := drawScenarioVersion(t, ctx, pool, svc, "22020000", 2, "approved", false, instructor.ID)
	spawn := drawScenarioVersion(t, ctx, pool, svc, "14080106", 1, "approved", true, instructor.ID)
	archived := drawScenarioVersion(t, ctx, pool, svc, "14080106", 1, "archived", false, instructor.ID)
	foreign := drawScenarioVersion(t, ctx, pool, otherSvc, "14080106", 1, "approved", false, instructor.ID)
	medium14 := drawScenarioVersion(t, ctx, pool, svc, "14080106", 5, "approved", false, instructor.ID) // outside easy

	lesson, err := service.CreateLesson(ctx, actor, training.LessonCreate{
		ExerciseType: content.ExerciseTypeDDSProcessing, Title: "Draw", Mode: training.ModeTraining, Level: auth.LevelEasy,
	}, "req-draw-create")
	if err != nil {
		t.Fatalf("CreateLesson: %v", err)
	}
	rows := []training.DrawRow{{WorkstationNo: 231, UserID: traineeA.ID}, {WorkstationNo: 232, UserID: traineeB.ID}}

	drawn, err := service.DrawAssignments(ctx, actor, lesson.ID, training.DrawInput{Categories: []string{"14"}, Count: 3, Rows: rows})
	if err != nil {
		t.Fatalf("DrawAssignments: %v", err)
	}
	if len(drawn) != 2 {
		t.Fatalf("drawn %d rows, want 2", len(drawn))
	}
	for _, a := range drawn {
		if len(a.ScenarioVersionIDs) != 3 {
			t.Fatalf("queue length %d, want 3", len(a.ScenarioVersionIDs))
		}
		seen := map[uuid.UUID]bool{}
		for _, id := range a.ScenarioVersionIDs {
			if !easy14[id] || seen[id] {
				t.Fatalf("queue %v holds %s: not an easy section-14 scenario of the trainee's service, or a repeat (excluded: spawn %s, archived %s, foreign %s, medium %s, section 22 %s)",
					a.ScenarioVersionIDs, id, spawn, archived, foreign, medium14, easy22)
			}
			seen[id] = true
		}
	}

	// A proposal is nothing until saved — and saving it goes through the
	// ordinary ReplaceAssignments checks.
	var stored int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM assignments WHERE lesson_id=$1`, lesson.ID).Scan(&stored); err != nil || stored != 0 {
		t.Fatalf("draw must not save anything: %d rows, %v", stored, err)
	}
	inputs := make([]training.AssignmentInput, len(drawn))
	for i, a := range drawn {
		inputs[i] = training.AssignmentInput{WorkstationNo: a.WorkstationNo, UserID: a.UserID, ScenarioVersionIDs: a.ScenarioVersionIDs}
	}
	if _, err := service.ReplaceAssignments(ctx, actor, lesson.ID, inputs, "req-draw-save"); err != nil {
		t.Fatalf("a drawn plan must be accepted by ReplaceAssignments: %v", err)
	}

	// Sections 14+22: 4 suitable; asking for 5 is not enough, and the error
	// says how many there are.
	_, err = service.DrawAssignments(ctx, actor, lesson.ID, training.DrawInput{Categories: []string{"14", "22"}, Count: 5, Rows: rows[:1]})
	var notEnough *training.NotEnoughScenariosError
	if !errors.As(err, &notEnough) || notEnough.Available != 4 || notEnough.Requested != 5 || notEnough.WorkstationNo != 231 {
		t.Fatalf("err=%v, want NotEnoughScenariosError{available 4, requested 5, ws 231}", err)
	}
	// A section with no scenarios at all.
	if _, err = service.DrawAssignments(ctx, actor, lesson.ID, training.DrawInput{Categories: []string{"99"}, Count: 1, Rows: rows[:1]}); !errors.As(err, &notEnough) || notEnough.Available != 0 {
		t.Fatalf("empty section: err=%v", err)
	}

	// Ownership, state and input checks.
	other := insertInstructor(t, ctx, pool, "draw-other-"+uuid.NewString())
	if _, err := service.DrawAssignments(ctx, principal(other, uuid.Nil), lesson.ID, training.DrawInput{Categories: []string{"14"}, Count: 1, Rows: rows}); !errors.Is(err, training.ErrNotFound) {
		t.Fatalf("foreign lesson: %v, want ErrNotFound", err)
	}
	var ve *training.ValidationError
	for name, in := range map[string]training.DrawInput{
		"no categories":   {Count: 1, Rows: rows},
		"bad category":    {Categories: []string{"1"}, Count: 1, Rows: rows},
		"zero count":      {Categories: []string{"14"}, Count: 0, Rows: rows},
		"no rows":         {Categories: []string{"14"}, Count: 1},
		"dup workstation": {Categories: []string{"14"}, Count: 1, Rows: []training.DrawRow{rows[0], {WorkstationNo: 231, UserID: traineeB.ID}}},
	} {
		if _, err := service.DrawAssignments(ctx, actor, lesson.ID, in); !errors.As(err, &ve) {
			t.Fatalf("%s: err=%v, want ValidationError", name, err)
		}
	}
	if _, err := service.Start(ctx, actor, lesson.ID, "req-draw-start"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := service.DrawAssignments(ctx, actor, lesson.ID, training.DrawInput{Categories: []string{"14"}, Count: 1, Rows: rows}); !errors.Is(err, training.ErrConflict) {
		t.Fatalf("started lesson: %v, want ErrConflict", err)
	}

	// The category summary: drawable scenarios only, all levels, one service.
	contentSvc := content.NewService(contentpg.NewStore(pool), nil)
	cats, err := contentSvc.ScenarioCategories(ctx, svc)
	if err != nil {
		t.Fatalf("ScenarioCategories: %v", err)
	}
	if len(cats) != 2 || cats[0].Code != "14" || cats[1].Code != "22" {
		t.Fatalf("categories=%+v, want sections 14 and 22", cats)
	}
	if got := cats[0].CountByLevel; got.Easy != 3 || got.Medium != 1 || got.Hard != 0 {
		t.Fatalf("section 14 counts=%+v, want easy 3, medium 1 (spawn/archived/foreign excluded)", got)
	}
	if got := cats[1].CountByLevel; got.Easy != 1 {
		t.Fatalf("section 22 counts=%+v", got)
	}
}
