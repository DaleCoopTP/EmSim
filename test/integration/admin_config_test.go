//go:build integration

package integration_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type adminConfigResponse struct {
	API struct {
		Version string `json:"version"`
		Params  []struct {
			Group string `json:"group"`
			Env   string `json:"env"`
			Value string `json:"value"`
		} `json:"params"`
	} `json:"api"`
	Workers []struct {
		ID     string `json:"id"`
		Params []struct {
			Env   string `json:"env"`
			Value string `json:"value"`
		} `json:"params"`
	} `json:"workers"`
}

// TestAdminConfigIsReadOnlyAndSecretFree (ADR-038): only the admin reads
// GET /admin/config; the api's and the worker's effective settings appear
// with the variable to change, and a database password or an API key never
// does. There is no write route.
func TestAdminConfigIsReadOnlyAndSecretFree(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	f := newAdminFixture(t, ctx, "SESSION_TTL=90m", "LOG_LEVEL=warn")
	admin := f.admin(t)
	f.createUser(t, admin, "cfg-instr", "instructor-password-1", "instructor")
	instructor := f.login(t, "cfg-instr", "instructor-password-1")

	if response := jsonRequest(t, ctx, instructor, f.baseURL, http.MethodGet, "/api/v1/admin/config", nil, nil); response.StatusCode != http.StatusForbidden {
		t.Fatalf("instructor GET config = %d, want 403", response.StatusCode)
	}
	for _, method := range []string{http.MethodPut, http.MethodPatch, http.MethodPost, http.MethodDelete} {
		if response := jsonRequest(t, ctx, admin, f.baseURL, method, "/api/v1/admin/config", map[string]any{}, nil); response.StatusCode == http.StatusOK || response.StatusCode == http.StatusCreated || response.StatusCode == http.StatusAccepted {
			t.Fatalf("%s /admin/config = %d: configuration must be read-only", method, response.StatusCode)
		}
	}

	t.Setenv("BACKUP_DIR", t.TempDir())
	t.Setenv("LLM_API_KEY", "sk-live-secret-key")
	worker := startWorkerProcess(t, f.binary, f.databaseURL, "all", "cfg-worker", f.blobRoot)
	t.Cleanup(func() { worker.stop(t, false) })

	var body adminConfigResponse
	var raw string
	deadline := time.Now().Add(30 * time.Second)
	for {
		body = adminConfigResponse{}
		response := jsonRequest(t, ctx, admin, f.baseURL, http.MethodGet, "/api/v1/admin/config", nil, &body)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("GET config = %d", response.StatusCode)
		}
		if len(body.Workers) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no worker settings: %+v", body)
		}
		time.Sleep(200 * time.Millisecond)
	}
	values := map[string]string{}
	for _, p := range body.API.Params {
		values[p.Env] = p.Value
	}
	if values["SESSION_TTL"] != "1h30m0s" || values["LOG_LEVEL"] != "warn" || values["COOKIE_SECURE"] != "false" {
		t.Fatalf("api params = %+v", values)
	}
	if strings.Contains(values["DATABASE_URL"], "@") || strings.Contains(values["DATABASE_URL"], "integration-only") || values["DATABASE_URL"] == "" {
		t.Fatalf("api DATABASE_URL = %q", values["DATABASE_URL"])
	}
	workerValues := map[string]string{}
	for _, p := range body.Workers[0].Params {
		workerValues[p.Env] = p.Value
	}
	if body.Workers[0].ID != "cfg-worker" || workerValues["LLM_API_KEY"] != "задан" || workerValues["BACKUP_KEEP"] == "" {
		t.Fatalf("worker params = %+v", workerValues)
	}
	response, err := admin.Get(f.baseURL + "/api/v1/admin/config")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	rawBytes, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	raw = string(rawBytes)
	for _, secret := range []string{"integration-only", "sk-live-secret-key"} {
		if strings.Contains(raw, secret) {
			t.Fatalf("configuration leaks %q: %s", secret, raw)
		}
	}
}
