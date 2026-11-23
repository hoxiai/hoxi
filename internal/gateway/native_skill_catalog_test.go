package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/runner"
	"github.com/hoxiai/hoxi/internal/skills"
)

func TestBuildNativeSkillCatalogPromptDeterministicSortAndExposure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/hoxi/skills/list", func(w http.ResponseWriter, _ *http.Request) {
		resp := skills.ListResponse{
			Skills: []skills.SkillDescriptor{
				{Name: "zebra-deploy", Description: "Deploy zebra service", Exposure: skills.ExposureAuto},
				{Name: "hidden-blocked", Description: "Should never appear", Exposure: skills.ExposureUnavailable},
				{Name: "alpha-audit", Description: "Audit repository boundaries", Exposure: skills.ExposureAuto},
				{Name: "zeta-manual", Description: "Manual skill", Exposure: skills.ExposureOnDemand},
				{Name: "beta-manual", Description: "Beta manual skill", Exposure: skills.ExposureOnDemand},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	gw := &Gateway{mux: mux, rootDir: t.TempDir()}
	out := gw.buildNativeSkillCatalogPrompt(context.Background(), "ses_test_sort", gw.rootDir)
	if out == "" {
		t.Fatal("expected non-empty skill catalog prompt")
	}
	if strings.Contains(out, "hidden-blocked") {
		t.Fatalf("unavailable skill must be excluded, got:\n%s", out)
	}
	alphaIdx := strings.Index(out, "<name>alpha-audit</name>")
	zebraIdx := strings.Index(out, "<name>zebra-deploy</name>")
	if alphaIdx < 0 || zebraIdx < 0 || alphaIdx >= zebraIdx {
		t.Fatalf("expected alpha-audit before zebra-deploy in sorted catalog, got:\n%s", out)
	}
	if !strings.Contains(out, "On-demand skills, listed by name only: beta-manual, zeta-manual.") {
		t.Fatalf("expected deterministically sorted on-demand skills line, got:\n%s", out)
	}
}

type stubSessionMCPExt struct {
	servers map[string]any
}

func (s stubSessionMCPExt) HasSessionContext(string) bool                   { return false }
func (s stubSessionMCPExt) ForgetSession(string)                            {}
func (s stubSessionMCPExt) RegisterRoutes(*http.ServeMux)                   {}
func (s stubSessionMCPExt) Close()                                          {}
func (s stubSessionMCPExt) SessionMCPServers(string, string) map[string]any { return s.servers }

func TestAppDevSessionIsolatesSkillSurfaceAndSessionMCP(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOXI_HOME", filepath.Join(home, ".hoxi"))

	workDir := filepath.Join(home, ".hoxi", "workspaces", "default", "demo-app")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	svc := runner.NewService(workDir, nil, nil, filepath.Join(home, "runner-sessions.json"))
	t.Cleanup(svc.CloseAll)

	normalSess, _, err := svc.CreateSession(runner.CreateSessionRequest{
		Directory:   workDir,
		Runtime:     string(runner.RuntimeNative),
		Surface:     "work",
		Mode:        "build",
		SandboxMode: "local",
	}, workDir)
	if err != nil {
		t.Fatal(err)
	}
	devSess, _, err := svc.CreateSession(runner.CreateSessionRequest{
		Directory:   workDir,
		Runtime:     string(runner.RuntimeNative),
		Surface:     "work",
		Mode:        "app-dev",
		SandboxMode: "local",
	}, workDir)
	if err != nil {
		t.Fatal(err)
	}

	gw := &Gateway{
		rootDir: workDir,
		runner:  svc,
		extensions: []Extension{
			stubSessionMCPExt{servers: map[string]any{"work-local-demo": map[string]any{"type": "remote", "url": "http://127.0.0.1:5400/mcp"}}},
		},
	}

	// Normal Work session keeps surface="work" and dynamic Work App MCP servers.
	normalCtx, err := gw.resolveSkillContext(context.Background(), workDir, normalSess.ID, "native")
	if err != nil {
		t.Fatalf("resolveSkillContext normal failed: %v", err)
	}
	if normalCtx.Surface != "work" {
		t.Fatalf("normal Work session Surface = %q, want work", normalCtx.Surface)
	}
	if len(normalCtx.ExtraMCPServers) != 1 || normalCtx.ExtraMCPServers[0] != "work-local-demo" {
		t.Fatalf("normal Work session ExtraMCPServers = %v, want [work-local-demo]", normalCtx.ExtraMCPServers)
	}

	// App-dev session demotes surface to "code" and drops runtime Work App MCP servers.
	devCtx, err := gw.resolveSkillContext(context.Background(), workDir, devSess.ID, "native")
	if err != nil {
		t.Fatalf("resolveSkillContext app-dev failed: %v", err)
	}
	if devCtx.Surface != "code" {
		t.Fatalf("app-dev session Surface = %q, want code", devCtx.Surface)
	}
	if len(devCtx.ExtraMCPServers) != 0 {
		t.Fatalf("app-dev session ExtraMCPServers = %v, want empty", devCtx.ExtraMCPServers)
	}
	if gotMCP := gw.extensionSessionMCPServers(devSess.ID, workDir); len(gotMCP) != 0 {
		t.Fatalf("extensionSessionMCPServers(app-dev) = %v, want empty", gotMCP)
	}
}
