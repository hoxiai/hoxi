package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type applyPatchTool struct{}

func (t *applyPatchTool) Name() string     { return "apply_patch" }
func (t *applyPatchTool) IsReadOnly() bool { return false }
func (t *applyPatchTool) Description() string {
	return "Atomically apply a multi-file patch (*** Begin Patch envelope or unified diff) across workspace files and run compiler diagnostics."
}
func (t *applyPatchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"patch": map[string]any{
				"type":        "string",
				"description": "Multi-file patch in '*** Begin Patch ... *** End Patch' format or standard unified diff ('--- a/... +++ b/...')",
			},
		},
		"required": []string{"patch"},
	}
}

type patchOpKind string

const (
	patchOpAdd    patchOpKind = "add"
	patchOpUpdate patchOpKind = "update"
	patchOpDelete patchOpKind = "delete"
)

type patchHunkLine struct {
	prefix byte // ' ', '-', '+'
	text   string
}

type patchFileOp struct {
	kind     patchOpKind
	path     string
	moveTo   string
	addLines []string
	hunks    [][]patchHunkLine
}

type stagedFileMutation struct {
	op         patchFileOp
	absSource  string
	absTarget  string
	newContent []byte
	fileMode   os.FileMode
}

func (t *applyPatchTool) Execute(ctx context.Context, tc ToolContext, input json.RawMessage) (ToolOutput, error) {
	var args struct {
		Patch string `json:"patch"`
		Diff  string `json:"diff"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return ToolOutput{Title: "apply_patch", IsError: true}, err
	}
	rawPatch := strings.TrimSpace(args.Patch)
	if rawPatch == "" {
		rawPatch = strings.TrimSpace(args.Diff)
	}
	if rawPatch == "" {
		return ToolOutput{Title: "apply_patch", IsError: true}, fmt.Errorf("patch text is required")
	}

	ops, err := parseMultiFilePatch(rawPatch)
	if err != nil {
		return ToolOutput{Title: "apply_patch", IsError: true}, err
	}
	if len(ops) == 0 {
		return ToolOutput{Title: "apply_patch", IsError: true}, fmt.Errorf("no file operations found in patch")
	}

	// Stage all file mutations in memory first so a single bad hunk aborts cleanly with zero partial writes.
	virtualFS := make(map[string][]byte)
	deletedFS := make(map[string]bool)
	staged := make([]stagedFileMutation, 0, len(ops))

	readVirtual := func(absPath string) ([]byte, error) {
		if deletedFS[absPath] {
			return nil, os.ErrNotExist
		}
		if b, ok := virtualFS[absPath]; ok {
			return b, nil
		}
		return os.ReadFile(absPath)
	}

	for _, op := range ops {
		srcAbs, err := resolveWorkspacePath(tc.WorktreeDir, op.path, true)
		if err != nil {
			return ToolOutput{Title: op.path, IsError: true}, err
		}
		if isSensitiveAgentPath(srcAbs) {
			return ToolOutput{Title: op.path, IsError: true}, fmt.Errorf("refusing to patch secret/credential file %s", op.path)
		}
		dstAbs := srcAbs
		if op.moveTo != "" {
			dstAbs, err = resolveWorkspacePath(tc.WorktreeDir, op.moveTo, true)
			if err != nil {
				return ToolOutput{Title: op.moveTo, IsError: true}, err
			}
			if isSensitiveAgentPath(dstAbs) {
				return ToolOutput{Title: op.moveTo, IsError: true}, fmt.Errorf("refusing to move into secret/credential file %s", op.moveTo)
			}
		}

		switch op.kind {
		case patchOpAdd:
			content := []byte(strings.Join(op.addLines, "\n") + "\n")
			if len(op.addLines) == 0 {
				content = []byte{}
			}
			virtualFS[dstAbs] = content
			delete(deletedFS, dstAbs)
			staged = append(staged, stagedFileMutation{op: op, absSource: srcAbs, absTarget: dstAbs, newContent: content})

		case patchOpDelete:
			if _, err := readVirtual(srcAbs); err != nil {
				return ToolOutput{Title: op.path, IsError: true}, fmt.Errorf("cannot delete %s: %w", op.path, err)
			}
			delete(virtualFS, srcAbs)
			deletedFS[srcAbs] = true
			staged = append(staged, stagedFileMutation{op: op, absSource: srcAbs, absTarget: srcAbs})

		case patchOpUpdate:
			var srcMode os.FileMode
			if st, sErr := os.Stat(srcAbs); sErr == nil && st.Mode().IsRegular() {
				srcMode = st.Mode().Perm()
			}
			origBytes, err := readVirtual(srcAbs)
			if err != nil {
				return ToolOutput{Title: op.path, IsError: true}, fmt.Errorf("cannot read %s: %w", op.path, err)
			}
			useCRLF := strings.Contains(string(origBytes), "\r\n")
			normSrc := strings.ReplaceAll(string(origBytes), "\r\n", "\n")
			updated, err := applyPatchHunks(normSrc, op.hunks)
			if err != nil {
				return ToolOutput{Title: op.path, IsError: true}, fmt.Errorf("%s: %w", op.path, err)
			}
			if useCRLF {
				updated = strings.ReplaceAll(updated, "\n", "\r\n")
			}
			outBytes := []byte(updated)
			if dstAbs != srcAbs {
				delete(virtualFS, srcAbs)
				deletedFS[srcAbs] = true
			}
			virtualFS[dstAbs] = outBytes
			delete(deletedFS, dstAbs)
			staged = append(staged, stagedFileMutation{op: op, absSource: srcAbs, absTarget: dstAbs, newContent: outBytes, fileMode: srcMode})
		}
	}

	var summaryLines []string
	var diagTargets []string
	seenDiag := make(map[string]bool)

	for _, m := range staged {
		switch m.op.kind {
		case patchOpDelete:
			if err := os.Remove(m.absSource); err != nil && !os.IsNotExist(err) {
				return ToolOutput{Title: m.op.path, IsError: true}, err
			}
			summaryLines = append(summaryLines, "D "+m.op.path)
		case patchOpAdd, patchOpUpdate:
			if err := os.MkdirAll(filepath.Dir(m.absTarget), 0o755); err != nil {
				return ToolOutput{Title: m.op.path, IsError: true}, err
			}
			if err := writeAtomicFileMode(m.absTarget, m.newContent, m.fileMode); err != nil {
				return ToolOutput{Title: m.op.path, IsError: true}, err
			}
			if m.absTarget != m.absSource {
				_ = os.Remove(m.absSource)
				summaryLines = append(summaryLines, "R "+m.op.path+" -> "+m.op.moveTo)
			} else if m.op.kind == patchOpAdd {
				summaryLines = append(summaryLines, "A "+m.op.path)
			} else {
				summaryLines = append(summaryLines, "M "+m.op.path)
			}
			if !seenDiag[m.absTarget] {
				seenDiag[m.absTarget] = true
				diagTargets = append(diagTargets, m.absTarget)
			}
		}
	}

	out := fmt.Sprintf("applied patch across %d file operation(s):\n%s", len(staged), strings.Join(summaryLines, "\n"))
	if tc.Diagnostics != nil && len(diagTargets) > 0 {
		var diags []string
		seenDiagText := make(map[string]bool)
		for _, target := range diagTargets {
			if d := strings.TrimSpace(tc.Diagnostics(ctx, tc.WorktreeDir, target)); d != "" && !seenDiagText[d] {
				seenDiagText[d] = true
				diags = append(diags, d)
			}
		}
		if len(diags) > 0 {
			out += "\n\nDiagnostics:\n" + strings.Join(diags, "\n")
		}
	}
	return ToolOutput{
		Title:    fmt.Sprintf("%d files patched", len(staged)),
		Output:   out,
		Metadata: map[string]any{"files": summaryLines},
	}, nil
}

func parseMultiFilePatch(raw string) ([]patchFileOp, error) {
	norm := strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(norm, "\n")
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "*** Begin Patch") || strings.HasPrefix(t, "*** Add File:") ||
			strings.HasPrefix(t, "*** Update File:") || strings.HasPrefix(t, "*** Delete File:") {
			return parseEnvelopePatch(lines)
		}
	}
	return parseUnifiedDiffPatch(lines)
}

func parseEnvelopePatch(lines []string) ([]patchFileOp, error) {
	var ops []patchFileOp
	i := 0
	for i < len(lines) {
		trimmed := strings.TrimSpace(lines[i])
		switch {
		case trimmed == "" || strings.HasPrefix(trimmed, "*** Begin Patch") || strings.HasPrefix(trimmed, "*** End Patch"):
			i++
		case strings.HasPrefix(trimmed, "*** Add File:"):
			p := strings.TrimSpace(strings.TrimPrefix(trimmed, "*** Add File:"))
			i++
			var content []string
			for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "*** ") {
				line := lines[i]
				if strings.HasPrefix(line, "+") {
					content = append(content, line[1:])
				} else {
					content = append(content, line)
				}
				i++
			}
			ops = append(ops, patchFileOp{kind: patchOpAdd, path: p, addLines: content})

		case strings.HasPrefix(trimmed, "*** Delete File:"):
			p := strings.TrimSpace(strings.TrimPrefix(trimmed, "*** Delete File:"))
			ops = append(ops, patchFileOp{kind: patchOpDelete, path: p})
			i++

		case strings.HasPrefix(trimmed, "*** Update File:"):
			p := strings.TrimSpace(strings.TrimPrefix(trimmed, "*** Update File:"))
			i++
			moveTo := ""
			if i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "*** Move to:") {
				moveTo = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i]), "*** Move to:"))
				i++
			}
			var hunks [][]patchHunkLine
			var cur []patchHunkLine
			flushHunk := func() {
				if len(cur) > 0 {
					hunks = append(hunks, cur)
					cur = nil
				}
			}
			for i < len(lines) {
				lt := strings.TrimSpace(lines[i])
				if strings.HasPrefix(lt, "*** End of File") {
					i++
					continue
				}
				if strings.HasPrefix(lt, "*** ") {
					break
				}
				rawLine := lines[i]
				if strings.HasPrefix(rawLine, "@@") {
					flushHunk()
					i++
					continue
				}
				if rawLine == "" {
					cur = append(cur, patchHunkLine{prefix: ' ', text: ""})
				} else {
					switch rawLine[0] {
					case ' ', '+', '-':
						cur = append(cur, patchHunkLine{prefix: rawLine[0], text: rawLine[1:]})
					default:
						cur = append(cur, patchHunkLine{prefix: ' ', text: rawLine})
					}
				}
				i++
			}
			flushHunk()
			ops = append(ops, patchFileOp{kind: patchOpUpdate, path: p, moveTo: moveTo, hunks: hunks})
		default:
			i++
		}
	}
	return ops, nil
}

func parseUnifiedDiffPatch(lines []string) ([]patchFileOp, error) {
	var ops []patchFileOp
	i := 0
	cleanDiffPath := func(s string) string {
		s = strings.TrimSpace(s)
		if tab := strings.IndexByte(s, '\t'); tab >= 0 {
			s = strings.TrimSpace(s[:tab])
		}
		if s == "/dev/null" {
			return s
		}
		s = strings.TrimPrefix(s, "a/")
		s = strings.TrimPrefix(s, "b/")
		return s
	}

	for i < len(lines) {
		if !strings.HasPrefix(lines[i], "--- ") {
			i++
			continue
		}
		oldPath := cleanDiffPath(strings.TrimPrefix(lines[i], "--- "))
		i++
		if i >= len(lines) || !strings.HasPrefix(lines[i], "+++ ") {
			return nil, fmt.Errorf("malformed unified diff: expected '+++ ' after '--- %s'", oldPath)
		}
		newPath := cleanDiffPath(strings.TrimPrefix(lines[i], "+++ "))
		i++

		var hunks [][]patchHunkLine
		var cur []patchHunkLine
		flush := func() {
			if len(cur) > 0 {
				hunks = append(hunks, cur)
				cur = nil
			}
		}
		for i < len(lines) && !strings.HasPrefix(lines[i], "--- ") && !strings.HasPrefix(lines[i], "diff --git ") {
			rawLine := lines[i]
			if strings.HasPrefix(rawLine, "@@") {
				flush()
				i++
				continue
			}
			if strings.HasPrefix(rawLine, `\ No newline`) {
				i++
				continue
			}
			if len(hunks) > 0 || len(cur) > 0 {
				if rawLine == "" && i == len(lines)-1 {
					i++
					continue
				}
				if rawLine == "" {
					cur = append(cur, patchHunkLine{prefix: ' ', text: ""})
				} else if rawLine[0] == ' ' || rawLine[0] == '+' || rawLine[0] == '-' {
					cur = append(cur, patchHunkLine{prefix: rawLine[0], text: rawLine[1:]})
				}
			}
			i++
		}
		flush()

		switch {
		case oldPath == "/dev/null" && newPath != "/dev/null":
			var added []string
			for _, h := range hunks {
				for _, hl := range h {
					if hl.prefix == '+' || hl.prefix == ' ' {
						added = append(added, hl.text)
					}
				}
			}
			ops = append(ops, patchFileOp{kind: patchOpAdd, path: newPath, addLines: added})
		case newPath == "/dev/null" && oldPath != "/dev/null":
			ops = append(ops, patchFileOp{kind: patchOpDelete, path: oldPath})
		default:
			moveTo := ""
			if newPath != "" && newPath != oldPath {
				moveTo = newPath
			}
			ops = append(ops, patchFileOp{kind: patchOpUpdate, path: oldPath, moveTo: moveTo, hunks: hunks})
		}
	}
	return ops, nil
}

func applyPatchHunks(src string, hunks [][]patchHunkLine) (string, error) {
	hasTrailingNewline := strings.HasSuffix(src, "\n")
	trimmedSrc := strings.TrimSuffix(src, "\n")
	var srcLines []string
	if trimmedSrc != "" {
		srcLines = strings.Split(trimmedSrc, "\n")
	}

	searchFrom := 0
	for hunkIdx, h := range hunks {
		var oldChunk, newChunk []string
		for _, hl := range h {
			switch hl.prefix {
			case ' ':
				oldChunk = append(oldChunk, hl.text)
				newChunk = append(newChunk, hl.text)
			case '-':
				oldChunk = append(oldChunk, hl.text)
			case '+':
				newChunk = append(newChunk, hl.text)
			}
		}
		if len(oldChunk) == 0 {
			srcLines = append(srcLines, newChunk...)
			searchFrom = len(srcLines)
			continue
		}
		pos := findChunkMatch(srcLines, oldChunk, searchFrom)
		if pos < 0 && searchFrom > 0 {
			pos = findChunkMatch(srcLines, oldChunk, 0)
		}
		if pos < 0 {
			return "", fmt.Errorf("hunk #%d did not match file contents (first expected line: %q)", hunkIdx+1, oldChunk[0])
		}
		next := make([]string, 0, len(srcLines)-len(oldChunk)+len(newChunk))
		next = append(next, srcLines[:pos]...)
		next = append(next, newChunk...)
		next = append(next, srcLines[pos+len(oldChunk):]...)
		srcLines = next
		searchFrom = pos + len(newChunk)
	}

	result := strings.Join(srcLines, "\n")
	if hasTrailingNewline && result != "" {
		result += "\n"
	}
	return result, nil
}

func findChunkMatch(srcLines, oldChunk []string, start int) int {
	norms := []func(string) string{
		func(s string) string { return s },
		func(s string) string { return strings.TrimRight(s, " \t") },
		strings.TrimSpace,
	}
	for _, norm := range norms {
		for i := start; i <= len(srcLines)-len(oldChunk); i++ {
			matched := true
			for j := range oldChunk {
				if norm(srcLines[i+j]) != norm(oldChunk[j]) {
					matched = false
					break
				}
			}
			if matched {
				return i
			}
		}
	}
	return -1
}
