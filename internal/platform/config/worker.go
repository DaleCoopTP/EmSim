package config

import (
	"errors"
	"fmt"
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

// AssessmentJudge's two allowed values (ADR-028) — API.AssessmentJudge's
// and Worker.AssessmentJudge's own doc comments. Named distinctly from
// CallerReplier's own Stub/LLM constants (rather than reused) since the
// two settings are read by different call sites for different reasons
// (a rubric-version choice at lesson creation vs. an actual model
// client in the worker) and are allowed to diverge operationally even
// though a real deployment sets both the same way.
const (
	AssessmentJudgeOff = "off"
	AssessmentJudgeLLM = "llm"
)

// Judge generation defaults mirror the archived prototype's own tuned
// values (handoff/claude_evaluator_112_20260926.zip's description_
// evaluator.py: temperature=0, num_predict=1024) — deterministic
// wording matters more than variety for a yes/no/needs_review judge.
const (
	defaultJudgeTimeout   = 120 * time.Second
	defaultJudgeMaxTokens = 1024
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
	// pool (112-5b/ADR-025): CallerReplierLLM (default since ADR-029 —
	// aicaller.Replier over CallerLLMURL/CallerLLMModel, the compose
	// "llm" service in a stock deployment) or CallerReplierStub
	// (StubCallerReplier, no model dependency, exactly 112-5a's own
	// behavior — an explicit setting for e2e/CI and development without
	// a model). An unset env var resolves to CallerReplierLLM, and then a
	// missing URL/model is a startup error naming the variable, never a
	// silent fallback to the stub.
	CallerReplier string
	// CallerLLMURL/CallerLLMModel are required only when CallerReplier is
	// CallerReplierLLM — an OpenAI-compatible base URL (compose's own
	// "http://llm:8080/v1" llama-server, ADR-029; or
	// "http://host.docker.internal:11434/v1" for Ollama in development)
	// and the model name (llama-server's --alias) it serves under.
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
	// CallerWarmup (ADR-029) lets caller.warmup tasks reach the model:
	// the api enqueues them at answer_incoming and at the first operator
	// message (API.CallerWarmup, the same CALLER_WARMUP), and with this
	// off the worker finishes them without a model call. Only meaningful
	// with CallerReplierLLM; CALLER_WARMUP unset means true.
	CallerWarmup bool
	// CallerOpeningDelay is API.CallerOpeningDelay read here too, only so
	// bench-llm reproduces the class's own timing; the worker itself
	// never delays anything.
	CallerOpeningDelay time.Duration
	// AssessmentJudge (ADR-028) selects whether assessment.evaluate's
	// worker-side Handle actually calls a model for operator112_intake's
	// DESCRIPTION_CONTENT criterion (operator112/rubric-v3):
	// AssessmentJudgeLLM (default since ADR-029 — a descjudge.Handler
	// over JudgeLLMURL/JudgeLLMModel, registered into
	// assessment.JudgeConfig.Registry) or AssessmentJudgeOff (explicit —
	// no judge wired at all, exactly 112-6's pre-ADR-028 behavior). It is deliberately the same enum
	// spelling as API.AssessmentJudge (both read the same env var in
	// compose) but a separate config field: the api process only ever
	// needs it to pick a rubric_version at lesson creation, never to
	// build an LLM client.
	AssessmentJudge string
	// JudgeLLMURL/JudgeLLMModel are required only when AssessmentJudge is
	// AssessmentJudgeLLM — an OpenAI-compatible base URL and the model
	// name it serves under, the same CALLER_LLM_URL/CALLER_LLM_MODEL
	// convention (a separate pair, not reused: the judge and the caller
	// may run different models, or only one of the two may be enabled).
	JudgeLLMURL   string
	JudgeLLMModel string
	// JudgeTimeout bounds one SemanticJudge.Answer call the same way
	// CallerReplyTimeout bounds one CallerReplier.Reply call — Handle
	// cancels its ctx after this, so a stuck judge call cannot hold
	// assessment.evaluate's own lease forever.
	JudgeTimeout time.Duration
	// JudgeMaxTokens is descjudge's own num_predict/max_tokens — see the
	// defaultJudgeMaxTokens doc comment for why 1024 is the default.
	JudgeMaxTokens  int
	LocalTestPolicy string
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
		callerReplier = CallerReplierLLM
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
	callerWarmup, callerOpeningDelay, ok := callerTimingFromEnvironment(lookup)
	if !ok {
		return Worker{}, ErrInvalidWorkerConfiguration
	}
	assessmentJudge := strings.TrimSpace(lookup("ASSESSMENT_JUDGE"))
	if assessmentJudge == "" {
		assessmentJudge = AssessmentJudgeLLM
	}
	judgeTimeout, err := parseDurationOrDefault(lookup("JUDGE_TIMEOUT"), defaultJudgeTimeout)
	if err != nil {
		return Worker{}, ErrInvalidWorkerConfiguration
	}
	judgeMaxTokens, err := parseIntOrDefault(lookup("JUDGE_MAX_TOKENS"), defaultJudgeMaxTokens)
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
		CallerRepeatPenalty: callerRepeatPenalty, CallerMaxTokens: callerMaxTokens, CallerWarmup: callerWarmup,
		CallerOpeningDelay: callerOpeningDelay,
		AssessmentJudge:    assessmentJudge, JudgeLLMURL: strings.TrimSpace(lookup("JUDGE_LLM_URL")),
		JudgeLLMModel: strings.TrimSpace(lookup("JUDGE_LLM_MODEL")),
		JudgeTimeout:  judgeTimeout, JudgeMaxTokens: judgeMaxTokens,
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
		return fmt.Errorf("%w: CALLER_REPLIER=llm (the default) needs CALLER_LLM_URL and CALLER_LLM_MODEL; set CALLER_REPLIER=stub to run without a model", ErrInvalidWorkerConfiguration)
	}
	if c.CallerTemperature < 0 || c.CallerTopP < 0 || c.CallerTopP > 1 || c.CallerRepeatPenalty < 0 || c.CallerMaxTokens < 1 {
		return ErrInvalidWorkerConfiguration
	}
	if c.AssessmentJudge != AssessmentJudgeOff && c.AssessmentJudge != AssessmentJudgeLLM {
		return ErrInvalidWorkerConfiguration
	}
	if c.AssessmentJudge == AssessmentJudgeLLM && (c.JudgeLLMURL == "" || c.JudgeLLMModel == "") {
		return fmt.Errorf("%w: ASSESSMENT_JUDGE=llm (the default) needs JUDGE_LLM_URL and JUDGE_LLM_MODEL; set ASSESSMENT_JUDGE=off to run without a model", ErrInvalidWorkerConfiguration)
	}
	if c.JudgeTimeout <= 0 || c.JudgeMaxTokens < 1 {
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
