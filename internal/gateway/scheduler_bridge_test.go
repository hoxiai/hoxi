package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/agent"
	"github.com/hoxiai/hoxi/internal/runner"
	"github.com/hoxiai/hoxi/internal/scheduler"
)

func TestInvokeNativeScheduleToolCreateBindAndRecursionBlock(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOXI_HOME", home)
	rootDir := t.TempDir()

	g := New(0, "test", rootDir, nil, nil)
	defer g.CloseBrowser()

	// Create a normal interactive session with explicit model.
	sess, status, err := g.runner.CreateSession(runner.CreateSessionRequest{
		Title:       "Interactive Chat",
		Directory:   rootDir,
		Runtime:     string(runner.RuntimeNative),
		ProviderID:  "anthropic",
		ModelID:     "claude-sonnet-4-5",
		SandboxMode: "local",
	}, rootDir)
	if err != nil || status != 200 {
		t.Fatalf("CreateSession failed: status=%d err=%v", status, err)
	}

	// Create a schedule bound to the current session via hoxi_schedule.
	res, err := g.invokeNativeScheduleTool(context.Background(), agent.ToolContext{
		SessionID:   sess.ID,
		WorktreeDir: rootDir,
	}, json.RawMessage(`{
		"action": "create",
		"name": "Daily Repo Check",
		"cron": "0 9 * * 1-5",
		"bindCurrentSession": true,
		"prompt": "Inspect open PR failures and report summary."
	}`))
	if err != nil {
		t.Fatalf("hoxi_schedule create failed: %v", err)
	}
	if !strings.Contains(res.Output, sess.ID) {
		t.Fatalf("expected created schedule bound to %s, got: %s", sess.ID, res.Output)
	}

	// Get schedule detail and run history via hoxi_schedule action=get.
	var createdSch scheduler.Schedule
	if err := json.Unmarshal([]byte(res.Output), &createdSch); err != nil {
		t.Fatalf("unmarshal created schedule: %v", err)
	}
	getRes, err := g.invokeNativeScheduleTool(context.Background(), agent.ToolContext{
		SessionID:   sess.ID,
		WorktreeDir: rootDir,
	}, json.RawMessage(`{"action": "get", "id": "`+createdSch.ID+`"}`))
	if err != nil {
		t.Fatalf("hoxi_schedule get failed: %v", err)
	}
	if !strings.Contains(getRes.Output, createdSch.ID) || !strings.Contains(getRes.Output, "Daily Repo Check") {
		t.Fatalf("expected schedule details from get, got: %s", getRes.Output)
	}

	// List schedules in this workspace.
	listRes, err := g.invokeNativeScheduleTool(context.Background(), agent.ToolContext{
		SessionID:   sess.ID,
		WorktreeDir: rootDir,
	}, json.RawMessage(`{"action": "list"}`))
	if err != nil {
		t.Fatalf("hoxi_schedule list failed: %v", err)
	}
	if !strings.Contains(listRes.Output, "Daily Repo Check") {
		t.Fatalf("expected Daily Repo Check in list, got: %s", listRes.Output)
	}

	// A session spawned by a schedule MUST NOT be allowed to create/modify schedules (recursion block).
	schedSess, status, err := g.runner.CreateSession(runner.CreateSessionRequest{
		Title:       "[Schedule] Patrol",
		Directory:   rootDir,
		ScheduleID:  "sch_parent123",
		Runtime:     string(runner.RuntimeNative),
		ProviderID:  "anthropic",
		ModelID:     "claude-sonnet-4-5",
		SandboxMode: "local",
	}, rootDir)
	if err != nil || status != 200 {
		t.Fatalf("CreateSession for scheduled run failed: status=%d err=%v", status, err)
	}
	_, err = g.invokeNativeScheduleTool(context.Background(), agent.ToolContext{
		SessionID:   schedSess.ID,
		WorktreeDir: rootDir,
	}, json.RawMessage(`{
		"action": "create",
		"name": "Recursive Schedule",
		"intervalMinutes": 10,
		"prompt": "Should fail"
	}`))
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "cannot create") {
		t.Fatalf("expected scheduled session mutation to be denied, got err=%v", err)
	}
}

func TestInvokeNativeScheduleToolResolvesSkillKeyAndRejectsUnknown(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOXI_HOME", home)
	rootDir := t.TempDir()

	// Create a valid project skill under <rootDir>/.hoxi/skills/patrol-check/SKILL.md.
	skillDir := filepath.Join(rootDir, ".hoxi", "skills", "patrol-check")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillContent := "---\nname: patrol-check\ndescription: Patrol repo checks\n---\nCheck CI and PR status.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0o644); err != nil {
		t.Fatal(err)
	}

	g := New(0, "test", rootDir, nil, nil)
	defer g.CloseBrowser()

	sess, status, err := g.runner.CreateSession(runner.CreateSessionRequest{
		Title:       "Skill Schedule Chat",
		Directory:   rootDir,
		Runtime:     string(runner.RuntimeNative),
		ProviderID:  "anthropic",
		ModelID:     "claude-sonnet-4-5",
		SandboxMode: "local",
	}, rootDir)
	if err != nil || status != 200 {
		t.Fatalf("CreateSession failed: status=%d err=%v", status, err)
	}

	// Passing only skillName resolves canonical skl_ key automatically.
	res, err := g.invokeNativeScheduleTool(context.Background(), agent.ToolContext{
		SessionID:   sess.ID,
		WorktreeDir: rootDir,
	}, json.RawMessage(`{
		"action": "create",
		"name": "Patrol With Skill",
		"intervalMinutes": 30,
		"skillName": "patrol-check",
		"prompt": "Run patrol check"
	}`))
	if err != nil {
		t.Fatalf("hoxi_schedule create with skillName failed: %v", err)
	}
	var created scheduler.Schedule
	if err := json.Unmarshal([]byte(res.Output), &created); err != nil {
		t.Fatalf("unmarshal created schedule: %v", err)
	}
	if created.SkillSelection == nil || !strings.HasPrefix(created.SkillSelection.Key, "skl_") || created.SkillSelection.Name != "patrol-check" {
		t.Fatalf("expected canonical skl_ key for patrol-check, got %+v", created.SkillSelection)
	}

	// Unknown skill name is rejected cleanly at creation time.
	_, err = g.invokeNativeScheduleTool(context.Background(), agent.ToolContext{
		SessionID:   sess.ID,
		WorktreeDir: rootDir,
	}, json.RawMessage(`{
		"action": "create",
		"name": "Bad Skill",
		"intervalMinutes": 30,
		"skillName": "does-not-exist",
		"prompt": "Run patrol check"
	}`))
	if err == nil {
		t.Fatal("expected unknown skill to fail schedule creation")
	}
}

func TestScheduleRoutesRequireGatewayToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOXI_HOME", home)
	rootDir := t.TempDir()

	g := New(0, "test", rootDir, nil, nil)
	defer g.CloseBrowser()
	const tok = "schedule-boot-token-123"
	g.SetGatewayToken(tok)

	// Missing token is rejected with 403.
	noTokReq := localRequest(http.MethodGet, "/api/hoxi/schedules", nil)
	noTokRec := httptest.NewRecorder()
	g.ServeHTTP(noTokRec, noTokReq)
	if noTokRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without gateway token, got %d: %s", noTokRec.Code, noTokRec.Body.String())
	}

	// Valid X-Hoxi-Token succeeds with 200.
	withTokReq := localRequest(http.MethodGet, "/api/hoxi/schedules", nil)
	withTokReq.Header.Set(gatewayTokenHeader, tok)
	withTokRec := httptest.NewRecorder()
	g.ServeHTTP(withTokRec, withTokReq)
	if withTokRec.Code != http.StatusOK {
		t.Fatalf("expected 200 with gateway token, got %d: %s", withTokRec.Code, withTokRec.Body.String())
	}
}
