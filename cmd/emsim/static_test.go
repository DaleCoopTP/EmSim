package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestNewSPAHandlerServesClosedNotFoundWithoutABuild(t *testing.T) {
	handler := newSPAHandler(fstest.MapFS{".gitkeep": &fstest.MapFile{}})

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, `"code":"not_found"`) {
		t.Fatalf("body = %q, want the closed not_found error envelope", body)
	}
}

func TestNewSPAHandlerServesIndexAtRoot(t *testing.T) {
	handler := builtSPAHandler()

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if response.Body.String() != "<html>index</html>" {
		t.Fatalf("body = %q", response.Body.String())
	}
}

func TestNewSPAHandlerFallsBackToIndexForAClientRoute(t *testing.T) {
	handler := builtSPAHandler()

	// /admin/users names no file in the build — react-router owns it
	// client-side, so it must get the SPA shell, not a 404.
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/users", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if response.Body.String() != "<html>index</html>" {
		t.Fatalf("body = %q, want the SPA shell", response.Body.String())
	}
}

func TestNewSPAHandlerCachesHashedAssetsForever(t *testing.T) {
	handler := builtSPAHandler()

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/assets/app.abc123.js", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if response.Body.String() != "console.log(1)" {
		t.Fatalf("body = %q", response.Body.String())
	}
	if got := response.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("Cache-Control = %q", got)
	}
}

func TestNewSPAHandlerServesARealNonAssetFileAsIs(t *testing.T) {
	handler := builtSPAHandler()

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/favicon.svg", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if response.Body.String() != "<svg/>" {
		t.Fatalf("body = %q", response.Body.String())
	}
	// Only the hashed assets/ directory gets the long-lived override —
	// everything else keeps whatever Cache-Control the outer middleware
	// (httpapi.WithRequestID, not exercised by this handler-level test)
	// already set.
	if got := response.Header().Get("Cache-Control"); got != "" {
		t.Fatalf("Cache-Control = %q, want unset at this layer", got)
	}
}

func TestNewSPAHandlerFallsBackToIndexForADirectoryPath(t *testing.T) {
	handler := builtSPAHandler()

	// "/assets" names a directory (assets/app.abc123.js implies it), not
	// a file — must fall back to the SPA shell, never a directory
	// listing.
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/assets", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if response.Body.String() != "<html>index</html>" {
		t.Fatalf("body = %q, want the SPA shell", response.Body.String())
	}
}

func builtSPAHandler() http.Handler {
	return newSPAHandler(fstest.MapFS{
		"index.html":           &fstest.MapFile{Data: []byte("<html>index</html>")},
		"favicon.svg":          &fstest.MapFile{Data: []byte("<svg/>")},
		"assets/app.abc123.js": &fstest.MapFile{Data: []byte("console.log(1)")},
	})
}
