// Ported from orchestration-core@34290d74656bc594f8968ae17871dc997559b497
// cmd/migrate/main.go; adapted: folded into the emsim single-binary
// dispatcher (runMigrate is called from main.go's run()) instead of its own
// main(); otherwise as-is.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	pgstore "emsim/internal/platform/postgres"
)

var (
	errMigrateCommandRequired = errors.New("migration command is required")
	errMigrateUnknownCommand  = errors.New("unknown migration command")
	errSchemaNotReady         = errors.New("schema is not ready")
)

func runMigrate(ctx context.Context, args []string) error {
	return migrateRun(ctx, args, os.Getenv("DATABASE_URL"), os.Stdout)
}

func migrateRun(ctx context.Context, args []string, databaseURL string, stdout io.Writer) error {
	if len(args) == 0 {
		return errMigrateCommandRequired
	}
	if len(args) != 1 {
		return errMigrateUnknownCommand
	}

	command := args[0]
	if command != "up" && command != "down" && command != "version" && command != "ready" {
		return errMigrateUnknownCommand
	}
	if databaseURL == "" {
		return pgstore.ErrDatabaseURLRequired
	}

	switch command {
	case "up":
		return pgstore.Up(ctx, databaseURL)
	case "down":
		return pgstore.Down(ctx, databaseURL)
	}

	pool, err := pgstore.Open(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := pgstore.Ping(ctx, pool); err != nil {
		return err
	}

	if command == "version" {
		version, err := pgstore.CurrentVersion(ctx, pool)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, version)
		return err
	}

	ready, err := pgstore.Ready(ctx, pool)
	if err != nil {
		return err
	}
	if !ready {
		return errSchemaNotReady
	}
	_, err = fmt.Fprintln(stdout, "ready")
	return err
}
