package main

import (
	"context"
	"errors"
	"testing"

	"emsim/internal/platform/config"
	"emsim/internal/platform/tasks"
)

func TestRunRejectsMissingAndUnknownSubcommand(t *testing.T) {
	t.Parallel()

	if err := run(context.Background(), nil); !errors.Is(err, errCommandRequired) {
		t.Fatalf("run(nil) error = %v, want errCommandRequired", err)
	}
	if err := run(context.Background(), []string{"bogus"}); !errors.Is(err, errUnknownCommand) {
		t.Fatalf(`run(["bogus"]) error = %v, want errUnknownCommand`, err)
	}
}

// TestRunDispatchesKnownSubcommands checks dispatch reaches each
// subcommand's own body — not that the body succeeds. With no environment
// configured, each one fails at its first validation step; the specific
// sentinel proves run() routed to the right place. migrate and worker
// never read the environment before failing on these empty args, so they
// are deterministic regardless of the ambient shell; api does read
// DATABASE_URL/API_LISTEN_ADDR/ADMIN_LISTEN_ADDR as its first step, so
// those are explicitly cleared with t.Setenv (which requires this test not
// run in parallel) instead of relying on them being unset. migrate, api,
// and worker are all implemented now (migrate.go/api.go/worker.go); a
// future subcommand not yet wired up would still surface errNotImplemented.
func TestRunDispatchesKnownSubcommands(t *testing.T) {
	if err := run(context.Background(), []string{"migrate"}); !errors.Is(err, errMigrateCommandRequired) {
		t.Fatalf(`run(["migrate"]) error = %v, want errMigrateCommandRequired`, err)
	}
	if err := run(context.Background(), []string{"worker"}); !errors.Is(err, tasks.ErrInvalidRole) {
		t.Fatalf(`run(["worker"]) error = %v, want tasks.ErrInvalidRole`, err)
	}
	for _, name := range []string{"DATABASE_URL", "API_LISTEN_ADDR", "ADMIN_LISTEN_ADDR"} {
		t.Setenv(name, "")
	}
	if err := run(context.Background(), []string{"api"}); !errors.Is(err, config.ErrInvalidAPIConfiguration) {
		t.Fatalf(`run(["api"]) error = %v, want config.ErrInvalidAPIConfiguration`, err)
	}
}

func TestErrorCodeIsWhitelistedAndStable(t *testing.T) {
	t.Parallel()

	cases := map[error]string{
		errCommandRequired: "invalid_invocation",
		errUnknownCommand:  "invalid_invocation",
		errNotImplemented:  "not_implemented",
		errors.New("anything else, including secrets or paths"): "operational_error",
	}
	for err, want := range cases {
		if got := errorCode(err); got != want {
			t.Fatalf("errorCode(%v) = %q, want %q", err, got, want)
		}
	}
}
