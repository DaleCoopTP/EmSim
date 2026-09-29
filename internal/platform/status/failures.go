package status

import (
	"context"
	"encoding/csv"
	"net/http"
	"strconv"
	"time"

	"emsim/internal/platform/audit"
	"emsim/internal/platform/httpapi"
)

// The failures report (ADR-038): what went wrong in a period, summed up
// so an administrator sees patterns rather than rows — failed background
// tasks by kind and error code, server restarts that interrupted lessons,
// rejected logins by cause, errors the modules audited, and server errors
// the api answered. It reads platform tables only and carries no task
// payload and no lesson content.

const failuresDefaultDays = 7

type failureTaskJSON struct {
	Kind      string    `json:"kind"`
	ErrorCode string    `json:"error_code"`
	Count     int       `json:"count"`
	FirstAt   time.Time `json:"first_at"`
	LastAt    time.Time `json:"last_at"`
}

type failureCountJSON struct {
	Key     string    `json:"key"`
	Count   int       `json:"count"`
	FirstAt time.Time `json:"first_at"`
	LastAt  time.Time `json:"last_at"`
}

type failuresJSON struct {
	From            time.Time          `json:"from"`
	To              time.Time          `json:"to"`
	Tasks           []failureTaskJSON  `json:"tasks"`
	Restarts        []failureCountJSON `json:"restarts"`
	RejectedLogins  []failureCountJSON `json:"rejected_logins"`
	AuditedErrors   []failureCountJSON `json:"audited_errors"`
	ServerErrorsAPI struct {
		Since time.Time `json:"since"`
		Count int       `json:"count"`
	} `json:"server_errors"`
}

// parsePeriod reads from/to (RFC 3339); a missing "to" is now, a missing
// "from" is seven days before "to".
func parsePeriod(r *http.Request, now time.Time) (time.Time, time.Time, bool) {
	to := now
	from := time.Time{}
	if raw := r.URL.Query().Get("to"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return from, to, false
		}
		to = t
	}
	if raw := r.URL.Query().Get("from"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return from, to, false
		}
		from = t
	} else {
		from = to.AddDate(0, 0, -failuresDefaultDays)
	}
	return from, to, !to.Before(from)
}

func (h *Handlers) buildFailures(ctx context.Context, from, to time.Time) (failuresJSON, error) {
	report := failuresJSON{From: from, To: to, Tasks: []failureTaskJSON{}}
	rows, err := h.pool.Query(ctx, `
		SELECT kind, COALESCE(last_error_code, ''), count(*), min(terminal_at), max(terminal_at)
		FROM tasks
		WHERE status IN ('failed', 'dead_letter') AND terminal_at >= $1 AND terminal_at < $2
		GROUP BY kind, COALESCE(last_error_code, '')
		ORDER BY count(*) DESC, kind, 2 LIMIT 50`, from, to)
	if err != nil {
		return report, ErrStorage
	}
	for rows.Next() {
		var t failureTaskJSON
		if err := rows.Scan(&t.Kind, &t.ErrorCode, &t.Count, &t.FirstAt, &t.LastAt); err != nil {
			rows.Close()
			return report, ErrStorage
		}
		report.Tasks = append(report.Tasks, t)
	}
	rows.Close()
	if rows.Err() != nil {
		return report, ErrStorage
	}

	counts := func(f audit.Filter, by audit.GroupBy) ([]failureCountJSON, error) {
		f.From, f.To = &from, &to
		list, err := audit.CountBy(ctx, h.pool, f, by)
		if err != nil {
			return nil, ErrStorage
		}
		out := make([]failureCountJSON, 0, len(list))
		for _, c := range list {
			out = append(out, failureCountJSON{Key: c.Key, Count: c.Count, FirstAt: c.FirstAt, LastAt: c.LastAt})
		}
		return out, nil
	}
	if report.Restarts, err = counts(audit.Filter{ActionPrefix: "training.recover", Outcome: audit.OutcomeOK}, audit.ByAction); err != nil {
		return report, err
	}
	if report.RejectedLogins, err = counts(audit.Filter{ActionPrefix: "auth.login", Outcome: audit.OutcomeRejected}, audit.ByReason); err != nil {
		return report, err
	}
	if report.AuditedErrors, err = counts(audit.Filter{Outcome: audit.OutcomeError}, audit.ByAction); err != nil {
		return report, err
	}
	stats := h.load.Window.Snapshot()
	report.ServerErrorsAPI.Since, report.ServerErrorsAPI.Count = stats.Since, stats.Errors5xxSinceStart
	return report, nil
}

func (h *Handlers) getFailures(w http.ResponseWriter, r *http.Request) {
	from, to, ok := parsePeriod(r, time.Now().UTC())
	if !ok {
		httpapi.WriteError(w, r, httpapi.CodeValidationFailed, "invalid period", nil)
		return
	}
	report, err := h.buildFailures(r.Context(), from, to)
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to build the failures report", nil)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

// exportFailures writes the same report as one flat CSV: a `section`
// column tells the parts apart.
func (h *Handlers) exportFailures(w http.ResponseWriter, r *http.Request) {
	from, to, ok := parsePeriod(r, time.Now().UTC())
	if !ok {
		httpapi.WriteError(w, r, httpapi.CodeValidationFailed, "invalid period", nil)
		return
	}
	report, err := h.buildFailures(r.Context(), from, to)
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to build the failures report", nil)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="failures.csv"`)
	w.WriteHeader(http.StatusOK)
	cw := csv.NewWriter(w)
	stamp := func(t time.Time) string { return t.UTC().Format(time.RFC3339) }
	_ = cw.Write([]string{"section", "key", "detail", "count", "first_at", "last_at"})
	for _, t := range report.Tasks {
		_ = cw.Write([]string{"tasks", t.Kind, t.ErrorCode, strconv.Itoa(t.Count), stamp(t.FirstAt), stamp(t.LastAt)})
	}
	for _, section := range []struct {
		name string
		rows []failureCountJSON
	}{{"restarts", report.Restarts}, {"rejected_logins", report.RejectedLogins}, {"audited_errors", report.AuditedErrors}} {
		for _, c := range section.rows {
			_ = cw.Write([]string{section.name, c.Key, "", strconv.Itoa(c.Count), stamp(c.FirstAt), stamp(c.LastAt)})
		}
	}
	_ = cw.Write([]string{"server_errors", "since " + stamp(report.ServerErrorsAPI.Since), "", strconv.Itoa(report.ServerErrorsAPI.Count), "", ""})
	cw.Flush()
}
