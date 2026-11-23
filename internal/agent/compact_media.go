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
	maxLiveToolScreenshots  = 4
	keepLiveToolScreenshots = 2
)

func isEphemeralScreenshotTool(toolName string) bool {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "hoxi_computer", "hoxi_browser":
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
// maxLiveToolScreenshots, leaving the latest keepLiveToolScreenshots intact.
// Plain user-uploaded images and media from other tools (e.g. hoxi_image, MCP)
// are never touched by this pass.
func pruneStaleToolScreenshots(msgs []Message) []Message {
	if len(msgs) == 0 {
		return msgs
	}
	toolNameByID := make(map[string]string)
	var positions [][2]int

	for mIdx, m := range msgs {
		switch m.Role {
		case RoleAssistant:
			for _, b := range m.Content {
				if b.Type == ContentToolUse && b.ID != "" {
					toolNameByID[b.ID] = strings.ToLower(strings.TrimSpace(b.Name))
				}
			}
		case RoleUser:
			hasToolResult := false
			allScreenshotTools := true
			for _, b := range m.Content {
				if b.Type != ContentToolResult {
					continue
				}
				hasToolResult = true
				if !isEphemeralScreenshotTool(toolNameByID[b.ToolUseID]) {
					allScreenshotTools = false
				}
			}
			if !hasToolResult {
				continue
			}
			for bIdx, b := range m.Content {
				if b.Type != ContentMedia || b.Data == "" {
					continue
				}
				if isEphemeralScreenshotFilename(b.Filename) || (allScreenshotTools && strings.TrimSpace(b.Filename) == "") {
					positions = append(positions, [2]int{mIdx, bIdx})
				}
			}
		}
	}
	if len(positions) <= maxLiveToolScreenshots {
		return msgs
	}

	dropCount := len(positions) - keepLiveToolScreenshots
	toDrop := make(map[[2]int]bool, dropCount)
	for _, pos := range positions[:dropCount] {
		toDrop[pos] = true
	}

	out := make([]Message, len(msgs))
	copy(out, msgs)
	for mIdx := range out {
		var blocks []ContentBlock
		for bIdx, b := range out[mIdx].Content {
			if !toDrop[[2]int{mIdx, bIdx}] {
				continue
			}
			if blocks == nil {
				blocks = append([]ContentBlock(nil), out[mIdx].Content...)
			}
			name := strings.TrimSpace(b.Filename)
			if name == "" {
				name = "screenshot"
			}
			blocks[bIdx] = ContentBlock{
				Type: ContentText,
				Text: fmt.Sprintf("[omitted earlier screenshot: %s (%s)]", name, b.MimeType),
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
