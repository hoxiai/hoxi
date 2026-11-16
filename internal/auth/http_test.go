package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogoutClearFailurePreservesAuth(t *testing.T) {
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("failed local logout must not revoke the token")
	})
	s.current = &StoredAuth{DeviceToken: "existing-token", User: UserProfile{Email: "user@example.com"}}
	// A nonempty directory cannot be removed as a credential file, even as root.
	if err := os.Mkdir(s.store.filePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.store.filePath, "blocker"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	state := startTestAuth(t, s)
	generation := s.generation
	r := httptest.NewRequest(http.MethodPost, "/api/hoxi/auth/logout", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	NewHTTPHandler(s).ServeHTTP(w, r)
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "failed to clear local credentials") {
		t.Fatalf("unexpected failure response: %d %s", w.Code, w.Body.String())
	}
	if snap := s.Snapshot(); !snap.IsLoggedIn || snap.User.Email != "user@example.com" || snap.PendingState != state {
		t.Fatalf("failed logout changed auth: %+v", snap)
	}
	if s.CurrentDeviceToken() != "existing-token" || s.generation != generation {
		t.Fatal("failed logout changed token or generation")
	}
}

func TestLogoutRemoteRevokeBestEffort(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusInternalServerError, 0} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var s *Service
			s = testService(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/hoxi/device-tokens/revoke" || r.Header.Get("Authorization") != "Bearer existing-token" {
					t.Error("unexpected revoke request")
				}
				assertStoredToken(t, s, "")
				w.WriteHeader(status)
			})
			var logs bytes.Buffer
			s.logger = slog.New(slog.NewTextHandler(&logs, nil))
			s.current = &StoredAuth{DeviceToken: "existing-token"}
			if err := s.store.Save(s.current); err != nil {
				t.Fatal(err)
			}
			if status == 0 {
				s.serverBaseURL = ":invalid"
			}
			r := httptest.NewRequest(http.MethodPost, "/api/hoxi/auth/logout", strings.NewReader(`{}`))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			NewHTTPHandler(s).ServeHTTP(w, r)
			var result struct{ Success bool }
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != http.StatusOK || !result.Success {
				t.Fatalf("local logout failed: %d %s", w.Code, w.Body.String())
			}
			assertStoredToken(t, s, "")
			if status != http.StatusNoContent && !strings.Contains(logs.String(), "revocation was not confirmed") {
				t.Fatal("unconfirmed revocation was not logged")
			}
			if err := s.Logout(context.Background()); err != nil {
				t.Fatalf("repeated logout failed: %v", err)
			}
		})
	}
}

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
