package intake

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExtractToolFileChangesAcrossToolShapes(t *testing.T) {
	// 1. Multi-file metadata["files"] (apply_patch / native patch)
	files, adds, dels := ExtractToolFileChanges("apply_patch", nil, map[string]interface{}{
		"files": []interface{}{
			map[string]interface{}{
				"filePath":     "/Users/alice/secret-repo/web/src/LoginModal.tsx",
				"relativePath": "web/src/LoginModal.tsx",
				"additions":    json.Number("12"),
				"deletions":    json.Number("4"),
			},
			map[string]interface{}{
				"filePath": "/Users/alice/secret-repo/internal/auth/service.go",
				"before":   "a\nb\nc\n",
				"after":    "a\nb2\nb3\nc\n",
			},
		},
	})
	if adds != 14 || dels != 5 {
		t.Fatalf("multi-file metadata counts = (+%d -%d), want (+14 -5)", adds, dels)
	}
	if len(files) != 2 || files[0].File != "LoginModal.tsx" || files[0].Additions != 12 || files[0].Deletions != 4 {
		t.Fatalf("unexpected first file change: %+v", files)
	}
	if files[1].File != "service.go" || files[1].Additions != 2 || files[1].Deletions != 1 {
		t.Fatalf("unexpected second file change: %+v", files)
	}
	encoded, _ := json.Marshal(files)
	if strings.Contains(string(encoded), "alice") || strings.Contains(string(encoded), "secret-repo") {
		t.Fatalf("file changes leaked local path: %s", string(encoded))
	}

	// 2. Single-file metadata["filediff"]
	files, adds, dels = ExtractToolFileChanges("edit", map[string]interface{}{
		"filePath": "/Users/alice/proj/src/App.tsx",
	}, map[string]interface{}{
		"filediff": map[string]interface{}{
			"file":      "src/App.tsx",
			"additions": 7,
			"deletions": 2,
		},
	})
	if adds != 7 || dels != 2 || len(files) != 1 || files[0].File != "App.tsx" {
		t.Fatalf("filediff counts = %+v (+%d -%d)", files, adds, dels)
	}

	// 3. apply_patch patchText fallback without metadata
	patchText := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: /Users/alice/repo/src/Header.tsx",
		"@@",
		" const a = 1;",
		"-const oldLine = true;",
		"+const newLine = false;",
		"+const extra = 2;",
		"*** Add File: /Users/alice/repo/src/Badge.tsx",
		"+export function Badge() {}",
		"*** End Patch",
	}, "\n")
	files, adds, dels = ExtractToolFileChanges("apply_patch", map[string]interface{}{
		"patchText": patchText,
	}, nil)
	if adds != 3 || dels != 1 || len(files) != 2 {
		t.Fatalf("patchText counts = %+v (+%d -%d), want 2 files (+3 -1)", files, adds, dels)
	}
	if files[0].File != "Header.tsx" || files[0].Additions != 2 || files[0].Deletions != 1 ||
		files[1].File != "Badge.tsx" || files[1].Additions != 1 || files[1].Deletions != 0 {
		t.Fatalf("unexpected patchText file breakdown: %+v", files)
	}

	// 4. Multi-chunk replacement input fallback
	files, adds, dels = ExtractToolFileChanges("multi_replace_file_content", map[string]interface{}{
		"TargetFile": "/Users/alice/repo/src/List.tsx",
		"ReplacementChunks": []interface{}{
			map[string]interface{}{"TargetContent": "a\nb", "ReplacementContent": "a\nb1\nb2"},
			map[string]interface{}{"TargetContent": "x\ny", "ReplacementContent": "z"},
		},
	}, nil)
	if adds != 3 || dels != 3 || len(files) != 1 || files[0].File != "List.tsx" {
		t.Fatalf("chunks counts = %+v (+%d -%d), want List.tsx (+3 -3)", files, adds, dels)
	}

	// 5. Non-edit tools must always return zero diff stats
	files, adds, dels = ExtractToolFileChanges("read_file", map[string]interface{}{
		"filePath": "/Users/alice/repo/src/App.tsx",
	}, map[string]interface{}{
		"filediff": map[string]interface{}{"additions": 99, "deletions": 88},
	})
	if len(files) != 0 || adds != 0 || dels != 0 {
		t.Fatalf("read tool must not produce diff stats, got %+v (+%d -%d)", files, adds, dels)
	}

	// 6. Bare home path must never leak host username as basename
	if base := safeBasename("/Users/alice"); base != "" {
		t.Fatalf("safeBasename leaked home username: %q", base)
	}
}

func TestParsePatchFileChangesEdgeCases(t *testing.T) {
	// 1. Multi-file unified diff without `diff --git` where file 2 is deleted and file 3 is added.
	multiUnified := strings.Join([]string{
		"--- a/src/first.go",
		"+++ b/src/first.go",
		"@@ -1 +1 @@",
		"-old1",
		"+new1",
		"--- a/src/removed.go",
		"+++ /dev/null",
		"@@ -1,2 +0,0 @@",
		"-gone1",
		"-gone2",
		"--- /dev/null",
		"+++ b/src/added.go",
		"@@ -0,0 +1,2 @@",
		"+add1",
		"+add2",
	}, "\n")
	files, adds, dels := parsePatchFileChanges(multiUnified)
	if adds != 3 || dels != 3 || len(files) != 3 {
		t.Fatalf("multi unified = %+v (+%d -%d), want 3 files (+3 -3)", files, adds, dels)
	}
	if files[0] != (SanitizedFileChange{File: "first.go", Additions: 1, Deletions: 1}) ||
		files[1] != (SanitizedFileChange{File: "removed.go", Additions: 0, Deletions: 2}) ||
		files[2] != (SanitizedFileChange{File: "added.go", Additions: 2, Deletions: 0}) {
		t.Fatalf("unexpected multi unified breakdown: %+v", files)
	}

	// 2. diff --git with spaces and quoted paths (including deleted file).
	spacedDiff := strings.Join([]string{
		`diff --git a/my dir/old file.css b/my dir/old file.css`,
		`deleted file mode 100644`,
		`--- a/my dir/old file.css`,
		`+++ /dev/null`,
		`@@ -1,2 +0,0 @@`,
		`---bg-panel: #fff;`,
		`--- sql comment`,
		`diff --git "a/space dir/count file.js" "b/space dir/count file.js"`,
		`--- "a/space dir/count file.js"`,
		`+++ "b/space dir/count file.js"`,
		`@@ -1,2 +1,2 @@`,
		`--- old sql`,
		`+++ new sql`,
		`---bg-old: 0;`,
		`+++i;`,
	}, "\n")
	files, adds, dels = parsePatchFileChanges(spacedDiff)
	if adds != 2 || dels != 4 || len(files) != 2 {
		t.Fatalf("spaced/triple-sign diff = %+v (+%d -%d), want 2 files (+2 -4)", files, adds, dels)
	}
	if files[0] != (SanitizedFileChange{File: "old file.css", Additions: 0, Deletions: 2}) ||
		files[1] != (SanitizedFileChange{File: "count file.js", Additions: 2, Deletions: 2}) {
		t.Fatalf("unexpected spaced/triple-sign breakdown: %+v", files)
	}
}
