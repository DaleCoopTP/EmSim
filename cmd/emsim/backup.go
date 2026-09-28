// backup and restore (ADR-033) run outside the worker: `emsim backup`
// makes a copy on demand, e.g. right before an upgrade, and `emsim
// restore --yes <dir>` replaces the database and blob store with one —
// only with api and worker stopped (scripts/restore.sh does that). Both
// read DATABASE_URL, BLOB_ROOT, BACKUP_DIR and BACKUP_KEEP from the
// environment, the same variables the worker's backup.run uses.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"emsim/internal/platform/audit"
	"emsim/internal/platform/backup"
	pgstore "emsim/internal/platform/postgres"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	errBackupUsage         = errors.New("usage: emsim backup")
	errRestoreUsage        = errors.New("usage: emsim restore --yes <backup directory>")
	errRestoreNotConfirmed = errors.New("restore replaces all data: pass --yes")
)

func runBackup(ctx context.Context, args []string, out io.Writer) error {
	if len(args) != 0 {
		return errBackupUsage
	}
	cfg, err := backupConfigFromEnvironment()
	if err != nil {
		return err
	}
	pool, err := openReadyDatabase(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	version, err := pgstore.CurrentVersion(ctx, pool)
	if err != nil {
		return err
	}
	made, err := backup.Run(ctx, cfg, version, time.Now())
	if err != nil {
		return err
	}
	if err := recordSystemAudit(ctx, pool, "backup.run", "backup", map[string]any{"name": made.Name, "size_bytes": made.SizeBytes, "source": "cli"}); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "backup %s (%d bytes)\n", made.Name, made.SizeBytes)
	return err
}

func runRestore(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("restore", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	yes := flags.Bool("yes", false, "confirm that all current data is replaced")
	if err := flags.Parse(args); err != nil || flags.NArg() != 1 {
		return errRestoreUsage
	}
	if !*yes {
		return errRestoreNotConfirmed
	}
	cfg, err := backupConfigFromEnvironment()
	if err != nil {
		return err
	}
	pool, err := pgstore.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return errors.New("database connection is unavailable")
	}
	defer pool.Close()
	manifest, err := backup.Restore(ctx, cfg, flags.Arg(0), pgstore.ExpectedSchemaVersion, func(ctx context.Context) error {
		// An empty public schema, so a copy made on an older schema
		// restores as it was and `emsim migrate up` then moves it forward.
		_, err := pool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public")
		return err
	})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "restored copy of %s (schema %d, %d blobs); run `emsim migrate up` before starting api and worker\n",
		manifest.CreatedAt.Format(time.RFC3339), manifest.SchemaVersion, manifest.BlobCount)
	return err
}

func backupConfigFromEnvironment() (backup.Config, error) {
	keep := 14
	if raw := os.Getenv("BACKUP_KEEP"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			return backup.Config{}, backup.ErrNotConfigured
		}
		keep = parsed
	}
	cfg := backup.Config{
		Dir: os.Getenv("BACKUP_DIR"), BlobRoot: os.Getenv("BLOB_ROOT"),
		DatabaseURL: os.Getenv("DATABASE_URL"), Keep: keep,
	}
	if cfg.DatabaseURL == "" || cfg.BlobRoot == "" {
		return backup.Config{}, backup.ErrNotConfigured
	}
	return cfg, nil
}

func recordSystemAudit(ctx context.Context, pool *pgxpool.Pool, action, resourceType string, details map[string]any) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return errors.New("audit transaction failed")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := audit.Record(ctx, tx, audit.Entry{
		ActorRole: "system", Action: action, ResourceType: resourceType, Outcome: audit.OutcomeOK, Details: details,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
