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
	return "Delegate broad, open-ended research or a cohesive multi-step subtask to an isolated in-process subagent (subagent_type: 'explore' for broad read-only codebase/web research, 'scout' for multi-area symbol/file discovery across unfamiliar directories, 'general' for cohesive full-capability execution, or a custom subagent). Do NOT use task for simple questions, reading 1–3 known files, single symbol/grep lookups, or small focused edits—use hoxi_code_index, read, glob, grep, and edit tools directly in the main turn because subagents cold-start without the parent's loaded context. Read-only task calls ('explore', 'scout') run concurrently when genuinely independent (consolidate closely related checks rather than spawning many tiny subagents), whereas write-capable 'general' subagents run serially in the shared worktree and serialize calls placed after them in the same turn; never split edits into multiple 'general' calls expecting parallelism."
}
func (t *subtaskTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"subagent_type": map[string]any{
				"type":        "string",
				"description": "Subagent profile to invoke: 'explore' (default, read-only broad research), 'scout' (read-only multi-area locator), 'general' (cohesive full tool access), or a configured custom subagent ID",
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
			"resume": map[string]any{
				"type":        "string",
				"description": "Optional subagent_id from an earlier task result in this session to resume with its prior conversation context intact",
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
