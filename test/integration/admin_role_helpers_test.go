//go:build integration

package integration_test

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	pgstore "emsim/internal/platform/postgres"

	"github.com/jackc/pgx/v5/pgxpool"
)

// adminFixture is a real api process over a fresh PostgreSQL with one
// bootstrapped admin, the base of every ADR-038 integration test. A test
// needing a worker starts it itself (startWorkerProcess) with the fixture's
// binary, database and blob root.
type adminFixture struct {
	ctx         context.Context
	binary      string
	databaseURL string
	pool        *pgxpool.Pool
	baseURL     string
	blobRoot    string
	api         *apiProcess
}

const (
	adminFixtureLogin    = "adm-admin"
	adminFixturePassword = "correct-horse-battery-staple"
)

// newAdminFixture starts the api with extraEnv appended to its
// environment (a later entry wins).
func newAdminFixture(t *testing.T, ctx context.Context, extraEnv ...string) *adminFixture {
	t.Helper()
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
	runBootstrapAdminProcess(t, ctx, binary, databaseURL, adminFixtureLogin, adminFixturePassword)
	publicAddr, adminAddr := freeAddr(t), freeAddr(t)
	blobRoot := t.TempDir()
	api := startAPIProcessWithEnv(t, binary, databaseURL, publicAddr, adminAddr, extraEnv, blobRoot)
	t.Cleanup(func() { api.stop(t) })
	return &adminFixture{ctx: ctx, binary: binary, databaseURL: databaseURL, pool: pool, baseURL: "http://" + publicAddr, blobRoot: blobRoot, api: api}
}

// tryLogin returns a cookie client and the login response status.
func (f *adminFixture) tryLogin(t *testing.T, name, password string) (*http.Client, int) {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	response := jsonRequest(t, f.ctx, client, f.baseURL, http.MethodPost, "/api/v1/auth/login", map[string]any{"login": name, "password": password}, nil)
	return client, response.StatusCode
}

func (f *adminFixture) login(t *testing.T, name, password string) *http.Client {
	t.Helper()
	client, status := f.tryLogin(t, name, password)
	if status != http.StatusOK {
		t.Fatalf("login %s = %d", name, status)
	}
	return client
}

func (f *adminFixture) admin(t *testing.T) *http.Client {
	t.Helper()
	return f.login(t, adminFixtureLogin, adminFixturePassword)
}

// createUser makes a user through the admin API and fails unless it is 201.
func (f *adminFixture) createUser(t *testing.T, admin *http.Client, login, password, role string) {
	t.Helper()
	if response := jsonRequest(t, f.ctx, admin, f.baseURL, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"login": login, "password": password, "full_name": "Test " + login, "role": role,
	}, nil); response.StatusCode != http.StatusCreated {
		t.Fatalf("create %s = %d", login, response.StatusCode)
	}
}
