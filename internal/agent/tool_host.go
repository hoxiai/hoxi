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
	return "Control the active session browser (Desktop Window with persistent ~/.hoxi/browser-profile sign-ins, isolated headless Chrome, or an attached Chrome Link tab). Call navigate with an HTTP(S) URL to open a page; snapshot, screenshot, and diagnostics only inspect an already-open browser and return empty tabs when idle. snapshot returns visible text plus interactive controls with @e1..@e200 refs; pass @eN or a CSS selector to click, hover, type, select, or wait. For action='type', pass clear=true to replace existing input text and/or key (e.g. 'Enter' or 'Tab') to submit in one step. For <select> dropdowns, use action='select' with selector and text (option value or label). Read-only actions (snapshot, screenshot, diagnostics, wait) skip approval; all mutations (including evaluate) require approval, and manual mode blocks agent control."
}
func (t *hoxiBrowserTool) IsReadOnly() bool { return false }
func (t *hoxiBrowserTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type": "string",
				"enum": []string{
					"navigate", "back", "forward", "reload", "click", "hover", "type", "select", "key",
					"scroll", "wait", "snapshot", "screenshot", "diagnostics", "evaluate",
					"tab_new", "tab_select", "tab_close", "close",
				},
				"description": "Browser action to perform",
			},
			"url":       map[string]any{"type": "string", "description": "HTTP(S) URL for navigate or tab_new"},
			"selector":  map[string]any{"type": "string", "description": "CSS selector or @eN snapshot ref (e.g. @e3) for click, hover, type, select, or wait"},
			"text":      map[string]any{"type": "string", "description": "Text to type, option value/label for select, or synchronous JS expression for evaluate"},
			"key":       map[string]any{"type": "string", "description": "DOM key chord (e.g. Enter, Escape, Tab, Control+a) for key, or optional trailing key after type"},
			"clear":     map[string]any{"type": "boolean", "description": "Clear existing input/textarea value before typing (action='type' only)"},
			"timeoutMs": map[string]any{"type": "integer", "minimum": 100, "maximum": 10000, "description": "Bounded wait timeout in milliseconds (100..10000, action='wait' only)"},
			"x":         map[string]any{"type": "number", "description": "Viewport CSS X coordinate"},
			"y":         map[string]any{"type": "number", "description": "Viewport CSS Y coordinate"},
			"deltaX":    map[string]any{"type": "number", "description": "Horizontal scroll delta"},
			"deltaY":    map[string]any{"type": "number", "description": "Vertical scroll delta"},
			"tabID":     map[string]any{"type": "string", "description": "Tab identifier for tab_select or tab_close"},
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
	return "Inspect and control a running desktop app on macOS and Windows. Use the app the user authorized in Hoxi's Computer panel, or pass 'app' (app name like 'Calculator' or 'Safari', bundleID, or id from action='apps') ONLY on state/screenshot to request native dialog confirmation for another app (wait and input actions cannot switch apps). Call state or screenshot first: visible interactive controls are prioritized and each readable element includes windowBounds (relative to the target window), optional focused/disabled flags, and an integer id you can pass as elementID (1..300) to click, move, scroll, or type without coordinate math. Use action='wait' (seconds: 1..60, default 5; optional screenshot=true) when waiting for an app's long-running task or confirmation dialog instead of rapid polling—wait performs no desktop input, skips input approval, and returns a fresh observation. Manual x/y/endX/endY coordinates are relative to the target window's bounds, NEVER screen coordinates or scaled screenshot pixels. Every input requires the latest windowID (string) and observationID (string), waits for desktop approval (unless continuous input is enabled for the active grant), and returns a fresh post-input observationID and element state (pass screenshot=true on an input action to also capture a fresh JPEG in one call). For action='type', pass clear=true to replace existing input text and/or 'key' (e.g. 'Enter' or 'Tab') to type and press a key atomically; multiline '\\n' text is inserted safely with Shift+Enter without premature submission. Protected apps (Hoxi, System Settings, terminals/script editors, password managers) and password fields are blocked."
}
func (t *hoxiComputerTool) IsReadOnly() bool { return false }
func (t *hoxiComputerTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"status", "apps", "state", "screenshot", "wait", "focus", "move", "click", "drag", "scroll", "type", "key"},
				"description": "Desktop action to execute",
			},
			"app":           map[string]any{"type": "string", "maxLength": 256, "description": "Target application name, bundleID, or id from action='apps' (only on state/screenshot to request user confirmation)"},
			"windowID":      map[string]any{"type": "string", "maxLength": 64, "description": "Target window ID string returned by state/screenshot (required for all input actions)"},
			"observationID": map[string]any{"type": "string", "maxLength": 64, "description": "Fresh 32-hex observationID from the latest state/screenshot/wait or prior input response"},
			"elementID":     map[string]any{"type": "integer", "minimum": 1, "maximum": 300, "description": "Accessibility element integer ID (1..300) from state/screenshot/wait to target element center"},
			"x":             map[string]any{"type": "number", "minimum": 0, "maximum": 16384, "description": "Window-relative X coordinate (relative to target window bounds, NOT screen or scaled image pixels)"},
			"y":             map[string]any{"type": "number", "minimum": 0, "maximum": 16384, "description": "Window-relative Y coordinate (relative to target window bounds, NOT screen or scaled image pixels)"},
			"endX":          map[string]any{"type": "number", "minimum": 0, "maximum": 16384, "description": "Drag destination window-relative X coordinate"},
			"endY":          map[string]any{"type": "number", "minimum": 0, "maximum": 16384, "description": "Drag destination window-relative Y coordinate"},
			"deltaX":        map[string]any{"type": "integer", "minimum": -2000, "maximum": 2000, "description": "Horizontal scroll delta in integer pixels"},
			"deltaY":        map[string]any{"type": "integer", "minimum": -2000, "maximum": 2000, "description": "Vertical scroll delta in integer pixels"},
			"button":        map[string]any{"type": "string", "enum": []string{"left", "right", "middle"}},
			"clickCount":    map[string]any{"type": "integer", "minimum": 1, "maximum": 3, "description": "Click count (1=single, 2=double, 3=triple)"},
			"text":          map[string]any{"type": "string", "maxLength": 8192, "description": "Unicode text to type (newlines insert Shift+Enter safely without submitting)"},
			"key":           map[string]any{"type": "string", "maxLength": 128, "description": "DOM key chord (e.g. Enter, Tab, Escape, Meta+c); also supported on action='type'"},
			"clear":         map[string]any{"type": "boolean", "description": "Select all and clear existing field text before typing (action='type' only)"},
			"seconds":       map[string]any{"type": "number", "minimum": 1, "maximum": 60, "description": "Bounded wait duration in seconds (1..60, default 5; action='wait' only)"},
			"screenshot":    map[string]any{"type": "boolean", "description": "Include a fresh JPEG screenshot in the wait or post-input response"},
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
	return "Generate or edit images: checks the active session model's native image capability first, then configured local image models, and saves outputs to disk (fall back to hoxi_tools only if unavailable)."
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

type hoxiVideoTool struct{}

func (t *hoxiVideoTool) Name() string { return "hoxi_video" }
func (t *hoxiVideoTool) Description() string {
	return "Generate text-to-video or image-to-video clips: checks the active session model's native video capability first, then configured local video models (Veo, Sora, Wan, Seedance, Kling, CogVideo), and saves outputs to disk (fall back to hoxi_tools only if unavailable)."
}
func (t *hoxiVideoTool) IsReadOnly() bool { return false }
func (t *hoxiVideoTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"prompt":          map[string]any{"type": "string", "description": "Detailed video motion, scene, and camera prompt"},
			"outputPath":      map[string]any{"type": "string", "description": "Optional workspace-relative output video file path (e.g. 'public/intro.mp4')"},
			"inputPaths":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional workspace-relative first-frame reference image path(s)"},
			"aspectRatio":     map[string]any{"type": "string", "enum": []string{"16:9", "9:16", "1:1", "4:3", "3:4"}},
			"resolution":      map[string]any{"type": "string", "enum": []string{"720p", "1080p"}},
			"durationSeconds": map[string]any{"type": "integer", "description": "Optional target clip duration in seconds (2-20)"},
			"providerID":      map[string]any{"type": "string", "description": "Optional provider override"},
			"modelID":         map[string]any{"type": "string", "description": "Optional model override"},
		},
		"required": []string{"prompt"},
	}
}
func (t *hoxiVideoTool) Execute(ctx context.Context, tc ToolContext, input json.RawMessage) (ToolOutput, error) {
	return execHostTool(ctx, tc, t.Name(), input)
}

type hoxiSpeechTool struct{}

func (t *hoxiSpeechTool) Name() string { return "hoxi_speech" }
func (t *hoxiSpeechTool) Description() string {
	return "Synthesize spoken audio (TTS / voiceover) from text: checks the active session model's native audio output capability first, then configured local TTS models, and saves outputs to disk (fall back to hoxi_tools only if unavailable)."
}
func (t *hoxiSpeechTool) IsReadOnly() bool { return false }
func (t *hoxiSpeechTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"text":       map[string]any{"type": "string", "description": "Spoken script or text to synthesize into audio"},
			"outputPath": map[string]any{"type": "string", "description": "Optional workspace-relative output audio file path (e.g. 'public/voiceover.mp3')"},
			"voice":      map[string]any{"type": "string", "description": "Optional voice name (e.g. 'alloy', 'nova', 'Kore')"},
			"format":     map[string]any{"type": "string", "enum": []string{"mp3", "wav", "opus", "flac", "aac"}},
			"speed":      map[string]any{"type": "number", "description": "Optional playback speed (0.25 - 4.0)"},
			"providerID": map[string]any{"type": "string", "description": "Optional provider override"},
			"modelID":    map[string]any{"type": "string", "description": "Optional model override"},
		},
		"required": []string{"text"},
	}
}
func (t *hoxiSpeechTool) Execute(ctx context.Context, tc ToolContext, input json.RawMessage) (ToolOutput, error) {
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
	return "List, test, or invoke credential-isolated HTTP APIs, SQL databases (SQLite, PostgreSQL, MySQL), and SSH host profiles by handle ID without ever exposing tokens, passwords, or private keys in context. Call action='list' first to see configured profile IDs, kinds, targetHints, and readOnly flags."
}
func (t *hoxiDataProfileTool) IsReadOnly() bool { return false }
func (t *hoxiDataProfileTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"list", "http_request", "sql_query", "ssh_exec", "test"},
				"description": "Operation to execute against configured connection profile handles",
			},
			"profile":  map[string]any{"type": "string", "description": "Profile handle ID (omit for list)"},
			"method":   map[string]any{"type": "string", "description": "HTTP method for http_request (default GET)"},
			"endpoint": map[string]any{"type": "string", "description": "Relative endpoint path starting with '/' for http_request"},
			"body":     map[string]any{"type": "object", "description": "Optional JSON request body for http_request"},
			"query":    map[string]any{"type": "string", "description": "Single SQL statement for sql_query against sqlite/postgres/mysql profiles"},
			"maxRows":  map[string]any{"type": "integer", "minimum": 1, "maximum": 1000, "description": "Optional row limit for sql_query (default 200, max 1000)"},
			"command":  map[string]any{"type": "string", "description": "Remote command string for ssh_exec against an ssh profile"},
		},
		"required": []string{"action"},
	}
}
func (t *hoxiDataProfileTool) Execute(ctx context.Context, tc ToolContext, input json.RawMessage) (ToolOutput, error) {
	return execHostTool(ctx, tc, t.Name(), input)
}

type hoxiScheduleTool struct{}

func (t *hoxiScheduleTool) Name() string { return "hoxi_schedule" }
func (t *hoxiScheduleTool) Description() string {
	return "Manage workspace scheduled tasks (mutations require desktop approval). Set bindCurrentSession=true to reuse this chat, or omit for a fresh session."
}
func (t *hoxiScheduleTool) IsReadOnly() bool { return false }
func (t *hoxiScheduleTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action":             map[string]any{"type": "string", "enum": []string{"list", "get", "create", "update", "pause", "resume", "delete"}},
			"id":                 map[string]any{"type": "string"},
			"name":               map[string]any{"type": "string"},
			"prompt":             map[string]any{"type": "string"},
			"cron":               map[string]any{"type": "string"},
			"intervalMinutes":    map[string]any{"type": "integer"},
			"bindCurrentSession": map[string]any{"type": "boolean"},
			"sessionID":          map[string]any{"type": "string"},
			"mode":               map[string]any{"type": "string", "enum": []string{"build", "plan"}},
			"sandboxMode":        map[string]any{"type": "string", "enum": []string{"local", "worktree"}},
			"commandSandbox":     map[string]any{"type": "boolean"},
			"skillName":          map[string]any{"type": "string"},
			"skillKey":           map[string]any{"type": "string"},
			"providerID":         map[string]any{"type": "string"},
			"modelID":            map[string]any{"type": "string"},
			"notifyOn":           map[string]any{"type": "string", "enum": []string{"alert_or_error", "always", "never"}},
		},
		"required": []string{"action"},
	}
}
func (t *hoxiScheduleTool) Execute(ctx context.Context, tc ToolContext, input json.RawMessage) (ToolOutput, error) {
	return execHostTool(ctx, tc, t.Name(), input)
}
