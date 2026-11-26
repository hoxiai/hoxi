package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckEditOmissionPlaceholders(t *testing.T) {
	for _, bad := range []string{
		"// ... rest of code ...",
		"// rest of methods ...",
		"  # ... unchanged code ...  ",
		"/* ... existing implementation ... */",
		"<!-- rest of file ... -->",
		"-- ... remaining code ...",
		"(rest of methods ...)",
		"... rest of implementation ...",
	} {
		if err := checkEditOmissionPlaceholders("main.go", "func a() {}\n", "func a() {}\n"+bad+"\n"); err == nil {
			t.Errorf("expected omission placeholder %q to be rejected", bad)
		}
	}

	// Normal code, string literals, prose comments, and markdown files must never false-positive.
	for _, tc := range []struct {
		file    string
		oldText string
		newText string
	}{
		{"main.go", "", `const sample = "// ... rest of code ..."` + "\n"},
		{"main.go", "", "// This function parses the rest of the payload ... then returns\n"},
		{"main.go", "", "func f(args ...string) {}\n"},
		{"README.md", "", "```go\n// ... rest of code ...\n```\n"},
		{"NOTES.txt", "", "# unchanged code ...\n"},
		// Pre-existing placeholder in the file stays allowed when not introducing another.
		{"main.go", "// ... rest of code ...\nvar a = 1\n", "// ... rest of code ...\nvar a = 2\n"},
	} {
		if err := checkEditOmissionPlaceholders(tc.file, tc.oldText, tc.newText); err != nil {
			t.Errorf("unexpected rejection for %s (%q): %v", tc.file, tc.newText, err)
		}
	}
}

func TestEditWriteMultiEditAndPatchRejectOmissionPlaceholders(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.go")
	initial := "package app\n\nfunc A() int { return 1 }\nfunc B() int { return 2 }\n"
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	revs := NewFileRevisionTracker()
	revs.RecordBytes(path, []byte(initial))
	tc := ToolContext{WorktreeDir: dir, FileRevisions: revs}

	// 1. edit rejects newly introduced omission placeholder and leaves file untouched.
	editInput, _ := json.Marshal(map[string]any{
		"filePath":  "app.go",
		"oldString": "func B() int { return 2 }",
		"newString": "// ... rest of methods ...",
	})
	if out, err := (&editTool{}).Execute(context.Background(), tc, editInput); err == nil || !out.IsError || !strings.Contains(err.Error(), "omission placeholder") {
		t.Fatalf("expected edit omission error, got out=%+v err=%v", out, err)
	}

	// 2. write rejects omission placeholder when overwriting existing file.
	writeInput, _ := json.Marshal(map[string]any{
		"filePath": "app.go",
		"content":  "package app\n// ... existing implementation ...\n",
	})
	if out, err := (&writeTool{}).Execute(context.Background(), tc, writeInput); err == nil || !out.IsError {
		t.Fatalf("expected write omission error, got out=%+v err=%v", out, err)
	}

	// 3. multiedit rejects atomically with zero writes.
	multiInput, _ := json.Marshal(map[string]any{
		"filePath": "app.go",
		"edits": []map[string]any{
			{"oldString": "return 1", "newString": "return 10"},
			{"oldString": "func B() int { return 2 }", "newString": "/* ... unchanged code ... */"},
		},
	})
	if out, err := (&multiEditTool{}).Execute(context.Background(), tc, multiInput); err == nil || !out.IsError {
		t.Fatalf("expected multiedit omission error, got out=%+v err=%v", out, err)
	}

	// 4. apply_patch rejects during staging with zero writes.
	patchInput, _ := json.Marshal(map[string]any{
		"patch": "*** Begin Patch\n*** Update File: app.go\n@@ func B\n-func B() int { return 2 }\n+// ... rest of file ...\n*** End Patch\n",
	})
	if out, err := (&applyPatchTool{}).Execute(context.Background(), tc, patchInput); err == nil || !out.IsError {
		t.Fatalf("expected apply_patch omission error, got out=%+v err=%v", out, err)
	}

	// Verify file on disk is still completely intact.
	after, err := os.ReadFile(path)
	if err != nil || string(after) != initial {
		t.Fatalf("file was unexpectedly modified: %q", string(after))
	}
}
