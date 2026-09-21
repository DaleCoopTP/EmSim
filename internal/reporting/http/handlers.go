package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"

	"emsim/internal/auth"
	authhttp "emsim/internal/auth/http"
	"emsim/internal/media"
	"emsim/internal/platform/httpapi"
	"emsim/internal/reporting"
	"emsim/internal/training"

	"github.com/google/uuid"
)

type reportingService interface {
	LessonReport(context.Context, uuid.UUID) (reporting.LessonReport, error)
	Results(context.Context, uuid.UUID) ([]reporting.ItemResult, error)
	Progress(context.Context, uuid.UUID) (reporting.Progress, error)
	RequestPDF(context.Context, uuid.UUID, uuid.UUID) (reporting.ReportFile, error)
	ListPDFs(context.Context, uuid.UUID) ([]reporting.ReportFile, error)
	ReportFile(context.Context, uuid.UUID) (reporting.ReportFile, error)
}

type lessonReader interface {
	Lesson(context.Context, auth.Principal, uuid.UUID) (training.Lesson, []training.Assignment, error)
}
type authenticator interface {
	Authenticate(context.Context, string) (auth.Principal, error)
}

type Handlers struct {
	reporting    reportingService
	training     lessonReader
	auth         authenticator
	cookieSecure bool
}

func NewHandlers(service reportingService, trainingService lessonReader, authService authenticator, cookieSecure bool) *Handlers {
	return &Handlers{reporting: service, training: trainingService, auth: authService, cookieSecure: cookieSecure}
}

func (h *Handlers) Register(mux *http.ServeMux) {
	reports := func(next http.HandlerFunc) http.Handler {
		return authhttp.SessionMiddleware(h.auth, h.cookieSecure)(authhttp.RequireRole(auth.GroupReports)(next))
	}
	self := func(next http.HandlerFunc) http.Handler {
		return authhttp.SessionMiddleware(h.auth, h.cookieSecure)(authhttp.RequireRole(auth.GroupTraineeSelf)(next))
	}
	mux.Handle("GET /api/v1/lessons/{lessonId}/report", reports(h.lessonReport))
	mux.Handle("GET /api/v1/lessons/{lessonId}/report.csv", reports(h.lessonCSV))
	mux.Handle("POST /api/v1/lessons/{lessonId}/report.pdf", reports(h.requestPDF))
	mux.Handle("GET /api/v1/lessons/{lessonId}/report-files", reports(h.listPDFs))
	mux.Handle("GET /api/v1/reports/{reportId}/download", reports(h.downloadPDF))
	mux.Handle("GET /api/v1/my/results", self(h.results))
	mux.Handle("GET /api/v1/my/progress", self(h.progress))
}

func (h *Handlers) lessonReport(w http.ResponseWriter, r *http.Request) {
	report, ok := h.authorizedReport(w, r)
	if ok {
		writeJSON(w, http.StatusOK, report)
	}
}
func (h *Handlers) lessonCSV(w http.ResponseWriter, r *http.Request) {
	report, ok := h.authorizedReport(w, r)
	if !ok {
		return
	}
	body, err := reporting.CSV(report)
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "report export failed", nil)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=lesson-report.csv")
	_, _ = w.Write(body)
}
func (h *Handlers) results(w http.ResponseWriter, r *http.Request) {
	principal, _ := authhttp.PrincipalFromContext(r.Context())
	values, err := h.reporting.Results(r.Context(), principal.UserID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, values)
}
func (h *Handlers) progress(w http.ResponseWriter, r *http.Request) {
	principal, _ := authhttp.PrincipalFromContext(r.Context())
	value, err := h.reporting.Progress(r.Context(), principal.UserID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (h *Handlers) requestPDF(w http.ResponseWriter, r *http.Request) {
	id, ok := h.authorizedLesson(w, r)
	if !ok {
		return
	}
	principal, _ := authhttp.PrincipalFromContext(r.Context())
	file, err := h.reporting.RequestPDF(r.Context(), id, principal.UserID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, publicFile(file))
}
func (h *Handlers) listPDFs(w http.ResponseWriter, r *http.Request) {
	id, ok := h.authorizedLesson(w, r)
	if !ok {
		return
	}
	files, err := h.reporting.ListPDFs(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	for i := range files {
		files[i] = publicFile(files[i])
	}
	writeJSON(w, http.StatusOK, files)
}
func (h *Handlers) downloadPDF(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("reportId"))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeNotFound, "report not found", nil)
		return
	}
	file, err := h.reporting.ReportFile(r.Context(), id)
	if err != nil || file.Blob == nil || file.Status != reporting.ReportReady {
		httpapi.WriteError(w, r, httpapi.CodeNotFound, "report not found", nil)
		return
	}
	principal, _ := authhttp.PrincipalFromContext(r.Context())
	if _, _, err := h.training.Lesson(r.Context(), principal, file.LessonID); err != nil {
		writeError(w, r, err)
		return
	}
	files, err := media.NewFileStore(os.Getenv("BLOB_ROOT"))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "media storage unavailable", nil)
		return
	}
	stream, err := files.Open(file.Blob.SHA256)
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeNotFound, "report not found", nil)
		return
	}
	defer stream.Close()
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", "attachment; filename=lesson-report.pdf")
	_, _ = io.Copy(w, stream)
}

func (h *Handlers) authorizedReport(w http.ResponseWriter, r *http.Request) (reporting.LessonReport, bool) {
	id, ok := h.authorizedLesson(w, r)
	if !ok {
		return reporting.LessonReport{}, false
	}
	report, err := h.reporting.LessonReport(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return reporting.LessonReport{}, false
	}
	return report, true
}

func (h *Handlers) authorizedLesson(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("lessonId"))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeNotFound, "lesson not found", nil)
		return uuid.Nil, false
	}
	principal, _ := authhttp.PrincipalFromContext(r.Context())
	lesson, _, err := h.training.Lesson(r.Context(), principal, id)
	if err != nil {
		writeError(w, r, err)
		return uuid.Nil, false
	}
	if lesson.State != training.LessonFinished {
		httpapi.WriteError(w, r, httpapi.CodeConflict, "lesson is not finished", nil)
		return uuid.Nil, false
	}
	return id, true
}

func publicFile(file reporting.ReportFile) reporting.ReportFile {
	if file.Status == reporting.ReportReady {
		value := "/api/v1/reports/" + file.ID.String() + "/download"
		file.DownloadURL = &value
	}
	return file
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, reporting.ErrNotFound) || errors.Is(err, training.ErrNotFound) {
		httpapi.WriteError(w, r, httpapi.CodeNotFound, "report not found", nil)
		return
	}
	httpapi.WriteError(w, r, httpapi.CodeInternalError, "report operation failed", nil)
}
