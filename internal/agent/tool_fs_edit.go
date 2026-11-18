package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type writeTool struct{}

func (t *writeTool) Name() string        { return "write" }
func (t *writeTool) IsReadOnly() bool    { return false }
func (t *writeTool) Description() string { return "Create or overwrite a file inside the workspace." }
func (t *writeTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"filePath": map[string]any{"type": "string", "description": "Workspace path to write"},
			"content":  map[string]any{"type": "string", "description": "Full file content"},
		},
		"required": []string{"filePath", "content"},
	}
}

func (t *writeTool) Execute(ctx context.Context, tc ToolContext, input json.RawMessage) (ToolOutput, error) {
	var args struct {
		FilePath string `json:"filePath"`
		Path     string `json:"path"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return ToolOutput{Title: "write", IsError: true}, err
	}
	p := strings.TrimSpace(args.FilePath)
	if p == "" {
		p = strings.TrimSpace(args.Path)
	}
	target, err := resolveWorkspacePath(tc.WorktreeDir, p, !tc.PermissionVetted)
	if err != nil {
		return ToolOutput{Title: p, IsError: true}, err
	}
	if !tc.PermissionVetted && isSensitiveAgentPath(target) {
		return ToolOutput{Title: p, IsError: true}, fmt.Errorf("writing sensitive credential or key file %q is blocked", filepath.Base(target))
	}
	release, peerModified, err := coordinateFileMutation(ctx, tc, []string{target}, map[string]string{target: p})
	if err != nil {
		return ToolOutput{Title: p, IsError: true}, err
	}
	defer release()
	if err := tc.FileRevisions.CheckWrite(target, p); err != nil {
		return ToolOutput{Title: p, IsError: true}, err
	}
	prevBytes, prevErr := os.ReadFile(target)
	if peerModified(target) && prevErr == nil && len(prevBytes) > 0 {
		if err := checkEditWithPeerGuard(tc.FileRevisions, target, p, prevBytes, true); err != nil {
			return ToolOutput{Title: p, IsError: true}, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return ToolOutput{Title: p, IsError: true}, err
	}
	contentBytes := []byte(args.Content)
	if err := writeAtomicFile(target, contentBytes); err != nil {
		return ToolOutput{Title: p, IsError: true}, err
	}
	tc.FileRevisions.RecordBytes(target, contentBytes)
	recordCompletedFileEdit(tc, target)
	release()
	out := fmt.Sprintf("wrote %d bytes to %s", len(args.Content), p)
	if tc.Diagnostics != nil {
		if diag := strings.TrimSpace(tc.Diagnostics(ctx, tc.WorktreeDir, target)); diag != "" {
			out += "\n\nDiagnostics:\n" + diag
		}
	}
	out = appendSubdirectoryRules(tc, target, out)
	var beforeText string
	if prevErr == nil {
		beforeText = string(prevBytes)
	}
	return ToolOutput{
		Title:    p,
		Output:   out,
		Metadata: buildSingleFileDiffMeta(tc.WorktreeDir, target, beforeText, args.Content),
	}, nil
}

type editTool struct{}

func (t *editTool) Name() string { return "edit" }
func (t *editTool) Description() string {
	return "Replace a target text block in an existing workspace file with progressive whitespace/indent matching."
}
func (t *editTool) IsReadOnly() bool { return false }
func (t *editTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"filePath":   map[string]any{"type": "string", "description": "Workspace file path"},
			"oldString":  map[string]any{"type": "string", "description": "Exact or near-exact text block to replace"},
			"newString":  map[string]any{"type": "string", "description": "Replacement text block"},
			"replaceAll": map[string]any{"type": "boolean", "description": "Replace all occurrences"},
		},
		"required": []string{"filePath", "oldString", "newString"},
	}
}

func (t *editTool) Execute(ctx context.Context, tc ToolContext, input json.RawMessage) (ToolOutput, error) {
	var args struct {
		FilePath   string `json:"filePath"`
		Path       string `json:"path"`
		OldString  string `json:"oldString"`
		NewString  string `json:"newString"`
		ReplaceAll bool   `json:"replaceAll"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return ToolOutput{Title: "edit", IsError: true}, err
	}
	p := strings.TrimSpace(args.FilePath)
	if p == "" {
		p = strings.TrimSpace(args.Path)
	}
	target, err := resolveWorkspacePath(tc.WorktreeDir, p, !tc.PermissionVetted)
	if err != nil {
		return ToolOutput{Title: p, IsError: true}, err
	}
	if !tc.PermissionVetted && isSensitiveAgentPath(target) {
		return ToolOutput{Title: p, IsError: true}, fmt.Errorf("editing sensitive credential or key file %q is blocked", filepath.Base(target))
	}
	release, peerModified, err := coordinateFileMutation(ctx, tc, []string{target}, map[string]string{target: p})
	if err != nil {
		return ToolOutput{Title: p, IsError: true}, err
	}
	defer release()
	raw, err := os.ReadFile(target)
	if err != nil {
		return ToolOutput{Title: p, IsError: true}, err
	}
	if err := checkEditWithPeerGuard(tc.FileRevisions, target, p, raw, peerModified(target)); err != nil {
		return ToolOutput{Title: p, IsError: true}, err
	}
	useCRLF := strings.Contains(string(raw), "\r\n")
	src := strings.ReplaceAll(string(raw), "\r\n", "\n")
	oldStr := strings.ReplaceAll(args.OldString, "\r\n", "\n")
	newStr := strings.ReplaceAll(args.NewString, "\r\n", "\n")

	if oldStr == "" {
		return ToolOutput{Title: p, IsError: true}, fmt.Errorf("oldString cannot be empty")
	}
	updated, startLine, err := applyProgressiveEdit(src, oldStr, newStr, args.ReplaceAll)
	if err != nil {
		return ToolOutput{Title: p, IsError: true}, err
	}
	if useCRLF {
		updated = strings.ReplaceAll(updated, "\n", "\r\n")
	}
	updatedBytes := []byte(updated)
	if err := writeAtomicFile(target, updatedBytes); err != nil {
		return ToolOutput{Title: p, IsError: true}, err
	}
	tc.FileRevisions.RecordTargetedEdit(target, raw, updatedBytes)
	recordCompletedFileEdit(tc, target)
	release()
	snippet := numberedExcerpt(strings.ReplaceAll(updated, "\r\n", "\n"), startLine, 12)
	out := fmt.Sprintf("edited %s around line %d:\n%s", p, startLine, snippet)
	if tc.Diagnostics != nil {
		if diag := strings.TrimSpace(tc.Diagnostics(ctx, tc.WorktreeDir, target)); diag != "" {
			out += "\n\nDiagnostics:\n" + diag
		}
	}
	out = appendSubdirectoryRules(tc, target, out)
	return ToolOutput{
		Title:    p,
		Output:   out,
		Metadata: buildSingleFileDiffMeta(tc.WorktreeDir, target, string(raw), updated),
	}, nil
}

func applyProgressiveEdit(src, oldStr, newStr string, replaceAll bool) (string, int, error) {
	if count := strings.Count(src, oldStr); count > 0 {
		if count > 1 && !replaceAll {
			return "", 0, fmt.Errorf("oldString matched %d locations; include more surrounding lines or set replaceAll=true", count)
		}
		idx := strings.Index(src, oldStr)
		startLine := 1 + strings.Count(src[:idx], "\n")
		if replaceAll {
			return strings.ReplaceAll(src, oldStr, newStr), startLine, nil
		}
		return src[:idx] + newStr + src[idx+len(oldStr):], startLine, nil
	}

	srcLines := strings.Split(src, "\n")
	oldLines := strings.Split(oldStr, "\n")
	newLines := strings.Split(newStr, "\n")

	type matchSpan struct {
		start int
		end   int
	}
	findMatches := func(norm func(string) string) []matchSpan {
		var spans []matchSpan
		if len(oldLines) == 0 || len(oldLines) > len(srcLines) {
			return nil
		}
		for i := 0; i <= len(srcLines)-len(oldLines); i++ {
			ok := true
			for j := range oldLines {
				if norm(srcLines[i+j]) != norm(oldLines[j]) {
					ok = false
					break
				}
			}
			if ok {
				spans = append(spans, matchSpan{start: i, end: i + len(oldLines)})
			}
		}
		return spans
	}

	trimmedMatch := false
	spans := findMatches(func(s string) string { return strings.TrimRight(s, " \t") })
	if len(spans) == 0 {
		spans = findMatches(strings.TrimSpace)
		trimmedMatch = len(spans) > 0
	}
	if len(spans) == 0 {
		return "", 0, fmt.Errorf("oldString not found in file; read the target lines first to confirm exact content")
	}
	if len(spans) > 1 && !replaceAll {
		var lineNums []string
		for _, sp := range spans {
			lineNums = append(lineNums, fmt.Sprintf("%d", sp.start+1))
		}
		return "", 0, fmt.Errorf("oldString matched %d locations (lines %s); provide more context", len(spans), strings.Join(lineNums, ", "))
	}

	var out []string
	cursor := 0
	for _, sp := range spans {
		out = append(out, srcLines[cursor:sp.start]...)
		repl := newLines
		if trimmedMatch {
			repl = rebaseMatchedIndentation(srcLines[sp.start:sp.end], oldLines, newLines)
		}
		out = append(out, repl...)
		cursor = sp.end
		if !replaceAll {
			break
		}
	}
	out = append(out, srcLines[cursor:]...)
	return strings.Join(out, "\n"), spans[0].start + 1, nil
}

func leadingHorizWS(s string) string {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return s[:i]
}

func commonLeadingIndent(lines []string) (string, bool) {
	hasNonEmpty := false
	prefix := ""
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		ws := leadingHorizWS(line)
		if !hasNonEmpty {
			prefix = ws
			hasNonEmpty = true
			continue
		}
		limit := min(len(prefix), len(ws))
		k := 0
		for k < limit && prefix[k] == ws[k] {
			k++
		}
		prefix = prefix[:k]
	}
	return prefix, hasNonEmpty
}

func allNonEmptyHavePrefix(lines []string, prefix string) bool {
	if prefix == "" {
		return true
	}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !strings.HasPrefix(line, prefix) {
			return false
		}
	}
	return true
}

func rebaseMatchedIndentation(srcSpan, oldLines, newLines []string) []string {
	srcIndent, okSrc := commonLeadingIndent(srcSpan)
	oldIndent, okOld := commonLeadingIndent(oldLines)
	if !okSrc || !okOld || srcIndent == oldIndent {
		return newLines
	}
	if !allNonEmptyHavePrefix(newLines, oldIndent) {
		return newLines
	}
	if oldIndent == "" && srcIndent != "" && allNonEmptyHavePrefix(newLines, srcIndent) {
		return newLines
	}
	trimLen := len(oldIndent)
	rebased := make([]string, len(newLines))
	for i, line := range newLines {
		if strings.TrimSpace(line) == "" {
			rebased[i] = line
		} else {
			rebased[i] = srcIndent + line[trimLen:]
		}
	}
	return rebased
}

func numberedExcerpt(src string, centerLine, radius int) string {
	lines := strings.Split(src, "\n")
	start := max(1, centerLine-2)
	end := min(len(lines), centerLine+radius)
	var b strings.Builder
	for i := start; i <= end; i++ {
		fmt.Fprintf(&b, "%d: %s\n", i, lines[i-1])
	}
	return b.String()
}

func writeAtomicFile(target string, data []byte) error {
	return writeAtomicFileMode(target, data, 0)
}

func writeAtomicFileMode(target string, data []byte, explicitMode os.FileMode) error {
	mode := explicitMode.Perm()
	if mode == 0 {
		if st, err := os.Stat(target); err == nil && st.Mode().IsRegular() {
			mode = st.Mode().Perm()
		} else {
			mode = 0o644
		}
	}
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".hoxi-edit-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	chmodErr := tmp.Chmod(mode)
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if chmodErr != nil {
		if err := os.Chmod(tmpName, mode); err != nil {
			return err
		}
	}
	if err := os.Rename(tmpName, target); err != nil {
		return err
	}
	cleanup = false
	return nil
}

type multiEditTool struct{}

func (t *multiEditTool) Name() string { return "multiedit" }
func (t *multiEditTool) Description() string {
	return "Apply multiple ordered edits to a single file atomically in one pass, running post-edit diagnostics once."
}
func (t *multiEditTool) IsReadOnly() bool { return false }
func (t *multiEditTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"filePath": map[string]any{"type": "string", "description": "Workspace file path"},
			"edits": map[string]any{
				"type":        "array",
				"description": "Ordered list of text replacements applied sequentially to the file",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"oldString":  map[string]any{"type": "string", "description": "Target text block to replace"},
						"newString":  map[string]any{"type": "string", "description": "Replacement text block"},
						"replaceAll": map[string]any{"type": "boolean", "description": "Replace all occurrences"},
					},
					"required": []string{"oldString", "newString"},
				},
			},
		},
		"required": []string{"filePath", "edits"},
	}
}

func (t *multiEditTool) Execute(ctx context.Context, tc ToolContext, input json.RawMessage) (ToolOutput, error) {
	var args struct {
		FilePath string `json:"filePath"`
		Path     string `json:"path"`
		Edits    []struct {
			OldString  string `json:"oldString"`
			NewString  string `json:"newString"`
			ReplaceAll bool   `json:"replaceAll"`
		} `json:"edits"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return ToolOutput{Title: "multiedit", IsError: true}, err
	}
	p := strings.TrimSpace(args.FilePath)
	if p == "" {
		p = strings.TrimSpace(args.Path)
	}
	if len(args.Edits) == 0 {
		return ToolOutput{Title: p, IsError: true}, fmt.Errorf("edits array cannot be empty")
	}
	target, err := resolveWorkspacePath(tc.WorktreeDir, p, !tc.PermissionVetted)
	if err != nil {
		return ToolOutput{Title: p, IsError: true}, err
	}
	if !tc.PermissionVetted && isSensitiveAgentPath(target) {
		return ToolOutput{Title: p, IsError: true}, fmt.Errorf("editing sensitive credential or key file %q is blocked", filepath.Base(target))
	}
	release, peerModified, err := coordinateFileMutation(ctx, tc, []string{target}, map[string]string{target: p})
	if err != nil {
		return ToolOutput{Title: p, IsError: true}, err
	}
	defer release()
	raw, err := os.ReadFile(target)
	if err != nil {
		return ToolOutput{Title: p, IsError: true}, err
	}
	if err := checkEditWithPeerGuard(tc.FileRevisions, target, p, raw, peerModified(target)); err != nil {
		return ToolOutput{Title: p, IsError: true}, err
	}
	useCRLF := strings.Contains(string(raw), "\r\n")
	cur := strings.ReplaceAll(string(raw), "\r\n", "\n")

	firstLine := 1
	for idx, ed := range args.Edits {
		oldStr := strings.ReplaceAll(ed.OldString, "\r\n", "\n")
		newStr := strings.ReplaceAll(ed.NewString, "\r\n", "\n")
		if oldStr == "" {
			return ToolOutput{Title: p, IsError: true}, fmt.Errorf("edit #%d: oldString cannot be empty", idx+1)
		}
		next, line, applyErr := applyProgressiveEdit(cur, oldStr, newStr, ed.ReplaceAll)
		if applyErr != nil {
			return ToolOutput{Title: p, IsError: true}, fmt.Errorf("edit #%d failed (file left unchanged): %w", idx+1, applyErr)
		}
		if idx == 0 {
			firstLine = line
		}
		cur = next
	}

	finalText := cur
	if useCRLF {
		finalText = strings.ReplaceAll(finalText, "\n", "\r\n")
	}
	finalBytes := []byte(finalText)
	if err := writeAtomicFile(target, finalBytes); err != nil {
		return ToolOutput{Title: p, IsError: true}, err
	}
	tc.FileRevisions.RecordTargetedEdit(target, raw, finalBytes)
	recordCompletedFileEdit(tc, target)
	release()
	snippet := numberedExcerpt(cur, firstLine, 10)
	out := fmt.Sprintf("applied %d edits atomically to %s (first change at line %d):\n%s", len(args.Edits), p, firstLine, snippet)
	if tc.Diagnostics != nil {
		if diag := strings.TrimSpace(tc.Diagnostics(ctx, tc.WorktreeDir, target)); diag != "" {
			out += "\n\nDiagnostics:\n" + diag
		}
	}
	out = appendSubdirectoryRules(tc, target, out)
	return ToolOutput{
		Title:    p,
		Output:   out,
		Metadata: buildSingleFileDiffMeta(tc.WorktreeDir, target, string(raw), finalText),
	}, nil
}

const maxInlineDiffMetadataBytes = 128 * 1024

func workspaceRelativeSlash(worktreeDir, targetAbs string) string {
	cleanTarget := filepath.Clean(strings.TrimSpace(targetAbs))
	if cleanRoot := filepath.Clean(strings.TrimSpace(worktreeDir)); cleanRoot != "" {
		if rel, err := filepath.Rel(cleanRoot, cleanTarget); err == nil && rel != "." && rel != ".." && !strings.HasPrefix(filepath.ToSlash(rel), "../") {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(cleanTarget)
}

func buildSingleFileDiffMeta(worktreeDir, targetAbs, before, after string) map[string]any {
	rel := workspaceRelativeSlash(worktreeDir, targetAbs)
	adds, dels := countTextDiffLines(before, after)
	fd := map[string]any{
		"file":      rel,
		"filePath":  targetAbs,
		"additions": adds,
		"deletions": dels,
	}
	if len(before)+len(after) <= maxInlineDiffMetadataBytes {
		fd["before"] = before
		fd["after"] = after
	}
	return map[string]any{
		"filepath": targetAbs,
		"filediff": fd,
	}
}

func countTextDiffLines(before, after string) (int, int) {
	if before == after {
		return 0, 0
	}
	splitLines := func(s string) []string {
		if s == "" {
			return nil
		}
		norm := strings.TrimSuffix(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
		if norm == "" {
			return []string{""}
		}
		return strings.Split(norm, "\n")
	}
	bLines := splitLines(before)
	aLines := splitLines(after)
	prefix := 0
	for prefix < len(bLines) && prefix < len(aLines) && bLines[prefix] == aLines[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(bLines)-prefix && suffix < len(aLines)-prefix &&
		bLines[len(bLines)-1-suffix] == aLines[len(aLines)-1-suffix] {
		suffix++
	}
	return max(0, len(aLines)-prefix-suffix), max(0, len(bLines)-prefix-suffix)
}
