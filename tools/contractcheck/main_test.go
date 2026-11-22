package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func findRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Dir(filepath.Dir(wd))
}

func TestCurrentRepoPassesContractCheck(t *testing.T) {
	repoRoot := findRepoRoot(t)
	violations, stats, err := Run(repoRoot)
	if err != nil {
		t.Fatalf("Run(%s) returned error: %v", repoRoot, err)
	}
	if len(violations) > 0 {
		var lines []string
		for _, v := range violations {
			lines = append(lines, v.String())
		}
		t.Fatalf("expected 0 violations on current repository, got %d:\n%s", len(violations), strings.Join(lines, "\n"))
	}
	if stats.GoFiles < 100 {
		t.Fatalf("expected >100 Go files scanned, got %d", stats.GoFiles)
	}
	if stats.InternalPackages < 30 {
		t.Fatalf("expected >=30 internal packages scanned, got %d", stats.InternalPackages)
	}
	if stats.PluginModules < 10 {
		t.Fatalf("expected >=10 transitive plugin JS modules, got %d", stats.PluginModules)
	}
}

func writeSyntheticMinimalGoRepo(t *testing.T, extraFiles map[string]string) string {
	t.Helper()
	root := t.TempDir()

	baseFiles := map[string]string{
		"go.mod": "module " + defaultModulePath + "\n\ngo 1.25.0\n",
		"internal/llm/llm.go": `package llm
type Client interface{}
`,
		"internal/sandbox/sandbox.go": "package sandbox\nimport _ \"" + defaultModulePath + "/internal/llm\"\n",
		"internal/provider/http.go": `package provider
import (
	"net/http"
	"time"
)
var tr = &http.Transport{IdleConnTimeout: 30 * time.Second}
func NewClient() *http.Client {
	return &http.Client{Timeout: 5 * time.Second, Transport: tr}
}
`,
		"internal/gateway/gateway.go": "package gateway\nimport (\n\t_ \"" + defaultModulePath + "/internal/llm\"\n\t_ \"" + defaultModulePath + "/internal/provider\"\n\t_ \"" + defaultModulePath + "/internal/sandbox\"\n)\n",
	}

	for rel, content := range baseFiles {
		writeTestFile(t, root, rel, content)
	}
	for rel, content := range extraFiles {
		writeTestFile(t, root, rel, content)
	}
	return root
}

func filterNonPluginViolations(vs []Violation) []Violation {
	var out []Violation
	for _, v := range vs {
		if v.Rule != "plugin-sync" {
			out = append(out, v)
		}
	}
	return out
}

func writeTestFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", full, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

func assertHasRule(t *testing.T, violations []Violation, wantRule, wantSubstr string) {
	t.Helper()
	for _, v := range violations {
		if v.Rule == wantRule && strings.Contains(v.String(), wantSubstr) {
			return
		}
	}
	var got []string
	for _, v := range violations {
		got = append(got, v.String())
	}
	t.Fatalf("expected violation rule=%q containing %q, got:\n%s", wantRule, wantSubstr, strings.Join(got, "\n"))
}

func TestSyntheticGoBaselinePasses(t *testing.T) {
	root := writeSyntheticMinimalGoRepo(t, nil)
	vs, _, err := Run(root)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if got := filterNonPluginViolations(vs); len(got) != 0 {
		t.Fatalf("expected 0 Go violations on synthetic baseline, got: %v", got)
	}
}

func TestDetectsLineCeilingAndNamingViolations(t *testing.T) {
	var big strings.Builder
	big.WriteString("package intake\n")
	for i := 0; i < 801; i++ {
		fmt.Fprintf(&big, "// line %d\n", i)
	}
	root := writeSyntheticMinimalGoRepo(t, map[string]string{
		"internal/intake/too_long.go":         big.String(),
		"internal/intake/BadCamelCase.go":     "package intake\n",
		"internal/intake/zz_scratch_probe.go": "package intake\n",
	})
	violations, _, err := Run(root)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	assertHasRule(t, violations, "line-limit", "internal/intake/too_long.go")
	assertHasRule(t, violations, "file-naming", "internal/intake/BadCamelCase.go")
	assertHasRule(t, violations, "scratch-file", "internal/intake/zz_scratch_probe.go")
}

func TestDetectsLayerDAGAndCycleViolations(t *testing.T) {
	t.Run("base_importing_domain", func(t *testing.T) {
		root := writeSyntheticMinimalGoRepo(t, map[string]string{
			"internal/llm/bad.go": "package llm\nimport _ \"" + defaultModulePath + "/internal/sandbox\"\n",
		})
		vs, _, _ := Run(root)
		assertHasRule(t, vs, "layer-dag", "base package internal/llm must only import Tier 0 base packages")
	})

	t.Run("domain_reverse_importing_gateway_and_provider", func(t *testing.T) {
		root := writeSyntheticMinimalGoRepo(t, map[string]string{
			"internal/intake/bad_gw.go":   "package intake\nimport _ \"" + defaultModulePath + "/internal/gateway\"\n",
			"internal/intake/bad_prov.go": "package intake\nimport _ \"" + defaultModulePath + "/internal/provider\"\n",
			"internal/intake/bad_dom.go":  "package intake\nimport _ \"" + defaultModulePath + "/internal/sandbox\"\n",
		})
		vs, _, _ := Run(root)
		assertHasRule(t, vs, "layer-dag", "must not reverse-import top-tier internal/gateway")
		assertHasRule(t, vs, "layer-dag", "must not import internal/provider directly")
		assertHasRule(t, vs, "layer-dag", "must not cross-import domain package internal/sandbox")
	})

	t.Run("import_cycle", func(t *testing.T) {
		root := writeSyntheticMinimalGoRepo(t, map[string]string{
			"internal/opencode/oc.go":     "package opencode\nimport _ \"" + defaultModulePath + "/internal/sandbox\"\n",
			"internal/sandbox/sandbox.go": "package sandbox\nimport _ \"" + defaultModulePath + "/internal/opencode\"\n",
		})
		vs, _, _ := Run(root)
		assertHasRule(t, vs, "layer-cycle", "internal/opencode -> internal/sandbox -> internal/opencode")
	})
}

func TestDetectsProviderBareHTTPClients(t *testing.T) {
	root := writeSyntheticMinimalGoRepo(t, map[string]string{
		"internal/provider/http.go": `package provider
import (
	"net/http"
	"time"
)
func BadNoTransport() *http.Client {
	return &http.Client{Timeout: 5 * time.Second}
}
func BadNilTransport() *http.Client {
	return &http.Client{Transport: nil}
}
func BadDefault() *http.Client {
	return http.DefaultClient
}
`,
		"internal/provider/image_gen_openai.go": `package provider
import "net/http"
func UnauthorizedFileClient(rt http.RoundTripper) *http.Client {
	return &http.Client{Transport: rt}
}
`,
		"internal/sandbox/raw_get.go": `package sandbox
import "net/http"
func Fetch(u string) { _, _ = http.Get(u) }
`,
	})
	vs, _, err := Run(root)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	assertHasRule(t, vs, "provider-http-client", "internal/provider/http.go:7")
	assertHasRule(t, vs, "provider-http-client", "internal/provider/http.go:10")
	assertHasRule(t, vs, "provider-http-client", "DefaultClient is forbidden")
	assertHasRule(t, vs, "provider-http-client", "constructing http.Client in internal/provider/image_gen_openai.go is forbidden")
	assertHasRule(t, vs, "http-client", "http.Get bypasses explicit timeout")
}

func TestDetectsStaleBrowserPluginBundle(t *testing.T) {
	modules := map[string]string{
		"internal/opencode/code_index_tool.js": `const indexActions = ["search", "symbol", "impact", "map", "rebuild"];`,
		"internal/opencode/hoxi_plugin.js": `return {
  tool: { hoxi_code_index: uc(), hoxi_video: pc(), goal_report: gr() },
  "experimental.chat.system.transform": async () => {}
};`,
		"web/src/policy/external-path-risk.js": `const PROTECTED_PATH_SEGMENTS = new Set([".hoxi"]);
const SENSITIVE_BASENAMES = new Set(["sample_guard_entry"]);`,
	}

	vs := verifyPluginSemanticTokens(
		"internal/opencode/browser_plugin.mjs",
		`const acts=["search","symbol","impact","rebuild"],seg=[".hoxi"],sens=["sample_guard_entry"],t={tool:{hoxi_code_index:1,goal_report:2}};`,
		modules,
	)
	assertHasRule(t, vs, "plugin-sync", `missing hoxi_code_index action "map"`)
	assertHasRule(t, vs, "plugin-sync", `missing registered tool "hoxi_video"`)

	root := t.TempDir()
	writeTestFile(t, root, "internal/opencode/browser_plugin.js", "export default {};\n")
	writeTestFile(t, root, "internal/opencode/browser_plugin.mjs", "export default {};\n")
	bannerVs, _, _ := checkBrowserPluginSync(root)
	assertHasRule(t, bannerVs, "plugin-sync", "bundle must start with")
}
