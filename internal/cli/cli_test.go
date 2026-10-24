package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hoxiai/hoxi/internal/runner"
)

func TestShouldHandleAndIsServeCommand(t *testing.T) {
	noEnv := func(string) string { return "" }

	// macOS .app bundle launch without CLI subcommands -> Desktop GUI (false)
	if ShouldHandle([]string{"/Applications/Hoxi.app/Contents/MacOS/Hoxi"}, noEnv) {
		t.Fatal("expected .app bundle launch with no args to start desktop GUI, not CLI")
	}
	if ShouldHandle([]string{"/Applications/Hoxi.app/Contents/MacOS/Hoxi", "-psn_0_12345"}, noEnv) {
		t.Fatal("expected macOS LaunchServices -psn_ argument to start desktop GUI")
	}

	// wails dev -> Desktop GUI (false)
	if ShouldHandle([]string{"hoxi"}, func(k string) string {
		if k == "devserver" {
			return "127.0.0.1:34115"
		}
		return ""
	}) {
		t.Fatal("expected wails devserver env to start desktop GUI")
	}

	// Bare `hoxi` in terminal -> CLI (true)
	if !ShouldHandle([]string{"/usr/local/bin/hoxi"}, noEnv) {
		t.Fatal("expected /usr/local/bin/hoxi to enter interactive CLI")
	}
	if !ShouldHandle([]string{"hoxi"}, noEnv) {
		t.Fatal("expected bare hoxi to enter interactive CLI")
	}

	// Positional prompt or subcommands -> CLI (true)
	for _, args := range [][]string{
		{"/Applications/Hoxi.app/Contents/MacOS/Hoxi", "run", "fix bug"},
		{"hoxi", "fix the login validation"},
		{"hoxi", "open"},
		{"hoxi", "handoff"},
		{"hoxi", "worktree", "list"},
		{"hoxi", "sessions"},
		{"hoxi", "-r", "claude"},
	} {
		if !ShouldHandle(args, noEnv) {
			t.Fatalf("expected ShouldHandle(%v) to be true", args)
		}
	}

	// `hoxi serve` rewrites to `--no-desktop`
	rewritten, ok := IsServeCommand([]string{"hoxi", "serve", "--address", "127.0.0.1:5410"})
	if !ok {
		t.Fatal("expected IsServeCommand to match `hoxi serve`")
	}
	if ShouldHandle(rewritten, noEnv) {
		t.Fatalf("expected rewritten serve args %v to run headless server, not CLI", rewritten)
	}
}

func TestNormalizeRuntime(t *testing.T) {
	cases := map[string]string{
		"":           "opencode",
		"oc":         "opencode",
		"opencode":   "opencode",
		"claude":     "claude-code",
		"claudecode": "claude-code",
		"agy":        "antigravity",
		"gemini":     "antigravity",
		"qoder":      "qoder",
		"codebuddy":  "codebuddy",
		"cb":         "codebuddy",
	}
	for in, want := range cases {
		if got := NormalizeRuntime(in); got != want {
			t.Fatalf("NormalizeRuntime(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInstallSymlink(t *testing.T) {
	tmpDir := t.TempDir()
	fakeExe := filepath.Join(tmpDir, "Hoxi")
	if err := os.WriteFile(fakeExe, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(tmpDir, "bin")
	linkPath, err := InstallSymlink(fakeExe, binDir)
	if err != nil {
		t.Fatalf("InstallSymlink failed: %v", err)
	}
	if linkPath != filepath.Join(binDir, "hoxi") {
		t.Fatalf("unexpected linkPath: %s", linkPath)
	}
	target, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("Readlink failed: %v", err)
	}
	if !strings.HasSuffix(target, "Hoxi") {
		t.Fatalf("unexpected symlink target: %s", target)
	}
}

func TestCLISingleTurnAndREPLWithGateway(t *testing.T) {
	var (
		focusedDesktop atomic.Bool
		repliedPerm    atomic.Value
		activeRuntime  atomic.Value
	)
	activeRuntime.Store("opencode")

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/hoxi/sessions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode([]runner.SessionInfo{
				{ID: "ses_test_1", Runtime: activeRuntime.Load().(string), SandboxMode: "local", Directory: "/tmp/proj", Title: "Test Session"},
			})
			return
		}
		var body struct {
			Runtime string `json:"runtime"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Runtime != "" {
			activeRuntime.Store(body.Runtime)
		}
		_ = json.NewEncoder(w).Encode(runner.SessionInfo{
			ID:          "ses_test_1",
			Runtime:     activeRuntime.Load().(string),
			SandboxMode: "local",
			Directory:   "/tmp/proj",
			Title:       "Test Session",
		})
	})
	mux.HandleFunc("/api/hoxi/sessions/ses_test_1/runtime", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Runtime string `json:"runtime"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		activeRuntime.Store(body.Runtime)
		_ = json.NewEncoder(w).Encode(runner.SessionInfo{
			ID:          "ses_test_1",
			Runtime:     body.Runtime,
			SandboxMode: "local",
			Directory:   "/tmp/proj",
			Title:       "Test Session",
		})
	})
	mux.HandleFunc("/api/hoxi/sessions/ses_test_1/prompt", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/hoxi/permission/per_rnr_123/reply", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Reply string `json:"reply"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		repliedPerm.Store(body.Reply)
		_ = json.NewEncoder(w).Encode(true)
	})
	mux.HandleFunc("/api/hoxi/desktop/focus", func(w http.ResponseWriter, _ *http.Request) {
		focusedDesktop.Store(true)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/hoxi/event", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("directory"); got != "/tmp/proj" {
			http.Error(w, "missing directory query", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		events := []runner.SSEEvent{
			{Type: "server.heartbeat", Properties: map[string]any{}},
			{Type: "session.status", Properties: map[string]any{"sessionID": "ses_other", "status": map[string]any{"type": "idle"}}},
			{Type: "session.status", Properties: map[string]any{"sessionID": "ses_test_1", "status": map[string]any{"type": "busy"}}},
			{Type: "message.part.delta", Properties: map[string]any{"sessionID": "ses_other", "messageID": "msg_other", "partID": "prt_other", "field": "text", "delta": "WRONG SESSION"}},
			{Type: "message.part.delta", Properties: map[string]any{"sessionID": "ses_test_1", "messageID": "msg_a_1", "partID": "prt_r_1", "field": "text", "delta": "Analyzing codebase..."}},
			{Type: "message.part.updated", Properties: map[string]any{"part": runner.MessagePart{
				ID: "prt_t_1", SessionID: "ses_test_1", MessageID: "msg_a_1", Type: "tool", Tool: "bash",
				State: &runner.ToolState{Status: "completed", Title: "go test ./...", Time: &runner.PartTime{Start: 100, End: 250}},
			}}},
			{Type: "permission.asked", Properties: map[string]any{
				"id":         "per_rnr_123",
				"sessionID":  "ses_test_1",
				"permission": "bash",
				"metadata":   map[string]any{"command": "rm -rf dist"},
			}},
			{Type: "message.part.delta", Properties: map[string]any{"sessionID": "ses_test_1", "messageID": "msg_a_1", "partID": "prt_txt_1", "field": "text", "delta": "All tests passed!"}},
			{Type: "session.status", Properties: map[string]any{"sessionID": "ses_test_1", "status": map[string]any{"type": "idle"}}},
		}
		for _, evt := range events {
			raw, _ := json.Marshal(evt)
			fmt.Fprintf(w, "data: %s\n\n", string(raw))
			if flusher != nil {
				flusher.Flush()
			}
		}
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	// 1. Test one-shot `hoxi run` with permission approval
	var outBuf, errBuf bytes.Buffer
	code := Run(
		context.Background(),
		[]string{"hoxi", "run", "--address", addr, "run tests and clean dist"},
		strings.NewReader("y\n"),
		&outBuf,
		&errBuf,
		addr,
		nil,
	)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr: %s)", code, errBuf.String())
	}
	output := outBuf.String()
	for _, want := range []string{"Analyzing codebase...", "go test ./...", "Approval Required", "All tests passed!"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, output)
		}
	}
	if got, _ := repliedPerm.Load().(string); got != "once" {
		t.Fatalf("expected permission reply 'once', got %q", got)
	}

	// 2. Test interactive REPL slash commands (/runtime claude, /sessions, /open, /exit)
	outBuf.Reset()
	errBuf.Reset()
	replInput := strings.Join([]string{
		"/runtime claude",
		"hello agent",
		"y",
		"/sessions",
		"/open",
		"/exit",
	}, "\n") + "\n"
	code = Run(
		context.Background(),
		[]string{"hoxi", "--address", addr},
		strings.NewReader(replInput),
		&outBuf,
		&errBuf,
		addr,
		nil,
	)
	if code != 0 {
		t.Fatalf("expected REPL exit code 0, got %d (stderr: %s)", code, errBuf.String())
	}
	if got := activeRuntime.Load().(string); got != "claude-code" {
		t.Fatalf("expected runtime to switch to claude-code, got %q", got)
	}
	if !focusedDesktop.Load() {
		t.Fatal("expected /open to trigger /api/hoxi/desktop/focus")
	}
}

func TestStreamTurnUnifiedSSEFilterHeartbeatAndResync(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/hoxi/sessions/ses_target/prompt", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("directory"); got != "/tmp/wt" {
			http.Error(w, "missing directory", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/hoxi/sessions/ses_target/message", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("directory"); got != "/tmp/wt" {
			http.Error(w, "missing directory", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode([]runner.MessageTurn{
			{
				Info: runner.MessageInfo{ID: "msg_old_u", SessionID: "ses_target", Role: "user"},
				Parts: []runner.MessagePart{
					{ID: "prt_old_u", SessionID: "ses_target", MessageID: "msg_old_u", Type: "text", Text: "prior prompt"},
				},
			},
			{
				Info: runner.MessageInfo{ID: "msg_old_a", SessionID: "ses_target", Role: "assistant"},
				Parts: []runner.MessagePart{
					{ID: "prt_old_a", SessionID: "ses_target", MessageID: "msg_old_a", Type: "text", Text: "prior answer"},
				},
			},
			{
				Info: runner.MessageInfo{ID: "msg_u_2", SessionID: "ses_target", Role: "user"},
				Parts: []runner.MessagePart{
					{ID: "prt_u_2", SessionID: "ses_target", MessageID: "msg_u_2", Type: "text", Text: "current prompt"},
				},
			},
			{
				Info: runner.MessageInfo{ID: "msg_a_2", SessionID: "ses_target", Role: "assistant"},
				Parts: []runner.MessagePart{
					{ID: "prt_txt_2", SessionID: "ses_target", MessageID: "msg_a_2", Type: "text", Text: "Hello recovered world!"},
				},
			},
		})
	})
	mux.HandleFunc("/api/hoxi/permission", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": "per_other", "sessionID": "ses_other", "permission": "bash"},
		})
	})
	mux.HandleFunc("/api/hoxi/sessions/status", func(w http.ResponseWriter, _ *http.Request) {
		// Turn already settled to idle during the gap before resync.
		_ = json.NewEncoder(w).Encode(map[string]runner.SessionStatus{})
	})
	mux.HandleFunc("/api/hoxi/event", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("directory"); got != "/tmp/wt" {
			http.Error(w, "missing directory", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		frames := []any{
			runner.SSEEvent{Type: "server.heartbeat", Properties: map[string]any{}},
			// Other session events across different property shapes must be ignored.
			runner.SSEEvent{Type: "message.updated", Properties: map[string]any{
				"info": runner.MessageInfo{ID: "msg_other", SessionID: "ses_other", Role: "assistant"},
			}},
			runner.SSEEvent{Type: "message.part.updated", Properties: map[string]any{
				"part": runner.MessagePart{ID: "prt_other", SessionID: "ses_other", MessageID: "msg_other", Type: "text", Text: "noise"},
			}},
			runner.SSEEvent{Type: "session.updated", Properties: map[string]any{
				"info": runner.SessionInfo{ID: "ses_other", Title: "Other"},
			}},
			runner.SSEEvent{Type: "session.error", Properties: map[string]any{
				"sessionID": "ses_other",
				"error":     map[string]any{"name": "OtherSessionFailed"},
			}},
			// Wrapped /global/event frame for target session.
			map[string]any{
				"directory": "/tmp/wt",
				"payload": runner.SSEEvent{
					Type:       "message.part.delta",
					Properties: map[string]any{"sessionID": "ses_target", "messageID": "msg_a_2", "partID": "prt_txt_2", "field": "text", "delta": "Hello "},
				},
			},
			// Stream resync recovers "recovered world!" from /message and sees idle from /sessions/status.
			runner.SSEEvent{Type: "hoxi.stream.resync", Properties: map[string]any{}},
		}
		for _, frame := range frames {
			raw, _ := json.Marshal(frame)
			fmt.Fprintf(w, "data: %s\n\n", string(raw))
			if flusher != nil {
				flusher.Flush()
			}
		}
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := NewClient(srv.URL, true)
	session := &runner.SessionInfo{
		ID:        "ses_target",
		Runtime:   "opencode",
		Directory: "/tmp/wt",
	}
	var outBuf, errBuf bytes.Buffer
	renderer := NewTurnRenderer(nil, &outBuf, &errBuf, client, session.Directory)
	var seenTypes []string
	err := client.StreamTurn(context.Background(), session, "current prompt", "build", "m1", "p1", func(evt runner.SSEEvent) {
		seenTypes = append(seenTypes, evt.Type)
		if sid := sseEventSessionID(evt); sid != "ses_target" {
			t.Errorf("unexpected event sessionID %q in %+v", sid, evt)
		}
		renderer.HandleEvent(context.Background(), evt)
	})
	renderer.Finish()
	if err != nil {
		t.Fatalf("StreamTurn failed: %v", err)
	}
	for _, typ := range seenTypes {
		if typ == "server.heartbeat" || typ == "hoxi.stream.resync" {
			t.Fatalf("control event %q should not be forwarded to onEvent", typ)
		}
	}
	gotOut := strings.TrimSpace(outBuf.String())
	if gotOut != "Hello recovered world!" {
		t.Fatalf("rendered output = %q, want %q", gotOut, "Hello recovered world!")
	}
}

func TestCLICodeIndexSubcommand(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc RunServer() {\n\tprintln(\"ok\")\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".hoxi"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".hoxi", "cloudflared.json"), []byte(`{"token":"secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var outBuf, errBuf bytes.Buffer
	code := runCodeIndexCommand([]string{"search", "--dir", dir, "RunServer"}, dir, &outBuf, &errBuf)
	if code != 0 || !strings.Contains(outBuf.String(), "RunServer") {
		t.Fatalf("code-index search failed (code=%d): out=%s err=%s", code, outBuf.String(), errBuf.String())
	}

	outBuf.Reset()
	errBuf.Reset()
	code = runCodeIndexCommand([]string{"inspect", "--dir", dir, "--path", "main.go", "--symbol", "RunServer"}, dir, &outBuf, &errBuf)
	if code != 0 || !strings.Contains(outBuf.String(), "func RunServer()") {
		t.Fatalf("code-index inspect failed (code=%d): out=%s err=%s", code, outBuf.String(), errBuf.String())
	}

	outBuf.Reset()
	errBuf.Reset()
	code = runCodeIndexCommand([]string{"inspect", "--dir", dir, "--path", ".hoxi/cloudflared.json"}, dir, &outBuf, &errBuf)
	if code == 0 {
		t.Fatalf("expected code-index inspect on .hoxi/cloudflared.json to fail, got out=%s", outBuf.String())
	}
}
