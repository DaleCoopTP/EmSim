// import.go implements `emsim import services|classifier|scenarios|seed
// --actor <login> <path>` (slice-2-plan.md's C3): a one-shot CLI, meant
// to run between "migrate up" and "api" the same way bootstrap-admin
// does (bootstrap.go), loading the prepared reference data
// internal/content.Validate checks scenario files against. See
// seed/README.md for the operator-facing walkthrough.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"emsim/internal/auth"
	authpg "emsim/internal/auth/postgres"
	"emsim/internal/content"
	contentpg "emsim/internal/content/postgres"
	"emsim/internal/content/schema"
	pgstore "emsim/internal/platform/postgres"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	errImportCommandRequired = errors.New("import subcommand is required: services | classifier | scenarios | seed")
	errImportUnknownCommand  = errors.New("unknown import subcommand")
	errImportActorRequired   = errors.New("--actor <login> is required")
	errImportPathRequired    = errors.New("a file or directory path is required")
	errImportActorNotAdmin   = errors.New("--actor must be an active admin")
	errOrphanServiceCodes    = errors.New("orphan_service_codes: some users.service_code have no matching service; see stdout for the codes")
)

func runImport(ctx context.Context, args []string) error {
	return importRun(ctx, args, os.Getenv("DATABASE_URL"), os.Stdout)
}

func importRun(ctx context.Context, args []string, databaseURL string, stdout io.Writer) error {
	if len(args) == 0 {
		return errImportCommandRequired
	}
	command, rest := args[0], args[1:]
	if command != "services" && command != "classifier" && command != "scenarios" && command != "seed" {
		return errImportUnknownCommand
	}
	actorLogin, path, err := parseImportArgs(rest)
	if err != nil {
		return err
	}
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

	actorID, actorRole, err := resolveImportActor(ctx, pool, actorLogin)
	if err != nil {
		return err
	}

	validator, err := schema.New()
	if err != nil {
		return fmt.Errorf("compile scenario schemas: %w", err)
	}
	contentService := content.NewService(contentpg.NewStore(pool), validator)
	requestID := uuid.NewString()

	switch command {
	case "services":
		return importServicesStep(ctx, contentService, pool, path, actorID, actorRole, requestID, stdout)
	case "classifier":
		return importClassifierStep(ctx, contentService, path, actorID, actorRole, requestID, stdout)
	case "scenarios":
		return importScenariosStep(ctx, contentService, path, actorID, actorRole, requestID, stdout)
	case "seed":
		if err := importServicesStep(ctx, contentService, pool, filepath.Join(path, "services.json"), actorID, actorRole, requestID, stdout); err != nil {
			return err
		}
		if err := importClassifierStep(ctx, contentService, filepath.Join(path, "classifier.json"), actorID, actorRole, requestID, stdout); err != nil {
			return err
		}
		return importScenariosStep(ctx, contentService, filepath.Join(path, "scenarios"), actorID, actorRole, requestID, stdout)
	}
	return errImportUnknownCommand
}

// parseImportArgs reads "--actor <login> <path>" in either argument
// order (both `--actor x path` and `path --actor x` are accepted, since
// there is exactly one flag and one positional argument — no ambiguity).
func parseImportArgs(args []string) (actorLogin, path string, err error) {
	var positionals []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--actor" {
			if i+1 >= len(args) {
				return "", "", errImportActorRequired
			}
			actorLogin = args[i+1]
			i++
			continue
		}
		positionals = append(positionals, args[i])
	}
	if actorLogin == "" {
		return "", "", errImportActorRequired
	}
	if len(positionals) != 1 {
		return "", "", errImportPathRequired
	}
	return actorLogin, positionals[0], nil
}

// resolveImportActor looks up actorLogin and requires it to name an
// active admin (slice-2-plan.md: "Actor: активный admin, иначе ошибка") —
// the same account boundary GroupAdmin enforces over HTTP, checked here
// by hand since a CLI process has no session to authenticate.
func resolveImportActor(ctx context.Context, pool *pgxpool.Pool, login string) (uuid.UUID, string, error) {
	store := authpg.NewStore(pool)
	var user auth.User
	err := store.WithTx(ctx, func(tx pgx.Tx) error {
		u, err := store.UserByLogin(ctx, tx, login)
		user = u
		return err
	})
	if errors.Is(err, auth.ErrNotFound) {
		return uuid.UUID{}, "", errImportActorNotAdmin
	}
	if err != nil {
		return uuid.UUID{}, "", err
	}
	if user.Role != auth.RoleAdmin || !user.Active {
		return uuid.UUID{}, "", errImportActorNotAdmin
	}
	return user.ID, string(user.Role), nil
}

func importServicesStep(ctx context.Context, svc *content.Service, pool *pgxpool.Pool, path string, actorID uuid.UUID, actorRole, requestID string, stdout io.Writer) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	result, err := svc.ImportServices(ctx, f, actorID, actorRole, requestID)
	if err != nil {
		return fmt.Errorf("import services: %w", err)
	}
	fmt.Fprintf(stdout, "services: created=%d unchanged=%d\n", result.Created, result.Unchanged)

	orphans, err := pgstore.OrphanServiceCodes(ctx, pool)
	if err != nil {
		return err
	}
	if len(orphans) > 0 {
		sort.Strings(orphans)
		fmt.Fprintf(stdout, "orphan service codes (users.service_code with no matching service): %v\n", orphans)
		return errOrphanServiceCodes
	}
	if err := pgstore.ValidateUsersServiceCodeFK(ctx, pool); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "users_service_code_fkey: validated")
	return nil
}

func importClassifierStep(ctx context.Context, svc *content.Service, path string, actorID uuid.UUID, actorRole, requestID string, stdout io.Writer) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	result, err := svc.ImportClassifierTypes(ctx, f, actorID, actorRole, requestID)
	if err != nil {
		return fmt.Errorf("import classifier: %w", err)
	}
	fmt.Fprintf(stdout, "classifier: created=%d unchanged=%d\n", result.Created, result.Unchanged)
	return nil
}

func importScenariosStep(ctx context.Context, svc *content.Service, dir string, actorID uuid.UUID, actorRole, requestID string, stdout io.Writer) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read %s: %w", dir, err)
	}
	files := make(map[string]io.Reader, len(entries))
	var opened []*os.File
	defer func() {
		for _, f := range opened {
			_ = f.Close()
		}
	}()
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		f, err := os.Open(filepath.Join(dir, entry.Name()))
		if err != nil {
			return fmt.Errorf("open %s: %w", entry.Name(), err)
		}
		opened = append(opened, f)
		files[entry.Name()] = f
	}
	if len(files) == 0 {
		return fmt.Errorf("%s has no *.json scenario files", dir)
	}

	result, err := svc.ImportScenarios(ctx, files, actorID, actorRole, requestID)
	if err != nil {
		return fmt.Errorf("import scenarios: %w", err)
	}
	fmt.Fprintf(stdout, "scenarios: new_scenarios=%d new_versions=%d unchanged=%d\n",
		result.NewScenarios, result.NewVersions, result.Unchanged)
	return nil
}
