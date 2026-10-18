package auth

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestPollGuardsLateResponses(t *testing.T) {
	for _, action := range []string{"logout", "restart", "server change"} {
		t.Run(action, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			s := testService(t, func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				<-release
				authorizedResponse(w)
			})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			ignoreRequestCancellation(s)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			attempt := &authorization{state: "old", baseURL: s.ServerBaseURL(), expiresAt: time.Now().Add(3 * time.Minute), ctx: ctx, cancel: cancel}
			s.pending = attempt
			done := make(chan struct{})
			go func() {
				s.runPollWorker(attempt)
				close(done)
			}()
			await(t, entered)
			nextState := ""
			switch action {
			case "logout":
				if err := s.Logout(context.Background()); err != nil {
					t.Fatal(err)
				}
			case "restart":
				nextState = startTestAuth(t, s)
			case "server change":
				s.SetServerBaseURL("https://other.example")
			}
			unblock()
			await(t, done)
			assertStoredToken(t, s, "")
			if s.Snapshot().PendingState != nextState {
				t.Fatal("stale poll changed pending authorization")
			}
		})
	}
}

func TestPollRejectsHTTPError(t *testing.T) {
	responded := make(chan struct{})
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		authorizedResponse(w)
		close(responded)
	})
	ctx, cancel := context.WithCancel(context.Background())
	attempt := &authorization{state: "pending", baseURL: s.ServerBaseURL(), expiresAt: time.Now().Add(3 * time.Minute), ctx: ctx, cancel: cancel}
	s.pending = attempt
	done := make(chan struct{})
	go func() {
		s.runPollWorker(attempt)
		close(done)
	}()
	await(t, responded)
	cancel()
	await(t, done)
	assertStoredToken(t, s, "")
}
