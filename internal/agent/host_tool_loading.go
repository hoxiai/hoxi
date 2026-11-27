package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/hoxiai/hoxi/internal/llm"
)

var deferredHostTools = []string{
	"hoxi_browser",
	"hoxi_computer",
	"hoxi_data",
	"hoxi_image",
	"hoxi_schedule",
	"hoxi_speech",
	"hoxi_tools",
	"hoxi_video",
}

func isEagerHostToolLoading(cfg map[string]any) bool {
	mode, _ := cfg["hostToolLoading"].(string)
	return strings.TrimSpace(mode) == "eager"
}

type loadHostTool struct{ registry ToolRegistry }

func (*loadHostTool) Name() string     { return "load_host_tool" }
func (*loadHostTool) IsReadOnly() bool { return true }
func (*loadHostTool) Description() string {
	return "Load a browser, desktop-control, data-profile, scheduled-task, tools-gateway, image, speech, or video tool's full parameters and safety instructions. Then use call_host_tool."
}
func (*loadHostTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string", "enum": deferredHostTools}}, "required": []string{"name"}}
}
func (t *loadHostTool) Execute(_ context.Context, tc ToolContext, input json.RawMessage) (ToolOutput, error) {
	name := requestedHostTool(input)
	if name == "" || strings.EqualFold(tc.Mode, "plan") {
		return ToolOutput{}, fmt.Errorf("choose an available host tool in Build mode")
	}
	tool, ok := t.registry.Get(name)
	if !ok || (tc.SubtaskDepth > 0 && name == "hoxi_schedule") {
		return ToolOutput{}, fmt.Errorf("unavailable host tool %q", name)
	}
	params, err := json.Marshal(tool.Parameters())
	if err != nil {
		return ToolOutput{}, err
	}
	out := tool.Description() + "\ncall_host_tool name=" + name + ", args schema: " + string(params)
	switch name {
	case "hoxi_browser":
		out += "\n\n<hoxi_tool_safety>\n" + hoxiBrowserSafetyRule + "\n</hoxi_tool_safety>"
	case "hoxi_computer":
		out += "\n\n<hoxi_tool_safety>\n" + hoxiComputerSafetyRule + "\n</hoxi_tool_safety>"
	}
	return ToolOutput{Title: name, Output: out, Metadata: map[string]any{"loadedHostTool": name}}, nil
}
func requestedHostTool(input json.RawMessage) string {
	var args struct{ Name string }
	if json.Unmarshal(input, &args) != nil || !slices.Contains(deferredHostTools, args.Name) {
		return ""
	}
	return args.Name
}

type hostToolCatalog struct {
	onDemand                 bool
	omitMCP                  bool
	omitCodeIndex            bool
	omitInteractiveQuestions bool
	omitTask                 bool
	omitDirectExecution      bool
	omitGoal, omitSkill      bool
}

func newHostToolCatalog(cfg map[string]any) hostToolCatalog {
	return hostToolCatalog{onDemand: !isEagerHostToolLoading(cfg)}
}

func (c hostToolCatalog) specs(reg ToolRegistry, mode string, depth int) []ToolSpec {
	var all []ToolSpec
	if scoped, ok := reg.(turnScopedRegistry); ok {
		all = scoped.SpecsForTurn(mode, depth)
	} else {
		all = reg.SpecsForMode(mode)
	}
	out := make([]ToolSpec, 0, len(all))
	for _, spec := range all {
		if spec.Name == "load_host_tool" && (!c.onDemand || strings.EqualFold(mode, "plan")) {
			continue
		}
		if c.onDemand && slices.Contains(deferredHostTools, spec.Name) {
			continue
		}
		if c.omitMCP && (spec.Name == "mcp_list_tools" || spec.Name == "mcp_call_tool") {
			continue
		}
		if c.omitCodeIndex && spec.Name == "hoxi_code_index" {
			continue
		}
		if c.omitInteractiveQuestions && (spec.Name == "plan_consent" || spec.Name == "question") {
			continue
		}
		if c.omitTask && spec.Name == "task" {
			continue
		}
		if c.omitDirectExecution && isOrchestratorDirectExecutionTool(spec.Name) {
			continue
		}
		if (spec.Name == "goal_report" && c.omitGoal) || (spec.Name == "skill" && c.omitSkill) {
			continue
		}
		out = append(out, spec)
		if spec.Name == "load_host_tool" {
			out = append(out, ToolSpec{Name: "call_host_tool", Description: "Invoke a host tool using the args documented by load_host_tool.", Parameters: map[string]any{
				"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string", "enum": deferredHostTools}, "args": map[string]any{"type": "object"}}, "required": []string{"name", "args"},
			}})
		}
	}
	return out
}

func (c *hostToolCatalog) configureMCP(worktreeDir string, cfgDoc map[string]any, mcpCfg MCPConfigSource, history []Message) {
	c.omitMCP = !hasConfiguredMCPServers(worktreeDir, cfgDoc, mcpCfg) && !historyUsesMCPTools(history)
}

func (c *hostToolCatalog) configureAppScope(callerSystem string) {
	c.omitCodeIndex = isPackagedAppSession(callerSystem)
	c.omitInteractiveQuestions = isExternalCollabSession(callerSystem)
}

func (c *hostToolCatalog) configureTaskPolicy(primaryMode string, agentMap map[string]llm.AgentSpec, cfgDoc map[string]any) {
	c.omitTask = !llm.HasAnyAllowedSubagent(primaryMode, agentMap, cfgDoc)
}

func (c *hostToolCatalog) configureOrchestrator(active bool, effectiveMode string) {
	if !active {
		return
	}
	c.omitTask = false
	c.omitDirectExecution = !strings.EqualFold(strings.TrimSpace(effectiveMode), "plan")
}

func hasConfiguredMCPServers(worktreeDir string, cfgDoc map[string]any, mcpCfg MCPConfigSource) bool {
	servers, err := resolveMCPServers(ToolContext{
		WorktreeDir:   worktreeDir,
		MCPConfig:     mcpCfg,
		projectConfig: cfgDoc,
	})
	// Fail open if config failed to parse so the user/model can still see the config error via MCP tools.
	return err != nil || len(servers) > 0
}

func historyUsesMCPTools(history []Message) bool {
	for _, m := range history {
		for _, b := range m.Content {
			if b.Type == ContentToolUse && (b.Name == "mcp_list_tools" || b.Name == "mcp_call_tool") {
				return true
			}
		}
	}
	return false
}

// The wire call stays in history. Scheduling, permissions and recovery see the concrete tool.
func resolveHostProxy(call ToolCall) (ToolCall, bool) {
	if call.Name != "call_host_tool" {
		return call, false
	}
	var in struct {
		Name string
		Args json.RawMessage
	}
	if json.Unmarshal(call.Input, &in) != nil || !slices.Contains(deferredHostTools, in.Name) || len(in.Args) == 0 || in.Args[0] != '{' {
		return call, false
	}
	call.Name, call.Input = in.Name, in.Args
	return call, true
}
