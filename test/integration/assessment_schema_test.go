// New test: the assessment tables added by migrations/00009 (slice 6,
// ADR-006/013/016/019) against real PostgreSQL — the assessments_expert_shape/
// assessments_score_shape CHECK constraints, the one-auto-per-item and
// item_final_assessment-backed guard_assessment_revision trigger (stale
// revision, auto forbidden after expert, input belongs to another item),
// and immutability of assessment_inputs/assessments. It does not exercise
// internal/assessment's own application logic (a later commit's
// integration tests do) — every fixture here is a bare SQL row, the same
// convention training_schema_test.go uses for migrations/00006.
//
//go:build integration

package integration_test

import (
	"context"
	"strings"
	"testing"

	pgstore "emsim/internal/platform/postgres"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAssessmentSchemaConstraints(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool := openTestPool(t, ctx, databaseURL)

	ids := newIDSource()
	instructorID := ids.next()
	traineeID := ids.next()
	workstationID := ids.next()
	scenarioID := ids.next()
	versionID := ids.next()
	lessonID := ids.next()
	runID := ids.next()
	itemID := ids.next()
	otherItemID := ids.next()

	mustExec(t, ctx, pool, `INSERT INTO users(id,login,password_hash,full_name,role) VALUES
		($1,'assessment_schema_instructor','fixture','Instructor','instructor'),
		($2,'assessment_schema_trainee','fixture','Trainee','trainee')`,
		instructorID, traineeID)
	mustExec(t, ctx, pool, `INSERT INTO workstations(id,number) VALUES ($1,911)`, workstationID)
	mustExec(t, ctx, pool, `INSERT INTO services(code,name,workflow) VALUES ('assessment_schema_test','Test','{}')`)
	mustExec(t, ctx, pool, `INSERT INTO scenarios(id,title,target_service,difficulty,origin,status,created_by) VALUES
		($1,'Fixture','assessment_schema_test',1,'manual','approved',$2)`, scenarioID, instructorID)
	mustExec(t, ctx, pool, `INSERT INTO scenario_versions(id,scenario_id,version,status,body,digest,difficulty,created_by,approved_by,approved_at) VALUES
		($1,$2,1,'approved','{}',decode(repeat('00',32),'hex'),1,$3,$3,now())`, versionID, scenarioID, instructorID)
	mustExec(t, ctx, pool, `INSERT INTO lessons(id,instructor_id,title,mode,level,state,timing,rubric_version,started_at) VALUES
		($1,$2,'Fixture','training','easy','running','{"open_s":30,"primary_s":30,"complete_s":180}','dds/rubric-v1',now())`,
		lessonID, instructorID)
	mustExec(t, ctx, pool, `INSERT INTO assignments(lesson_id,workstation_id,user_id,scenario_version_ids) VALUES
		($1,$2,$3,ARRAY[$4]::uuid[])`, lessonID, workstationID, traineeID, versionID)
	mustExec(t, ctx, pool, `INSERT INTO runs(id,lesson_id,user_id,workstation_id,mode,state,level_at_start) VALUES
		($1,$2,$3,$4,'training','active','easy')`, runID, lessonID, traineeID, workstationID)
	mustExec(t, ctx, pool, `INSERT INTO items(id,run_id,scenario_version_id,ordinal,state,reaction,card,workflow,timing_effective,closed_at,close_reason) VALUES
		($1,$2,$3,1,'closed','completed','{"number":"1"}','{"transitions":{}}','{}',now(),'completed'),
		($4,$2,$3,2,'closed','completed','{"number":"2"}','{"transitions":{}}','{}',now(),'completed')`,
		itemID, runID, versionID, otherItemID)
	mustExec(t, ctx, pool, `INSERT INTO evidence(item_id,body,digest) VALUES
		($1,'{"mode":"training"}',decode(repeat('00',32),'hex')),
		($2,'{"mode":"training"}',decode(repeat('00',32),'hex'))`, itemID, otherItemID)

	inputID := ids.next()
	otherInputID := ids.next()
	mustExec(t, ctx, pool, `INSERT INTO assessment_inputs(id,item_id,body,digest) VALUES
		($1,$2,'{}',decode(repeat('00',32),'hex')),
		($3,$4,'{}',decode(repeat('00',32),'hex'))`, inputID, itemID, otherInputID, otherItemID)
	// One assessment_inputs row per item — a second seal for the same
	// item is rejected (ADR-006: "Sealed input создаётся один раз").
	expectUniqueViolation(t, ctx, pool, `INSERT INTO assessment_inputs(id,item_id,body,digest) VALUES
		($1,$2,'{}',decode(repeat('00',32),'hex'))`, ids.next(), itemID)
	expectImmutable(t, ctx, pool, `UPDATE assessment_inputs SET body='{"x":1}' WHERE id=$1`, inputID)
	expectImmutable(t, ctx, pool, `DELETE FROM assessment_inputs WHERE id=$1`, inputID)

	autoID := ids.next()
	sourceTaskID := ids.next()
	mustExec(t, ctx, pool, `INSERT INTO assessments(id,item_id,revision,kind,status,evidence_digest,input_id,source_task_id,rubric_version,rubric_effective,criteria) VALUES
		($1,$2,1,'auto','needs_review',decode(repeat('00',32),'hex'),$3,$4,'dds/rubric-v1','{}','[]')`,
		autoID, itemID, inputID, sourceTaskID)

	// assessments_expert_shape: an auto row's own shape is pinned —
	// revision must be 1, created_by must be NULL, input_id/source_task_id
	// must both be set, base_revision must be NULL.
	expectViolation(t, ctx, pool, `INSERT INTO assessments(id,item_id,revision,kind,status,evidence_digest,input_id,source_task_id,rubric_version,rubric_effective,criteria) VALUES
		($1,$2,1,'auto','needs_review',decode(repeat('00',32),'hex'),NULL,$3,'dds/rubric-v1','{}','[]')`,
		ids.next(), otherItemID, ids.next())
	expectViolation(t, ctx, pool, `INSERT INTO assessments(id,item_id,revision,kind,status,evidence_digest,input_id,source_task_id,rubric_version,rubric_effective,criteria,created_by) VALUES
		($1,$2,1,'auto','needs_review',decode(repeat('00',32),'hex'),$3,$4,'dds/rubric-v1','{}','[]',$5)`,
		ids.next(), otherItemID, otherInputID, ids.next(), instructorID)
	// assessments_score_shape: needs_review with a numeric score, or ready
	// without one, are both rejected (ADR-013/016 A3: no NULL-averaging).
	expectViolation(t, ctx, pool, `INSERT INTO assessments(id,item_id,revision,kind,status,evidence_digest,input_id,source_task_id,rubric_version,rubric_effective,criteria,score,passed) VALUES
		($1,$2,1,'auto','needs_review',decode(repeat('00',32),'hex'),$3,$4,'dds/rubric-v1','{}','[]',50,true)`,
		ids.next(), otherItemID, otherInputID, ids.next())

	// guard_assessment_revision: a second auto for the same item is
	// blocked by assessments_one_auto_idx before the trigger even runs.
	expectUniqueViolation(t, ctx, pool, `INSERT INTO assessments(id,item_id,revision,kind,status,evidence_digest,input_id,source_task_id,rubric_version,rubric_effective,criteria) VALUES
		($1,$2,1,'auto','needs_review',decode(repeat('00',32),'hex'),$3,$4,'dds/rubric-v1','{}','[]')`,
		ids.next(), itemID, inputID, ids.next())
	// An input_id belonging to another item is rejected by the trigger
	// itself, not by any FK (assessment_inputs.id has no per-item scoping).
	expectRaised(t, ctx, pool, "assessment input belongs to another item", `INSERT INTO assessments(id,item_id,revision,kind,status,evidence_digest,input_id,source_task_id,rubric_version,rubric_effective,criteria,created_by,reason,base_revision) VALUES
		($1,$2,2,'expert','ready',decode(repeat('00',32),'hex'),$3,NULL,'dds/rubric-v1','{}','[]',$4,'fixed the record',1)`,
		ids.next(), itemID, otherInputID, instructorID)
	// A stale base_revision (not the item's current final revision) is
	// rejected by the trigger (RFC-001 §7.4: "Под блокировкой item
	// проверяется base_revision; stale → 409").
	expectRaised(t, ctx, pool, "stale assessment revision", `INSERT INTO assessments(id,item_id,revision,kind,status,evidence_digest,input_id,source_task_id,rubric_version,rubric_effective,criteria,created_by,reason,base_revision) VALUES
		($1,$2,3,'expert','ready',decode(repeat('00',32),'hex'),NULL,NULL,'dds/rubric-v1','{}','[]',$3,'wrong base',2)`,
		ids.next(), itemID, instructorID)

	expertID := ids.next()
	mustExec(t, ctx, pool, `INSERT INTO assessments(id,item_id,revision,kind,status,evidence_digest,input_id,source_task_id,rubric_version,rubric_effective,criteria,score,passed,created_by,reason,base_revision) VALUES
		($1,$2,2,'expert','ready',decode(repeat('00',32),'hex'),NULL,NULL,'dds/rubric-v1','{}','[]',85,true,$3,'reviewed manually',1)`,
		expertID, itemID, instructorID)

	// Once an expert revision exists, a late auto for the same item is
	// forbidden outright — even for a *different* item's own first auto
	// this must still succeed, so the check is scoped per item_id.
	expectRaised(t, ctx, pool, "auto assessment forbidden after expert", `INSERT INTO assessments(id,item_id,revision,kind,status,evidence_digest,input_id,source_task_id,rubric_version,rubric_effective,criteria) VALUES
		($1,$2,1,'auto','needs_review',decode(repeat('00',32),'hex'),NULL,$3,'dds/rubric-v1','{}','[]')`,
		ids.next(), itemID, ids.next())
	mustExec(t, ctx, pool, `INSERT INTO assessments(id,item_id,revision,kind,status,evidence_digest,input_id,source_task_id,rubric_version,rubric_effective,criteria) VALUES
		($1,$2,1,'auto','needs_review',decode(repeat('00',32),'hex'),$3,$4,'dds/rubric-v1','{}','[]')`,
		ids.next(), otherItemID, otherInputID, ids.next())

	expectImmutable(t, ctx, pool, `UPDATE assessments SET status='ready' WHERE id=$1`, autoID)
	expectImmutable(t, ctx, pool, `DELETE FROM assessments WHERE id=$1`, autoID)

	// item_final_assessment: the latest expert outranks the sole auto.
	var finalRevision int
	var finalKind string
	if err := pool.QueryRow(ctx, `SELECT revision, kind FROM item_final_assessment WHERE item_id=$1`, itemID).Scan(&finalRevision, &finalKind); err != nil {
		t.Fatalf("read item_final_assessment: %v", err)
	}
	if finalRevision != 2 || finalKind != "expert" {
		t.Fatalf("item_final_assessment = (%d, %s), want (2, expert)", finalRevision, finalKind)
	}

	// training_examples requires a real assessment to compare against
	// (ADR-013's "AI said / instructor said" pairs); assessments are
	// themselves immutable/append-only (assessments_immutable above), so
	// the FK's ON DELETE CASCADE never actually fires in practice — only
	// the FK itself is worth asserting here.
	exampleID := ids.next()
	mustExec(t, ctx, pool, `INSERT INTO training_examples(id,assessment_id,criterion_id,auto_result,expert_result) VALUES
		($1,$2,'D_PRIMARY','{"status":"not_met"}','{"status":"met"}')`, exampleID, expertID)
	expectViolation(t, ctx, pool, `INSERT INTO training_examples(id,assessment_id,criterion_id,auto_result,expert_result) VALUES
		($1,$2,'D_PRIMARY','{"status":"not_met"}','{"status":"met"}')`, ids.next(), ids.next())

	// trainee_assessment_state: one row per (user, exercise_type), version
	// only ever moves forward under the application's own transaction —
	// here just the PK/CHECK shape.
	mustExec(t, ctx, pool, `INSERT INTO trainee_assessment_state(user_id,exercise_type,version) VALUES ($1,'dds_processing',0)`, traineeID)
	expectUniqueViolation(t, ctx, pool, `INSERT INTO trainee_assessment_state(user_id,exercise_type,version) VALUES ($1,'dds_processing',1)`, traineeID)
	expectViolation(t, ctx, pool, `UPDATE trainee_assessment_state SET version=-1 WHERE user_id=$1`, traineeID)
}

// expectRaised asserts query fails with a specific RAISE EXCEPTION message
// from guard_assessment_revision() — a plain P0001, outside
// expectViolation's SQLSTATE-23 class (see expectImmutable's own doc
// comment for the same reasoning about reject_immutable_change()).
func expectRaised(t *testing.T, ctx context.Context, pool *pgxpool.Pool, wantSubstring, query string, args ...any) {
	t.Helper()
	_, err := pool.Exec(ctx, query, args...)
	if err == nil {
		t.Fatalf("statement unexpectedly succeeded: %s", strings.TrimSpace(query))
	}
	if !strings.Contains(err.Error(), wantSubstring) {
		t.Fatalf("statement error = %v, want to contain %q", err, wantSubstring)
	}
}
