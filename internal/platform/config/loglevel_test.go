package config

import (
	"errors"
	"log/slog"
	"testing"
)

func TestLogLevelFromEnvironment(t *testing.T) {
	apiValues := map[string]string{
		"DATABASE_URL": "postgres://example.invalid/emsim", "API_LISTEN_ADDR": "127.0.0.1:8080", "ADMIN_LISTEN_ADDR": "127.0.0.1:8081",
	}
	workerValues := map[string]string{
		"DATABASE_URL": "postgres://example.invalid/emsim", "WORKER_ID": "w", "WORKER_POLL_INTERVAL": "250ms", "WORKER_DRAIN_TIMEOUT": "10s",
		"WORKER_ADMIN_LISTEN_ADDR": "127.0.0.1:8082", "SHORT_CONCURRENCY": "1", "LLM_CONCURRENCY": "1", "STT_CONCURRENCY": "1",
		"REPORT_CONCURRENCY": "1", "CALLER_CONCURRENCY": "1", "CALLER_REPLY_TIMEOUT": "10s", "CALLER_REPLIER": "stub", "ASSESSMENT_JUDGE": "off",
	}
	for _, tt := range []struct {
		raw  string
		want string
		ok   bool
	}{
		{"", "info", true}, {"debug", "debug", true}, {" WARN ", "warn", true}, {"error", "error", true}, {"verbose", "", false},
	} {
		apiValues["LOG_LEVEL"], workerValues["LOG_LEVEL"] = tt.raw, tt.raw
		api, apiErr := APIFromEnvironment(func(n string) string { return apiValues[n] })
		worker, workerErr := WorkerFromEnvironment(func(n string) string { return workerValues[n] }, "all")
		if tt.ok {
			if apiErr != nil || workerErr != nil || api.LogLevel != tt.want || worker.LogLevel != tt.want {
				t.Fatalf("LOG_LEVEL=%q: api=%q/%v worker=%q/%v, want %q", tt.raw, api.LogLevel, apiErr, worker.LogLevel, workerErr, tt.want)
			}
			continue
		}
		if !errors.Is(apiErr, ErrInvalidAPIConfiguration) || !errors.Is(workerErr, ErrInvalidWorkerConfiguration) {
			t.Fatalf("LOG_LEVEL=%q: api=%v worker=%v, want both invalid", tt.raw, apiErr, workerErr)
		}
	}
}

func TestSlogLevel(t *testing.T) {
	for level, want := range map[string]slog.Level{"debug": slog.LevelDebug, "info": slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError, "": slog.LevelInfo} {
		if got := SlogLevel(level); got != want {
			t.Errorf("SlogLevel(%q) = %v, want %v", level, got, want)
		}
	}
}
