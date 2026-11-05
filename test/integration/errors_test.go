package integration

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestErrorHandlingAndInvalidInput(t *testing.T) {
	a := newIntegrationAPI(t)
	tests := []struct {
		name, method, path string
		body               io.Reader
		contentType        string
		wantStatus         int
		wantCode           string
	}{
		{"invalid pagination", http.MethodGet, "/api/v1/blogs?page=0&limit=101", nil, "", 400, "VALIDATION_FAILED"},
		{"invalid ID", http.MethodGet, "/api/v1/blogs/not-a-uuid", nil, "", 400, "VALIDATION_FAILED"},
		{"missing blog", http.MethodGet, "/api/v1/blogs/00000000-0000-4000-8000-000000000001", nil, "", 404, "NOT_FOUND"},
		{"malformed JSON", http.MethodPost, "/api/v1/blogs", strings.NewReader(`{"title":`), "application/json", 400, "INVALID_JSON"},
		{"unknown field", http.MethodPost, "/api/v1/blogs", strings.NewReader(`{"title":"x","author":"a","content":"c","description":"d","extra":true}`), "application/json", 400, "VALIDATION_FAILED"},
		{"wrong content type", http.MethodPost, "/api/v1/blogs", strings.NewReader(`{}`), "text/plain", 415, "UNSUPPORTED_MEDIA_TYPE"},
		{"method not allowed", http.MethodPatch, "/api/v1/blogs", nil, "", 405, "METHOD_NOT_ALLOWED"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, tc.body)
			req.RemoteAddr = "198.51.100.10:4321"
			req.Header.Set("X-User-ID", a.editor.ID.String())
			if tc.contentType != "" {
				req.Header.Set("Content-Type", tc.contentType)
			}
			w := httptest.NewRecorder()
			a.router.ServeHTTP(w, req)
			if w.Code != tc.wantStatus || responseErrorCode(t, w) != tc.wantCode {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/blogs", nil)
	req.RemoteAddr = "198.51.100.10:4321"
	req.Header.Set("X-User-ID", "not-a-uuid")
	w := httptest.NewRecorder()
	a.router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized || responseErrorCode(t, w) != "UNAUTHENTICATED" {
		t.Fatalf("invalid authentication returned status=%d body=%s", w.Code, w.Body.String())
	}
}
