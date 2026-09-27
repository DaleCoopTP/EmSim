package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"emsim/internal/platform/config"
)

func TestParseBenchOptions(t *testing.T) {
	opts, err := parseBenchOptions([]string{"--concurrency", "1, 4,20", "--duration", "30s", "--judge", "1"}, io.Discard)
	if err != nil {
		t.Fatalf("parseBenchOptions: %v", err)
	}
	if fmt.Sprint(opts.levels) != "[1 4 20]" || opts.duration != 30*time.Second || opts.judgeLoops != 1 {
		t.Fatalf("unexpected options: %+v", opts)
	}
	if _, err := parseBenchOptions([]string{"--help"}, io.Discard); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("--help error = %v, want flag.ErrHelp so the command exits cleanly", err)
	}
	for _, args := range [][]string{
		{"--concurrency", "0"}, {"--concurrency", "two"}, {"--duration", "0s"}, {"--judge", "-1"}, {"extra"},
	} {
		if _, err := parseBenchOptions(args, io.Discard); err == nil {
			t.Fatalf("parseBenchOptions(%v) accepted invalid arguments", args)
		}
	}
}

// TestLoadBenchScenariosUsesNewestAIVersions reads the real seed: every
// AI caller scenario once, at its newest version — the v2 files, which
// also carry ADR-028's description questions for the judge load.
func TestLoadBenchScenariosUsesNewestAIVersions(t *testing.T) {
	scenarios, err := loadBenchScenarios("../../seed/scenarios")
	if err != nil {
		t.Fatalf("loadBenchScenarios: %v", err)
	}
	if len(scenarios) != 3 {
		t.Fatalf("got %d scenarios, want the 3 AI caller seeds", len(scenarios))
	}
	for _, s := range scenarios {
		if s.caller == nil || len(s.facts) == 0 || len(s.questions) == 0 {
			t.Fatalf("scenario %s is missing caller, facts or description questions", s.key)
		}
	}
	if _, err := loadBenchScenarios(t.TempDir()); err == nil {
		t.Fatal("an empty directory must be an error, not an empty benchmark")
	}
}

func TestPercentileIsNearestRank(t *testing.T) {
	sorted := []time.Duration{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if got := percentile(sorted, 50); got != 5 {
		t.Fatalf("p50 = %v, want 5", got)
	}
	if got := percentile(sorted, 95); got != 10 {
		t.Fatalf("p95 = %v, want 10", got)
	}
	if got := percentile([]time.Duration{7}, 99); got != 7 {
		t.Fatalf("p99 of one sample = %v, want 7", got)
	}
}

// TestBenchRunAgainstFakeServer drives one short level against an
// OpenAI-compatible fake with llama-server-style /metrics: model replies,
// no-model openings, judge answers and server stats must all be reported.
func TestBenchRunAgainstFakeServer(t *testing.T) {
	var predicted, warmups atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/metrics":
			fmt.Fprintf(w, "# HELP llamacpp:tokens_predicted_total x\nllamacpp:tokens_predicted_total %d\nllamacpp:prompt_tokens_total 10\nllamacpp:requests_deferred 1\nllamacpp:requests_processing 2\n", predicted.Load())
		case "/v1/chat/completions":
			var req struct {
				MaxTokens      int `json:"max_tokens"`
				ResponseFormat *struct {
					JSONSchema struct {
						Schema struct {
							Required []string `json:"required"`
						} `json:"schema"`
					} `json:"json_schema"`
				} `json:"response_format"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			predicted.Add(20)
			if req.MaxTokens == 1 {
				warmups.Add(1)
			}
			text := "Мы на месте, приезжайте скорее."
			if req.ResponseFormat != nil {
				answers := map[string]string{}
				for _, id := range req.ResponseFormat.JSONSchema.Schema.Required {
					answers[id] = "yes"
				}
				body, _ := json.Marshal(answers)
				text = string(body)
			}
			body, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
				"message": map[string]string{"role": "assistant", "content": text}, "finish_reason": "stop",
			}}})
			_, _ = w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	scenarios, err := loadBenchScenarios("../../seed/scenarios")
	if err != nil {
		t.Fatalf("loadBenchScenarios: %v", err)
	}
	cfg := config.Worker{
		CallerLLMURL: server.URL + "/v1", CallerLLMModel: "m", CallerReplyTimeout: 10 * time.Second,
		CallerTemperature: 0.3, CallerTopP: 0.9, CallerMaxTokens: 150, CallerWarmup: true,
		JudgeLLMURL: server.URL + "/v1", JudgeLLMModel: "m", JudgeMaxTokens: 1024,
	}
	opts := benchOptions{levels: []int{2}, duration: 1500 * time.Millisecond, think: 10 * time.Millisecond, judgeLoops: 1}
	report, err := benchRun(context.Background(), opts, cfg, scenarios, io.Discard)
	if err != nil {
		t.Fatalf("benchRun: %v", err)
	}
	if len(report.Levels) != 1 {
		t.Fatalf("got %d levels, want 1", len(report.Levels))
	}
	level := report.Levels[0]
	if level.Concurrency != 2 || level.Caller.Count == 0 || level.Caller.Errors != 0 || level.NoModelReplies == 0 {
		t.Fatalf("unexpected caller results: %+v", level)
	}
	if warmups.Load() == 0 || level.FirstReply.Count == 0 || level.FirstReply.Count > level.Caller.Count {
		t.Fatalf("warm-up requests = %d, first replies = %+v, model replies = %+v: every dialogue must be warmed after its opening and its first model reply measured separately",
			warmups.Load(), level.FirstReply, level.Caller)
	}
	if !report.Warmup {
		t.Fatal("report must say the warm-up was on")
	}
	if level.OverTimeout != 0 {
		t.Fatalf("fast fake replies reported over timeout: %+v", level)
	}
	if level.Judge.Count == 0 || level.Judge.Errors != 0 {
		t.Fatalf("unexpected judge results: %+v", level.Judge)
	}
	if level.Server == nil || level.Server.MaxDeferred != 1 || level.Server.PredictedTokensPerSecond <= 0 {
		t.Fatalf("unexpected server stats: %+v", level.Server)
	}

	var table strings.Builder
	printBenchTable(&table, report)
	if !strings.Contains(table.String(), "p95 s") {
		t.Fatalf("table has no header:\n%s", table.String())
	}
	var jsonOut strings.Builder
	if err := writeBenchJSON("-", &jsonOut, report); err != nil {
		t.Fatalf("writeBenchJSON: %v", err)
	}
	var decoded benchReport
	if err := json.Unmarshal([]byte(jsonOut.String()), &decoded); err != nil || len(decoded.Levels) != 1 {
		t.Fatalf("JSON report does not round-trip: %v\n%s", err, jsonOut.String())
	}
}
