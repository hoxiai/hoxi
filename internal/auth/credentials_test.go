package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCredentialCommitIsOrderedWithLogout(t *testing.T) {
	s := testService(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	s.current = &StoredAuth{DeviceToken: "old"}
	if err := s.store.Save(s.current); err != nil {
		t.Fatal(err)
	}
	started, resume := make(chan struct{}), make(chan struct{})
	var inCommit, clearCalled atomic.Bool
	s.SetOnClear(func() error {
		if inCommit.Load() {
			t.Error("logout cleared credentials during an active commit")
		}
		clearCalled.Store(true)
		return nil
	})
	done := make(chan error, 1)
	go func() {
		accepted, err := s.WithCurrentDeviceToken("old", s.ServerBaseURL(), func() error {
			inCommit.Store(true)
			close(started)
			<-resume
			inCommit.Store(false)
			return nil
		})
		if !accepted && err == nil {
			err = errors.New("current account was rejected")
		}
		done <- err
	}()
	<-started
	logoutDone := make(chan error, 1)
	go func() { logoutDone <- s.Logout(context.Background()) }()
	close(resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-logoutDone; err != nil {
		t.Fatal(err)
	}
	if !clearCalled.Load() {
		t.Fatal("logout did not clear dependent credentials")
	}
	accepted, err := s.WithCurrentDeviceToken("old", s.ServerBaseURL(), func() error { t.Error("stale commit was executed"); return nil })
	if accepted || err != nil {
		t.Fatalf("unexpected stale-account result: %v %v", accepted, err)
	}
}

func TestLogoutPropagatesDependentCleanupFailureAndRetries(t *testing.T) {
	s := testService(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	s.current = &StoredAuth{DeviceToken: "old"}
	if err := s.store.Save(s.current); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	s.SetOnClear(func() error {
		attempts++
		if attempts == 1 {
			return errors.New("provider config is read-only")
		}
		return nil
	})
	req := httptest.NewRequest(http.MethodPost, "/api/hoxi/auth/logout", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	NewHTTPHandler(s).ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "provider config is read-only") {
		t.Fatalf("cleanup failure hidden: %d %s", rec.Code, rec.Body.String())
	}
	if s.Snapshot().IsLoggedIn {
		t.Fatal("cleared device token must stay invalidated despite dependent cleanup error")
	}
	if err := s.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatal("logout retry did not retry dependent cleanup")
	}
}

func TestStaleAccountInvalidationDoesNotClearCurrentCredentials(t *testing.T) {
	s := testService(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	s.current = &StoredAuth{DeviceToken: "new"}
	cleared := false
	s.SetOnClear(func() error { cleared = true; return nil })
	s.InvalidateDeviceToken("old", s.ServerBaseURL())
	if cleared || s.CurrentDeviceToken() != "new" {
		t.Fatal("stale 401 cleared the new account")
	}
}
