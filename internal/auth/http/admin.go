package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"emsim/internal/auth"
	"emsim/internal/platform/httpapi"

	"github.com/google/uuid"
)

// adminService is the subset of *auth.Service the admin handlers need,
// declared here (their consumer) same as service in handlers.go.
// Authenticate is embedded because these routes sit behind
// SessionMiddleware too — admin, not just "any authenticated user", but
// role checking happens in RequireRole, not here.
type adminService interface {
	authenticator
	CreateUser(ctx context.Context, n auth.NewUser, actor auth.Principal, requestID string) (auth.User, error)
	UpdateUser(ctx context.Context, id uuid.UUID, patch auth.Patch, actor auth.Principal, requestID string) (auth.User, error)
	ListUsers(ctx context.Context, page, pageSize int) ([]auth.User, int, error)
	ListWorkstations(ctx context.Context) ([]auth.Workstation, error)
	ReplaceWorkstations(ctx context.Context, workstations []auth.Workstation, actor auth.Principal, requestID string) ([]auth.Workstation, error)
}

// AdminHandlers owns RFC-001 §5's "admin" route group's user and
// workstation management (the rest of /admin/* — import, backup, status,
// task retry — belongs to later slices/modules).
type AdminHandlers struct {
	service adminService
}

func NewAdminHandlers(service adminService) *AdminHandlers {
	return &AdminHandlers{service: service}
}

// Register adds this package's admin routes to mux, each behind
// SessionMiddleware (resolves the Principal) then RequireRole(GroupAdmin)
// (only admin may reach the handler).
func (h *AdminHandlers) Register(mux *http.ServeMux) {
	protect := func(handler http.HandlerFunc) http.Handler {
		return SessionMiddleware(h.service)(RequireRole(auth.GroupAdmin)(handler))
	}
	mux.Handle("GET /api/v1/admin/users", protect(h.listUsers))
	mux.Handle("POST /api/v1/admin/users", protect(h.createUser))
	mux.Handle("PATCH /api/v1/admin/users/{userId}", protect(h.patchUser))
	mux.Handle("GET /api/v1/admin/workstations", protect(h.listWorkstations))
	mux.Handle("PUT /api/v1/admin/workstations", protect(h.replaceWorkstations))
}

type userListJSON struct {
	Items []userJSON `json:"items"`
	Total int        `json:"total"`
}

func (h *AdminHandlers) listUsers(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	page := queryIntOrDefault(query, "page", 1)
	pageSize := min(queryIntOrDefault(query, "page_size", 50), 200) // openapi.yaml PageSize: maximum 200

	users, total, err := h.service.ListUsers(r.Context(), page, pageSize)
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to list users", nil)
		return
	}
	items := make([]userJSON, len(users))
	for i, u := range users {
		items[i] = toUserJSON(u)
	}
	writeJSON(w, r, http.StatusOK, userListJSON{Items: items, Total: total})
}

type userCreateBody struct {
	Login       string  `json:"login"`
	Password    string  `json:"password"`
	FullName    string  `json:"full_name"`
	Role        string  `json:"role"`
	ServiceCode *string `json:"service_code,omitempty"`
	Level       string  `json:"level,omitempty"`
}

func (h *AdminHandlers) createUser(w http.ResponseWriter, r *http.Request) {
	var body userCreateBody
	if err := httpapi.DecodeJSON(r, 0, &body); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	actor, _ := PrincipalFromContext(r.Context())

	created, err := h.service.CreateUser(r.Context(), auth.NewUser{
		Login: body.Login, Password: body.Password, FullName: body.FullName,
		Role: auth.Role(body.Role), ServiceCode: body.ServiceCode, Level: auth.Level(body.Level),
	}, actor, httpapi.RequestIDFromContext(r.Context()))
	if err != nil {
		writeUserMutationError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusCreated, toUserJSON(created))
}

func (h *AdminHandlers) patchUser(w http.ResponseWriter, r *http.Request) {
	userID, err := uuid.Parse(r.PathValue("userId"))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeNotFound, "user not found", nil)
		return
	}

	raw := map[string]json.RawMessage{}
	if err := httpapi.DecodeJSON(r, 0, &raw); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	patch, err := parseUserPatch(raw)
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInvalidRequest, "malformed request body", nil)
		return
	}

	actor, _ := PrincipalFromContext(r.Context())
	updated, err := h.service.UpdateUser(r.Context(), userID, patch, actor, httpapi.RequestIDFromContext(r.Context()))
	if err != nil {
		writeUserMutationError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusOK, toUserJSON(updated))
}

// parseUserPatch builds an auth.Patch from a raw JSON object, tracking
// which keys the client actually sent (a nil auth.Patch field means
// "untouched" — see Patch's doc comment). It decodes into
// map[string]json.RawMessage first, rather than a struct of pointer
// fields, specifically so an explicit `"service_code": null` (clear it) is
// distinguishable from the key being absent (leave it alone) — both would
// decode to the same nil *string with a plain struct target. An explicit
// null clears service_code (Patch's own empty-string convention); a
// literal JSON null for any other field is not a valid shape for that
// field and surfaces as a validation error downstream (json.Unmarshal of
// null into a non-pointer string/bool leaves it at its zero value, which
// Service's own validation then rejects where the field cannot be empty).
func parseUserPatch(raw map[string]json.RawMessage) (auth.Patch, error) {
	var patch auth.Patch
	if v, ok := raw["password"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return auth.Patch{}, err
		}
		patch.Password = &s
	}
	if v, ok := raw["full_name"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return auth.Patch{}, err
		}
		patch.FullName = &s
	}
	if v, ok := raw["role"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return auth.Patch{}, err
		}
		role := auth.Role(s)
		patch.Role = &role
	}
	if v, ok := raw["service_code"]; ok {
		if string(v) == "null" {
			empty := ""
			patch.ServiceCode = &empty
		} else {
			var s string
			if err := json.Unmarshal(v, &s); err != nil {
				return auth.Patch{}, err
			}
			patch.ServiceCode = &s
		}
	}
	if v, ok := raw["level"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return auth.Patch{}, err
		}
		level := auth.Level(s)
		patch.Level = &level
	}
	if v, ok := raw["active"]; ok {
		var b bool
		if err := json.Unmarshal(v, &b); err != nil {
			return auth.Patch{}, err
		}
		patch.Active = &b
	}
	return patch, nil
}

// writeUserMutationError maps CreateUser/UpdateUser/ReplaceWorkstations'
// domain errors to the API's closed error codes.
func writeUserMutationError(w http.ResponseWriter, r *http.Request, err error) {
	var ve *auth.ValidationError
	switch {
	case errors.Is(err, auth.ErrLoginTaken):
		httpapi.WriteError(w, r, httpapi.CodeConflict, "login is already taken", nil)
	case errors.Is(err, auth.ErrLastAdmin):
		httpapi.WriteError(w, r, httpapi.CodeConflict, "cannot deactivate or demote the last active admin", nil)
	case errors.Is(err, auth.ErrNotFound):
		httpapi.WriteError(w, r, httpapi.CodeNotFound, "user not found", nil)
	case errors.As(err, &ve):
		httpapi.WriteError(w, r, httpapi.CodeValidationFailed, "validation failed", map[string]any{"field": ve.Field})
	default:
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "operation failed", nil)
	}
}

func (h *AdminHandlers) listWorkstations(w http.ResponseWriter, r *http.Request) {
	workstations, err := h.service.ListWorkstations(r.Context())
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to list workstations", nil)
		return
	}
	items := make([]workstationJSON, len(workstations))
	for i, w2 := range workstations {
		items[i] = toWorkstationJSON(w2)
	}
	writeJSON(w, r, http.StatusOK, items)
}

// workstationReplaceBody is one element of PUT /admin/workstations' array
// body (openapi.yaml Workstation schema used as input). id is ignored —
// it is readOnly on the schema, and a workstation is matched by Number,
// not id, per ReplaceWorkstations' upsert semantics; active is likewise
// ignored on input — every workstation the caller includes ends up
// active, and DeactivateWorkstationsNotIn handles the rest.
type workstationReplaceBody struct {
	Number    int     `json:"number"`
	Label     string  `json:"label"`
	IPAddress *string `json:"ip_address,omitempty"`
}

func (h *AdminHandlers) replaceWorkstations(w http.ResponseWriter, r *http.Request) {
	var body []workstationReplaceBody
	if err := httpapi.DecodeJSON(r, 0, &body); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	workstations := make([]auth.Workstation, len(body))
	for i, w2 := range body {
		workstations[i] = auth.Workstation{Number: w2.Number, Label: w2.Label, IPAddress: w2.IPAddress}
	}

	actor, _ := PrincipalFromContext(r.Context())
	result, err := h.service.ReplaceWorkstations(r.Context(), workstations, actor, httpapi.RequestIDFromContext(r.Context()))
	if err != nil {
		writeUserMutationError(w, r, err)
		return
	}
	items := make([]workstationJSON, len(result))
	for i, w2 := range result {
		items[i] = toWorkstationJSON(w2)
	}
	writeJSON(w, r, http.StatusOK, items)
}

// queryIntOrDefault parses query[key] as a positive integer, or returns
// fallback for an absent, malformed, or non-positive value — pagination
// query parameters fail soft, not with a 422, since an odd page/page_size
// value from a client is harmless to just clamp.
func queryIntOrDefault(query url.Values, key string, fallback int) int {
	raw := query.Get(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return fallback
	}
	return value
}
