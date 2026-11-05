package unit

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sky/blog-service/internal/domain"
)

func TestCORSAllowsEveryOrigin(t *testing.T) {
	a := newUnitAPI(t)
	w := a.do(http.MethodGet, "/api/v1/blogs", nil, domain.Anonymous())

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin=%q, want *", got)
	}
}

func TestCORSPreflight(t *testing.T) {
	a := newUnitAPI(t)
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/blogs", nil)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "content-type,x-user-id")
	w := httptest.NewRecorder()

	a.router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d, want=%d body=%s", w.Code, http.StatusNoContent, w.Body.String())
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin=%q, want *", got)
	}
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
		if got := w.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, method) {
			t.Errorf("Access-Control-Allow-Methods=%q does not contain %s", got, method)
		}
	}
	for _, header := range []string{"Content-Type", "X-User-ID", "X-Request-ID"} {
		if got := w.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, header) {
			t.Errorf("Access-Control-Allow-Headers=%q does not contain %s", got, header)
		}
	}
}
