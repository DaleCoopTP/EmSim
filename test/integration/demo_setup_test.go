//go:build integration

package integration_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pgstore "emsim/internal/platform/postgres"
)

// TestDemoSetupIsIdempotent (ADR-033): demo-setup creates workstations
// РМ-01…РМ-N, an instructor and one trainee per workstation spread over
// ДДС района / 03 / 112; a second run with a different password and more
// workstations only adds what is missing and never changes an existing
// account's password or an admin's own workstation.
func TestDemoSetupIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	binary := filepath.Join(t.TempDir(), "emsim")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/emsim")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build emsim: %v\n%s", err, output)
	}
	runBootstrapAdminProcess(t, ctx, binary, databaseURL, "demo-admin", "correct-horse-battery-staple")
	seedDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(seedDir, "services.json"), mustRead(t, "../../seed/services.json"), 0o600); err != nil {
		t.Fatal(err)
	}
	importServices := exec.CommandContext(ctx, binary, "import", "services", "--actor", "demo-admin", filepath.Join(seedDir, "services.json"))
	importServices.Env = append(os.Environ(), "DATABASE_URL="+databaseURL)
	if output, err := importServices.CombinedOutput(); err != nil {
		t.Fatalf("import services: %v\n%s", err, output)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO workstations (id, number, label, active) VALUES (gen_random_uuid(), 40, 'Учебный класс 40', true)`); err != nil {
		t.Fatal(err)
	}
	demo := func(password, workstations string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, binary, "demo-setup", "--actor", "demo-admin")
		cmd.Env = append(os.Environ(), "DATABASE_URL="+databaseURL, "DEMO_PASSWORD="+password, "DEMO_WORKSTATIONS="+workstations)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("demo-setup: %v\n%s", err, output)
		}
		return string(output)
	}

	first := demo("demo-password-1", "3")
	if !strings.Contains(first, "trainee03") || !strings.Contains(first, "created") {
		t.Fatalf("first run output:\n%s", first)
	}
	var hashBefore string
	if err := pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE login = 'trainee01'`).Scan(&hashBefore); err != nil {
		t.Fatal(err)
	}
	second := demo("another-password", "4")
	if !strings.Contains(second, "exists, left unchanged") {
		t.Fatalf("second run output:\n%s", second)
	}
	var hashAfter string
	var users, trainees, district, ambulance, noService, activeWorkstations int
	if err := pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE login = 'trainee01'`).Scan(&hashAfter); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE login LIKE 'trainee%' OR login = 'instructor'),
		count(*) FILTER (WHERE role = 'trainee'),
		count(*) FILTER (WHERE service_code = 'dds_district_chertanovo'),
		count(*) FILTER (WHERE service_code = 'dds_ambulance_03'),
		count(*) FILTER (WHERE role = 'trainee' AND service_code IS NULL)
		FROM users`).Scan(&users, &trainees, &district, &ambulance, &noService); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM workstations WHERE active`).Scan(&activeWorkstations); err != nil {
		t.Fatal(err)
	}
	if hashAfter != hashBefore {
		t.Fatal("a second run changed an existing password")
	}
	if users != 5 || trainees != 4 || district != 2 || ambulance != 1 || noService != 1 || activeWorkstations != 5 {
		t.Fatalf("users=%d trainees=%d district=%d ambulance=%d 112=%d workstations=%d", users, trainees, district, ambulance, noService, activeWorkstations)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
