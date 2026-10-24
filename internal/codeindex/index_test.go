package codeindex

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fixtureWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc HandleRequest() {\n\tprintln(\"hello\")\n}\n")
	writeFile(t, filepath.Join(dir, "lib", "util.ts"), "export function computeTotal(items: number[]) {\n  return items.reduce((a, b) => a + b, 0)\n}\n")
	writeFile(t, filepath.Join(dir, "README.md"), "# Demo\n\nCall HandleRequest from the entrypoint.\n")
	writeFile(t, filepath.Join(dir, "node_modules", "pkg", "index.js"), "function hiddenDependency() {}\n")
	writeFile(t, filepath.Join(dir, "dist", "bundle.js"), "function builtArtifact() {}\n")
	writeFile(t, filepath.Join(dir, ".git", "config"), "function gitInternal() {}\n")
	writeFile(t, filepath.Join(dir, "logo.png"), "\x00\x01\x02binary")
	return dir
}

func TestSearchFindsSymbolsAndSkipsIgnoredPaths(t *testing.T) {
	dir := fixtureWorkspace(t)
	svc := NewService(t.TempDir())
	response, err := svc.Search(dir, "HandleRequest", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Results) == 0 {
		t.Fatal("expected a result")
	}
	if response.Results[0].Path != "main.go" || response.Results[0].Symbol != "HandleRequest" {
		t.Fatalf("unexpected first result: %#v", response.Results[0])
	}
	for _, result := range response.Results {
		switch result.Path {
		case "node_modules/pkg/index.js", "dist/bundle.js", ".git/config", "logo.png":
			t.Fatalf("ignored file was indexed: %s", result.Path)
		}
	}
	if response.Files != 3 {
		t.Fatalf("expected 3 indexed files, got %d", response.Files)
	}
}

func TestSearchMatchesIdentifiersAcrossCasing(t *testing.T) {
	dir := fixtureWorkspace(t)
	svc := NewService(t.TempDir())
	response, err := svc.Search(dir, "compute total", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Results) == 0 || response.Results[0].Path != "lib/util.ts" {
		t.Fatalf("unexpected results: %#v", response.Results)
	}
}

func TestRefreshPicksUpChangesAndDeletions(t *testing.T) {
	dir := fixtureWorkspace(t)
	svc := NewService(t.TempDir())
	if _, err := svc.Status(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "README.md")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "lib", "new.go"), "package lib\n\nfunc BrandNewSymbol() {}\n")
	status, err := svc.Refresh(dir)
	if err != nil {
		t.Fatal(err)
	}
	if status.Files != 3 {
		t.Fatalf("expected 3 files after refresh, got %d", status.Files)
	}
	response, err := svc.Search(dir, "BrandNewSymbol", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Results) == 0 || response.Results[0].Path != "lib/new.go" {
		t.Fatalf("new symbol missing: %#v", response.Results)
	}
}

func TestSearchRejectsMissingDirectory(t *testing.T) {
	svc := NewService(t.TempDir())
	if _, err := svc.Search(filepath.Join(t.TempDir(), "missing"), "x", 5); err == nil {
		t.Fatal("expected missing directory to fail")
	}
	if _, err := svc.Search("", "x", 5); err == nil {
		t.Fatal("expected empty directory to fail")
	}
}

func TestEmptyQueryReturnsNoResults(t *testing.T) {
	dir := fixtureWorkspace(t)
	svc := NewService(t.TempDir())
	response, err := svc.Search(dir, "   ", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Results) != 0 {
		t.Fatalf("expected no results, got %d", len(response.Results))
	}
}

func TestIndexableFileSkipsCredentials(t *testing.T) {
	for path, want := range map[string]bool{
		"main.go":              true,
		"config/app.yaml":      true,
		".env":                 false,
		".env.production":      false,
		"deploy/prod.env":      false,
		"infra/prod.tfvars":    false,
		"certs/server.pem":     false,
		".npmrc":               false,
		"terraform.tfstate":    false,
		"internal/keys/api.go": true,
	} {
		if got := indexableFile(path); got != want {
			t.Fatalf("indexableFile(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestInspectSymbolAndCallersSeparation(t *testing.T) {
	dir := fixtureWorkspace(t)
	writeFile(t, filepath.Join(dir, "server.go"), "package main\n\nfunc ServeHTTP() {\n\tHandleRequest()\n}\n")
	writeFile(t, filepath.Join(dir, "main_test.go"), "package main\n\nimport \"testing\"\n\nfunc TestHandleRequest(t *testing.T) {\n\tHandleRequest()\n}\n")
	writeFile(t, filepath.Join(dir, ".env"), "SECRET_KEY=super-secret-value\n")

	svc := NewService(t.TempDir())
	inspected, err := svc.Inspect(dir, "", "HandleRequest", 0, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if inspected.Path != "main.go" || inspected.Symbol != "HandleRequest" || !strings.Contains(inspected.Content, "func HandleRequest()") {
		t.Fatalf("unexpected inspect response: %+v", inspected)
	}

	callers, err := svc.Callers(dir, "HandleRequest", "main.go", 10)
	if err != nil {
		t.Fatal(err)
	}
	foundProdCaller := false
	for _, c := range callers.Callers {
		if c.Path == "server.go" && c.Symbol == "ServeHTTP" {
			foundProdCaller = true
		}
		if c.Path == "main.go" && c.Line == 3 {
			t.Fatalf("expected declaration line to be excluded from callers: %+v", c)
		}
	}
	if !foundProdCaller {
		t.Fatalf("expected server.go caller in Callers, got: %+v", callers.Callers)
	}
	if len(callers.Tests) != 1 || callers.Tests[0].Path != "main_test.go" {
		t.Fatalf("expected main_test.go in Tests, got: %+v", callers.Tests)
	}

	// Secret and path-traversal protection
	if _, err := svc.Inspect(dir, ".env", "", 1, 5, 0); err == nil {
		t.Fatal("expected .env inspection to be rejected")
	}
	if _, err := svc.Inspect(dir, "../outside.go", "", 1, 5, 0); err == nil {
		t.Fatal("expected path traversal inspection to be rejected")
	}

	// HTTP route verification
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	inspectW := httptest.NewRecorder()
	mux.ServeHTTP(inspectW, httptest.NewRequest(http.MethodGet, "/api/hoxi/code-index/inspect?directory="+url.QueryEscape(dir)+"&symbol=HandleRequest", nil))
	if inspectW.Code != http.StatusOK {
		t.Fatalf("inspect HTTP route failed: %d %s", inspectW.Code, inspectW.Body.String())
	}
	var httpInspect InspectResponse
	if err := json.Unmarshal(inspectW.Body.Bytes(), &httpInspect); err != nil || httpInspect.Path != "main.go" {
		t.Fatalf("unexpected HTTP inspect payload: %+v (%v)", httpInspect, err)
	}

	callersW := httptest.NewRecorder()
	mux.ServeHTTP(callersW, httptest.NewRequest(http.MethodGet, "/api/hoxi/code-index/callers?directory="+url.QueryEscape(dir)+"&symbol=HandleRequest&path=main.go", nil))
	if callersW.Code != http.StatusOK {
		t.Fatalf("callers HTTP route failed: %d %s", callersW.Code, callersW.Body.String())
	}
	var httpCallers CallersResponse
	if err := json.Unmarshal(callersW.Body.Bytes(), &httpCallers); err != nil || len(httpCallers.Tests) != 1 {
		t.Fatalf("unexpected HTTP callers payload: %+v (%v)", httpCallers, err)
	}
}
