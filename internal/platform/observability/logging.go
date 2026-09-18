// Ported from orchestration-core@34290d74656bc594f8968ae17871dc997559b497
// internal/observability/logging.go; adapted: core whitelisted exactly 3
// operations (http_request/queue_sampler/lifecycle) and 2 error codes
// (database_unavailable/operational_error) — too narrow once the tasks
// package has an open kind registry (docs/technical-discovery.md §6:
// "Реестр kind'ов в одном месте"). Operation() keeps the same principle —
// only whitelisted, shape-validated fields reach the log, never request
// payloads or free text — but validates operation/outcome/code by shape
// (the same lowercase-with-underscores pattern as ErrorCode) instead of a
// fixed list. safeStage is dropped along with domain.FailureStage (see
// internal/platform/tasks/failure.go).
package observability

import (
	"context"
	"io"
	"log/slog"
	"regexp"
)

type Logger struct {
	logger  *slog.Logger
	process string
	role    string
}

func NewLogger(logger *slog.Logger, process, role string) Logger {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return Logger{logger: logger, process: process, role: role}
}

var fieldPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
var statusClassPattern = regexp.MustCompile(`^[1-5]xx$`)

// Operation logs one whitelisted-shape event. operation and outcome are
// required and must match fieldPattern (e.g. "http_request"/"success") or,
// for outcome, an HTTP status class ("2xx") — InstrumentHTTP logs the
// response class as its outcome. requestID and code are optional and
// dropped silently if they do not match their own shape, rather than
// logged as-is.
func (l Logger) Operation(ctx context.Context, level slog.Level, operation, outcome, requestID, code string) {
	if !fieldPattern.MatchString(operation) {
		operation = "unknown"
	}
	if !fieldPattern.MatchString(outcome) && !statusClassPattern.MatchString(outcome) {
		outcome = "unknown"
	}
	attrs := []slog.Attr{slog.String("process", l.process), slog.String("operation", operation), slog.String("outcome", outcome)}
	if l.role != "" {
		attrs = append(attrs, slog.String("role", l.role))
	}
	if validCorrelation(requestID) {
		attrs = append(attrs, slog.String("request_id", requestID))
	}
	if code != "" && fieldPattern.MatchString(code) {
		attrs = append(attrs, slog.String("error_code", code))
	}
	l.logger.LogAttrs(ctx, level, "operation", attrs...)
}

func validCorrelation(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-' || character == '_') {
			return false
		}
	}
	return true
}
