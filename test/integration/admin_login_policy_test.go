//go:build integration

package integration_test

import (
	"context"
	"net/http"
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
