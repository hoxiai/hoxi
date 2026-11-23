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
	return "Delegate broad research or a cohesive multi-step subtask to an isolated in-process subagent ('executor', 'explore', 'scout', 'general', or custom ID). Skip for simple questions, 1–3 known files, or small edits. Read-only subagents run concurrently; write-capable ones run serially in the shared worktree."
}
func (t *subtaskTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"subagent_type": map[string]any{
				"type":        "string",
				"description": "Profile: 'executor', 'explore' (default), 'scout', 'general', or custom subagent ID",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "Short 3-6 word label",
			},
			"prompt": map[string]any{
				"type":        "string",
				"description": "Self-contained instructions and target paths/symbols",
			},
			"model": map[string]any{
				"type":        "string",
				"description": "Optional override ('providerID/modelID', 'modelID', or 'economy'/'fast')",
			},
			"resume": map[string]any{
				"type":        "string",
				"description": "Optional subagent_id from an earlier task result to resume",
			},
			"fresh": map[string]any{
				"type":        "boolean",
				"description": "Set true to start a fresh executor thread",
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
		Resume       string `json:"resume"`
		Fresh        bool   `json:"fresh"`
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
		ResumeID:     strings.TrimSpace(args.Resume),
		Fresh:        args.Fresh,
	})
	resolvedType := strings.TrimSpace(res.SubagentType)
	if resolvedType == "" {
		resolvedType = subType
	}
	meta := map[string]any{
		"subagent":          true,
		"subagent_type":     resolvedType,
		"description":       desc,
		"readOnly":          res.ReadOnly,
		"mutated":           res.Mutated,
		"completedMutation": res.CompletedMutation,
	}
	if res.SubtaskID != "" {
		meta["subtaskID"] = res.SubtaskID
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
	outText := res.Summary
	if res.SubtaskID != "" {
		outText = strings.TrimRight(outText, "\n") + "\n\n[subagent_id: " + res.SubtaskID + "]"
	}
	return ToolOutput{
		Title:    desc,
		Output:   outText,
		Metadata: meta,
	}, nil
}
