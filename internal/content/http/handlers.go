// Package http is the content module's HTTP adapter (CLAUDE.md:
// "HTTP... are adapters") — the instructor catalogue routes
// slice-planning.md §3's C4 adds: GET /services (admin+instructor),
// GET /scenarios, GET /scenarios/{id}, GET /scenarios/{id}/versions,
// GET /scenarios/{id}/preview (instructor). It reuses
// internal/auth/http's SessionMiddleware/RequireRole rather than
// reimplementing session handling — every module's HTTP layer sits
// behind the same auth boundary, not a module-specific one.
package http

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"emsim/internal/auth"
	authhttp "emsim/internal/auth/http"
	"emsim/internal/content"
	"emsim/internal/platform/httpapi"

	"github.com/google/uuid"
)

// contentService is the subset of *content.Service Handlers need —
// declared here (their consumer), matching internal/auth/http's own
// convention.
type contentService interface {
	ListServices(ctx context.Context) ([]content.ServiceRecord, error)
	ListScenarios(ctx context.Context, filter content.ScenarioFilter) ([]content.ScenarioSummary, int, error)
	ScenarioDetail(ctx context.Context, id uuid.UUID) (content.ScenarioDetail, error)
	ScenarioVersions(ctx context.Context, id uuid.UUID) ([]content.VersionSummary, error)
	ScenarioPreview(ctx context.Context, id uuid.UUID) (content.ScenarioPreview, error)

	// The methods below back the 112-7/ADR-027 scenario editor
	// (editor_handlers.go) — declared here alongside the read-only
	// methods above rather than as a second interface, matching this
	// package's existing convention of one consumer-owned port per HTTP
	// package (internal/auth/http's own single Service interface).
	CreateOperator112Scenario(ctx context.Context, actorID uuid.UUID, in content.ScenarioCreateInput) (content.EditorScenario, error)
	EditorScenarioDetail(ctx context.Context, actorID, scenarioID uuid.UUID) (content.EditorScenario, error)
	SaveOperator112Draft(ctx context.Context, actorID, scenarioID uuid.UUID, in content.ScenarioEditInput) (content.EditorScenario, error)
	ValidateOperator112Draft(ctx context.Context, actorID, scenarioID uuid.UUID, body content.Body) ([]content.ValidationIssue, error)
	ProbeOperator112(ctx context.Context, actorID, scenarioID uuid.UUID, body content.Body, text string) ([]content.ProbeMatch, error)
	ApproveOperator112Scenario(ctx context.Context, actorID, scenarioID, versionID uuid.UUID, baseDigestHex string) (content.EditorScenario, error)
	IntakeCatalogForInstructor(ctx context.Context) (content.IntakeCatalog, error)
}

// previewStarter is content/http's own consumer-owned port onto
// training's StartPreview (112-7/ADR-027): POST
// /scenarios/{id}/preview-runs needs to actually create a run, which
// only training can do, but content itself (unlike content/http) never
// depends on training — RFC-001 §4.2's dependency table lists training
// as depending on content, not the reverse. *training.Service satisfies
// this structurally, the same way assessment/reporting's own http
// packages already take a *training.Service parameter for their own
// cross-module reads.
type previewStarter interface {
	StartPreview(ctx context.Context, actorID, scenarioID, versionID uuid.UUID) (lessonID, itemID uuid.UUID, err error)
}

// authenticator is the session-verification port SessionMiddleware needs
// — a *auth.Service satisfies it structurally, so composition
// (cmd/emsim/api.go) passes the same instance auth's own handlers use.
type authenticator interface {
	Authenticate(ctx context.Context, token string) (auth.Principal, error)
}

// Handlers owns GET /services, the /scenarios* catalogue routes, and
// (112-7/ADR-027) the operator-112 scenario editor's own routes.
type Handlers struct {
	content      contentService
	preview      previewStarter
	auth         authenticator
	cookieSecure bool
}

func NewHandlers(content contentService, preview previewStarter, authService authenticator, cookieSecure bool) *Handlers {
	return &Handlers{content: content, preview: preview, auth: authService, cookieSecure: cookieSecure}
}

// Register adds this package's routes to mux, each behind
// SessionMiddleware then the route's own RequireRole group —
// authz.GroupServices for /services (admin+instructor),
// authz.GroupContent for /scenarios* (instructor only).
func (h *Handlers) Register(mux *http.ServeMux) {
	servicesGroup := func(handler http.HandlerFunc) http.Handler {
		return authhttp.SessionMiddleware(h.auth, h.cookieSecure)(authhttp.RequireRole(auth.GroupServices)(handler))
	}
	contentGroup := func(handler http.HandlerFunc) http.Handler {
		return authhttp.SessionMiddleware(h.auth, h.cookieSecure)(authhttp.RequireRole(auth.GroupContent)(handler))
	}
	mux.Handle("GET /api/v1/services", servicesGroup(h.listServices))
	mux.Handle("GET /api/v1/scenarios", contentGroup(h.listScenarios))
	mux.Handle("POST /api/v1/scenarios", contentGroup(h.createScenario))
	mux.Handle("GET /api/v1/scenarios/{scenarioId}", contentGroup(h.scenarioDetail))
	mux.Handle("PUT /api/v1/scenarios/{scenarioId}", contentGroup(h.saveDraft))
	mux.Handle("GET /api/v1/scenarios/{scenarioId}/versions", contentGroup(h.scenarioVersions))
	mux.Handle("GET /api/v1/scenarios/{scenarioId}/preview", contentGroup(h.scenarioPreview))
	mux.Handle("POST /api/v1/scenarios/{scenarioId}/validate", contentGroup(h.validateDraft))
	mux.Handle("POST /api/v1/scenarios/{scenarioId}/probe", contentGroup(h.probe))
	mux.Handle("POST /api/v1/scenarios/{scenarioId}/approve", contentGroup(h.approveScenario))
	mux.Handle("POST /api/v1/scenarios/{scenarioId}/preview-runs", contentGroup(h.startPreviewRun))
	mux.Handle("GET /api/v1/intake112/catalog", contentGroup(h.intakeCatalog))
}

func (h *Handlers) listServices(w http.ResponseWriter, r *http.Request) {
	records, err := h.content.ListServices(r.Context())
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to list services", nil)
		return
	}
	items := make([]serviceJSON, len(records))
	for i, s := range records {
		items[i] = toServiceJSON(s)
	}
	writeJSON(w, r, http.StatusOK, items)
}

func (h *Handlers) listScenarios(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	difficultyMin, err := queryIntInRange(query, "difficulty_min", 1, 10)
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeValidationFailed, "invalid difficulty_min", map[string]any{"field": "difficulty_min"})
		return
	}
	difficultyMax, err := queryIntInRange(query, "difficulty_max", 1, 10)
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeValidationFailed, "invalid difficulty_max", map[string]any{"field": "difficulty_max"})
		return
	}
	if difficultyMin != 0 && difficultyMax != 0 && difficultyMin > difficultyMax {
		httpapi.WriteError(w, r, httpapi.CodeValidationFailed, "difficulty_min must not exceed difficulty_max", map[string]any{"field": "difficulty_min"})
		return
	}

	filter := content.ScenarioFilter{
		TargetService: query.Get("service"),
		ExerciseType:  content.ExerciseType(query.Get("exercise_type")),
		Status:        query.Get("status"),
		DifficultyMin: difficultyMin,
		DifficultyMax: difficultyMax,
		Page:          queryIntOrDefault(query, "page", 1),
		PageSize:      min(queryIntOrDefault(query, "page_size", 50), 200), // openapi.yaml PageSize: maximum 200
	}
	if actor, ok := authhttp.PrincipalFromContext(r.Context()); ok {
		filter.RequestingUserID = actor.UserID
	}
	if filter.ExerciseType != "" && !filter.ExerciseType.Valid() {
		httpapi.WriteError(w, r, httpapi.CodeValidationFailed, "invalid exercise_type", map[string]any{"field": "exercise_type"})
		return
	}

	items, total, err := h.content.ListScenarios(r.Context(), filter)
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to list scenarios", nil)
		return
	}
	summaries := make([]scenarioSummaryJSON, len(items))
	for i, s := range items {
		summaries[i] = toScenarioSummaryJSON(s)
	}
	writeJSON(w, r, http.StatusOK, scenarioListJSON{Items: summaries, Total: total})
}

// scenarioDetail is GET /scenarios/{id}: the approved version for
// everyone (unchanged since slice 2), or — 112-7/ADR-027 — if there is
// none yet, the caller's own latest draft (404 for anyone else, same as
// before: a scenario with no approved version and no draft of the
// caller's own is indistinguishable from one that does not exist).
func (h *Handlers) scenarioDetail(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("scenarioId"))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeNotFound, "scenario not found", nil)
		return
	}
	detail, err := h.content.ScenarioDetail(r.Context(), id)
	if err == nil {
		writeJSON(w, r, http.StatusOK, toScenarioJSON(detail))
		return
	}
	if !errors.Is(err, content.ErrNotFound) {
		writeScenarioLookupError(w, r, err)
		return
	}
	actor, ok := authhttp.PrincipalFromContext(r.Context())
	if !ok {
		httpapi.WriteError(w, r, httpapi.CodeNotFound, "scenario not found", nil)
		return
	}
	editorDetail, err := h.content.EditorScenarioDetail(r.Context(), actor.UserID, id)
	if err != nil {
		writeScenarioLookupError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusOK, toEditorScenarioJSON(editorDetail))
}

func (h *Handlers) scenarioVersions(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("scenarioId"))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeNotFound, "scenario not found", nil)
		return
	}
	versions, err := h.content.ScenarioVersions(r.Context(), id)
	if err != nil {
		writeScenarioLookupError(w, r, err)
		return
	}
	items := make([]versionSummaryJSON, len(versions))
	for i, v := range versions {
		items[i] = toVersionSummaryJSON(v)
	}
	writeJSON(w, r, http.StatusOK, items)
}

func (h *Handlers) scenarioPreview(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("scenarioId"))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeNotFound, "scenario not found", nil)
		return
	}
	preview, err := h.content.ScenarioPreview(r.Context(), id)
	if err != nil {
		writeScenarioLookupError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusOK, toPreviewJSON(preview))
}

func writeScenarioLookupError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, content.ErrNotFound) {
		httpapi.WriteError(w, r, httpapi.CodeNotFound, "scenario not found", nil)
		return
	}
	httpapi.WriteError(w, r, httpapi.CodeInternalError, "operation failed", nil)
}

// queryIntOrDefault mirrors internal/auth/http/admin.go's own helper —
// kept as its own small copy rather than a shared one, since each
// module's HTTP layer is otherwise self-contained.
func queryIntOrDefault(query url.Values, key string, fallback int) int {
	raw := query.Get(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return fallback
	}
	return value
}

// queryIntInRange parses query[key] as an integer in [lo, hi], returning
// 0 (meaning "no filter", content.ScenarioFilter's convention) for an
// absent value, and an error for a present but out-of-range or malformed
// one — unlike pagination, a garbled difficulty filter is a client
// mistake worth a 422, not something to silently clamp.
func queryIntInRange(query url.Values, key string, lo, hi int) (int, error) {
	raw := query.Get(key)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < lo || value > hi {
		return 0, errors.New("out of range")
	}
	return value, nil
}

// -------------------------------------------------------------- JSON DTOs

type workflowJSON struct {
	Transitions     map[string][]string `json:"transitions"`
	CommentRequired []string            `json:"comment_required"`
	Terminal        []string            `json:"terminal"`
}

type serviceJSON struct {
	Code     string       `json:"code"`
	Name     string       `json:"name"`
	Workflow workflowJSON `json:"workflow"`
}

func toServiceJSON(s content.ServiceRecord) serviceJSON {
	transitions := make(map[string][]string, len(s.Workflow.Transitions))
	for from, tos := range s.Workflow.Transitions {
		reactions := make([]string, len(tos))
		for i, r := range tos {
			reactions[i] = string(r)
		}
		transitions[string(from)] = reactions
	}
	return serviceJSON{
		Code: s.Code, Name: s.Name,
		Workflow: workflowJSON{
			Transitions:     transitions,
			CommentRequired: reactionsToStrings(s.Workflow.CommentRequired),
			Terminal:        reactionsToStrings(s.Workflow.Terminal),
		},
	}
}

func reactionsToStrings(reactions []content.Reaction) []string {
	out := make([]string, len(reactions))
	for i, r := range reactions {
		out[i] = string(r)
	}
	return out
}

type scenarioListJSON struct {
	Items []scenarioSummaryJSON `json:"items"`
	Total int                   `json:"total"`
}

type scenarioSummaryJSON struct {
	ID            string  `json:"id"`
	Title         string  `json:"title"`
	ExerciseType  string  `json:"exercise_type"`
	TargetService string  `json:"target_service,omitempty"`
	Difficulty    int     `json:"difficulty"`
	Status        string  `json:"status"`
	Origin        string  `json:"origin"`
	Version       int     `json:"version"`
	SourceKey     *string `json:"source_key"`
	HasEvents     bool    `json:"has_events"`
	// HasVoice is always false in slice 2 — voice_assets do not exist
	// until slice 11 (openapi.yaml ScenarioSummary.has_voice).
	HasVoice  bool   `json:"has_voice"`
	UpdatedAt string `json:"updated_at"`
	// CreatedBy is 112-7/ADR-027's own addition — not secret (an approved
	// scenario is already visible to every instructor), needed so the web
	// editor can decide whether to offer "Редактировать"/"Копировать" for
	// a given scenario without a second request.
	CreatedBy string `json:"created_by"`
}

func toScenarioSummaryJSON(s content.ScenarioSummary) scenarioSummaryJSON {
	return scenarioSummaryJSON{
		ID: s.ID.String(), Title: s.Title, ExerciseType: string(s.ExerciseType), TargetService: s.TargetService, Difficulty: s.Difficulty,
		Status: s.Status, Origin: s.Origin, Version: s.Version, SourceKey: s.SourceKey,
		HasEvents: s.HasEvents, HasVoice: false, UpdatedAt: formatTime(s.UpdatedAt), CreatedBy: s.CreatedBy.String(),
	}
}

// scenarioJSON is openapi.yaml's Scenario (ScenarioSummary + body/
// version_id/digest); scenarioSummaryJSON is embedded unqualified so its
// fields flatten into the same JSON object, matching the contract's
// allOf composition. Body is json.RawMessage (d.BodyJSON) rather than
// content.Body: re-marshaling the typed struct has no omitempty, so an
// optional field the approved file omitted (hints, generation,
// reference.scoring, ...) would come back as an explicit null that
// matches neither Digest nor scenario.schema.json — serving the stored
// canonical bytes as-is keeps the response byte-consistent with Digest.
type scenarioJSON struct {
	scenarioSummaryJSON
	Body json.RawMessage `json:"body"`
	// VersionID/Digest describe the version Body carries: the current
	// approved one, or — 112-7/ADR-027's own owner-only fallback in
	// scenarioDetail — the caller's own latest draft/approved/superseded
	// version. VersionStatus/Issues are only ever populated by that
	// fallback (empty JSON string/omitted array for the ordinary
	// approved-version response, unchanged since slice 2).
	VersionID     string                `json:"version_id"`
	Digest        string                `json:"digest"`
	VersionStatus string                `json:"version_status,omitempty"`
	Issues        []validationIssueJSON `json:"issues,omitempty"`
}

func toScenarioJSON(d content.ScenarioDetail) scenarioJSON {
	return scenarioJSON{
		scenarioSummaryJSON: toScenarioSummaryJSON(d.ScenarioSummary),
		Body:                json.RawMessage(d.BodyJSON),
		VersionID:           d.VersionID.String(),
		Digest:              hex.EncodeToString(d.Digest[:]),
	}
}

func toEditorScenarioJSON(e content.EditorScenario) scenarioJSON {
	summary := content.ScenarioSummary{ScenarioRecord: e.ScenarioRecord, ExerciseType: e.Body.ExerciseType, Version: e.Version, HasEvents: len(e.Body.Events) > 0}
	return scenarioJSON{
		scenarioSummaryJSON: toScenarioSummaryJSON(summary),
		Body:                json.RawMessage(e.BodyJSON),
		VersionID:           e.VersionID.String(),
		Digest:              hex.EncodeToString(e.Digest[:]),
		VersionStatus:       e.Status,
		Issues:              toValidationIssuesJSON(e.Issues),
	}
}

type validationIssueJSON struct {
	Path     string `json:"path"`
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message,omitempty"`
}

// toValidationIssuesJSON always returns a non-nil (possibly empty) slice:
// Go's encoding/json still omits an empty-but-non-nil slice wherever the
// caller's own field has "omitempty" (scenarioJSON.Issues, only ever
// populated by the owner-only draft fallback), but every other caller's
// "issues" field is a required, always-present array per openapi.yaml
// (ScenarioEditResult, POST .../validate) — those must never serialize
// as JSON null, which the web editor's own callers do not all guard
// against (112-7/ADR-027's own regression: clicking "Проверить" on an
// issue-free draft crashed the whole SPA on issues.filter(null)).
func toValidationIssuesJSON(issues []content.ValidationIssue) []validationIssueJSON {
	out := make([]validationIssueJSON, len(issues))
	for i, issue := range issues {
		out[i] = validationIssueJSON{Path: issue.Path, Code: issue.Code, Severity: string(issue.Severity), Message: issue.Message}
	}
	return out
}

type versionSummaryJSON struct {
	ID         string  `json:"id"`
	Version    int     `json:"version"`
	Status     string  `json:"status"`
	CreatedBy  string  `json:"created_by"`
	CreatedAt  string  `json:"created_at"`
	ApprovedAt *string `json:"approved_at"`
	Digest     string  `json:"digest"`
	Difficulty int     `json:"difficulty"`
}

func toVersionSummaryJSON(v content.VersionSummary) versionSummaryJSON {
	var approvedAt *string
	if v.ApprovedAt != nil {
		s := formatTime(*v.ApprovedAt)
		approvedAt = &s
	}
	return versionSummaryJSON{
		ID: v.ID.String(), Version: v.Version, Status: v.Status, CreatedBy: v.CreatedBy.String(),
		CreatedAt: formatTime(v.CreatedAt), ApprovedAt: approvedAt,
		Digest: hex.EncodeToString(v.Digest[:]), Difficulty: v.Difficulty,
	}
}

// previewJSON is openapi.yaml's inline /scenarios/{id}/preview response
// {card, reference}. This instructor-only route carries every populated
// reference field, but through an explicit wire DTO so absent optional
// values are omitted rather than serialized as null/invalid enum zeroes.
// content.CardPreview itself carries no json tags (like auth.User, it is a
// domain type, not a wire type — see internal/auth/http's toUserJSON
// convention), so cardPreviewJSON maps it field by field too.
type previewJSON struct {
	Card      cardPreviewJSON      `json:"card"`
	Reference referencePreviewJSON `json:"reference"`
}

type applicantPreviewJSON struct {
	Name   string `json:"name,omitempty"`
	Phone  string `json:"phone,omitempty"`
	Status string `json:"status,omitempty"`
}

type incidentPreviewJSON struct {
	TypeCode    string         `json:"type_code"`
	TypeName    string         `json:"type_name"`
	Features    map[string]any `json:"features"`
	Description string         `json:"description"`
	Victims     int            `json:"victims"`
	Danger      string         `json:"danger,omitempty"`
}

type notificationPreviewJSON struct {
	Service string `json:"service"`
	Status  string `json:"status"`
	Mine    bool   `json:"mine"`
}

type phonesJSON struct {
	AON      string `json:"aon,omitempty"`
	Provided string `json:"provided,omitempty"`
	OnSite   string `json:"on_site,omitempty"`
}

type contactPreviewJSON struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Number string `json:"number"`
	Voice  string `json:"voice,omitempty"`
}

type cardPreviewJSON struct {
	Number              string                    `json:"number"`
	RegisteredAtOffsetS int                       `json:"registered_at_offset_s"`
	Applicant           applicantPreviewJSON      `json:"applicant"`
	Address             content.Address           `json:"address"` // already json-tagged (body.go) matching openapi.yaml's Address schema
	Incident            incidentPreviewJSON       `json:"incident"`
	NotificationList    []notificationPreviewJSON `json:"notification_list"`
	Phones              phonesJSON                `json:"phones"`
	Channel             string                    `json:"channel,omitempty"`
	Contacts            []contactPreviewJSON      `json:"contacts"`
}

// referencePreviewJSON is deliberately separate from content.Reference.
// Reference is a lossless domain/storage shape, so its zero values marshal
// as null arrays, scoring:null, and invalid empty enum strings. The HTTP
// contract instead omits optional properties and always emits the required
// expected_chain array as an array.
type referencePreviewJSON struct {
	PrimaryDecision  primaryDecisionPreviewJSON   `json:"primary_decision"`
	ExpectedChain    []content.Reaction           `json:"expected_chain"`
	Call             callPreviewJSON              `json:"call"`
	FieldCorrections []fieldCorrectionPreviewJSON `json:"field_corrections,omitempty"`
	PilotGoal        string                       `json:"pilot_goal,omitempty"`
	GuideRefs        []string                     `json:"guide_refs,omitempty"`
	Notes            string                       `json:"notes,omitempty"`
	Scoring          *scoringPreviewJSON          `json:"scoring,omitempty"`
}

type primaryDecisionPreviewJSON struct {
	Status             content.Reaction `json:"status"`
	ReasonTags         []string         `json:"reason_tags,omitempty"`
	CommentRequired    bool             `json:"comment_required,omitempty"`
	CommentMustMention []string         `json:"comment_must_mention,omitempty"`
}

type callPreviewJSON struct {
	Required     bool             `json:"required"`
	To           string           `json:"to,omitempty"`
	MustMention  []string         `json:"must_mention,omitempty"`
	BeforeStatus content.Reaction `json:"before_status,omitempty"`
}

type fieldCorrectionPreviewJSON struct {
	Path          string           `json:"path"`
	ExpectedValue string           `json:"expected_value"`
	BeforeStatus  content.Reaction `json:"before_status"`
}

type scoringPreviewJSON struct {
	Weights  map[string]float64 `json:"weights,omitempty"`
	Critical []string           `json:"critical,omitempty"`
	Disabled []string           `json:"disabled,omitempty"`
	Note     string             `json:"note,omitempty"`
}

func toCardPreviewJSON(c content.CardPreview, contacts []content.ContactPreview) cardPreviewJSON {
	notifications := make([]notificationPreviewJSON, len(c.NotificationList))
	for i, n := range c.NotificationList {
		notifications[i] = notificationPreviewJSON{Service: n.Service, Status: string(n.Status), Mine: n.Mine}
	}
	contactItems := make([]contactPreviewJSON, len(contacts))
	for i, ct := range contacts {
		contactItems[i] = contactPreviewJSON{Key: ct.Key, Label: ct.Label, Number: ct.Number, Voice: ct.Voice}
	}
	return cardPreviewJSON{
		Number: c.Number, RegisteredAtOffsetS: c.RegisteredAtOffsetS,
		Applicant: applicantPreviewJSON{Name: c.Applicant.Name, Phone: c.Applicant.Phone, Status: c.Applicant.Status},
		Address:   c.Address,
		Incident: incidentPreviewJSON{
			TypeCode: c.Incident.TypeCode, TypeName: c.Incident.TypeName, Features: c.Incident.Features,
			Description: c.Incident.Description, Victims: c.Incident.Victims, Danger: c.Incident.Danger,
		},
		NotificationList: notifications,
		Phones:           phonesJSON{AON: c.Phones.AON, Provided: c.Phones.Provided, OnSite: c.Phones.OnSite},
		Channel:          c.Channel,
		Contacts:         contactItems,
	}
}

func toReferencePreviewJSON(r content.Reference) referencePreviewJSON {
	expectedChain := append([]content.Reaction{}, r.ExpectedChain...)
	fieldCorrections := make([]fieldCorrectionPreviewJSON, len(r.FieldCorrections))
	for i, correction := range r.FieldCorrections {
		fieldCorrections[i] = fieldCorrectionPreviewJSON{
			Path: correction.Path, ExpectedValue: correction.ExpectedValue, BeforeStatus: correction.BeforeStatus,
		}
	}

	var scoring *scoringPreviewJSON
	if r.Scoring != nil {
		scoring = &scoringPreviewJSON{
			Weights: r.Scoring.Weights, Critical: r.Scoring.Critical,
			Disabled: r.Scoring.Disabled, Note: r.Scoring.Note,
		}
	}

	return referencePreviewJSON{
		PrimaryDecision: primaryDecisionPreviewJSON{
			Status: r.PrimaryDecision.Status, ReasonTags: r.PrimaryDecision.ReasonTags,
			CommentRequired:    r.PrimaryDecision.CommentRequired,
			CommentMustMention: r.PrimaryDecision.CommentMustMention,
		},
		ExpectedChain: expectedChain,
		Call: callPreviewJSON{
			Required: r.Call.Required, To: r.Call.To, MustMention: r.Call.MustMention,
			BeforeStatus: r.Call.BeforeStatus,
		},
		FieldCorrections: fieldCorrections,
		PilotGoal:        r.PilotGoal, GuideRefs: r.GuideRefs, Notes: r.Notes,
		Scoring: scoring,
	}
}

func toPreviewJSON(p content.ScenarioPreview) any {
	if p.ExerciseType == content.ExerciseTypeOperator112Intake {
		return map[string]any{"exercise_type": p.ExerciseType, "intake112": p.Intake112}
	}
	return previewJSON{
		Card:      toCardPreviewJSON(p.Card, p.Contacts),
		Reference: toReferencePreviewJSON(p.Reference),
	}
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// writeJSON mirrors internal/auth/http's own helper of the same name.
func writeJSON(w http.ResponseWriter, r *http.Request, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
