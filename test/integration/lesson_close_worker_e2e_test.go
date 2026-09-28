// New test: slice-4-plan.md's C8/C11 gap a post-review pass found —
// every existing lesson.close assertion (TestTrainingStopBarrierAndDurableClose
// and friends in training_service_test.go) drives the worker-side half
// by calling Service.CloseStoppedLesson directly, never through the
// real platform/tasks queue and a real emsim worker process claiming
// and running cmd/emsim's own lessonCloseHandler. This file is the one
// piece of the DoD ("stop корректно прерывает незавершённые карточки")
// that closes that gap: it builds the real binary, starts a real
// "emsim worker" process (worker_process_test.go's own harness), and
// drives Stop -> the durable task -> a finished lesson end to end
// through the actual queue, the same way TestWorkerProcessCrashRecovery
// already does for the generic system.noop kind.
//
//go:build integration

package integration_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"emsim/internal/auth"
	pgstore "emsim/internal/platform/postgres"

	"github.com/google/uuid"
)

func TestWorkerProcessDrivesLessonCloseThroughRealQueue(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	service := newTrainingService(pool)

	binary := filepath.Join(t.TempDir(), "emsim")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/emsim")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build emsim: %v\n%s", err, output)
	}

	_, trainee, workstationID, lesson := setupPilotLesson(t, ctx, pool, service, "ЮАО")
	instructorActor := principal(auth.User{ID: lesson.InstructorID, Role: auth.RoleInstructor}, uuid.Nil)
	if _, err := service.Start(ctx, instructorActor, lesson.ID, "req-start"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	traineeActor := principal(trainee, workstationID)
	items, err := service.MyItems(ctx, traineeActor)
	if err != nil || len(items) != 1 {
		t.Fatalf("MyItems = %+v, %v", items, err)
	}
	item := items[0]

	worker := startWorkerProcess(t, binary, databaseURL, "worker", "lesson-close-worker")

	if _, err := service.Stop(ctx, instructorActor, lesson.ID, nil, "req-stop"); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	waitFor := func(label, query string, args ...any) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
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

	// The real worker process must claim training.KindLessonClose
	// (dedup_key lesson.close:<lesson_id>:<epoch>) and run
	// cmd/emsim's own lessonCloseHandler to completion — not a direct
	// in-test call to Service.CloseStoppedLesson.
	waitFor("lesson.close task done via the real worker process",
		`SELECT EXISTS(SELECT 1 FROM tasks WHERE kind='lesson.close' AND scope_id=$1 AND status='done' AND terminal_worker='lesson-close-worker')`,
		lesson.ID)
	waitFor("lesson finished by the real worker process",
		`SELECT state='finished' FROM lessons WHERE id=$1`, lesson.ID)
	waitFor("item interrupted with evidence sealed by the real worker process",
		`SELECT i.state='interrupted' AND i.close_reason='interrupted' AND EXISTS(SELECT 1 FROM evidence WHERE item_id=i.id) FROM items i WHERE i.id=$1`,
		item.ID)
	// Slice 6/9: lesson.close's interrupted evidence is itself a normal
	// training close for assessment purposes.  The same real worker must
	// therefore run the coordinator and create the auto review, while all
	// timing criteria become not_applicable under the interruption marker.
	// dds/rubric-v2 (ДДС-3) has no llm criteria left, so an item
	// interrupted before it was even opened has every criterion resolve
	// not_applicable (D_PRIMARY included: no decision was ever due) and
	// nothing left unavailable to force needs_review — unlike dds/
	// rubric-v1, where G_GRAMMAR was unconditionally unavailable under
	// JUDGE=off and needs_review was unconditional. The auto assessment
	// is legitimately ready with an all-not_applicable criteria set.
	waitFor("interrupted item auto-assessed by the real worker process",
		`SELECT EXISTS (
			SELECT FROM assessments a
			WHERE a.item_id=$1 AND a.kind='auto' AND a.revision=1 AND a.status='ready'
			  AND (a.criteria @> '[{"id":"T_OPEN","status":"not_applicable"}]'::jsonb)
			  AND (a.criteria @> '[{"id":"D_PRIMARY","status":"not_applicable"}]'::jsonb)
		)`, item.ID)

	worker.stop(t, false)

	// A durable-close retry safety net: stopping the (already-finished)
	// lesson again must stay idempotent even once the real worker has
	// already run — no new epoch, no new task row.
	before, err := service.Stop(ctx, instructorActor, lesson.ID, nil, "req-stop-again")
	if err != nil {
		t.Fatalf("Stop (repeat, already finished): %v", err)
	}
	if before.State != "finished" {
		t.Fatalf("repeat Stop on a finished lesson = state %q, want finished", before.State)
	}
	var taskCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE kind='lesson.close' AND scope_id=$1`, lesson.ID).Scan(&taskCount); err != nil {
		t.Fatal(err)
	}
	if taskCount != 1 {
		t.Fatalf("lesson.close task rows for lesson %s = %d, want exactly 1 (no duplicate from the repeat Stop)", lesson.ID, taskCount)
	}
}
