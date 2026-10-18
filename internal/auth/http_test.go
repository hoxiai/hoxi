package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPOSTRequiresJSON(t *testing.T) {
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("unexpected outbound request")
	})
	h := NewHTTPHandler(s)
	for _, path := range []string{"start", "exchange", "logout"} {
		for _, contentType := range []string{"", "text/plain", "application/x-www-form-urlencoded"} {
			r := httptest.NewRequest(http.MethodPost, "/api/hoxi/auth/"+path, strings.NewReader(`{}`))
			r.Header.Set("Content-Type", contentType)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusUnsupportedMediaType {
				t.Fatalf("%s with %q: status %d", path, contentType, w.Code)
			}
		}
	}
	r := httptest.NewRequest(http.MethodPost, "/api/hoxi/auth/logout", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json; charset=utf-8")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("JSON with charset rejected: %d", w.Code)
	}
}
