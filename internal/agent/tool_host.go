package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

func execHostTool(ctx context.Context, tc ToolContext, name string, input json.RawMessage) (ToolOutput, error) {
	if tc.HostInvoke == nil {
		return ToolOutput{
			Title:   name,
			Output:  fmt.Sprintf("%s is not configured in this host environment", name),
			IsError: true,
		}, fmt.Errorf("%s is not configured", name)
	}
	res, err := tc.HostInvoke(ctx, tc, name, input)
	title := res.Title
	if title == "" {
		title = name
	}
	if err != nil {
		outText := res.Output
		if outText == "" {
			outText = err.Error()
		}
		return ToolOutput{
			Title:    title,
			Output:   outText,
			Metadata: res.Metadata,
			IsError:  true,
		}, err
	}
	return ToolOutput{
		Title:    title,
		Output:   res.Output,
		Metadata: res.Metadata,
		Media:    res.Media,
	}, nil
}

type hoxiBrowserTool struct{}

func (t *hoxiBrowserTool) Name() string { return "hoxi_browser" }
func (t *hoxiBrowserTool) Description() string {
	return "Control Hoxi's built-in headless/windowed Chrome browser for navigation, DOM snapshot, screenshot, click, type, scroll, JS evaluation, and tab management."
}
func (t *hoxiBrowserTool) IsReadOnly() bool { return false }
func (t *hoxiBrowserTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type": "string",
				"enum": []string{
					"navigate", "back", "forward", "reload", "click", "type", "key",
					"scroll", "snapshot", "screenshot", "evaluate", "tab_new", "tab_select", "tab_close", "close",
				},
				"description": "Browser action to perform",
			},
			"url":      map[string]any{"type": "string", "description": "Target URL for navigate or tab_new"},
			"selector": map[string]any{"type": "string", "description": "CSS selector or snapshot ref (e.g. @e3)"},
			"text":     map[string]any{"type": "string", "description": "Text to type or JS expression for evaluate"},
			"key":      map[string]any{"type": "string", "description": "Key press (e.g. Enter, Escape, Tab)"},
			"x":        map[string]any{"type": "number", "description": "Viewport X coordinate"},
			"y":        map[string]any{"type": "number", "description": "Viewport Y coordinate"},
			"deltaX":   map[string]any{"type": "number", "description": "Horizontal scroll delta"},
			"deltaY":   map[string]any{"type": "number", "description": "Vertical scroll delta"},
			"tabID":    map[string]any{"type": "string", "description": "Tab identifier for tab_select/tab_close"},
		},
		"required": []string{"action"},
	}
}
func (t *hoxiBrowserTool) Execute(ctx context.Context, tc ToolContext, input json.RawMessage) (ToolOutput, error) {
	return execHostTool(ctx, tc, t.Name(), input)
}

type hoxiComputerTool struct{}

func (t *hoxiComputerTool) Name() string { return "hoxi_computer" }
func (t *hoxiComputerTool) Description() string {
	return "Inspect and control native desktop GUI applications via Hoxi Computer Use (status, apps, accessibility state, screenshot, focus, click, drag, scroll, type, key)."
}
func (t *hoxiComputerTool) IsReadOnly() bool { return false }
func (t *hoxiComputerTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"status", "apps", "state", "screenshot", "focus", "move", "click", "drag", "scroll", "type", "key"},
				"description": "Desktop action to execute",
			},
			"app":           map[string]any{"type": "string", "description": "Target application name or bundle ID"},
			"windowID":      map[string]any{"type": "integer", "description": "Target window ID"},
			"observationID": map[string]any{"type": "string", "description": "Observation ID from prior state/screenshot"},
			"elementID":     map[string]any{"type": "string", "description": "Accessibility element ID"},
			"x":             map[string]any{"type": "number", "description": "Screen X coordinate"},
			"y":             map[string]any{"type": "number", "description": "Screen Y coordinate"},
			"endX":          map[string]any{"type": "number", "description": "Drag destination X coordinate"},
			"endY":          map[string]any{"type": "number", "description": "Drag destination Y coordinate"},
			"deltaX":        map[string]any{"type": "number", "description": "Horizontal scroll delta"},
			"deltaY":        map[string]any{"type": "number", "description": "Vertical scroll delta"},
			"button":        map[string]any{"type": "string", "enum": []string{"left", "right", "middle"}},
			"clickCount":    map[string]any{"type": "integer", "description": "Click count (1 or 2)"},
			"text":          map[string]any{"type": "string", "description": "Text to type"},
			"key":           map[string]any{"type": "string", "description": "Key chord to press"},
			"screenshot":    map[string]any{"type": "boolean", "description": "Include screenshot after action"},
		},
		"required": []string{"action"},
	}
}
func (t *hoxiComputerTool) Execute(ctx context.Context, tc ToolContext, input json.RawMessage) (ToolOutput, error) {
	return execHostTool(ctx, tc, t.Name(), input)
}

type hoxiImageTool struct{}

func (t *hoxiImageTool) Name() string { return "hoxi_image" }
func (t *hoxiImageTool) Description() string {
	return "Generate or edit images using Hoxi's configured image generation models and save outputs to disk."
}
func (t *hoxiImageTool) IsReadOnly() bool { return false }
func (t *hoxiImageTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"prompt":      map[string]any{"type": "string", "description": "Detailed image generation or edit prompt"},
			"outputPath":  map[string]any{"type": "string", "description": "Optional workspace-relative output file path"},
			"inputPaths":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional reference image paths"},
			"aspectRatio": map[string]any{"type": "string", "enum": []string{"1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3", "21:9"}},
			"imageSize":   map[string]any{"type": "string", "enum": []string{"1K", "2K", "4K"}},
			"providerID":  map[string]any{"type": "string", "description": "Optional provider override"},
			"modelID":     map[string]any{"type": "string", "description": "Optional model override"},
		},
		"required": []string{"prompt"},
	}
}
func (t *hoxiImageTool) Execute(ctx context.Context, tc ToolContext, input json.RawMessage) (ToolOutput, error) {
	return execHostTool(ctx, tc, t.Name(), input)
}

type hoxiToolsGatewayTool struct{}

func (t *hoxiToolsGatewayTool) Name() string { return "hoxi_tools" }
func (t *hoxiToolsGatewayTool) Description() string {
	return "Discover, inspect, and invoke external APIs through Hoxi's authenticated Tools Gateway."
}
func (t *hoxiToolsGatewayTool) IsReadOnly() bool { return false }
func (t *hoxiToolsGatewayTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action":      map[string]any{"type": "string", "enum": []string{"discover", "inspect", "run"}, "description": "Gateway action"},
			"query":       map[string]any{"type": "string", "description": "Search query for discover"},
			"category":    map[string]any{"type": "string", "description": "Optional category filter"},
			"limit":       map[string]any{"type": "integer", "description": "Maximum results for discover"},
			"provider":    map[string]any{"type": "string", "description": "Provider slug for inspect/run"},
			"endpoint":    map[string]any{"type": "string", "description": "Endpoint ID or path for inspect/run"},
			"body":        map[string]any{"type": "object", "description": "JSON request body for run"},
			"queryParams": map[string]any{"type": "object", "description": "Query parameters for run"},
			"pathParams":  map[string]any{"type": "object", "description": "Path parameters for run"},
			"wait":        map[string]any{"type": "boolean", "description": "Wait for async job completion"},
		},
		"required": []string{"action"},
	}
}
func (t *hoxiToolsGatewayTool) Execute(ctx context.Context, tc ToolContext, input json.RawMessage) (ToolOutput, error) {
	return execHostTool(ctx, tc, t.Name(), input)
}

type skillLoaderTool struct{}

func (t *skillLoaderTool) Name() string { return "skill" }
func (t *skillLoaderTool) Description() string {
	return "List available workspace/user skills when called without name, or load the full SKILL.md instructions for a named skill on demand."
}
func (t *skillLoaderTool) IsReadOnly() bool { return true }
func (t *skillLoaderTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string", "description": "Optional skill name to load (omit to list available skills)"},
		},
	}
}
func (t *skillLoaderTool) Execute(ctx context.Context, tc ToolContext, input json.RawMessage) (ToolOutput, error) {
	return execHostTool(ctx, tc, t.Name(), input)
}

type goalReportTool struct{}

func (t *goalReportTool) Name() string { return "goal_report" }
func (t *goalReportTool) Description() string {
	return "Record structured completion or blocker status for an autonomous or orchestrated goal."
}
func (t *goalReportTool) IsReadOnly() bool { return true }
func (t *goalReportTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"status": map[string]any{
				"type":        "string",
				"enum":        []string{"complete", "blocked"},
				"description": "Whether the goal is complete or blocked",
			},
			"reason": map[string]any{
				"type":        "string",
				"description": "Concise verification evidence or blocker details",
			},
		},
		"required": []string{"status", "reason"},
	}
}
func (t *goalReportTool) Execute(_ context.Context, _ ToolContext, input json.RawMessage) (ToolOutput, error) {
	var args struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return ToolOutput{Title: "goal_report", IsError: true}, err
	}
	status := strings.ToLower(strings.TrimSpace(args.Status))
	if status != "complete" && status != "blocked" {
		return ToolOutput{Title: "goal_report", IsError: true}, fmt.Errorf("status must be \"complete\" or \"blocked\"")
	}
	reason := strings.TrimSpace(args.Reason)
	if reason == "" {
		return ToolOutput{Title: "goal_report", IsError: true}, fmt.Errorf("reason is required")
	}
	return ToolOutput{
		Title:    "goal: " + status,
		Output:   fmt.Sprintf("Goal %s recorded: %s", status, reason),
		Metadata: map[string]any{"status": status, "reason": reason},
	}, nil
}

type hoxiDataProfileTool struct{}

func (t *hoxiDataProfileTool) Name() string { return "hoxi_data" }
func (t *hoxiDataProfileTool) Description() string {
	return "List or invoke credential-isolated HTTP/API endpoint profiles by handle ID without exposing bearer tokens or secret headers."
}
func (t *hoxiDataProfileTool) IsReadOnly() bool { return false }
func (t *hoxiDataProfileTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"list", "http_request"},
				"description": "Operation to execute against configured endpoint profile handles",
			},
			"profile":  map[string]any{"type": "string", "description": "Profile handle ID (omit for list)"},
			"method":   map[string]any{"type": "string", "description": "HTTP method for http_request (default GET)"},
			"endpoint": map[string]any{"type": "string", "description": "Relative endpoint path starting with '/' for http_request"},
			"body":     map[string]any{"type": "object", "description": "Optional JSON request body for http_request"},
		},
		"required": []string{"action"},
	}
}
func (t *hoxiDataProfileTool) Execute(ctx context.Context, tc ToolContext, input json.RawMessage) (ToolOutput, error) {
	return execHostTool(ctx, tc, t.Name(), input)
}
