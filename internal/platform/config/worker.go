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
	LocalTestPolicy    string
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
	config := Worker{
		DatabaseURL: strings.TrimSpace(lookup("DATABASE_URL")), Role: role,
		WorkerID: strings.TrimSpace(lookup("WORKER_ID")), PollInterval: poll, DrainTimeout: drain,
		AdminAddr:        strings.TrimSpace(lookup("WORKER_ADMIN_LISTEN_ADDR")),
		ShortConcurrency: short, LLMConcurrency: llm, STTConcurrency: stt, ReportConcurrency: report,
		CallerConcurrency: caller, CallerReplyTimeout: callerReplyTimeout,
		LocalTestPolicy: strings.TrimSpace(lookup("WORKER_LOCAL_TEST_POLICY")),
	}
	if err := config.Validate(); err != nil {
		return Worker{}, err
	}
	return config, nil
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
