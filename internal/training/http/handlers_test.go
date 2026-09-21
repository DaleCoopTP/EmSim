package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"emsim/internal/auth"
	authhttp "emsim/internal/auth/http"
	"emsim/internal/content"
	"emsim/internal/platform/httpapi"
	"emsim/internal/platform/realtime"
	"emsim/internal/training"

	"github.com/google/uuid"
)

type fakeAuth struct {
	principal auth.Principal
}

func (f *fakeAuth) Authenticate(_ context.Context, token string) (auth.Principal, error) {
	if token != "token" {
		return auth.Principal{}, auth.ErrSessionInvalid
	}
	return f.principal, nil
}

type fakeTraining struct {
	lessons         []training.Lesson
	lesson          training.Lesson
	assignments     []training.Assignment
	options         training.LessonOptionsResult
	run             training.Run
	item            training.Item
	actions         []training.Action
	events          []training.DeliveredEvent
	monitor         training.MonitorResult
	monitorErr      error
	queueTotal      int
	reference       content.Body
	now             time.Time
	readErr         error
	itemErr         error
	nowErr          error
	executeReceipt  training.Receipt
	executeErr      error
	lastCreate      training.LessonCreate
	lastAssignments []training.AssignmentInput
}

func (f *fakeTraining) CreateLesson(_ context.Context, _ auth.Principal, in training.LessonCreate, _ string) (training.Lesson, error) {
	f.lastCreate = in
	return f.lesson, f.readErr
}
func (f *fakeTraining) ReplaceAssignments(_ context.Context, _ auth.Principal, _ uuid.UUID, in []training.AssignmentInput, _ string) (training.Lesson, error) {
	f.lastAssignments = in
	return f.lesson, f.readErr
}
func (f *fakeTraining) Start(context.Context, auth.Principal, uuid.UUID, string) (training.Lesson, error) {
	return f.lesson, f.readErr
}
func (f *fakeTraining) Stop(context.Context, auth.Principal, uuid.UUID, *string, string) (training.Lesson, error) {
	return f.lesson, f.readErr
}
func (f *fakeTraining) Monitor(context.Context, auth.Principal, uuid.UUID) (training.MonitorResult, error) {
	return f.monitor, f.monitorErr
}
func (f *fakeTraining) Execute(context.Context, auth.Principal, uuid.UUID, training.Command, string) (training.Receipt, error) {
	return f.executeReceipt, f.executeErr
}
func (f *fakeTraining) MyRun(context.Context, auth.Principal) (training.Run, training.Lesson, int, error) {
	return f.run, f.lesson, f.queueTotal, f.readErr
}
func (f *fakeTraining) MyItems(context.Context, auth.Principal) ([]training.Item, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	return []training.Item{f.item}, nil
}
func (f *fakeTraining) ItemForTrainee(context.Context, auth.Principal, uuid.UUID) (training.Item, []training.Action, []training.DeliveredEvent, error) {
	return f.item, f.actions, f.events, f.itemErr
}
func (f *fakeTraining) ItemForInstructor(context.Context, auth.Principal, uuid.UUID) (training.Item, []training.Action, []training.DeliveredEvent, content.Body, error) {
	return f.item, f.actions, f.events, f.reference, f.itemErr
}
func (f *fakeTraining) RunActions(context.Context, auth.Principal, uuid.UUID, uuid.UUID) ([]training.Action, error) {
	return f.actions, f.readErr
}
func (f *fakeTraining) ListLessons(context.Context, auth.Principal, *training.LessonState) ([]training.Lesson, error) {
	return f.lessons, f.readErr
}
func (f *fakeTraining) Lesson(context.Context, auth.Principal, uuid.UUID) (training.Lesson, []training.Assignment, error) {
	return f.lesson, f.assignments, f.readErr
}
func (f *fakeTraining) LessonOptions(context.Context) (training.LessonOptionsResult, error) {
	return f.options, f.readErr
}
func (f *fakeTraining) Now(context.Context) (time.Time, error) { return f.now, f.nowErr }
func (f *fakeTraining) UploadRecording(context.Context, auth.Principal, uuid.UUID, uuid.UUID, training.Blob) error {
	return f.readErr
}
func (f *fakeTraining) RecordingForInstructor(context.Context, auth.Principal, uuid.UUID, uuid.UUID) (training.Blob, error) {
	return training.Blob{}, f.readErr
}

func trainingPrincipal(role auth.Role) auth.Principal {
	ws := uuid.New()
	return auth.Principal{UserID: uuid.New(), Role: role, WorkstationID: &ws}
}

func trainingFixture() *fakeTraining {
	now := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	lessonID, runID, itemID := uuid.New(), uuid.New(), uuid.New()
	return &fakeTraining{
		now:    now,
		lesson: training.Lesson{ID: lessonID, ExerciseType: content.ExerciseTypeDDSProcessing, Title: "Пилот", Mode: training.ModeTraining, Level: auth.LevelEasy, State: training.LessonDraft, Timing: training.Timing{OpenS: 30, PrimaryS: 30, CompleteS: 180}},
		run:    training.Run{ID: runID, LessonID: lessonID, ExerciseType: content.ExerciseTypeDDSProcessing, Mode: training.ModeTraining, WorkstationNo: 7},
		item: training.Item{
			ID: itemID, RunID: runID, LessonID: lessonID, State: training.ItemOpened, Reaction: content.ReactionReceived,
			Mode: training.ModeTraining, OfferedAt: now, OpenedAt: &now, Deadlines: training.Deadlines{OpenAt: now.Add(30 * time.Second), PrimaryAt: now.Add(60 * time.Second)},
			Card:     content.CardPreview{Number: "42", Applicant: content.ApplicantPreview{Name: "Иван", Phone: "+7000", Status: "witness"}, Address: content.Address{Text: "Москва", Okrug: "ЮАР"}, Incident: content.IncidentPreview{TypeCode: "fire", TypeName: "Пожар", Features: map[string]any{}}, NotificationList: []content.NotificationPreview{}, Phones: content.Phones{}},
			Workflow: content.Workflow{Transitions: map[content.Reaction][]content.Reaction{content.ReactionReceived: {content.ReactionAccepted}}},
		},
		reference: content.Body{Reference: content.Reference{PilotGoal: "accept_card", Notes: "СЕКРЕТНЫЙ ЭТАЛОН"}},
	}
}

func trainingMux(svc *fakeTraining, principal auth.Principal) http.Handler {
	mux := httpapi.NewMux()
	NewHandlers(svc, &fakeAuth{principal: principal}, true, realtime.NewHub()).Register(mux)
	return httpapi.WithRequestID(mux)
}

func trainingRequest(method, path string, body []byte, authenticated bool) *http.Request {
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	if authenticated {
		r.AddCookie(&http.Cookie{Name: authhttp.CookieName, Value: "token"})
	}
	return r
}

func requireError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status=%d want=%d body=%s", response.Code, status, response.Body.String())
	}
	var got struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Error.Code != code {
		t.Fatalf("error.code=%q want=%q", got.Error.Code, code)
	}
}

func TestRoutesRequireAuthenticationAndCorrectRoles(t *testing.T) {
	id := uuid.New().String()
	lessonRoutes := []struct{ method, path string }{{"GET", "/api/v1/lessons"}, {"POST", "/api/v1/lessons"}, {"GET", "/api/v1/lessons/options"}, {"GET", "/api/v1/lessons/" + id}, {"PUT", "/api/v1/lessons/" + id + "/assignments"}, {"POST", "/api/v1/lessons/" + id + "/start"}, {"GET", "/api/v1/lessons/" + id + "/runs/" + id + "/actions"}}
	traineeRoutes := []struct{ method, path string }{{"GET", "/api/v1/my/run"}, {"GET", "/api/v1/my/items"}, {"POST", "/api/v1/items/" + id + "/actions"}}
	all := append(append([]struct{ method, path string }{}, lessonRoutes...), traineeRoutes...)
	all = append(all, struct{ method, path string }{"GET", "/api/v1/items/" + id})
	for _, route := range all {
		response := httptest.NewRecorder()
		trainingMux(trainingFixture(), trainingPrincipal(auth.RoleInstructor)).ServeHTTP(response, trainingRequest(route.method, route.path, nil, false))
		requireError(t, response, 401, "unauthorized")
	}
	for _, role := range []auth.Role{auth.RoleAdmin, auth.RoleTrainee} {
		for _, route := range lessonRoutes {
			response := httptest.NewRecorder()
			trainingMux(trainingFixture(), trainingPrincipal(role)).ServeHTTP(response, trainingRequest(route.method, route.path, []byte(`{}`), true))
			requireError(t, response, 403, "forbidden")
		}
	}
	for _, role := range []auth.Role{auth.RoleAdmin, auth.RoleInstructor} {
		for _, route := range traineeRoutes {
			response := httptest.NewRecorder()
			trainingMux(trainingFixture(), trainingPrincipal(role)).ServeHTTP(response, trainingRequest(route.method, route.path, []byte(`{}`), true))
			requireError(t, response, 403, "forbidden")
		}
	}
	response := httptest.NewRecorder()
	trainingMux(trainingFixture(), trainingPrincipal(auth.RoleAdmin)).ServeHTTP(response, trainingRequest("GET", "/api/v1/items/"+id, nil, true))
	requireError(t, response, 403, "forbidden")
}

func TestOwnershipAndWorkstationErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		role   auth.Role
		path   string
		err    error
		status int
		code   string
	}{
		{"foreign instructor lesson", auth.RoleInstructor, "/api/v1/lessons/" + uuid.New().String(), training.ErrNotFound, 404, "not_found"},
		{"foreign trainee item", auth.RoleTrainee, "/api/v1/items/" + uuid.New().String(), training.ErrNotFound, 404, "not_found"},
		{"wrong workstation read", auth.RoleTrainee, "/api/v1/items/" + uuid.New().String(), training.ErrWorkstationMismatch, 403, "forbidden"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := trainingFixture()
			if strings.Contains(tc.path, "/items/") {
				svc.itemErr = tc.err
			} else {
				svc.readErr = tc.err
			}
			response := httptest.NewRecorder()
			trainingMux(svc, trainingPrincipal(tc.role)).ServeHTTP(response, trainingRequest("GET", tc.path, nil, true))
			body := response.Body.String()
			requireError(t, response, tc.status, tc.code)
			if tc.err == training.ErrWorkstationMismatch && !strings.Contains(body, "workstation_mismatch") {
				t.Fatalf("missing mismatch detail: %s", body)
			}
		})
	}
	svc := trainingFixture()
	svc.executeErr = training.ErrWorkstationMismatch
	response := httptest.NewRecorder()
	trainingMux(svc, trainingPrincipal(auth.RoleTrainee)).ServeHTTP(response, trainingRequest("POST", "/api/v1/items/"+svc.item.ID.String()+"/actions", []byte(`{"command_id":"`+uuid.New().String()+`","expected_seq":0,"type":"open","payload":{}}`), true))
	requireError(t, response, 403, "forbidden")

	svc = trainingFixture()
	svc.readErr = training.ErrWorkstationMismatch
	response = httptest.NewRecorder()
	trainingMux(svc, trainingPrincipal(auth.RoleTrainee)).ServeHTTP(response, trainingRequest("GET", "/api/v1/my/items", nil, true))
	body := response.Body.String()
	requireError(t, response, 403, "forbidden")
	if !strings.Contains(body, "workstation_mismatch") {
		t.Fatalf("missing mismatch detail: %s", body)
	}
}

func TestTraineeItemHasExactPublicShapeAndNoReferenceLeak(t *testing.T) {
	svc := trainingFixture()
	response := httptest.NewRecorder()
	trainingMux(svc, trainingPrincipal(auth.RoleTrainee)).ServeHTTP(response, trainingRequest("GET", "/api/v1/items/"+svc.item.ID.String(), nil, true))
	if response.Code != 200 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var got map[string]any
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	wantKeys := []string{"actions", "address_short", "allowed_transitions", "calls", "card", "card_number", "close_reason", "closed_at", "deadlines", "events", "id", "incident_type", "interruptions", "mode", "offered_at", "opened_at", "primary_at", "reaction", "seq", "server_time", "state"}
	gotKeys := make([]string, 0, len(got))
	for k := range got {
		gotKeys = append(gotKeys, k)
	}
	sort.Strings(gotKeys)
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Fatalf("keys=%v want=%v", gotKeys, wantKeys)
	}
	encoded := response.Body.String()
	for _, forbidden := range []string{"reference", "field_corrections", "pilot_goal", "hints", "СЕКРЕТНЫЙ ЭТАЛОН"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("trainee response leaked %q: %s", forbidden, encoded)
		}
	}

	response = httptest.NewRecorder()
	trainingMux(svc, trainingPrincipal(auth.RoleInstructor)).ServeHTTP(response, trainingRequest("GET", "/api/v1/items/"+svc.item.ID.String(), nil, true))
	if response.Code != 200 || !strings.Contains(response.Body.String(), "СЕКРЕТНЫЙ ЭТАЛОН") {
		t.Fatalf("instructor item missing reference: %d %s", response.Code, response.Body.String())
	}
}

func TestCommandResponseStatusesAndErrors(t *testing.T) {
	itemID := uuid.New()
	base := training.Receipt{CommandID: uuid.New(), Seq: 1, Reaction: content.ReactionReceived, ItemState: training.ItemOpened, ActionID: uuid.New(), LogSeq: 1}
	for _, tc := range []struct {
		name      string
		rejection *training.Rejection
		err       error
		status    int
		code      string
	}{
		{"applied", nil, nil, 200, ""},
		{"stale", rejectionPtr(training.RejectStaleSeq), nil, 409, ""},
		{"domain invalid", rejectionPtr(training.RejectTransitionNotAllowed), nil, 422, ""},
		{"command conflict", nil, training.ErrCommandIDConflict, 409, "command_id_conflict"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := trainingFixture()
			svc.item.ID = itemID
			svc.executeErr = tc.err
			svc.executeReceipt = base
			if tc.rejection != nil {
				svc.executeReceipt.Outcome = training.OutcomeRejected
				svc.executeReceipt.ErrorCode = tc.rejection
			} else {
				svc.executeReceipt.Outcome = training.OutcomeApplied
			}
			response := httptest.NewRecorder()
			trainingMux(svc, trainingPrincipal(auth.RoleTrainee)).ServeHTTP(response, trainingRequest("POST", "/api/v1/items/"+itemID.String()+"/actions", []byte(`{"command_id":"`+base.CommandID.String()+`","expected_seq":0,"type":"open","payload":{}}`), true))
			if tc.code != "" {
				requireError(t, response, tc.status, tc.code)
			} else if response.Code != tc.status {
				t.Fatalf("status=%d want=%d body=%s", response.Code, tc.status, response.Body.String())
			}
		})
	}
}

func rejectionPtr(value training.Rejection) *training.Rejection { return &value }

func TestLessonCreateAssignmentValidationAndNoActiveRun(t *testing.T) {
	svc := trainingFixture()
	svc.readErr = training.ErrNotFound
	response := httptest.NewRecorder()
	trainingMux(svc, trainingPrincipal(auth.RoleTrainee)).ServeHTTP(response, trainingRequest("GET", "/api/v1/my/run", nil, true))
	if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
		t.Fatalf("no run response=%d %q", response.Code, response.Body.String())
	}

	svc = trainingFixture()
	response = httptest.NewRecorder()
	trainingMux(svc, trainingPrincipal(auth.RoleInstructor)).ServeHTTP(response, trainingRequest("POST", "/api/v1/lessons", []byte(`{"exercise_type":"dds_processing","title":"Урок","mode":"training","level":"easy"}`), true))
	if response.Code != 201 || svc.lastCreate.Title != "Урок" {
		t.Fatalf("create=%d input=%+v body=%s", response.Code, svc.lastCreate, response.Body.String())
	}

	response = httptest.NewRecorder()
	trainingMux(svc, trainingPrincipal(auth.RoleInstructor)).ServeHTTP(response, trainingRequest("PUT", "/api/v1/lessons/"+svc.lesson.ID.String()+"/assignments", []byte(`[{"workstation_no":7,"user_id":"bad","scenario_version_ids":[]}]`), true))
	requireError(t, response, 422, "validation_failed")
}

func TestRequestBodiesAreLimitedTo64KiB(t *testing.T) {
	svc := trainingFixture()
	response := httptest.NewRecorder()
	oversized := []byte(`{"title":"` + strings.Repeat("x", httpapi.MaxJSONBodyBytes) + `"}`)
	trainingMux(svc, trainingPrincipal(auth.RoleInstructor)).ServeHTTP(response, trainingRequest("POST", "/api/v1/lessons", oversized, true))
	requireError(t, response, 413, "payload_too_large")
}

var _ trainingService = (*fakeTraining)(nil)
var _ authenticator = (*fakeAuth)(nil)
