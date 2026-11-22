package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGlobDoublestarBracesAndGitignore(t *testing.T) {
	if !matchGlob("internal/**/*.ts", "internal/index.ts") {
		t.Fatal("expected internal/**/*.ts to match zero-depth internal/index.ts")
	}
	if !matchGlob("internal/**/*.ts", "internal/a/b/c/deep.ts") {
		t.Fatal("expected internal/**/*.ts to match multi-depth nested file")
	}
	if !matchGlob("**/*.{ts,tsx}", "web/src/App.tsx") || !matchGlob("**/*.{ts,tsx}", "src/util.ts") {
		t.Fatal("expected brace + doublestar pattern to match both .ts and .tsx")
	}
	if matchGlob("internal/**/*.ts", "other/a/b.ts") {
		t.Fatal("expected internal/**/*.ts not to match outside prefix")
	}

	ws := t.TempDir()
	outside := t.TempDir()
	mustWrite := func(rel, body string) {
		full := filepath.Join(ws, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mustWrite(".gitignore", "dist/\n*.log\n!keep.log\nsecret_dir/\n")
	mustWrite("src/nested/.gitignore", "*.gen.ts\n")
	mustWrite("src/app.ts", "export const needle = 1;\n")
	mustWrite("src/nested/view.tsx", "export const needle = 2;\n")
	mustWrite("src/nested/api.gen.ts", "export const needle = 3;\n")
	mustWrite("dist/bundle.ts", "export const needle = 4;\n")
	mustWrite("debug.log", "needle ignored log\n")
	mustWrite("keep.log", "needle negated keep\n")
	mustWrite("secret_dir/inside.ts", "export const needle = 5;\n")
	mustWrite("secret_dir/keep.log", "needle inside excluded dir must stay skipped\n")
	mustWrite(".next/cache.ts", "export const needle = 6;\n")

	if runtime.GOOS != "windows" {
		outsideSecret := filepath.Join(outside, "escape.ts")
		_ = os.WriteFile(outsideSecret, []byte("export const needle = 99;\n"), 0o644)
		_ = os.Symlink(outsideSecret, filepath.Join(ws, "src", "escape_link.ts"))
	}

	reg := newToolRegistry()

	// 1. Glob with doublestar + braces honors .gitignore, nested .gitignore, and skips .next
	globRes, _ := reg.Execute(context.Background(), ToolContext{WorktreeDir: ws}, ToolCall{ID: "c1", Name: "glob", Input: json.RawMessage(`{"pattern":"**/*.{ts,tsx,log}"}`)}, nil)
	if globRes.IsError {
		t.Fatalf("glob failed: %s", globRes.Output)
	}
	for _, want := range []string{"src/app.ts", "src/nested/view.tsx", "keep.log"} {
		if !strings.Contains(globRes.Output, want) {
			t.Fatalf("expected glob output to contain %q, got:\n%s", want, globRes.Output)
		}
	}
	for _, forbidden := range []string{"dist/bundle.ts", "src/nested/api.gen.ts", "debug.log", "secret_dir/inside.ts", "secret_dir/keep.log", ".next/cache.ts", "escape_link.ts"} {
		if strings.Contains(globRes.Output, forbidden) {
			t.Fatalf("expected glob output to exclude %q, got:\n%s", forbidden, globRes.Output)
		}
	}

	// 2. Explicit path into an ignored directory still searches inside that directory
	explicitRes, _ := reg.Execute(context.Background(), ToolContext{WorktreeDir: ws}, ToolCall{ID: "c2", Name: "glob", Input: json.RawMessage(`{"path":"secret_dir","pattern":"*.ts"}`)}, nil)
	if explicitRes.IsError || !strings.Contains(explicitRes.Output, "inside.ts") {
		t.Fatalf("expected explicit path into ignored dir to return inside.ts, got: %s", explicitRes.Output)
	}

	// 3. Grep respects .gitignore + !keep.log negation + brace include filter
	grepRes, _ := reg.Execute(context.Background(), ToolContext{WorktreeDir: ws}, ToolCall{ID: "c3", Name: "grep", Input: json.RawMessage(`{"pattern":"needle","include":"**/*.{ts,tsx,log}"}`)}, nil)
	if grepRes.IsError {
		t.Fatalf("grep failed: %s", grepRes.Output)
	}
	if !strings.Contains(grepRes.Output, "keep.log") || !strings.Contains(grepRes.Output, "src/nested/view.tsx") {
		t.Fatalf("expected grep to find negated keep.log and view.tsx, got:\n%s", grepRes.Output)
	}
	if strings.Contains(grepRes.Output, "debug.log") || strings.Contains(grepRes.Output, "api.gen.ts") || strings.Contains(grepRes.Output, "dist/bundle.ts") {
		t.Fatalf("expected grep to exclude gitignored files, got:\n%s", grepRes.Output)
	}
}

func TestBashOutputCompactionAndStreaming(t *testing.T) {
	noisy := "\x1b[32mDownloading 10%\x1b[0m\r\x1b[32mDownloading 50%\x1b[0m\rDownloading 100%\n\x1b]0;WindowTitle\x07Build ready\n"
	cleaned := stripTerminalControlNoise(noisy)
	if strings.Contains(cleaned, "10%") || strings.Contains(cleaned, "\x1b") || !strings.Contains(cleaned, "Downloading 100%\nBuild ready") {
		t.Fatalf("unexpected cleaned terminal output: %q", cleaned)
	}

	// Passing verbose go test log (> 4 KiB) should condense cleanly
	var passLines []string
	for i := 0; i < 120; i++ {
		passLines = append(passLines, fmt.Sprintf("ok  \tgithub.com/acme/pkg%03d\t0.012s", i))
	}
	condensed := compactBashToolOutput(json.RawMessage(`{"command":"go test ./..."}`), strings.Join(passLines, "\n"), false, maxToolOutputBytes)
	if !strings.Contains(condensed, "lines of passing check output omitted") {
		t.Fatalf("expected passing check output to be condensed, got len=%d:\n%s", len(condensed), condensed)
	}

	// Large (> 32 KiB) output with a compiler error in the middle preserves failure excerpt
	var midLines []string
	for i := 0; i < 450; i++ {
		midLines = append(midLines, fmt.Sprintf("info: compiling module_%04d with standard flags...", i))
	}
	midLines = append(midLines,
		"internal/core/handler.go:88:14: undefined: missingCriticalHelper",
		"FAIL\tgithub.com/acme/internal/core [build failed]",
	)
	for i := 0; i < 450; i++ {
		midLines = append(midLines, fmt.Sprintf("info: post-step cleanup artifact_%04d completed...", i))
	}
	bigOut := strings.Join(midLines, "\n") + "\n\n[exit code: 2]"
	bounded := compactBashToolOutput(json.RawMessage(`{"command":"go test ./..."}`), bigOut, true, maxToolOutputBytes)
	if !strings.Contains(bounded, "missingCriticalHelper") || !strings.Contains(bounded, "[failure excerpt from middle of log]") {
		t.Fatalf("expected middle compiler error to survive compaction + bounding, got:\n%s", bounded)
	}

	// Streaming bash runner forwards cleaned progress via OnToolOutput
	ws := t.TempDir()
	var streamed []string
	reg := newToolRegistry()
	res, _ := reg.Execute(context.Background(), ToolContext{
		WorktreeDir: ws,
		StreamRunCommand: func(_ context.Context, _, _ string, capture io.Writer, onOutput func(string)) (string, int, error) {
			onOutput("step 1\r\x1b[33mstep 2\x1b[0m\n")
			return "done\n", 0, nil
		},
		OnToolOutput: func(live string) {
			streamed = append(streamed, live)
		},
	}, ToolCall{ID: "cb", Name: "bash", Input: json.RawMessage(`{"command":"echo done"}`)}, nil)
	if res.IsError || len(streamed) != 1 || streamed[0] != "step 2\n" {
		t.Fatalf("expected cleaned live streaming callback 'step 2\\n', got err=%v streamed=%+v", res.IsError, streamed)
	}
}

func TestSessionTodoSeedAndDelete(t *testing.T) {
	sid := "ses_todo_lifecycle_test"
	DeleteSessionTodos(sid)
	t.Cleanup(func() { DeleteSessionTodos(sid) })

	SeedSessionTodosIfAbsent(sid, []TodoItem{
		{ID: "1", Content: "Restore after restart", Status: "in_progress"},
	})
	reg := newToolRegistry()
	readRes, _ := reg.Execute(context.Background(), ToolContext{SessionID: sid, WorktreeDir: t.TempDir()}, ToolCall{ID: "t1", Name: "todoread", Input: json.RawMessage(`{}`)}, nil)
	if readRes.IsError || !strings.Contains(readRes.Output, "Restore after restart") {
		t.Fatalf("expected seeded todo from todoread, got: %s", readRes.Output)
	}

	// Seeding again must not overwrite active session state
	_, _ = reg.Execute(context.Background(), ToolContext{SessionID: sid, WorktreeDir: t.TempDir()}, ToolCall{
		ID:    "t2",
		Name:  "todowrite",
		Input: json.RawMessage(`{"todos":[{"id":"1","content":"Updated live","status":"completed"}]}`),
	}, nil)
	SeedSessionTodosIfAbsent(sid, []TodoItem{
		{ID: "1", Content: "Stale disk copy", Status: "pending"},
	})
	readRes2, _ := reg.Execute(context.Background(), ToolContext{SessionID: sid, WorktreeDir: t.TempDir()}, ToolCall{ID: "t3", Name: "todoread", Input: json.RawMessage(`{}`)}, nil)
	if !strings.Contains(readRes2.Output, "Updated live") || strings.Contains(readRes2.Output, "Stale disk copy") {
		t.Fatalf("expected SeedSessionTodosIfAbsent not to clobber active todos, got: %s", readRes2.Output)
	}

	DeleteSessionTodos(sid)
	readRes3, _ := reg.Execute(context.Background(), ToolContext{SessionID: sid, WorktreeDir: t.TempDir()}, ToolCall{ID: "t4", Name: "todoread", Input: json.RawMessage(`{}`)}, nil)
	if strings.TrimSpace(readRes3.Output) != "[]" {
		t.Fatalf("expected DeleteSessionTodos to clear session checklist to [], got: %s", readRes3.Output)
	}
}
