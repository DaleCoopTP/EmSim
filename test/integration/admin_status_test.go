//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pgstore "emsim/internal/platform/postgres"
)

type adminStatusResponse struct {
	DBSchemaVersion       int64                     `json:"db_schema_version"`
	ExpectedSchemaVersion int64                     `json:"expected_schema_version"`
	Tasks                 map[string]map[string]int `json:"tasks"`
	LastBackupAt          *time.Time                `json:"last_backup_at"`
	Backup                struct {
		Enabled bool `json:"enabled"`
		Copies  []struct {
			Name string `json:"name"`
		} `json:"copies"`
		Runs []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"runs"`
	} `json:"backup"`
	Workers []struct {
		ID string `json:"id"`
	} `json:"workers"`
	FailedTasks []struct {
		Kind          string  `json:"kind"`
		LastErrorCode *string `json:"last_error_code"`
	} `json:"failed_tasks"`
}

// TestAdminStatusAndManualBackupThroughProcesses (ADR-033): real api and
// worker processes. Only the admin reads GET /admin/status, which shows
// schema, queue, workers and failed tasks without any task payload.
// POST /admin/backup is refused until a worker reports a backup
// directory and while a backup is queued; once the worker runs it, the
// copy appears in the status.
func TestAdminStatusAndManualBackupThroughProcesses(t *testing.T) {
	if _, err := exec.LookPath("pg_dump"); err != nil {
		t.Skip("pg_dump is not installed on this host")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	seedServiceFixture(t, ctx, databaseURL, "dds_district")
	pool := openTestPool(t, ctx, databaseURL)
	binary := filepath.Join(t.TempDir(), "emsim")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/emsim")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build emsim: %v\n%s", err, output)
	}
	const adminLogin, adminPassword = "status-admin", "correct-horse-battery-staple"
	runBootstrapAdminProcess(t, ctx, binary, databaseURL, adminLogin, adminPassword)
	publicAddr, adminAddr := freeAddr(t), freeAddr(t)
	blobRoot := t.TempDir()
	api := startAPIProcess(t, binary, databaseURL, publicAddr, adminAddr, blobRoot)
	t.Cleanup(func() { api.stop(t) })
	baseURL := "http://" + publicAddr

	login := func(name, password string) *http.Client {
		t.Helper()
		jar, err := cookiejar.New(nil)
		if err != nil {
			t.Fatal(err)
		}
		client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
		if response := jsonRequest(t, ctx, client, baseURL, http.MethodPost, "/api/v1/auth/login", map[string]any{"login": name, "password": password}, nil); response.StatusCode != http.StatusOK {
			t.Fatalf("login %s = %d", name, response.StatusCode)
		}
		return client
	}
	admin := login(adminLogin, adminPassword)
	if response := jsonRequest(t, ctx, admin, baseURL, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"login": "status-instructor", "password": "instructor-password-1", "full_name": "Instructor", "role": "instructor",
	}, nil); response.StatusCode != http.StatusCreated {
		t.Fatalf("create instructor = %d", response.StatusCode)
	}
	instructor := login("status-instructor", "instructor-password-1")
	for _, request := range []struct{ method, path string }{{http.MethodGet, "/api/v1/admin/status"}, {http.MethodPost, "/api/v1/admin/backup"}} {
		if response := jsonRequest(t, ctx, instructor, baseURL, request.method, request.path, nil, nil); response.StatusCode != http.StatusForbidden {
			t.Fatalf("instructor %s %s = %d, want 403", request.method, request.path, response.StatusCode)
		}
	}

	// A failed task with a payload that must never reach the status body.
	if _, err := pool.Exec(ctx, `INSERT INTO tasks (id, kind, scope_type, dedup_key, payload, status, attempts, max_attempts, lease_token, terminal_worker, last_error_code, terminal_at)
		VALUES (gen_random_uuid(), 'system.noop', 'system', 'status:failed', '{"note":"do-not-leak"}', 'dead_letter', 1, 1, 1, 'w', 'boom', clock_timestamp())`); err != nil {
		t.Fatal(err)
	}

	// No worker yet: nothing has reported a backup directory.
	var refused struct {
		Error struct {
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if response := jsonRequest(t, ctx, admin, baseURL, http.MethodPost, "/api/v1/admin/backup", nil, &refused); response.StatusCode != http.StatusConflict || refused.Error.Details["reason"] != "backup_not_configured" {
		t.Fatalf("backup without worker = %d %+v", response.StatusCode, refused)
	}

	t.Setenv("BACKUP_DIR", t.TempDir())
	// A slot that has always passed: the scheduler queues today's backup
	// as soon as the worker starts.
	t.Setenv("SCHEDULE_TZ", "UTC")
	t.Setenv("BACKUP_AT", "00:00")
	worker := startWorkerProcess(t, binary, databaseURL, "all", "status-worker", blobRoot)
	t.Cleanup(func() { worker.stop(t, false) })
	getStatus := func() (adminStatusResponse, string) {
		t.Helper()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/v1/admin/status", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := admin.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("GET status = %d, %v", response.StatusCode, err)
		}
		var body adminStatusResponse
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		return body, string(raw)
	}
	waitStatus := func(label string, ready func(adminStatusResponse) bool) adminStatusResponse {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for {
			body, _ := getStatus()
			if ready(body) {
				return body
			}
			if time.Now().After(deadline) {
				t.Fatalf("timeout waiting for %s: %+v", label, body)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	body := waitStatus("worker heartbeat", func(b adminStatusResponse) bool { return b.Backup.Enabled && len(b.Workers) == 1 })
	if body.DBSchemaVersion != pgstore.ExpectedSchemaVersion || body.ExpectedSchemaVersion != pgstore.ExpectedSchemaVersion || body.Workers[0].ID != "status-worker" {
		t.Fatalf("status = %+v", body)
	}
	if _, raw := getStatus(); strings.Contains(raw, "do-not-leak") || !strings.Contains(raw, `"last_error_code":"boom"`) {
		t.Fatalf("failed task in status body: %s", raw)
	}

	// The admin retries the dead-lettered task; the running worker then
	// completes it. A done task cannot be retried, nor can an instructor
	// retry anything.
	var failedID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM tasks WHERE dedup_key = 'status:failed'`).Scan(&failedID); err != nil {
		t.Fatal(err)
	}
	if response := jsonRequest(t, ctx, instructor, baseURL, http.MethodPost, "/api/v1/admin/tasks/"+failedID+"/retry", nil, nil); response.StatusCode != http.StatusForbidden {
		t.Fatalf("instructor retry = %d, want 403", response.StatusCode)
	}
	var retried struct {
		Status      string `json:"status"`
		MaxAttempts int    `json:"max_attempts"`
	}
	if response := jsonRequest(t, ctx, admin, baseURL, http.MethodPost, "/api/v1/admin/tasks/"+failedID+"/retry", nil, &retried); response.StatusCode != http.StatusOK || retried.MaxAttempts != 2 {
		t.Fatalf("admin retry = %d %+v", response.StatusCode, retried)
	}
	waitStatus("retried task done", func(b adminStatusResponse) bool {
		return len(b.FailedTasks) == 0 && b.Tasks["system.noop"]["done"] == 1
	})
	refused.Error.Details = nil
	if response := jsonRequest(t, ctx, admin, baseURL, http.MethodPost, "/api/v1/admin/tasks/"+failedID+"/retry", nil, &refused); response.StatusCode != http.StatusConflict || refused.Error.Details["reason"] != "not_retryable" {
		t.Fatalf("retry of done task = %d %+v", response.StatusCode, refused)
	}

	// The scheduler's backup for today runs first; the manual one waits
	// for it to finish.
	waitStatus("scheduled backup done", func(b adminStatusResponse) bool {
		return len(b.Backup.Runs) == 1 && b.Backup.Runs[0].Status == "done"
	})
	var accepted struct {
		TaskID string `json:"task_id"`
	}
	if response := jsonRequest(t, ctx, admin, baseURL, http.MethodPost, "/api/v1/admin/backup", nil, &accepted); response.StatusCode != http.StatusAccepted || accepted.TaskID == "" {
		t.Fatalf("manual backup = %d %+v", response.StatusCode, accepted)
	}
	body = waitStatus("manual backup done", func(b adminStatusResponse) bool {
		return b.LastBackupAt != nil && len(b.Backup.Copies) == 2 && len(b.Backup.Runs) == 2 && b.Backup.Runs[0].Status == "done"
	})
	var audited bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT FROM audit_log a JOIN users u ON u.id = a.actor_id
		WHERE a.action = 'admin.backup.start' AND u.login = $1 AND a.resource_id::text = $2)`, adminLogin, accepted.TaskID).Scan(&audited); err != nil || !audited {
		t.Fatalf("manual backup audit row = %v, %v", audited, err)
	}

	// While one is queued, a second is refused.
	worker.stop(t, false)
	if response := jsonRequest(t, ctx, admin, baseURL, http.MethodPost, "/api/v1/admin/backup", nil, nil); response.StatusCode != http.StatusAccepted {
		t.Fatalf("second backup = %d", response.StatusCode)
	}
	refused.Error.Details = nil
	if response := jsonRequest(t, ctx, admin, baseURL, http.MethodPost, "/api/v1/admin/backup", nil, &refused); response.StatusCode != http.StatusConflict || refused.Error.Details["reason"] != "backup_in_progress" {
		t.Fatalf("backup while queued = %d %+v", response.StatusCode, refused)
	}
}
