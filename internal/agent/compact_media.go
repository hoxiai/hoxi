package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	// maxLiveToolScreenshots caps live hoxi_computer / hoxi_browser screenshot
	// attachments in a conversation before older frames are pruned down to
	// keepLiveToolScreenshots. The hysteresis (4 -> 2) preserves upstream
	// KV-cache prefixes across consecutive steps while preventing multimodal
	// payload and vision-token explosion during long desktop/browser loops.
	maxLiveToolScreenshots     = 4
	relaxedLiveToolScreenshots = 6
	keepLiveToolScreenshots    = 2
	// maxLiveGeneratedImages applies the same hysteresis to hoxi_image outputs
	// so multi-image generation loops never resend unbounded base64 payloads.
	maxLiveGeneratedImages  = 4
	keepLiveGeneratedImages = 2
)

func isEphemeralScreenshotTool(toolName string) bool {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "hoxi_computer", "hoxi_browser":
		return true
	default:
		return false
	}
}

func isGeneratedImageTool(toolName string) bool {
	return strings.ToLower(strings.TrimSpace(toolName)) == "hoxi_image"
}

func isOtherMediaTool(toolName string) bool {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "hoxi_computer", "hoxi_browser", "mcp_call_tool", "hoxi_tools":
		return true
	default:
		return false
	}
}

func isEphemeralScreenshotFilename(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	return strings.HasPrefix(lower, "hoxi_computer.") || strings.HasPrefix(lower, "hoxi_browser.") ||
		lower == "computer.jpg" || lower == "browser.jpg"
}

// pruneStaleToolScreenshots replaces older ephemeral screenshot media blocks
// produced by hoxi_computer / hoxi_browser once live screenshot count exceeds
// maxLiveToolScreenshots, and independently bounds hoxi_image tool outputs once
// count exceeds maxLiveGeneratedImages, leaving the latest 2 of each intact.
// Plain user-uploaded images and MCP media are never touched by this pass.
func pruneStaleToolScreenshots(msgs []Message) []Message {
	if len(msgs) == 0 {
		return msgs
	}
	toolNameByID := make(map[string]string)
	screenshotTrigger := maxLiveToolScreenshots
	var screenshotPositions [][2]int
	var generatedImagePositions [][2]int

	for mIdx, m := range msgs {
		switch m.Role {
		case RoleAssistant:
			for _, b := range m.Content {
				if b.Type == ContentToolUse && b.ID != "" {
					call, _ := resolveHostProxy(ToolCall{Name: b.Name, Input: b.Input})
					toolNameByID[b.ID] = strings.ToLower(strings.TrimSpace(call.Name))
				}
			}
		case RoleUser:
			hasToolResult := false
			allScreenshotTools := true
			hasGeneratedImageTool := false
			hasOtherMedia := false
			for _, b := range m.Content {
				if b.Type != ContentToolResult {
					continue
				}
				hasToolResult = true
				toolName := toolNameByID[b.ToolUseID]
				if !isEphemeralScreenshotTool(toolName) {
					allScreenshotTools = false
				}
				if isGeneratedImageTool(toolName) {
					hasGeneratedImageTool = true
				} else if isOtherMediaTool(toolName) {
					hasOtherMedia = true
				}
			}
			if !hasToolResult {
				continue
			}
			for bIdx, b := range m.Content {
				if b.Type != ContentMedia || b.Data == "" {
					continue
				}
				if b.ScreenshotTrigger != 0 || isEphemeralScreenshotFilename(b.Filename) || (allScreenshotTools && strings.TrimSpace(b.Filename) == "") {
					screenshotTrigger = maxLiveToolScreenshots
					if b.ScreenshotTrigger == relaxedLiveToolScreenshots {
						screenshotTrigger = relaxedLiveToolScreenshots
					}
					screenshotPositions = append(screenshotPositions, [2]int{mIdx, bIdx})
				} else if hasGeneratedImageTool && !hasOtherMedia && strings.HasPrefix(strings.ToLower(strings.TrimSpace(b.MimeType)), "image/") {
					generatedImagePositions = append(generatedImagePositions, [2]int{mIdx, bIdx})
				}
			}
		}
	}
	dropLabel := make(map[[2]int]string)
	if len(screenshotPositions) > screenshotTrigger {
		for _, pos := range screenshotPositions[:len(screenshotPositions)-keepLiveToolScreenshots] {
			dropLabel[pos] = "screenshot"
		}
	}
	if len(generatedImagePositions) > maxLiveGeneratedImages {
		for _, pos := range generatedImagePositions[:len(generatedImagePositions)-keepLiveGeneratedImages] {
			dropLabel[pos] = "generated image"
		}
	}
	if len(dropLabel) == 0 {
		return msgs
	}

	out := make([]Message, len(msgs))
	copy(out, msgs)
	for mIdx := range out {
		var blocks []ContentBlock
		for bIdx, b := range out[mIdx].Content {
			label, ok := dropLabel[[2]int{mIdx, bIdx}]
			if !ok {
				continue
			}
			if blocks == nil {
				blocks = append([]ContentBlock(nil), out[mIdx].Content...)
			}
			name := strings.TrimSpace(b.Filename)
			if name == "" {
				name = label
			}
			blocks[bIdx] = ContentBlock{
				Type: ContentText,
				Text: fmt.Sprintf("[omitted earlier %s: %s (%s)]", label, name, b.MimeType),
			}
		}
		if blocks != nil {
			out[mIdx].Content = blocks
		}
	}
	return out
}

func summarizeComputerToolOutput(body string) (string, bool) {
	trimmed := strings.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return "", false
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(trimmed), &obj); err != nil || obj == nil {
		return "", false
	}
	obsID, _ := obj["observationID"].(string)
	winID, _ := obj["windowID"].(string)
	rawElems, hasElems := obj["elements"].([]any)
	if obsID == "" && winID == "" && !hasElems {
		return "", false
	}
	var parts []string
	if appObj, ok := obj["app"].(map[string]any); ok {
		if name, _ := appObj["name"].(string); strings.TrimSpace(name) != "" {
			parts = append(parts, "app="+strings.TrimSpace(name))
		}
	}
	if winID != "" {
		parts = append(parts, "window="+winID)
	}
	if obsID != "" {
		parts = append(parts, "observation="+obsID)
	}
	if hasElems {
		parts = append(parts, fmt.Sprintf("elements=%d", len(rawElems)))
	}
	if completed, ok := obj["completed"].(bool); ok && completed {
		parts = append(parts, "completed=true")
	}
	if waitedMs, ok := obj["waitedMs"].(float64); ok && waitedMs > 0 {
		parts = append(parts, fmt.Sprintf("waitedMs=%d", int64(waitedMs)))
	}
	return fmt.Sprintf(
		"[tool output pruned: %d chars (hoxi_computer); %s; call state/screenshot/wait if fresh elements are needed]",
		len(body),
		strings.Join(parts, " "),
	), true
}
