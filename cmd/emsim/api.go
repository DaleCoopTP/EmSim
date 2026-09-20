// Ported from orchestration-core@34290d74656bc594f8968ae17871dc997559b497
// cmd/api/main.go; adapted: no httpapi.API/runapp.Service — core's public
// server served exactly one route (POST/GET /v1/runs). This process now
// composes auth (slice 1: login/logout/me) and content (slice 2: the
// instructor catalogue — GET /services, /scenarios*) on top of the shared
// public-API middleware chain from internal/platform/httpapi (request id,
// no-store, Origin check, JSON 404 envelope); training/assessment/
// reporting register their own routes on the same mux in later slices.
// It also serves the embedded SPA build (static.go, web/embed.go)
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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"emsim/internal/auth"
	authhttp "emsim/internal/auth/http"
	authpg "emsim/internal/auth/postgres"
	"emsim/internal/content"
	contenthttp "emsim/internal/content/http"
	contentpg "emsim/internal/content/postgres"
	"emsim/internal/content/schema"
	"emsim/internal/platform/admin"
	"emsim/internal/platform/config"
	"emsim/internal/platform/httpapi"
	"emsim/internal/platform/observability"
	pgstore "emsim/internal/platform/postgres"
	"emsim/internal/platform/tasks"
	"emsim/internal/training"
	traininghttp "emsim/internal/training/http"
	"emsim/web"

	"github.com/google/uuid"
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
	publicRoutes, publicRouteName, trainingService := newPublicHTTP(pool, processConfig)
	publicHandler := observability.InstrumentHTTP(publicRoutes, metrics, logger, publicRouteName)
	publicServer := &http.Server{Addr: processConfig.PublicAddr, Handler: publicHandler, ReadHeaderTimeout: 5 * time.Second}
	adminServer := &http.Server{Addr: processConfig.AdminAddr, Handler: adminHandler, ReadHeaderTimeout: 5 * time.Second}

	// RFC-001 §7.2's restart recovery runs once, synchronously, before
	// this process ever reports ready: every item left open by a prior
	// process gets one idempotent interruption marker, with no request
	// accepted (readiness still false) in the meantime.
	recoveryID := uuid.New()
	affected, err := trainingService.Recover(ctx, recoveryID, trainingRecoveryCause)
	if err != nil {
		return fmt.Errorf("training recovery: %w", err)
	}
	recoverOutcome := "clean"
	if len(affected) > 0 {
		recoverOutcome = "interrupted"
	}
	logger.Operation(ctx, slog.LevelInfo, "training_recover", recoverOutcome, "", "")

	metrics.SetReady("api", true)
	defer metrics.SetReady("api", false)

	schedulerCtx, stopScheduler := context.WithCancel(ctx)
	defer stopScheduler()
	go runTrainingScheduler(schedulerCtx, trainingService, logger)

	return serveAPI(ctx, publicServer, adminServer)
}

const trainingRecoveryCause = "server_restart"

// runTrainingScheduler is the durable time-based training work loop
// (RFC-001 §7.2's scheduler tick — due scenario events and hard-level
// offers, internal/training.Service.Tick). It ticks every 500 ms until
// ctx is cancelled; a single failed Tick is logged and retried on the
// next tick rather than treated as fatal (transient DB contention is
// expected and Tick is designed to be safely re-run). RFC-001's "a
// scheduler crash makes the process unhealthy and leads to a restart" is
// satisfied by ordinary Go semantics: an unrecovered panic here brings
// down the whole binary, which docker's restart policy then restarts —
// nothing here should catch and swallow one.
func runTrainingScheduler(ctx context.Context, svc *training.Service, logger observability.Logger) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := svc.Tick(ctx); err != nil && ctx.Err() == nil {
				logger.Operation(ctx, slog.LevelError, "training_scheduler_tick", "error", "", "tick_failed")
			}
		}
	}
}

var errAPITakesNoArgs = errors.New("api subcommand takes no arguments")

// newPublicHandler composes every module's routes onto one API mux,
// mounts that under "/api/" alongside the embedded SPA build under "/"
// (static.go), and wraps the result in the shared public-API middleware
// chain (httpapi.WrapPublic: request id, no-store, Origin check). Each
// module follows the same shape — a pgx store, an application Service
// built on it, HTTP Handlers built on that — composed here and nowhere
// else, matching CLAUDE.md's "composition lives in cmd/emsim" (content
// is the first to follow auth's lead). A future module (training/
// assessment/reporting) adds its own three lines here and calls its own
// Register on apiMux.
func newPublicHandler(pool *pgxpool.Pool, cfg config.API) http.Handler {
	handler, _, _ := newPublicHTTP(pool, cfg)
	return handler
}

// newPublicHTTP also returns the composed *training.Service so runAPI can
// drive it outside the HTTP path: the C6 restart-recovery marker (before
// readiness) and the C5/C6 scheduler tick loop (500 ms, RFC-001 §7.2)
// both need the same Service instance the HTTP handlers use, not a
// second one built from the same pool.
func newPublicHTTP(pool *pgxpool.Pool, cfg config.API) (http.Handler, observability.RouteNamer, *training.Service) {
	apiMux := httpapi.NewMux()

	// contentService is built first: auth.NewService takes it as its
	// ServiceCatalog port (service_code validation, slice-2-plan.md's
	// C5) — content has no dependency on auth, so this order is the only
	// one that avoids a forward reference.
	contentService := content.NewService(contentpg.NewStore(pool), mustSchemaValidator())

	authStore := authpg.NewStore(pool)
	authService := auth.NewService(authStore, auth.NewPasswordIdentityProvider(authStore), cfg.SessionTTL, nil, contentService)
	authhttp.NewHandlers(authService, cfg.CookieSecure).Register(apiMux)
	authhttp.NewAdminHandlers(authService, cfg.CookieSecure).Register(apiMux)

	contenthttp.NewHandlers(contentService, authService, cfg.CookieSecure).Register(apiMux)

	trainingService := newTrainingService(pool, mustTaskEnqueuer(pool))
	traininghttp.NewHandlers(trainingService, authService, cfg.CookieSecure).Register(apiMux)

	root := http.NewServeMux()
	root.Handle("/api/", apiMux)
	root.Handle("/", newSPAHandler(web.DistFS))

	apiRouteName := observability.PatternRouteNamer(apiMux)
	routeName := func(r *http.Request) string {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			return apiRouteName(r)
		}
		return "spa"
	}
	return httpapi.WrapPublic(root), routeName, trainingService
}

// mustSchemaValidator compiles the embedded scenario/scenario-file JSON
// Schemas once per process. A failure here can only mean the embedded
// contracts themselves are malformed — a build-time invariant, not a
// runtime condition to recover from (internal/auth/password.go's
// mustHashDummy is the same pattern for its own always-succeeds-in-
// practice precomputation).
func mustSchemaValidator() *schema.Validator {
	validator, err := schema.New()
	if err != nil {
		panic("emsim: failed to compile embedded scenario schemas: " + err.Error())
	}
	return validator
}

// mustTaskEnqueuer builds the api process's own *tasks.Store purely to
// enqueue (training.Stop's KindLessonClose) — it never claims, and no
// worker pool runs in this process. Its Registry is built by the exact
// same registerKinds the worker calls (worker_composition.go), so the
// Spec (priority/max_attempts) EnqueueTx reads is guaranteed identical
// to what the worker will later run the task under. A failure here can
// only mean a malformed Spec constant — a build-time invariant, the same
// class of always-succeeds-in-practice precomputation as
// mustSchemaValidator above.
func mustTaskEnqueuer(pool *pgxpool.Pool) *tasks.Store {
	registry, err := tasks.NewRegistry(tasks.DefaultPolicy())
	if err != nil {
		panic("emsim: invalid task queue policy: " + err.Error())
	}
	if err := registerKinds(registry); err != nil {
		panic("emsim: invalid task kind registration: " + err.Error())
	}
	return tasks.NewStore(pool, registry)
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
