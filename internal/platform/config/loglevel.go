package config

import (
	"log/slog"
	"strings"
)

// LogLevel names (LOG_LEVEL, ADR-038). An unset variable means info.
const (
	LogLevelDebug = "debug"
	LogLevelInfo  = "info"
	LogLevelWarn  = "warn"
	LogLevelError = "error"
)

// parseLogLevel returns the canonical level name, or false for a value
// that is set but not one of the four names — a mistyped LOG_LEVEL
// should fail startup, not silently fall back.
func parseLogLevel(raw string) (string, bool) {
	switch level := strings.ToLower(strings.TrimSpace(raw)); level {
	case "":
		return LogLevelInfo, true
	case LogLevelDebug, LogLevelInfo, LogLevelWarn, LogLevelError:
		return level, true
	default:
		return "", false
	}
}

// SlogLevel maps a canonical level name to slog's level (info for
// anything unrecognised — Validate has already rejected those).
func SlogLevel(level string) slog.Level {
	switch level {
	case LogLevelDebug:
		return slog.LevelDebug
	case LogLevelWarn:
		return slog.LevelWarn
	case LogLevelError:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
