package status

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"emsim/internal/platform/audit"
	"emsim/internal/platform/httpapi"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// LoginResolver names the actors of audit rows. It is the status package's
// own port: the auth module's store satisfies it in cmd/emsim, so this
// package never reads the users table itself.
type LoginResolver func(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error)

// WithLogins sets how an audit row's actor is turned into a login. Without
// it the audit view shows ids only.
func (h *Handlers) WithLogins(resolve LoginResolver) *Handlers {
	h.logins = resolve
	return h
}

const (
	auditDefaultPage = 100
	// auditExportRowLimit bounds one CSV export; a longer period is
	// truncated (the header row says nothing, the last row's id lets the
	// administrator continue with `before`).
	auditExportRowLimit = 50000
	auditExportBatch    = 1000
)

type auditRowJSON struct {
	ID           int64          `json:"id"`
	At           time.Time      `json:"at"`
	ActorID      *uuid.UUID     `json:"actor_id"`
	ActorLogin   *string        `json:"actor_login"`
	ActorRole    *string        `json:"actor_role"`
	Action       string         `json:"action"`
	ResourceType string         `json:"resource_type"`
	ResourceID   *uuid.UUID     `json:"resource_id"`
	Outcome      string         `json:"outcome"`
	RequestID    *string        `json:"request_id"`
	Details      map[string]any `json:"details"`
}

// parseAuditFilter reads the shared query parameters of both audit routes.
func parseAuditFilter(r *http.Request) (audit.Filter, int64, error) {
	q := r.URL.Query()
	var f audit.Filter
	var before int64
	parseTime := func(name string) (*time.Time, error) {
		raw := q.Get(name)
		if raw == "" {
			return nil, nil
		}
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, err
		}
		return &t, nil
	}
	var err error
	if f.From, err = parseTime("from"); err != nil {
		return f, 0, err
	}
	if f.To, err = parseTime("to"); err != nil {
		return f, 0, err
	}
	if raw := q.Get("actor_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return f, 0, err
		}
		f.ActorID = &id
	}
	f.ActionPrefix = q.Get("action")
	f.Outcome = audit.Outcome(q.Get("outcome"))
	f.ResourceType = q.Get("resource_type")
	if raw := q.Get("before"); raw != "" {
		before, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || before < 0 {
			return f, 0, audit.ErrInvalidEntry
		}
	}
	return f, before, f.Validate()
}

// loginsFor resolves the distinct actors of rows; a resolver failure just
// leaves the logins out — the view is still useful by id and role.
func (h *Handlers) loginsFor(ctx context.Context, rows []audit.Row) map[uuid.UUID]string {
	if h.logins == nil {
		return nil
	}
	seen := map[uuid.UUID]struct{}{}
	var ids []uuid.UUID
	for _, row := range rows {
		if row.ActorID == nil {
			continue
		}
		if _, ok := seen[*row.ActorID]; !ok {
			seen[*row.ActorID] = struct{}{}
			ids = append(ids, *row.ActorID)
		}
	}
	names, err := h.logins(ctx, ids)
	if err != nil {
		return nil
	}
	return names
}

func (h *Handlers) listAudit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	filter, before, err := parseAuditFilter(r)
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeValidationFailed, "invalid audit filter", nil)
		return
	}
	limit := auditDefaultPage
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, convErr := strconv.Atoi(raw)
		if convErr != nil || n < 1 || n > audit.MaxPageSize {
			httpapi.WriteError(w, r, httpapi.CodeValidationFailed, "invalid limit", nil)
			return
		}
		limit = n
	}
	// One more row than asked tells whether another page exists.
	rows, err := audit.List(ctx, h.pool, filter, before, limit+1)
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to read audit log", nil)
		return
	}
	var next *string
	if len(rows) > limit {
		rows = rows[:limit]
		c := strconv.FormatInt(rows[len(rows)-1].ID, 10)
		next = &c
	}
	names := h.loginsFor(ctx, rows)
	items := make([]auditRowJSON, 0, len(rows))
	for _, row := range rows {
		items = append(items, toAuditJSON(row, names))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func toAuditJSON(row audit.Row, names map[uuid.UUID]string) auditRowJSON {
	out := auditRowJSON{
		ID: row.ID, At: row.At, ActorID: row.ActorID, Action: row.Action, ResourceType: row.ResourceType,
		ResourceID: row.ResourceID, Outcome: string(row.Outcome), Details: row.Details,
	}
	if row.ActorRole != "" {
		role := row.ActorRole
		out.ActorRole = &role
	}
	if row.RequestID != "" {
		rid := row.RequestID
		out.RequestID = &rid
	}
	if row.ActorID != nil {
		if login, ok := names[*row.ActorID]; ok {
			out.ActorLogin = &login
		}
	}
	return out
}

// exportAudit streams the filtered log as CSV, newest first, in keyset
// batches so a long period never sits in memory. The export itself is
// audited before any byte is sent — reading the log is an action too.
func (h *Handlers) exportAudit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	filter, before, err := parseAuditFilter(r)
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeValidationFailed, "invalid audit filter", nil)
		return
	}
	actorID, role, ok := h.actor(ctx)
	if !ok {
		httpapi.WriteError(w, r, httpapi.CodeUnauthorized, "authentication required", nil)
		return
	}
	details := map[string]any{"format": "csv"}
	if filter.ActionPrefix != "" {
		details["action"] = filter.ActionPrefix
	}
	if filter.Outcome != "" {
		details["outcome"] = string(filter.Outcome)
	}
	if filter.From != nil {
		details["from"] = filter.From.UTC().Format(time.RFC3339)
	}
	if filter.To != nil {
		details["to"] = filter.To.UTC().Format(time.RFC3339)
	}
	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to export audit log", nil)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := audit.Record(ctx, tx, audit.Entry{
		ActorID: &actorID, ActorRole: role, Action: "admin.audit.export", ResourceType: "audit_log",
		Outcome: audit.OutcomeOK, RequestID: httpapi.RequestIDFromContext(ctx), Details: details,
	}); err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to export audit log", nil)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to export audit log", nil)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="audit.csv"`)
	w.WriteHeader(http.StatusOK)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"id", "at", "actor_id", "actor_login", "actor_role", "action", "resource_type", "resource_id", "outcome", "request_id", "details"})
	written := 0
	for written < auditExportRowLimit {
		rows, err := audit.List(ctx, h.pool, filter, before, auditExportBatch)
		if err != nil || len(rows) == 0 {
			break
		}
		names := h.loginsFor(ctx, rows)
		for _, row := range rows {
			j := toAuditJSON(row, names)
			raw, _ := json.Marshal(j.Details)
			_ = cw.Write([]string{
				strconv.FormatInt(j.ID, 10), j.At.UTC().Format(time.RFC3339),
				optUUID(j.ActorID), optStr(j.ActorLogin), optStr(j.ActorRole), j.Action, j.ResourceType,
				optUUID(j.ResourceID), j.Outcome, optStr(j.RequestID), string(raw),
			})
		}
		written += len(rows)
		before = rows[len(rows)-1].ID
		if len(rows) < auditExportBatch {
			break
		}
	}
	cw.Flush()
}

func optStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func optUUID(u *uuid.UUID) string {
	if u == nil {
		return ""
	}
	return u.String()
}
