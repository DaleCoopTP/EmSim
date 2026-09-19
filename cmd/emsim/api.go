// Ported from orchestration-core@34290d74656bc594f8968ae17871dc997559b497
// cmd/api/main.go; adapted: no httpapi.API/runapp.Service — core's public
// server served exactly one route (POST/GET /v1/runs). This process now
// composes auth (slice 1: login/logout/me) on top of the shared
// public-API middleware chain from internal/platform/httpapi (request id,
// no-store, Origin check, JSON 404 envelope); content/training/
// assessment/reporting register their own routes on the same mux in later
// slices. It also serves the embedded SPA build (static.go, web/embed.go)
// for everything outside "/api/" (ADR-009). ready() drops the profile
// registry check (no profiles were ported, see docs/technical-discovery.md
// §3.5).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"emsim/internal/auth"
	authhttp "emsim/internal/auth/http"
	authpg "emsim/internal/auth/postgres"
	"emsim/internal/platform/admin"
	"emsim/internal/platform/config"
	"emsim/internal/platform/httpapi"
	"emsim/internal/platform/observability"
	pgstore "emsim/internal/platform/postgres"
	"emsim/web"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

const apiShutdownTimeout = 10 * time.Second

func runAPI(ctx context.Context, args []string) error {
	if len(args) != 0 {
		return errAPITakesNoArgs
	}
	processConfig, err := config.APIFromEnvironment(os.Getenv)
	if err != nil {
		return err
	}
	pool, err := pgstore.Open(ctx, processConfig.DatabaseURL)
	if err != nil {
		return errors.New("database connection is unavailable")
	}
	defer pool.Close()
	if !apiSchemaReady(ctx, pool) {
		return errors.New("database schema is not ready")
	}

	metricRegistry := prometheus.NewRegistry()
	metrics, err := observability.NewMetrics(metricRegistry, "api")
	if err != nil {
		return errors.New("observability configuration is invalid")
	}
	logger := observability.NewLogger(slog.New(slog.NewJSONHandler(os.Stderr, nil)), "api", "api")
	var live atomic.Bool
	live.Store(true)
	defer live.Store(false)
	readiness := func(requestCtx context.Context) bool {
		isReady := live.Load() && ctx.Err() == nil && requestCtx.Err() == nil && apiSchemaReady(requestCtx, pool)
		metrics.SetReady("api", isReady)
		return isReady
	}

	adminHandler := observability.InstrumentHTTP(
		admin.AdminWithMetrics(readiness, metricRegistry), metrics, logger, observability.AdminRouteNamer,
	)
	publicHandler := observability.InstrumentHTTP(newPublicHandler(pool, processConfig), metrics, logger, observability.PatternRouteNamer)
	publicServer := &http.Server{Addr: processConfig.PublicAddr, Handler: publicHandler, ReadHeaderTimeout: 5 * time.Second}
	adminServer := &http.Server{Addr: processConfig.AdminAddr, Handler: adminHandler, ReadHeaderTimeout: 5 * time.Second}

	metrics.SetReady("api", true)
	defer metrics.SetReady("api", false)
	return serveAPI(ctx, publicServer, adminServer)
}

var errAPITakesNoArgs = errors.New("api subcommand takes no arguments")

// newPublicHandler composes every module's routes onto one API mux,
// mounts that under "/api/" alongside the embedded SPA build under "/"
// (static.go), and wraps the result in the shared public-API middleware
// chain (httpapi.WrapPublic: request id, no-store, Origin check). Each
// module follows the same shape — a pgx store, an application Service
// built on it, HTTP Handlers built on that — composed here and nowhere
// else, matching CLAUDE.md's "composition lives in cmd/emsim". A future
// module (content/training/assessment/reporting) adds its own three
// lines here and calls its own Register on apiMux.
func newPublicHandler(pool *pgxpool.Pool, cfg config.API) http.Handler {
	apiMux := httpapi.NewMux()

	authStore := authpg.NewStore(pool)
	authService := auth.NewService(authStore, cfg.SessionTTL, nil)
	authhttp.NewHandlers(authService, cfg.CookieSecure).Register(apiMux)
	authhttp.NewAdminHandlers(authService).Register(apiMux)

	root := http.NewServeMux()
	root.Handle("/api/", apiMux)
	root.Handle("/", newSPAHandler(web.DistFS))

	return httpapi.WrapPublic(root)
}

func apiSchemaReady(ctx context.Context, pool *pgxpool.Pool) bool {
	if err := pgstore.Ping(ctx, pool); err != nil {
		return false
	}
	ready, err := pgstore.Ready(ctx, pool)
	return err == nil && ready
}

func serveAPI(ctx context.Context, servers ...*http.Server) error {
	errorsCh := make(chan error, len(servers))
	for _, server := range servers {
		server := server
		go func() { errorsCh <- server.ListenAndServe() }()
	}
	received := 0
	var listenErr error
	select {
	case <-ctx.Done():
	case err := <-errorsCh:
		received++
		if !errors.Is(err, http.ErrServerClosed) {
			listenErr = err
		}
	}
	shutdownAPIServers(servers)
	for received < len(servers) {
		<-errorsCh
		received++
	}
	if listenErr != nil {
		return fmt.Errorf("listen: %w", listenErr)
	}
	return nil
}

func shutdownAPIServers(servers []*http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), apiShutdownTimeout)
	defer cancel()
	var group sync.WaitGroup
	for _, server := range servers {
		group.Add(1)
		go func() {
			defer group.Done()
			_ = server.Shutdown(ctx)
		}()
	}
	group.Wait()
}
