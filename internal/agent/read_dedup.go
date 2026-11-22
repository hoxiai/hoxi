package agent

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	// maxReadLineBytes caps individual source lines in read output so a single
	// minified JS/CSS/SVG or inline base64 line cannot blow the entire 32 KiB
	// tool output budget and starve subsequent lines in the file.
	maxReadLineBytes = 2048

	// maxGrepSnippetBytes bounds each grep match line to a UTF-8 window centered
	// around the matched span so minified/long lines keep the match visible
	// without consuming thousands of tokens on a single hit.
	maxGrepSnippetBytes = 512

	// minUnchangedReadDedupBytes avoids deduplicating tiny reads where a short
	// reference notice saves negligible tokens.
	minUnchangedReadDedupBytes = 600

	unchangedReadNoticePrefix = "[unchanged since earlier read in this context]"
)

func formatReadLine(lineNo int, line string) (formatted string, truncated bool) {
	if len(line) <= maxReadLineBytes {
		return fmt.Sprintf("%d: %s\n", lineNo, line), false
	}
	prefix := utf8Prefix(line, maxReadLineBytes)
	omitted := len(line) - len(prefix)
	return fmt.Sprintf("%d: %s ... [%d bytes omitted on line %d]\n", lineNo, prefix, omitted, lineNo), true
}

func formatGrepMatchSnippet(rawLine []byte, matchStart, matchEnd, maxBytes int) string {
	if maxBytes <= 0 {
		maxBytes = maxGrepSnippetBytes
	}
	if len(rawLine) <= maxBytes {
		return ensureValidUTF8(strings.TrimSpace(string(rawLine)))
	}
	if matchStart < 0 {
		matchStart = 0
	}
	if matchEnd < matchStart {
		matchEnd = matchStart
	}
	if matchEnd > len(rawLine) {
		matchEnd = len(rawLine)
	}
	mid := (matchStart + matchEnd) / 2
	winStart := max(0, mid-maxBytes/2)
	winEnd := min(len(rawLine), winStart+maxBytes)
	if winEnd-winStart < maxBytes && winStart > 0 {
		winStart = max(0, winEnd-maxBytes)
	}
	for skipped := 0; skipped < 3 && winStart < winEnd && !utf8.RuneStart(rawLine[winStart]); skipped++ {
		winStart++
	}
	window := utf8Prefix(string(rawLine[winStart:winEnd]), winEnd-winStart)
	actualEnd := winStart + len(window)
	snippet := strings.TrimSpace(ensureValidUTF8(window))
	if winStart > 0 {
		snippet = "..." + snippet
	}
	if actualEnd < len(rawLine) {
		snippet = snippet + "..."
	}
	return snippet
}

func extractReadInputPath(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var args struct {
		FilePath string `json:"filePath"`
		Path     string `json:"path"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return ""
	}
	if p := strings.TrimSpace(args.FilePath); p != "" {
		return p
	}
	return strings.TrimSpace(args.Path)
}

func isCodeIndexLineSourceTool(toolName string, raw json.RawMessage) bool {
	lower := strings.ToLower(strings.TrimSpace(toolName))
	if lower == "symbol_inspect" {
		return true
	}
	if lower != "hoxi_code_index" {
		return false
	}
	var args struct {
		Action string `json:"action"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return false
	}
	act := strings.ToLower(strings.TrimSpace(args.Action))
	return act == "inspect" || act == "context"
}

func codeIndexOutputMatchesTarget(worktreeDir, targetKey, text string) bool {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		var rest string
		switch {
		case strings.HasPrefix(trimmed, "# "):
			rest = strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
		case strings.HasPrefix(trimmed, "Definition: "):
			rest = strings.TrimSpace(strings.TrimPrefix(trimmed, "Definition: "))
		default:
			continue
		}
		colon := strings.LastIndexByte(rest, ':')
		if colon <= 0 {
			continue
		}
		// Headers look like "path/to/file.go:10-40 (of 200)" or "path/to/file.go:10-40".
		// Split on the first ':' followed by a digit to preserve Windows drive letters.
		candidate := ""
		for i := 0; i < len(rest)-1; i++ {
			if rest[i] == ':' && rest[i+1] >= '0' && rest[i+1] <= '9' {
				candidate = strings.TrimSpace(rest[:i])
				break
			}
		}
		if candidate == "" {
			continue
		}
		if resolved, err := resolveWorkspacePath(worktreeDir, candidate, false); err == nil && canonicalRevisionKey(resolved) == targetKey {
			return true
		}
	}
	return false
}

func containsExactLineBlock(haystack, needle string) bool {
	needle = strings.TrimSuffix(needle, "\n")
	if needle == "" {
		return false
	}
	start := 0
	for start <= len(haystack)-len(needle) {
		rel := strings.Index(haystack[start:], needle)
		if rel < 0 {
			return false
		}
		idx := start + rel
		end := idx + len(needle)
		leftOK := idx == 0 || haystack[idx-1] == '\n'
		rightOK := end == len(haystack) || haystack[end] == '\n'
		if leftOK && rightOK {
			return true
		}
		start = idx + 1
	}
	return false
}

// tryBuildUnchangedReadNotice checks whether the exact rendered [startLine..endLine]
// block is already present verbatim (unpruned and untruncated) in an earlier
// read or hoxi_code_index (inspect/context) tool result for the same file within
// tc.VisibleMessages.
//
// Because it checks live visible messages rather than out-of-band session state:
//   - Earlier messages are never mutated (preserving 100% upstream KV-cache hit rate).
//   - If an earlier read was pruned by 75% compaction or summarized via /compact,
//     it is no longer present in visible messages, so re-reading succeeds in full.
//   - If any line in [startLine..endLine] changed on disk, renderedLines no longer
//     matches the earlier tool result, so the updated lines are returned in full.
//   - If the most recent read for this file was already an [unchanged] notice,
//     deduplication is bypassed so an explicit consecutive re-read always gets full lines.
func tryBuildUnchangedReadNotice(
	visible []Message,
	worktreeDir, realTarget, displayPath, renderedLines string,
	raw []byte,
	startLine, endLine, totalLines int,
) (string, bool) {
	if len(visible) == 0 || len(renderedLines) < minUnchangedReadDedupBytes {
		return "", false
	}
	targetKey := canonicalRevisionKey(realTarget)
	if targetKey == "" {
		return "", false
	}

	type pendingKind uint8
	const (
		pendingRead pendingKind = iota + 1
		pendingIndexKnownPath
		pendingIndexAnyPath
	)

	// Older Gemini transcripts can reuse a call ID on the next model step.
	// Pair each result with its pending request before considering its text.
	pending := make(map[string]pendingKind)
	var matched, latestWasNotice bool
	for _, msg := range visible {
		for _, b := range msg.Content {
			switch {
			case msg.Role == RoleAssistant && b.Type == ContentToolUse && b.ID != "":
				delete(pending, b.ID)
				if strings.EqualFold(strings.TrimSpace(b.Name), "read") {
					if path := extractReadInputPath(b.Input); path != "" {
						resolved, err := resolveWorkspacePath(worktreeDir, path, false)
						if err == nil && canonicalRevisionKey(resolved) == targetKey {
							pending[b.ID] = pendingRead
						}
					}
				} else if isCodeIndexLineSourceTool(b.Name, b.Input) {
					if path := extractReadInputPath(b.Input); path != "" {
						resolved, err := resolveWorkspacePath(worktreeDir, path, false)
						if err == nil && canonicalRevisionKey(resolved) == targetKey {
							pending[b.ID] = pendingIndexKnownPath
						}
					} else {
						pending[b.ID] = pendingIndexAnyPath
					}
				}
			case msg.Role == RoleUser && b.Type == ContentToolResult && pending[b.ToolUseID] != 0:
				kind := pending[b.ToolUseID]
				delete(pending, b.ToolUseID)
				if b.IsError || strings.TrimSpace(b.Text) == "" {
					continue
				}
				if kind == pendingRead {
					latestWasNotice = strings.HasPrefix(b.Text, unchangedReadNoticePrefix)
				}
				if !matched && !strings.HasPrefix(b.Text, unchangedReadNoticePrefix) && !strings.HasPrefix(b.Text, "[tool output pruned:") {
					if kind == pendingIndexAnyPath && !codeIndexOutputMatchesTarget(worktreeDir, targetKey, b.Text) {
						continue
					}
					matched = containsExactLineBlock(b.Text, renderedLines)
				}
			}
		}
	}
	if !matched || latestWasNotice {
		return "", false
	}
	sum := sha256.Sum256(raw)
	label := strings.TrimSpace(displayPath)
	if label == "" {
		label = filepath.Base(realTarget)
	}
	return fmt.Sprintf(
		"%s %s (lines %d-%d of %d, %d bytes, sha256=%x); exact lines are already present above in this conversation.",
		unchangedReadNoticePrefix, label, startLine, endLine, totalLines, len(raw), sum[:6],
	), true
}
