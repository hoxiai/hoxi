package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestEnvelopeAnchorAndEndOfFile(t *testing.T) {
	const source = "class A\nmethod\n  old\nclass B\nmethod\n  old\n"
	for _, tt := range []struct{ name, body, want string }{
		{"nested anchors", "@@ class B\n@@ method\n-old\n+  new", "class A\nmethod\n  old\nclass B\nmethod\n  new\n"},
		{"end of file", "@@\n-  old\n+new\n*** End of File", "class A\nmethod\n  old\nclass B\nmethod\nnew\n"},
		{"duplicate without context", "@@\n-  old\n+new", ""},
		{"missing anchor", "@@ class C\n-  old\n+new", ""},
		{"not at EOF", "@@\n-class A\n+new\n*** End of File", ""},
		{"dangling anchor", "@@ class A", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "a.txt")
			if err := os.WriteFile(path, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(map[string]string{"patch": "*** Begin Patch\n*** Update File: a.txt\n" + tt.body + "\n*** End Patch"})
			_, err := (&applyPatchTool{}).Execute(context.Background(), ToolContext{WorktreeDir: dir}, raw)
			want := tt.want
			if (err != nil) != (want == "") {
				t.Fatalf("unexpected patch result %v", err)
			}
			if want == "" {
				want = source
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != want {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
}

func TestEnvelopeFileDirectoryConflictPreflight(t *testing.T) {
	for _, paths := range [][]string{{"asset", "asset/part"}, {"asset/part", "asset"}} {
		dir := t.TempDir()
		raw, _ := json.Marshal(map[string]string{"patch": "*** Begin Patch\n*** Add File: " + paths[0] + "\n+one\n*** Add File: " + paths[1] + "\n+two\n*** End Patch"})
		if _, err := (&applyPatchTool{}).Execute(context.Background(), ToolContext{WorktreeDir: dir}, raw); err == nil {
			t.Fatal("expected conflicting paths to fail preflight")
		}
		if _, err := os.Stat(filepath.Join(dir, "asset")); !os.IsNotExist(err) {
			t.Fatalf("partial file or directory exists: %v", err)
		}
	}
}

func TestEnvelopeReportsCompletedWritesOnDiskFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX directory permissions")
	}
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0700) })
	if err := os.WriteFile(filepath.Join(locked, "probe"), nil, 0600); err == nil {
		t.Skip("filesystem permits writing to a read-only directory")
	}
	raw, _ := json.Marshal(map[string]string{"patch": "*** Begin Patch\n*** Add File: first\n+yes\n*** Add File: locked/no\n+no\n*** Add File: last\n+no\n*** End Patch"})
	out, err := (&applyPatchTool{}).Execute(context.Background(), ToolContext{WorktreeDir: dir}, raw)
	if err == nil || out.Metadata["completedMutation"] != true || out.Metadata["failedPath"] != "locked/no" || !strings.Contains(out.Output, "Already applied:\nA first") || !strings.Contains(out.Output, "Not attempted: last") {
		t.Fatalf("missing partial result: %+v %v", out, err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "first")); err != nil || string(got) != "yes\n" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "last")); !os.IsNotExist(err) {
		t.Fatalf("unexpected final write: %v", err)
	}
}

func TestPartialPatchInvalidatesReadsAndBoundsRetries(t *testing.T) {
	guard := newStepLoopGuard()
	patch := ToolCall{Name: "apply_patch", Input: json.RawMessage(`{"patch":"same"}`)}
	read := ToolCall{Name: "read", Input: json.RawMessage(`{"filePath":"first"}`)}
	for i := 0; i < identicalFailBlockCount; i++ {
		if blocked, _ := guard.CheckBeforeExecute(patch); blocked {
			t.Fatal("premature block")
		}
		guard.CheckBeforeExecute(read)
		guard.ObserveStepCalls([]ToolCall{patch}, []ToolOutput{{IsError: true, Metadata: map[string]any{"completedMutation": true}}}, func(string) bool { return false })
		if len(guard.readsSinceMutation) != 0 {
			t.Fatal("stale read guard after partial write")
		}
	}
	if blocked, _ := guard.CheckBeforeExecute(patch); !blocked {
		t.Fatal("repeated partial writes reset their own retry limit")
	}
}
