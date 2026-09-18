// Command emsim is the single binary for the EmSim server: subcommands select
// the role a process plays (ADR-001). "migrate" applies the database schema,
// "api" serves the operator/instructor HTTP API and SSE, "worker" claims and
// executes background tasks (LLM/STT/TTS, reports, scenario generation).
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

var (
	errCommandRequired = errors.New("subcommand is required: migrate | api | worker")
	errUnknownCommand  = errors.New("unknown subcommand")
	errNotImplemented  = errors.New("subcommand is not implemented yet")
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:]); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("process stopped",
			"process", "emsim", "outcome", "failed", "error_code", errorCode(err))
		os.Exit(1)
	}
}

// run dispatches to the subcommand named by args[0]. It never logs or wraps
// args/env into error text: main() maps the returned error to a fixed,
// whitelisted error_code, so nothing operator-supplied reaches the log.
func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errCommandRequired
	}
	command, rest := args[0], args[1:]
	switch command {
	case "migrate":
		return runMigrate(ctx, rest)
	case "api":
		return runAPI(ctx, rest)
	case "worker":
		return runWorker(ctx, rest)
	default:
		return errUnknownCommand
	}
}

// runAPI and runWorker are filled in by later commits (HTTP API, worker
// composition). They are wired here first so the subcommand surface and its
// error handling are fixed before the bodies land. runMigrate lives in
// migrate.go.
func runAPI(_ context.Context, _ []string) error    { return errNotImplemented }
func runWorker(_ context.Context, _ []string) error { return errNotImplemented }

func errorCode(err error) string {
	switch {
	case errors.Is(err, errCommandRequired), errors.Is(err, errUnknownCommand):
		return "invalid_invocation"
	case errors.Is(err, errNotImplemented):
		return "not_implemented"
	default:
		return "operational_error"
	}
}
