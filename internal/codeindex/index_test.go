package codeindex

import (
	"os"
	"path/filepath"
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
