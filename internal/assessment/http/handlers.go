// Package http exposes the instructor-only assessment review endpoints.
package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"emsim/internal/assessment"
	"emsim/internal/auth"
	authhttp "emsim/internal/auth/http"
	"emsim/internal/content"
	"emsim/internal/platform/httpapi"
	"emsim/internal/training"

	"github.com/google/uuid"
)

type assessmentService interface {
	Get(context.Context, uuid.UUID) (assessment.Detail, error)
	ListForLesson(context.Context, uuid.UUID) ([]assessment.LessonAssessmentItem, error)
	CreateExpertRevision(context.Context, uuid.UUID, uuid.UUID, assessment.RevisionInput, string) (assessment.Assessment, error)
}

// instructorReader is intentionally the existing training authorization
// surface.  It is the authoritative owner check for an item or lesson and
// keeps reference/evidence access from leaking to another instructor.
type instructorReader interface {
	ItemForInstructor(context.Context, auth.Principal, uuid.UUID) (training.Item, []training.Action, []training.DeliveredEvent, content.Body, error)
	Lesson(context.Context, auth.Principal, uuid.UUID) (training.Lesson, []training.Assignment, error)
}

type authenticator interface {
	Authenticate(context.Context, string) (auth.Principal, error)
}

type Handlers struct {
	assessment   assessmentService
	training     instructorReader
	auth         authenticator
	cookieSecure bool
}

func NewHandlers(assessmentService assessmentService, trainingService instructorReader, authService authenticator, cookieSecure bool) *Handlers {
	return &Handlers{assessment: assessmentService, training: trainingService, auth: authService, cookieSecure: cookieSecure}
}

func (h *Handlers) Register(mux *http.ServeMux) {
	guard := func(handler http.HandlerFunc) http.Handler {
		return authhttp.SessionMiddleware(h.auth, h.cookieSecure)(authhttp.RequireRole(auth.GroupAssessment)(handler))
	}
	mux.Handle("GET /api/v1/lessons/{lessonId}/assessments", guard(h.list))
	mux.Handle("GET /api/v1/items/{itemId}/assessment", guard(h.get))
	mux.Handle("POST /api/v1/items/{itemId}/assessment/revisions", guard(h.createRevision))
}

func (h *Handlers) get(w http.ResponseWriter, r *http.Request) {
	itemID, ok := parseID(w, r, "itemId", "item")
	if !ok {
		return
	}
	principal, _ := authhttp.PrincipalFromContext(r.Context())
	if _, _, _, _, err := h.training.ItemForInstructor(r.Context(), principal, itemID); err != nil {
		writeError(w, r, err)
		return
	}
	detail, err := h.assessment.Get(r.Context(), itemID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toDetailJSON(detail))
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) {
	lessonID, ok := parseID(w, r, "lessonId", "lesson")
	if !ok {
		return
	}
	principal, _ := authhttp.PrincipalFromContext(r.Context())
	if _, _, err := h.training.Lesson(r.Context(), principal, lessonID); err != nil {
		writeError(w, r, err)
		return
	}
	rows, err := h.assessment.ListForLesson(r.Context(), lessonID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]lessonRowJSON, len(rows))
	for i := range rows {
		out[i] = toLessonRowJSON(rows[i])
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handlers) createRevision(w http.ResponseWriter, r *http.Request) {
	itemID, ok := parseID(w, r, "itemId", "item")
	if !ok {
		return
	}
	principal, _ := authhttp.PrincipalFromContext(r.Context())
	if _, _, _, _, err := h.training.ItemForInstructor(r.Context(), principal, itemID); err != nil {
		writeError(w, r, err)
		return
	}
	var body revisionJSON
	if err := httpapi.DecodeJSON(r, 0, &body); err != nil {
		if errors.Is(err, httpapi.ErrBodyTooLarge) {
			httpapi.WriteError(w, r, httpapi.CodePayloadTooLarge, "request body too large", nil)
		} else {
			httpapi.WriteError(w, r, httpapi.CodeInvalidRequest, "malformed request body", nil)
		}
		return
	}
	created, err := h.assessment.CreateExpertRevision(r.Context(), itemID, principal.UserID, body.toDomain(), httpapi.RequestIDFromContext(r.Context()))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toAssessmentJSON(created))
}

func parseID(w http.ResponseWriter, r *http.Request, name, noun string) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue(name))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeNotFound, noun+" not found", nil)
		return uuid.Nil, false
	}
	return id, true
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	var validation *assessment.ValidationError
	switch {
	case errors.Is(err, assessment.ErrNotFound), errors.Is(err, assessment.ErrNotClosed), errors.Is(err, training.ErrNotFound):
		httpapi.WriteError(w, r, httpapi.CodeNotFound, "assessment not found", nil)
	case errors.Is(err, assessment.ErrStaleRevision):
		httpapi.WriteError(w, r, httpapi.CodeStaleRevision, "assessment revision is stale", nil)
	case errors.As(err, &validation):
		httpapi.WriteError(w, r, httpapi.CodeValidationFailed, "validation failed", map[string]any{"field": validation.Field, "reason": validation.Reason})
	default:
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "assessment operation failed", nil)
	}
}

type criterionJSON struct {
	ID           string                     `json:"id"`
	Status       assessment.CriterionStatus `json:"status"`
	Score        *float64                   `json:"score"`
	Weight       float64                    `json:"weight"`
	Critical     bool                       `json:"critical"`
	EvidenceRefs []string                   `json:"evidence_refs"`
	Explanation  string                     `json:"explanation"`
}

func toCriterionJSON(c assessment.CriterionResult) criterionJSON {
	refs := c.EvidenceRefs
	if refs == nil {
		refs = []string{}
	}
	return criterionJSON{ID: c.ID, Status: c.Status, Score: c.Score, Weight: c.Weight, Critical: c.Critical, EvidenceRefs: refs, Explanation: c.Explanation}
}
func (c criterionJSON) toDomain() assessment.CriterionResult {
	return assessment.CriterionResult{ID: c.ID, Status: c.Status, Score: c.Score, Weight: c.Weight, Critical: c.Critical, EvidenceRefs: c.EvidenceRefs, Explanation: c.Explanation}
}

type assessmentJSON struct {
	ID              string                `json:"id"`
	ItemID          string                `json:"item_id"`
	Revision        int                   `json:"revision"`
	Kind            assessment.Kind       `json:"kind"`
	Status          assessment.Status     `json:"status"`
	RubricVersion   string                `json:"rubric_version"`
	RubricEffective assessment.Rubric     `json:"rubric_effective"`
	Score           *float64              `json:"score"`
	Passed          *bool                 `json:"passed"`
	Criteria        []criterionJSON       `json:"criteria"`
	CriticalErrors  []string              `json:"critical_errors"`
	Feedback        []assessment.Feedback `json:"feedback"`
	Model           *string               `json:"model"`
	CreatedBy       *string               `json:"created_by"`
	Reason          *string               `json:"reason"`
	CreatedAt       string                `json:"created_at"`
	InputID         *string               `json:"input_id"`
	BaseRevision    *int                  `json:"base_revision"`
}

func toAssessmentJSON(a assessment.Assessment) assessmentJSON {
	criteria := make([]criterionJSON, len(a.Criteria))
	for i := range a.Criteria {
		criteria[i] = toCriterionJSON(a.Criteria[i])
	}
	critical := a.CriticalErrors
	if critical == nil {
		critical = []string{}
	}
	feedback := a.Feedback
	if feedback == nil {
		feedback = []assessment.Feedback{}
	}
	var inputID, createdBy *string
	if a.InputID != nil {
		s := a.InputID.String()
		inputID = &s
	}
	if a.CreatedBy != nil {
		s := a.CreatedBy.String()
		createdBy = &s
	}
	return assessmentJSON{ID: a.ID.String(), ItemID: a.ItemID.String(), Revision: a.Revision, Kind: a.Kind, Status: a.Status,
		RubricVersion: a.RubricVersion, RubricEffective: a.RubricEffective, Score: a.Score, Passed: a.Passed,
		Criteria: criteria, CriticalErrors: critical, Feedback: feedback, Model: a.Model, CreatedBy: createdBy,
		Reason: a.Reason, CreatedAt: formatTime(a.CreatedAt), InputID: inputID, BaseRevision: a.BaseRevision}
}

type detailJSON struct {
	AutomaticState  *string           `json:"automatic_state"`
	Final           *assessmentJSON   `json:"final"`
	Revisions       []assessmentJSON  `json:"revisions"`
	RubricEffective assessment.Rubric `json:"rubric_effective"`
	Evidence        any               `json:"evidence"`
}

func toDetailJSON(d assessment.Detail) detailJSON {
	rows := make([]assessmentJSON, len(d.Revisions))
	for i := range d.Revisions {
		rows[i] = toAssessmentJSON(d.Revisions[i])
	}
	var final *assessmentJSON
	if d.Final != nil {
		x := toAssessmentJSON(*d.Final)
		final = &x
	}
	return detailJSON{AutomaticState: d.AutomaticState, Final: final, Revisions: rows, RubricEffective: d.RubricEffective, Evidence: d.Evidence}
}

type userJSON struct {
	ID          string  `json:"id"`
	Login       string  `json:"login"`
	FullName    string  `json:"full_name"`
	Role        string  `json:"role"`
	ServiceCode *string `json:"service_code"`
	Level       string  `json:"level"`
	Active      bool    `json:"active"`
}
type finalJSON struct {
	Revision int               `json:"revision"`
	Kind     assessment.Kind   `json:"kind"`
	Status   assessment.Status `json:"status"`
	Score    *float64          `json:"score"`
	Passed   *bool             `json:"passed"`
}
type lessonRowJSON struct {
	ItemID         string     `json:"item_id"`
	User           userJSON   `json:"user"`
	WorkstationNo  int        `json:"workstation_no"`
	Ordinal        int        `json:"ordinal"`
	CardNumber     string     `json:"card_number"`
	ItemState      string     `json:"item_state"`
	CloseReason    *string    `json:"close_reason"`
	ClosedAt       string     `json:"closed_at"`
	AutomaticState *string    `json:"automatic_state"`
	Final          *finalJSON `json:"final"`
}

func toLessonRowJSON(row assessment.LessonAssessmentItem) lessonRowJSON {
	out := lessonRowJSON{ItemID: row.ItemID.String(), User: userJSON{ID: row.UserID.String(), Login: row.Login, FullName: row.FullName, Role: string(auth.RoleTrainee), ServiceCode: row.ServiceCode, Level: row.Level, Active: row.Active}, WorkstationNo: row.WorkstationNo, Ordinal: row.Ordinal, CardNumber: row.CardNumber, ItemState: row.ItemState, CloseReason: row.CloseReason, ClosedAt: formatTime(row.ClosedAt), AutomaticState: row.AutomaticState}
	if row.Final != nil {
		out.Final = &finalJSON{Revision: row.Final.Revision, Kind: row.Final.Kind, Status: row.Final.Status, Score: row.Final.Score, Passed: row.Final.Passed}
	}
	return out
}

type revisionJSON struct {
	Reason        string          `json:"reason"`
	Criteria      []criterionJSON `json:"criteria"`
	ScoreOverride *float64        `json:"score_override"`
	BaseRevision  int             `json:"base_revision"`
}

func (r revisionJSON) toDomain() assessment.RevisionInput {
	out := assessment.RevisionInput{Reason: r.Reason, ScoreOverride: r.ScoreOverride, BaseRevision: r.BaseRevision, Criteria: make([]assessment.CriterionResult, len(r.Criteria))}
	for i := range r.Criteria {
		out.Criteria[i] = r.Criteria[i].toDomain()
	}
	return out
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
