package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestFileSearchScorePrefersBasename(t *testing.T) {
	cases := []struct {
		rel   string
		name  string
		query string
		want  int
	}{
		{"src/main.ts", "main.ts", "main.ts", 0},
		{"src/main.ts", "main.ts", "main", 1},
		{"src/MainView.tsx", "MainView.tsx", "main", 1},
		{"src/main.ts", "main.ts", "src", 3},
		{"docs/readme.md", "readme.md", "read", 1},
		{"a/b/c.ts", "c.ts", "zzz", -1},
	}
	for _, tc := range cases {
		if got := fileSearchScore(tc.rel, tc.name, tc.query); got != tc.want {
			t.Fatalf("fileSearchScore(%q, %q, %q) = %d, want %d", tc.rel, tc.name, tc.query, got, tc.want)
		}
	}
}

func TestSkipFileSearchDir(t *testing.T) {
	for _, name := range []string{".git", "node_modules", "dist", ".hoxi", "Pods", "venv"} {
		if !skipFileSearchDir(name) {
			t.Fatalf("expected %q to be skipped", name)
		}
	}
	for _, name := range []string{"src", "packages", ".github", "internal"} {
		if skipFileSearchDir(name) {
			t.Fatalf("expected %q to be searched", name)
		}
	}
}

func TestHandleFileSearchReturnsRankedRelativePaths(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src", "components"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"src/components/Button.tsx", "src/button.ts", "README.md"} {
		target := filepath.Join(root, file)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte("hello\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "node_modules", "button"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "node_modules", "button", "index.js"), []byte("skip\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	g := &Gateway{rootDir: root}
	req := httptest.NewRequest(http.MethodGet, "/api/hoxi/file/search?q=button", nil)
	rec := httptest.NewRecorder()
	g.handleFileSearch(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var results []fileSearchResult
	if err := json.Unmarshal(rec.Body.Bytes(), &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %+v", results)
	}
	if results[0].Path != "src/button.ts" || results[1].Path != "src/components/Button.tsx" {
		t.Fatalf("unexpected ranking: %+v", results)
	}
	for _, result := range results {
		if filepath.IsAbs(result.Path) {
			t.Fatalf("expected relative path, got %q", result.Path)
		}
	}
}
