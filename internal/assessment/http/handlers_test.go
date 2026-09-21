package http

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"emsim/internal/assessment"
	"emsim/internal/auth"
	authhttp "emsim/internal/auth/http"
	"emsim/internal/content"
	"emsim/internal/platform/httpapi"
	"emsim/internal/training"

	"github.com/google/uuid"
)

type fakeAuth struct{ principal auth.Principal }

func (f fakeAuth) Authenticate(_ context.Context, token string) (auth.Principal, error) {
	if token != "token" {
		return auth.Principal{}, auth.ErrSessionInvalid
	}
	return f.principal, nil
}

type fakeTraining struct {
	itemErr   error
	lessonErr error
}

func (f fakeTraining) ItemForInstructor(context.Context, auth.Principal, uuid.UUID) (training.Item, []training.Action, []training.DeliveredEvent, content.Body, error) {
	return training.Item{}, nil, nil, content.Body{}, f.itemErr
}
func (f fakeTraining) Lesson(context.Context, auth.Principal, uuid.UUID) (training.Lesson, []training.Assignment, error) {
	return training.Lesson{}, nil, f.lessonErr
}

type fakeAssessment struct {
	detail                     assessment.Detail
	rows                       []assessment.LessonAssessmentItem
	getErr, listErr, createErr error
	created                    assessment.Assessment
	received                   assessment.RevisionInput
}

func (f *fakeAssessment) Get(context.Context, uuid.UUID) (assessment.Detail, error) {
	return f.detail, f.getErr
}
func (f *fakeAssessment) ListForLesson(context.Context, uuid.UUID) ([]assessment.LessonAssessmentItem, error) {
	return f.rows, f.listErr
}
func (f *fakeAssessment) CreateExpertRevision(_ context.Context, _ uuid.UUID, _ uuid.UUID, in assessment.RevisionInput, _ string) (assessment.Assessment, error) {
	f.received = in
	return f.created, f.createErr
}

func assessmentMux(s *fakeAssessment, t fakeTraining, role auth.Role) http.Handler {
	mux := httpapi.NewMux()
	NewHandlers(s, t, fakeAuth{principal: auth.Principal{UserID: uuid.New(), Role: role}}, true).Register(mux)
	return httpapi.WithRequestID(mux)
}
func assessmentRequest(method, path, body string, authenticated bool) *http.Request {
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if authenticated {
		r.AddCookie(&http.Cookie{Name: authhttp.CookieName, Value: "token"})
	}
	return r
}
func requireCode(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status=%d want=%d body=%s", w.Code, status, w.Body.String())
	}
	if code == "" {
		return
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"code":"`+code+`"`)) {
		t.Fatalf("body=%s does not contain code=%q", w.Body.String(), code)
	}
}

func TestAssessmentRoutesRequireInstructor(t *testing.T) {
	id := uuid.New().String()
	routes := []struct{ method, path string }{{"GET", "/api/v1/lessons/" + id + "/assessments"}, {"GET", "/api/v1/items/" + id + "/assessment"}, {"POST", "/api/v1/items/" + id + "/assessment/revisions"}}
	for _, route := range routes {
		w := httptest.NewRecorder()
		assessmentMux(&fakeAssessment{}, fakeTraining{}, auth.RoleInstructor).ServeHTTP(w, assessmentRequest(route.method, route.path, "{}", false))
		requireCode(t, w, 401, "unauthorized")
		w = httptest.NewRecorder()
		assessmentMux(&fakeAssessment{}, fakeTraining{}, auth.RoleTrainee).ServeHTTP(w, assessmentRequest(route.method, route.path, "{}", true))
		requireCode(t, w, 403, "forbidden")
	}
}

func TestAssessmentOwnershipAndClosedStateAreNotFound(t *testing.T) {
	id := uuid.New().String()
	for _, tc := range []struct {
		name, path                 string
		trainingErr, assessmentErr error
	}{
		{"foreign instructor", "/api/v1/items/" + id + "/assessment", training.ErrNotFound, nil},
		{"unclosed item", "/api/v1/items/" + id + "/assessment", nil, assessment.ErrNotClosed},
		{"foreign lesson", "/api/v1/lessons/" + id + "/assessments", training.ErrNotFound, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			assessmentMux(&fakeAssessment{getErr: tc.assessmentErr}, fakeTraining{itemErr: tc.trainingErr, lessonErr: tc.trainingErr}, auth.RoleInstructor).ServeHTTP(w, assessmentRequest("GET", tc.path, "", true))
			requireCode(t, w, 404, "not_found")
		})
	}
}

func TestCreateRevisionValidationAndStale(t *testing.T) {
	id := uuid.New().String()
	path := "/api/v1/items/" + id + "/assessment/revisions"
	for _, tc := range []struct {
		name, body string
		err        error
		status     int
		code       string
	}{
		{"malformed", "{", nil, 400, "invalid_request"},
		{"validation", `{"reason":"x","criteria":[],"base_revision":0}`, &assessment.ValidationError{Field: "reason", Reason: "length"}, 422, "validation_failed"},
		{"stale", `{"reason":"достаточная причина","criteria":[],"base_revision":1}`, assessment.ErrStaleRevision, 409, "stale_revision"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			assessmentMux(&fakeAssessment{createErr: tc.err}, fakeTraining{}, auth.RoleInstructor).ServeHTTP(w, assessmentRequest("POST", path, tc.body, true))
			requireCode(t, w, tc.status, tc.code)
		})
	}
}

func TestAssessmentGetAndListShape(t *testing.T) {
	id := uuid.New()
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	state := "done"
	score := 75.0
	passed := true
	a := assessment.Assessment{ID: uuid.New(), ItemID: id, Revision: 1, Kind: assessment.KindAuto, Status: assessment.StatusReady, RubricVersion: "dds/rubric-v1", Score: &score, Passed: &passed, CreatedAt: now, Criteria: []assessment.CriterionResult{}}
	service := &fakeAssessment{detail: assessment.Detail{AutomaticState: &state, Final: &a, Revisions: []assessment.Assessment{a}, RubricEffective: assessment.Rubric{Version: "dds/rubric-v1"}, Evidence: map[string]any{"schema": "emsim/evidence/v1"}}, rows: []assessment.LessonAssessmentItem{{ItemID: id, UserID: uuid.New(), Login: "trainee", FullName: "Иванов", Level: "easy", Active: true, WorkstationNo: 4, Ordinal: 2, CardNumber: "42", ItemState: "closed", ClosedAt: now, AutomaticState: &state, Final: &a}}}
	w := httptest.NewRecorder()
	assessmentMux(service, fakeTraining{}, auth.RoleInstructor).ServeHTTP(w, assessmentRequest("GET", "/api/v1/items/"+id.String()+"/assessment", "", true))
	requireCode(t, w, 200, "")
	if !bytes.Contains(w.Body.Bytes(), []byte(`"automatic_state":"done"`)) || !bytes.Contains(w.Body.Bytes(), []byte(`"evidence"`)) {
		t.Fatalf("unexpected detail: %s", w.Body.String())
	}
	w = httptest.NewRecorder()
	assessmentMux(service, fakeTraining{}, auth.RoleInstructor).ServeHTTP(w, assessmentRequest("GET", "/api/v1/lessons/"+uuid.New().String()+"/assessments", "", true))
	requireCode(t, w, 200, "")
	if !bytes.Contains(w.Body.Bytes(), []byte(`"card_number":"42"`)) {
		t.Fatalf("unexpected list: %s", w.Body.String())
	}
}
