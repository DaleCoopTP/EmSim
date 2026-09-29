package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"emsim/internal/auth"
	"emsim/internal/platform/httpapi"
)

// service is the subset of *auth.Service the handlers need — declared
// here (their consumer) so a fake can drive handler tests without a real
// Store (CLAUDE.md: "declare [interfaces] near the consuming application
// service").
type service interface {
	authenticator
	Login(ctx context.Context, req auth.LoginRequest, requestID string) (auth.LoginResult, error)
	Logout(ctx context.Context, token, requestID string) error
	Me(ctx context.Context, principal auth.Principal) (auth.Me, error)
	ChangePassword(ctx context.Context, principal auth.Principal, current, next, requestID string) error
}

// Handlers owns the "auth" route group (RFC-001 §5: "все") — login,
// logout, and the current user's own view of their session.
type Handlers struct {
	service      service
	cookieSecure bool
}

func NewHandlers(service service, cookieSecure bool) *Handlers {
	return &Handlers{service: service, cookieSecure: cookieSecure}
}

// Register adds this package's routes to mux (cmd/emsim/api.go composes
// the process from here and from each other module's own Register).
func (h *Handlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/auth/login", h.login)
	mux.HandleFunc("POST /api/v1/auth/logout", h.logout)
	mux.Handle("GET /api/v1/me", SessionMiddleware(h.service, h.cookieSecure)(http.HandlerFunc(h.me)))
	mux.Handle("POST /api/v1/me/password", SessionMiddleware(h.service, h.cookieSecure)(http.HandlerFunc(h.changePassword)))
}

type loginRequestBody struct {
	Login         string `json:"login"`
	Password      string `json:"password"`
	WorkstationNo *int   `json:"workstation_no,omitempty"`
}

func (h *Handlers) login(w http.ResponseWriter, r *http.Request) {
	var body loginRequestBody
	if err := httpapi.DecodeJSON(r, 0, &body); err != nil {
		writeDecodeError(w, r, err)
		return
	}

	result, err := h.service.Login(r.Context(), auth.LoginRequest{
		Login: body.Login, Password: body.Password, WorkstationNo: body.WorkstationNo,
	}, httpapi.RequestIDFromContext(r.Context()))
	if err != nil {
		writeLoginError(w, r, err)
		return
	}

	SetSessionCookie(w, result.Token, result.ExpiresAt, h.cookieSecure)
	writeMe(w, r, http.StatusOK, result.Me)
}

func (h *Handlers) logout(w http.ResponseWriter, r *http.Request) {
	if token := sessionTokenFromRequest(r); token != "" {
		if err := h.service.Logout(r.Context(), token, httpapi.RequestIDFromContext(r.Context())); err != nil {
			httpapi.WriteError(w, r, httpapi.CodeInternalError, "logout failed", nil)
			return
		}
	}
	ClearSessionCookie(w, h.cookieSecure)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) me(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		// Unreachable through Register's own wiring (SessionMiddleware
		// always sets a Principal before this runs) — kept as a direct
		// 401 rather than a panic in case this handler is ever wired up
		// without the middleware.
		httpapi.WriteError(w, r, httpapi.CodeUnauthorized, "authentication required", nil)
		return
	}
	me, err := h.service.Me(r.Context(), principal)
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to load current user", nil)
		return
	}
	writeMe(w, r, http.StatusOK, me)
}

type changePasswordBody struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// changePassword is POST /me/password (ADR-038): the user replaces their
// own password; the other sessions end, this one stays.
func (h *Handlers) changePassword(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		httpapi.WriteError(w, r, httpapi.CodeUnauthorized, "authentication required", nil)
		return
	}
	var body changePasswordBody
	if err := httpapi.DecodeJSON(r, 0, &body); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	if err := h.service.ChangePassword(r.Context(), principal, body.CurrentPassword, body.NewPassword, httpapi.RequestIDFromContext(r.Context())); err != nil {
		var ve *auth.ValidationError
		if errors.As(err, &ve) {
			httpapi.WriteError(w, r, httpapi.CodeValidationFailed, "validation failed", map[string]any{"field": ve.Field, "reason": ve.Reason})
			return
		}
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to change password", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeDecodeError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, httpapi.ErrBodyTooLarge) {
		httpapi.WriteError(w, r, httpapi.CodePayloadTooLarge, "request body too large", nil)
		return
	}
	httpapi.WriteError(w, r, httpapi.CodeInvalidRequest, "malformed request body", nil)
}

// writeLoginError maps Service.Login's domain errors to the API's closed
// error codes (openapi.yaml). Credential problems and an inactive account
// collapse into the same opaque 401 (RFC-001 §5: a client must never be
// able to tell "wrong password" from "account disabled" apart) —
// workstation problems get precise 422 feedback instead, since they are
// not a credential-guessing attack surface and a legitimate trainee
// benefits from knowing exactly what to fix.
func writeLoginError(w http.ResponseWriter, r *http.Request, err error) {
	var ve *auth.ValidationError
	switch {
	case errors.Is(err, auth.ErrRateLimited):
		httpapi.WriteError(w, r, httpapi.CodeRateLimited, "too many login attempts", nil)
	case errors.Is(err, auth.ErrAccountLocked):
		httpapi.WriteError(w, r, httpapi.CodeAccountLocked, "account is locked", nil)
	case errors.Is(err, auth.ErrInvalidCredentials), errors.Is(err, auth.ErrUserInactive):
		httpapi.WriteError(w, r, httpapi.CodeUnauthorized, "invalid credentials", nil)
	case errors.Is(err, auth.ErrWorkstationRequired):
		httpapi.WriteError(w, r, httpapi.CodeValidationFailed, "workstation_no is required", map[string]any{"field": "workstation_no", "reason": "required"})
	case errors.Is(err, auth.ErrWorkstationUnknown):
		httpapi.WriteError(w, r, httpapi.CodeValidationFailed, "unknown workstation", map[string]any{"field": "workstation_no", "reason": "unknown"})
	case errors.Is(err, auth.ErrWorkstationInactive):
		httpapi.WriteError(w, r, httpapi.CodeValidationFailed, "workstation is inactive", map[string]any{"field": "workstation_no", "reason": "inactive"})
	case errors.As(err, &ve):
		httpapi.WriteError(w, r, httpapi.CodeValidationFailed, "validation failed", map[string]any{"field": ve.Field})
	default:
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "login failed", nil)
	}
}

type userJSON struct {
	ID          string  `json:"id"`
	Login       string  `json:"login"`
	FullName    string  `json:"full_name"`
	Role        string  `json:"role"`
	ServiceCode *string `json:"service_code"`
	Level       string  `json:"level"`
	Active      bool    `json:"active"`
	// ADR-038: login policy state. LockedUntil is set only while a lock is
	// in force. The flag is named for what the client must do, not for the
	// secret it concerns: response bodies never mention "password".
	LockedUntil        *string `json:"locked_until"`
	MustChangePassword bool    `json:"credentials_change_required"`
}

type workstationJSON struct {
	ID        string  `json:"id"`
	Number    int     `json:"number"`
	Label     string  `json:"label"`
	IPAddress *string `json:"ip_address"`
	Active    bool    `json:"active"`
}

type meJSON struct {
	User             userJSON         `json:"user"`
	Workstation      *workstationJSON `json:"workstation,omitempty"`
	SessionExpiresAt string           `json:"session_expires_at"`
}

func writeMe(w http.ResponseWriter, r *http.Request, status int, me auth.Me) {
	body := meJSON{User: toUserJSON(me.User), SessionExpiresAt: me.SessionExpiresAt.UTC().Format(time.RFC3339)}
	if me.Workstation != nil {
		ws := toWorkstationJSON(*me.Workstation)
		body.Workstation = &ws
	}
	writeJSON(w, r, status, body)
}

// toUserJSON/toWorkstationJSON are shared with admin.go's list/create/patch
// responses — every endpoint that renders a User or Workstation uses the
// same shape (openapi.yaml's User/Workstation schemas).
func toUserJSON(u auth.User) userJSON {
	body := userJSON{
		ID: u.ID.String(), Login: u.Login, FullName: u.FullName,
		Role: string(u.Role), ServiceCode: u.ServiceCode, Level: string(u.Level), Active: u.Active,
		MustChangePassword: u.MustChangePassword,
	}
	if u.LockedUntil != nil && u.LockedUntil.After(time.Now()) {
		until := u.LockedUntil.UTC().Format(time.RFC3339)
		body.LockedUntil = &until
	}
	return body
}

func toWorkstationJSON(w auth.Workstation) workstationJSON {
	return workstationJSON{
		ID: w.ID.String(), Number: w.Number, Label: w.Label, IPAddress: w.IPAddress, Active: w.Active,
	}
}

// writeJSON writes body as the response with the shared JSON headers.
func writeJSON(w http.ResponseWriter, r *http.Request, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
