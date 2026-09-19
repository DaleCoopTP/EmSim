package httpapi

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

// RequestIDHeader is the header every API response carries (RFC-001 §5:
// "Каждый ответ — X-Request-ID").
const RequestIDHeader = "X-Request-ID"

type requestIDKeyType struct{}

var requestIDKey requestIDKeyType

// WithRequestID assigns every request a fresh, server-generated request
// id, stores it in the request context for handlers and audit, sets it as
// the response header, and marks the response Cache-Control: no-store
// (RFC-001 §5, both on every response regardless of outcome). A
// client-supplied X-Request-ID is never trusted or echoed back: accepting
// one would let a client inject arbitrary values into logs and audit.
func WithRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := uuid.NewString()
		w.Header().Set(RequestIDHeader, id)
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

// RequestIDFromContext returns the id WithRequestID stored on ctx, or ""
// if the request never went through WithRequestID.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}
