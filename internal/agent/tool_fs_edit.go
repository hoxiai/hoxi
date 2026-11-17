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
	target, err := resolveWorkspacePath(tc.WorktreeDir, p, true)
	if err != nil {
		return ToolOutput{Title: p, IsError: true}, err
	}
	if !tc.PermissionVetted && isSensitiveAgentPath(target) {
		return ToolOutput{Title: p, IsError: true}, fmt.Errorf("writing sensitive credential or key file %q is blocked", filepath.Base(target))
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return ToolOutput{Title: p, IsError: true}, err
	}
	if err := writeAtomicFile(target, []byte(args.Content)); err != nil {
		return ToolOutput{Title: p, IsError: true}, err
	}
	out := fmt.Sprintf("wrote %d bytes to %s", len(args.Content), p)
	if tc.Diagnostics != nil {
		if diag := strings.TrimSpace(tc.Diagnostics(ctx, tc.WorktreeDir, target)); diag != "" {
			out += "\n\nDiagnostics:\n" + diag
		}
	}
	return ToolOutput{Title: p, Output: out}, nil
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
	target, err := resolveWorkspacePath(tc.WorktreeDir, p, true)
	if err != nil {
		return ToolOutput{Title: p, IsError: true}, err
	}
	if !tc.PermissionVetted && isSensitiveAgentPath(target) {
		return ToolOutput{Title: p, IsError: true}, fmt.Errorf("editing sensitive credential or key file %q is blocked", filepath.Base(target))
	}
	raw, err := os.ReadFile(target)
	if err != nil {
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
	if err := writeAtomicFile(target, []byte(updated)); err != nil {
		return ToolOutput{Title: p, IsError: true}, err
	}
	snippet := numberedExcerpt(strings.ReplaceAll(updated, "\r\n", "\n"), startLine, 12)
	out := fmt.Sprintf("edited %s around line %d:\n%s", p, startLine, snippet)
	if tc.Diagnostics != nil {
		if diag := strings.TrimSpace(tc.Diagnostics(ctx, tc.WorktreeDir, target)); diag != "" {
			out += "\n\nDiagnostics:\n" + diag
		}
	}
	return ToolOutput{Title: p, Output: out}, nil
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

	spans := findMatches(func(s string) string { return strings.TrimRight(s, " \t") })
	if len(spans) == 0 {
		spans = findMatches(strings.TrimSpace)
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
		out = append(out, newLines...)
		cursor = sp.end
		if !replaceAll {
			break
		}
	}
	out = append(out, srcLines[cursor:]...)
	return strings.Join(out, "\n"), spans[0].start + 1, nil
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
	target, err := resolveWorkspacePath(tc.WorktreeDir, p, true)
	if err != nil {
		return ToolOutput{Title: p, IsError: true}, err
	}
	if !tc.PermissionVetted && isSensitiveAgentPath(target) {
		return ToolOutput{Title: p, IsError: true}, fmt.Errorf("editing sensitive credential or key file %q is blocked", filepath.Base(target))
	}
	raw, err := os.ReadFile(target)
	if err != nil {
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
	if err := writeAtomicFile(target, []byte(finalText)); err != nil {
		return ToolOutput{Title: p, IsError: true}, err
	}
	snippet := numberedExcerpt(cur, firstLine, 10)
	out := fmt.Sprintf("applied %d edits atomically to %s (first change at line %d):\n%s", len(args.Edits), p, firstLine, snippet)
	if tc.Diagnostics != nil {
		if diag := strings.TrimSpace(tc.Diagnostics(ctx, tc.WorktreeDir, target)); diag != "" {
			out += "\n\nDiagnostics:\n" + diag
		}
	}
	return ToolOutput{Title: p, Output: out}, nil
}
