// Ported from orchestration-core@34290d74656bc594f8968ae17871dc997559b497
// cmd/worker/{main,runtime}.go; adapted: role parsing/config/composition
// use internal/platform/tasks and internal/platform/config instead of the
// dialogue/judge-specific worker package.
package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"net/http"
	"os"
	"time"

	"emsim/internal/platform/config"
	pgstore "emsim/internal/platform/postgres"
	"emsim/internal/platform/tasks"

	"github.com/jackc/pgx/v5/pgxpool"
)

func runWorker(ctx context.Context, args []string) error {
	roleValue, err := parseWorkerRole(args)
	if err != nil {
		return err
	}
	processConfig, err := config.WorkerFromEnvironment(os.Getenv, roleValue)
	if err != nil {
		return err
	}
	pool, err := openReadyDatabase(ctx, processConfig.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	telemetry, err := newWorkerTelemetry(processConfig.Role, processConfig.LogLevel)
	if err != nil {
		return err
	}
	components, err := compose(processConfig, pool, telemetry.metrics, telemetry.logger)
	if err != nil {
		return err
	}
	admin := newWorkerAdmin(ctx, processConfig, pool, telemetry)
	defer admin.stop()
	return serveWorker(ctx, admin.server, func(runCtx context.Context) error {
		return tasks.RunRole(runCtx, processConfig.Role, components)
	})
}

func parseWorkerRole(args []string) (string, error) {
	flags := flag.NewFlagSet("worker", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	role := flags.String("role", "", "worker role")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return "", tasks.ErrInvalidRole
	}
	if _, err := tasks.ParseRole(*role); err != nil {
		return "", err
	}
	return *role, nil
}

func openReadyDatabase(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	pool, err := pgstore.Open(ctx, databaseURL)
	if err != nil {
		return nil, errors.New("database connection is unavailable")
	}
	if err := pgstore.Ping(ctx, pool); err != nil {
		pool.Close()
		return nil, errors.New("database connection is unavailable")
	}
	ready, err := pgstore.Ready(ctx, pool)
	if err != nil || !ready {
		pool.Close()
		return nil, errors.New("database schema is not ready")
	}
	return pool, nil
}

func serveWorker(ctx context.Context, server *http.Server, runSupervisor func(context.Context) error) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, 2)
	go func() { results <- server.ListenAndServe() }()
	go func() { results <- runSupervisor(runCtx) }()
	first := <-results
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	_ = server.Shutdown(shutdownCtx)
	shutdownCancel()
	second := <-results
	if !errors.Is(first, http.ErrServerClosed) && first != nil {
		return errors.New("worker operational failure")
	}
	if !errors.Is(second, http.ErrServerClosed) && second != nil {
		return errors.New("worker operational failure")
	}
	return nil
}
