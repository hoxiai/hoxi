package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// fakeTurnEngine is a minimal OpenCode engine: POST /message streams one
// assistant reply over /event, then returns (or blocks until released).
func fakeTurnEngine(t *testing.T, reply string, hold <-chan struct{}, aborts *atomic.Int32) *httptest.Server {
	t.Helper()
	events := make(chan SSEEvent, 16)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/session":
			writeJSON(w, 200, SessionInfo{ID: "ses_engine"})
		case r.URL.Path == "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			for {
				select {
				case <-r.Context().Done():
					return
				case evt := <-events:
					raw, _ := json.Marshal(evt)
					fmt.Fprintf(w, "data: %s\n\n", raw)
					w.(http.Flusher).Flush()
				}
			}
		case r.URL.Path == "/session/ses_engine/abort":
			aborts.Add(1)
			writeJSON(w, 200, true)
		case r.URL.Path == "/session/ses_engine/message" && r.Method == http.MethodPost:
			now := time.Now().UnixMilli()
			msg := MessageInfo{ID: "msg_reply", SessionID: "ses_engine", Role: "assistant", Time: MessageTime{Created: now, Completed: now}, Finish: "stop"}
			part := MessagePart{ID: "prt_reply", MessageID: msg.ID, SessionID: "ses_engine", Type: "text", Text: reply}
			events <- SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": msg}}
			events <- SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": part}}
			if hold != nil {
				select {
				case <-hold:
				case <-r.Context().Done():
					return
				}
			}
			time.Sleep(50 * time.Millisecond) // let the stream deliver before the turn ends
			writeJSON(w, 200, map[string]interface{}{"ok": true})
		case r.Method == http.MethodGet:
			writeJSON(w, 200, []MessageTurn{})
		default:
			writeJSON(w, 200, map[string]interface{}{})
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func newRunTestService(t *testing.T, engineURL string) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	svc := NewService(root, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	svc.ConfigureOpenCode(engineURL, "secret")
	info, status, err := svc.CreateConversation(context.Background(), CreateSessionRequest{
		Title: "Convoy · test", Directory: root, Runtime: "opencode", ProviderID: "provider", ModelID: "model",
	}, "")
	if err != nil {
		t.Fatalf("CreateConversation: %d %v", status, err)
	}
	return svc, info.ID
}

func TestRunPromptAndWaitReturnsAssistantText(t *testing.T) {
	t.Parallel()
	var aborts atomic.Int32
	engine := fakeTurnEngine(t, "convoy task done", nil, &aborts)
	svc, id := newRunTestService(t, engine.URL)
	if _, ok := svc.Store().Get(id); !ok {
		t.Fatal("conversation was not stored on the session plane")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	text, err := svc.RunPromptAndWait(ctx, id, PromptRequest{Text: "do the task", Agent: "build"})
	if err != nil {
		t.Fatalf("RunPromptAndWait: %v", err)
	}
	if text != "convoy task done" {
		t.Fatalf("assistant text = %q", text)
	}
	svc.mu.Lock()
	busy := svc.procs[id] != nil
	svc.mu.Unlock()
	if busy {
		t.Fatal("turn still registered as running after RunPromptAndWait returned")
	}
}

func TestRunPromptAndWaitAbortsOnCancel(t *testing.T) {
	t.Parallel()
	var aborts atomic.Int32
	hold := make(chan struct{})
	defer close(hold)
	engine := fakeTurnEngine(t, "partial", hold, &aborts)
	svc, id := newRunTestService(t, engine.URL)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := svc.RunPromptAndWait(ctx, id, PromptRequest{Text: "long task"})
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		svc.mu.Lock()
		running := svc.procs[id] != nil
		svc.mu.Unlock()
		if running {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("RunPromptAndWait did not return after cancel")
	}
	svc.mu.Lock()
	busy := svc.procs[id] != nil
	svc.mu.Unlock()
	if busy {
		t.Fatal("cancelled turn is still running")
	}
}
