package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func requestWithID(id string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if id != "" {
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey, id))
	}
	return r
}

func TestWriteErrorEnvelopeShapeAndStatus(t *testing.T) {
	response := httptest.NewRecorder()
	WriteError(response, requestWithID("req-1"), CodeValidationFailed, "workstation_no is required", map[string]any{"field": "workstation_no"})

	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", response.Code)
	}
	if ct := response.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", ct)
	}

	var body struct {
		Error struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"error"`
		RequestID string `json:"request_id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != string(CodeValidationFailed) {
		t.Fatalf("error.code = %q, want %q", body.Error.Code, CodeValidationFailed)
	}
	if body.Error.Message != "workstation_no is required" {
		t.Fatalf("error.message = %q", body.Error.Message)
	}
	if body.Error.Details["field"] != "workstation_no" {
		t.Fatalf("error.details = %v", body.Error.Details)
	}
	if body.RequestID != "req-1" {
		t.Fatalf("request_id = %q, want req-1", body.RequestID)
	}
}

func TestWriteErrorOmitsDetailsWhenNil(t *testing.T) {
	response := httptest.NewRecorder()
	WriteError(response, requestWithID(""), CodeUnauthorized, "invalid credentials", nil)

	var raw map[string]json.RawMessage
	if err := json.NewDecoder(response.Body).Decode(&raw); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	var errorObj map[string]json.RawMessage
	if err := json.Unmarshal(raw["error"], &errorObj); err != nil {
		t.Fatalf("decode error object: %v", err)
	}
	if _, present := errorObj["details"]; present {
		t.Fatalf("details present with nil value: %s", raw["error"])
	}
}

func TestStatusForEveryDocumentedCode(t *testing.T) {
	cases := map[ErrorCode]int{
		CodeInvalidRequest:          http.StatusBadRequest,
		CodeValidationFailed:        http.StatusUnprocessableEntity,
		CodeUnauthorized:            http.StatusUnauthorized,
		CodeForbidden:               http.StatusForbidden,
		CodeNotFound:                http.StatusNotFound,
		CodeConflict:                http.StatusConflict,
		CodeRateLimited:             http.StatusTooManyRequests,
		CodePayloadTooLarge:         http.StatusRequestEntityTooLarge,
		CodeUnsupportedMediaType:    http.StatusUnsupportedMediaType,
		CodeLessonStopped:           http.StatusConflict,
		CodeItemClosed:              http.StatusConflict,
		CodeStaleSeq:                http.StatusConflict,
		CodeTransitionNotAllowed:    http.StatusUnprocessableEntity,
		CodeCommentRequired:         http.StatusUnprocessableEntity,
		CodeInternalError:           http.StatusInternalServerError,
		CodeCommandIDConflict:       http.StatusConflict,
		CodeRecordingConflict:       http.StatusConflict,
		CodeRecordingDeadlinePassed: http.StatusConflict,
		CodeStaleRevision:           http.StatusConflict,
		CodeStaleRecommendation:     http.StatusConflict,
		CodeDictationBusy:           http.StatusTooManyRequests,
		CodeDictationUnavailable:    http.StatusServiceUnavailable,
		CodeMaintenanceMode:         http.StatusConflict,
	}
	for code, want := range cases {
		if got := StatusFor(code); got != want {
			t.Errorf("StatusFor(%q) = %d, want %d", code, got, want)
		}
	}
}

func TestStatusForUnknownCodeIsInternalError(t *testing.T) {
	if got := StatusFor(ErrorCode("not_a_real_code")); got != http.StatusInternalServerError {
		t.Fatalf("StatusFor(unknown) = %d, want 500", got)
	}
}

func TestNotFoundJSONWritesNotFoundEnvelope(t *testing.T) {
	response := httptest.NewRecorder()
	NotFoundJSON(response, requestWithID("req-2"))

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != string(CodeNotFound) {
		t.Fatalf("error.code = %q, want not_found", body.Error.Code)
	}
}
