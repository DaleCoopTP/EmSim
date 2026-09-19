package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"emsim/internal/auth"

	"github.com/google/uuid"
)

func adminPrincipal() auth.Principal {
	return auth.Principal{UserID: uuid.New(), Role: auth.RoleAdmin}
}

func trainingPrincipal(role auth.Role) auth.Principal {
	return auth.Principal{UserID: uuid.New(), Role: role}
}

func sampleUser(role auth.Role) auth.User {
	return auth.User{
		ID: uuid.New(), Login: "dispatcher-1", FullName: "Иванов Иван", Role: role,
		Level: auth.LevelEasy, Active: true,
	}
}

func TestAdminRoutesRequireAuthentication(t *testing.T) {
	svc := &fakeService{}
	mux := newTestAdminMux(svc)

	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil),
		httptest.NewRequest(http.MethodPost, "/api/v1/admin/users", strings.NewReader(`{}`)),
		httptest.NewRequest(http.MethodGet, "/api/v1/admin/workstations", nil),
		httptest.NewRequest(http.MethodPut, "/api/v1/admin/workstations", strings.NewReader(`[]`)),
	} {
		response := httptest.NewRecorder()
		wrapped(mux).ServeHTTP(response, req)
		assertErrorEnvelope(t, response, http.StatusUnauthorized, "unauthorized")
	}
}

func TestAdminRoutesRejectNonAdminRoles(t *testing.T) {
	for _, role := range []auth.Role{auth.RoleInstructor, auth.RoleTrainee} {
		svc := &fakeService{validToken: "tok", principal: trainingPrincipal(role)}
		mux := newTestAdminMux(svc)

		response := httptest.NewRecorder()
		wrapped(mux).ServeHTTP(response, authedAdminRequest(http.MethodGet, "/api/v1/admin/users", nil, "tok"))
		assertErrorEnvelope(t, response, http.StatusForbidden, "forbidden")
	}
}

func TestListUsersReturnsItemsAndTotal(t *testing.T) {
	svc := &fakeService{
		validToken: "tok", principal: adminPrincipal(),
		listUsersResult: []auth.User{sampleUser(auth.RoleAdmin), sampleUser(auth.RoleTrainee)}, listUsersTotal: 5,
	}
	mux := newTestAdminMux(svc)

	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, authedAdminRequest(http.MethodGet, "/api/v1/admin/users?page=2&page_size=2", nil, "tok"))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	var body userListJSON
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Total != 5 || len(body.Items) != 2 {
		t.Fatalf("body = %+v", body)
	}
	if len(svc.listUsersCalls) != 1 || svc.listUsersCalls[0].page != 2 || svc.listUsersCalls[0].pageSize != 2 {
		t.Fatalf("listUsersCalls = %+v", svc.listUsersCalls)
	}
}

func TestListUsersClampsPageSizeTo200(t *testing.T) {
	svc := &fakeService{validToken: "tok", principal: adminPrincipal()}
	mux := newTestAdminMux(svc)

	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, authedAdminRequest(http.MethodGet, "/api/v1/admin/users?page_size=99999", nil, "tok"))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if svc.listUsersCalls[0].pageSize != 200 {
		t.Fatalf("pageSize = %d, want clamped to 200", svc.listUsersCalls[0].pageSize)
	}
}

func TestListUsersDefaultsPageAndPageSizeOnGarbageInput(t *testing.T) {
	svc := &fakeService{validToken: "tok", principal: adminPrincipal()}
	mux := newTestAdminMux(svc)

	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, authedAdminRequest(http.MethodGet, "/api/v1/admin/users?page=abc&page_size=-5", nil, "tok"))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if svc.listUsersCalls[0].page != 1 || svc.listUsersCalls[0].pageSize != 50 {
		t.Fatalf("call = %+v, want defaults {1 50}", svc.listUsersCalls[0])
	}
}

func TestCreateUserReturns201WithBody(t *testing.T) {
	created := sampleUser(auth.RoleTrainee)
	svc := &fakeService{validToken: "tok", principal: adminPrincipal(), createUserResult: created}
	mux := newTestAdminMux(svc)

	body := `{"login":"dispatcher-1","password":"correct-horse","full_name":"Иванов Иван","role":"trainee","service_code":"dds_district"}`
	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, authedAdminRequest(http.MethodPost, "/api/v1/admin/users", strings.NewReader(body), "tok"))

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", response.Code, response.Body.String())
	}
	var got userJSON
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Login != created.Login || got.Role != "trainee" {
		t.Fatalf("body = %+v", got)
	}
	if len(svc.createUserCalls) != 1 || svc.createUserCalls[0].Login != "dispatcher-1" || svc.createUserCalls[0].Password != "correct-horse" {
		t.Fatalf("createUserCalls = %+v", svc.createUserCalls)
	}
}

func TestCreateUserMapsLoginTakenTo409(t *testing.T) {
	svc := &fakeService{validToken: "tok", principal: adminPrincipal(), createUserErr: auth.ErrLoginTaken}
	mux := newTestAdminMux(svc)

	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, authedAdminRequest(http.MethodPost, "/api/v1/admin/users", strings.NewReader(`{"login":"a","password":"12345678","full_name":"x","role":"admin"}`), "tok"))

	assertErrorEnvelope(t, response, http.StatusConflict, "conflict")
}

func TestCreateUserMapsValidationErrorTo422WithField(t *testing.T) {
	svc := &fakeService{validToken: "tok", principal: adminPrincipal(), createUserErr: &auth.ValidationError{Field: "login", Reason: "invalid"}}
	mux := newTestAdminMux(svc)

	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, authedAdminRequest(http.MethodPost, "/api/v1/admin/users", strings.NewReader(`{"login":"a","password":"12345678","full_name":"x","role":"admin"}`), "tok"))

	// assertErrorEnvelope decodes response.Body (a stream); capture the
	// bytes first so this test can decode details afterward too.
	bodyBytes := append([]byte{}, response.Body.Bytes()...)
	assertErrorEnvelope(t, response, http.StatusUnprocessableEntity, "validation_failed")

	var body struct {
		Error struct {
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error.Details["field"] != "login" {
		t.Fatalf("details = %v", body.Error.Details)
	}
}

func TestPatchUserSendsPathIDAndParsedPatch(t *testing.T) {
	userID := uuid.New()
	updated := sampleUser(auth.RoleAdmin)
	updated.ID = userID
	svc := &fakeService{validToken: "tok", principal: adminPrincipal(), updateUserResult: updated}
	mux := newTestAdminMux(svc)

	body := `{"full_name":"Новое Имя","active":false}`
	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, authedAdminRequest(http.MethodPatch, "/api/v1/admin/users/"+userID.String(), strings.NewReader(body), "tok"))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	if len(svc.updateUserCalls) != 1 {
		t.Fatalf("updateUserCalls = %+v", svc.updateUserCalls)
	}
	call := svc.updateUserCalls[0]
	if call.id != userID {
		t.Fatalf("id = %v, want %v", call.id, userID)
	}
	if call.patch.FullName == nil || *call.patch.FullName != "Новое Имя" {
		t.Fatalf("patch.FullName = %v", call.patch.FullName)
	}
	if call.patch.Active == nil || *call.patch.Active != false {
		t.Fatalf("patch.Active = %v", call.patch.Active)
	}
	if call.patch.Password != nil || call.patch.Role != nil || call.patch.ServiceCode != nil {
		t.Fatalf("untouched fields were set: %+v", call.patch)
	}
}

func TestPatchUserExplicitNullServiceCodeMeansClear(t *testing.T) {
	svc := &fakeService{validToken: "tok", principal: adminPrincipal(), updateUserResult: sampleUser(auth.RoleAdmin)}
	mux := newTestAdminMux(svc)

	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, authedAdminRequest(http.MethodPatch, "/api/v1/admin/users/"+uuid.New().String(), strings.NewReader(`{"service_code":null}`), "tok"))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	patch := svc.updateUserCalls[0].patch
	if patch.ServiceCode == nil || *patch.ServiceCode != "" {
		t.Fatalf("patch.ServiceCode = %v, want a pointer to \"\" (the clear sentinel)", patch.ServiceCode)
	}
}

func TestPatchUserRejectsNullForNonNullableField(t *testing.T) {
	svc := &fakeService{validToken: "tok", principal: adminPrincipal()}
	mux := newTestAdminMux(svc)

	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, authedAdminRequest(http.MethodPatch, "/api/v1/admin/users/"+uuid.New().String(), strings.NewReader(`{"active":null}`), "tok"))

	assertErrorEnvelope(t, response, http.StatusBadRequest, "invalid_request")
	if len(svc.updateUserCalls) != 0 {
		t.Fatal("Service.UpdateUser was called for active:null")
	}
}

func TestPatchUserRejectsNullBody(t *testing.T) {
	svc := &fakeService{validToken: "tok", principal: adminPrincipal()}
	mux := newTestAdminMux(svc)

	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, authedAdminRequest(http.MethodPatch, "/api/v1/admin/users/"+uuid.New().String(), strings.NewReader(`null`), "tok"))

	assertErrorEnvelope(t, response, http.StatusBadRequest, "invalid_request")
	if len(svc.updateUserCalls) != 0 {
		t.Fatal("Service.UpdateUser was called for a null body")
	}
}

func TestPatchUserOmittedServiceCodeMeansNoChange(t *testing.T) {
	svc := &fakeService{validToken: "tok", principal: adminPrincipal(), updateUserResult: sampleUser(auth.RoleAdmin)}
	mux := newTestAdminMux(svc)

	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, authedAdminRequest(http.MethodPatch, "/api/v1/admin/users/"+uuid.New().String(), strings.NewReader(`{"full_name":"x"}`), "tok"))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if svc.updateUserCalls[0].patch.ServiceCode != nil {
		t.Fatalf("patch.ServiceCode = %v, want nil (key was absent)", svc.updateUserCalls[0].patch.ServiceCode)
	}
}

func TestPatchUserInvalidUUIDIs404(t *testing.T) {
	svc := &fakeService{validToken: "tok", principal: adminPrincipal()}
	mux := newTestAdminMux(svc)

	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, authedAdminRequest(http.MethodPatch, "/api/v1/admin/users/not-a-uuid", strings.NewReader(`{}`), "tok"))

	assertErrorEnvelope(t, response, http.StatusNotFound, "not_found")
	if len(svc.updateUserCalls) != 0 {
		t.Fatal("Service.UpdateUser was called with an invalid path id")
	}
}

func TestPatchUserMapsLastAdminTo409(t *testing.T) {
	svc := &fakeService{validToken: "tok", principal: adminPrincipal(), updateUserErr: auth.ErrLastAdmin}
	mux := newTestAdminMux(svc)

	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, authedAdminRequest(http.MethodPatch, "/api/v1/admin/users/"+uuid.New().String(), strings.NewReader(`{"active":false}`), "tok"))

	assertErrorEnvelope(t, response, http.StatusConflict, "conflict")
}

func TestPatchUserMapsNotFoundTo404(t *testing.T) {
	svc := &fakeService{validToken: "tok", principal: adminPrincipal(), updateUserErr: auth.ErrNotFound}
	mux := newTestAdminMux(svc)

	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, authedAdminRequest(http.MethodPatch, "/api/v1/admin/users/"+uuid.New().String(), strings.NewReader(`{"active":false}`), "tok"))

	assertErrorEnvelope(t, response, http.StatusNotFound, "not_found")
}

func TestListWorkstationsReturnsArray(t *testing.T) {
	svc := &fakeService{
		validToken: "tok", principal: adminPrincipal(),
		listWorkstationsResult: []auth.Workstation{{ID: uuid.New(), Number: 1, Label: "РМ-01", Active: true}},
	}
	mux := newTestAdminMux(svc)

	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, authedAdminRequest(http.MethodGet, "/api/v1/admin/workstations", nil, "tok"))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	var got []workstationJSON
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].Number != 1 {
		t.Fatalf("body = %+v", got)
	}
}

func TestReplaceWorkstationsSendsParsedArrayAndReturnsResult(t *testing.T) {
	result := []auth.Workstation{
		{ID: uuid.New(), Number: 1, Label: "РМ-01", Active: true},
		{ID: uuid.New(), Number: 2, Label: "РМ-02", Active: false},
	}
	svc := &fakeService{validToken: "tok", principal: adminPrincipal(), replaceWorkstationsResult: result}
	mux := newTestAdminMux(svc)

	body := `[{"number":1,"label":"РМ-01"}]`
	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, authedAdminRequest(http.MethodPut, "/api/v1/admin/workstations", strings.NewReader(body), "tok"))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	if len(svc.replaceWorkstationsCalls) != 1 || len(svc.replaceWorkstationsCalls[0]) != 1 || svc.replaceWorkstationsCalls[0][0].Number != 1 {
		t.Fatalf("replaceWorkstationsCalls = %+v", svc.replaceWorkstationsCalls)
	}
	var got []workstationJSON
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("body = %+v, want the full resulting list (2 rows) not just what was sent", got)
	}
}

func TestReplaceWorkstationsMapsValidationErrorTo422(t *testing.T) {
	svc := &fakeService{validToken: "tok", principal: adminPrincipal(), replaceWorkstationsErr: &auth.ValidationError{Field: "number", Reason: "duplicate"}}
	mux := newTestAdminMux(svc)

	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, authedAdminRequest(http.MethodPut, "/api/v1/admin/workstations", strings.NewReader(`[{"number":1,"label":"a"},{"number":1,"label":"b"}]`), "tok"))

	assertErrorEnvelope(t, response, http.StatusUnprocessableEntity, "validation_failed")
}

func TestReplaceWorkstationsRejectsNullWithoutMutating(t *testing.T) {
	svc := &fakeService{validToken: "tok", principal: adminPrincipal()}
	mux := newTestAdminMux(svc)

	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, authedAdminRequest(http.MethodPut, "/api/v1/admin/workstations", strings.NewReader(`null`), "tok"))

	assertErrorEnvelope(t, response, http.StatusBadRequest, "invalid_request")
	if len(svc.replaceWorkstationsCalls) != 0 {
		t.Fatal("Service.ReplaceWorkstations was called for a null body")
	}
}
