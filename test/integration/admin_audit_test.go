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

type auditPageResponse struct {
	Items []struct {
		ID         int64          `json:"id"`
		ActorLogin *string        `json:"actor_login"`
		Action     string         `json:"action"`
		Outcome    string         `json:"outcome"`
		Details    map[string]any `json:"details"`
	} `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

// TestAdminAuditViewAndExport (ADR-038): only the admin reads the audit
// log; it filters, pages by keyset cursor, names actors by login, and the
// CSV export is itself audited.
func TestAdminAuditViewAndExport(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	f := newAdminFixture(t, ctx)
	admin := f.admin(t)
	f.createUser(t, admin, "adm-instr", "instructor-password-1", "instructor")
	instructor := f.login(t, "adm-instr", "instructor-password-1")
	// A rejected login goes to the log too.
	if _, status := f.tryLogin(t, "adm-instr", "wrong-password-xx"); status != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d", status)
	}

	for _, path := range []string{"/api/v1/admin/audit", "/api/v1/admin/audit.csv"} {
		if response := jsonRequest(t, ctx, instructor, f.baseURL, http.MethodGet, path, nil, nil); response.StatusCode != http.StatusForbidden {
			t.Fatalf("instructor GET %s = %d, want 403", path, response.StatusCode)
		}
	}

	var all auditPageResponse
	if response := jsonRequest(t, ctx, admin, f.baseURL, http.MethodGet, "/api/v1/admin/audit?limit=500", nil, &all); response.StatusCode != http.StatusOK {
		t.Fatalf("GET audit = %d", response.StatusCode)
	}
	sawCreate := false
	for _, row := range all.Items {
		if row.Action == "admin.user.create" {
			sawCreate = true
			if row.ActorLogin == nil || *row.ActorLogin != adminFixtureLogin {
				t.Fatalf("actor login of user.create = %v", row.ActorLogin)
			}
		}
	}
	if !sawCreate {
		t.Fatalf("no admin.user.create in %+v", all.Items)
	}

	var admins auditPageResponse
	jsonRequest(t, ctx, admin, f.baseURL, http.MethodGet, "/api/v1/admin/audit?action=admin.&limit=500", nil, &admins)
	if len(admins.Items) == 0 {
		t.Fatal("action prefix filter returned nothing")
	}
	for _, row := range admins.Items {
		if !strings.HasPrefix(row.Action, "admin.") {
			t.Fatalf("filter leaked %q", row.Action)
		}
	}
	var rejected auditPageResponse
	jsonRequest(t, ctx, admin, f.baseURL, http.MethodGet, "/api/v1/admin/audit?outcome=rejected", nil, &rejected)
	if len(rejected.Items) != 1 || rejected.Items[0].Action != "auth.login" {
		t.Fatalf("rejected = %+v", rejected.Items)
	}

	// Keyset paging visits every row once, newest first.
	seen := map[int64]bool{}
	cursor := ""
	for pages := 0; pages < 100; pages++ {
		path := "/api/v1/admin/audit?limit=2"
		if cursor != "" {
			path += "&before=" + cursor
		}
		var page auditPageResponse
		jsonRequest(t, ctx, admin, f.baseURL, http.MethodGet, path, nil, &page)
		for _, row := range page.Items {
			if seen[row.ID] {
				t.Fatalf("row %d seen twice", row.ID)
			}
			seen[row.ID] = true
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(seen) < len(all.Items) {
		t.Fatalf("paging saw %d rows, one read saw %d", len(seen), len(all.Items))
	}

	for _, bad := range []string{"?from=yesterday", "?outcome=maybe", "?action=DROP%20TABLE", "?limit=0", "?before=-3", "?actor_id=nope"} {
		if response := jsonRequest(t, ctx, admin, f.baseURL, http.MethodGet, "/api/v1/admin/audit"+bad, nil, nil); response.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("GET audit%s = %d, want 422", bad, response.StatusCode)
		}
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, f.baseURL+"/api/v1/admin/audit.csv?action=auth.", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := admin.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/csv") {
		t.Fatalf("csv = %d %q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	raw, _ := io.ReadAll(response.Body)
	records, err := csv.NewReader(strings.NewReader(string(raw))).ReadAll()
	if err != nil || len(records) < 2 || records[0][0] != "id" || records[0][5] != "action" {
		t.Fatalf("csv body %q: %v", raw, err)
	}
	for _, record := range records[1:] {
		if !strings.HasPrefix(record[5], "auth.") {
			t.Fatalf("csv leaked %q", record[5])
		}
	}
	var exported int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'admin.audit.export'`).Scan(&exported); err != nil || exported != 1 {
		t.Fatalf("export audit rows = %d, %v", exported, err)
	}
}
