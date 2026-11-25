package agent

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	compactTriggerPercent          = 75
	compactTargetPercent           = 85
	compactHardCapPercent          = 90
	maxRegularCompactTriggerTokens = 150000
	maxRegularCompactHardCapTokens = 180000
	compactMinKeepRecentMsgs       = 6
	compactMediumKeepRecentMsgs    = 14
	compactLargeKeepRecentMsgs     = 20
	// compactChunkStride quantizes the pruning cutoff to fixed message-index
	// boundaries so rebuilt turn histories keep a byte-identical prefix across
	// consecutive steps and turns instead of shifting cutoff by +1/+2 each turn
	// and invalidating upstream LLM prompt caches.
	compactChunkStride        = 12
	compactTightMinToolLen    = 800
	compactStandardMinToolLen = 2400
	// compactStaleReadMinLen is the minimum body size before a read snapshot
	// superseded by a later edit or covering re-read in the same quantized chunk
	// is folded into a compact staleness notice.
	compactStaleReadMinLen = 320
)

func preferredKeepRecentMsgs(contextLimit int) int {
	switch {
	case contextLimit >= 64000:
		return compactLargeKeepRecentMsgs
	case contextLimit >= 16000:
		return compactMediumKeepRecentMsgs
	default:
		return compactMinKeepRecentMsgs
	}
}

func preferredMinToolLen(contextLimit int) int {
	if contextLimit >= 32000 {
		return compactStandardMinToolLen
	}
	return compactTightMinToolLen
}

// PruneOldToolResults compacts large older tool outputs once estimated tokens
// exceed 75% of contextLimit while quantizing the cutoff index to preserve
// upstream KV-cache prefix stability across consecutive turns.
func PruneOldToolResults(msgs []Message, system string, contextLimit int) []Message {
	return pruneOldToolResultsInWorkspace(msgs, system, "", contextLimit)
}

// PruneOldToolResultsInWorkspace compacts older tool outputs like
// PruneOldToolResults while unifying absolute and workspace-relative file paths
// against worktreeDir (falling back to the active <environment> Working
// directory in system when worktreeDir is empty).
func PruneOldToolResultsInWorkspace(msgs []Message, system, worktreeDir string, contextLimit int) []Message {
	return pruneOldToolResultsInWorkspace(msgs, system, worktreeDir, contextLimit)
}

func pruneOldToolResults(msgs []Message, system string, contextLimit int) []Message {
	return pruneOldToolResultsInWorkspace(msgs, system, "", contextLimit)
}

func pruneOldToolResultsInWorkspace(msgs []Message, system, worktreeDir string, contextLimit int) []Message {
	if contextLimit <= 0 {
		contextLimit = 128000
	}
	triggerTokens := min(contextLimit*compactTriggerPercent/100, maxRegularCompactTriggerTokens)
	hardCapTokens := min(contextLimit*compactHardCapPercent/100, maxRegularCompactHardCapTokens)
	return pruneOldToolResultsCore(msgs, system, worktreeDir, contextLimit, triggerTokens, hardCapTokens, func(cand []Message) int {
		return estimateTokens(cand, system)
	})
}

func pruneOldToolResultsCore(
	msgs []Message,
	system, worktreeDir string,
	contextLimit, triggerTokens, hardCapTokens int,
	measure func([]Message) int,
) []Message {
	msgs = sanitizeMessagesUTF8(msgs)
	msgs = pruneStaleToolScreenshots(msgs)
	if contextLimit <= 0 {
		contextLimit = 128000
	}
	if measure(msgs) <= triggerTokens {
		return msgs
	}

	effectiveRoot := strings.TrimSpace(worktreeDir)
	if effectiveRoot == "" {
		effectiveRoot = extractWorkspaceRootFromSystem(system)
	}
	norm := newPrunePathNormalizer(effectiveRoot)

	keepRecent := preferredKeepRecentMsgs(contextLimit)
	minToolLen := preferredMinToolLen(contextLimit)
	richPreview := contextLimit >= 32000
	rawCutoff := len(msgs) - keepRecent

	pruned := msgs
	if rawCutoff > 0 {
		quantizedCutoff := (rawCutoff / compactChunkStride) * compactChunkStride
		if quantizedCutoff <= 0 {
			const smallStride = 4
			if smallCutoff := (rawCutoff / smallStride) * smallStride; smallCutoff > 0 {
				pruned = pruneToolOutputsBeforeWithNorm(msgs, smallCutoff, minToolLen, richPreview, norm)
			} else {
				pruned = pruneToolOutputsBeforeWithNorm(msgs, rawCutoff, minToolLen, richPreview, norm)
			}
		} else {
			targetTokens := triggerTokens * compactTargetPercent / 100
			for stride := compactChunkStride; stride <= quantizedCutoff; stride += compactChunkStride {
				pruned = pruneToolOutputsBeforeWithNorm(msgs, stride, minToolLen, richPreview, norm)
				if measure(pruned) <= targetTokens {
					break
				}
			}
		}

		if measure(pruned) > hardCapTokens {
			pruned = pruneToolOutputsBeforeWithNorm(msgs, rawCutoff, minToolLen, richPreview, norm)
		}
	}

	if measure(pruned) > hardCapTokens {
		tightCutoff := len(msgs) - compactMinKeepRecentMsgs
		if tightCutoff > 0 {
			pruned = pruneToolOutputsBeforeWithNorm(msgs, tightCutoff, compactTightMinToolLen, false, norm)
		}
	}
	return pruned
}

const minProactiveCompactContextLimit = 16000

func sanitizeMessagesUTF8(msgs []Message) []Message {
	var copied []Message
	for i := range msgs {
		var blocks []ContentBlock
		for j := range msgs[i].Content {
			if !utf8.ValidString(msgs[i].Content[j].Text) {
				if blocks == nil {
					blocks = make([]ContentBlock, len(msgs[i].Content))
					copy(blocks, msgs[i].Content)
				}
				blocks[j].Text = ensureValidUTF8(blocks[j].Text)
				if blocks[j].Type == ContentThinking {
					// Replacing invalid UTF-8 bytes invalidates any cryptographic signature over Text.
					blocks[j].Signature = ""
				}
			}
		}
		if blocks != nil {
			if copied == nil {
				copied = make([]Message, len(msgs))
				copy(copied, msgs)
			}
			copied[i].Content = blocks
		}
	}
	if copied != nil {
		return copied
	}
	return msgs
}

type pruneReadSpan struct {
	pathKey     string
	displayPath string
	startLine   int
	endLine     int
	truncated   bool
}

type pruneToolExchange struct {
	msgIdx      int
	blockIdx    int
	toolName    string
	isError     bool
	readSpan    pruneReadSpan
	mutatedKeys map[string]bool
}

type supersededReadKind uint8

const (
	readNotSuperseded supersededReadKind = iota
	readSupersededByEdit
	readSupersededByRead
)

func indexPruneToolExchanges(msgs []Message, cutoff int, norm *prunePathNormalizer) ([]pruneToolExchange, map[[2]int]int) {
	latestUseByID := make(map[string]ContentBlock)
	var list []pruneToolExchange
	posIndex := make(map[[2]int]int)

	for mIdx := 0; mIdx < cutoff; mIdx++ {
		switch msgs[mIdx].Role {
		case RoleAssistant:
			for _, b := range msgs[mIdx].Content {
				if b.Type == ContentToolUse && b.ID != "" {
					latestUseByID[b.ID] = b
				}
			}
		case RoleUser:
			for bIdx, b := range msgs[mIdx].Content {
				if b.Type != ContentToolResult {
					continue
				}
				use := latestUseByID[b.ToolUseID]
				tName := strings.ToLower(strings.TrimSpace(use.Name))
				ex := pruneToolExchange{
					msgIdx:   mIdx,
					blockIdx: bIdx,
					toolName: tName,
					isError:  b.IsError,
				}
				if !b.IsError && len(use.Input) > 0 {
					switch tName {
					case "read":
						ex.readSpan = parsePruneReadSpan(use.Input, b.Text, norm)
					case "write", "edit", "multiedit":
						if key, _ := extractSingleFileInputKey(use.Input, norm); key != "" {
							ex.mutatedKeys = map[string]bool{key: true}
						}
					case "apply_patch":
						ex.mutatedKeys = extractPatchMutatedKeys(use.Input, norm)
					}
				}
				posIndex[[2]int{mIdx, bIdx}] = len(list)
				list = append(list, ex)
			}
		}
	}
	return list, posIndex
}

func parsePruneReadSpan(raw json.RawMessage, resultText string, norm *prunePathNormalizer) pruneReadSpan {
	// References, pruned previews, directory listings and EOF notices do not
	// supply a replacement for numbered source lines.
	first, _, _ := strings.Cut(resultText, ": ")
	if len(first) == 0 || len(first) > 20 {
		return pruneReadSpan{}
	}
	if n, err := strconv.Atoi(first); err != nil || n < 1 {
		return pruneReadSpan{}
	}
	var args struct {
		FilePath string `json:"filePath"`
		Path     string `json:"path"`
		Offset   int    `json:"offset"`
		Limit    int    `json:"limit"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return pruneReadSpan{}
	}
	p := strings.TrimSpace(args.FilePath)
	if p == "" {
		p = strings.TrimSpace(args.Path)
	}
	key, disp := norm.normalize(p)
	if key == "" {
		return pruneReadSpan{}
	}
	start := max(1, args.Offset)
	limit := args.Limit
	if limit <= 0 {
		limit = 2000
	}
	return pruneReadSpan{
		pathKey:     key,
		displayPath: disp,
		startLine:   start,
		endLine:     start + limit - 1,
		truncated:   strings.Contains(resultText, " bytes truncated] ...") || strings.Contains(resultText, " bytes truncated at line boundary") || strings.Contains(resultText, " bytes omitted on line "),
	}
}

func extractSingleFileInputKey(raw json.RawMessage, norm *prunePathNormalizer) (string, string) {
	var args struct {
		FilePath string `json:"filePath"`
		Path     string `json:"path"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return "", ""
	}
	p := strings.TrimSpace(args.FilePath)
	if p == "" {
		p = strings.TrimSpace(args.Path)
	}
	return norm.normalize(p)
}

func extractPatchMutatedKeys(raw json.RawMessage, norm *prunePathNormalizer) map[string]bool {
	var args struct {
		Patch string `json:"patch"`
		Diff  string `json:"diff"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return nil
	}
	patchText := args.Patch
	if strings.TrimSpace(patchText) == "" {
		patchText = args.Diff
	}
	if strings.TrimSpace(patchText) == "" {
		return nil
	}
	ops, err := parseMultiFilePatch(patchText)
	if err != nil || len(ops) == 0 {
		return nil
	}
	keys := make(map[string]bool, len(ops))
	for _, op := range ops {
		if k, _ := norm.normalize(op.path); k != "" {
			keys[k] = true
		}
		if k, _ := norm.normalize(op.moveTo); k != "" {
			keys[k] = true
		}
	}
	if len(keys) == 0 {
		return nil
	}
	return keys
}

// classifySupersededRead checks strictly within the message's own quantized
// 12-message chunk [0, chunkEnd) so earlier chunks stay 100% byte-identical
// across future turns and stride advances (preserving upstream KV-cache hits).
func classifySupersededRead(exchanges []pruneToolExchange, idx, cutoff int) supersededReadKind {
	cur := exchanges[idx]
	if cur.readSpan.pathKey == "" {
		return readNotSuperseded
	}
	chunkEnd := min(cutoff, ((cur.msgIdx/compactChunkStride)+1)*compactChunkStride)
	sawCoveringRead := false
	for k := idx + 1; k < len(exchanges); k++ {
		later := exchanges[k]
		if later.msgIdx >= chunkEnd {
			break
		}
		if later.mutatedKeys[cur.readSpan.pathKey] {
			return readSupersededByEdit
		}
		if later.readSpan.pathKey == cur.readSpan.pathKey &&
			!later.readSpan.truncated &&
			later.readSpan.startLine <= cur.readSpan.startLine &&
			later.readSpan.endLine >= cur.readSpan.endLine {
			sawCoveringRead = true
		}
	}
	if sawCoveringRead {
		return readSupersededByRead
	}
	return readNotSuperseded
}

func splitEmbeddedRuleBlocks(s string) (body, rules string) {
	if !strings.Contains(s, "<subdirectory_instructions ") && !strings.Contains(s, "<rules_budget_notice ") {
		return s, ""
	}
	type ruleTagPair struct{ open, close string }
	tags := []ruleTagPair{
		{open: "<subdirectory_instructions ", close: "</subdirectory_instructions>"},
		{open: "<rules_budget_notice ", close: "</rules_budget_notice>"},
	}
	rem := s
	var kept []string
	for {
		bestStart := -1
		var bestTag ruleTagPair
		for _, tg := range tags {
			if pos := strings.Index(rem, tg.open); pos >= 0 && (bestStart < 0 || pos < bestStart) {
				bestStart = pos
				bestTag = tg
			}
		}
		if bestStart < 0 {
			break
		}
		endRel := strings.Index(rem[bestStart:], bestTag.close)
		if endRel < 0 {
			break
		}
		spanEnd := bestStart + endRel + len(bestTag.close)
		kept = append(kept, strings.TrimSpace(rem[bestStart:spanEnd]))
		rem = strings.TrimRight(rem[:bestStart], " \t\r\n") + "\n" + strings.TrimLeft(rem[spanEnd:], " \t\r\n")
	}
	if len(kept) == 0 {
		return s, ""
	}
	return strings.TrimRight(rem, " \t\r\n"), strings.Join(kept, "\n\n")
}

func attachPreservedRules(prunedBody, rules string) string {
	if rules == "" {
		return prunedBody
	}
	return prunedBody + "\n\n" + rules
}

// splitBashForegroundLogHandle peels a trailing `[Foreground log: bash action=output ...]`
// paging handle appended by turnCommandLogs so pruning an old bash result keeps
// the exact handle for on-demand log inspection without re-running the command.
func splitBashForegroundLogHandle(s string) (body, handle string) {
	trimmed := strings.TrimRight(s, " \t\r\n")
	const prefix = "[Foreground log: bash action=output "
	idx := strings.LastIndex(trimmed, prefix)
	if idx < 0 || !strings.HasSuffix(trimmed, "]") {
		return s, ""
	}
	if idx > 0 && trimmed[idx-1] != '\n' {
		return s, ""
	}
	cand := trimmed[idx:]
	if strings.Contains(cand, "\n") {
		return s, ""
	}
	return strings.TrimRight(trimmed[:idx], " \t\r\n"), cand
}

func pruneToolOutputsBeforeWithNorm(msgs []Message, cutoff, minToolLen int, richPreview bool, norm *prunePathNormalizer) []Message {
	if cutoff <= 0 {
		return msgs
	}
	if cutoff > len(msgs) {
		cutoff = len(msgs)
	}
	exchanges, posIndex := indexPruneToolExchanges(msgs, cutoff, norm)
	out := make([]Message, len(msgs))
	copy(out, msgs)
	for i := 0; i < cutoff; i++ {
		if out[i].Role == RoleAssistant {
			changed := false
			blocks := append([]ContentBlock(nil), out[i].Content...)
			for j := range blocks {
				if blocks[j].Type != ContentToolUse || len(blocks[j].Input) <= minToolLen {
					continue
				}
				if compacted, ok := compactOldToolUseInput(blocks[j].Name, blocks[j].Input); ok {
					blocks[j].Input = compacted
					changed = true
				}
			}
			if changed {
				out[i].Content = blocks
			}
			continue
		}
		if out[i].Role != RoleUser {
			continue
		}
		changed := false
		blocks := append([]ContentBlock(nil), out[i].Content...)
		for j := range blocks {
			switch blocks[j].Type {
			case ContentToolResult:
				if strings.HasPrefix(blocks[j].Text, "[tool output pruned:") {
					continue
				}
				body, rules := splitEmbeddedRuleBlocks(ensureValidUTF8(blocks[j].Text))
				var toolName string
				if exIdx, ok := posIndex[[2]int{i, j}]; ok {
					ex := exchanges[exIdx]
					toolName = ex.toolName
					if len(body) >= compactStaleReadMinLen {
						switch classifySupersededRead(exchanges, exIdx, cutoff) {
						case readSupersededByEdit:
							blocks[j].Text = attachPreservedRules(
								fmt.Sprintf("[tool output pruned: %d chars; stale read of %s superseded by a later edit — call read if current lines are needed]", len(body), ex.readSpan.displayPath),
								rules,
							)
							changed = true
							continue
						case readSupersededByRead:
							blocks[j].Text = attachPreservedRules(
								fmt.Sprintf("[tool output pruned: %d chars; duplicate read of %s superseded by a later read]", len(body), ex.readSpan.displayPath),
								rules,
							)
							changed = true
							continue
						}
					}
				}
				if len(body) > minToolLen {
					var logHandle string
					if toolName == "bash" {
						body, logHandle = splitBashForegroundLogHandle(body)
					}
					summarized := summarizePrunedToolOutputForCall(body, toolName, blocks[j].IsError, richPreview)
					if logHandle != "" {
						summarized += "\n" + logHandle
					}
					blocks[j].Text = attachPreservedRules(summarized, rules)
					changed = true
				}
			case ContentMedia:
				if blocks[j].Data != "" {
					blocks[j] = ContentBlock{
						Type: ContentText,
						Text: fmt.Sprintf("[omitted earlier media attachment: %s (%s)]", blocks[j].Filename, blocks[j].MimeType),
					}
					changed = true
				}
			}
		}
		if changed {
			out[i].Content = blocks
		}
	}
	return out
}

func compactOldToolUseInput(toolName string, raw json.RawMessage) (json.RawMessage, bool) {
	if len(raw) == 0 || strings.Contains(string(raw), `"_prunedInput"`) {
		return nil, false
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil || len(obj) == 0 {
		return nil, false
	}
	summary := map[string]any{
		"_prunedInput": fmt.Sprintf("[tool input pruned: %d chars]", len(raw)),
	}
	lower := strings.ToLower(strings.TrimSpace(toolName))
	switch lower {
	case "write", "edit", "multiedit":
		for _, key := range []string{"filePath", "path"} {
			if s, ok := obj[key].(string); ok && strings.TrimSpace(s) != "" {
				summary[key] = s
			}
		}
	case "apply_patch":
		patchText, _ := obj["patch"].(string)
		if strings.TrimSpace(patchText) == "" {
			patchText, _ = obj["diff"].(string)
		}
		if ops, err := parseMultiFilePatch(patchText); err == nil && len(ops) > 0 {
			files := make([]string, 0, len(ops))
			for _, op := range ops {
				if op.moveTo != "" && op.moveTo != op.path {
					files = append(files, op.path+" -> "+op.moveTo)
				} else if op.path != "" {
					files = append(files, op.path)
				}
			}
			if len(files) > 0 {
				summary["files"] = files
			}
		}
	default:
		for _, key := range []string{"filePath", "path", "action", "server", "tool", "pattern", "query"} {
			if s, ok := obj[key].(string); ok && strings.TrimSpace(s) != "" {
				if len(s) > 160 {
					s = utf8Prefix(s, 160) + "..."
				}
				summary[key] = s
			}
		}
		if cmd, ok := obj["command"].(string); ok && strings.TrimSpace(cmd) != "" {
			if len(cmd) > 160 {
				cmd = utf8Prefix(cmd, 160) + "..."
			}
			summary["command"] = cmd
		}
	}
	marshaled, err := json.Marshal(summary)
	if err != nil || len(marshaled) >= len(raw)/2 {
		return nil, false
	}
	return json.RawMessage(marshaled), true
}

func summarizePrunedToolOutput(s string, richPreview bool) string {
	return summarizePrunedToolOutputForCall(s, "", false, richPreview)
}

func summarizePrunedToolOutputForCall(s, toolName string, isError, richPreview bool) string {
	if toolName == "hoxi_computer" && !isError {
		if summary, ok := summarizeComputerToolOutput(s); ok {
			return summary
		}
	}
	norm := ensureValidUTF8(strings.ReplaceAll(s, "\r\n", "\n"))
	lines := strings.Split(norm, "\n")

	headLines, tailLines := 4, 6
	maxHead, maxTail := 360, 520
	if richPreview {
		headLines, tailLines = 8, 8
		maxHead, maxTail = 640, 720
	}

	inspectDiag := isError || toolName == "bash" || strings.Contains(norm, "\nDiagnostics:\n")
	maxDiag := 4
	if richPreview {
		maxDiag = 6
	}

	// Passing command logs without diagnostics anywhere in the omitted span can
	// use a tighter head/tail window without losing compiler or test errors.
	if toolName == "bash" && !isError {
		bashHead, bashTail := 2, 4
		bashMaxHead, bashMaxTail := 180, 320
		if richPreview {
			bashHead, bashTail = 3, 5
			bashMaxHead, bashMaxTail = 240, 420
		}
		hasOmittedDiag := len(lines) > bashHead+bashTail &&
			len(extractDiagnosticSignalLines(lines[bashHead:len(lines)-bashTail], 1)) > 0
		if !hasOmittedDiag {
			headLines, tailLines = bashHead, bashTail
			maxHead, maxTail = bashMaxHead, bashMaxTail
			inspectDiag = false
		}
	}

	var diagLines []string
	if inspectDiag && len(lines) > headLines+tailLines {
		diagLines = extractDiagnosticSignalLines(lines[headLines:len(lines)-tailLines], maxDiag)
	}

	var preview string
	if len(lines) <= headLines+tailLines {
		if len(norm) > maxHead+maxTail {
			preview = utf8Prefix(norm, maxHead) + "\n... [middle omitted] ...\n" + utf8Suffix(norm, maxTail)
		} else {
			preview = norm
		}
	} else {
		head := strings.Join(lines[:headLines], "\n")
		if len(head) > maxHead {
			head = utf8Prefix(head, maxHead) + "..."
		}
		tail := strings.Join(lines[len(lines)-tailLines:], "\n")
		if len(tail) > maxTail {
			tail = "..." + utf8Suffix(tail, maxTail)
		}
		omitted := len(lines) - headLines - tailLines
		if len(diagLines) > 0 {
			preview = fmt.Sprintf(
				"%s\n... [%d lines omitted; key diagnostics preserved:]\n%s\n...\n%s",
				head, omitted, strings.Join(diagLines, "\n"), tail,
			)
		} else {
			preview = fmt.Sprintf("%s\n... [%d lines omitted] ...\n%s", head, omitted, tail)
		}
	}

	switch toolName {
	case "", "read":
		return fmt.Sprintf("[tool output pruned: %d chars; use read with offset/limit only if specific omitted lines are needed]\n%s", len(s), preview)
	case "bash":
		return fmt.Sprintf("[tool output pruned: %d chars (bash); re-run a targeted check or inspect saved log only if omitted lines are needed]\n%s", len(s), preview)
	default:
		return fmt.Sprintf("[tool output pruned: %d chars (%s); narrow query/path if omitted lines are needed]\n%s", len(s), toolName, preview)
	}
}

func extractDiagnosticSignalLines(lines []string, maxKeep int) []string {
	if maxKeep <= 0 || len(lines) == 0 {
		return nil
	}
	var out []string
	seen := make(map[string]bool, maxKeep)
	for _, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		if !isDiagnosticSignalLine(trimmed) {
			continue
		}
		if len(trimmed) > 160 {
			trimmed = utf8Prefix(trimmed, 160) + "..."
		}
		if seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		out = append(out, "  "+trimmed)
		if len(out) >= maxKeep {
			break
		}
	}
	return out
}

func isDiagnosticSignalLine(trimmed string) bool {
	if len(trimmed) < 4 {
		return false
	}
	for _, noisy := range []string{"=== RUN ", "=== PAUSE ", "=== CONT ", "--- PASS:", "PASS", "ok  \t", "ok \t"} {
		if strings.HasPrefix(trimmed, noisy) {
			return false
		}
	}
	if hasFileLineDiagnostic(trimmed) {
		return true
	}
	for _, marker := range []string{
		"--- FAIL:", "FAIL\t", "FAIL ",
		"panic:", "fatal:", "FATAL:",
		"error:", "Error:", "ERROR:", "error[E",
		"npm ERR!", "AssertionError", "SyntaxError", "TypeError", "ReferenceError",
		"Traceback (most recent call last)", "exit status ",
	} {
		if strings.Contains(trimmed, marker) {
			return true
		}
	}
	return false
}

var diagFileExts = []string{
	".go:", ".ts:", ".tsx:", ".js:", ".jsx:", ".py:", ".rs:",
	".java:", ".c:", ".cc:", ".cpp:", ".h:", ".hpp:", ".cs:",
	".rb:", ".swift:", ".kt:", ".vue:", ".svelte:", ".sql:", ".sh:",
	".ts(", ".tsx(", ".js(", ".cs(",
}

func hasFileLineDiagnostic(s string) bool {
	if idx := strings.Index(s, `", line `); idx > 0 && idx+8 < len(s) && s[idx+8] >= '1' && s[idx+8] <= '9' {
		return true
	}
	for _, ext := range diagFileExts {
		if idx := strings.Index(s, ext); idx > 0 {
			next := idx + len(ext)
			if next < len(s) && s[next] >= '1' && s[next] <= '9' {
				return true
			}
		}
	}
	return false
}
