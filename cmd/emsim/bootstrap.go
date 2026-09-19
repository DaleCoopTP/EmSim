// bootstrap-admin creates the initial admin account for an empty
// installation (slice-planning.md §2: "первоначальная учётная запись
// администратора для пустой установки"). It is meant to run as a
// one-shot step between "migrate up" and "api" (see compose.yaml's
// "bootstrap" service) and is idempotent: run again against an
// installation that already has an active admin, it exits 0 without
// creating a second one. Nothing read from the environment is ever
// wrapped into a returned error, matching main.go's rule that
// errorCode() never leaks operator-supplied text into the process log.
package main

import (
	"context"
	"errors"
	"os"

	"emsim/internal/auth"
	authpg "emsim/internal/auth/postgres"
	pgstore "emsim/internal/platform/postgres"
)

var (
	errBootstrapTakesNoArgs         = errors.New("bootstrap-admin subcommand takes no arguments")
	errBootstrapCredentialsRequired = errors.New("BOOTSTRAP_ADMIN_LOGIN and BOOTSTRAP_ADMIN_PASSWORD are required")
)

func runBootstrapAdmin(ctx context.Context, args []string) error {
	if len(args) != 0 {
		return errBootstrapTakesNoArgs
	}
	login := os.Getenv("BOOTSTRAP_ADMIN_LOGIN")
	password := os.Getenv("BOOTSTRAP_ADMIN_PASSWORD")
	if login == "" || password == "" {
		return errBootstrapCredentialsRequired
	}
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return pgstore.ErrDatabaseURLRequired
	}

	pool, err := pgstore.Open(ctx, databaseURL)
	if err != nil {
		return errors.New("database connection is unavailable")
	}
	defer pool.Close()
	ready, err := pgstore.Ready(ctx, pool)
	if err != nil {
		return errors.New("database schema is not ready")
	}
	if !ready {
		return errSchemaNotReady
	}

	authStore := authpg.NewStore(pool)
	// ttl is unused: BootstrapAdmin never creates a session. catalog is
	// nil: BootstrapAdmin always creates an admin, which never carries a
	// service_code, so it has no need of internal/content here (see
	// auth.NewService's doc comment).
	authService := auth.NewService(authStore, auth.NewPasswordIdentityProvider(authStore), 0, nil, nil)
	_, err = authService.BootstrapAdmin(ctx, login, password)
	return err
}
