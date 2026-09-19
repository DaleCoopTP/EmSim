//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	pgstore "emsim/internal/platform/postgres"
)

// apiProcess is startAPIProcess's handle on a running "emsim api"
// subprocess — the same shape as worker_process_test.go's workerProcess,
// duplicated rather than shared since the two commands have different
// readiness/env/shutdown shapes and sharing would only add an abstraction
// neither test needs on its own.
type apiProcess struct {
	cmd    *exec.Cmd
	done   chan error
	waited bool
}

func (p *apiProcess) stop(t *testing.T) {
	t.Helper()
	if p.waited {
		return
	}
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal api: %v", err)
	}
	select {
	case err := <-p.done:
		p.waited = true
		if err != nil {
			t.Fatalf("graceful api exit: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("api did not exit")
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

func startAPIProcess(t *testing.T, binary, databaseURL, publicAddr, adminAddr string) *apiProcess {
	t.Helper()
	log, err := os.Create(filepath.Join(t.TempDir(), "api.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	cmd := exec.Command(binary, "api")
	cmd.Env = append(os.Environ(),
		"DATABASE_URL="+databaseURL, "API_LISTEN_ADDR="+publicAddr, "ADMIN_LISTEN_ADDR="+adminAddr,
		"COOKIE_SECURE=false", // no TLS in this test, same as compose's demo profile
	)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &apiProcess{cmd: cmd, done: make(chan error, 1)}
	go func() { p.done <- cmd.Wait() }()
	t.Cleanup(func() {
		if !p.waited {
			_ = p.cmd.Process.Kill()
			select {
			case <-p.done:
			case <-time.After(10 * time.Second):
				t.Error("api cleanup timeout")
			}
		}
	})
	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case err := <-p.done:
			p.waited = true
			t.Fatalf("api exited before readiness: %v; log %s", err, log.Name())
		default:
		}
		response, err := client.Get("http://" + adminAddr + "/readyz")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return p
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("api readiness timeout; log %s", log.Name())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func runBootstrapAdminProcess(t *testing.T, ctx context.Context, binary, databaseURL, login, password string) {
	t.Helper()
	cmd := exec.CommandContext(ctx, binary, "bootstrap-admin")
	cmd.Env = append(os.Environ(),
		"DATABASE_URL="+databaseURL, "BOOTSTRAP_ADMIN_LOGIN="+login, "BOOTSTRAP_ADMIN_PASSWORD="+password,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bootstrap-admin: %v\n%s", err, output)
	}
}

// jsonRequest sends body (marshaled to JSON, or no body when nil) as
// method to path on client/baseURL and decodes a JSON response into out
// (when out is not nil). It fails the test on a transport error, but
// lets the caller assert on the status code, since this helper is used
// for both expected-success and expected-failure requests.
func jsonRequest(t *testing.T, ctx context.Context, client *http.Client, baseURL, method, path string, body any, out any) *http.Response {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	if out != nil {
		if err := json.NewDecoder(response.Body).Decode(out); err != nil {
			t.Fatalf("%s %s: decode response: %v", method, path, err)
		}
	}
	return response
}

type meResponse struct {
	User struct {
		ID          string  `json:"id"`
		Login       string  `json:"login"`
		Role        string  `json:"role"`
		ServiceCode *string `json:"service_code"`
		Active      bool    `json:"active"`
	} `json:"user"`
	Workstation *struct {
		Number int    `json:"number"`
		Label  string `json:"label"`
	} `json:"workstation"`
}

type userResponse struct {
	ID   string `json:"id"`
	Role string `json:"role"`
}

// TestAPIProcessAuthHappyPath is slice 1's end-to-end automatic check
// (slice-planning.md §2 "проверки аутентификации, ролей и сессий проходят
// автоматически", C7 in the slice-1 commit plan): a real "emsim" binary,
// migrated schema, bootstrapped admin, and a running "api" process are
// driven purely over HTTP, the same way a browser would — admin logs in,
// provisions a workstation/instructor/trainee, the trainee logs in with
// that workstation and sees their own session, and a session revoked by
// logout stops working. It also checks the one authorization boundary
// slice 1 promises outside individual handler unit tests: a non-admin
// role gets 403 from the admin API.
func TestAPIProcessAuthHappyPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}

	binary := filepath.Join(t.TempDir(), "emsim")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/emsim")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build emsim: %v\n%s", err, output)
	}

	const adminLogin, adminPassword = "e2e-admin", "correct-horse-battery-staple"
	runBootstrapAdminProcess(t, ctx, binary, databaseURL, adminLogin, adminPassword)
	// A second run must be a no-op, not a second admin or an error — this
	// is the same invocation compose.yaml's "bootstrap" service performs
	// on every "docker compose up".
	runBootstrapAdminProcess(t, ctx, binary, databaseURL, adminLogin, adminPassword)

	publicAddr, adminAddr := freeAddr(t), freeAddr(t)
	api := startAPIProcess(t, binary, databaseURL, publicAddr, adminAddr)
	t.Cleanup(func() { api.stop(t) })
	baseURL := "http://" + publicAddr

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}

	// Admin logs in.
	var adminMe meResponse
	response := jsonRequest(t, ctx, client, baseURL, http.MethodPost, "/api/v1/auth/login",
		map[string]any{"login": adminLogin, "password": adminPassword}, &adminMe)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("admin login status = %d, want 200", response.StatusCode)
	}
	if adminMe.User.Role != "admin" || !adminMe.User.Active {
		t.Fatalf("admin Me = %+v", adminMe)
	}
	var sessionCookie *http.Cookie
	for _, c := range response.Cookies() {
		if c.Name == "emsim_session" {
			sessionCookie = c
		}
	}
	if sessionCookie == nil || !sessionCookie.HttpOnly || sessionCookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie = %+v, want HttpOnly + SameSite=Strict", sessionCookie)
	}

	// Admin creates a workstation.
	var workstations []map[string]any
	response = jsonRequest(t, ctx, client, baseURL, http.MethodPut, "/api/v1/admin/workstations",
		[]map[string]any{{"number": 5, "label": "РМ-05"}}, &workstations)
	if response.StatusCode != http.StatusOK || len(workstations) != 1 {
		t.Fatalf("PUT workstations status = %d, body = %+v", response.StatusCode, workstations)
	}

	// Admin creates an instructor and a trainee.
	var instructor userResponse
	response = jsonRequest(t, ctx, client, baseURL, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"login": "e2e-instructor", "password": "correct-horse-battery-staple",
		"full_name": "Инструктор Тестов", "role": "instructor",
	}, &instructor)
	if response.StatusCode != http.StatusCreated || instructor.Role != "instructor" {
		t.Fatalf("create instructor status = %d, body = %+v", response.StatusCode, instructor)
	}

	const traineeLogin, traineePassword, serviceCode = "e2e-trainee", "correct-horse-battery-staple", "dds_district"
	var trainee userResponse
	response = jsonRequest(t, ctx, client, baseURL, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"login": traineeLogin, "password": traineePassword,
		"full_name": "Курсант Тестов", "role": "trainee", "service_code": serviceCode,
	}, &trainee)
	if response.StatusCode != http.StatusCreated || trainee.Role != "trainee" {
		t.Fatalf("create trainee status = %d, body = %+v", response.StatusCode, trainee)
	}

	// Admin logs out; the same cookie must now be rejected.
	response = jsonRequest(t, ctx, client, baseURL, http.MethodPost, "/api/v1/auth/logout", nil, nil)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("admin logout status = %d, want 204", response.StatusCode)
	}
	response = jsonRequest(t, ctx, client, baseURL, http.MethodGet, "/api/v1/me", nil, nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /me after admin logout status = %d, want 401", response.StatusCode)
	}

	// Trainee logs in with their workstation.
	var traineeMe meResponse
	number := 5
	response = jsonRequest(t, ctx, client, baseURL, http.MethodPost, "/api/v1/auth/login",
		map[string]any{"login": traineeLogin, "password": traineePassword, "workstation_no": number}, &traineeMe)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("trainee login status = %d", response.StatusCode)
	}
	if traineeMe.User.Role != "trainee" || traineeMe.User.ServiceCode == nil || *traineeMe.User.ServiceCode != serviceCode {
		t.Fatalf("trainee Me.user = %+v", traineeMe.User)
	}
	if traineeMe.Workstation == nil || traineeMe.Workstation.Number != 5 {
		t.Fatalf("trainee Me.workstation = %+v, want number=5", traineeMe.Workstation)
	}

	// A page reload (GET /me again) must see the same session.
	var reloadedMe meResponse
	response = jsonRequest(t, ctx, client, baseURL, http.MethodGet, "/api/v1/me", nil, &reloadedMe)
	if response.StatusCode != http.StatusOK || reloadedMe.User.ID != traineeMe.User.ID {
		t.Fatalf("GET /me after reload status = %d, body = %+v", response.StatusCode, reloadedMe)
	}

	// The trainee must not reach the admin API.
	response = jsonRequest(t, ctx, client, baseURL, http.MethodGet, "/api/v1/admin/users", nil, nil)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("trainee GET /admin/users status = %d, want 403", response.StatusCode)
	}

	// Trainee logs out; the session stops working.
	response = jsonRequest(t, ctx, client, baseURL, http.MethodPost, "/api/v1/auth/logout", nil, nil)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("trainee logout status = %d, want 204", response.StatusCode)
	}
	response = jsonRequest(t, ctx, client, baseURL, http.MethodGet, "/api/v1/me", nil, nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /me after trainee logout status = %d, want 401", response.StatusCode)
	}

	// A wrong password is rejected without revealing which part was wrong.
	response = jsonRequest(t, ctx, client, baseURL, http.MethodPost, "/api/v1/auth/login",
		map[string]any{"login": traineeLogin, "password": "definitely-wrong"}, nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password login status = %d, want 401", response.StatusCode)
	}
}
