// Package http is the training module's HTTP adapter (RFC-001 §5's
// lessons/trainee route groups, slice-planning.md §4's slice 3). It
// reuses internal/auth/http's SessionMiddleware/RequireRole rather than
// reimplementing session handling, matching internal/content/http's own
// convention (every module's HTTP layer sits behind the same auth
// boundary).
//
// GET /items/{itemId} is the one route both an instructor (with
// reference) and a trainee (without it) may call — auth.GroupItemRead
// (added alongside this file) is the group RequireRole checks; the
// handler itself still tells the two views apart and enforces ownership
// through the application service, which is where CLAUDE.md places that
// check ("middleware проверяет роль по маршруту, обработчик —
// принадлежность").
package http

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"emsim/internal/auth"
	authhttp "emsim/internal/auth/http"
	"emsim/internal/content"
	"emsim/internal/media"
	"emsim/internal/platform/httpapi"
	"emsim/internal/platform/realtime"
	"emsim/internal/training"

	"github.com/google/uuid"
)

// trainingService is the subset of *training.Service Handlers need —
// declared here (their consumer), matching internal/content/http's own
// convention.
type trainingService interface {
	CreateLesson(ctx context.Context, actor auth.Principal, in training.LessonCreate, requestID string) (training.Lesson, error)
	ReplaceAssignments(ctx context.Context, actor auth.Principal, lessonID uuid.UUID, inputs []training.AssignmentInput, requestID string) (training.Lesson, error)
	Start(ctx context.Context, actor auth.Principal, lessonID uuid.UUID, requestID string) (training.Lesson, error)
	Stop(ctx context.Context, actor auth.Principal, lessonID uuid.UUID, reason *string, requestID string) (training.Lesson, error)
	Monitor(ctx context.Context, actor auth.Principal, lessonID uuid.UUID) (training.MonitorResult, error)
	Execute(ctx context.Context, actor auth.Principal, itemID uuid.UUID, cmd training.Command, requestID string) (training.Receipt, error)
	MyRun(ctx context.Context, actor auth.Principal) (training.Run, training.Lesson, int, error)
	MyItems(ctx context.Context, actor auth.Principal) ([]training.Item, error)
	ItemForTrainee(ctx context.Context, actor auth.Principal, itemID uuid.UUID) (training.Item, []training.Action, []training.DeliveredEvent, error)
	ItemForInstructor(ctx context.Context, actor auth.Principal, itemID uuid.UUID) (training.Item, []training.Action, []training.DeliveredEvent, content.Body, error)
	RunActions(ctx context.Context, actor auth.Principal, lessonID, runID uuid.UUID) ([]training.Action, error)
	ListLessons(ctx context.Context, actor auth.Principal, state *training.LessonState) ([]training.Lesson, error)
	Lesson(ctx context.Context, actor auth.Principal, lessonID uuid.UUID) (training.Lesson, []training.Assignment, error)
	LessonOptions(ctx context.Context) (training.LessonOptionsResult, error)
	Now(ctx context.Context) (time.Time, error)
	UploadRecording(ctx context.Context, actor auth.Principal, itemID, callID uuid.UUID, blob training.Blob) error
	RecordingForInstructor(ctx context.Context, actor auth.Principal, itemID, callID uuid.UUID) (training.Blob, error)
	VoicePhraseForTrainee(ctx context.Context, actor auth.Principal, itemID uuid.UUID, contactKey, phrase string) (training.Blob, error)
}

// authenticator is the session-verification port SessionMiddleware needs
// — see internal/content/http's own copy of this interface for why it is
// redeclared per module rather than shared.
type authenticator interface {
	Authenticate(ctx context.Context, token string) (auth.Principal, error)
}

// Handlers owns the "lessons" and "trainee" route groups, plus the one
// item_read route both roles share.
type Handlers struct {
	training     trainingService
	auth         authenticator
	cookieSecure bool
	hub          *realtime.Hub
	presence     *presenceTracker
}

func NewHandlers(trainingService trainingService, authService authenticator, cookieSecure bool, hub *realtime.Hub) *Handlers {
	return &Handlers{training: trainingService, auth: authService, cookieSecure: cookieSecure, hub: hub, presence: newPresenceTracker()}
}

// Register adds this package's routes to mux.
func (h *Handlers) Register(mux *http.ServeMux) {
	lessons := func(handler http.HandlerFunc) http.Handler {
		return authhttp.SessionMiddleware(h.auth, h.cookieSecure)(authhttp.RequireRole(auth.GroupLessons)(handler))
	}
	trainee := func(handler http.HandlerFunc) http.Handler {
		return authhttp.SessionMiddleware(h.auth, h.cookieSecure)(authhttp.RequireRole(auth.GroupTrainee)(handler))
	}
	itemRead := func(handler http.HandlerFunc) http.Handler {
		return authhttp.SessionMiddleware(h.auth, h.cookieSecure)(authhttp.RequireRole(auth.GroupItemRead)(handler))
	}

	mux.Handle("GET /api/v1/lessons", lessons(h.listLessons))
	mux.Handle("POST /api/v1/lessons", lessons(h.createLesson))
	// The literal /lessons/options path is registered alongside
	// /lessons/{lessonId} — Go 1.22's ServeMux gives a literal segment
	// priority over a wildcard at the same position regardless of
	// registration order, so "options" is never captured as a lessonId.
	mux.Handle("GET /api/v1/lessons/options", lessons(h.lessonOptions))
	mux.Handle("GET /api/v1/lessons/{lessonId}", lessons(h.getLesson))
	mux.Handle("PUT /api/v1/lessons/{lessonId}/assignments", lessons(h.replaceAssignments))
	mux.Handle("POST /api/v1/lessons/{lessonId}/start", lessons(h.startLesson))
	mux.Handle("POST /api/v1/lessons/{lessonId}/stop", lessons(h.stopLesson))
	mux.Handle("GET /api/v1/lessons/{lessonId}/monitor", lessons(h.getMonitor))
	mux.Handle("GET /api/v1/lessons/{lessonId}/stream", lessons(h.streamLesson))
	mux.Handle("GET /api/v1/lessons/{lessonId}/runs/{runId}/actions", lessons(h.runActions))

	mux.Handle("GET /api/v1/my/run", trainee(h.myRun))
	mux.Handle("GET /api/v1/my/items", trainee(h.myItems))
	mux.Handle("GET /api/v1/my/stream", trainee(h.streamMy))
	mux.Handle("POST /api/v1/items/{itemId}/actions", trainee(h.execute))
	mux.Handle("PUT /api/v1/items/{itemId}/calls/{callId}/recording", trainee(h.uploadRecording))
	mux.Handle("GET /api/v1/items/{itemId}/calls/{callId}/recording", itemRead(h.downloadRecording))
	mux.Handle("GET /api/v1/items/{itemId}/contacts/{contactKey}/phrases/{phrase}", trainee(h.voicePhrase))

	mux.Handle("GET /api/v1/items/{itemId}", itemRead(h.getItem))
}

const maxRecordingBytes int64 = 10 << 20

func (h *Handlers) uploadRecording(w http.ResponseWriter, r *http.Request) {
	principal, _ := authhttp.PrincipalFromContext(r.Context())
	itemID, err := uuid.Parse(r.PathValue("itemId"))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeValidationFailed, "invalid item id", nil)
		return
	}
	callID, err := uuid.Parse(r.PathValue("callId"))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeValidationFailed, "invalid call id", nil)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRecordingBytes+1024)
	if err := r.ParseMultipartForm(maxRecordingBytes + 1024); err != nil {
		httpapi.WriteError(w, r, httpapi.CodePayloadTooLarge, "recording is too large", nil)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeValidationFailed, "file is required", nil)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxRecordingBytes+1))
	if err != nil || int64(len(data)) > maxRecordingBytes {
		httpapi.WriteError(w, r, httpapi.CodePayloadTooLarge, "recording is too large", nil)
		return
	}
	mime := header.Header.Get("Content-Type")
	if mime != "audio/webm" && mime != "audio/ogg" && mime != "audio/wav" {
		httpapi.WriteError(w, r, httpapi.CodeUnsupportedMediaType, "unsupported recording type", nil)
		return
	}
	if !validAudioMagic(mime, data) {
		httpapi.WriteError(w, r, httpapi.CodeUnsupportedMediaType, "invalid recording data", nil)
		return
	}
	digest := sha256.Sum256(data)
	root := os.Getenv("BLOB_ROOT")
	files, err := media.NewFileStore(root)
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "media storage unavailable", nil)
		return
	}
	if err = files.Put(r.Context(), bytes.NewReader(data), digest, int64(len(data)), maxRecordingBytes); err != nil {
		httpapi.WriteError(w, r, httpapi.CodeValidationFailed, "recording validation failed", nil)
		return
	}
	err = h.training.UploadRecording(r.Context(), principal, itemID, callID, training.Blob{ID: uuid.New(), SHA256: digest, MIME: mime, Size: int64(len(data))})
	if err != nil {
		writeTrainingError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func validAudioMagic(mime string, data []byte) bool {
	if mime == "audio/webm" {
		return len(data) >= 4 && bytes.Equal(data[:4], []byte{0x1a, 0x45, 0xdf, 0xa3})
	}
	if mime == "audio/ogg" {
		return len(data) >= 4 && bytes.Equal(data[:4], []byte("OggS"))
	}
	return len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WAVE"))
}

func (h *Handlers) downloadRecording(w http.ResponseWriter, r *http.Request) {
	principal, _ := authhttp.PrincipalFromContext(r.Context())
	itemID, err := uuid.Parse(r.PathValue("itemId"))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeValidationFailed, "invalid item id", nil)
		return
	}
	callID, err := uuid.Parse(r.PathValue("callId"))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeValidationFailed, "invalid call id", nil)
		return
	}
	blob, err := h.training.RecordingForInstructor(r.Context(), principal, itemID, callID)
	if err != nil {
		writeTrainingError(w, r, err)
		return
	}
	files, err := media.NewFileStore(os.Getenv("BLOB_ROOT"))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "media storage unavailable", nil)
		return
	}
	f, err := files.Open(blob.SHA256)
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeNotFound, "recording not found", nil)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", blob.MIME)
	w.Header().Set("Content-Length", strconv.FormatInt(blob.Size, 10))
	http.ServeContent(w, r, "recording", blob.CreatedAt, f)
}

func (h *Handlers) voicePhrase(w http.ResponseWriter, r *http.Request) {
	principal, _ := authhttp.PrincipalFromContext(r.Context())
	itemID, err := uuid.Parse(r.PathValue("itemId"))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeValidationFailed, "invalid item id", nil)
		return
	}
	blob, err := h.training.VoicePhraseForTrainee(r.Context(), principal, itemID, r.PathValue("contactKey"), r.PathValue("phrase"))
	if err != nil {
		writeTrainingError(w, r, err)
		return
	}
	files, err := media.NewFileStore(os.Getenv("BLOB_ROOT"))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "media storage unavailable", nil)
		return
	}
	f, err := files.Open(blob.SHA256)
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeNotFound, "voice asset not found", nil)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", blob.MIME)
	w.Header().Set("Content-Length", strconv.FormatInt(blob.Size, 10))
	http.ServeContent(w, r, "voice", blob.CreatedAt, f)
}

// -------------------------------------------------------------- lessons

func (h *Handlers) listLessons(w http.ResponseWriter, r *http.Request) {
	principal, _ := authhttp.PrincipalFromContext(r.Context())
	var statePtr *training.LessonState
	if raw := r.URL.Query().Get("state"); raw != "" {
		state := training.LessonState(raw)
		switch state {
		case training.LessonDraft, training.LessonRunning, training.LessonStopped, training.LessonFinished:
			statePtr = &state
		default:
			httpapi.WriteError(w, r, httpapi.CodeValidationFailed, "invalid state filter", map[string]any{"field": "state"})
			return
		}
	}
	lessonList, err := h.training.ListLessons(r.Context(), principal, statePtr)
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to list lessons", nil)
		return
	}
	items := make([]lessonJSON, len(lessonList))
	for i, l := range lessonList {
		items[i] = toLessonJSON(l, nil)
	}
	writeJSON(w, r, http.StatusOK, items)
}

type lessonCreateRequest struct {
	ExerciseType string      `json:"exercise_type"`
	Title        string      `json:"title"`
	Mode         string      `json:"mode"`
	Level        string      `json:"level"`
	Timing       *timingJSON `json:"timing,omitempty"`
}

func (h *Handlers) createLesson(w http.ResponseWriter, r *http.Request) {
	principal, _ := authhttp.PrincipalFromContext(r.Context())
	var body lessonCreateRequest
	if err := httpapi.DecodeJSON(r, 0, &body); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	in := training.LessonCreate{
		ExerciseType: content.ExerciseType(body.ExerciseType),
		Title:        body.Title,
		Mode:         training.Mode(body.Mode),
		Level:        auth.Level(body.Level),
	}
	if body.Timing != nil {
		timing := fromTimingJSON(*body.Timing)
		in.Timing = &timing
	}
	lesson, err := h.training.CreateLesson(r.Context(), principal, in, httpapi.RequestIDFromContext(r.Context()))
	if err != nil {
		writeTrainingError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusCreated, toLessonJSON(lesson, nil))
}

func (h *Handlers) lessonOptions(w http.ResponseWriter, r *http.Request) {
	result, err := h.training.LessonOptions(r.Context())
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to load lesson options", nil)
		return
	}
	writeJSON(w, r, http.StatusOK, toLessonOptionsJSON(result))
}

func (h *Handlers) getLesson(w http.ResponseWriter, r *http.Request) {
	principal, _ := authhttp.PrincipalFromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("lessonId"))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeNotFound, "lesson not found", nil)
		return
	}
	lesson, assignments, err := h.training.Lesson(r.Context(), principal, id)
	if err != nil {
		writeTrainingError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusOK, toLessonJSON(lesson, assignments))
}

type assignmentRequest struct {
	WorkstationNo      int      `json:"workstation_no"`
	UserID             string   `json:"user_id"`
	ScenarioVersionIDs []string `json:"scenario_version_ids"`
}

func (h *Handlers) replaceAssignments(w http.ResponseWriter, r *http.Request) {
	principal, _ := authhttp.PrincipalFromContext(r.Context())
	lessonID, err := uuid.Parse(r.PathValue("lessonId"))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeNotFound, "lesson not found", nil)
		return
	}
	var body []assignmentRequest
	if err := httpapi.DecodeJSON(r, 0, &body); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	inputs := make([]training.AssignmentInput, len(body))
	for i, a := range body {
		userID, err := uuid.Parse(a.UserID)
		if err != nil {
			httpapi.WriteError(w, r, httpapi.CodeValidationFailed, "invalid user_id", map[string]any{"field": "user_id"})
			return
		}
		versionIDs := make([]uuid.UUID, len(a.ScenarioVersionIDs))
		for j, v := range a.ScenarioVersionIDs {
			versionID, err := uuid.Parse(v)
			if err != nil {
				httpapi.WriteError(w, r, httpapi.CodeValidationFailed, "invalid scenario_version_ids", map[string]any{"field": "scenario_version_ids"})
				return
			}
			versionIDs[j] = versionID
		}
		inputs[i] = training.AssignmentInput{WorkstationNo: a.WorkstationNo, UserID: userID, ScenarioVersionIDs: versionIDs}
	}
	lesson, err := h.training.ReplaceAssignments(r.Context(), principal, lessonID, inputs, httpapi.RequestIDFromContext(r.Context()))
	if err != nil {
		writeTrainingError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusOK, toLessonJSON(lesson, nil))
}

func (h *Handlers) startLesson(w http.ResponseWriter, r *http.Request) {
	principal, _ := authhttp.PrincipalFromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("lessonId"))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeNotFound, "lesson not found", nil)
		return
	}
	lesson, err := h.training.Start(r.Context(), principal, id, httpapi.RequestIDFromContext(r.Context()))
	if err != nil {
		writeTrainingError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusOK, toLessonJSON(lesson, nil))
}

type lessonStopRequest struct {
	Reason string `json:"reason,omitempty"`
}

// stopLesson is POST /lessons/{lessonId}/stop (ADR-018/RFC-001 §7.5): the
// barrier itself. It never waits for the worker that actually closes the
// remaining items — that happens in the background, per the endpoint's
// own summary ("закрытие карточек — фоновая задача").
func (h *Handlers) stopLesson(w http.ResponseWriter, r *http.Request) {
	principal, _ := authhttp.PrincipalFromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("lessonId"))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeNotFound, "lesson not found", nil)
		return
	}
	var body lessonStopRequest
	if r.ContentLength != 0 {
		if err := httpapi.DecodeJSON(r, 0, &body); err != nil {
			writeDecodeError(w, r, err)
			return
		}
	}
	var reason *string
	if trimmed := strings.TrimSpace(body.Reason); trimmed != "" {
		if len(trimmed) > 500 {
			httpapi.WriteError(w, r, httpapi.CodeValidationFailed, "reason too long", map[string]any{"field": "reason"})
			return
		}
		reason = &trimmed
	}
	lesson, err := h.training.Stop(r.Context(), principal, id, reason, httpapi.RequestIDFromContext(r.Context()))
	if err != nil {
		writeTrainingError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusOK, toLessonJSON(lesson, nil))
}

func (h *Handlers) runActions(w http.ResponseWriter, r *http.Request) {
	principal, _ := authhttp.PrincipalFromContext(r.Context())
	lessonID, err := uuid.Parse(r.PathValue("lessonId"))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeNotFound, "lesson not found", nil)
		return
	}
	runID, err := uuid.Parse(r.PathValue("runId"))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeNotFound, "run not found", nil)
		return
	}
	actions, err := h.training.RunActions(r.Context(), principal, lessonID, runID)
	if err != nil {
		writeTrainingError(w, r, err)
		return
	}
	items := make([]actionJSON, len(actions))
	for i, a := range actions {
		items[i] = toActionJSON(a)
	}
	writeJSON(w, r, http.StatusOK, items)
}

// -------------------------------------------------------------- trainee

func (h *Handlers) myRun(w http.ResponseWriter, r *http.Request) {
	principal, _ := authhttp.PrincipalFromContext(r.Context())
	run, lesson, queueTotal, err := h.training.MyRun(r.Context(), principal)
	if err != nil {
		if errors.Is(err, training.ErrNotFound) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to load run", nil)
		return
	}
	items, err := h.training.MyItems(r.Context(), principal)
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to load run", nil)
		return
	}
	now, err := h.training.Now(r.Context())
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to load run", nil)
		return
	}
	var currentItemID *string
	for _, it := range items {
		if it.State == training.ItemClosed || it.State == training.ItemInterrupted {
			continue
		}
		id := it.ID.String()
		currentItemID = &id
		break
	}
	queueLeft := queueTotal - run.QueueCursor
	if queueLeft < 0 {
		queueLeft = 0
	}
	writeJSON(w, r, http.StatusOK, myRunJSON{
		ExerciseType: string(run.ExerciseType), RunID: run.ID.String(), Lesson: toLessonJSON(lesson, nil),
		Mode: string(run.Mode), WorkstationNo: run.WorkstationNo, CurrentItemID: currentItemID,
		QueueLeft: queueLeft, ServerTime: formatTime(now),
	})
}

func (h *Handlers) myItems(w http.ResponseWriter, r *http.Request) {
	principal, _ := authhttp.PrincipalFromContext(r.Context())
	items, err := h.training.MyItems(r.Context(), principal)
	if err != nil {
		writeTrainingError(w, r, err)
		return
	}
	out := make([]itemSummaryJSON, len(items))
	for i, it := range items {
		out[i] = toItemSummaryJSON(it)
	}
	writeJSON(w, r, http.StatusOK, out)
}

// getItem is the one route both an instructor and a trainee may call
// (auth.GroupItemRead) — ownership and the reference/no-reference split
// are decided here from the caller's role, then enforced by whichever
// service method that role's branch calls (ItemForTrainee never returns
// a reference at all; ItemForInstructor is the only path that can).
func (h *Handlers) getItem(w http.ResponseWriter, r *http.Request) {
	principal, _ := authhttp.PrincipalFromContext(r.Context())
	itemID, err := uuid.Parse(r.PathValue("itemId"))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeNotFound, "item not found", nil)
		return
	}
	now, err := h.training.Now(r.Context())
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to load item", nil)
		return
	}
	switch principal.Role {
	case auth.RoleTrainee:
		item, actions, events, err := h.training.ItemForTrainee(r.Context(), principal, itemID)
		if err != nil {
			writeTrainingError(w, r, err)
			return
		}
		writeJSON(w, r, http.StatusOK, toItemJSON(item, actions, events, nil, now))
	case auth.RoleInstructor:
		item, actions, events, body, err := h.training.ItemForInstructor(r.Context(), principal, itemID)
		if err != nil {
			writeTrainingError(w, r, err)
			return
		}
		writeJSON(w, r, http.StatusOK, toItemJSON(item, actions, events, &body.Reference, now))
	default:
		httpapi.WriteError(w, r, httpapi.CodeForbidden, "insufficient role", nil)
	}
}

type commandRequest struct {
	CommandID   string          `json:"command_id"`
	ExpectedSeq int64           `json:"expected_seq"`
	Type        string          `json:"type"`
	Payload     json.RawMessage `json:"payload"`
	ClientAt    *time.Time      `json:"client_at,omitempty"`
}

// execute is POST /items/{itemId}/actions. A structurally malformed body
// (bad JSON, an invalid command_id, a negative expected_seq) is a plain
// 400/422 the caller never authorized past — it is not written to the
// journal (training/domain.go's own note on RejectInvalidPayload: only a
// well-formed-but-domain-invalid payload reaches Exercise.Decide).
func (h *Handlers) execute(w http.ResponseWriter, r *http.Request) {
	principal, _ := authhttp.PrincipalFromContext(r.Context())
	itemID, err := uuid.Parse(r.PathValue("itemId"))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeNotFound, "item not found", nil)
		return
	}
	var body commandRequest
	if err := httpapi.DecodeJSON(r, 0, &body); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	commandID, err := uuid.Parse(body.CommandID)
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInvalidRequest, "command_id must be a uuid", map[string]any{"field": "command_id"})
		return
	}
	if body.ExpectedSeq < 0 {
		httpapi.WriteError(w, r, httpapi.CodeInvalidRequest, "expected_seq must not be negative", map[string]any{"field": "expected_seq"})
		return
	}
	payload := body.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	cmd := training.Command{
		CommandID: commandID, ExpectedSeq: body.ExpectedSeq, Type: training.CommandType(body.Type),
		Payload: payload, ClientAt: body.ClientAt,
	}
	receipt, err := h.training.Execute(r.Context(), principal, itemID, cmd, httpapi.RequestIDFromContext(r.Context()))
	if err != nil {
		writeExecuteError(w, r, err)
		return
	}
	status := http.StatusOK
	if receipt.Outcome == training.OutcomeRejected && receipt.ErrorCode != nil {
		status = receipt.ErrorCode.HTTPStatus()
	}
	writeJSON(w, r, status, receipt)
}

func writeExecuteError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, training.ErrNotFound):
		httpapi.WriteError(w, r, httpapi.CodeNotFound, "item not found", nil)
	case errors.Is(err, training.ErrWorkstationMismatch):
		httpapi.WriteError(w, r, httpapi.CodeForbidden, "workstation mismatch", map[string]any{"reason": "workstation_mismatch"})
	case errors.Is(err, training.ErrCommandIDConflict):
		httpapi.WriteError(w, r, httpapi.CodeCommandIDConflict, "command_id already used for a different request", nil)
	default:
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "command failed", nil)
	}
}

// writeTrainingError maps a read/administrative call's domain errors —
// distinct from writeExecuteError, since command_id_conflict/workstation
// mismatch on read paths carry different meaning (there is no receipt to
// protect here).
func writeTrainingError(w http.ResponseWriter, r *http.Request, err error) {
	var ve *training.ValidationError
	switch {
	case errors.Is(err, training.ErrNotFound):
		httpapi.WriteError(w, r, httpapi.CodeNotFound, "not found", nil)
	case errors.Is(err, training.ErrWorkstationMismatch):
		httpapi.WriteError(w, r, httpapi.CodeForbidden, "workstation mismatch", map[string]any{"reason": "workstation_mismatch"})
	case errors.Is(err, training.ErrRecordingConflict):
		httpapi.WriteError(w, r, httpapi.CodeRecordingConflict, "recording does not match the declared manifest", nil)
	case errors.Is(err, training.ErrRecordingDeadlinePassed):
		httpapi.WriteError(w, r, httpapi.CodeRecordingDeadlinePassed, "recording upload deadline passed", nil)
	case errors.Is(err, training.ErrConflict):
		httpapi.WriteError(w, r, httpapi.CodeConflict, "conflict", nil)
	case errors.As(err, &ve):
		httpapi.WriteError(w, r, httpapi.CodeValidationFailed, "validation failed", map[string]any{"field": ve.Field, "reason": ve.Reason})
	default:
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "operation failed", nil)
	}
}

func writeDecodeError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, httpapi.ErrBodyTooLarge) {
		httpapi.WriteError(w, r, httpapi.CodePayloadTooLarge, "request body too large", nil)
		return
	}
	httpapi.WriteError(w, r, httpapi.CodeInvalidRequest, "malformed request body", nil)
}

// -------------------------------------------------------------- JSON DTOs

type timingJSON struct {
	OpenS       int  `json:"open_s"`
	PrimaryS    int  `json:"primary_s"`
	CompleteS   int  `json:"complete_s"`
	SpawnEveryS *int `json:"spawn_every_s,omitempty"`
}

func toTimingJSON(t training.Timing) timingJSON {
	return timingJSON{OpenS: t.OpenS, PrimaryS: t.PrimaryS, CompleteS: t.CompleteS, SpawnEveryS: t.SpawnEveryS}
}

func fromTimingJSON(t timingJSON) training.Timing {
	return training.Timing{OpenS: t.OpenS, PrimaryS: t.PrimaryS, CompleteS: t.CompleteS, SpawnEveryS: t.SpawnEveryS}
}

type assignmentJSON struct {
	WorkstationNo      int      `json:"workstation_no"`
	UserID             string   `json:"user_id"`
	ScenarioVersionIDs []string `json:"scenario_version_ids"`
}

func toAssignmentJSON(a training.Assignment) assignmentJSON {
	ids := make([]string, len(a.ScenarioVersionIDs))
	for i, id := range a.ScenarioVersionIDs {
		ids[i] = id.String()
	}
	return assignmentJSON{WorkstationNo: a.WorkstationNo, UserID: a.UserID.String(), ScenarioVersionIDs: ids}
}

// lessonJSON is openapi.yaml's Lesson (LessonCreate + id/state/epoch/...).
// assignments is only populated by callers that already loaded them
// (GET /lessons/{id}); the list endpoint passes nil and the field is
// omitted, matching Lesson.assignments being optional in the contract.
type lessonJSON struct {
	ID            string           `json:"id"`
	ExerciseType  string           `json:"exercise_type"`
	Title         string           `json:"title"`
	Mode          string           `json:"mode"`
	Level         string           `json:"level"`
	State         string           `json:"state"`
	Epoch         int64            `json:"epoch"`
	Timing        timingJSON       `json:"timing"`
	RubricVersion string           `json:"rubric_version,omitempty"`
	Assignments   []assignmentJSON `json:"assignments,omitempty"`
	StartedAt     *string          `json:"started_at"`
	StoppedAt     *string          `json:"stopped_at"`
	StopReason    *string          `json:"stop_reason,omitempty"`
}

func toLessonJSON(l training.Lesson, assignments []training.Assignment) lessonJSON {
	out := lessonJSON{
		ID: l.ID.String(), ExerciseType: string(l.ExerciseType), Title: l.Title,
		Mode: string(l.Mode), Level: string(l.Level), State: string(l.State),
		Epoch: l.Epoch, Timing: toTimingJSON(l.Timing), RubricVersion: l.RubricVersion,
		StartedAt: formatTimePtr(l.StartedAt), StoppedAt: formatTimePtr(l.StoppedAt), StopReason: l.StopReason,
	}
	if assignments != nil {
		out.Assignments = make([]assignmentJSON, len(assignments))
		for i, a := range assignments {
			out.Assignments[i] = toAssignmentJSON(a)
		}
	}
	return out
}

type lessonOptionTraineeJSON struct {
	ID          string  `json:"id"`
	FullName    string  `json:"full_name"`
	ServiceCode *string `json:"service_code"`
	ServiceName *string `json:"service_name,omitempty"`
}

type lessonOptionWorkstationJSON struct {
	ID     string `json:"id"`
	Number int    `json:"number"`
	Label  string `json:"label,omitempty"`
}

type lessonOptionsJSON struct {
	Trainees     []lessonOptionTraineeJSON     `json:"trainees"`
	Workstations []lessonOptionWorkstationJSON `json:"workstations"`
}

func toLessonOptionsJSON(r training.LessonOptionsResult) lessonOptionsJSON {
	out := lessonOptionsJSON{
		Trainees:     make([]lessonOptionTraineeJSON, len(r.Trainees)),
		Workstations: make([]lessonOptionWorkstationJSON, len(r.Workstations)),
	}
	for i, t := range r.Trainees {
		item := lessonOptionTraineeJSON{ID: t.ID.String(), FullName: t.FullName, ServiceCode: t.ServiceCode}
		if t.ServiceName != "" {
			name := t.ServiceName
			item.ServiceName = &name
		}
		out.Trainees[i] = item
	}
	for i, ws := range r.Workstations {
		out.Workstations[i] = lessonOptionWorkstationJSON{ID: ws.ID.String(), Number: ws.Number, Label: ws.Label}
	}
	return out
}

type myRunJSON struct {
	ExerciseType  string     `json:"exercise_type"`
	RunID         string     `json:"run_id"`
	Lesson        lessonJSON `json:"lesson"`
	Mode          string     `json:"mode"`
	WorkstationNo int        `json:"workstation_no"`
	CurrentItemID *string    `json:"current_item_id"`
	QueueLeft     int        `json:"queue_left"`
	ServerTime    string     `json:"server_time"`
}

type deadlinesJSON struct {
	OpenAt     string  `json:"open_at"`
	PrimaryAt  string  `json:"primary_at"`
	CompleteAt *string `json:"complete_at,omitempty"`
}

func toDeadlinesJSON(d training.Deadlines) deadlinesJSON {
	return deadlinesJSON{OpenAt: formatTime(d.OpenAt), PrimaryAt: formatTime(d.PrimaryAt), CompleteAt: formatTimePtr(d.CompleteAt)}
}

// itemSummaryJSON is openapi.yaml's ItemSummary — the trainee's card-list
// projection. It carries no reference, hints, pilot_goal or
// field_corrections: those only ever appear on the instructor's Item.reference
// (itemJSON below), never here.
type itemSummaryJSON struct {
	ID            string             `json:"id"`
	State         string             `json:"state"`
	Reaction      string             `json:"reaction"`
	Seq           int64              `json:"seq"`
	CardNumber    string             `json:"card_number"`
	IncidentType  string             `json:"incident_type,omitempty"`
	AddressShort  string             `json:"address_short,omitempty"`
	OfferedAt     string             `json:"offered_at"`
	OpenedAt      *string            `json:"opened_at"`
	ClosedAt      *string            `json:"closed_at"`
	CloseReason   *string            `json:"close_reason"`
	Deadlines     deadlinesJSON      `json:"deadlines"`
	PrimaryAt     *string            `json:"primary_at"`
	Interruptions []interruptionJSON `json:"interruptions"`
}

// interruptionJSON is openapi.yaml's ItemSummary.interruptions entry
// shape (also DeliveredEvent's sibling on the Item schema) — a plain
// mirror of training.Interruption, kept as its own type only because the
// domain type's json tags are for jsonb storage, not the public API (in
// this case they happen to already match, but the two are conceptually
// different surfaces).
type interruptionJSON struct {
	RecoveryID string `json:"recovery_id"`
	Cause      string `json:"cause"`
	DetectedAt string `json:"detected_at"`
}

func toInterruptionsJSON(interruptions []training.Interruption) []interruptionJSON {
	out := make([]interruptionJSON, len(interruptions))
	for i, in := range interruptions {
		out[i] = interruptionJSON{RecoveryID: in.RecoveryID.String(), Cause: in.Cause, DetectedAt: formatTime(in.DetectedAt)}
	}
	return out
}

func toItemSummaryJSON(item training.Item) itemSummaryJSON {
	var closeReason *string
	if item.CloseReason != nil {
		s := string(*item.CloseReason)
		closeReason = &s
	}
	return itemSummaryJSON{
		ID: item.ID.String(), State: string(item.State), Reaction: string(item.Reaction), Seq: item.Seq,
		CardNumber: item.Card.Number, IncidentType: item.Card.Incident.TypeName, AddressShort: item.Card.Address.Text,
		OfferedAt: formatTime(item.OfferedAt), OpenedAt: formatTimePtr(item.OpenedAt), ClosedAt: formatTimePtr(item.ClosedAt),
		CloseReason: closeReason, Deadlines: toDeadlinesJSON(item.Deadlines), PrimaryAt: formatTimePtr(item.PrimaryAt),
		Interruptions: toInterruptionsJSON(item.Interruptions),
	}
}

// cardViewJSON is openapi.yaml's CardView — the same allowlist projection
// content.CardPreview already is (internal/content/preview.go), except
// registered_at is the item's absolute server time (offered_at +
// registered_at_offset_s) rather than a relative offset: the card was
// actually issued to a trainee here, not merely previewed by an
// instructor. Every other field type is content's own preview type
// reused as-is (already json-tagged to match), so this struct only
// re-declares the one field that differs.
type cardViewJSON struct {
	Number           string                        `json:"number"`
	RegisteredAt     string                        `json:"registered_at"`
	Applicant        content.ApplicantPreview      `json:"applicant"`
	Address          content.Address               `json:"address"`
	Incident         content.IncidentPreview       `json:"incident"`
	NotificationList []content.NotificationPreview `json:"notification_list"`
	Phones           content.Phones                `json:"phones"`
	Channel          string                        `json:"channel,omitempty"`
	Contacts         []content.ContactPreview      `json:"contacts,omitempty"`
}

func toCardViewJSON(card content.CardPreview, offeredAt time.Time) cardViewJSON {
	registeredAt := offeredAt.Add(time.Duration(card.RegisteredAtOffsetS) * time.Second)
	return cardViewJSON{
		Number: card.Number, RegisteredAt: formatTime(registeredAt),
		Applicant: card.Applicant, Address: card.Address, Incident: card.Incident,
		NotificationList: card.NotificationList, Phones: card.Phones,
		Channel: card.Channel, Contacts: card.Contacts,
	}
}

type actionJSON struct {
	ID        string         `json:"id"`
	Seq       int64          `json:"seq"`
	Type      string         `json:"type"`
	Payload   map[string]any `json:"payload,omitempty"`
	Accepted  bool           `json:"accepted"`
	Rejection *string        `json:"rejection"`
	Effect    map[string]any `json:"effect"`
	ServerAt  string         `json:"server_at"`
	LogSeq    int64          `json:"log_seq"`
}

func toActionJSON(a training.Action) actionJSON {
	var payload map[string]any
	_ = json.Unmarshal(a.Payload, &payload)
	var rejection *string
	if !a.Accepted && a.Rejection != "" {
		s := string(a.Rejection)
		rejection = &s
	}
	return actionJSON{
		ID: a.ID.String(), Seq: a.Seq, Type: string(a.Type), Payload: payload,
		Accepted: a.Accepted, Rejection: rejection, Effect: a.Effect,
		ServerAt: formatTime(a.ServerAt), LogSeq: a.LogSeq,
	}
}

type commentJSON struct {
	Seq  int64  `json:"seq"`
	Text string `json:"text"`
	At   string `json:"at"`
}

// buildComments extracts add_comment/set_status(comment) accepted
// actions into the item's comment feed — the only two command types that
// ever carry free text (RFC-001 §7.4's obязательный comment on
// not_accepted is one of them).
func buildComments(actions []training.Action) []commentJSON {
	var out []commentJSON
	for _, a := range actions {
		if !a.Accepted {
			continue
		}
		var text string
		switch a.Type {
		case training.CommandAddComment:
			var payload struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(a.Payload, &payload); err == nil {
				text = payload.Text
			}
		case training.CommandSetStatus:
			var payload struct {
				Comment string `json:"comment"`
			}
			if err := json.Unmarshal(a.Payload, &payload); err == nil {
				text = payload.Comment
			}
		default:
			continue
		}
		if text == "" {
			continue
		}
		out = append(out, commentJSON{Seq: a.Seq, Text: text, At: formatTime(a.ServerAt)})
	}
	return out
}

// itemJSON is openapi.yaml's Item (ItemSummary + card/actions/events/
// calls/mode/allowed_transitions/server_time, all required, plus the
// optional comments/reference). reference is nil for a trainee's own
// view and set only when getItem takes the instructor branch — the one
// place this package ever puts scenario reference data on the wire.
type itemJSON struct {
	itemSummaryJSON
	Mode               string               `json:"mode"`
	Card               cardViewJSON         `json:"card"`
	AllowedTransitions []string             `json:"allowed_transitions"`
	Actions            []actionJSON         `json:"actions"`
	Events             []deliveredEventJSON `json:"events"`
	Calls              []callJSON           `json:"calls"`
	Comments           []commentJSON        `json:"comments,omitempty"`
	Reference          *content.Reference   `json:"reference,omitempty"`
	ServerTime         string               `json:"server_time"`
}

type callJSON struct {
	ID                        string  `json:"id"`
	ContactKey                string  `json:"contact_key"`
	StartedAt                 string  `json:"started_at"`
	EndedAt                   *string `json:"ended_at"`
	AcceptedBy                *string `json:"accepted_by"`
	Summary                   *string `json:"summary"`
	HasRecording              bool    `json:"has_recording"`
	RecordingState            string  `json:"recording_state"`
	RecordingUploadDeadlineAt *string `json:"recording_upload_deadline_at"`
}

func toCallsJSON(calls []training.Call, now time.Time) []callJSON {
	out := make([]callJSON, 0, len(calls))
	for _, c := range calls {
		var ended, deadline *string
		if c.EndedAt != nil {
			v := formatTime(*c.EndedAt)
			ended = &v
		}
		if c.RecordingUploadDeadlineAt != nil {
			v := formatTime(*c.RecordingUploadDeadlineAt)
			deadline = &v
		}
		state := c.RecordingState
		if state == training.RecordingAwaiting && c.RecordingUploadDeadlineAt != nil && now.After(*c.RecordingUploadDeadlineAt) {
			state = training.RecordingExpired
		}
		out = append(out, callJSON{ID: c.ID.String(), ContactKey: c.ContactKey, StartedAt: formatTime(c.StartedAt), EndedAt: ended, AcceptedBy: c.AcceptedBy, Summary: c.Summary, HasRecording: c.Recording != nil, RecordingState: string(state), RecordingUploadDeadlineAt: deadline})
	}
	return out
}

// deliveredEventJSON is openapi.yaml's DeliveredEvent.
type deliveredEventJSON struct {
	Key         string  `json:"key"`
	Delivery    string  `json:"delivery"`
	From        string  `json:"from,omitempty"`
	Text        string  `json:"text"`
	VoiceURL    *string `json:"voice_url"`
	DeliveredAt string  `json:"delivered_at"`
	Late        bool    `json:"late"`
}

func toDeliveredEventsJSON(events []training.DeliveredEvent) []deliveredEventJSON {
	out := make([]deliveredEventJSON, len(events))
	for i, e := range events {
		// Voice rendering (Piper TTS) is a later slice — e.Voice is
		// carried through the domain type already, but no voice_assets
		// lookup exists yet to resolve it to a URL.
		out[i] = deliveredEventJSON{
			Key: e.Key, Delivery: e.Delivery, From: e.From, Text: e.Text,
			VoiceURL: nil, DeliveredAt: formatTime(e.DeliveredAt), Late: e.Late,
		}
	}
	return out
}

func toItemJSON(item training.Item, actions []training.Action, events []training.DeliveredEvent, reference *content.Reference, now time.Time) itemJSON {
	actionItems := make([]actionJSON, len(actions))
	for i, a := range actions {
		actionItems[i] = toActionJSON(a)
	}
	transitions := item.Workflow.Transitions[item.Reaction]
	allowed := make([]string, len(transitions))
	for i, next := range transitions {
		allowed[i] = string(next)
	}
	return itemJSON{
		itemSummaryJSON:    toItemSummaryJSON(item),
		Mode:               string(item.Mode),
		Card:               toCardViewJSON(item.Card, item.OfferedAt),
		AllowedTransitions: allowed,
		Actions:            actionItems,
		Events:             toDeliveredEventsJSON(events),
		Calls:              toCallsJSON(item.Calls, now),
		Comments:           buildComments(actions),
		Reference:          reference,
		ServerTime:         formatTime(now),
	}
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

func formatTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := formatTime(*t)
	return &s
}

// writeJSON mirrors internal/content/http's own helper of the same name.
func writeJSON(w http.ResponseWriter, r *http.Request, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
