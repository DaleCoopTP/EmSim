package httpapi

import (
	"encoding/json"
	"net/http"
)

// ErrorCode is the closed set of error codes the API ever returns
// (design-docs/contracts/openapi.yaml components.schemas.Error.code —
// "закрытым списком кодов", RFC-001 §5). Every module reuses this type
// instead of inventing its own strings.
type ErrorCode string

const (
	CodeInvalidRequest          ErrorCode = "invalid_request"
	CodeValidationFailed        ErrorCode = "validation_failed"
	CodeUnauthorized            ErrorCode = "unauthorized"
	CodeForbidden               ErrorCode = "forbidden"
	CodeNotFound                ErrorCode = "not_found"
	CodeConflict                ErrorCode = "conflict"
	CodeRateLimited             ErrorCode = "rate_limited"
	CodePayloadTooLarge         ErrorCode = "payload_too_large"
	CodeUnsupportedMediaType    ErrorCode = "unsupported_media_type"
	CodeLessonStopped           ErrorCode = "lesson_stopped"
	CodeItemClosed              ErrorCode = "item_closed"
	CodeStaleSeq                ErrorCode = "stale_seq"
	CodeTransitionNotAllowed    ErrorCode = "transition_not_allowed"
	CodeCommentRequired         ErrorCode = "comment_required"
	CodeInternalError           ErrorCode = "internal_error"
	CodeCommandIDConflict       ErrorCode = "command_id_conflict"
	CodeRecordingConflict       ErrorCode = "recording_conflict"
	CodeRecordingDeadlinePassed ErrorCode = "recording_deadline_passed"
	CodeStaleRevision           ErrorCode = "stale_revision"
	CodeStaleRecommendation     ErrorCode = "stale_recommendation"
)

// statusFor is the fixed HTTP status each code carries. Five of them —
// CodeLessonStopped, CodeItemClosed, CodeStaleSeq, CodeTransitionNotAllowed
// and CodeCommentRequired — belong to the trainee command endpoint
// (RFC-001 §7.1/openapi.yaml POST /items/{itemId}/actions) and are only
// ever written inside its Receipt body, not through WriteError's generic
// Error envelope; they are listed here only so the status they imply is
// recorded in one place and reused when that endpoint is built (slice 3).
var statusFor = map[ErrorCode]int{
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
}

// StatusFor returns the HTTP status WriteError sends for code, or 500 for
// a code outside the closed set (unreachable through the typed constants
// above; only possible if a caller constructs an ErrorCode from a raw
// string).
func StatusFor(code ErrorCode) int {
	if status, ok := statusFor[code]; ok {
		return status
	}
	return http.StatusInternalServerError
}

type errorEnvelope struct {
	Error struct {
		Code    ErrorCode      `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details,omitempty"`
	} `json:"error"`
	RequestID string `json:"request_id"`
}

// WriteError writes the closed error envelope (openapi.yaml Error schema)
// with the status StatusFor(code) implies. message is safe, operator-
// facing text: callers must not put user input or internal detail in it
// (CLAUDE.md: "Return safe public errors; keep operational detail in
// structured logs"). details is optional structured context (e.g.
// {"field": "workstation_no"}) and must itself carry no secrets — it is
// part of the response, not the log.
//
// WriteError is for the generic Error envelope only. The five command-
// receipt codes documented on statusFor are written by the training
// module as part of a Receipt body instead (slice 3).
func WriteError(w http.ResponseWriter, r *http.Request, code ErrorCode, message string, details map[string]any) {
	body := errorEnvelope{RequestID: RequestIDFromContext(r.Context())}
	body.Error.Code = code
	body.Error.Message = message
	body.Error.Details = details
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(StatusFor(code))
	_ = json.NewEncoder(w).Encode(body)
}

// NotFoundJSON answers an unmatched /api/ path with the same error
// envelope every other endpoint uses, instead of the stdlib mux's empty
// 404 body.
func NotFoundJSON(w http.ResponseWriter, r *http.Request) {
	WriteError(w, r, CodeNotFound, "resource not found", nil)
}
