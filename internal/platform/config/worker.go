package config

import (
	"errors"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"emsim/internal/platform/tasks"
)

var ErrInvalidWorkerConfiguration = errors.New("invalid worker configuration")

// CallerReplier's two allowed values (Worker.CallerReplier's own doc
// comment).
const (
	CallerReplierStub = "stub"
	CallerReplierLLM  = "llm"
)

// Caller generation defaults mirror the local MVP's own tuned values
// (handoff/README.md, 2026-09-25: t-tech/T-lite-it-2.1:q5_K_M via
// Ollama) — a development starting point, not a W0-validated production
// setting (slice-112-5b-plan.md's stage 2 still owns that).
const (
	defaultCallerTemperature   = 0.3
	defaultCallerTopP          = 0.9
	defaultCallerRepeatPenalty = 1.1
	defaultCallerMaxTokens     = 150
)

type Worker struct {
	DatabaseURL       string
	Role              tasks.Role
	WorkerID          string
	PollInterval      time.Duration
	DrainTimeout      time.Duration
	AdminAddr         string
	ShortConcurrency  int
	LLMConcurrency    int
	STTConcurrency    int
	ReportConcurrency int
	// CallerConcurrency (112-5a/ADR-024) is caller.reply's own pool
	// size, kept separate from LLMConcurrency so one caller-chat reply
	// never queues behind assessment.evaluate/scenario.generate — see
	// cmd/emsim/worker_composition.go's registerKinds. 112-5b's model
	// adapter reuses this same pool; nothing here is stub-specific.
	CallerConcurrency int
	// CallerReplyTimeout bounds one CallerReplier.Reply call
	// (112-5a/ADR-024): the worker's caller.reply handler cancels its
	// ctx after this, so a stuck stub/model call cannot hold the task's
	// lease forever. caller.reply's own Spec.Lease (registerKinds) must
	// stay comfortably longer than this.
	CallerReplyTimeout time.Duration
	// CallerReplier selects operator112.CallerReplier for the "caller"
	// pool (112-5b/ADR-025): CallerReplierStub (default — StubCallerReplier,
	// no model dependency, exactly 112-5a's own behavior) or
	// CallerReplierLLM (aicaller.Replier over CallerLLMURL/CallerLLMModel).
	// A stock `docker compose up` with no CALLER_REPLIER set must keep
	// working without Ollama/llama-server, so the zero value from an
	// unset env var resolves to CallerReplierStub, not an error.
	CallerReplier string
	// CallerLLMURL/CallerLLMModel are required only when CallerReplier is
	// CallerReplierLLM — an OpenAI-compatible base URL (e.g.
	// "http://host.docker.internal:11434/v1" for Ollama in development;
	// a llama-server URL at the customer, slice-112-5b-plan.md's stage 2)
	// and the model name Ollama/llama-server serves under it.
	CallerLLMURL   string
	CallerLLMModel string
	// CallerTemperature/CallerTopP/CallerRepeatPenalty/CallerMaxTokens are
	// aicaller.Replier's own generation parameters, defaulted to the
	// local MVP's own tuned values (see the defaultCaller* constants)
	// when CallerReplier is CallerReplierLLM and the env var is unset.
	CallerTemperature   float64
	CallerTopP          float64
	CallerRepeatPenalty float64
	CallerMaxTokens     int
	LocalTestPolicy     string
}

func WorkerFromEnvironment(lookup func(string) string, roleValue string) (Worker, error) {
	if lookup == nil {
		return Worker{}, ErrInvalidWorkerConfiguration
	}
	role, err := tasks.ParseRole(strings.TrimSpace(roleValue))
	if err != nil {
		return Worker{}, ErrInvalidWorkerConfiguration
	}
	poll, err := time.ParseDuration(strings.TrimSpace(lookup("WORKER_POLL_INTERVAL")))
	if err != nil {
		return Worker{}, ErrInvalidWorkerConfiguration
	}
	drain, err := time.ParseDuration(strings.TrimSpace(lookup("WORKER_DRAIN_TIMEOUT")))
	if err != nil {
		return Worker{}, ErrInvalidWorkerConfiguration
	}
	short, err := parsePoolSize(lookup("SHORT_CONCURRENCY"))
	if err != nil {
		return Worker{}, err
	}
	llm, err := parsePoolSize(lookup("LLM_CONCURRENCY"))
	if err != nil {
		return Worker{}, err
	}
	stt, err := parsePoolSize(lookup("STT_CONCURRENCY"))
	if err != nil {
		return Worker{}, err
	}
	report, err := parsePoolSize(lookup("REPORT_CONCURRENCY"))
	if err != nil {
		return Worker{}, err
	}
	caller, err := parsePoolSize(lookup("CALLER_CONCURRENCY"))
	if err != nil {
		return Worker{}, err
	}
	callerReplyTimeout, err := time.ParseDuration(strings.TrimSpace(lookup("CALLER_REPLY_TIMEOUT")))
	if err != nil {
		return Worker{}, ErrInvalidWorkerConfiguration
	}
	callerReplier := strings.TrimSpace(lookup("CALLER_REPLIER"))
	if callerReplier == "" {
		callerReplier = CallerReplierStub
	}
	callerTemperature, err := parseFloatOrDefault(lookup("CALLER_TEMPERATURE"), defaultCallerTemperature)
	if err != nil {
		return Worker{}, ErrInvalidWorkerConfiguration
	}
	callerTopP, err := parseFloatOrDefault(lookup("CALLER_TOP_P"), defaultCallerTopP)
	if err != nil {
		return Worker{}, ErrInvalidWorkerConfiguration
	}
	callerRepeatPenalty, err := parseFloatOrDefault(lookup("CALLER_REPEAT_PENALTY"), defaultCallerRepeatPenalty)
	if err != nil {
		return Worker{}, ErrInvalidWorkerConfiguration
	}
	callerMaxTokens, err := parseIntOrDefault(lookup("CALLER_MAX_TOKENS"), defaultCallerMaxTokens)
	if err != nil {
		return Worker{}, ErrInvalidWorkerConfiguration
	}
	config := Worker{
		DatabaseURL: strings.TrimSpace(lookup("DATABASE_URL")), Role: role,
		WorkerID: strings.TrimSpace(lookup("WORKER_ID")), PollInterval: poll, DrainTimeout: drain,
		AdminAddr:        strings.TrimSpace(lookup("WORKER_ADMIN_LISTEN_ADDR")),
		ShortConcurrency: short, LLMConcurrency: llm, STTConcurrency: stt, ReportConcurrency: report,
		CallerConcurrency: caller, CallerReplyTimeout: callerReplyTimeout,
		CallerReplier: callerReplier, CallerLLMURL: strings.TrimSpace(lookup("CALLER_LLM_URL")),
		CallerLLMModel:    strings.TrimSpace(lookup("CALLER_LLM_MODEL")),
		CallerTemperature: callerTemperature, CallerTopP: callerTopP,
		CallerRepeatPenalty: callerRepeatPenalty, CallerMaxTokens: callerMaxTokens,
		LocalTestPolicy: strings.TrimSpace(lookup("WORKER_LOCAL_TEST_POLICY")),
	}
	if err := config.Validate(); err != nil {
		return Worker{}, err
	}
	return config, nil
}

func parseFloatOrDefault(raw string, fallback float64) (float64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	return strconv.ParseFloat(raw, 64)
}

func parseIntOrDefault(raw string, fallback int) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	return strconv.Atoi(raw)
}

func parsePoolSize(value string) (int, error) {
	size, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || size < 1 {
		return 0, ErrInvalidWorkerConfiguration
	}
	return size, nil
}

func (c Worker) Validate() error {
	if c.DatabaseURL == "" || c.WorkerID == "" || c.WorkerID != strings.TrimSpace(c.WorkerID) ||
		len(c.WorkerID) > 256 || !utf8.ValidString(c.WorkerID) || c.PollInterval <= 0 || c.DrainTimeout <= 0 ||
		!validWorkerListenAddress(c.AdminAddr) || c.ShortConcurrency < 1 || c.LLMConcurrency < 1 || c.STTConcurrency < 1 || c.ReportConcurrency < 1 ||
		c.CallerConcurrency < 1 || c.CallerReplyTimeout <= 0 {
		return ErrInvalidWorkerConfiguration
	}
	if _, err := tasks.ParseRole(string(c.Role)); err != nil {
		return ErrInvalidWorkerConfiguration
	}
	if c.LocalTestPolicy != "" && c.LocalTestPolicy != "e2e-fast-v1" {
		return ErrInvalidWorkerConfiguration
	}
	if c.CallerReplier != CallerReplierStub && c.CallerReplier != CallerReplierLLM {
		return ErrInvalidWorkerConfiguration
	}
	if c.CallerReplier == CallerReplierLLM && (c.CallerLLMURL == "" || c.CallerLLMModel == "") {
		return ErrInvalidWorkerConfiguration
	}
	if c.CallerTemperature < 0 || c.CallerTopP < 0 || c.CallerTopP > 1 || c.CallerRepeatPenalty < 0 || c.CallerMaxTokens < 1 {
		return ErrInvalidWorkerConfiguration
	}
	return nil
}

func validWorkerListenAddress(address string) bool {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	value, err := strconv.ParseUint(port, 10, 16)
	return err == nil && value > 0
}
