package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type questionTool struct{}

func (t *questionTool) Name() string     { return "question" }
func (t *questionTool) IsReadOnly() bool { return false }
func (t *questionTool) Description() string {
	return "Ask the user 1 to 4 structured multiple-choice or custom-answer clarifying questions when requirements are genuinely ambiguous."
}
func (t *questionTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"questions": map[string]any{
				"type":        "array",
				"description": "Questions to display in the interactive Question Dock",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"header":   map[string]any{"type": "string", "description": "Short tab/chip label (max 24 chars)"},
						"question": map[string]any{"type": "string", "description": "Full question text"},
						"multiple": map[string]any{"type": "boolean", "description": "Allow selecting multiple options"},
						"custom":   map[string]any{"type": "boolean", "description": "Allow typing a custom answer (default true)"},
						"options": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"label":       map[string]any{"type": "string"},
									"description": map[string]any{"type": "string"},
								},
								"required": []string{"label"},
							},
						},
					},
					"required": []string{"question", "options"},
				},
			},
		},
		"required": []string{"questions"},
	}
}

func (t *questionTool) Execute(ctx context.Context, tc ToolContext, input json.RawMessage) (ToolOutput, error) {
	if tc.SubtaskDepth > 0 {
		return ToolOutput{Title: "question", IsError: true}, fmt.Errorf("subagents cannot ask interactive questions")
	}
	var args struct {
		Questions []map[string]any `json:"questions"`
		Question  string           `json:"question"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return ToolOutput{Title: "question", IsError: true}, err
	}
	if len(args.Questions) == 0 && strings.TrimSpace(args.Question) == "" {
		return ToolOutput{Title: "question", IsError: true}, fmt.Errorf("questions array is required")
	}
	if tc.AskQuestion == nil {
		return ToolOutput{Title: "question", IsError: true}, fmt.Errorf("interactive question handler is not available in this session")
	}
	out, meta, err := tc.AskQuestion(ctx, tc.CallID, string(input))
	if err != nil {
		return ToolOutput{
			Title:    "question",
			Output:   err.Error(),
			Metadata: meta,
			IsError:  true,
		}, err
	}
	return ToolOutput{
		Title:    "question",
		Output:   out,
		Metadata: meta,
	}, nil
}
