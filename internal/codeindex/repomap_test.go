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

func writeRepoMapFixture(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
}

func TestRepoMapPageRankAndDampenedReferences(t *testing.T) {
	ws := t.TempDir()
	idxStore := t.TempDir()

	var noisyCalls strings.Builder
	for i := 0; i < 25; i++ {
		noisyCalls.WriteString("\tlogutil.TraceEvent(\"tick\")\n")
	}

	writeRepoMapFixture(t, ws, map[string]string{
		"pkg/core/engine.go": `package core

type CoreEngine struct{}

func NewCoreEngine() *CoreEngine {
	return &CoreEngine{}
}

func (e *CoreEngine) ExecuteTurn(prompt string) string {
	return prompt
}
`,
		"pkg/logutil/logger.go": `package logutil

func TraceEvent(msg string) {}
`,
		"pkg/router/dispatch.go": `package router

type RouteDispatcher struct{}

func DispatchAction(id string) error {
	return nil
}
`,
		"pkg/api/server.go": `package api

import "demo/pkg/core"

func HandlePrompt(p string) string {
	eng := core.NewCoreEngine()
	return eng.ExecuteTurn(p)
}
`,
		"pkg/worker/runner.go": `package worker

import "demo/pkg/core"

func RunJob() string {
	eng := core.NewCoreEngine()
	return eng.ExecuteTurn("bg")
}
`,
		"cmd/cli/main.go": "package main\n\nimport (\n\t\"demo/pkg/core\"\n\t\"demo/pkg/logutil\"\n)\n\nfunc main() {\n" +
			noisyCalls.String() +
			"\teng := core.NewCoreEngine()\n\t_ = eng.ExecuteTurn(\"cli\")\n}\n",
	})

	svc := NewService(idxStore)
	res, err := svc.Map(ws, "", "", 10)
	if err != nil {
		t.Fatalf("Map failed: %v", err)
	}
	if res.Files != 6 {
		t.Fatalf("expected 6 indexed code files, got %d (%+v)", res.Files, res.Entries)
	}
	if len(res.Entries) == 0 {
		t.Fatal("expected non-empty entries")
	}

	if res.Entries[0].Path != "pkg/core/engine.go" {
		t.Fatalf("expected pkg/core/engine.go ranked #1 by dampened PageRank, got %q (entries=%+v)", res.Entries[0].Path, res.Entries)
	}
	foundExecuteTurn := false
	for _, s := range res.Entries[0].Symbols {
		if s.Symbol == "ExecuteTurn" || s.Symbol == "NewCoreEngine" {
			foundExecuteTurn = true
			if s.References <= 0 {
				t.Fatalf("expected positive inbound references on %s, got %+v", s.Symbol, s)
			}
		}
	}
	if !foundExecuteTurn {
		t.Fatalf("expected core symbols on pkg/core/engine.go, got %+v", res.Entries[0].Symbols)
	}

	focused, err := svc.Map(ws, "", "DispatchAction", 5)
	if err != nil {
		t.Fatalf("focused Map failed: %v", err)
	}
	if len(focused.Entries) == 0 || focused.Entries[0].Path != "pkg/router/dispatch.go" {
		t.Fatalf("expected query-focused RepoMap to rank pkg/router/dispatch.go #1, got %+v", focused.Entries)
	}

	scoped, err := svc.Map(ws, "pkg/api", "", 5)
	if err != nil {
		t.Fatalf("scoped Map failed: %v", err)
	}
	if len(scoped.Entries) != 1 || scoped.Entries[0].Path != "pkg/api/server.go" {
		t.Fatalf("expected scoped RepoMap to return only pkg/api/server.go, got %+v", scoped.Entries)
	}

	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	u := "/api/hoxi/code-index/map?directory=" + url.QueryEscape(ws) + "&limit=3"
	req := httptest.NewRequest(http.MethodGet, u, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from /api/hoxi/code-index/map, got %d: %s", rec.Code, rec.Body.String())
	}
	var httpRes RepoMapResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &httpRes); err != nil {
		t.Fatalf("unmarshal map HTTP response: %v", err)
	}
	if len(httpRes.Entries) != 3 || httpRes.Entries[0].Path != "pkg/core/engine.go" {
		t.Fatalf("unexpected HTTP RepoMap response: %+v", httpRes)
	}
}
