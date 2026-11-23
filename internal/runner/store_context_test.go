package runner

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/llm"
)

func TestSessionStoreContextInjectionAndNativeCLIDedup(t *testing.T) {
	tmp := t.TempDir()
	svc := NewService(tmp, nil, nil, filepath.Join(tmp, "runner-sessions.json"))
	t.Cleanup(svc.CloseAll)

	currentBlock := "<hoxi_work_store_profile>\nBound Store: AnkerHome US (ID: amz-us-main, Platform: amazon, Marketplace: US, Currency: USD)\n</hoxi_work_store_profile>"
	svc.SetSessionContextResolver(func(sessionID, _ string) string {
		if sessionID == "ses_bound_1" {
			return "  " + currentBlock + "\n"
		}
		return ""
	})

	if got := svc.resolveSessionContextBlock("ses_unbound", tmp); got != "" {
		t.Fatalf("expected empty context block for unbound session, got %q", got)
	}
	resolved := svc.resolveSessionContextBlock("ses_bound_1", tmp)
	if resolved != currentBlock {
		t.Fatalf("unexpected resolved context block: %q", resolved)
	}

	buildTurnSystems := func(userSystem string) (callerSys, fullSys string) {
		callerSys = strings.TrimSpace(userSystem)
		if storeCtx := svc.resolveSessionContextBlock("ses_bound_1", tmp); storeCtx != "" && !strings.Contains(callerSys, "<hoxi_work_store_profile>") {
			if callerSys == "" {
				callerSys = storeCtx
			} else {
				callerSys = storeCtx + "\n\n" + callerSys
			}
		}
		fullSys = strings.TrimSpace(commandExecutionGuidance + "\n\n" + callerSys)
		return callerSys, fullSys
	}

	rec := SessionRecord{
		ID:         "ses_bound_1",
		Runtime:    string(RuntimeClaudeCode),
		ProjectDir: tmp,
	}

	// Turn 1 (fresh CLI conversation): delivers commandExecutionGuidance + store profile + user system.
	caller1, full1 := buildTurnSystems("Follow brand voice.")
	sys1 := svc.nativeCLISystemForTurn(rec, caller1, full1)
	if !strings.Contains(sys1, "hoxi code-index") || !strings.Contains(sys1, "amz-us-main") || !strings.Contains(sys1, "Follow brand voice.") {
		t.Fatalf("expected Turn 1 system to include execution guidance, store profile, and caller system, got:\n%s", sys1)
	}
	svc.recordCLISystemDelivered(rec.ID, rec.Runtime, caller1)

	// Turn 2 (resumed CLI conversation with unchanged store binding): deduplicated to empty string.
	rec.ExternalID = "cld_conv_1"
	caller2, full2 := buildTurnSystems("Follow brand voice.")
	if sys2 := svc.nativeCLISystemForTurn(rec, caller2, full2); sys2 != "" {
		t.Fatalf("expected unchanged resumed Turn 2 system to be deduplicated, got:\n%s", sys2)
	}

	// Turn 3 (resumed CLI conversation after store switch / updated economics): re-delivers only the changed store profile.
	currentBlock = "<hoxi_work_store_profile>\nBound Store: AnkerHome UK (ID: amz-uk-2, Platform: amazon, Marketplace: UK, Currency: GBP)\n</hoxi_work_store_profile>"
	caller3, full3 := buildTurnSystems("Follow brand voice.")
	sys3 := svc.nativeCLISystemForTurn(rec, caller3, full3)
	if !strings.Contains(sys3, "amz-uk-2") {
		t.Fatalf("expected updated store profile on Turn 3 after binding change, got:\n%s", sys3)
	}
	if strings.Contains(sys3, "Follow brand voice.") {
		t.Fatalf("expected unchanged caller block to be omitted on Turn 3, got:\n%s", sys3)
	}
}

func TestNativeCLISystemSectionDedupPersistsAcrossRestartAndHotReload(t *testing.T) {
	storeDir := filepath.Join(t.TempDir(), "sessions")
	wsDir := t.TempDir()
	svc := NewService(wsDir, nil, nil, storeDir)
	t.Cleanup(svc.CloseAll)

	rec := SessionRecord{
		ID:           "ses_appdev_cli",
		Runtime:      string(RuntimeClaudeCode),
		ProjectDir:   wsDir,
		WorktreePath: wsDir,
		SandboxMode:  "local",
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatalf("store.Put: %v", err)
	}

	appDevBlock := "<hoxi_work_app_dev_mode>\n### 1. Rules\nLine A\n\n### 2. Schema\nLine B\n</hoxi_work_app_dev_mode>"
	wsBlock := "Workspace context for this session:\n- Primary folder: " + wsDir
	storeV1 := "<hoxi_work_store_profile>\nStore: V1\n</hoxi_work_store_profile>"
	storeV2 := "<hoxi_work_store_profile>\nStore: V2\n</hoxi_work_store_profile>"

	caller1 := strings.Join([]string{appDevBlock, storeV1, wsBlock}, "\n\n")
	full1 := strings.TrimSpace(commandExecutionGuidance + "\n\n" + caller1)
	if got := svc.nativeCLISystemForTurn(rec, caller1, full1); got != full1 {
		t.Fatalf("Turn 1 should deliver fullSystem")
	}
	svc.recordCLISystemDeliveredForTurn(rec, caller1)

	// Simulate ExternalID captured on Turn 1 and persisted.
	if _, _, err := svc.store.UpdateMetadata(rec.ID, func(sm *SessionMetadata) {
		sm.ExternalID = "cld_thread_42"
	}); err != nil {
		t.Fatalf("UpdateMetadata: %v", err)
	}

	// Simulate Hoxi restart: brand-new Service instance with empty in-memory maps.
	restarted := NewService(wsDir, nil, nil, storeDir)
	t.Cleanup(restarted.CloseAll)
	reloaded, ok := restarted.store.Get(rec.ID)
	if !ok {
		t.Fatalf("expected session reloaded after restart")
	}

	// Turn 2 after restart with identical callerSystem must NOT re-inject fullSystem or appDevBlock.
	if got := restarted.nativeCLISystemForTurn(reloaded, caller1, full1); got != "" {
		t.Fatalf("expected empty system after restart for unchanged callerSystem, got:\n%s", got)
	}

	// Turn 3 after dynamic store block changes must ONLY deliver storeV2, never re-sending appDevBlock or wsBlock.
	caller3 := strings.Join([]string{appDevBlock, storeV2, wsBlock}, "\n\n")
	full3 := strings.TrimSpace(commandExecutionGuidance + "\n\n" + caller3)
	got3 := restarted.nativeCLISystemForTurn(reloaded, caller3, full3)
	if got3 != storeV2 {
		t.Fatalf("expected only updated store block on Turn 3, got:\n%s", got3)
	}
	restarted.recordCLISystemDeliveredForTurn(reloaded, caller3)
	reloaded, _ = restarted.store.Get(rec.ID)

	// Turn 4 toggling mode away (build) and back (app-dev) must not re-inject appDevBlock.
	callerBuild := strings.Join([]string{storeV2, wsBlock}, "\n\n")
	if got := restarted.nativeCLISystemForTurn(reloaded, callerBuild, commandExecutionGuidance+"\n\n"+callerBuild); got != "" {
		t.Fatalf("expected empty system when toggling to build, got:\n%s", got)
	}
	restarted.recordCLISystemDeliveredForTurn(reloaded, callerBuild)
	reloaded, _ = restarted.store.Get(rec.ID)
	if got := restarted.nativeCLISystemForTurn(reloaded, caller3, full3); got != "" {
		t.Fatalf("expected appDevBlock not to be re-injected when toggling back to app-dev, got:\n%s", got)
	}

	// Turn 5 switching active store BACK from V2 -> V1 must re-deliver storeV1
	// (replacing stale active tag state) while still omitting appDevBlock and wsBlock.
	if got := restarted.nativeCLISystemForTurn(reloaded, caller1, full1); got != storeV1 {
		t.Fatalf("expected storeV1 re-delivered when switching store V2 -> V1, got:\n%s", got)
	}
}

func TestAppDevModeOverridesAvoidOpenCodeDoubleInjectionAndStripPackagedAppContext(t *testing.T) {
	const testMode = "app-dev"
	const devPrompt = "<hoxi_work_app_dev_mode>\nBuild self-contained Work Apps.\n</hoxi_work_app_dev_mode>"
	llm.RegisterBuiltinAgent(llm.AgentSpec{
		Name:        testMode,
		Description: "App Dev test mode",
		Mode:        "primary",
		Prompt:      devPrompt,
	})
	t.Cleanup(func() { llm.UnregisterBuiltinAgent(testMode) })

	wsDir := t.TempDir()
	packagedCtx := "<hoxi_work_app>\nActive Work App: Demo\nDo NOT browse the app package directory.\n</hoxi_work_app>\n\n<hoxi_work_store_profile>\nStore: Demo\n</hoxi_work_store_profile>\n\nWorkspace context"

	// 1. OpenCode daemon already mounts built-in primary agents via OPENCODE_CONFIG_CONTENT:
	// must NOT re-inject devPrompt into callerSystem, and must strip <hoxi_work_app>.
	_, _, _, _, ocDaemonSys := resolveSlashCommandAndAgentOverridesForRuntime("Fix UI", wsDir, testMode, "openai", "gpt-5", packagedCtx, string(RuntimeOpenCode), true)
	if strings.Contains(ocDaemonSys, "<hoxi_work_app_dev_mode>") {
		t.Fatalf("OpenCode daemon must not double-inject built-in app-dev prompt into system, got:\n%s", ocDaemonSys)
	}
	if strings.Contains(ocDaemonSys, "<hoxi_work_app>") {
		t.Fatalf("app-dev mode must strip conflicting <hoxi_work_app> block, got:\n%s", ocDaemonSys)
	}
	if !strings.Contains(ocDaemonSys, "<hoxi_work_store_profile>") || !strings.Contains(ocDaemonSys, "Workspace context") {
		t.Fatalf("expected non-conflicting context preserved, got:\n%s", ocDaemonSys)
	}

	// 2. OpenCode CLI (daemon unavailable) only passes --agent build|plan, so it STILL receives devPrompt once.
	_, _, _, _, ocCLISys := resolveSlashCommandAndAgentOverridesForRuntime("Fix UI", wsDir, testMode, "openai", "gpt-5", packagedCtx, string(RuntimeOpenCode), false)
	if strings.Count(ocCLISys, "<hoxi_work_app_dev_mode>") != 1 || strings.Contains(ocCLISys, "<hoxi_work_app>") {
		t.Fatalf("OpenCode CLI should inject devPrompt once and strip <hoxi_work_app>, got:\n%s", ocCLISys)
	}

	// 3. External CLI (Claude Code) injects devPrompt once and strips <hoxi_work_app>.
	_, _, _, _, claudeSys := resolveSlashCommandAndAgentOverridesForRuntime("Fix UI", wsDir, testMode, "claude-code", "claude-sonnet-4-6", packagedCtx, string(RuntimeClaudeCode), false)
	if strings.Count(claudeSys, "<hoxi_work_app_dev_mode>") != 1 || strings.Contains(claudeSys, "<hoxi_work_app>") {
		t.Fatalf("Claude CLI should inject devPrompt once and strip <hoxi_work_app>, got:\n%s", claudeSys)
	}

	// 4. Native runtime skips prompt injection here (Engine.RunTurn injects it) and still strips <hoxi_work_app>.
	_, _, _, _, nativeSys := resolveSlashCommandAndAgentOverridesForRuntime("Fix UI", wsDir, testMode, "openai", "gpt-5", packagedCtx, string(RuntimeNative), false)
	if strings.Contains(nativeSys, "<hoxi_work_app_dev_mode>") || strings.Contains(nativeSys, "<hoxi_work_app>") {
		t.Fatalf("Native runner override should leave devPrompt to Engine.RunTurn and strip <hoxi_work_app>, got:\n%s", nativeSys)
	}
}
