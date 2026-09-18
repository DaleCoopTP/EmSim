package main

import (
	"context"
	"errors"
	"testing"
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

func TestRunDispatchesKnownSubcommands(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"api", "worker"} {
		if err := run(context.Background(), []string{command}); !errors.Is(err, errNotImplemented) {
			t.Fatalf("run([%q]) error = %v, want errNotImplemented", command, err)
		}
	}
	// migrate is implemented (migrate.go); dispatch reaches it and surfaces
	// its own missing-subcommand error rather than errNotImplemented.
	if err := run(context.Background(), []string{"migrate"}); !errors.Is(err, errMigrateCommandRequired) {
		t.Fatalf(`run(["migrate"]) error = %v, want errMigrateCommandRequired`, err)
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
