//go:build integration

package integration_test

import (
	"context"
	"encoding/csv"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type failuresResponse struct {
	Tasks []struct {
		Kind      string `json:"kind"`
		ErrorCode string `json:"error_code"`
		Count     int    `json:"count"`
	} `json:"tasks"`
	Restarts []struct {
		Key   string
		Count int
	} `json:"restarts"`
	RejectedLogins []struct {
		Key   string `json:"key"`
		Count int    `json:"count"`
	} `json:"rejected_logins"`
	AuditedErrors []struct {
		Key   string `json:"key"`
		Count int    `json:"count"`
	} `json:"audited_errors"`
	ServerErrors struct {
		Count int `json:"count"`
	} `json:"server_errors"`
}

// TestAdminFailuresReport (ADR-038): the report sums up failed tasks by
// kind and error code, rejected logins by cause, audited errors and
// restarts within the period, shows nothing outside it, keeps task
// payloads out, and is for the admin only; the CSV carries the same rows.
func TestAdminFailuresReport(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	f := newAdminFixture(t, ctx)
	admin := f.admin(t)
	f.createUser(t, admin, "fail-instr", "instructor-password-1", "instructor")
	instructor := f.login(t, "fail-instr", "instructor-password-1")

	for i := 0; i < 2; i++ {
		f.tryLogin(t, "fail-instr", "wrong-password-xx")
	}
	f.tryLogin(t, "nobody-here", "wrong-password-xx")
	for _, sql := range []string{
		`INSERT INTO tasks (id, kind, scope_type, dedup_key, payload, status, attempts, max_attempts, lease_token, terminal_worker, last_error_code, terminal_at)
		 VALUES (gen_random_uuid(), 'assessment.evaluate', 'item', 'fail:a', '{"note":"do-not-leak"}', 'dead_letter', 3, 3, 1, 'w', 'judge_unavailable', now()),
		        (gen_random_uuid(), 'assessment.evaluate', 'item', 'fail:b', '{}', 'failed', 1, 3, 1, 'w', 'judge_unavailable', now()),
		        (gen_random_uuid(), 'backup.run', 'system', 'fail:c', '{}', 'failed', 1, 3, 1, 'w', 'backup_failed', now()),
		        (gen_random_uuid(), 'backup.run', 'system', 'fail:old', '{}', 'failed', 1, 3, 1, 'w', 'ancient', now() - interval '30 days')`,
		`INSERT INTO audit_log (actor_role, action, resource_type, outcome) VALUES
		   ('system', 'backup.run', 'backup', 'error'), ('system', 'backup.run', 'backup', 'error'),
		   ('system', 'training.recover', 'lesson', 'ok')`,
		`INSERT INTO audit_log (at, actor_role, action, resource_type, outcome) VALUES (now() - interval '30 days', 'system', 'audit.prune', 'audit_log', 'error')`,
	} {
		if _, err := f.pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}

	for _, path := range []string{"/api/v1/admin/failures", "/api/v1/admin/failures.csv"} {
		if response := jsonRequest(t, ctx, instructor, f.baseURL, http.MethodGet, path, nil, nil); response.StatusCode != http.StatusForbidden {
			t.Fatalf("instructor GET %s = %d, want 403", path, response.StatusCode)
		}
	}
	if response := jsonRequest(t, ctx, admin, f.baseURL, http.MethodGet, "/api/v1/admin/failures?from=2026-01-01T00:00:00Z&to=2025-01-01T00:00:00Z", nil, nil); response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("reversed period = %d, want 422", response.StatusCode)
	}

	var report failuresResponse
	if response := jsonRequest(t, ctx, admin, f.baseURL, http.MethodGet, "/api/v1/admin/failures", nil, &report); response.StatusCode != http.StatusOK {
		t.Fatalf("GET failures = %d", response.StatusCode)
	}
	taskCounts := map[string]int{}
	for _, task := range report.Tasks {
		taskCounts[task.Kind+"/"+task.ErrorCode] = task.Count
	}
	if taskCounts["assessment.evaluate/judge_unavailable"] != 2 || taskCounts["backup.run/backup_failed"] != 1 || len(taskCounts) != 2 {
		t.Fatalf("task groups = %+v (the 30-day-old failure must be outside the default period)", taskCounts)
	}
	if len(report.RejectedLogins) == 0 || report.RejectedLogins[0].Count != 3 {
		t.Fatalf("rejected logins = %+v, want one group of 3", report.RejectedLogins)
	}
	if len(report.AuditedErrors) != 1 || report.AuditedErrors[0].Key != "backup.run" || report.AuditedErrors[0].Count != 2 {
		t.Fatalf("audited errors = %+v, want backup.run x2 only", report.AuditedErrors)
	}
	if len(report.Restarts) != 1 || report.Restarts[0].Count != 1 {
		t.Fatalf("restarts = %+v", report.Restarts)
	}

	// A wide period brings the old rows in.
	var wide failuresResponse
	jsonRequest(t, ctx, admin, f.baseURL, http.MethodGet, "/api/v1/admin/failures?from="+time.Now().AddDate(0, 0, -60).UTC().Format(time.RFC3339), nil, &wide)
	if len(wide.Tasks) != 3 || len(wide.AuditedErrors) != 2 {
		t.Fatalf("wide period: tasks=%d audited=%d, want 3 and 2", len(wide.Tasks), len(wide.AuditedErrors))
	}

	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.baseURL+"/api/v1/admin/failures.csv", nil)
	response, err := admin.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if strings.Contains(string(raw), "do-not-leak") {
		t.Fatalf("csv leaks a task payload: %s", raw)
	}
	records, err := csv.NewReader(strings.NewReader(string(raw))).ReadAll()
	if err != nil || len(records) < 5 || records[0][0] != "section" {
		t.Fatalf("csv = %q, %v", raw, err)
	}
	sections := map[string]int{}
	for _, record := range records[1:] {
		sections[record[0]]++
	}
	if sections["tasks"] != 2 || sections["rejected_logins"] != 1 || sections["audited_errors"] != 1 || sections["server_errors"] != 1 {
		t.Fatalf("csv sections = %+v", sections)
	}
}
