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
		"SHORT_CONCURRENCY":        "4", "LLM_CONCURRENCY": "1", "STT_CONCURRENCY": "1",
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
