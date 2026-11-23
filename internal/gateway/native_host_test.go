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
	if raw := checkGET("/api/hoxi/projects"); !strings.Contains(string(raw), wsDir) {
		t.Fatalf("unexpected /api/hoxi/projects payload: %s", string(raw))
	}
	if raw := checkGET("/api/hoxi/projects/current"); !strings.Contains(string(raw), wsDir) {
		t.Fatalf("unexpected /api/hoxi/projects/current payload: %s", string(raw))
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
	if raw := checkGET("/pty"); strings.TrimSpace(string(raw)) != "[]" {
		t.Fatalf("expected default native /pty list to return [], got: %s", string(raw))
	}
	if raw := checkGET("/api/hoxi/dataprofiles"); !strings.Contains(string(raw), `"profiles"`) {
		t.Fatalf("expected default /api/hoxi/dataprofiles route, got: %s", string(raw))
	}
	// Remote callers must be rejected on /api/hoxi/dataprofiles (desktop-only).
	remReq := localRequest(http.MethodGet, "/api/hoxi/dataprofiles", nil)
	remReq.Host = "wails"
	remReq = remReq.WithContext(context.WithValue(remReq.Context(), remoteSourceKey{}, remoteSourceTunnel))
	remRec := httptest.NewRecorder()
	g.ServeHTTP(remRec, remReq)
	if remRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for remote /api/hoxi/dataprofiles, got %d: %s", remRec.Code, remRec.Body.String())
	}

	// Workspace skill catalog without sessionID must default to runtime="native" when OpenCode is absent,
	// so its skl_* key matches a newly created native session.
	rawSkillList := checkGET("/api/hoxi/skills/list?directory=" + wsDir)
	var listResp struct {
		Runtime string `json:"runtime"`
		Skills  []struct {
			Key  string `json:"key"`
			Name string `json:"name"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(rawSkillList, &listResp); err != nil {
		t.Fatalf("unmarshal skills list: %v", err)
	}
	if listResp.Runtime != "native" {
		t.Fatalf("expected default offline skills runtime='native', got %q", listResp.Runtime)
	}
	var demoKey string
	for _, item := range listResp.Skills {
		if item.Name == "demo-skill" {
			demoKey = item.Key
			break
		}
	}
	if demoKey == "" {
		t.Fatalf("demo-skill missing from /api/hoxi/skills/list: %s", string(rawSkillList))
	}
	natSess, _, err := g.runner.CreateConversation(context.Background(), runner.CreateSessionRequest{Directory: wsDir}, wsDir)
	if err != nil {
		t.Fatalf("create native conversation: %v", err)
	}
	rawSkillDetail := checkGET("/api/hoxi/skills/detail?directory=" + wsDir + "&sessionID=" + natSess.ID + "&key=" + demoKey)
	if !strings.Contains(string(rawSkillDetail), "Always run unit tests.") {
		t.Fatalf("expected workspace skill key %s to resolve under native session %s: %s", demoKey, natSess.ID, string(rawSkillDetail))
	}
	if raw := checkGET("/skill"); !strings.Contains(string(raw), "demo-skill") {
		t.Fatalf("unexpected /skill payload: %s", string(raw))
	}
	// Verify canonical /api/hoxi/* host & provider routes (plus legacy /pty) work out-of-the-box without opencode serve.
	if raw := checkGET("/api/hoxi/pty"); strings.TrimSpace(string(raw)) != "[]" {
		t.Fatalf("expected out-of-the-box native /api/hoxi/pty list [], got: %s", string(raw))
	}
	if raw := checkGET("/pty"); strings.TrimSpace(string(raw)) != "[]" {
		t.Fatalf("expected out-of-the-box native /pty list [], got: %s", string(raw))
	}
	if raw := checkGET("/api/hoxi/agents?directory=" + wsDir); !strings.Contains(string(raw), `"build"`) {
		t.Fatalf("expected out-of-the-box /api/hoxi/agents, got: %s", string(raw))
	}
	if raw := checkGET("/api/hoxi/commands?directory=" + wsDir); !strings.Contains(string(raw), `"plan"`) {
		t.Fatalf("expected out-of-the-box /api/hoxi/commands, got: %s", string(raw))
	}
	if raw := checkGET("/api/hoxi/lsp?directory=" + wsDir); !json.Valid(raw) {
		t.Fatalf("expected valid JSON from /api/hoxi/lsp, got: %s", string(raw))
	}
	if raw := checkGET("/api/hoxi/provider/catalog"); !strings.Contains(string(raw), `"all"`) {
		t.Fatalf("expected out-of-the-box /api/hoxi/provider/catalog, got: %s", string(raw))
	}
	if raw := checkGET("/api/hoxi/provider/auth-methods"); !json.Valid(raw) {
		t.Fatalf("expected valid JSON from /api/hoxi/provider/auth-methods, got: %s", string(raw))
	}
	if raw := checkGET("/api/hoxi/provider/config"); !strings.Contains(string(raw), `"provider"`) {
		t.Fatalf("expected out-of-the-box /api/hoxi/provider/config, got: %s", string(raw))
	}
	if raw := checkGET("/api/hoxi/dataprofiles"); !strings.Contains(string(raw), `"profiles"`) {
		t.Fatalf("expected out-of-the-box /api/hoxi/dataprofiles, got: %s", string(raw))
	}
	// Verify default hoxi_data host tool works out-of-the-box before any test stub override.
	defaultDPRes, err := g.invokeNativeHostTool(context.Background(), agent.ToolContext{SessionID: "ses_nat_test1", WorktreeDir: wsDir}, "hoxi_data", json.RawMessage(`{"action":"list"}`))
	if err != nil || !strings.Contains(defaultDPRes.Output, `"count":0`) {
		t.Fatalf("default hoxi_data list failed: err=%v out=%s", err, defaultDPRes.Output)
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

func TestNormalizeComputerAgentInputCoercionAndStrictness(t *testing.T) {
	coerced := normalizeComputerAgentInput(json.RawMessage(`{"action":"click","windowID":4821,"elementID":"12","deltaY":120.0,"clickCount":"2","seconds":"12.5"}`))
	var parsed map[string]any
	if err := json.Unmarshal(coerced, &parsed); err != nil {
		t.Fatalf("unmarshal coerced: %v", err)
	}
	if parsed["windowID"] != "4821" {
		t.Fatalf("expected windowID string '4821', got %#v", parsed["windowID"])
	}
	if parsed["elementID"] != float64(12) || parsed["deltaY"] != float64(120) || parsed["clickCount"] != float64(2) || parsed["seconds"] != float64(12.5) {
		t.Fatalf("unexpected numeric coercions: %+v", parsed)
	}

	// Malformed non-integer values must NOT be silently dropped or coerced, so decodeComputerJSON rejects them.
	untouched := normalizeComputerAgentInput(json.RawMessage(`{"action":"scroll","windowID":"w1","elementID":"abc","deltaY":12.5,"seconds":"bad"}`))
	var bad map[string]any
	if err := json.Unmarshal(untouched, &bad); err != nil {
		t.Fatalf("unmarshal untouched: %v", err)
	}
	if bad["elementID"] != "abc" || bad["deltaY"] != 12.5 || bad["seconds"] != "bad" {
		t.Fatalf("expected malformed values preserved for strict rejection, got %+v", bad)
	}
}

func TestExtractHostActionResultDetectsScreenshotMime(t *testing.T) {
	pngRes, err := extractHostActionResult("hoxi_computer", http.StatusOK, []byte(`{"windowID":"1","image":"iVBORw0KGgoAAAANSUhEUg=="}`))
	if err != nil || len(pngRes.Media) != 1 || pngRes.Media[0].MimeType != "image/png" || pngRes.Media[0].Filename != "hoxi_computer.png" {
		t.Fatalf("expected PNG screenshot media detection, got err=%v media=%+v", err, pngRes.Media)
	}
	jpgRes, err := extractHostActionResult("hoxi_browser", http.StatusOK, []byte(`{"tabID":"t1","image":"/9j/4AAQSkZJRgABAQ=="}`))
	if err != nil || len(jpgRes.Media) != 1 || jpgRes.Media[0].MimeType != "image/jpeg" || jpgRes.Media[0].Filename != "hoxi_browser.jpg" {
		t.Fatalf("expected JPEG screenshot media detection, got err=%v media=%+v", err, jpgRes.Media)
	}
}
