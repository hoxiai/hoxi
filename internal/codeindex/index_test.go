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
		if c.Path == "README.md" {
			t.Fatalf("expected prose README.md to be excluded from callers: %+v", c)
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

func TestInspectRejectsProtectedDirectoriesAndStateFiles(t *testing.T) {
	dir := fixtureWorkspace(t)
	writeFile(t, filepath.Join(dir, ".hoxi", "cloudflared.json"), `{"token":"secret-cf-token"}`)
	writeFile(t, filepath.Join(dir, ".hoxi", "models.json"), `{"apiKey":"sk-secret"}`)
	writeFile(t, filepath.Join(dir, ".claude", ".credentials.json"), `{"claudeAiOauth":"secret"}`)
	writeFile(t, filepath.Join(dir, ".opencode", "plugins", "custom.js"), "export default {}\n")
	writeFile(t, filepath.Join(dir, ".agents", "rules.md"), "internal rule\n")
	writeFile(t, filepath.Join(dir, ".vscode", "settings.json"), `{"terminal.integrated.env.osx":{}}`)
	writeFile(t, filepath.Join(dir, "models.json"), `{"providers":{}}`)
	writeFile(t, filepath.Join(dir, "pairing.json"), `{"token":"lan-secret"}`)
	_ = os.Symlink(filepath.Join(dir, ".hoxi", "cloudflared.json"), filepath.Join(dir, "leak.go"))

	svc := NewService(t.TempDir())
	for _, blocked := range []string{
		".hoxi/cloudflared.json",
		".hoxi/models.json",
		".claude/.credentials.json",
		".git/config",
		".opencode/plugins/custom.js",
		".agents/rules.md",
		".vscode/settings.json",
		"models.json",
		"pairing.json",
		"leak.go",
	} {
		if res, err := svc.Inspect(dir, blocked, "", 1, 20, 0); err == nil {
			t.Fatalf("expected Inspect(%q) to be rejected, but got content: %+v", blocked, res)
		}
	}
	// Attempting to pass a protected directory itself as workspace root must also fail,
	// while an isolated session worktree under .hoxi/worktrees/<id> remains allowed.
	if _, err := svc.Inspect(filepath.Join(dir, ".hoxi"), "cloudflared.json", "", 1, 10, 0); err == nil {
		t.Fatal("expected Inspect with directory=.hoxi to be rejected")
	}
	wtDir := filepath.Join(dir, ".hoxi", "worktrees", "wt_1")
	writeFile(t, filepath.Join(wtDir, "app.go"), "package main\n\nfunc InWorktree() {}\n")
	if res, err := svc.Inspect(wtDir, "app.go", "InWorktree", 0, 0, 1); err != nil || res.Symbol != "InWorktree" {
		t.Fatalf("expected isolated worktree under .hoxi/worktrees/wt_1 to be allowed, got: %+v (%v)", res, err)
	}
}

func TestInspectLocatesLateSymbolAndRejectsMissingSymbol(t *testing.T) {
	dir := t.TempDir()
	var lines []string
	lines = append(lines, "package sample", "")
	lines = append(lines, "func EarlyHelper() {", "\tprintln(\"early\")", "}", "")
	for len(lines) < 151 {
		lines = append(lines, "// filler comment line")
	}
	// Line 152 (1-indexed)
	lines = append(lines,
		"func TestLateFunction(t *testing.T) {",
		"\tprintln(\"target at line 152\")",
		"}",
		"",
		"func TrailingHelper() {",
		"\tprintln(\"trailing\")",
		"}",
	)
	writeFile(t, filepath.Join(dir, "long_test.go"), strings.Join(lines, "\n")+"\n")

	svc := NewService(t.TempDir())
	for _, startLine := range []int{0, 1} {
		res, err := svc.Inspect(dir, "long_test.go", "TestLateFunction", startLine, 0, 2)
		if err != nil {
			t.Fatalf("Inspect(TestLateFunction, startLine=%d) failed: %v", startLine, err)
		}
		if res.StartLine < 148 || res.StartLine > 152 {
			t.Fatalf("expected StartLine near 152 (startLine=%d), got StartLine=%d EndLine=%d Content:\n%s", startLine, res.StartLine, res.EndLine, res.Content)
		}
		if !strings.Contains(res.Content, "func TestLateFunction") || !strings.Contains(res.Content, "target at line 152") {
			t.Fatalf("expected TestLateFunction body in content, got:\n%s", res.Content)
		}
		if strings.Contains(res.Content, "func EarlyHelper") {
			t.Fatalf("unexpectedly returned file header instead of late symbol:\n%s", res.Content)
		}
	}

	for _, startLine := range []int{0, 1, 10} {
		if bad, err := svc.Inspect(dir, "long_test.go", "NonExistentSymbol", startLine, 0, 2); err == nil {
			t.Fatalf("expected missing symbol (startLine=%d) in explicit path to return error, got: %+v", startLine, bad)
		}
	}
}

func TestCalleesImpactAndContextGraph(t *testing.T) {
	dir := fixtureWorkspace(t)
	writeFile(t, filepath.Join(dir, "billing", "pay.go"), "package billing\n\nfunc ChargeCard(amount int) error {\n\treturn nil\n}\n")
	writeFile(t, filepath.Join(dir, "checkout.go"), "package main\n\nimport (\n\t\"example.com/app/billing\"\n\t\"example.com/app/lib\"\n)\n\nfunc HandleCheckout(items []int) error {\n\ttotal := computeTotal(items)\n\treturn billing.ChargeCard(total)\n}\n")
	writeFile(t, filepath.Join(dir, "router.go"), "package main\n\nfunc DispatchRoute() error {\n\treturn HandleCheckout([]int{1, 2})\n}\n")
	writeFile(t, filepath.Join(dir, "checkout_test.go"), "package main\n\nimport \"testing\"\n\nfunc TestHandleCheckout(t *testing.T) {\n\t_ = HandleCheckout(nil)\n}\n")
	writeFile(t, filepath.Join(dir, "router_test.go"), "package main\n\nimport \"testing\"\n\nfunc TestDispatchRoute(t *testing.T) {\n\t_ = DispatchRoute()\n}\n")
	writeFile(t, filepath.Join(dir, ".env"), "STRIPE_SECRET=sk_live_secret\n")

	svc := NewService(t.TempDir())

	// 1. Callees + Imports
	callees, err := svc.Callees(dir, "HandleCheckout", "", 10)
	if err != nil {
		t.Fatalf("Callees failed: %v", err)
	}
	if callees.Path != "checkout.go" || callees.Symbol != "HandleCheckout" {
		t.Fatalf("unexpected callees metadata: %+v", callees)
	}
	if len(callees.Imports) != 2 {
		t.Fatalf("expected 2 imports on checkout.go, got: %+v", callees.Imports)
	}
	calleeSymbols := map[string]string{}
	for _, c := range callees.Callees {
		calleeSymbols[c.Symbol] = c.Path
	}
	if calleeSymbols["computeTotal"] != "lib/util.ts" || calleeSymbols["ChargeCard"] != "billing/pay.go" {
		t.Fatalf("expected computeTotal and ChargeCard in callees, got: %+v", callees.Callees)
	}

	// 2. Multi-hop Impact (ChargeCard -> HandleCheckout -> DispatchRoute + transitive tests)
	impact, err := svc.Impact(dir, "ChargeCard", "", 2, 10)
	if err != nil {
		t.Fatalf("Impact failed: %v", err)
	}
	if len(impact.DirectCallers) != 1 || impact.DirectCallers[0].Symbol != "HandleCheckout" {
		t.Fatalf("expected HandleCheckout as direct caller, got: %+v", impact.DirectCallers)
	}
	if len(impact.TransitiveCallers) != 1 || impact.TransitiveCallers[0].Symbol != "DispatchRoute" || impact.TransitiveCallers[0].Via != "HandleCheckout -> ChargeCard" {
		t.Fatalf("expected DispatchRoute as depth-2 transitive caller, got: %+v", impact.TransitiveCallers)
	}
	testPaths := map[string]bool{}
	for _, th := range impact.Tests {
		testPaths[th.Path] = true
	}
	if !testPaths["checkout_test.go"] || !testPaths["router_test.go"] {
		t.Fatalf("expected both checkout_test.go and router_test.go in impact Tests, got: %+v", impact.Tests)
	}

	// 3. Single-shot Context bundle
	ctxRes, err := svc.Context(dir, "", "HandleCheckout", "", 1, 10)
	if err != nil {
		t.Fatalf("Context failed: %v", err)
	}
	if ctxRes.Inspect.Path != "checkout.go" || !strings.Contains(ctxRes.Inspect.Content, "billing.ChargeCard(total)") {
		t.Fatalf("unexpected Context.Inspect: %+v", ctxRes.Inspect)
	}
	if len(ctxRes.Callees) != 2 || len(ctxRes.Callers) != 1 || len(ctxRes.Tests) < 1 {
		t.Fatalf("incomplete Context bundle: %+v", ctxRes)
	}

	// 4. Reject protected/secret paths in Callees, Impact, and Context
	for _, blocked := range []string{".env", "../outside.go"} {
		if _, err := svc.Callees(dir, "HandleCheckout", blocked, 5); err == nil {
			t.Fatalf("expected Callees with path=%q to fail", blocked)
		}
		if _, err := svc.Impact(dir, "HandleCheckout", blocked, 2, 5); err == nil {
			t.Fatalf("expected Impact with path=%q to fail", blocked)
		}
		if _, err := svc.Context(dir, blocked, "HandleCheckout", "", 1, 5); err == nil {
			t.Fatalf("expected Context with path=%q to fail", blocked)
		}
	}

	// 5. HTTP endpoints
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	for _, endpoint := range []string{
		"/api/hoxi/code-index/callees?directory=" + url.QueryEscape(dir) + "&symbol=HandleCheckout",
		"/api/hoxi/code-index/impact?directory=" + url.QueryEscape(dir) + "&symbol=ChargeCard",
		"/api/hoxi/code-index/context?directory=" + url.QueryEscape(dir) + "&symbol=HandleCheckout",
	} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, endpoint, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s failed: %d %s", endpoint, w.Code, w.Body.String())
		}
	}
}

func TestPythonIndentationAndStringBraceBoundaries(t *testing.T) {
	dir := t.TempDir()
	pyCode := strings.Join([]string{
		"from billing.service import charge_user",
		"",
		"async def process_order(items:",
		"                        list[int]) -> int:",
		"    total = sum(items)",
		"    # comment inside block",
		"    if total > 0:",
		"        charge_user(total)",
		"    return total",
		"",
		"def unrelated_helper():",
		"    return 42",
	}, "\n") + "\n"
	writeFile(t, filepath.Join(dir, "order.py"), pyCode)

	goCode := strings.Join([]string{
		"package main",
		"",
		"func ParseBraces() string {",
		"\tlit := \"}}}{{{\"",
		"\t/* block comment with }}} */",
		"\t// line comment with }}}",
		"\treturn lit",
		"}",
		"",
		"func NextFunc() {}",
	}, "\n") + "\n"
	writeFile(t, filepath.Join(dir, "braces.go"), goCode)

	svc := NewService(t.TempDir())
	pyInspect, err := svc.Inspect(dir, "order.py", "process_order", 0, 0, -1)
	if err != nil {
		t.Fatalf("Inspect(process_order) failed: %v", err)
	}
	if pyInspect.StartLine != 3 || pyInspect.EndLine != 9 {
		t.Fatalf("expected Python block lines 3..9, got %d..%d:\n%s", pyInspect.StartLine, pyInspect.EndLine, pyInspect.Content)
	}
	if strings.Contains(pyInspect.Content, "unrelated_helper") {
		t.Fatalf("Python inspect spilled into next function:\n%s", pyInspect.Content)
	}

	goInspect, err := svc.Inspect(dir, "braces.go", "ParseBraces", 0, 0, -1)
	if err != nil {
		t.Fatalf("Inspect(ParseBraces) failed: %v", err)
	}
	if goInspect.StartLine != 3 || goInspect.EndLine != 8 {
		t.Fatalf("expected Go function lines 3..8, got %d..%d:\n%s", goInspect.StartLine, goInspect.EndLine, goInspect.Content)
	}
	if !strings.Contains(goInspect.Content, "return lit") || strings.Contains(goInspect.Content, "NextFunc") {
		t.Fatalf("unexpected Go function boundary content:\n%s", goInspect.Content)
	}
}
