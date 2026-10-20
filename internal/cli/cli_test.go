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
	mux.HandleFunc("/api/hoxi/runner/sessions/ses_test_1/event", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		events := []runner.SSEEvent{
			{Type: "session.status", Properties: map[string]any{"sessionID": "ses_test_1", "status": map[string]any{"type": "busy"}}},
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
