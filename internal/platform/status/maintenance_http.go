package status

import (
	"net/http"
	"time"

	"emsim/internal/platform/httpapi"
	"emsim/internal/platform/maintenance"

	"github.com/jackc/pgx/v5"
)

type maintenanceJSON struct {
	Enabled bool      `json:"enabled"`
	Reason  string    `json:"reason"`
	SetAt   time.Time `json:"set_at"`
}

func toMaintenanceJSON(s maintenance.State) maintenanceJSON {
	return maintenanceJSON{Enabled: s.Enabled, Reason: s.Reason, SetAt: s.SetAt}
}

// RegisterSystem adds GET /system for every signed-in role: the banner
// that tells a trainee or an instructor that maintenance mode is on
// (ADR-038). It carries no operational detail beyond the administrator's
// own note.
func (h *Handlers) RegisterSystem(mux *http.ServeMux, protectAny func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /api/v1/system", protectAny(h.getSystem))
}

func (h *Handlers) getSystem(w http.ResponseWriter, r *http.Request) {
	state, err := maintenance.Get(r.Context(), h.pool)
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to read system state", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"maintenance": toMaintenanceJSON(state)})
}

type maintenanceRequest struct {
	Enabled *bool  `json:"enabled"`
	Reason  string `json:"reason"`
}

// setMaintenance is PUT /admin/maintenance.
func (h *Handlers) setMaintenance(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body maintenanceRequest
	if err := httpapi.DecodeJSON(r, 4<<10, &body); err != nil || body.Enabled == nil {
		httpapi.WriteError(w, r, httpapi.CodeInvalidRequest, "malformed request body", nil)
		return
	}
	actorID, role, ok := h.actor(ctx)
	if !ok {
		httpapi.WriteError(w, r, httpapi.CodeUnauthorized, "authentication required", nil)
		return
	}
	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to set maintenance mode", nil)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	state, err := maintenance.SetTx(ctx, tx, *body.Enabled, body.Reason, actorID, role, httpapi.RequestIDFromContext(ctx))
	switch err {
	case nil:
	case maintenance.ErrInvalid:
		httpapi.WriteError(w, r, httpapi.CodeValidationFailed, "reason is too long", map[string]any{"field": "reason", "reason": "too_long"})
		return
	default:
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to set maintenance mode", nil)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to set maintenance mode", nil)
		return
	}
	writeJSON(w, http.StatusOK, toMaintenanceJSON(state))
}
