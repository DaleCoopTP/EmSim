package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
)

// MaxJSONBodyBytes is the RFC-001 §9 limit on request bodies: "JSON ≤ 64
// КБ".
const MaxJSONBodyBytes = 64 * 1024

// ErrBodyTooLarge is returned by DecodeJSON when the body exceeded its
// limit; callers map it to a 413 CodePayloadTooLarge response.
var ErrBodyTooLarge = errors.New("request body too large")

// ErrTrailingData is returned by DecodeJSON when the body holds more than
// one JSON value; callers map it to a 400 CodeInvalidRequest response.
var ErrTrailingData = errors.New("request body has trailing data after the JSON value")

// DecodeJSON reads r.Body into dst, enforcing limitBytes (0 means
// MaxJSONBodyBytes) and rejecting any content after the JSON value. It
// leaves r.Body open — the http server closes it once the handler
// returns — and does not consume more than limitBytes+1 bytes from it
// regardless of dst's shape.
func DecodeJSON(r *http.Request, limitBytes int64, dst any) error {
	if limitBytes <= 0 {
		limitBytes = MaxJSONBodyBytes
	}
	body := http.MaxBytesReader(nil, r.Body, limitBytes)
	decoder := json.NewDecoder(body)
	if err := decoder.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return ErrBodyTooLarge
		}
		return err
	}
	if decoder.More() {
		return ErrTrailingData
	}
	return nil
}
