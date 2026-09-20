// Ported from orchestration-core@34290d74656bc594f8968ae17871dc997559b497
// cmd/worker/main_test.go; adapted: roles are worker|maintenance|all
// instead of dialogue|judge|maintenance|all, and there is no
// e2eFinalizationPolicy (finalization was not ported).
package main

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"emsim/internal/platform/tasks"
	"emsim/internal/training"
)

func TestParseWorkerRoleRequiresKnownExplicitFlag(t *testing.T) {
	for _, args := range [][]string{nil, {"--role="}, {"--role=unknown"}, {"worker"}} {
		if _, err := parseWorkerRole(args); !errors.Is(err, tasks.ErrInvalidRole) {
			t.Fatalf("parseWorkerRole(%v) error = %v", args, err)
		}
	}
	for _, role := range []string{"worker", "maintenance", "all"} {
		if got, err := parseWorkerRole([]string{"--role=" + role}); err != nil || got != role {
			t.Fatalf("parseWorkerRole(%s) = %q, %v", role, got, err)
		}
	}
}

func TestServeWorkerReturnsOperationalErrorWhenAdminCannotListen(t *testing.T) {
	stopped := make(chan struct{})
	err := serveWorker(context.Background(), &http.Server{Addr: "invalid-address"}, func(ctx context.Context) error {
		<-ctx.Done()
		close(stopped)
		return nil
	})
	if err == nil {
		t.Fatal("admin listen failure was accepted")
	}
	<-stopped
}

func TestE2ERecoveryPolicyRemainsValidAndBounded(t *testing.T) {
	if err := e2eRecoveryPolicy().Validate(); err != nil {
		t.Fatalf("recovery policy: %v", err)
	}
}

// TestRegisterKindsIncludesLessonClose guards the one invariant C8 adds
// to registerKinds: training.KindLessonClose must be present with a
// Spec both processes agree on (mustTaskEnqueuer/api.go and
// composePools/worker_composition.go both call this same function to
// get there) — a missing or malformed entry would make Stop's own
// EnqueueTx fail with ErrUnknownKind at runtime instead of here.
func TestRegisterKindsIncludesLessonClose(t *testing.T) {
	registry, err := tasks.NewRegistry(tasks.DefaultPolicy())
	if err != nil {
		t.Fatalf("tasks.NewRegistry: %v", err)
	}
	if err := registerKinds(registry); err != nil {
		t.Fatalf("registerKinds: %v", err)
	}
	spec, ok := registry.Lookup(training.KindLessonClose)
	if !ok {
		t.Fatal("registerKinds did not register training.KindLessonClose")
	}
	if spec.Pool != "short" || spec.Priority != 100 || spec.MaxAttempts != 5 {
		t.Fatalf("lesson.close spec = %+v, want pool=short priority=100 max_attempts=5", spec)
	}
}
