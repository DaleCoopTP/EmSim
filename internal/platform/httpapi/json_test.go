package httpapi

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

type decodeTarget struct {
	Login string `json:"login"`
}

func TestDecodeJSONHappyPath(t *testing.T) {
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"login":"dispatcher-1"}`))
	var got decodeTarget
	if err := DecodeJSON(r, 0, &got); err != nil {
		t.Fatalf("DecodeJSON() error = %v", err)
	}
	if got.Login != "dispatcher-1" {
		t.Fatalf("Login = %q", got.Login)
	}
}

func TestDecodeJSONRejectsOversizedBody(t *testing.T) {
	body := `{"login":"` + strings.Repeat("a", 100) + `"}`
	r := httptest.NewRequest("POST", "/", strings.NewReader(body))
	var got decodeTarget
	err := DecodeJSON(r, 16, &got)
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("DecodeJSON() error = %v, want ErrBodyTooLarge", err)
	}
}

func TestDecodeJSONDefaultLimitAllowsUnderMaxJSONBodyBytes(t *testing.T) {
	body := `{"login":"` + strings.Repeat("a", 100) + `"}`
	r := httptest.NewRequest("POST", "/", strings.NewReader(body))
	var got decodeTarget
	if err := DecodeJSON(r, 0, &got); err != nil {
		t.Fatalf("DecodeJSON() error = %v", err)
	}
}

func TestDecodeJSONRejectsTrailingData(t *testing.T) {
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"login":"a"}{"login":"b"}`))
	var got decodeTarget
	err := DecodeJSON(r, 0, &got)
	if !errors.Is(err, ErrTrailingData) {
		t.Fatalf("DecodeJSON() error = %v, want ErrTrailingData", err)
	}
}

func TestDecodeJSONRejectsMalformedJSON(t *testing.T) {
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"login":`))
	var got decodeTarget
	err := DecodeJSON(r, 0, &got)
	if err == nil || errors.Is(err, ErrBodyTooLarge) || errors.Is(err, ErrTrailingData) {
		t.Fatalf("DecodeJSON() error = %v, want a plain decode error", err)
	}
}
