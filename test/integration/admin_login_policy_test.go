//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

type policyUser struct {
	ID                        string  `json:"id"`
	Login                     string  `json:"login"`
	LockedUntil               *string `json:"locked_until"`
	CredentialsChangeRequired bool    `json:"credentials_change_required"`
}

type errorEnvelope struct {
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

// TestAdminLoginPolicy (ADR-038), through a real api: an instructor created
// by the administrator must change the password before anything else and
// the change ends their other session; a trainee is locked after three
// wrong passwords, sees 423 even with the right one, is listed as locked
// and gets back in when the administrator unlocks; the administrator lists
// and ends a user's sessions; every step is audited.
func TestAdminLoginPolicy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	f := newAdminFixture(t, ctx, "PASSWORD_FORCE_CHANGE=instructor", "LOGIN_LOCKOUT_ATTEMPTS=3", "LOGIN_LOCKOUT_DURATION=1h")
	admin := f.admin(t)

	find := func(login string) policyUser {
		t.Helper()
		var list struct {
			Items []policyUser `json:"items"`
		}
		jsonRequest(t, ctx, admin, f.baseURL, http.MethodGet, "/api/v1/admin/users", nil, &list)
		for _, u := range list.Items {
			if u.Login == login {
				return u
			}
		}
		t.Fatalf("user %s not listed", login)
		return policyUser{}
	}

	// --- forced password change
	f.createUser(t, admin, "pol-instr", "temporary-pass-1", "instructor")
	if !find("pol-instr").CredentialsChangeRequired {
		t.Fatal("an administrator-created instructor is not marked for a password change")
	}
	first := f.login(t, "pol-instr", "temporary-pass-1")
	second := f.login(t, "pol-instr", "temporary-pass-1")
	var blocked errorEnvelope
	if response := jsonRequest(t, ctx, first, f.baseURL, http.MethodGet, "/api/v1/lessons", nil, &blocked); response.StatusCode != http.StatusForbidden || blocked.Error.Code != "password_change_required" {
		t.Fatalf("GET /lessons before the change = %d %+v, want 403 password_change_required", response.StatusCode, blocked)
	}
	if response := jsonRequest(t, ctx, first, f.baseURL, http.MethodGet, "/api/v1/me", nil, nil); response.StatusCode != http.StatusOK {
		t.Fatalf("GET /me before the change = %d, want 200", response.StatusCode)
	}
	if response := jsonRequest(t, ctx, first, f.baseURL, http.MethodPost, "/api/v1/me/password", map[string]any{"current_password": "wrong-one", "new_password": "a-much-better-pass"}, nil); response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("change with a wrong current password = %d, want 422", response.StatusCode)
	}
	if response := jsonRequest(t, ctx, first, f.baseURL, http.MethodPost, "/api/v1/me/password", map[string]any{"current_password": "temporary-pass-1", "new_password": "a-much-better-pass"}, nil); response.StatusCode != http.StatusNoContent {
		t.Fatalf("password change = %d, want 204", response.StatusCode)
	}
	if response := jsonRequest(t, ctx, first, f.baseURL, http.MethodGet, "/api/v1/lessons", nil, nil); response.StatusCode != http.StatusOK {
		t.Fatalf("GET /lessons after the change = %d, want 200", response.StatusCode)
	}
	if response := jsonRequest(t, ctx, second, f.baseURL, http.MethodGet, "/api/v1/me", nil, nil); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the other session after the change = %d, want 401", response.StatusCode)
	}
	if find("pol-instr").CredentialsChangeRequired {
		t.Fatal("the flag survived the change")
	}
	f.login(t, "pol-instr", "a-much-better-pass")

	// --- sessions the administrator can list and end
	instructorID := find("pol-instr").ID
	var sessions []map[string]any
	if response := jsonRequest(t, ctx, admin, f.baseURL, http.MethodGet, "/api/v1/admin/users/"+instructorID+"/sessions", nil, &sessions); response.StatusCode != http.StatusOK || len(sessions) < 2 {
		t.Fatalf("sessions = %d %v, want at least two", response.StatusCode, sessions)
	}
	for _, key := range []string{"token", "id", "session_id"} {
		if _, leaked := sessions[0][key]; leaked {
			t.Fatalf("session listing carries %q", key)
		}
	}
	if response := jsonRequest(t, ctx, admin, f.baseURL, http.MethodDelete, "/api/v1/admin/users/"+instructorID+"/sessions", nil, nil); response.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke sessions = %d, want 204", response.StatusCode)
	}
	if response := jsonRequest(t, ctx, first, f.baseURL, http.MethodGet, "/api/v1/me", nil, nil); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("session after revoke = %d, want 401", response.StatusCode)
	}

	// --- lockout
	if response := jsonRequest(t, ctx, admin, f.baseURL, http.MethodPut, "/api/v1/admin/workstations", []map[string]any{{"number": 1, "label": "РМ-01"}}, nil); response.StatusCode != http.StatusOK {
		t.Fatalf("workstations = %d", response.StatusCode)
	}
	f.createUser(t, admin, "pol-trainee", "trainee-password-1", "trainee")
	if find("pol-trainee").CredentialsChangeRequired {
		t.Fatal("a trainee was marked for a password change although only instructors are")
	}
	for i := 0; i < 3; i++ {
		if _, status := f.tryLogin(t, "pol-trainee", "wrong-password-xx"); status != http.StatusUnauthorized {
			t.Fatalf("wrong password %d = %d, want 401", i+1, status)
		}
	}
	if _, status := f.tryLoginResponse(t, "pol-trainee", "trainee-password-1", 1); status != http.StatusLocked {
		t.Fatalf("right password while locked = %d, want 423", status)
	}
	if find("pol-trainee").LockedUntil == nil {
		t.Fatal("the lock is not visible in the user list")
	}
	if response := jsonRequest(t, ctx, admin, f.baseURL, http.MethodPatch, "/api/v1/admin/users/"+find("pol-trainee").ID, map[string]any{"unlock": true}, nil); response.StatusCode != http.StatusOK {
		t.Fatalf("unlock = %d, want 200", response.StatusCode)
	}
	if status := func() int { _, s := f.tryLoginResponse(t, "pol-trainee", "trainee-password-1", 1); return s }(); status != http.StatusOK {
		t.Fatalf("login after unlock = %d, want 200", status)
	}

	for _, action := range []string{"auth.lockout", "admin.user.unlock", "admin.user.sessions_revoke", "auth.password_change"} {
		var n int
		if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = $1`, action).Scan(&n); err != nil || n == 0 {
			t.Fatalf("no audit row for %s (%v)", action, err)
		}
	}
}

// TestAdminUserImport (ADR-038): a CSV table becomes users all-or-nothing
// through the real api; a dry run creates nobody and shows no passwords; a
// file with bad rows is refused with every problem listed by row; the
// generated passwords work and are not in the audit log; only the admin may
// import.
func TestAdminUserImport(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	f := newAdminFixture(t, ctx)
	admin := f.admin(t)
	f.createUser(t, admin, "imp-instr", "instructor-password-1", "instructor")
	instructor := f.login(t, "imp-instr", "instructor-password-1")

	post := func(client *http.Client, query, body string) (int, map[string]any) {
		t.Helper()
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, f.baseURL+"/api/v1/admin/users/import"+query, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "text/csv")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(response.Body).Decode(&out)
		return response.StatusCode, out
	}
	good := "login;full_name;role\nimp-one;Иванов Иван;trainee\nimp-two;Петров Пётр;trainee\n"

	if status, _ := post(instructor, "", good); status != http.StatusForbidden {
		t.Fatalf("instructor import = %d, want 403", status)
	}

	status, body := post(admin, "", "login,full_name,role\nimp-one,A,trainee\nimp-instr,B,trainee\nbad login!,C,trainee\nimp-three,D,boss\n")
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("bad file = %d %v, want 422", status, body)
	}
	errorsList := body["error"].(map[string]any)["details"].(map[string]any)["errors"].([]any)
	if len(errorsList) != 3 {
		t.Fatalf("issues = %v, want three (row 2 taken, row 3 login, row 4 role)", errorsList)
	}
	var created int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE login LIKE 'imp-%' AND login <> 'imp-instr'`).Scan(&created); err != nil || created != 0 {
		t.Fatalf("a refused file created %d users (%v)", created, err)
	}

	status, body = post(admin, "?dry_run=true", good)
	if status != http.StatusOK {
		t.Fatalf("dry run = %d %v", status, body)
	}
	for _, u := range body["users"].([]any) {
		if _, has := u.(map[string]any)["password"]; has {
			t.Fatal("a dry run returned a password")
		}
	}

	status, body = post(admin, "", good)
	if status != http.StatusOK || body["count"].(float64) != 2 {
		t.Fatalf("import = %d %v", status, body)
	}
	passwords := map[string]string{}
	for _, u := range body["users"].([]any) {
		m := u.(map[string]any)
		passwords[m["login"].(string)] = m["password"].(string)
	}
	if len(passwords["imp-one"]) < 14 || passwords["imp-one"] == passwords["imp-two"] {
		t.Fatalf("passwords = %v", passwords)
	}
	if err := f.pool.QueryRow(ctx, `INSERT INTO workstations (id, number, label) VALUES (gen_random_uuid(), 7, 'РМ-07') ON CONFLICT (number) DO UPDATE SET label = 'РМ-07' RETURNING number`).Scan(new(int)); err != nil {
		t.Fatal(err)
	}
	if _, status := f.tryLoginResponse(t, "imp-one", passwords["imp-one"], 7); status != http.StatusOK {
		t.Fatalf("login with the generated password = %d, want 200", status)
	}

	var rows int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'admin.user.import'`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("audit rows = %d (%v), want exactly one", rows, err)
	}
	var leaked int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE details::text LIKE '%' || $1 || '%'`, passwords["imp-one"]).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("a generated password appears in the audit log (%d, %v)", leaked, err)
	}
}
