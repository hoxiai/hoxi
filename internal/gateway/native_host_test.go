package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hoxiai/hoxi/internal/agent"
	"github.com/hoxiai/hoxi/internal/llm"
	"github.com/hoxiai/hoxi/internal/runner"
)

func TestNativeHostOfflineRoutesAndSkillTool(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	wsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(wsDir, "hello_native.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(wsDir, ".hoxi", "skills", "demo-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillBody := "---\nname: demo-skill\ndescription: Demo local workspace skill\n---\n# Demo Skill\nAlways run unit tests."
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillBody), 0o644); err != nil {
		t.Fatal(err)
	}
	customCmdDir := filepath.Join(wsDir, ".hoxi", "commands")
	if err := os.MkdirAll(customCmdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(customCmdDir, "ship-check.md"), []byte("---\ndescription: Pre-release verification\n---\nRun ./verify.sh $ARGUMENTS"), 0o644); err != nil {
		t.Fatal(err)
	}
	customAgentDir := filepath.Join(wsDir, ".hoxi", "agents")
	if err := os.MkdirAll(customAgentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(customAgentDir, "qa-bot.md"), []byte("---\ndescription: QA verification specialist\n---\nVerify edge cases."), 0o644); err != nil {
		t.Fatal(err)
	}

	g := New(0, "", wsDir, nil, nil)
	defer g.CloseBrowser()

	checkGET := func(target string) []byte {
		t.Helper()
		req := localRequest(http.MethodGet, target, nil)
		req.Host = "wails"
		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: status=%d body=%s", target, rec.Code, rec.Body.String())
		}
		return rec.Body.Bytes()
	}

	if raw := checkGET("/agent?directory=" + wsDir); !strings.Contains(string(raw), `"build"`) || !strings.Contains(string(raw), `"explore"`) || !strings.Contains(string(raw), `"qa-bot"`) {
		t.Fatalf("unexpected /agent payload: %s", string(raw))
	}
	if raw := checkGET("/command?directory=" + wsDir); !strings.Contains(string(raw), `"compact"`) || !strings.Contains(string(raw), `"review"`) || !strings.Contains(string(raw), `"ship-check"`) {
		t.Fatalf("unexpected /command payload: %s", string(raw))
	}
	if raw := checkGET("/lsp"); strings.TrimSpace(string(raw)) != "[]" {
		t.Fatalf("unexpected /lsp payload: %s", string(raw))
	}
	if raw := checkGET("/project/current"); !strings.Contains(string(raw), wsDir) {
		t.Fatalf("unexpected /project/current payload: %s", string(raw))
	}
	if raw := checkGET("/find/file?query=hello_native"); !strings.Contains(string(raw), "hello_native.go") {
		t.Fatalf("unexpected /find/file payload: %s", string(raw))
	}
	if raw := checkGET("/path"); !strings.Contains(string(raw), wsDir) {
		t.Fatalf("unexpected /path payload: %s", string(raw))
	}
	if raw := checkGET("/provider"); !strings.Contains(string(raw), `"all"`) {
		t.Fatalf("unexpected /provider payload: %s", string(raw))
	}
	if raw := checkGET("/global/health"); !strings.Contains(string(raw), `"native"`) {
		t.Fatalf("unexpected /global/health payload: %s", string(raw))
	}
	if raw := checkGET("/skill"); !strings.Contains(string(raw), "demo-skill") {
		t.Fatalf("unexpected /skill payload: %s", string(raw))
	}
	if cat := g.buildNativeSkillCatalogPrompt(context.Background(), "ses_nat_test1", wsDir); !strings.Contains(cat, "<available_skills>") || !strings.Contains(cat, "demo-skill") {
		t.Fatalf("unexpected native skill catalog prompt:\n%s", cat)
	}

	tc := agent.ToolContext{
		SessionID:   "ses_nat_test1",
		WorktreeDir: wsDir,
	}
	listRes, err := g.invokeNativeHostTool(context.Background(), tc, "skill", json.RawMessage(`{}`))
	if err != nil || !strings.Contains(listRes.Output, "demo-skill") {
		t.Fatalf("skill list failed: err=%v out=%s", err, listRes.Output)
	}
	detailRes, err := g.invokeNativeHostTool(context.Background(), tc, "skill", json.RawMessage(`{"name":"demo-skill"}`))
	if err != nil || !strings.Contains(detailRes.Output, "Always run unit tests.") {
		t.Fatalf("skill detail failed: err=%v out=%s", err, detailRes.Output)
	}

	actionRes, err := extractHostActionResult("hoxi_browser", http.StatusOK, []byte(`{"ok":true,"url":"https://example.com","image":"ZmFrZS1qcGVn"}`))
	if err != nil {
		t.Fatalf("extractHostActionResult err=%v", err)
	}
	if len(actionRes.Media) != 1 || actionRes.Media[0].Type != agent.ContentMedia || actionRes.Media[0].Data != "ZmFrZS1qcGVn" {
		t.Fatalf("expected extracted media block, got %+v", actionRes.Media)
	}
	if strings.Contains(actionRes.Output, "ZmFrZS1qcGVn") {
		t.Fatalf("expected raw base64 image stripped from JSON text output: %s", actionRes.Output)
	}

	g.SetDataProfileService(stubDataProfileSvc{})
	dpRes, err := g.invokeNativeHostTool(context.Background(), tc, "hoxi_data", json.RawMessage(`{"action":"list"}`))
	if err != nil || !strings.Contains(dpRes.Output, "gopanel-stage") {
		t.Fatalf("hoxi_data list failed: err=%v out=%s", err, dpRes.Output)
	}
}

type stubDataProfileSvc struct{}

func (stubDataProfileSvc) InvokeJSON(_ context.Context, _ json.RawMessage) (map[string]any, error) {
	return map[string]any{"count": 1, "profiles": []map[string]any{{"id": "gopanel-stage", "hasAuth": true}}}, nil
}

type gatewayStubStream struct {
	step atomic.Int32
}

func (s *gatewayStubStream) ResolveStreamTarget(providerID, modelID string) llm.StreamTarget {
	return llm.StreamTarget{
		ProviderID:   providerID,
		ModelID:      modelID,
		BaseURL:      "https://api.example.com/v1",
		Protocol:     llm.ProtocolOpenAI,
		HasEndpoint:  true,
		ContextLimit: 128000,
		OutputLimit:  4096,
	}
}

func (s *gatewayStubStream) OpenStream(_ context.Context, _ llm.StreamHTTPRequest) (*http.Response, error) {
	n := s.step.Add(1)
	var body string
	if n == 1 {
		body = strings.Join([]string{
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c_task_1","function":{"name":"task","arguments":"{\"subagent_type\":\"explore\",\"description\":\"check\",\"prompt\":\"inspect\"}"}}]}}]}`,
			`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			`data: [DONE]`,
		}, "\n") + "\n"
	} else {
		body = "data: {\"choices\":[{\"delta\":{\"content\":\"handled rejection\"}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}, nil
}

func TestGatewayNativeAgentForwardsForceAskAndTurnFinish(t *testing.T) {
	wsDir := t.TempDir()
	na := newGatewayNativeAgent(
		&gatewayStubStream{},
		nil, nil, nil,
		func() map[string]any {
			return map[string]any{"permission": map[string]any{"task": "ask"}}
		},
		nil,
	)

	var sawForceAsk bool
	var gotFinish string
	err := na.RunTurn(context.Background(), runner.NativeTurnRequest{
		SessionID:   "ses_gw_ask",
		WorktreeDir: wsDir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Delegate task",
		PermissionHook: func(_ context.Context, req runner.NativePermissionRequest) error {
			if req.ToolName == "task" && req.ForceAsk {
				sawForceAsk = true
				return fmt.Errorf("explicit ask rejected")
			}
			return nil
		},
		Emit: func(evt runner.NativeEvent) {
			if evt.Kind == runner.NativeEventTurnFinish {
				gotFinish = evt.Finish
			}
		},
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if !sawForceAsk {
		t.Fatal("expected gatewayNativeAgent to forward ForceAsk=true for permission.task=ask")
	}
	if gotFinish != "stop" {
		t.Fatalf("expected NativeEventTurnFinish='stop', got %q", gotFinish)
	}
}
