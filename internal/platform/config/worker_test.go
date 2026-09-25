package config

import (
	"errors"
	"testing"
)

func TestWorkerConfigurationIsRoleAwareAndExplicit(t *testing.T) {
	values := map[string]string{
		"DATABASE_URL": "postgres://example.invalid/emsim", "WORKER_ID": "worker-1",
		"WORKER_POLL_INTERVAL": "250ms", "WORKER_DRAIN_TIMEOUT": "10s",
		"WORKER_ADMIN_LISTEN_ADDR": "127.0.0.1:8082",
		"SHORT_CONCURRENCY":        "4", "LLM_CONCURRENCY": "1", "STT_CONCURRENCY": "1", "REPORT_CONCURRENCY": "1",
		"CALLER_CONCURRENCY": "1", "CALLER_REPLY_TIMEOUT": "10s",
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

// TestWorkerCallerReplierDefaultsToStub is 112-5b/ADR-025's own
// compatibility requirement: a stock `docker compose up` with no
// CALLER_REPLIER set must behave exactly like 112-5a (no model
// dependency), so an entirely absent CALLER_REPLIER/generation-parameter
// set of env vars must still produce a valid configuration.
func TestWorkerCallerReplierDefaultsToStub(t *testing.T) {
	values := map[string]string{
		"DATABASE_URL": "postgres://example.invalid/emsim", "WORKER_ID": "worker-1",
		"WORKER_POLL_INTERVAL": "250ms", "WORKER_DRAIN_TIMEOUT": "10s",
		"WORKER_ADMIN_LISTEN_ADDR": "127.0.0.1:8082",
		"SHORT_CONCURRENCY":        "4", "LLM_CONCURRENCY": "1", "STT_CONCURRENCY": "1", "REPORT_CONCURRENCY": "1",
		"CALLER_CONCURRENCY": "1", "CALLER_REPLY_TIMEOUT": "10s",
	}
	lookup := func(name string) string { return values[name] }
	got, err := WorkerFromEnvironment(lookup, "worker")
	if err != nil {
		t.Fatalf("WorkerFromEnvironment: %v", err)
	}
	if got.CallerReplier != CallerReplierStub {
		t.Fatalf("CallerReplier default = %q, want %q", got.CallerReplier, CallerReplierStub)
	}
	if got.CallerTemperature != defaultCallerTemperature || got.CallerTopP != defaultCallerTopP ||
		got.CallerRepeatPenalty != defaultCallerRepeatPenalty || got.CallerMaxTokens != defaultCallerMaxTokens {
		t.Fatalf("unexpected caller generation defaults: %+v", got)
	}
}

// TestWorkerCallerReplierLLMRequiresURLAndModel exercises ADR-025's
// "stub is the safe default" requirement from the other side: opting
// into CALLER_REPLIER=llm without an endpoint/model configured must be
// rejected up front, not surface as a runtime failure the first time a
// trainee opens the caller chat.
func TestWorkerCallerReplierLLMRequiresURLAndModel(t *testing.T) {
	base := map[string]string{
		"DATABASE_URL": "postgres://example.invalid/emsim", "WORKER_ID": "worker-1",
		"WORKER_POLL_INTERVAL": "250ms", "WORKER_DRAIN_TIMEOUT": "10s",
		"WORKER_ADMIN_LISTEN_ADDR": "127.0.0.1:8082",
		"SHORT_CONCURRENCY":        "4", "LLM_CONCURRENCY": "1", "STT_CONCURRENCY": "1", "REPORT_CONCURRENCY": "1",
		"CALLER_CONCURRENCY": "1", "CALLER_REPLY_TIMEOUT": "10s", "CALLER_REPLIER": "llm",
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
	}
	lookup := func(name string) string { return values[name] }
	if _, err := WorkerFromEnvironment(lookup, "worker"); !errors.Is(err, ErrInvalidWorkerConfiguration) {
		t.Fatalf("unknown CALLER_REPLIER error = %v", err)
	}
}
