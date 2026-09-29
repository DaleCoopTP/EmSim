//go:build integration

package integration_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

type usageResponse struct {
	Days []struct {
		Day         string `json:"day"`
		Logins      int    `json:"logins"`
		ActiveUsers int    `json:"active_users"`
		CallerReply int    `json:"caller_replies"`
	} `json:"days"`
	Totals struct {
		Logins      int `json:"logins"`
		ActiveUsers int `json:"active_users"`
		CallerReply int `json:"caller_replies"`
	} `json:"totals"`
	ByRole []struct {
		Role   string `json:"role"`
		Logins int    `json:"logins"`
	} `json:"by_role"`
}

// TestAdminUsageStatistics (ADR-038): the usage report is a per-day series
// of anonymous counters over a whole-UTC-day period, counts logins and
// distinct active users from the audit log and finished caller replies from
// tasks, keeps out anything before the period, rejects an unbounded or
// reversed period, and is for the admin only.
func TestAdminUsageStatistics(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	f := newAdminFixture(t, ctx)
	admin := f.admin(t)
	f.createUser(t, admin, "usage-instr", "instructor-password-1", "instructor")
	instructor := f.login(t, "usage-instr", "instructor-password-1")
	f.login(t, "usage-instr", "instructor-password-1")

	if _, err := f.pool.Exec(ctx, `
		INSERT INTO tasks (id, kind, scope_type, dedup_key, payload, status, attempts, max_attempts, lease_token, terminal_worker, terminal_at)
		VALUES (gen_random_uuid(), 'caller.reply', 'item', 'usage:a', '{}', 'done', 1, 3, 1, 'w', now()),
		       (gen_random_uuid(), 'caller.reply', 'item', 'usage:old', '{}', 'done', 1, 3, 1, 'w', now() - interval '40 days')`); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/api/v1/admin/usage", "/api/v1/admin/usage.csv"} {
		if response := jsonRequest(t, ctx, instructor, f.baseURL, http.MethodGet, path, nil, nil); response.StatusCode != http.StatusForbidden {
			t.Fatalf("instructor GET %s = %d, want 403", path, response.StatusCode)
		}
	}
	for _, query := range []string{"?from=2026-02-01&to=2026-01-01", "?from=2020-01-01&to=2026-01-01", "?from=nonsense"} {
		if response := jsonRequest(t, ctx, admin, f.baseURL, http.MethodGet, "/api/v1/admin/usage"+query, nil, nil); response.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("period %q = %d, want 422", query, response.StatusCode)
		}
	}

	var usage usageResponse
	if response := jsonRequest(t, ctx, admin, f.baseURL, http.MethodGet, "/api/v1/admin/usage", nil, &usage); response.StatusCode != http.StatusOK {
		t.Fatalf("GET usage = %d", response.StatusCode)
	}
	if len(usage.Days) != 30 {
		t.Fatalf("default period has %d days, want 30", len(usage.Days))
	}
	today := time.Now().UTC().Format("2006-01-02")
	last := usage.Days[len(usage.Days)-1]
	if last.Day != today {
		t.Fatalf("last day = %s, want %s", last.Day, today)
	}
	// admin (once, via f.admin above and this request's own session) plus the instructor twice.
	if usage.Totals.Logins < 3 || last.Logins != usage.Totals.Logins {
		t.Fatalf("logins: today %d, total %d", last.Logins, usage.Totals.Logins)
	}
	if usage.Totals.ActiveUsers < 2 {
		t.Fatalf("active users = %d, want at least admin and instructor", usage.Totals.ActiveUsers)
	}
	if usage.Totals.CallerReply != 1 {
		t.Fatalf("caller replies = %d (the 40-day-old one is outside the period), want 1", usage.Totals.CallerReply)
	}
	roles := map[string]int{}
	for _, r := range usage.ByRole {
		roles[r.Role] = r.Logins
	}
	if roles["instructor"] != 2 {
		t.Fatalf("instructor logins by role = %+v, want 2", roles)
	}

	var wide usageResponse
	from := time.Now().AddDate(0, 0, -60).UTC().Format("2006-01-02")
	jsonRequest(t, ctx, admin, f.baseURL, http.MethodGet, "/api/v1/admin/usage?from="+from, nil, &wide)
	if wide.Totals.CallerReply != 2 {
		t.Fatalf("wide period caller replies = %d, want 2", wide.Totals.CallerReply)
	}

	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.baseURL+"/api/v1/admin/usage.csv", nil)
	response, err := admin.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/csv") {
		t.Fatalf("csv = %d %s", response.StatusCode, response.Header.Get("Content-Type"))
	}
}
