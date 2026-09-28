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
	lastPatch       training.LessonSettingsPatch
	lastDraw        training.DrawInput
	drawn           []training.Assignment
}

func (f *fakeTraining) CreateLesson(_ context.Context, _ auth.Principal, in training.LessonCreate, _ string) (training.Lesson, error) {
	f.lastCreate = in
	return f.lesson, f.readErr
}
func (f *fakeTraining) UpdateLessonSettings(_ context.Context, _ auth.Principal, _ uuid.UUID, patch training.LessonSettingsPatch, _ string) (training.Lesson, error) {
	f.lastPatch = patch
	return f.lesson, f.readErr
}
func (f *fakeTraining) DrawAssignments(_ context.Context, _ auth.Principal, _ uuid.UUID, in training.DrawInput) ([]training.Assignment, error) {
	f.lastDraw = in
	return f.drawn, f.readErr
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
func (f *fakeTraining) VoicePhraseForTrainee(context.Context, auth.Principal, uuid.UUID, string, string) (training.Blob, error) {
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
	lessonRoutes := []struct{ method, path string }{{"GET", "/api/v1/lessons"}, {"POST", "/api/v1/lessons"}, {"GET", "/api/v1/lessons/options"}, {"GET", "/api/v1/lessons/" + id}, {"PATCH", "/api/v1/lessons/" + id}, {"PUT", "/api/v1/lessons/" + id + "/assignments"}, {"POST", "/api/v1/lessons/" + id + "/assignments/draw"}, {"POST", "/api/v1/lessons/" + id + "/start"}, {"GET", "/api/v1/lessons/" + id + "/runs/" + id + "/actions"}}
	// traineeRoutes are trainee-only (unlike POST .../actions below,
	// 112-7/ADR-027 gives no instructor exception here).
	traineeRoutes := []struct{ method, path string }{{"GET", "/api/v1/my/run"}, {"GET", "/api/v1/my/items"}}
	itemActionsRoute := struct{ method, path string }{"POST", "/api/v1/items/" + id + "/actions"}
	all := append(append([]struct{ method, path string }{}, lessonRoutes...), traineeRoutes...)
	all = append(all, itemActionsRoute, struct{ method, path string }{"GET", "/api/v1/items/" + id})
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
	trainingMux(trainingFixture(), trainingPrincipal(auth.RoleAdmin)).ServeHTTP(response, trainingRequest(itemActionsRoute.method, itemActionsRoute.path, []byte(`{}`), true))
	requireError(t, response, 403, "forbidden")

	response = httptest.NewRecorder()
	trainingMux(trainingFixture(), trainingPrincipal(auth.RoleAdmin)).ServeHTTP(response, trainingRequest("GET", "/api/v1/items/"+id, nil, true))
	requireError(t, response, 403, "forbidden")
}

// TestPreviewInstructorReachesItemActions is 112-7/ADR-027's own route-
// level check: unlike every other trainee-only route, POST
// /items/{id}/actions must also admit an instructor (GroupItemActions),
// since a preview run's sole participant is its author. This only checks
// the route no longer 403s the request before it reaches the service —
// training.Service.Execute (internal/training/service_test.go) is what
// actually enforces run.UserID == actor.UserID and the workstationMatches
// preview exception.
func TestPreviewInstructorReachesItemActions(t *testing.T) {
	svc := trainingFixture()
	svc.executeReceipt = training.Receipt{Outcome: training.OutcomeApplied}
	response := httptest.NewRecorder()
	body := `{"command_id":"` + uuid.New().String() + `","expected_seq":0,"type":"open","payload":{}}`
	trainingMux(svc, trainingPrincipal(auth.RoleInstructor)).ServeHTTP(response, trainingRequest("POST", "/api/v1/items/"+svc.item.ID.String()+"/actions", []byte(body), true))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestUpdateLessonSettingsPassesTimingAndMapsErrors(t *testing.T) {
	svc := trainingFixture()
	path := "/api/v1/lessons/" + svc.lesson.ID.String()
	body := `{"timing":{"open_s":20,"primary_s":45,"complete_s":240}}`
	response := httptest.NewRecorder()
	trainingMux(svc, trainingPrincipal(auth.RoleInstructor)).ServeHTTP(response, trainingRequest("PATCH", path, []byte(body), true))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if got := svc.lastPatch.Timing; got == nil || got.OpenS != 20 || got.PrimaryS != 45 || got.CompleteS != 240 {
		t.Fatalf("timing not passed through: %+v", got)
	}
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{{training.ErrConflict, 409, "conflict"}, {training.ErrNotFound, 404, "not_found"}} {
		svc.readErr = tc.err
		response = httptest.NewRecorder()
		trainingMux(svc, trainingPrincipal(auth.RoleInstructor)).ServeHTTP(response, trainingRequest("PATCH", path, []byte(body), true))
		requireError(t, response, tc.status, tc.code)
	}
}

func TestUpdateLessonSettingsScoringAbsentNullAndSet(t *testing.T) {
	path := func(svc *fakeTraining) string { return "/api/v1/lessons/" + svc.lesson.ID.String() }
	patch := func(body string) *fakeTraining {
		svc := trainingFixture()
		response := httptest.NewRecorder()
		trainingMux(svc, trainingPrincipal(auth.RoleInstructor)).ServeHTTP(response, trainingRequest("PATCH", path(svc), []byte(body), true))
		if response.Code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s", body, response.Code, response.Body.String())
		}
		return svc
	}
	if svc := patch(`{"timing":{"open_s":30,"primary_s":30,"complete_s":180}}`); svc.lastPatch.ScoringSet {
		t.Fatal("absent scoring must leave the setting alone")
	}
	if svc := patch(`{"scoring":null}`); !svc.lastPatch.ScoringSet || svc.lastPatch.Scoring != nil {
		t.Fatalf("null scoring must reset: %+v", svc.lastPatch)
	}
	svc := patch(`{"scoring":{"weights":{"T_OPEN":100},"pass_threshold":80}}`)
	if got := svc.lastPatch.Scoring; !svc.lastPatch.ScoringSet || got == nil || got.PassThreshold != 80 || got.Weights["T_OPEN"] != 100 {
		t.Fatalf("scoring not passed through: %+v", svc.lastPatch)
	}
	response := httptest.NewRecorder()
	bad := trainingFixture()
	trainingMux(bad, trainingPrincipal(auth.RoleInstructor)).ServeHTTP(response, trainingRequest("PATCH", path(bad), []byte(`{"scoring":{"pass_threshold":80}}`), true))
	requireError(t, response, 400, "invalid_request")
}

func TestDrawAssignmentsPassesRowsAndMapsNotEnoughScenarios(t *testing.T) {
	svc := trainingFixture()
	userID := uuid.New()
	version := uuid.New()
	svc.drawn = []training.Assignment{{WorkstationNo: 3, UserID: userID, ScenarioVersionIDs: []uuid.UUID{version}}}
	path := "/api/v1/lessons/" + svc.lesson.ID.String() + "/assignments/draw"
	body := `{"categories":["14","22"],"count":1,"rows":[{"workstation_no":3,"user_id":"` + userID.String() + `"}]}`
	response := httptest.NewRecorder()
	trainingMux(svc, trainingPrincipal(auth.RoleInstructor)).ServeHTTP(response, trainingRequest("POST", path, []byte(body), true))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if got := svc.lastDraw; got.Count != 1 || len(got.Categories) != 2 || len(got.Rows) != 1 || got.Rows[0].WorkstationNo != 3 || got.Rows[0].UserID != userID {
		t.Fatalf("draw input not passed through: %+v", got)
	}
	if !strings.Contains(response.Body.String(), version.String()) {
		t.Fatalf("drawn queue missing from response: %s", response.Body.String())
	}

	svc.readErr = &training.NotEnoughScenariosError{WorkstationNo: 3, Available: 1, Requested: 4}
	response = httptest.NewRecorder()
	trainingMux(svc, trainingPrincipal(auth.RoleInstructor)).ServeHTTP(response, trainingRequest("POST", path, []byte(body), true))
	raw := response.Body.String() // requireError consumes the recorder's body
	requireError(t, response, 422, "not_enough_scenarios")
	if !strings.Contains(raw, `"available":1`) {
		t.Fatalf("details.available missing: %s", raw)
	}

	response = httptest.NewRecorder()
	trainingMux(svc, trainingPrincipal(auth.RoleInstructor)).ServeHTTP(response, trainingRequest("POST", path, []byte(`{"categories":["14"],"count":1,"rows":[{"workstation_no":3,"user_id":"nope"}]}`), true))
	requireError(t, response, 422, "validation_failed")
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
	wantKeys := []string{"actions", "address_short", "allowed_transitions", "calls", "card", "card_number", "card_status", "close_reason", "closed_at", "deadlines", "events", "id", "incident_type", "incoming_call", "interruptions", "mode", "offered_at", "opened_at", "primary_at", "reaction", "seq", "server_time", "state", "terminal_statuses"}
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

func TestDDSItemExposesTerminalStatusesAndCardStatus(t *testing.T) {
	svc := trainingFixture()
	svc.item.Workflow = content.Workflow{
		Transitions: map[content.Reaction][]content.Reaction{content.ReactionReceived: {content.ReactionAccepted, content.ReactionNotAccepted}},
		Terminal:    []content.Reaction{content.ReactionCompleted, content.ReactionRefused},
	}
	response := httptest.NewRecorder()
	trainingMux(svc, trainingPrincipal(auth.RoleTrainee)).ServeHTTP(response, trainingRequest("GET", "/api/v1/items/"+svc.item.ID.String(), nil, true))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		TerminalStatuses []string `json:"terminal_statuses"`
		CardStatus       string   `json:"card_status"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(body.TerminalStatuses, []string{"completed", "refused"}) {
		t.Fatalf("terminal_statuses=%v, want [completed refused]", body.TerminalStatuses)
	}
	if body.CardStatus == "" {
		t.Fatal("card_status missing on a DDS item")
	}
}

func TestTraineeItemUsesScenarioContactsForPhonePanel(t *testing.T) {
	svc := trainingFixture()
	svc.item.Contacts = []content.Contact{{Key: "crew_leader", Label: "Руководитель бригады", Number: "4152", Voice: "crew_leader_recorded"}}
	response := httptest.NewRecorder()
	trainingMux(svc, trainingPrincipal(auth.RoleTrainee)).ServeHTTP(response, trainingRequest("GET", "/api/v1/items/"+svc.item.ID.String(), nil, true))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Card struct {
			Contacts []content.ContactPreview `json:"contacts"`
		} `json:"card"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Card.Contacts) != 1 || body.Card.Contacts[0].Key != "crew_leader" || body.Card.Contacts[0].Number != "4152" {
		t.Fatalf("card contacts=%+v, want crew_leader/4152 from scenario", body.Card.Contacts)
	}
}

func TestOperator112ItemProjectsOnlyAvailableQuestions(t *testing.T) {
	svc := trainingFixture()
	card := training.UnansweredIntakeCard("112-1", "+79161313131", "02:03", "Europe/Moscow")
	svc.item.ExerciseType = content.ExerciseTypeOperator112Intake
	svc.item.IntakeCard = &card
	svc.item.IntakeState = &training.IntakeState{CallStatus: "connected", Transcript: []training.IntakeLine{}}
	svc.item.AvailableQuestions = []training.IntakeQuestionOption{{ID: "address", Text: "Где вы?", TopicID: "address", Asked: false}}
	svc.item.IntakeDialogue = &content.Intake112Dialogue{Questions: []content.Intake112Question{
		{ID: "address", Text: "Где вы?", TopicID: "address", Answer: content.Intake112Utterance{ID: "hidden_answer", Text: "СЕКРЕТНЫЙ АДРЕС"}},
		{ID: "locked", Text: "СЕКРЕТНЫЙ ВОПРОС", TopicID: "address", AvailableAfter: []string{"address"}},
	}}
	svc.reference.Intake112 = &content.Intake112{Dialogue: svc.item.IntakeDialogue}
	response := httptest.NewRecorder()
	trainingMux(svc, trainingPrincipal(auth.RoleTrainee)).ServeHTTP(response, trainingRequest("GET", "/api/v1/items/"+svc.item.ID.String(), nil, true))
	if response.Code != 200 || !strings.Contains(response.Body.String(), "Где вы?") {
		t.Fatalf("trainee question projection: %d %s", response.Code, response.Body.String())
	}
	for _, secret := range []string{"СЕКРЕТНЫЙ АДРЕС", "СЕКРЕТНЫЙ ВОПРОС", "intake_dialogue_reference", "hidden_answer"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatalf("trainee leaked %q: %s", secret, response.Body.String())
		}
	}
	response = httptest.NewRecorder()
	trainingMux(svc, trainingPrincipal(auth.RoleInstructor)).ServeHTTP(response, trainingRequest("GET", "/api/v1/items/"+svc.item.ID.String(), nil, true))
	if response.Code != 200 || !strings.Contains(response.Body.String(), "СЕКРЕТНЫЙ АДРЕС") {
		t.Fatalf("instructor missing dialogue reference: %d %s", response.Code, response.Body.String())
	}
}

// TestOperator112InstructorItemGetsAvailableServiceCodesToo is 112-7/
// ADR-027's own regression: web's Operator112ProfileCase (reused as-is
// for a preview run's instructor) needs available_service_codes to let
// the operator manually adjust the suggested set in its "add service"
// modal — this field must not stay trainee-only just because the
// instructor branch otherwise carries the full, untrimmed catalog.
func TestOperator112InstructorItemGetsAvailableServiceCodesToo(t *testing.T) {
	svc := trainingFixture()
	card := training.UnansweredIntakeCard("112-1", "+79161313131", "02:03", "Europe/Moscow")
	svc.item.ExerciseType = content.ExerciseTypeOperator112Intake
	svc.item.IntakeCard = &card
	svc.item.IntakeState = &training.IntakeState{Mode: "full_case", Catalog: &content.IntakeCatalog{
		Version: 1,
		ServiceRules: []content.IntakeServiceRule{
			{ID: "gas", ProfileID: "104", ServiceCode: "pilot_gas_104"},
			{ID: "fire", ProfileID: "101", ServiceCode: "pilot_fire_101"},
		},
	}}
	response := httptest.NewRecorder()
	trainingMux(svc, trainingPrincipal(auth.RoleInstructor)).ServeHTTP(response, trainingRequest("GET", "/api/v1/items/"+svc.item.ID.String(), nil, true))
	if response.Code != 200 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		AvailableServiceCodes []string `json:"available_service_codes"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.AvailableServiceCodes) != 2 {
		t.Fatalf("available_service_codes = %v, want [pilot_gas_104 pilot_fire_101]", body.AvailableServiceCodes)
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

// ADR-031: a phone_incoming event rings for 30 s; until it is answered the
// trainee sees who calls but not what they say, while the instructor
// always sees the text. An answered call's text is visible to both.
func TestIncomingCallProjection(t *testing.T) {
	svc := trainingFixture()
	svc.item.Reaction = content.ReactionAccepted
	svc.events = []training.DeliveredEvent{
		{Key: "e1", Delivery: "notice", From: "crew_leader", Text: "Выехали", DeliveredAt: svc.now.Add(-50 * time.Second)},
		{Key: "e2", Delivery: "phone_incoming", From: "crew_leader", Text: "Бригада на месте", DeliveredAt: svc.now.Add(-40 * time.Second)},
		{Key: "e3", Delivery: "phone_incoming", From: "control", Text: "Почему нет статуса?", DeliveredAt: svc.now.Add(-10 * time.Second)},
	}
	ended := svc.now.Add(-30 * time.Second)
	svc.item.Calls = []training.Call{{ID: uuid.New(), ContactKey: "crew_leader", Direction: training.CallIncoming, EventKey: "e2",
		StartedAt: svc.now.Add(-35 * time.Second), EndedAt: &ended, RecordingState: training.RecordingAbsent}}

	type eventView struct {
		Key      string `json:"key"`
		Text     string `json:"text"`
		Answered *bool  `json:"answered"`
	}
	type view struct {
		IncomingCall *struct {
			EventKey  string `json:"event_key"`
			From      string `json:"from"`
			RingUntil string `json:"ring_until"`
		} `json:"incoming_call"`
		Events []eventView `json:"events"`
		Calls  []struct {
			Direction string  `json:"direction"`
			EventKey  *string `json:"event_key"`
		} `json:"calls"`
	}
	read := func(role auth.Role) view {
		t.Helper()
		response := httptest.NewRecorder()
		trainingMux(svc, trainingPrincipal(role)).ServeHTTP(response, trainingRequest("GET", "/api/v1/items/"+svc.item.ID.String(), nil, true))
		if response.Code != 200 {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		var got view
		if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	trainee := read(auth.RoleTrainee)
	if trainee.IncomingCall == nil || trainee.IncomingCall.EventKey != "e3" || trainee.IncomingCall.From != "control" ||
		trainee.IncomingCall.RingUntil != formatTime(svc.now.Add(20*time.Second)) {
		t.Fatalf("incoming_call = %+v, want e3 from control ringing 20 s more", trainee.IncomingCall)
	}
	if len(trainee.Events) != 3 || trainee.Events[0].Answered != nil || trainee.Events[0].Text != "Выехали" {
		t.Fatalf("notice = %+v", trainee.Events)
	}
	if e := trainee.Events[1]; e.Answered == nil || !*e.Answered || e.Text != "Бригада на месте" {
		t.Fatalf("answered call = %+v", e)
	}
	if e := trainee.Events[2]; e.Answered == nil || *e.Answered || e.Text != "" {
		t.Fatalf("ringing call leaked its text to the trainee: %+v", e)
	}
	if len(trainee.Calls) != 1 || trainee.Calls[0].Direction != "incoming" || trainee.Calls[0].EventKey == nil || *trainee.Calls[0].EventKey != "e2" {
		t.Fatalf("calls = %+v", trainee.Calls)
	}

	instructor := read(auth.RoleInstructor)
	if e := instructor.Events[2]; e.Text != "Почему нет статуса?" {
		t.Fatalf("instructor must see the ringing call's text: %+v", e)
	}

	// Past its 30 s window the call is missed: nothing rings, and its text
	// stays hidden from the trainee.
	svc.now = svc.now.Add(25 * time.Second)
	missed := read(auth.RoleTrainee)
	if missed.IncomingCall != nil || missed.Events[2].Text != "" {
		t.Fatalf("missed call = %+v / %+v", missed.IncomingCall, missed.Events[2])
	}
}

// ADR-031: a DDS monitor row carries its active items' crew reports with
// the actual reaction time.
func TestMonitorReports(t *testing.T) {
	svc := trainingFixture()
	item := svc.item
	item.Contacts = []content.Contact{{Key: "crew_leader", Label: "Руководитель бригады"}}
	delivered := svc.now.Add(-60 * time.Second)
	svc.monitor = training.MonitorResult{Lesson: svc.lesson, Rows: []training.MonitorRow{{
		WorkstationNo: 7, RunID: svc.run.ID, ActiveItems: []training.Item{item},
		Comms: []training.ItemComms{{
			Item:    item,
			Events:  []training.DeliveredEvent{{Key: "e1", Delivery: "notice", From: "crew_leader", Text: "Выехали", DeliveredAt: delivered}},
			Actions: []training.Action{{Type: training.CommandSetStatus, Accepted: true, ServerAt: delivered.Add(12 * time.Second)}},
		}},
	}}}
	response := httptest.NewRecorder()
	trainingMux(svc, trainingPrincipal(auth.RoleInstructor)).ServeHTTP(response, trainingRequest("GET", "/api/v1/lessons/"+svc.lesson.ID.String()+"/monitor", nil, true))
	if response.Code != 200 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var got struct {
		Rows []struct {
			Reports []map[string]any `json:"reports"`
		} `json:"rows"`
	}
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 1 || len(got.Rows[0].Reports) != 1 {
		t.Fatalf("rows = %+v", got.Rows)
	}
	r := got.Rows[0].Reports[0]
	if r["item_id"] != item.ID.String() || r["event_key"] != "e1" || r["from_label"] != "Руководитель бригады" ||
		r["delivered_at"] != formatTime(delivered) || r["reaction_at"] != formatTime(delivered.Add(12*time.Second)) ||
		r["answered_at"] != nil || r["missed"] != false {
		t.Fatalf("report = %+v", r)
	}
	if _, leaked := r["text"]; leaked {
		t.Fatalf("report carries event text: %+v", r)
	}
}
