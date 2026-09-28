package http

import (
	"bytes"
	"context"
	"encoding/json"
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
	lesson    training.Lesson
}

func (f fakeTraining) ItemForInstructor(context.Context, auth.Principal, uuid.UUID) (training.Item, []training.Action, []training.DeliveredEvent, content.Body, error) {
	return training.Item{}, nil, nil, content.Body{}, f.itemErr
}
func (f fakeTraining) Lesson(context.Context, auth.Principal, uuid.UUID) (training.Lesson, []training.Assignment, error) {
	return f.lesson, nil, f.lessonErr
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
	routes := []struct{ method, path string }{{"GET", "/api/v1/lessons/" + id + "/assessments"}, {"GET", "/api/v1/lessons/" + id + "/rubric"}, {"GET", "/api/v1/items/" + id + "/assessment"}, {"POST", "/api/v1/items/" + id + "/assessment/revisions"}}
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

func TestLessonRubricAppliesLessonScoring(t *testing.T) {
	base, err := assessment.LoadRubric(content.ExerciseTypeDDSProcessing, "dds/rubric-v2")
	if err != nil {
		t.Fatal(err)
	}
	weights := map[string]float64{}
	for _, c := range base.Criteria {
		weights[c.ID] = 0
	}
	weights["D_PRIMARY"] = 100
	lesson := training.Lesson{ID: uuid.New(), ExerciseType: content.ExerciseTypeDDSProcessing, RubricVersion: "dds/rubric-v2",
		Scoring: &training.LessonScoring{Weights: weights, PassThreshold: 85}}
	w := httptest.NewRecorder()
	assessmentMux(&fakeAssessment{}, fakeTraining{lesson: lesson}, auth.RoleInstructor).ServeHTTP(w, assessmentRequest("GET", "/api/v1/lessons/"+lesson.ID.String()+"/rubric", "", true))
	requireCode(t, w, 200, "")
	var got lessonRubricJSON
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.RubricVersion != "dds/rubric-v2" || got.PassThreshold != 85 || got.DefaultPassThreshold != base.PassThreshold || len(got.Criteria) != len(base.Criteria) {
		t.Fatalf("unexpected rubric view: %+v", got)
	}
	for i, c := range got.Criteria {
		wantWeight := 0.0
		if c.ID == "D_PRIMARY" {
			wantWeight = 100
		}
		if c.Weight != wantWeight || c.DefaultWeight != base.Criteria[i].Weight || c.Title == "" {
			t.Fatalf("criterion %s: %+v (default weight should be %v)", c.ID, c, base.Criteria[i].Weight)
		}
	}

	// A lesson without its own scoring shows the rubric's own values.
	lesson.Scoring = nil
	w = httptest.NewRecorder()
	assessmentMux(&fakeAssessment{}, fakeTraining{lesson: lesson}, auth.RoleInstructor).ServeHTTP(w, assessmentRequest("GET", "/api/v1/lessons/"+lesson.ID.String()+"/rubric", "", true))
	got = lessonRubricJSON{}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.PassThreshold != base.PassThreshold {
		t.Fatalf("threshold=%v want rubric default %v", got.PassThreshold, base.PassThreshold)
	}
	for i, c := range got.Criteria {
		if c.Weight != c.DefaultWeight || c.Weight != base.Criteria[i].Weight {
			t.Fatalf("criterion %s not at its default: %+v", c.ID, c)
		}
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

// TestCriterionJSONRoundTripsPenaltyPointsAndDetails is 112-6 LLM-stage
// c2's own regression test: before this fix, criterionJSON had neither
// field, so GET .../assessment never surfaced a block's Details (the
// instructor review's "Подробности" panel had nothing to expand) and,
// more seriously, POST .../revisions silently dropped an expert's own
// penalty_points on decode — ValidateRevision/Score.Compute then saw nil
// regardless of what the instructor typed.
func TestCriterionJSONRoundTripsPenaltyPointsAndDetails(t *testing.T) {
	id := uuid.New()
	points, maxPoints := 5.0, 10.0
	expected := "Тверская"
	a := assessment.Assessment{
		ID: uuid.New(), ItemID: id, Revision: 1, Kind: assessment.KindAuto, Status: assessment.StatusReady,
		RubricVersion: "operator112/rubric-v2",
		Criteria: []assessment.CriterionResult{{
			ID: "P_SERVICES", Status: assessment.CriterionNotMet, Weight: 0, PenaltyPoints: &points,
			Details: []assessment.CriterionDetail{{Key: "pilot_fire_101", Label: "101", Points: points, MaxPoints: maxPoints, Status: assessment.CriterionNotMet, Expected: &expected}},
		}},
	}
	service := &fakeAssessment{detail: assessment.Detail{Final: &a, Revisions: []assessment.Assessment{a}}}
	w := httptest.NewRecorder()
	assessmentMux(service, fakeTraining{}, auth.RoleInstructor).ServeHTTP(w, assessmentRequest("GET", "/api/v1/items/"+id.String()+"/assessment", "", true))
	requireCode(t, w, 200, "")
	for _, want := range []string{`"penalty_points":5`, `"details":[{`, `"key":"pilot_fire_101"`, `"max_points":10`, `"expected":"Тверская"`} {
		if !bytes.Contains(w.Body.Bytes(), []byte(want)) {
			t.Fatalf("response missing %s: %s", want, w.Body.String())
		}
	}

	w = httptest.NewRecorder()
	body := `{"reason":"проверка штрафа","base_revision":1,"criteria":[{"id":"P_SERVICES","status":"not_met","weight":0,"critical":false,"penalty_points":7}]}`
	assessmentMux(service, fakeTraining{}, auth.RoleInstructor).ServeHTTP(w, assessmentRequest("POST", "/api/v1/items/"+id.String()+"/assessment/revisions", body, true))
	requireCode(t, w, 201, "")
	if len(service.received.Criteria) != 1 || service.received.Criteria[0].PenaltyPoints == nil || *service.received.Criteria[0].PenaltyPoints != 7 {
		t.Fatalf("decoded revision lost penalty_points: %+v", service.received.Criteria)
	}
}
