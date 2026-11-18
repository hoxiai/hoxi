package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type subtaskTool struct{}

func (t *subtaskTool) Name() string     { return "task" }
func (t *subtaskTool) IsReadOnly() bool { return true }
func (t *subtaskTool) Description() string {
	return "Delegate an independent research, exploration, or multi-step subtask to an isolated in-process subagent (subagent_type: 'explore' for fast read-only codebase/web research, 'scout' for quick symbol/file location, 'general' for full-capability execution, or a custom subagent). Read-only task calls run concurrently."
}
func (t *subtaskTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"subagent_type": map[string]any{
				"type":        "string",
				"description": "Subagent profile to invoke: 'explore' (default, read-only), 'scout' (read-only locator), 'general' (full tool access), or a configured custom subagent ID",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "Short 3-6 word label for this subtask",
			},
			"prompt": map[string]any{
				"type":        "string",
				"description": "Self-contained instructions and target directories/symbols to investigate or execute",
			},
			"model": map[string]any{
				"type":        "string",
				"description": "Optional model override ('providerID/modelID', same-provider 'modelID', or explicit 'economy'/'fast'); otherwise uses the configured subagent role model or inherits the parent model without automatic downgrading",
			},
		},
		"required": []string{"description", "prompt"},
	}
}

func (t *subtaskTool) Execute(ctx context.Context, tc ToolContext, input json.RawMessage) (ToolOutput, error) {
	var args struct {
		SubagentType string `json:"subagent_type"`
		Agent        string `json:"agent"`
		Description  string `json:"description"`
		Prompt       string `json:"prompt"`
		Model        string `json:"model"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return ToolOutput{Title: "task", IsError: true}, err
	}
	subType := strings.TrimSpace(args.SubagentType)
	if subType == "" {
		subType = strings.TrimSpace(args.Agent)
	}
	if subType == "" {
		subType = "explore"
	}
	desc := strings.TrimSpace(args.Description)
	prompt := strings.TrimSpace(args.Prompt)
	if prompt == "" {
		return ToolOutput{Title: "task", IsError: true}, fmt.Errorf("prompt is required")
	}
	if desc == "" {
		desc = subType + " subtask"
	}
	if tc.SubtaskDepth > 0 || tc.RunSubtask == nil {
		return ToolOutput{Title: desc, IsError: true}, fmt.Errorf("nested subagent tasks are disabled")
	}
	res, err := tc.RunSubtask(ctx, SubtaskRequest{
		ParentCallID: tc.CallID,
		SubagentType: subType,
		Description:  desc,
		Prompt:       prompt,
		Model:        strings.TrimSpace(args.Model),
	})
	resolvedType := strings.TrimSpace(res.SubagentType)
	if resolvedType == "" {
		resolvedType = subType
	}
	meta := map[string]any{
		"subagent":      true,
		"subagent_type": resolvedType,
		"description":   desc,
		"readOnly":      res.ReadOnly,
		"mutated":       res.Mutated,
	}
	if res.ProviderID != "" {
		meta["providerID"] = res.ProviderID
	}
	if res.ModelID != "" {
		meta["modelID"] = res.ModelID
	}
	if res.ProviderID != "" && res.ModelID != "" {
		meta["model"] = res.ProviderID + "/" + res.ModelID
	}
	if res.Routing != "" {
		meta["routing"] = res.Routing
	}
	if err != nil {
		return ToolOutput{Title: desc, Output: res.Summary, Metadata: meta, IsError: true}, err
	}
	return ToolOutput{
		Title:    desc,
		Output:   res.Summary,
		Metadata: meta,
	}, nil
}
