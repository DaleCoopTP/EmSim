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
	"time"

	"emsim/internal/assessment/operator112/descjudge"
	"emsim/internal/platform/config"
	"emsim/internal/platform/tasks"
	"emsim/internal/reporting"
	"emsim/internal/training"
	"emsim/internal/training/operator112"
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

// TestRegisterKindsIncludesAssessmentEvaluate guards slice 6's C5
// invariant: training.KindAssessmentEvaluate must already be registered
// so the api process's own EnqueueWaitingTx (training's close) can find
// a Spec — without this, closing a training item would fail with
// ErrUnknownKind at runtime instead of here.
func TestRegisterKindsIncludesAssessmentEvaluate(t *testing.T) {
	registry, err := tasks.NewRegistry(tasks.DefaultPolicy())
	if err != nil {
		t.Fatalf("tasks.NewRegistry: %v", err)
	}
	if err := registerKinds(registry); err != nil {
		t.Fatalf("registerKinds: %v", err)
	}
	spec, ok := registry.Lookup(training.KindAssessmentEvaluate)
	if !ok {
		t.Fatal("registerKinds did not register training.KindAssessmentEvaluate")
	}
	if spec.Pool != "llm" || spec.Priority != 100 || spec.MaxAttempts != 3 {
		t.Fatalf("assessment.evaluate spec = %+v, want pool=llm priority=100 max_attempts=3", spec)
	}
}

func TestRegisterKindsIncludesReportBuildInDedicatedPool(t *testing.T) {
	registry, err := tasks.NewRegistry(tasks.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if err := registerKinds(registry); err != nil {
		t.Fatal(err)
	}
	spec, ok := registry.Lookup(reporting.KindBuild)
	if !ok || spec.Pool != "report" || spec.MaxAttempts != 3 || spec.Lease != 2*time.Minute {
		t.Fatalf("report.build spec = %+v", spec)
	}
}

// TestCallerReplyFallbackOutcomeAppliesOnlyOnLastAttempt is 112-5b/
// ADR-025's decision 6, pulled out into callerReplyFallbackOutcome so it
// is testable without a database (see its own doc comment): a model
// failure on an attempt before the last one must still retry normally
// (ok=false, the pre-112-5b behavior callerReplyHandler already had),
// and CALLER_REPLIER=stub (fallback==nil) must never apply a fallback
// reply at all, on any attempt — 112-5a's original retry-then-Failed
// protocol stays exactly as it was.
func TestCallerReplyFallbackOutcomeAppliesOnlyOnLastAttempt(t *testing.T) {
	req := operator112.CallerReplyRequest{Turn: 3}
	called := false
	fallback := func(gotReq operator112.CallerReplyRequest) operator112.CallerReply {
		called = true
		if gotReq.Turn != req.Turn {
			t.Fatalf("fallback got req.Turn=%d, want %d", gotReq.Turn, req.Turn)
		}
		return operator112.CallerReply{Text: "fallback text"}
	}

	if _, ok := callerReplyFallbackOutcome(fallback, req, 1, 2); ok || called {
		t.Fatalf("attempt 1 of 2 must retry, not fall back (ok=%v called=%v)", ok, called)
	}
	reply, ok := callerReplyFallbackOutcome(fallback, req, 2, 2)
	if !ok || !called || reply.Text != "fallback text" {
		t.Fatalf("attempt 2 of 2 must apply the fallback: ok=%v called=%v reply=%+v", ok, called, reply)
	}

	called = false
	if _, ok := callerReplyFallbackOutcome(nil, req, 2, 2); ok || called {
		t.Fatalf("a nil fallback (CALLER_REPLIER=stub) must never apply, even on the last attempt: ok=%v called=%v", ok, called)
	}
}

// TestJudgeConfigForOff is ADR-028's own compatibility requirement,
// mirroring the caller wiring above: a stock deployment with
// ASSESSMENT_JUDGE unset (config.AssessmentJudgeOff's own default) must
// get a nil JudgeConfig — Service.judge==nil skips every new code path
// sealInputForItem/Handle added, exactly 112-6's pre-ADR-028 behavior.
func TestJudgeConfigForOff(t *testing.T) {
	if got := judgeConfigFor(config.Worker{AssessmentJudge: config.AssessmentJudgeOff}); got != nil {
		t.Fatalf("judgeConfigFor(off) = %+v, want nil", got)
	}
}

// TestJudgeConfigForLLMWiresOneHandler exercises the other side: an
// enabled judge gets a Registry with exactly one entry, keyed by
// descjudge.PromptVersion (the only prompt version this ADR's own
// evaluator knows how to dispatch), and Model/Timeout carried straight
// from config.Worker's own fields (sealed into assessment_inputs.judge
// verbatim by sealInputForItem).
func TestJudgeConfigForLLMWiresOneHandler(t *testing.T) {
	cfg := config.Worker{
		AssessmentJudge: config.AssessmentJudgeLLM, JudgeLLMURL: "http://host.docker.internal:11434/v1",
		JudgeLLMModel: "t-tech/T-lite-it-2.1:q5_K_M", JudgeTimeout: 45 * time.Second, JudgeMaxTokens: 777,
	}
	got := judgeConfigFor(cfg)
	if got == nil {
		t.Fatal("judgeConfigFor(llm) = nil, want a JudgeConfig")
	}
	if got.Model != cfg.JudgeLLMModel || got.Timeout != cfg.JudgeTimeout {
		t.Fatalf("unexpected JudgeConfig: %+v", got)
	}
	if _, ok := got.Registry[descjudge.PromptVersion]; !ok || len(got.Registry) != 1 {
		t.Fatalf("Registry = %+v, want exactly one entry for %q", got.Registry, descjudge.PromptVersion)
	}
	if maxTokens, _ := got.Parameters["max_tokens"].(int); maxTokens != cfg.JudgeMaxTokens {
		t.Fatalf("Parameters[max_tokens] = %v, want %d", got.Parameters["max_tokens"], cfg.JudgeMaxTokens)
	}
}
