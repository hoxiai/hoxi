package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	PlanConsentApproveAndBuild = "Approve & Build"
	PlanConsentKeepPlanOnly    = "Keep in Plan Mode"
)

type planConsentTool struct{}

func (t *planConsentTool) Name() string     { return "plan_consent" }
func (t *planConsentTool) IsReadOnly() bool { return false }

func (t *planConsentTool) Description() string {
	return "Present a structured implementation plan in Plan mode so the user can review steps, target files, and risks, and either click 'Approve & Build' to unlock Build mode in the same turn or request revisions."
}

func (t *planConsentTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title": map[string]any{
				"type":        "string",
				"description": "Concise plan title (max 80 chars)",
			},
			"summary": map[string]any{
				"type":        "string",
				"description": "Architectural approach and goal summary",
			},
			"steps": map[string]any{
				"type":        "array",
				"description": "Ordered implementation and verification steps",
				"items":       map[string]any{"type": "string"},
			},
			"files": map[string]any{
				"type":        "array",
				"description": "Workspace files expected to be created or modified",
				"items":       map[string]any{"type": "string"},
			},
			"risks": map[string]any{
				"type":        "array",
				"description": "Key edge cases, invariants, or blast-radius considerations",
				"items":       map[string]any{"type": "string"},
			},
		},
		"required": []string{"title", "summary", "steps"},
	}
}

func normalizeTrimmedSlice(items []string, limit int) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, min(len(items), limit))
	for _, it := range items {
		trimmed := strings.TrimSpace(it)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func extractPlanConsentPicks(meta map[string]any, output string) []string {
	var picks []string
	if meta != nil {
		switch raw := meta["answers"].(type) {
		case [][]string:
			for _, grp := range raw {
				for _, s := range grp {
					if trimmed := strings.TrimSpace(s); trimmed != "" {
						picks = append(picks, trimmed)
					}
				}
			}
		case []any:
			for _, grp := range raw {
				if arr, ok := grp.([]any); ok {
					for _, v := range arr {
						if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
							picks = append(picks, strings.TrimSpace(s))
						}
					}
				} else if s, ok := grp.(string); ok && strings.TrimSpace(s) != "" {
					picks = append(picks, strings.TrimSpace(s))
				}
			}
		}
	}
	if len(picks) == 0 && strings.TrimSpace(output) != "" {
		if strings.Contains(output, PlanConsentApproveAndBuild) {
			picks = append(picks, PlanConsentApproveAndBuild)
		} else if strings.Contains(output, PlanConsentKeepPlanOnly) {
			picks = append(picks, PlanConsentKeepPlanOnly)
		}
	}
	return picks
}

func (t *planConsentTool) Execute(ctx context.Context, tc ToolContext, input json.RawMessage) (ToolOutput, error) {
	if tc.SubtaskDepth > 0 {
		return ToolOutput{Title: "plan_consent", IsError: true}, fmt.Errorf("subagents cannot request plan consent")
	}
	var args struct {
		Title   string   `json:"title"`
		Summary string   `json:"summary"`
		Steps   []string `json:"steps"`
		Files   []string `json:"files"`
		Risks   []string `json:"risks"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return ToolOutput{Title: "plan_consent", IsError: true}, err
	}
	title := strings.TrimSpace(args.Title)
	summary := strings.TrimSpace(args.Summary)
	steps := normalizeTrimmedSlice(args.Steps, 16)
	files := normalizeTrimmedSlice(args.Files, 24)
	risks := normalizeTrimmedSlice(args.Risks, 12)
	if title == "" {
		return ToolOutput{Title: "plan_consent", IsError: true}, fmt.Errorf("title is required")
	}
	if len(steps) == 0 {
		return ToolOutput{Title: "plan_consent", IsError: true}, fmt.Errorf("at least one step is required")
	}
	if tc.AskQuestion == nil {
		return ToolOutput{Title: "plan_consent", IsError: true}, fmt.Errorf("interactive consent handler is not available in this session")
	}

	planMeta := map[string]any{
		"title":   title,
		"summary": summary,
		"steps":   steps,
	}
	if len(files) > 0 {
		planMeta["files"] = files
	}
	if len(risks) > 0 {
		planMeta["risks"] = risks
	}

	questionPrompt := title
	if summary != "" {
		questionPrompt = title + " — " + summary
	}

	payload := map[string]any{
		"plan": planMeta,
		"questions": []map[string]any{
			{
				"header":   "Plan Consent",
				"question": questionPrompt,
				"multiple": false,
				"custom":   true,
				"options": []map[string]any{
					{
						"label":       PlanConsentApproveAndBuild,
						"description": "Promote session from Plan to Build mode and execute the approved steps immediately",
					},
					{
						"label":       PlanConsentKeepPlanOnly,
						"description": "Approve the plan direction while keeping the session in read-only Plan mode",
					},
				},
			},
		},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ToolOutput{Title: title, IsError: true}, err
	}

	out, meta, err := tc.AskQuestion(ctx, tc.CallID, string(encoded))
	if meta == nil {
		meta = map[string]any{}
	}
	meta["plan"] = planMeta
	if err != nil {
		return ToolOutput{
			Title:    title,
			Output:   err.Error(),
			Metadata: meta,
			IsError:  true,
		}, err
	}

	picks := extractPlanConsentPicks(meta, out)
	approvedBuild := false
	keepPlanOnly := false
	var customFeedback []string
	for _, pick := range picks {
		switch {
		case strings.EqualFold(pick, PlanConsentApproveAndBuild):
			approvedBuild = true
		case strings.EqualFold(pick, PlanConsentKeepPlanOnly):
			keepPlanOnly = true
		default:
			customFeedback = append(customFeedback, pick)
		}
	}

	var msg strings.Builder
	msg.WriteString(strings.TrimSpace(out))
	switch {
	case approvedBuild && len(customFeedback) == 0:
		meta["consent"] = "approved_build"
		meta["promoteMode"] = "build"
		msg.WriteString("\n\n[Plan Consent Approved -> Mode Promoted to Build]: Mutating tools (edit, multiedit, write, apply_patch, bash) are now unlocked in this turn. Execute the approved plan steps directly and run verification.")
	case approvedBuild && len(customFeedback) > 0:
		meta["consent"] = "approved_build"
		meta["promoteMode"] = "build"
		msg.WriteString("\n\n[Plan Consent Approved with Notes -> Mode Promoted to Build]: Incorporate the user's notes (")
		msg.WriteString(strings.Join(customFeedback, "; "))
		msg.WriteString(") and execute the plan directly in Build mode.")
	case keepPlanOnly:
		meta["consent"] = "plan_only"
		msg.WriteString("\n\n[Plan Approved — Stay in Plan Mode]: The user approved the plan direction while keeping this session in read-only Plan mode. Summarize the finalized plan cleanly without mutating files.")
	default:
		meta["consent"] = "revise"
		msg.WriteString("\n\n[Plan Revision Requested]: Stay in read-only Plan mode, address the user's feedback, and call plan_consent again once the updated plan is ready.")
	}

	return ToolOutput{
		Title:    title,
		Output:   strings.TrimSpace(msg.String()),
		Metadata: meta,
	}, nil
}
