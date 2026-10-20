package runner

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSummarizeDoesNotBlockOtherConversation(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/session/engine_a/summarize":
			close(entered)
			<-release
			_, _ = w.Write([]byte("true"))
		case "/session/engine_a/message":
			_, _ = w.Write([]byte("[]"))
		default:
			_, _ = w.Write([]byte("{}"))
		}
	}))
	defer upstream.Close()
	root := t.TempDir()
	svc := NewService(root, nil, nil, filepath.Join(t.TempDir(), "sessions"))
	svc.ConfigureOpenCode(upstream.URL, "test")
	for _, id := range []string{"a", "b"} {
		if err := svc.store.Put(SessionRecord{ID: id, Runtime: "opencode", ExternalID: "engine_" + id,
			ProjectDir: root, WorktreePath: root, SandboxMode: "local", ProviderID: "p", ModelID: "m"}); err != nil {
			t.Fatal(err)
		}
	}
	summarized := make(chan struct{})
	go func() {
		defer close(summarized)
		svc.handleSessionSummarize(httptest.NewRecorder(), httptest.NewRequest("POST", "/summarize", strings.NewReader("{}")), "a")
	}()
	defer func() { close(release); <-summarized }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("summarize did not reach upstream")
	}
	done := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		svc.handleRuntimeSwitch(w, httptest.NewRequest("POST", "/runtime", strings.NewReader(`{"runtime":"opencode","providerID":"p","modelID":"m"}`)), "b")
		done <- w.Code
	}()
	select {
	case code := <-done:
		if code != 200 {
			t.Fatalf("runtime switch returned %d", code)
		}
	case <-time.After(time.Second):
		t.Fatal("an unrelated conversation waited for compaction")
	}
	locked := make(chan struct{})
	go func() { unlock := svc.turnMu.lock("a"); unlock(); close(locked) }()
	select {
	case <-locked:
		t.Fatal("same-session lock was bypassed")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestStreamOverflowResyncsWithoutBlockingOtherSubscribers(t *testing.T) {
	svc := NewService(t.TempDir(), nil, nil, filepath.Join(t.TempDir(), "sessions"))
	slow, stopSlow := svc.Subscribe("")
	defer stopSlow()
	fast, stopFast := svc.Subscribe("")
	defer stopFast()
	for i := 0; i < cap(slow); i++ {
		slow <- SSEEvent{Type: "message.part.delta"}
	}
	done := make(chan struct{})
	go func() { svc.broadcast("a", SSEEvent{Type: "permission.asked"}); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("a slow subscriber stalled broadcast")
	}
	if evt := <-slow; evt.Type != resyncEventType {
		t.Fatalf("missing recovery marker: %s", evt.Type)
	}
	if evt := <-slow; evt.Type != "permission.asked" {
		t.Fatalf("latest critical event lost: %s", evt.Type)
	}
	if evt := <-fast; evt.Type != "permission.asked" {
		t.Fatalf("healthy subscriber lost event: %s", evt.Type)
	}
}

func TestTurnLockRegistryReleasesUnusedEntries(t *testing.T) {
	var locks sessionTurnLocks
	for _, id := range []string{"a", "b", "a"} {
		unlock := locks.lock(id)
		unlock()
	}
	if len(locks.entries) != 0 {
		t.Fatalf("retained %d unused turn locks", len(locks.entries))
	}
}
