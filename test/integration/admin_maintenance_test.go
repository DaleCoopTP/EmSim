//go:build integration

package integration_test

import (
	"net/http"
	"testing"
)

// TestMaintenanceModeBlocksNewLessonStarts (ADR-038): with maintenance
// mode on, a new lesson cannot start (409 maintenance_mode) while a lesson
// already running — and its trainee — carry on untouched; only the admin
// flips the switch, everyone signed in can read it, and both flips are
// audited.
func TestMaintenanceModeBlocksNewLessonStarts(t *testing.T) {
	f := setupTrainingE2E(t)
	admin := newCookieClient(t)
	loginAdmin(t, f, admin)
	if response := jsonRequest(t, f.ctx, admin, f.baseURL, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"login": "mnt-instructor", "password": "correct-horse-battery-staple", "full_name": "Инструктор", "role": "instructor",
	}, nil); response.StatusCode != http.StatusCreated {
		t.Fatalf("create instructor = %d", response.StatusCode)
	}
	instructor := newCookieClient(t)
	if response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodPost, "/api/v1/auth/login",
		map[string]any{"login": "mnt-instructor", "password": "correct-horse-battery-staple"}, nil); response.StatusCode != http.StatusOK {
		t.Fatalf("instructor login = %d", response.StatusCode)
	}
	versionID := approvedVersionID(t, f, instructor, "pilot-tree-01")
	provisionWorkstations(t, f, admin, map[string]any{"number": 1, "label": "a"}, map[string]any{"number": 2, "label": "b"})
	traineeA := createTrainee(t, f, admin, "mnt-trainee-a", 1)
	traineeB := createTrainee(t, f, admin, "mnt-trainee-b", 2)
	userA, userB := currentUserID(t, f, traineeA), currentUserID(t, f, traineeB)

	draft := func(title, userID string, workstation int) lessonResponse {
		t.Helper()
		var lesson lessonResponse
		if response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodPost, "/api/v1/lessons", map[string]any{
			"exercise_type": "dds_processing", "title": title, "mode": "training", "level": "easy",
		}, &lesson); response.StatusCode != http.StatusCreated {
			t.Fatalf("create lesson = %d", response.StatusCode)
		}
		if response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodPut, "/api/v1/lessons/"+lesson.ID+"/assignments",
			[]map[string]any{{"workstation_no": workstation, "user_id": userID, "scenario_version_ids": []string{versionID}}}, nil); response.StatusCode != http.StatusOK {
			t.Fatalf("assign = %d", response.StatusCode)
		}
		return lesson
	}
	first, second := draft("До обслуживания", userA, 1), draft("Во время обслуживания", userB, 2)
	if response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodPost, "/api/v1/lessons/"+first.ID+"/start", nil, nil); response.StatusCode != http.StatusOK {
		t.Fatalf("start before maintenance = %d", response.StatusCode)
	}

	type systemState struct {
		Maintenance struct {
			Enabled bool   `json:"enabled"`
			Reason  string `json:"reason"`
		} `json:"maintenance"`
	}
	var state systemState
	jsonRequest(t, f.ctx, traineeA, f.baseURL, http.MethodGet, "/api/v1/system", nil, &state)
	if state.Maintenance.Enabled {
		t.Fatal("maintenance is on by default")
	}

	if response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodPut, "/api/v1/admin/maintenance", map[string]any{"enabled": true}, nil); response.StatusCode != http.StatusForbidden {
		t.Fatalf("instructor PUT maintenance = %d, want 403", response.StatusCode)
	}
	if response := jsonRequest(t, f.ctx, admin, f.baseURL, http.MethodPut, "/api/v1/admin/maintenance", map[string]any{"enabled": true, "reason": string(make([]rune, 201))}, nil); response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("overlong reason = %d, want 422", response.StatusCode)
	}
	if response := jsonRequest(t, f.ctx, admin, f.baseURL, http.MethodPut, "/api/v1/admin/maintenance", map[string]any{"enabled": true, "reason": "Обновление до 15:00"}, nil); response.StatusCode != http.StatusOK {
		t.Fatalf("enable maintenance = %d", response.StatusCode)
	}

	// Everyone signed in sees the banner text.
	for name, client := range map[string]*http.Client{"trainee": traineeA, "instructor": instructor, "admin": admin} {
		state = systemState{}
		if response := jsonRequest(t, f.ctx, client, f.baseURL, http.MethodGet, "/api/v1/system", nil, &state); response.StatusCode != http.StatusOK ||
			!state.Maintenance.Enabled || state.Maintenance.Reason != "Обновление до 15:00" {
			t.Fatalf("%s GET /system = %d %+v", name, response.StatusCode, state)
		}
	}
	if response := jsonRequest(t, f.ctx, newCookieClient(t), f.baseURL, http.MethodGet, "/api/v1/system", nil, nil); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous GET /system = %d, want 401", response.StatusCode)
	}

	// A new start is refused with its own code...
	var refused struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodPost, "/api/v1/lessons/"+second.ID+"/start", nil, &refused); response.StatusCode != http.StatusConflict || refused.Error.Code != "maintenance_mode" {
		t.Fatalf("start during maintenance = %d %+v, want 409 maintenance_mode", response.StatusCode, refused)
	}
	// ...while the lesson already running is untouched: a repeat start is
	// the same idempotent 200, and its trainee still reads and acts on the item.
	if response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodPost, "/api/v1/lessons/"+first.ID+"/start", nil, nil); response.StatusCode != http.StatusOK {
		t.Fatalf("repeat start of the running lesson = %d", response.StatusCode)
	}
	itemID := currentItemID(t, f, traineeA)
	if resp, receipt := sendCommand(t, f, traineeA, itemID, randomCommandID(), 0, "open", map[string]any{}); resp.StatusCode != http.StatusOK || receipt.Outcome != "applied" {
		t.Fatalf("trainee command during maintenance = %d %+v", resp.StatusCode, receipt)
	}
	// The admin sees the switch on the status screen too.
	var status struct {
		Maintenance struct {
			Enabled bool `json:"enabled"`
		} `json:"maintenance"`
	}
	jsonRequest(t, f.ctx, admin, f.baseURL, http.MethodGet, "/api/v1/admin/status", nil, &status)
	if !status.Maintenance.Enabled {
		t.Fatal("status does not show maintenance mode")
	}

	if response := jsonRequest(t, f.ctx, admin, f.baseURL, http.MethodPut, "/api/v1/admin/maintenance", map[string]any{"enabled": false}, nil); response.StatusCode != http.StatusOK {
		t.Fatalf("disable maintenance = %d", response.StatusCode)
	}
	if response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodPost, "/api/v1/lessons/"+second.ID+"/start", nil, nil); response.StatusCode != http.StatusOK {
		t.Fatalf("start after maintenance = %d", response.StatusCode)
	}

	pool := openTestPool(t, f.ctx, f.databaseURL)
	var flips int
	if err := pool.QueryRow(f.ctx, `SELECT count(*) FROM audit_log WHERE action = 'admin.maintenance.set' AND outcome = 'ok'`).Scan(&flips); err != nil || flips != 2 {
		t.Fatalf("audited maintenance changes = %d, %v, want 2", flips, err)
	}
}
