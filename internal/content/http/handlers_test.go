package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"emsim/internal/auth"
	authhttp "emsim/internal/auth/http"
	"emsim/internal/content"
	"emsim/internal/platform/httpapi"

	"github.com/google/uuid"
)

// fakeAuth is the smallest authenticator content/http's own
// SessionMiddleware usage needs — a single valid token/Principal pair,
// mirroring internal/auth/http's own fakeService.Authenticate.
type fakeAuth struct {
	validToken string
	principal  auth.Principal
	err        error
}

func (f *fakeAuth) Authenticate(_ context.Context, token string) (auth.Principal, error) {
	if f.err != nil {
		return auth.Principal{}, f.err
	}
	if token != f.validToken {
		return auth.Principal{}, auth.ErrSessionInvalid
	}
	return f.principal, nil
}

// fakeContentService drives the HTTP layer independently of
// content.Service's own business logic (covered by internal/content's
// own tests with a fake Store).
type fakeContentService struct {
	servicesResult []content.ServiceRecord
	servicesErr    error

	scenariosResult []content.ScenarioSummary
	scenariosTotal  int
	scenariosErr    error
	lastFilter      content.ScenarioFilter

	detailResult content.ScenarioDetail
	detailErr    error

	versionsResult []content.VersionSummary
	versionsErr    error

	previewResult content.ScenarioPreview
	previewErr    error
}

func (f *fakeContentService) ListServices(context.Context) ([]content.ServiceRecord, error) {
	return f.servicesResult, f.servicesErr
}

func (f *fakeContentService) ListScenarios(_ context.Context, filter content.ScenarioFilter) ([]content.ScenarioSummary, int, error) {
	f.lastFilter = filter
	return f.scenariosResult, f.scenariosTotal, f.scenariosErr
}

func (f *fakeContentService) ScenarioDetail(context.Context, uuid.UUID) (content.ScenarioDetail, error) {
	return f.detailResult, f.detailErr
}

func (f *fakeContentService) ScenarioVersions(context.Context, uuid.UUID) ([]content.VersionSummary, error) {
	return f.versionsResult, f.versionsErr
}

func (f *fakeContentService) ScenarioPreview(context.Context, uuid.UUID) (content.ScenarioPreview, error) {
	return f.previewResult, f.previewErr
}

func adminPrincipal() auth.Principal { return auth.Principal{UserID: uuid.New(), Role: auth.RoleAdmin} }
func instructorPrincipal() auth.Principal {
	return auth.Principal{UserID: uuid.New(), Role: auth.RoleInstructor}
}
func traineePrincipal() auth.Principal {
	return auth.Principal{UserID: uuid.New(), Role: auth.RoleTrainee}
}

func newTestMux(content *fakeContentService, auth *fakeAuth) *http.ServeMux {
	mux := httpapi.NewMux()
	NewHandlers(content, auth, true).Register(mux)
	return mux
}

func wrapped(mux *http.ServeMux) http.Handler {
	return httpapi.WithRequestID(mux)
}

func authedRequest(method, path, token string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	r.AddCookie(&http.Cookie{Name: authhttp.CookieName, Value: token})
	return r
}

func assertErrorEnvelope(t *testing.T, response *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if response.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, wantStatus, response.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if body.Error.Code != wantCode {
		t.Fatalf("error.code = %q, want %q", body.Error.Code, wantCode)
	}
}

var allRoutes = []struct{ method, path string }{
	{http.MethodGet, "/api/v1/services"},
	{http.MethodGet, "/api/v1/scenarios"},
	{http.MethodGet, "/api/v1/scenarios/" + uuid.Nil.String()},
	{http.MethodGet, "/api/v1/scenarios/" + uuid.Nil.String() + "/versions"},
	{http.MethodGet, "/api/v1/scenarios/" + uuid.Nil.String() + "/preview"},
}

func TestRoutesRequireAuthentication(t *testing.T) {
	mux := newTestMux(&fakeContentService{}, &fakeAuth{})
	for _, route := range allRoutes {
		response := httptest.NewRecorder()
		wrapped(mux).ServeHTTP(response, httptest.NewRequest(route.method, route.path, nil))
		assertErrorEnvelope(t, response, http.StatusUnauthorized, "unauthorized")
	}
}

func TestScenarioRoutesRejectAdminAndTrainee(t *testing.T) {
	scenarioRoutes := allRoutes[1:] // everything but /services
	for _, principal := range []auth.Principal{adminPrincipal(), traineePrincipal()} {
		auth := &fakeAuth{validToken: "tok", principal: principal}
		mux := newTestMux(&fakeContentService{}, auth)
		for _, route := range scenarioRoutes {
			response := httptest.NewRecorder()
			wrapped(mux).ServeHTTP(response, authedRequest(route.method, route.path, "tok"))
			assertErrorEnvelope(t, response, http.StatusForbidden, "forbidden")
		}
	}
}

func TestServicesRouteAllowsAdminAndInstructorRejectsTrainee(t *testing.T) {
	for _, principal := range []auth.Principal{adminPrincipal(), instructorPrincipal()} {
		auth := &fakeAuth{validToken: "tok", principal: principal}
		mux := newTestMux(&fakeContentService{servicesResult: []content.ServiceRecord{}}, auth)
		response := httptest.NewRecorder()
		wrapped(mux).ServeHTTP(response, authedRequest(http.MethodGet, "/api/v1/services", "tok"))
		if response.Code != http.StatusOK {
			t.Fatalf("role %s: status = %d, want 200; body=%s", principal.Role, response.Code, response.Body.String())
		}
	}

	auth := &fakeAuth{validToken: "tok", principal: traineePrincipal()}
	mux := newTestMux(&fakeContentService{}, auth)
	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, authedRequest(http.MethodGet, "/api/v1/services", "tok"))
	assertErrorEnvelope(t, response, http.StatusForbidden, "forbidden")
}

func TestListScenariosReturnsItemsAndAppliesFilters(t *testing.T) {
	source := "pilot-tree-01"
	svc := &fakeContentService{
		scenariosResult: []content.ScenarioSummary{{
			ScenarioRecord: content.ScenarioRecord{ID: uuid.New(), Title: "T", TargetService: "dds_district", Difficulty: 1, Origin: "manual", Status: "approved", SourceKey: &source},
			Version:        1,
		}},
		scenariosTotal: 1,
	}
	auth := &fakeAuth{validToken: "tok", principal: instructorPrincipal()}
	mux := newTestMux(svc, auth)

	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, authedRequest(http.MethodGet, "/api/v1/scenarios?service=dds_district&difficulty_min=1&difficulty_max=5&page=2&page_size=10", "tok"))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	if svc.lastFilter.TargetService != "dds_district" || svc.lastFilter.DifficultyMin != 1 || svc.lastFilter.DifficultyMax != 5 || svc.lastFilter.Page != 2 || svc.lastFilter.PageSize != 10 {
		t.Fatalf("filter not applied: %+v", svc.lastFilter)
	}
	var body struct {
		Items []struct {
			SourceKey *string `json:"source_key"`
			HasVoice  bool    `json:"has_voice"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Total != 1 || len(body.Items) != 1 || *body.Items[0].SourceKey != "pilot-tree-01" || body.Items[0].HasVoice {
		t.Fatalf("unexpected body: %+v", body)
	}
}

func TestListScenariosRejectsInvertedDifficultyRange(t *testing.T) {
	auth := &fakeAuth{validToken: "tok", principal: instructorPrincipal()}
	mux := newTestMux(&fakeContentService{}, auth)
	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, authedRequest(http.MethodGet, "/api/v1/scenarios?difficulty_min=8&difficulty_max=2", "tok"))
	assertErrorEnvelope(t, response, http.StatusUnprocessableEntity, "validation_failed")
}

func TestScenarioDetailNotFoundMapsTo404(t *testing.T) {
	auth := &fakeAuth{validToken: "tok", principal: instructorPrincipal()}
	mux := newTestMux(&fakeContentService{detailErr: content.ErrNotFound}, auth)
	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, authedRequest(http.MethodGet, "/api/v1/scenarios/"+uuid.New().String(), "tok"))
	assertErrorEnvelope(t, response, http.StatusNotFound, "not_found")
}

func TestScenarioDetailBadUUIDMapsTo404(t *testing.T) {
	auth := &fakeAuth{validToken: "tok", principal: instructorPrincipal()}
	mux := newTestMux(&fakeContentService{}, auth)
	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, authedRequest(http.MethodGet, "/api/v1/scenarios/not-a-uuid", "tok"))
	assertErrorEnvelope(t, response, http.StatusNotFound, "not_found")
}

func TestScenarioDetailReturnsFullBody(t *testing.T) {
	id := uuid.New()
	versionID := uuid.New()
	body := content.Body{
		TargetService: "dds_district",
		Reference:     content.Reference{PrimaryDecision: content.PrimaryDecision{Status: content.ReactionAccepted}, Notes: "instructor-only note"},
		Difficulty:    2, ExerciseType: content.ExerciseTypeDDSProcessing,
	}
	// BodyJSON stands in for what a real read populates it with
	// (postgres/store.go's scanScenarioVersion, from scenario_versions.body)
	// — the handler now serves it verbatim rather than re-marshaling Body.
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	detail := content.ScenarioDetail{
		ScenarioSummary: content.ScenarioSummary{
			ScenarioRecord: content.ScenarioRecord{ID: id, Title: "T", TargetService: "dds_district", Difficulty: 2, Origin: "manual", Status: "approved"},
			Version:        1,
		},
		VersionID: versionID,
		Body:      body,
		BodyJSON:  bodyJSON,
	}
	auth := &fakeAuth{validToken: "tok", principal: instructorPrincipal()}
	mux := newTestMux(&fakeContentService{detailResult: detail}, auth)

	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, authedRequest(http.MethodGet, "/api/v1/scenarios/"+id.String(), "tok"))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "instructor-only note") {
		t.Fatalf("scenario detail must include the full reference for an instructor: %s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), versionID.String()) {
		t.Fatalf("scenario detail must include version_id: %s", response.Body.String())
	}
}

func TestScenarioPreviewDoesNotLeakClosedFieldsOutsideReference(t *testing.T) {
	const sentinel = "SECRET-PILOT-GOAL-VALUE"
	preview := content.ScenarioPreview{
		Card: content.CardPreview{Number: "1"},
		Reference: content.Reference{
			PrimaryDecision: content.PrimaryDecision{Status: content.ReactionAccepted},
			PilotGoal:       sentinel,
		},
	}
	auth := &fakeAuth{validToken: "tok", principal: instructorPrincipal()}
	mux := newTestMux(&fakeContentService{previewResult: preview}, auth)

	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, authedRequest(http.MethodGet, "/api/v1/scenarios/"+uuid.New().String()+"/preview", "tok"))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Card map[string]any `json:"card"`
	}
	if err := json.NewDecoder(strings.NewReader(response.Body.String())).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for k := range body.Card {
		if k == "pilot_goal" || k == "reference" {
			t.Fatalf("preview.card must not carry %q", k)
		}
	}
	if !strings.Contains(response.Body.String(), sentinel) {
		t.Fatalf("preview.reference should still carry pilot_goal (instructor-only route): %s", response.Body.String())
	}
}

func TestScenarioPreviewReferenceOmitsAbsentOptionalFields(t *testing.T) {
	preview := content.ScenarioPreview{
		Card: content.CardPreview{Number: "1"},
		Reference: content.Reference{
			PrimaryDecision: content.PrimaryDecision{Status: content.ReactionAccepted},
		},
	}
	auth := &fakeAuth{validToken: "tok", principal: instructorPrincipal()}
	mux := newTestMux(&fakeContentService{previewResult: preview}, auth)

	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, authedRequest(http.MethodGet, "/api/v1/scenarios/"+uuid.New().String()+"/preview", "tok"))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Reference map[string]any `json:"reference"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, field := range []string{"scoring", "field_corrections", "pilot_goal", "guide_refs", "notes"} {
		if _, exists := body.Reference[field]; exists {
			t.Fatalf("preview.reference must omit absent %q: %s", field, response.Body.String())
		}
	}
	chain, exists := body.Reference["expected_chain"]
	if !exists {
		t.Fatalf("preview.reference must include required expected_chain: %s", response.Body.String())
	}
	if items, ok := chain.([]any); !ok || len(items) != 0 {
		t.Fatalf("expected_chain = %#v, want []", chain)
	}
	call, ok := body.Reference["call"].(map[string]any)
	if !ok {
		t.Fatalf("call = %#v, want object", body.Reference["call"])
	}
	if _, exists := call["before_status"]; exists {
		t.Fatalf("preview.reference.call must omit absent before_status: %s", response.Body.String())
	}
	if _, exists := call["must_mention"]; exists {
		t.Fatalf("preview.reference.call must omit absent must_mention: %s", response.Body.String())
	}
}

func TestScenarioVersionsReturnsMetadata(t *testing.T) {
	versionID := uuid.New()
	createdBy := uuid.New()
	auth := &fakeAuth{validToken: "tok", principal: instructorPrincipal()}
	svc := &fakeContentService{versionsResult: []content.VersionSummary{{
		ID: versionID, Version: 1, Status: "approved", Difficulty: 1, CreatedBy: createdBy,
	}}}
	mux := newTestMux(svc, auth)

	response := httptest.NewRecorder()
	wrapped(mux).ServeHTTP(response, authedRequest(http.MethodGet, "/api/v1/scenarios/"+uuid.New().String()+"/versions", "tok"))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	var items []struct {
		ID      string `json:"id"`
		Version int    `json:"version"`
		Status  string `json:"status"`
	}
	if err := json.NewDecoder(response.Body).Decode(&items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(items) != 1 || items[0].ID != versionID.String() || items[0].Version != 1 || items[0].Status != "approved" {
		t.Fatalf("unexpected items: %+v", items)
	}
}
