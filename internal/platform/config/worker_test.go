package config

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestWorkerConfigurationIsRoleAwareAndExplicit(t *testing.T) {
	values := map[string]string{
		"DATABASE_URL": "postgres://example.invalid/emsim", "WORKER_ID": "worker-1",
		"WORKER_POLL_INTERVAL": "250ms", "WORKER_DRAIN_TIMEOUT": "10s",
		"WORKER_ADMIN_LISTEN_ADDR": "127.0.0.1:8082",
		"SHORT_CONCURRENCY":        "4", "LLM_CONCURRENCY": "1", "STT_CONCURRENCY": "1", "REPORT_CONCURRENCY": "1",
		"CALLER_CONCURRENCY": "1", "CALLER_REPLY_TIMEOUT": "10s",
		"CALLER_REPLIER": "stub", "ASSESSMENT_JUDGE": "off",
	}
	lookup := func(name string) string { return values[name] }
	for _, role := range []string{"worker", "maintenance", "all"} {
		if _, err := WorkerFromEnvironment(lookup, role); err != nil {
			t.Fatalf("WorkerFromEnvironment(%s): %v", role, err)
		}
	}
	for _, role := range []string{"", "unknown", "dialogue"} {
		if _, err := WorkerFromEnvironment(lookup, role); !errors.Is(err, ErrInvalidWorkerConfiguration) {
			t.Fatalf("role %q error = %v", role, err)
		}
	}
	delete(values, "LLM_CONCURRENCY")
	if _, err := WorkerFromEnvironment(lookup, "maintenance"); !errors.Is(err, ErrInvalidWorkerConfiguration) {
		t.Fatalf("missing LLM concurrency error = %v", err)
	}
	values["LLM_CONCURRENCY"] = "0"
	if _, err := WorkerFromEnvironment(lookup, "maintenance"); !errors.Is(err, ErrInvalidWorkerConfiguration) {
		t.Fatalf("zero LLM concurrency error = %v", err)
	}
	values["LLM_CONCURRENCY"] = "1"
	delete(values, "CALLER_CONCURRENCY")
	if _, err := WorkerFromEnvironment(lookup, "maintenance"); !errors.Is(err, ErrInvalidWorkerConfiguration) {
		t.Fatalf("missing caller concurrency error = %v", err)
	}
	values["CALLER_CONCURRENCY"] = "1"
	delete(values, "CALLER_REPLY_TIMEOUT")
	if _, err := WorkerFromEnvironment(lookup, "maintenance"); !errors.Is(err, ErrInvalidWorkerConfiguration) {
		t.Fatalf("missing caller reply timeout error = %v", err)
	}
	values["CALLER_REPLY_TIMEOUT"] = "0s"
	if _, err := WorkerFromEnvironment(lookup, "maintenance"); !errors.Is(err, ErrInvalidWorkerConfiguration) {
		t.Fatalf("zero caller reply timeout error = %v", err)
	}
	values["CALLER_REPLY_TIMEOUT"] = "10s"
	delete(values, "WORKER_ADMIN_LISTEN_ADDR")
	if _, err := WorkerFromEnvironment(lookup, "maintenance"); !errors.Is(err, ErrInvalidWorkerConfiguration) {
		t.Fatalf("missing worker admin address error = %v", err)
	}
	values["WORKER_ADMIN_LISTEN_ADDR"] = "127.0.0.1:8082"
	values["WORKER_LOCAL_TEST_POLICY"] = "unknown"
	if _, err := WorkerFromEnvironment(lookup, "maintenance"); !errors.Is(err, ErrInvalidWorkerConfiguration) {
		t.Fatalf("unknown local test policy error = %v", err)
	}
	values["WORKER_LOCAL_TEST_POLICY"] = "e2e-fast-v1"
	if got, err := WorkerFromEnvironment(lookup, "maintenance"); err != nil || got.LocalTestPolicy != "e2e-fast-v1" {
		t.Fatalf("explicit local test policy = %q/%v", got.LocalTestPolicy, err)
	}
}

// TestWorkerModelDefaultsToLLM is ADR-029's own requirement: with
// CALLER_REPLIER and ASSESSMENT_JUDGE unset the worker runs the AI caller
// and the description judge, and a missing endpoint/model is a startup
// error naming the variable to set — never a silent fallback to the stub.
func TestWorkerModelDefaultsToLLM(t *testing.T) {
	values := map[string]string{
		"DATABASE_URL": "postgres://example.invalid/emsim", "WORKER_ID": "worker-1",
		"WORKER_POLL_INTERVAL": "250ms", "WORKER_DRAIN_TIMEOUT": "10s",
		"WORKER_ADMIN_LISTEN_ADDR": "127.0.0.1:8082",
		"SHORT_CONCURRENCY":        "4", "LLM_CONCURRENCY": "1", "STT_CONCURRENCY": "1", "REPORT_CONCURRENCY": "1",
		"CALLER_CONCURRENCY": "1", "CALLER_REPLY_TIMEOUT": "10s",
	}
	lookup := func(name string) string { return values[name] }
	_, err := WorkerFromEnvironment(lookup, "worker")
	if !errors.Is(err, ErrInvalidWorkerConfiguration) || !strings.Contains(err.Error(), "CALLER_LLM_URL") {
		t.Fatalf("default llm caller without endpoint error = %v, want one naming CALLER_LLM_URL", err)
	}
	values["CALLER_LLM_URL"] = "http://llm:8080/v1"
	values["CALLER_LLM_MODEL"] = "t-tech/T-lite-it-2.1:q5_K_M"
	_, err = WorkerFromEnvironment(lookup, "worker")
	if !errors.Is(err, ErrInvalidWorkerConfiguration) || !strings.Contains(err.Error(), "JUDGE_LLM_URL") {
		t.Fatalf("default llm judge without endpoint error = %v, want one naming JUDGE_LLM_URL", err)
	}
	values["JUDGE_LLM_URL"] = "http://llm:8080/v1"
	values["JUDGE_LLM_MODEL"] = "t-tech/T-lite-it-2.1:q5_K_M"
	got, err := WorkerFromEnvironment(lookup, "worker")
	if err != nil {
		t.Fatalf("WorkerFromEnvironment: %v", err)
	}
	if got.CallerReplier != CallerReplierLLM || got.AssessmentJudge != AssessmentJudgeLLM {
		t.Fatalf("defaults = %q/%q, want %q/%q", got.CallerReplier, got.AssessmentJudge, CallerReplierLLM, AssessmentJudgeLLM)
	}
	if got.CallerTemperature != defaultCallerTemperature || got.CallerTopP != defaultCallerTopP ||
		got.CallerRepeatPenalty != defaultCallerRepeatPenalty || got.CallerMaxTokens != defaultCallerMaxTokens {
		t.Fatalf("unexpected caller generation defaults: %+v", got)
	}
	if got.JudgeTimeout != defaultJudgeTimeout || got.JudgeMaxTokens != defaultJudgeMaxTokens {
		t.Fatalf("unexpected judge defaults: %+v", got)
	}
	if !got.CallerWarmup {
		t.Fatal("CallerWarmup default = false, want true (ADR-029)")
	}
	values["CALLER_WARMUP"] = "false"
	if got, err := WorkerFromEnvironment(lookup, "worker"); err != nil || got.CallerWarmup {
		t.Fatalf("CALLER_WARMUP=false = %v/%v, want disabled", got.CallerWarmup, err)
	}
	values["CALLER_WARMUP"] = "sometimes"
	if _, err := WorkerFromEnvironment(lookup, "worker"); !errors.Is(err, ErrInvalidWorkerConfiguration) {
		t.Fatalf("invalid CALLER_WARMUP error = %v", err)
	}
}

// TestWorkerRunsWithoutModelWhenExplicitlyDisabled keeps the no-model
// mode (112-5a's stub, 112-6's rubric-v2) available for e2e/CI and
// development: CALLER_REPLIER=stub and ASSESSMENT_JUDGE=off need no
// endpoint at all.
func TestWorkerRunsWithoutModelWhenExplicitlyDisabled(t *testing.T) {
	values := map[string]string{
		"DATABASE_URL": "postgres://example.invalid/emsim", "WORKER_ID": "worker-1",
		"WORKER_POLL_INTERVAL": "250ms", "WORKER_DRAIN_TIMEOUT": "10s",
		"WORKER_ADMIN_LISTEN_ADDR": "127.0.0.1:8082",
		"SHORT_CONCURRENCY":        "4", "LLM_CONCURRENCY": "1", "STT_CONCURRENCY": "1", "REPORT_CONCURRENCY": "1",
		"CALLER_CONCURRENCY": "1", "CALLER_REPLY_TIMEOUT": "10s",
		"CALLER_REPLIER": "stub", "ASSESSMENT_JUDGE": "off",
	}
	lookup := func(name string) string { return values[name] }
	got, err := WorkerFromEnvironment(lookup, "worker")
	if err != nil {
		t.Fatalf("WorkerFromEnvironment: %v", err)
	}
	if got.CallerReplier != CallerReplierStub || got.AssessmentJudge != AssessmentJudgeOff {
		t.Fatalf("explicit no-model mode = %q/%q", got.CallerReplier, got.AssessmentJudge)
	}
}

// TestWorkerCallerReplierLLMRequiresURLAndModel: CALLER_REPLIER=llm
// without an endpoint/model configured must be rejected up front, not
// surface as a runtime failure the first time a trainee opens the caller
// chat. The judge is switched off so only the caller's own settings are
// under test.
func TestWorkerCallerReplierLLMRequiresURLAndModel(t *testing.T) {
	base := map[string]string{
		"DATABASE_URL": "postgres://example.invalid/emsim", "WORKER_ID": "worker-1",
		"WORKER_POLL_INTERVAL": "250ms", "WORKER_DRAIN_TIMEOUT": "10s",
		"WORKER_ADMIN_LISTEN_ADDR": "127.0.0.1:8082",
		"SHORT_CONCURRENCY":        "4", "LLM_CONCURRENCY": "1", "STT_CONCURRENCY": "1", "REPORT_CONCURRENCY": "1",
		"CALLER_CONCURRENCY": "1", "CALLER_REPLY_TIMEOUT": "10s", "CALLER_REPLIER": "llm",
		"ASSESSMENT_JUDGE": "off",
	}
	lookup := func(values map[string]string) func(string) string {
		return func(name string) string { return values[name] }
	}
	if _, err := WorkerFromEnvironment(lookup(base), "worker"); !errors.Is(err, ErrInvalidWorkerConfiguration) {
		t.Fatalf("llm without URL/model error = %v", err)
	}
	withURL := map[string]string{}
	for k, v := range base {
		withURL[k] = v
	}
	withURL["CALLER_LLM_URL"] = "http://host.docker.internal:11434/v1"
	if _, err := WorkerFromEnvironment(lookup(withURL), "worker"); !errors.Is(err, ErrInvalidWorkerConfiguration) {
		t.Fatalf("llm without model error = %v", err)
	}
	withURL["CALLER_LLM_MODEL"] = "t-tech/T-lite-it-2.1:q5_K_M"
	withURL["CALLER_TEMPERATURE"] = "0.5"
	got, err := WorkerFromEnvironment(lookup(withURL), "worker")
	if err != nil {
		t.Fatalf("complete llm configuration: %v", err)
	}
	if got.CallerReplier != CallerReplierLLM || got.CallerLLMModel != "t-tech/T-lite-it-2.1:q5_K_M" || got.CallerTemperature != 0.5 {
		t.Fatalf("unexpected llm configuration: %+v", got)
	}
	withURL["CALLER_TOP_P"] = "1.5"
	if _, err := WorkerFromEnvironment(lookup(withURL), "worker"); !errors.Is(err, ErrInvalidWorkerConfiguration) {
		t.Fatalf("top_p out of [0,1] error = %v", err)
	}
}

func TestWorkerCallerReplierRejectsUnknownValue(t *testing.T) {
	values := map[string]string{
		"DATABASE_URL": "postgres://example.invalid/emsim", "WORKER_ID": "worker-1",
		"WORKER_POLL_INTERVAL": "250ms", "WORKER_DRAIN_TIMEOUT": "10s",
		"WORKER_ADMIN_LISTEN_ADDR": "127.0.0.1:8082",
		"SHORT_CONCURRENCY":        "4", "LLM_CONCURRENCY": "1", "STT_CONCURRENCY": "1", "REPORT_CONCURRENCY": "1",
		"CALLER_CONCURRENCY": "1", "CALLER_REPLY_TIMEOUT": "10s", "CALLER_REPLIER": "chatgpt",
		"ASSESSMENT_JUDGE": "off",
	}
	lookup := func(name string) string { return values[name] }
	if _, err := WorkerFromEnvironment(lookup, "worker"); !errors.Is(err, ErrInvalidWorkerConfiguration) {
		t.Fatalf("unknown CALLER_REPLIER error = %v", err)
	}
}

// TestWorkerAssessmentJudgeLLMRequiresURLAndModel mirrors
// TestWorkerCallerReplierLLMRequiresURLAndModel for ASSESSMENT_JUDGE=llm,
// with the caller on the stub so only the judge's settings are under
// test.
func TestWorkerAssessmentJudgeLLMRequiresURLAndModel(t *testing.T) {
	base := map[string]string{
		"DATABASE_URL": "postgres://example.invalid/emsim", "WORKER_ID": "worker-1",
		"WORKER_POLL_INTERVAL": "250ms", "WORKER_DRAIN_TIMEOUT": "10s",
		"WORKER_ADMIN_LISTEN_ADDR": "127.0.0.1:8082",
		"SHORT_CONCURRENCY":        "4", "LLM_CONCURRENCY": "1", "STT_CONCURRENCY": "1", "REPORT_CONCURRENCY": "1",
		"CALLER_CONCURRENCY": "1", "CALLER_REPLY_TIMEOUT": "10s", "ASSESSMENT_JUDGE": "llm",
		"CALLER_REPLIER": "stub",
	}
	lookup := func(values map[string]string) func(string) string {
		return func(name string) string { return values[name] }
	}
	if _, err := WorkerFromEnvironment(lookup(base), "worker"); !errors.Is(err, ErrInvalidWorkerConfiguration) {
		t.Fatalf("llm without URL/model error = %v", err)
	}
	withURL := map[string]string{}
	for k, v := range base {
		withURL[k] = v
	}
	withURL["JUDGE_LLM_URL"] = "http://host.docker.internal:11434/v1"
	if _, err := WorkerFromEnvironment(lookup(withURL), "worker"); !errors.Is(err, ErrInvalidWorkerConfiguration) {
		t.Fatalf("llm without model error = %v", err)
	}
	withURL["JUDGE_LLM_MODEL"] = "t-tech/T-lite-it-2.1:q5_K_M"
	withURL["JUDGE_TIMEOUT"] = "30s"
	withURL["JUDGE_MAX_TOKENS"] = "512"
	got, err := WorkerFromEnvironment(lookup(withURL), "worker")
	if err != nil {
		t.Fatalf("complete judge configuration: %v", err)
	}
	if got.AssessmentJudge != AssessmentJudgeLLM || got.JudgeLLMModel != "t-tech/T-lite-it-2.1:q5_K_M" ||
		got.JudgeTimeout != 30*time.Second || got.JudgeMaxTokens != 512 {
		t.Fatalf("unexpected judge configuration: %+v", got)
	}
}

func TestWorkerAssessmentJudgeRejectsUnknownValue(t *testing.T) {
	values := map[string]string{
		"DATABASE_URL": "postgres://example.invalid/emsim", "WORKER_ID": "worker-1",
		"WORKER_POLL_INTERVAL": "250ms", "WORKER_DRAIN_TIMEOUT": "10s",
		"WORKER_ADMIN_LISTEN_ADDR": "127.0.0.1:8082",
		"SHORT_CONCURRENCY":        "4", "LLM_CONCURRENCY": "1", "STT_CONCURRENCY": "1", "REPORT_CONCURRENCY": "1",
		"CALLER_CONCURRENCY": "1", "CALLER_REPLY_TIMEOUT": "10s", "ASSESSMENT_JUDGE": "gpt5",
		"CALLER_REPLIER": "stub",
	}
	lookup := func(name string) string { return values[name] }
	if _, err := WorkerFromEnvironment(lookup, "worker"); !errors.Is(err, ErrInvalidWorkerConfiguration) {
		t.Fatalf("unknown ASSESSMENT_JUDGE error = %v", err)
	}
}
