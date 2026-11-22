package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestUnifiedDiffPositionsAndValidation(t *testing.T) {
	for _, tc := range []struct {
		name, source, diff, want string
		reject                   bool
	}{
		{"declared occurrence", "A\nsame\nB\nsame\n", "@@ -4 +4 @@\n-same\n+fixed\n", "A\nsame\nB\nfixed\n", false},
		{"unique offset", "extra\nA\nB\n", "@@ -2 +2 @@\n-B\n+fixed\n", "extra\nA\nfixed\n", false},
		{"ambiguous offset", "same\nB\nsame\n", "@@ -2 +2 @@\n-same\n+fixed\n", "", true},
		{"middle insertion and later hunk", "A\nB\nC\n", "@@ -1,0 +2 @@\n+new\n@@ -3 +4 @@\n-C\n+c\n", "A\nnew\nB\nc\n", false},
		{"beginning insertion", "A\n", "@@ -0,0 +1 @@\n+new\n", "new\nA\n", false},
		{"no final newline", "A\nB", "@@ -2 +2 @@\n-B\n\\ No newline at end of file\n+fixed  \n\\ No newline at end of file\n", "A\nfixed  ", false},
		{"misplaced newline marker", "A\nB\n", "@@ -1 +1 @@\n-A\n+new\n\\ No newline at end of file\n", "", true},
		{"header-like source", "-- title\n", "@@ -1 +1 @@\n--- title\n+++ next\n", "++ next\n", false},
		{"CRLF", "A\r\nB\r\n", "@@ -2 +2 @@\n-B\n+new\n", "A\r\nnew\r\n", false},
		{"bad header", "A\n", "@@ -X +2 @@\n-A\n+new\n", "", true},
		{"mismatched count", "A\n", "@@ -1,2 +1 @@\n-A\n+new\n", "", true},
		{"extra hunk data", "A\n", "@@ -1 +1 @@\n-A\n+new\n+extra\n", "", true},
		{"inconsistent target coordinate", "A\n", "@@ -1 +2 @@\n-A\n+new\n", "", true},
		{"out of range insertion", "A\n", "@@ -2,0 +3 @@\n+new\n", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := t.TempDir()
			path := filepath.Join(ws, "sample.txt")
			if err := os.WriteFile(path, []byte(tc.source), 0600); err != nil {
				t.Fatal(err)
			}
			args, _ := json.Marshal(map[string]string{"patch": "--- a/sample.txt\n+++ b/sample.txt\n" + tc.diff})
			_, err := (&applyPatchTool{}).Execute(context.Background(), ToolContext{WorktreeDir: ws}, args)
			if (err != nil) != tc.reject {
				t.Fatalf("unexpected patch result: %v", err)
			}
			want := tc.want
			if tc.reject {
				want = tc.source
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != want {
				t.Fatalf("contents %q, want %q, err %v", got, want, err)
			}
		})
	}
}

func TestUnifiedDeleteValidatesBeforeApplyingAnyFile(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "old.txt")
	if err := os.WriteFile(path, []byte("keep\nold\n"), 0600); err != nil {
		t.Fatal(err)
	}
	patch := "--- /dev/null\n+++ b/new.txt\n@@ -0,0 +1 @@\n+created\n" +
		"--- a/old.txt\n+++ /dev/null\n@@ -2 +1,0 @@\n-old\n"
	args, _ := json.Marshal(map[string]string{"patch": patch})
	if _, err := (&applyPatchTool{}).Execute(context.Background(), ToolContext{WorktreeDir: ws}, args); err == nil {
		t.Fatal("accepted deletion leaving an unmatched source line")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "keep\nold\n" {
		t.Fatalf("original file changed: %q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(ws, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("earlier operation should not be applied: %v", err)
	}
}
