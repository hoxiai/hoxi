package agent

import (
	"strings"
	"testing"
)

func TestPackagedAppSessionGuidanceAndToolFiltering(t *testing.T) {
	reg := newToolRegistry()
	packagedAppSystem := "<hoxi_work_app>\nActive Work App: Sample Studio (ID: sample-studio)\n</hoxi_work_app>"

	// 1. Standard coding session keeps coding guidance and hoxi_code_index.
	codePrompt, _ := buildSystemPromptWithRules("/tmp/ws", "", "", "build", 0, nil)
	if !strings.Contains(codePrompt, "Prefer hoxi_code_index") {
		t.Fatalf("expected standard coding session to retain hoxi_code_index guidance")
	}
	codeCatalog := newHostToolCatalog(nil, nil)
	codeCatalog.configureAppScope("")
	if !hasToolSpec(codeCatalog.specs(reg, "build", 0), "hoxi_code_index") {
		t.Fatalf("expected standard coding session to expose hoxi_code_index")
	}

	// 2. Packaged app session switches to method-first + question guidance and hides hoxi_code_index.
	appPrompt, _ := buildSystemPromptWithRules("/tmp/ws", packagedAppSystem, "", "build", 0, nil)
	if strings.Contains(appPrompt, "Prefer hoxi_code_index") {
		t.Fatalf("packaged app session should not inject coding hoxi_code_index guidance")
	}
	for _, want := range []string{
		"Operate as an application workflow assistant",
		"Do NOT browse, glob, grep, or read the app package's internal source files",
		"call the question tool to present structured choices",
	} {
		if !strings.Contains(appPrompt, want) {
			t.Fatalf("expected packaged app system prompt to contain %q, got:\n%s", want, appPrompt)
		}
	}

	appCatalog := newHostToolCatalog(nil, nil)
	appCatalog.configureAppScope(packagedAppSystem)
	specs := appCatalog.specs(reg, "build", 0)
	if hasToolSpec(specs, "hoxi_code_index") {
		t.Fatalf("expected packaged app session to omit hoxi_code_index from tool specs")
	}
	if !hasToolSpec(specs, "question") {
		t.Fatalf("expected packaged app session to retain interactive question tool")
	}
	if !hasToolSpec(specs, "mcp_call_tool") || !hasToolSpec(specs, "mcp_list_tools") {
		t.Fatalf("expected packaged app session to retain MCP tools when not omitted")
	}

	// 3. App Dev mode (<hoxi_work_app_dev_mode>) must override packaged-app
	// restrictions even if <hoxi_work_app> is present, preserving coding guidance
	// and hoxi_code_index.
	appDevSystem := "<hoxi_work_app_dev_mode>\nBuild Hoxi Work App Bundles.\n</hoxi_work_app_dev_mode>\n\n" + packagedAppSystem
	devPrompt, _ := buildSystemPromptWithRules("/tmp/ws", appDevSystem, "", "app-dev", 0, nil)
	if !strings.Contains(devPrompt, "Prefer hoxi_code_index") {
		t.Fatalf("expected app-dev session to retain coding hoxi_code_index guidance, got:\n%s", devPrompt)
	}
	if strings.Contains(devPrompt, "Do NOT browse, glob, grep, or read the app package's internal source files") {
		t.Fatalf("app-dev session must not inject packaged-app no-source-diving guidance")
	}
	devCatalog := newHostToolCatalog(nil, nil)
	devCatalog.configureAppScope(appDevSystem)
	if !hasToolSpec(devCatalog.specs(reg, "app-dev", 0), "hoxi_code_index") {
		t.Fatalf("expected app-dev session to expose hoxi_code_index")
	}
}
