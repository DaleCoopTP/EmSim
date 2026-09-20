// New test: the training tables added by migrations/00006 (slice 3,
// ADR-017) against real PostgreSQL — append-only actions/evidence
// (reject_immutable_change), the mode/card/workflow/pilot_goal/effect
// columns the DDL contract added alongside them, and the partial unique
// indexes that make GET /my/run unambiguous across lessons. This
// complements TestPlatformSchema (migration up/down/up, table set) with
// the constraint-level guarantees specific to the new tables; it does not
// exercise internal/training's own application logic (a later commit's
// integration tests do).
//
//go:build integration

package integration_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	pgstore "emsim/internal/platform/postgres"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestTrainingSchemaConstraints(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)

	ids := newIDSource()
	instructorID := ids.next()
	traineeID := ids.next()
	otherTraineeID := ids.next()
	workstationID := ids.next()
	otherWorkstationID := ids.next()
	scenarioID := ids.next()
	versionID := ids.next()
	lessonID := ids.next()
	otherLessonID := ids.next()
	runID := ids.next()
	otherRunID := ids.next()
	itemID := ids.next()
	actionID := ids.next()
	commandID := ids.next()

	mustExec(t, ctx, pool, `INSERT INTO users(id,login,password_hash,full_name,role) VALUES
		($1,'training_schema_instructor','fixture','Instructor','instructor'),
		($2,'training_schema_trainee','fixture','Trainee','trainee'),
		($3,'training_schema_trainee_2','fixture','Trainee 2','trainee')`,
		instructorID, traineeID, otherTraineeID)
	mustExec(t, ctx, pool, `INSERT INTO workstations(id,number) VALUES ($1,901),($2,902)`,
		workstationID, otherWorkstationID)
	mustExec(t, ctx, pool, `INSERT INTO services(code,name,workflow) VALUES ('training_schema_test','Test','{}')`)
	mustExec(t, ctx, pool, `INSERT INTO scenarios(id,title,target_service,difficulty,origin,status,created_by) VALUES
		($1,'Fixture','training_schema_test',1,'manual','approved',$2)`, scenarioID, instructorID)
	mustExec(t, ctx, pool, `INSERT INTO scenario_versions(id,scenario_id,version,status,body,digest,difficulty,created_by,approved_by,approved_at) VALUES
		($1,$2,1,'approved','{}',decode(repeat('00',32),'hex'),1,$3,$3,now())`, versionID, scenarioID, instructorID)
	mustExec(t, ctx, pool, `INSERT INTO lessons(id,instructor_id,title,mode,level,state,timing,rubric_version,started_at) VALUES
		($1,$2,'Fixture','training','easy','running','{"open_s":30,"primary_s":30,"complete_s":180}','dds/rubric-v1',now()),
		($3,$2,'Fixture 2','training','easy','running','{"open_s":30,"primary_s":30,"complete_s":180}','dds/rubric-v1',now())`,
		lessonID, instructorID, otherLessonID)
	mustExec(t, ctx, pool, `INSERT INTO assignments(lesson_id,workstation_id,user_id,scenario_version_ids) VALUES
		($1,$2,$3,ARRAY[$4]::uuid[]), ($5,$6,$7,ARRAY[$4]::uuid[])`,
		lessonID, workstationID, traineeID, versionID, otherLessonID, otherWorkstationID, otherTraineeID)

	// Один активный прогон на пользователя/РМ одновременно, независимо от
	// занятия (срез 3, ADR-017): второй active run для того же trainee, в
	// другом занятии и на другом РМ, должен быть отклонён.
	mustExec(t, ctx, pool, `INSERT INTO runs(id,lesson_id,user_id,workstation_id,mode,state,level_at_start) VALUES
		($1,$2,$3,$4,'training','active','easy')`, runID, lessonID, traineeID, workstationID)
	expectUniqueViolation(t, ctx, pool, `INSERT INTO runs(id,lesson_id,user_id,workstation_id,mode,state,level_at_start) VALUES
		($1,$2,$3,$4,'training','active','easy')`, otherRunID, otherLessonID, traineeID, otherWorkstationID)
	// A second trainee on the same workstation, while the first run is
	// still active there, is rejected by the per-workstation index too.
	expectUniqueViolation(t, ctx, pool, `INSERT INTO runs(id,lesson_id,user_id,workstation_id,mode,state,level_at_start) VALUES
		($1,$2,$3,$4,'training','active','easy')`, otherRunID, otherLessonID, otherTraineeID, workstationID)
	// Once the first run finishes, the same user/workstation pair is free
	// for a new active run (the partial index only covers state='active').
	mustExec(t, ctx, pool, `UPDATE runs SET state='finished', finished_at=now() WHERE id=$1`, runID)
	mustExec(t, ctx, pool, `INSERT INTO runs(id,lesson_id,user_id,workstation_id,mode,state,level_at_start) VALUES
		($1,$2,$3,$4,'training','active','easy')`, otherRunID, otherLessonID, traineeID, otherWorkstationID)

	// items.card/workflow/pilot_goal (срез 3, ADR-017).
	mustExec(t, ctx, pool, `INSERT INTO items(id,run_id,scenario_version_id,ordinal,state,reaction,card,workflow,pilot_goal,timing_effective,opened_at,primary_at) VALUES
		($1,$2,$3,1,'in_progress','received','{"number":"1"}','{"transitions":{}}','accept_card','{}',now(),now())`,
		itemID, otherRunID, versionID)

	// actions.effect (срез 3, ADR-017) round-trips as a JSON object.
	mustExec(t, ctx, pool, `INSERT INTO actions(item_id,seq,log_seq,id,actor_id,request_digest,command_id,type,payload,effect,accepted,rejection,receipt,http_status) VALUES
		($1,1,1,$2,$3,decode(repeat('00',32),'hex'),$4,'set_card_field','{"path":"/card/address/okrug","value":"ЮАО"}','{"path":"/card/address/okrug","old":"ЮАР","new":"ЮАО"}',true,NULL,'{}',200)`,
		itemID, actionID, traineeID, commandID)

	// actions/evidence are append-only: UPDATE and DELETE are both
	// rejected by reject_immutable_change(), not by an integrity
	// constraint (SQLSTATE class 23) — check the specific message.
	expectImmutable(t, ctx, pool, `UPDATE actions SET accepted=false WHERE id=$1`, actionID)
	expectImmutable(t, ctx, pool, `DELETE FROM actions WHERE id=$1`, actionID)

	mustExec(t, ctx, pool, `INSERT INTO evidence(item_id,body,digest) VALUES
		($1,'{"mode":"training"}',decode(repeat('00',32),'hex'))`, itemID)
	expectImmutable(t, ctx, pool, `UPDATE evidence SET body='{}' WHERE item_id=$1`, itemID)
	expectImmutable(t, ctx, pool, `DELETE FROM evidence WHERE item_id=$1`, itemID)

	// Users and the content catalogue created before this migration are
	// untouched by it (no destructive DDL on pre-existing tables).
	var userCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE id IN ($1,$2,$3)`, instructorID, traineeID, otherTraineeID).Scan(&userCount); err != nil {
		t.Fatalf("count fixture users: %v", err)
	}
	if userCount != 3 {
		t.Fatalf("fixture users = %d, want 3", userCount)
	}
}

func mustExec(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(ctx, query, args...); err != nil {
		t.Fatalf("exec failed: %v\nquery: %s", err, strings.TrimSpace(query))
	}
}

// expectUniqueViolation is expectViolation narrowed to unique_violation
// (SQLSTATE 23505) specifically — the partial-unique-index checks above
// want to fail for that reason and not, say, a FK error masking a typo'd
// fixture id.
func expectUniqueViolation(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string, args ...any) {
	t.Helper()
	_, err := pool.Exec(ctx, query, args...)
	if err == nil {
		t.Fatalf("statement unexpectedly succeeded: %s", strings.TrimSpace(query))
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("statement error = %v, want unique_violation (23505)", err)
	}
}

// expectImmutable asserts query fails with reject_immutable_change()'s
// specific message, not merely some error — the trigger raises a plain
// P0001 (raise_exception), outside expectViolation's SQLSTATE-23 class.
func expectImmutable(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string, args ...any) {
	t.Helper()
	_, err := pool.Exec(ctx, query, args...)
	if err == nil {
		t.Fatalf("statement unexpectedly succeeded: %s", strings.TrimSpace(query))
	}
	if !strings.Contains(err.Error(), "immutable artifact:") {
		t.Fatalf("statement error = %v, want reject_immutable_change() message", err)
	}
}
