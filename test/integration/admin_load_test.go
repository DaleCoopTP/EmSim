//go:build integration

package integration_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type adminLoadResponse struct {
	Build  string `json:"build"`
	Models struct {
		STT *struct {
			Status string `json:"status"`
			Model  string `json:"model"`
		} `json:"stt"`
	} `json:"models"`
	Load struct {
		WindowSeconds int `json:"window_seconds"`
		Requests      struct {
			Requests int      `json:"requests"`
			P95      *float64 `json:"p95_ms"`
		} `json:"requests"`
		Commands struct {
			Requests int `json:"requests"`
		} `json:"commands"`
		ActiveSessions *int `json:"active_sessions"`
		RunningLessons *int `json:"running_lessons"`
		OpenItems      *int `json:"open_items"`
		Host           struct {
			CPUs int `json:"cpus"`
		} `json:"host"`
	} `json:"load"`
}

// TestAdminStatusLoadPanel (ADR-038): the status screen's load panel
// reports recent traffic, active sessions and training activity, and the
// api's own probe reports the speech engine when dictation runs on it.
func TestAdminStatusLoadPanel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	whisper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer whisper.Close()
	f := newAdminFixture(t, ctx, "DICTATION=whisper", "STT_URL="+whisper.URL, "STT_MODEL=ggml-test")
	admin := f.admin(t)
	f.createUser(t, admin, "load-instr", "instructor-password-1", "instructor")
	instructor := f.login(t, "load-instr", "instructor-password-1")
	jsonRequest(t, ctx, instructor, f.baseURL, http.MethodGet, "/api/v1/me", nil, nil)

	var body adminLoadResponse
	deadline := time.Now().Add(15 * time.Second)
	for {
		body = adminLoadResponse{}
		if response := jsonRequest(t, ctx, admin, f.baseURL, http.MethodGet, "/api/v1/admin/status", nil, &body); response.StatusCode != http.StatusOK {
			t.Fatalf("GET status = %d", response.StatusCode)
		}
		if body.Models.STT != nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if body.Build == "" {
		t.Fatal("build is empty")
	}
	if body.Models.STT == nil || body.Models.STT.Status != "ok" || body.Models.STT.Model != "ggml-test" {
		t.Fatalf("stt model status = %+v", body.Models.STT)
	}
	load := body.Load
	if load.WindowSeconds != 300 || load.Requests.Requests < 3 || load.Requests.P95 == nil {
		t.Fatalf("request window = %+v", load)
	}
	if load.Commands.Requests != 0 {
		t.Fatalf("commands = %d, want 0", load.Commands.Requests)
	}
	if load.ActiveSessions == nil || *load.ActiveSessions < 2 {
		t.Fatalf("active sessions = %v, want at least 2", load.ActiveSessions)
	}
	if load.RunningLessons == nil || *load.RunningLessons != 0 || load.OpenItems == nil || *load.OpenItems != 0 {
		t.Fatalf("activity = %v/%v, want 0/0", load.RunningLessons, load.OpenItems)
	}
	if load.Host.CPUs < 1 {
		t.Fatalf("host cpus = %d", load.Host.CPUs)
	}

	// An unreachable engine turns the model red.
	whisper.Close()
	deadline = time.Now().Add(45 * time.Second)
	for {
		body = adminLoadResponse{}
		jsonRequest(t, ctx, admin, f.baseURL, http.MethodGet, "/api/v1/admin/status", nil, &body)
		if body.Models.STT != nil && body.Models.STT.Status == "unavailable" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("stt did not turn unavailable: %+v", body.Models.STT)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
