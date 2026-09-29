package http

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"

	authhttp "emsim/internal/auth/http"
	"emsim/internal/content"
	"emsim/internal/platform/httpapi"
	"emsim/internal/training"

	"github.com/google/uuid"
)

// This file is the operator-112 scenario editor's own HTTP surface
// (112-7/ADR-027): create/copy, save (PUT), validate-without-saving,
// the phrase tester, approve, preview-runs, and the instructor's own
// intake catalog view. Every handler here resolves the actor from
// SessionMiddleware's own context (authhttp.PrincipalFromContext) —
// authz.GroupContent (instructor-only) is already applied by
// Register's contentGroup wrapper.

// --------------------------------------------------------------- create

type scenarioCreateBody struct {
	Title             string          `json:"title"`
	Difficulty        int             `json:"difficulty"`
	Body              json.RawMessage `json:"body,omitempty"`
	CopyFromVersionID *string         `json:"copy_from_version_id,omitempty"`
}

func (h *Handlers) createScenario(w http.ResponseWriter, r *http.Request) {
	actor, ok := authhttp.PrincipalFromContext(r.Context())
	if !ok {
		httpapi.WriteError(w, r, httpapi.CodeUnauthorized, "not authenticated", nil)
		return
	}
	var reqBody scenarioCreateBody
	if err := httpapi.DecodeJSON(r, 0, &reqBody); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	in := content.ScenarioCreateInput{Title: reqBody.Title, Difficulty: reqBody.Difficulty}
	switch {
	case reqBody.CopyFromVersionID != nil:
		id, err := uuid.Parse(*reqBody.CopyFromVersionID)
		if err != nil {
			httpapi.WriteError(w, r, httpapi.CodeValidationFailed, "invalid copy_from_version_id", map[string]any{"field": "copy_from_version_id"})
			return
		}
		in.CopyFromVersionID = &id
	case len(reqBody.Body) > 0:
		body, err := decodeScenarioBody(reqBody.Body)
		if err != nil {
			httpapi.WriteError(w, r, httpapi.CodeInvalidRequest, "malformed body", map[string]any{"field": "body"})
			return
		}
		in.Body = &body
	default:
		httpapi.WriteError(w, r, httpapi.CodeValidationFailed, "exactly one of body/copy_from_version_id is required", map[string]any{"field": "body"})
		return
	}

	created, err := h.content.CreateOperator112Scenario(r.Context(), actor.UserID, in)
	if err != nil {
		writeEditorError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusCreated, toEditorScenarioJSON(created))
}

// ----------------------------------------------------------------- save

type scenarioEditBody struct {
	BaseVersionID string          `json:"base_version_id"`
	BaseDigest    string          `json:"base_digest"`
	Title         *string         `json:"title,omitempty"`
	Difficulty    *int            `json:"difficulty,omitempty"`
	Body          json.RawMessage `json:"body"`
}

func (h *Handlers) saveDraft(w http.ResponseWriter, r *http.Request) {
	actor, ok := authhttp.PrincipalFromContext(r.Context())
	if !ok {
		httpapi.WriteError(w, r, httpapi.CodeUnauthorized, "not authenticated", nil)
		return
	}
	scenarioID, err := uuid.Parse(r.PathValue("scenarioId"))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeNotFound, "scenario not found", nil)
		return
	}
	var reqBody scenarioEditBody
	if err := httpapi.DecodeJSON(r, 0, &reqBody); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	baseVersionID, err := uuid.Parse(reqBody.BaseVersionID)
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeValidationFailed, "invalid base_version_id", map[string]any{"field": "base_version_id"})
		return
	}
	body, err := decodeScenarioBody(reqBody.Body)
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInvalidRequest, "malformed body", map[string]any{"field": "body"})
		return
	}
	saved, err := h.content.SaveOperator112Draft(r.Context(), actor.UserID, scenarioID, content.ScenarioEditInput{
		BaseVersionID: baseVersionID, BaseDigestHex: reqBody.BaseDigest, Title: reqBody.Title, Difficulty: reqBody.Difficulty, Body: body,
	})
	if err != nil {
		writeEditorError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusOK, scenarioEditResultJSON{
		Version: saved.Version, VersionID: saved.VersionID.String(),
		Digest: hex.EncodeToString(saved.Digest[:]), Issues: toValidationIssuesJSON(saved.Issues),
	})
}

type scenarioEditResultJSON struct {
	Version   int                   `json:"version"`
	VersionID string                `json:"version_id"`
	Digest    string                `json:"digest"`
	Issues    []validationIssueJSON `json:"issues"`
}

// ------------------------------------------------------------- validate

func (h *Handlers) validateDraft(w http.ResponseWriter, r *http.Request) {
	actor, ok := authhttp.PrincipalFromContext(r.Context())
	if !ok {
		httpapi.WriteError(w, r, httpapi.CodeUnauthorized, "not authenticated", nil)
		return
	}
	scenarioID, err := uuid.Parse(r.PathValue("scenarioId"))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeNotFound, "scenario not found", nil)
		return
	}
	limited := http.MaxBytesReader(nil, r.Body, httpapi.MaxJSONBodyBytes)
	_, body, err := content.DecodeBody(limited)
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInvalidRequest, "malformed body", map[string]any{"field": "body"})
		return
	}
	issues, err := h.content.ValidateOperator112Draft(r.Context(), actor.UserID, scenarioID, body)
	if err != nil {
		writeEditorError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusOK, struct {
		Issues []validationIssueJSON `json:"issues"`
	}{Issues: toValidationIssuesJSON(issues)})
}

// ---------------------------------------------------------------- probe

type scenarioProbeBody struct {
	Text string          `json:"text"`
	Body json.RawMessage `json:"body"`
}

func (h *Handlers) probe(w http.ResponseWriter, r *http.Request) {
	actor, ok := authhttp.PrincipalFromContext(r.Context())
	if !ok {
		httpapi.WriteError(w, r, httpapi.CodeUnauthorized, "not authenticated", nil)
		return
	}
	scenarioID, err := uuid.Parse(r.PathValue("scenarioId"))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeNotFound, "scenario not found", nil)
		return
	}
	var reqBody scenarioProbeBody
	if err := httpapi.DecodeJSON(r, 0, &reqBody); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	body, err := decodeScenarioBody(reqBody.Body)
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInvalidRequest, "malformed body", map[string]any{"field": "body"})
		return
	}
	matches, err := h.content.ProbeOperator112(r.Context(), actor.UserID, scenarioID, body, reqBody.Text)
	if err != nil {
		writeEditorError(w, r, err)
		return
	}
	opened := make([]probeMatchJSON, len(matches))
	for i, m := range matches {
		opened[i] = probeMatchJSON{FactID: m.FactID, Kind: m.Kind}
	}
	writeJSON(w, r, http.StatusOK, struct {
		Opened []probeMatchJSON `json:"opened"`
	}{Opened: opened})
}

type probeMatchJSON struct {
	FactID string `json:"fact_id"`
	Kind   string `json:"kind"`
}

// -------------------------------------------------------------- approve

type scenarioApproveBody struct {
	VersionID  string `json:"version_id"`
	BaseDigest string `json:"base_digest"`
}

func (h *Handlers) approveScenario(w http.ResponseWriter, r *http.Request) {
	actor, ok := authhttp.PrincipalFromContext(r.Context())
	if !ok {
		httpapi.WriteError(w, r, httpapi.CodeUnauthorized, "not authenticated", nil)
		return
	}
	scenarioID, err := uuid.Parse(r.PathValue("scenarioId"))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeNotFound, "scenario not found", nil)
		return
	}
	var reqBody scenarioApproveBody
	if err := httpapi.DecodeJSON(r, 0, &reqBody); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	versionID, err := uuid.Parse(reqBody.VersionID)
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeValidationFailed, "invalid version_id", map[string]any{"field": "version_id"})
		return
	}
	approved, err := h.content.ApproveOperator112Scenario(r.Context(), actor.UserID, scenarioID, versionID, reqBody.BaseDigest)
	if err != nil {
		writeEditorError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusOK, toEditorScenarioJSON(approved))
}

// --------------------------------------------------------- preview-runs

type scenarioPreviewRunBody struct {
	VersionID string `json:"version_id"`
}

func (h *Handlers) startPreviewRun(w http.ResponseWriter, r *http.Request) {
	actor, ok := authhttp.PrincipalFromContext(r.Context())
	if !ok {
		httpapi.WriteError(w, r, httpapi.CodeUnauthorized, "not authenticated", nil)
		return
	}
	scenarioID, err := uuid.Parse(r.PathValue("scenarioId"))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeNotFound, "scenario not found", nil)
		return
	}
	var reqBody scenarioPreviewRunBody
	if err := httpapi.DecodeJSON(r, 0, &reqBody); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	versionID, err := uuid.Parse(reqBody.VersionID)
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeValidationFailed, "invalid version_id", map[string]any{"field": "version_id"})
		return
	}
	if h.preview == nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "preview is not available", nil)
		return
	}
	if err := h.content.CheckPreviewable(r.Context(), actor.UserID, scenarioID, versionID); err != nil {
		writeEditorError(w, r, err)
		return
	}
	lessonID, itemID, err := h.preview.StartPreview(r.Context(), actor.UserID, scenarioID, versionID)
	if err != nil {
		writeEditorError(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusCreated, struct {
		LessonID string `json:"lesson_id"`
		ItemID   string `json:"item_id"`
	}{LessonID: lessonID.String(), ItemID: itemID.String()})
}

// ---------------------------------------------------------- intake112 catalog

func (h *Handlers) intakeCatalog(w http.ResponseWriter, r *http.Request) {
	catalog, err := h.content.IntakeCatalogForInstructor(r.Context())
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to load intake catalog", nil)
		return
	}
	writeJSON(w, r, http.StatusOK, catalog)
}

// -------------------------------------------------------------- helpers

// writeDecodeError mirrors internal/auth/http's own helper of the same
// name — each module's HTTP layer keeps its own small copy rather than
// sharing one (this package's own existing convention, per
// queryIntOrDefault's doc comment in handlers.go).
func writeDecodeError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, httpapi.ErrBodyTooLarge) {
		httpapi.WriteError(w, r, httpapi.CodePayloadTooLarge, "request body too large", nil)
		return
	}
	httpapi.WriteError(w, r, httpapi.CodeInvalidRequest, "malformed request body", nil)
}

// decodeScenarioBody parses raw (a request's own "body" field, already
// isolated by the outer httpapi.DecodeJSON/json.RawMessage) through
// content.DecodeBody — the same duplicate-key-checked, canonical
// pipeline SaveOperator112Draft/CreateOperator112Scenario use to compute
// a digest, so a client reading base_digest back from a prior response
// and echoing the same body gets a matching digest.
func decodeScenarioBody(raw json.RawMessage) (content.Body, error) {
	if len(raw) == 0 {
		return content.Body{}, errors.New("empty body")
	}
	_, body, err := content.DecodeBody(bytes.NewReader(raw))
	return body, err
}

// writeEditorError maps every domain error the editor's own Service
// methods return to the API's closed error codes.
func writeEditorError(w http.ResponseWriter, r *http.Request, err error) {
	var blocking *content.BlockingIssuesError
	if errors.As(err, &blocking) {
		httpapi.WriteError(w, r, httpapi.CodeHasBlockingIssues, "scenario has blocking validation issues",
			map[string]any{"issues": toValidationIssuesJSON(blocking.Issues)})
		return
	}
	var verr *content.ValidationError
	if errors.As(err, &verr) {
		httpapi.WriteError(w, r, httpapi.CodeValidationFailed, "validation failed", map[string]any{"field": verr.Field, "reason": verr.Reason})
		return
	}
	switch {
	case errors.Is(err, content.ErrNotFound):
		httpapi.WriteError(w, r, httpapi.CodeNotFound, "scenario not found", nil)
	case errors.Is(err, content.ErrStaleDraft):
		httpapi.WriteError(w, r, httpapi.CodeStaleDraft, "the scenario was changed since it was last read", nil)
	case errors.Is(err, training.ErrMaintenance):
		httpapi.WriteError(w, r, httpapi.CodeMaintenanceMode, "maintenance mode is on: a preview cannot be started", nil)
	case errors.Is(err, content.ErrUnsupportedForEditor):
		httpapi.WriteError(w, r, httpapi.CodeUnsupportedForEditor, "the editor only supports full_case scenarios with a free-text caller", nil)
	default:
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "operation failed", nil)
	}
}
