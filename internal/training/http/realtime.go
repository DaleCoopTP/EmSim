package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	authhttp "emsim/internal/auth/http"
	"emsim/internal/platform/httpapi"
	"emsim/internal/platform/realtime"
	"emsim/internal/training"

	"github.com/google/uuid"
)

// heartbeatInterval is ADR-018's SSE heartbeat ("heartbeat 15 с").
const heartbeatInterval = 15 * time.Second

// presenceTracker is RFC-001 §7.7's best-effort, single-process presence:
// "online" means at least one /my/stream connection from that user is
// currently open on this api process — not a durable PostgreSQL state,
// and lost entirely on restart (the same simplification recovery/C6
// already applies to open items). A ref count (not a bool) survives a
// trainee with more than one tab open.
type presenceTracker struct {
	mu    sync.Mutex
	count map[uuid.UUID]int
}

func newPresenceTracker() *presenceTracker {
	return &presenceTracker{count: make(map[uuid.UUID]int)}
}

func (p *presenceTracker) connect(userID uuid.UUID) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.count[userID]++
}

func (p *presenceTracker) disconnect(userID uuid.UUID) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.count[userID] <= 1 {
		delete(p.count, userID)
		return
	}
	p.count[userID]--
}

func (p *presenceTracker) online(userID uuid.UUID) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.count[userID] > 0
}

// -------------------------------------------------------------- monitor

type monitorUserJSON struct {
	ID          string  `json:"id"`
	Login       string  `json:"login"`
	FullName    string  `json:"full_name"`
	Role        string  `json:"role"`
	ServiceCode *string `json:"service_code"`
	Level       string  `json:"level"`
	Active      bool    `json:"active"`
}

type monitorRowJSON struct {
	WorkstationNo int               `json:"workstation_no"`
	User          monitorUserJSON   `json:"user"`
	RunID         string            `json:"run_id"`
	Online        bool              `json:"online"`
	ActiveItems   []itemSummaryJSON `json:"active_items"`
	QueueLeft     int               `json:"queue_left"`
	Done          int               `json:"done"`
	LastAction    *actionJSON       `json:"last_action,omitempty"`
}

type monitorJSON struct {
	Lesson      lessonJSON       `json:"lesson"`
	ServerTime  string           `json:"server_time"`
	LastEventID string           `json:"last_event_id,omitempty"`
	Rows        []monitorRowJSON `json:"rows"`
}

func (h *Handlers) toMonitorJSON(result training.MonitorResult, now time.Time) monitorJSON {
	rows := make([]monitorRowJSON, len(result.Rows))
	for i, row := range result.Rows {
		items := make([]itemSummaryJSON, len(row.ActiveItems))
		for j, it := range row.ActiveItems {
			items[j] = toItemSummaryJSON(it, now)
		}
		rowJSON := monitorRowJSON{
			WorkstationNo: row.WorkstationNo,
			User: monitorUserJSON{
				ID: row.User.ID.String(), Login: row.User.Login, FullName: row.User.FullName,
				Role: string(row.User.Role), ServiceCode: row.User.ServiceCode, Level: string(row.User.Level), Active: row.User.Active,
			},
			RunID: row.RunID.String(), Online: h.presence.online(row.User.ID),
			ActiveItems: items, QueueLeft: row.QueueLeft, Done: row.Done,
		}
		if row.LastAction != nil {
			action := toActionJSON(*row.LastAction)
			rowJSON.LastAction = &action
		}
		rows[i] = rowJSON
	}
	return monitorJSON{
		Lesson: toLessonJSON(result.Lesson, nil), ServerTime: formatTime(now),
		LastEventID: h.hub.Cursor(), Rows: rows,
	}
}

// getMonitor is GET /lessons/{lessonId}/monitor (ADR-018): the snapshot
// a client reads once its SSE stream is already open and buffering —
// never the SSE payload itself, which never carries more than
// identifiers.
func (h *Handlers) getMonitor(w http.ResponseWriter, r *http.Request) {
	principal, _ := authhttp.PrincipalFromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("lessonId"))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeNotFound, "lesson not found", nil)
		return
	}
	result, err := h.training.Monitor(r.Context(), principal, id)
	if err != nil {
		writeTrainingError(w, r, err)
		return
	}
	now, err := h.training.Now(r.Context())
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to load monitor", nil)
		return
	}
	writeJSON(w, r, http.StatusOK, h.toMonitorJSON(result, now))
}

// -------------------------------------------------------------- SSE

// streamLesson is GET /lessons/{lessonId}/stream — the instructor's own
// lesson feed. Ownership is checked once, up front, via the existing
// read (Lesson) rather than duplicating that check here.
func (h *Handlers) streamLesson(w http.ResponseWriter, r *http.Request) {
	principal, _ := authhttp.PrincipalFromContext(r.Context())
	lessonID, err := uuid.Parse(r.PathValue("lessonId"))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeNotFound, "lesson not found", nil)
		return
	}
	if _, _, err := h.training.Lesson(r.Context(), principal, lessonID); err != nil {
		writeTrainingError(w, r, err)
		return
	}
	h.serveSSE(w, r, func(e realtime.Event) bool {
		return e.LessonID != nil && *e.LessonID == lessonID
	})
}

// streamMy is GET /my/stream — the trainee's own invalidation feed,
// filtered by user id rather than by lesson (a trainee only ever has
// one active run, but that run's lesson id is not known up front the
// way an instructor's path parameter gives it). Presence toggles for
// exactly the lifetime of this connection.
func (h *Handlers) streamMy(w http.ResponseWriter, r *http.Request) {
	principal, _ := authhttp.PrincipalFromContext(r.Context())
	h.presence.connect(principal.UserID)
	defer h.presence.disconnect(principal.UserID)
	h.serveSSE(w, r, func(e realtime.Event) bool {
		return e.UserID != nil && *e.UserID == principal.UserID
	})
}

// serveSSE is both stream endpoints' shared loop (RFC-001 §7.7): a
// fresh connection gets stream.ready and nothing else — the client is
// expected to then read a snapshot (monitor, or my/run+my/items) before
// trusting anything this stream sends afterward. A reconnect (Last-
// Event-ID, or ?cursor for a brand-new EventSource — the header takes
// priority when both are present) either replays what it missed or, if
// that cannot be proven gap-free, gets resync and must redo the same
// snapshot dance. match filters the shared Hub to only this
// subscriber's own events; nothing case-specific about lesson vs.
// trainee leaks past that point.
func (h *Handlers) serveSSE(w http.ResponseWriter, r *http.Request, match func(realtime.Event) bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "streaming is not supported", nil)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	cursor := h.hub.Cursor()
	writeSSE(w, "stream.ready", cursor, map[string]string{"cursor": cursor})
	flusher.Flush()

	lastEventID := r.Header.Get("Last-Event-ID")
	if lastEventID == "" {
		lastEventID = r.URL.Query().Get("cursor")
	}
	if lastEventID != "" {
		h.replaySince(w, lastEventID, match, &cursor)
		flusher.Flush()
	}

	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			// Also a self-heal re-check, not just a keep-alive ping: a
			// Publish that lands in the narrow gap between an earlier
			// wake's replaySince() (which reads the buffer as of that
			// call, then returns) and this loop re-arming h.hub.Wait()
			// on the next iteration closes a waiters channel nobody is
			// blocked on yet — the missed wake is not a lost event
			// (Since(cursor) always catches up from cursor, whenever
			// next called), but with nothing here re-checking it, this
			// connection would otherwise only catch up on the *next*
			// Publish anywhere in the whole process (Wait() is
			// hub-global), which self-heals almost instantly in a busy
			// classroom but has no bound at all in an otherwise-quiet
			// one. Re-checking Since(cursor) on every heartbeat caps
			// that staleness at heartbeatInterval even in the fully
			// idle case.
			if h.replaySince(w, cursor, match, &cursor) {
				flusher.Flush()
				continue
			}
			fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		case <-h.hub.Wait():
			if h.replaySince(w, cursor, match, &cursor) {
				flusher.Flush()
			}
		}
	}
}

// replaySince writes every matching event after cursor (or a resync if
// the gap since cursor cannot be proven complete), advances *cursor,
// and reports whether it wrote anything worth flushing.
func (h *Handlers) replaySince(w http.ResponseWriter, cursor string, match func(realtime.Event) bool, out *string) bool {
	events, newCursor, resync := h.hub.Since(cursor)
	*out = newCursor
	if resync {
		writeSSE(w, "resync", newCursor, map[string]string{"cursor": newCursor})
		return true
	}
	wrote := false
	for _, e := range events {
		if !match(e) {
			continue
		}
		id := fmt.Sprintf("%d:%d", e.Epoch, e.Counter)
		writeSSE(w, "invalidate", id, invalidationPayload(e))
		wrote = true
	}
	return wrote
}

func invalidationPayload(e realtime.Event) map[string]string {
	payload := map[string]string{}
	if e.LessonID != nil {
		payload["lesson_id"] = e.LessonID.String()
	}
	if e.UserID != nil {
		payload["user_id"] = e.UserID.String()
	}
	if e.ItemID != nil {
		payload["item_id"] = e.ItemID.String()
	}
	return payload
}

func writeSSE(w http.ResponseWriter, event, id string, data any) {
	body, err := json.Marshal(data)
	if err != nil {
		body = []byte(`{}`)
	}
	fmt.Fprintf(w, "event: %s\nid: %s\ndata: %s\n\n", event, id, body)
}
