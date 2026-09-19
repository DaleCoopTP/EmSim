package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"emsim/internal/auth"
	"emsim/internal/platform/httpapi"

	"github.com/google/uuid"
)

// fakeService drives the HTTP layer's own behavior (status codes,
// headers, cookie attributes, JSON shape) independently of
// internal/auth.Service's business logic, which service_test.go already
// covers in depth with a fake Store. It models just enough state — a
// single valid token, a logged-out flag — to exercise the "login then
// replay /me" and "401 after logout" flows end to end through Register's
// real mux.
type fakeService struct {
	mu sync.Mutex

	loginResult auth.LoginResult
	loginErr    error
	loginCalls  []auth.LoginRequest

	validToken       string
	principal        auth.Principal
	authenticateErr  error
	loggedOut        bool
	logoutTokens     []string
	logoutRequestIDs []string
	logoutErr        error

	meResult auth.Me
	meErr    error

	createUserResult auth.User
	createUserErr    error
	createUserCalls  []auth.NewUser

	updateUserResult auth.User
	updateUserErr    error
	updateUserCalls  []updateUserCall

	listUsersResult []auth.User
	listUsersTotal  int
	listUsersErr    error
	listUsersCalls  []pageCall

	listWorkstationsResult []auth.Workstation
	listWorkstationsErr    error

	replaceWorkstationsResult []auth.Workstation
	replaceWorkstationsErr    error
	replaceWorkstationsCalls  [][]auth.Workstation
}

type updateUserCall struct {
	id    uuid.UUID
	patch auth.Patch
}

type pageCall struct {
	page, pageSize int
}

func (f *fakeService) Login(_ context.Context, req auth.LoginRequest, _ string) (auth.LoginResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loginCalls = append(f.loginCalls, req)
	return f.loginResult, f.loginErr
}

func (f *fakeService) Logout(_ context.Context, token, requestID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logoutTokens = append(f.logoutTokens, token)
	f.logoutRequestIDs = append(f.logoutRequestIDs, requestID)
	if f.logoutErr == nil {
		f.loggedOut = true
	}
	return f.logoutErr
}

func (f *fakeService) Authenticate(_ context.Context, token string) (auth.Principal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.authenticateErr != nil {
		return auth.Principal{}, f.authenticateErr
	}
	if f.loggedOut || token != f.validToken {
		return auth.Principal{}, auth.ErrSessionInvalid
	}
	return f.principal, nil
}

func (f *fakeService) Me(_ context.Context, _ auth.Principal) (auth.Me, error) {
	return f.meResult, f.meErr
}

func (f *fakeService) CreateUser(_ context.Context, n auth.NewUser, _ auth.Principal, _ string) (auth.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createUserCalls = append(f.createUserCalls, n)
	return f.createUserResult, f.createUserErr
}

func (f *fakeService) UpdateUser(_ context.Context, id uuid.UUID, patch auth.Patch, _ auth.Principal, _ string) (auth.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateUserCalls = append(f.updateUserCalls, updateUserCall{id: id, patch: patch})
	return f.updateUserResult, f.updateUserErr
}

func (f *fakeService) ListUsers(_ context.Context, page, pageSize int) ([]auth.User, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listUsersCalls = append(f.listUsersCalls, pageCall{page: page, pageSize: pageSize})
	return f.listUsersResult, f.listUsersTotal, f.listUsersErr
}

func (f *fakeService) ListWorkstations(_ context.Context) ([]auth.Workstation, error) {
	return f.listWorkstationsResult, f.listWorkstationsErr
}

func (f *fakeService) ReplaceWorkstations(_ context.Context, workstations []auth.Workstation, _ auth.Principal, _ string) ([]auth.Workstation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replaceWorkstationsCalls = append(f.replaceWorkstationsCalls, workstations)
	return f.replaceWorkstationsResult, f.replaceWorkstationsErr
}

var _ service = (*fakeService)(nil)
var _ adminService = (*fakeService)(nil)

func newTestHandlers(svc *fakeService, cookieSecure bool) (*Handlers, *http.ServeMux) {
	h := NewHandlers(svc, cookieSecure)
	mux := httpapi.NewMux()
	h.Register(mux)
	return h, mux
}

func newTestAdminMux(svc *fakeService) *http.ServeMux {
	mux := httpapi.NewMux()
	NewAdminHandlers(svc, true).Register(mux)
	return mux
}

// authedAdminRequest builds a request that will authenticate as an admin
// through fakeService's Authenticate (svc.validToken/svc.principal must
// already be set to an admin Principal).
func authedAdminRequest(method, path string, body *strings.Reader, token string) *http.Request {
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, path, body)
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	r.AddCookie(&http.Cookie{Name: CookieName, Value: token})
	return r
}

// wrapped runs a request through the same middleware chain cmd/emsim/api.go
// composes in production (request id + no-store), so header assertions
// reflect what a real client sees.
func wrapped(mux *http.ServeMux) http.Handler {
	return httpapi.WithRequestID(mux)
}

func sampleMe() auth.Me {
	return auth.Me{
		User: auth.User{
			ID: uuid.New(), Login: "dispatcher-1", FullName: "Иванов Иван", Role: auth.RoleAdmin,
			Level: auth.LevelEasy, Active: true,
		},
		SessionExpiresAt: time.Now().Add(12 * time.Hour),
	}
}

func TestLoginSuccessReturns200WithCookieAndBody(t *testing.T) {
	svc := &fakeService{loginResult: auth.LoginResult{
		Token: "the-session-token", ExpiresAt: time.Now().Add(12 * time.Hour), Me: sampleMe(),
	}}
	_, mux := newTestHandlers(svc, true)

	body := `{"login":"dispatcher-1","password":"correct-horse"}`
	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body)))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	if len(svc.loginCalls) != 1 || svc.loginCalls[0].Login != "dispatcher-1" || svc.loginCalls[0].Password != "correct-horse" {
		t.Fatalf("loginCalls = %+v", svc.loginCalls)
	}

	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %v, want exactly one", cookies)
	}
	cookie := cookies[0]
	if cookie.Name != CookieName || cookie.Value != "the-session-token" {
		t.Fatalf("cookie = %+v, want name=%s value=the-session-token", cookie, CookieName)
	}
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" {
		t.Fatalf("cookie attributes = %+v, want HttpOnly+Secure+SameSite=Strict+Path=/", cookie)
	}

	var got meJSON
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.User.Login != "dispatcher-1" || got.User.Role != "admin" {
		t.Fatalf("body.user = %+v", got.User)
	}
}

func TestLoginCookieNotSecureWhenCookieSecureFalse(t *testing.T) {
	svc := &fakeService{loginResult: auth.LoginResult{Token: "tok", ExpiresAt: time.Now().Add(time.Hour), Me: sampleMe()}}
	_, mux := newTestHandlers(svc, false)

	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"login":"a","password":"b"}`)))

	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Secure {
		t.Fatalf("cookies = %v, want Secure=false (demo compose profile)", cookies)
	}
}

func TestLoginRejectsMalformedJSONWith400(t *testing.T) {
	svc := &fakeService{}
	_, mux := newTestHandlers(svc, true)

	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"login":`)))

	assertErrorEnvelope(t, response, http.StatusBadRequest, "invalid_request")
	if len(svc.loginCalls) != 0 {
		t.Fatal("Service.Login was called with a malformed body")
	}
}

func TestLoginRejectsOversizedBodyWith413(t *testing.T) {
	svc := &fakeService{}
	_, mux := newTestHandlers(svc, true)

	oversized := `{"login":"` + strings.Repeat("a", httpapi.MaxJSONBodyBytes) + `"}`
	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(oversized)))

	assertErrorEnvelope(t, response, http.StatusRequestEntityTooLarge, "payload_too_large")
}

func TestLoginMapsDomainErrorsToClosedCodes(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"invalid credentials", auth.ErrInvalidCredentials, http.StatusUnauthorized, "unauthorized"},
		{"inactive user collapses to the same 401", auth.ErrUserInactive, http.StatusUnauthorized, "unauthorized"},
		{"rate limited", auth.ErrRateLimited, http.StatusTooManyRequests, "rate_limited"},
		{"workstation required", auth.ErrWorkstationRequired, http.StatusUnprocessableEntity, "validation_failed"},
		{"workstation unknown", auth.ErrWorkstationUnknown, http.StatusUnprocessableEntity, "validation_failed"},
		{"workstation inactive", auth.ErrWorkstationInactive, http.StatusUnprocessableEntity, "validation_failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc := &fakeService{loginErr: test.err}
			_, mux := newTestHandlers(svc, true)

			response := httptest.NewRecorder()
			wrapped(mux).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"login":"a","password":"12345678"}`)))

			assertErrorEnvelope(t, response, test.wantStatus, test.wantCode)
			if len(response.Result().Cookies()) != 0 {
				t.Fatal("a failed login must not set a cookie")
			}
		})
	}
}

func TestLoginWorkstationErrorsIncludeFieldDetail(t *testing.T) {
	svc := &fakeService{loginErr: auth.ErrWorkstationRequired}
	_, mux := newTestHandlers(svc, true)

	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"login":"a","password":"12345678"}`)))

	var body struct {
		Error struct {
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error.Details["field"] != "workstation_no" {
		t.Fatalf("details = %v, want field=workstation_no", body.Error.Details)
	}
}

func TestLogoutAlwaysReturns204AndClearsCookieEvenWithoutOne(t *testing.T) {
	svc := &fakeService{}
	_, mux := newTestHandlers(svc, true)

	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil))

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", response.Code)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Value != "" || cookies[0].MaxAge >= 0 {
		t.Fatalf("cookies = %v, want one cleared emsim_session cookie", cookies)
	}
	if len(svc.logoutTokens) != 0 {
		t.Fatal("Service.Logout was called despite no cookie on the request")
	}
}

func TestLogoutCallsServiceWithCookieTokenAndRequestID(t *testing.T) {
	svc := &fakeService{}
	_, mux := newTestHandlers(svc, true)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	r.AddCookie(&http.Cookie{Name: CookieName, Value: "session-to-kill"})
	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, r)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", response.Code)
	}
	if len(svc.logoutTokens) != 1 || svc.logoutTokens[0] != "session-to-kill" {
		t.Fatalf("logoutTokens = %v, want [session-to-kill]", svc.logoutTokens)
	}
	if svc.logoutRequestIDs[0] == "" {
		t.Fatal("Logout was called with an empty request id")
	}
}

func TestLogoutReturns500AndKeepsCookieWhenRevocationFails(t *testing.T) {
	svc := &fakeService{logoutErr: auth.ErrStorage}
	_, mux := newTestHandlers(svc, true)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	r.AddCookie(&http.Cookie{Name: CookieName, Value: "session-not-revoked"})
	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, r)

	assertErrorEnvelope(t, response, http.StatusInternalServerError, "internal_error")
	if cookies := response.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("cookies = %v, failed logout must not clear the browser cookie", cookies)
	}
}

func TestMeRequiresAuthenticationWithout401(t *testing.T) {
	svc := &fakeService{}
	_, mux := newTestHandlers(svc, true)

	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/me", nil))

	assertErrorEnvelope(t, response, http.StatusUnauthorized, "unauthorized")
}

func TestMeRejectsInvalidCookie(t *testing.T) {
	svc := &fakeService{authenticateErr: auth.ErrSessionInvalid}
	_, mux := newTestHandlers(svc, true)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	r.AddCookie(&http.Cookie{Name: CookieName, Value: "not-a-real-session"})
	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, r)

	assertErrorEnvelope(t, response, http.StatusUnauthorized, "unauthorized")
}

func TestMeRefreshesCookieToAuthoritativeSessionExpiry(t *testing.T) {
	expiresAt := time.Now().Add(10 * time.Hour).UTC().Truncate(time.Second)
	me := sampleMe()
	me.SessionExpiresAt = expiresAt
	svc := &fakeService{
		validToken: "renewed-token",
		principal:  auth.Principal{UserID: me.User.ID, Role: me.User.Role, SessionExpiresAt: expiresAt},
		meResult:   me,
	}
	_, mux := newTestHandlers(svc, true)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	r.AddCookie(&http.Cookie{Name: CookieName, Value: "renewed-token"})
	response := httptest.NewRecorder()
	wrapper := wrapped(mux)
	wrapper.ServeHTTP(response, r)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Value != "renewed-token" || !cookies[0].Expires.Equal(expiresAt) {
		t.Fatalf("cookies = %v, want renewed session cookie expiring at %v", cookies, expiresAt)
	}
}

func TestLoginThenReplayMeThenLogoutThen401(t *testing.T) {
	me := sampleMe()
	principal := auth.Principal{UserID: me.User.ID, Role: me.User.Role, SessionExpiresAt: me.SessionExpiresAt}
	svc := &fakeService{
		loginResult: auth.LoginResult{Token: "issued-session-token", ExpiresAt: me.SessionExpiresAt, Me: me},
		validToken:  "issued-session-token",
		principal:   principal,
		meResult:    me,
	}
	_, mux := newTestHandlers(svc, true)
	handler := wrapped(mux)

	// 1. Login.
	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"login":"dispatcher-1","password":"correct-horse"}`)))
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("login status = %d", loginResponse.Code)
	}
	sessionCookie := loginResponse.Result().Cookies()[0]

	// 2. Replay the cookie against /me: must succeed.
	meRequest := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	meRequest.AddCookie(sessionCookie)
	meResponse := httptest.NewRecorder()
	handler.ServeHTTP(meResponse, meRequest)
	if meResponse.Code != http.StatusOK {
		t.Fatalf("GET /me after login status = %d, want 200; body=%s", meResponse.Code, meResponse.Body.String())
	}
	var meBody meJSON
	if err := json.NewDecoder(meResponse.Body).Decode(&meBody); err != nil {
		t.Fatalf("decode /me body: %v", err)
	}
	if meBody.User.Login != me.User.Login {
		t.Fatalf("GET /me user = %+v, want login=%s", meBody.User, me.User.Login)
	}

	// 3. Logout with the same cookie.
	logoutRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logoutRequest.AddCookie(sessionCookie)
	logoutResponse := httptest.NewRecorder()
	handler.ServeHTTP(logoutResponse, logoutRequest)
	if logoutResponse.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d", logoutResponse.Code)
	}

	// 4. The same cookie must now be rejected.
	meAfterLogoutRequest := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	meAfterLogoutRequest.AddCookie(sessionCookie)
	meAfterLogoutResponse := httptest.NewRecorder()
	handler.ServeHTTP(meAfterLogoutResponse, meAfterLogoutRequest)
	assertErrorEnvelope(t, meAfterLogoutResponse, http.StatusUnauthorized, "unauthorized")
}

func TestEveryResponseCarriesNoStoreAndRequestID(t *testing.T) {
	svc := &fakeService{loginErr: auth.ErrInvalidCredentials}
	_, mux := newTestHandlers(svc, true)

	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"login":"a","password":"12345678"}`)))

	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if response.Header().Get(httpapi.RequestIDHeader) == "" {
		t.Fatal("X-Request-ID header missing")
	}
	var body struct {
		RequestID string `json:"request_id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.RequestID != response.Header().Get(httpapi.RequestIDHeader) {
		t.Fatalf("body.request_id = %q, header = %q", body.RequestID, response.Header().Get(httpapi.RequestIDHeader))
	}
}

func TestLoginResponseBodyNeverMentionsPassword(t *testing.T) {
	svc := &fakeService{loginResult: auth.LoginResult{Token: "tok", ExpiresAt: time.Now().Add(time.Hour), Me: sampleMe()}}
	_, mux := newTestHandlers(svc, true)

	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"login":"dispatcher-1","password":"correct-horse"}`)))

	if strings.Contains(strings.ToLower(response.Body.String()), "password") || strings.Contains(response.Body.String(), "correct-horse") {
		t.Fatalf("response body leaks password: %s", response.Body.String())
	}
}

func assertErrorEnvelope(t *testing.T, response *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if response.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, wantStatus, response.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		RequestID string `json:"request_id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if body.Error.Code != wantCode {
		t.Fatalf("error.code = %q, want %q", body.Error.Code, wantCode)
	}
}
