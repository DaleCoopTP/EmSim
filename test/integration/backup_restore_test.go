//go:build integration

package integration_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"

	"emsim/internal/platform/backup"
	pgstore "emsim/internal/platform/postgres"

	"github.com/jackc/pgx/v5/pgxpool"
)

// skipUnlessClientMatchesServer: a newer pg_restore emits settings an
// older server rejects (e.g. PostgreSQL 17+'s transaction_timeout), so
// the round trip runs only with client tools of the server's own major
// version — what the runtime image ships (postgresql16-client).
func skipUnlessClientMatchesServer(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	var serverNum int
	if err := pool.QueryRow(ctx, "SELECT current_setting('server_version_num')::int").Scan(&serverNum); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"pg_dump", "pg_restore"} {
		output, err := exec.CommandContext(ctx, tool, "--version").Output()
		if err != nil {
			t.Skipf("%s --version: %v", tool, err)
		}
		match := regexp.MustCompile(`\) (\d+)`).FindSubmatch(output)
		if match == nil {
			t.Skipf("cannot parse %s version %q", tool, output)
		}
		if major, _ := strconv.Atoi(string(match[1])); major != serverNum/10000 {
			t.Skipf("%s is version %d, server is %d: run with matching client tools (the runtime image has them)", tool, major, serverNum/10000)
		}
	}
}

// TestBackupRestoreRoundTrip (ADR-033): a copy restored into a database
// that changed afterwards brings back exactly the rows and blob bytes the
// copy held, and the restored database is ready for this build. Needs
// pg_dump/pg_restore on the host (the runtime image ships them).
func TestBackupRestoreRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("pg_dump"); err != nil {
		t.Skip("pg_dump is not installed on this host")
	}
	if _, err := exec.LookPath("pg_restore"); err != nil {
		t.Skip("pg_restore is not installed on this host")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	skipUnlessClientMatchesServer(t, ctx, pool)
	blobRoot, backupDir := t.TempDir(), t.TempDir()
	blobPath := filepath.Join(blobRoot, "ab", "cdef0123")
	if err := os.MkdirAll(filepath.Dir(blobPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blobPath, []byte("voice bytes"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO workstations (id, number, label) VALUES (gen_random_uuid(), 1, 'РМ-01'), (gen_random_uuid(), 2, 'РМ-02');
		INSERT INTO audit_log (action, resource_type, outcome) VALUES ('test.before', 'user', 'ok');
	`); err != nil {
		t.Fatal(err)
	}
	counts := func() [2]int {
		t.Helper()
		var c [2]int
		if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM workstations), (SELECT count(*) FROM audit_log WHERE action LIKE 'test.%')`).Scan(&c[0], &c[1]); err != nil {
			t.Fatal(err)
		}
		return c
	}
	before := counts()

	cfg := backup.Config{Dir: backupDir, BlobRoot: blobRoot, DatabaseURL: databaseURL, Keep: 14}
	made, err := backup.Run(ctx, cfg, pgstore.ExpectedSchemaVersion, time.Now())
	if err != nil {
		t.Fatalf("backup.Run: %v", err)
	}
	copies, err := backup.List(backupDir)
	if err != nil || len(copies) != 1 || copies[0].Name != made.Name {
		t.Fatalf("List = %+v, %v", copies, err)
	}

	// Change everything the copy covers.
	if _, err := pool.Exec(ctx, `
		DELETE FROM workstations WHERE number = 2;
		INSERT INTO audit_log (action, resource_type, outcome) VALUES ('test.after', 'user', 'ok');
	`); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(blobPath); err != nil {
		t.Fatal(err)
	}
	pool.Close()

	restorePool := openTestPool(t, ctx, databaseURL)
	manifest, err := backup.Restore(ctx, cfg, filepath.Join(backupDir, made.Name), pgstore.ExpectedSchemaVersion, func(ctx context.Context) error {
		_, err := restorePool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public")
		return err
	})
	if err != nil {
		t.Fatalf("backup.Restore: %v", err)
	}
	if manifest.SchemaVersion != pgstore.ExpectedSchemaVersion || manifest.BlobCount != 1 {
		t.Fatalf("manifest = %+v", manifest)
	}
	pool = restorePool
	if after := counts(); after != before {
		t.Fatalf("counts after restore = %v, want %v", after, before)
	}
	if got, err := os.ReadFile(blobPath); err != nil || string(got) != "voice bytes" {
		t.Fatalf("blob after restore = %q, %v", got, err)
	}
	if ready, err := pgstore.Ready(ctx, pool); err != nil || !ready {
		t.Fatalf("restored database ready = %v, %v", ready, err)
	}
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up after restore: %v", err)
	}
}
