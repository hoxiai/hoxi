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
	return "Apply a validated multi-file patch across workspace files and run deduplicated compiler diagnostics. Prefer the count-free '*** Begin Patch' envelope format ('*** Update File: <path>', '*** Add File: <path>', '*** Delete File: <path>', '@@ <symbol/line anchor>', ' '/'+'/'-' lines, '*** End Patch') over unified diffs to avoid hunk line-count errors."
}
func (t *applyPatchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"patch": map[string]any{
				"type":        "string",
				"description": "Preferred count-free '*** Begin Patch\\n*** Update File: path\\n@@ anchor\\n context\\n-old\\n+new\\n*** End Patch' envelope (or strict unified diff '--- a/... +++ b/...')",
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

type envelopeHunk struct {
	anchors []string
	lines   []patchHunkLine
	eof     bool
}

type patchFileOp struct {
	kind     patchOpKind
	path     string
	moveTo   string
	addLines []string
	hunks    []envelopeHunk // Context-based envelope hunks.
	unified  []unifiedHunk
}

type stagedFileMutation struct {
	op         patchFileOp
	absSource  string
	absTarget  string
	oldContent []byte
	hadSource  bool
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
	rawPatch := args.Patch
	if strings.TrimSpace(rawPatch) == "" {
		rawPatch = args.Diff
	}
	if strings.TrimSpace(rawPatch) == "" {
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

	type resolvedPatchOp struct {
		op     patchFileOp
		srcAbs string
		dstAbs string
	}
	resolvedOps := make([]resolvedPatchOp, 0, len(ops))
	coordPaths := make([]string, 0, len(ops)*2)
	coordLabels := make(map[string]string, len(ops)*2)
	for _, op := range ops {
		srcAbs, err := resolveWorkspacePath(tc.WorktreeDir, op.path, !tc.PermissionVetted)
		if err != nil {
			return ToolOutput{Title: op.path, IsError: true}, err
		}
		if !tc.PermissionVetted && isSensitiveAgentPath(srcAbs) {
			return ToolOutput{Title: op.path, IsError: true}, fmt.Errorf("refusing to patch secret/credential file %s", op.path)
		}
		dstAbs := srcAbs
		if op.moveTo != "" {
			dstAbs, err = resolveWorkspacePath(tc.WorktreeDir, op.moveTo, !tc.PermissionVetted)
			if err != nil {
				return ToolOutput{Title: op.moveTo, IsError: true}, err
			}
			if !tc.PermissionVetted && isSensitiveAgentPath(dstAbs) {
				return ToolOutput{Title: op.moveTo, IsError: true}, fmt.Errorf("refusing to move into secret/credential file %s", op.moveTo)
			}
		}
		resolvedOps = append(resolvedOps, resolvedPatchOp{op: op, srcAbs: srcAbs, dstAbs: dstAbs})
		coordPaths = append(coordPaths, srcAbs)
		coordLabels[srcAbs] = op.path
		if dstAbs != srcAbs {
			coordPaths = append(coordPaths, dstAbs)
			coordLabels[dstAbs] = op.moveTo
		}
	}

	release, peerModified, err := coordinateFileMutation(ctx, tc, coordPaths, coordLabels)
	if err != nil {
		return ToolOutput{Title: "apply_patch", IsError: true}, err
	}
	defer release()

	plannedDirs := make(map[string]bool)
	for _, rop := range resolvedOps {
		op := rop.op
		srcAbs := rop.srcAbs
		dstAbs := rop.dstAbs

		if op.kind != patchOpDelete {
			if plannedDirs[dstAbs] {
				return ToolOutput{Title: op.path, IsError: true}, fmt.Errorf("patch destination %s is required as a directory", dstAbs)
			}
			for parent := filepath.Dir(dstAbs); !plannedDirs[parent]; parent = filepath.Dir(parent) {
				if _, file := virtualFS[parent]; file {
					return ToolOutput{Title: op.path, IsError: true}, fmt.Errorf("patch requires file %s as a directory", parent)
				}
				plannedDirs[parent] = true
				if parent == filepath.Dir(parent) {
					break
				}
			}
		}
		switch op.kind {
		case patchOpAdd:
			var prevAddBytes []byte
			var hadPrevAdd bool
			if prevB, rErr := readVirtual(dstAbs); rErr == nil {
				prevAddBytes = prevB
				hadPrevAdd = true
			} else if !os.IsNotExist(rErr) {
				return ToolOutput{Title: op.path, IsError: true}, rErr
			}
			if _, stagedBefore := virtualFS[dstAbs]; !stagedBefore && !deletedFS[dstAbs] {
				if err := tc.FileRevisions.CheckWrite(dstAbs, op.path); err != nil {
					return ToolOutput{Title: op.path, IsError: true}, err
				}
				if peerModified(dstAbs) && hadPrevAdd && len(prevAddBytes) > 0 {
					if err := checkEditWithPeerGuard(tc.FileRevisions, dstAbs, op.path, prevAddBytes, true); err != nil {
						return ToolOutput{Title: op.path, IsError: true}, err
					}
				}
			}
			content := []byte(strings.Join(op.addLines, "\n") + "\n")
			if len(op.addLines) == 0 {
				content = []byte{}
			}
			if op.unified != nil {
				if hadPrevAdd {
					return ToolOutput{Title: op.path, IsError: true}, fmt.Errorf("%s: unified creation requires a missing file", op.path)
				}
				added, err := applyUnifiedHunks("", op.unified)
				if err != nil {
					return ToolOutput{Title: op.path, IsError: true}, err
				}
				content = []byte(added)
			}
			virtualFS[dstAbs] = content
			delete(deletedFS, dstAbs)
			staged = append(staged, stagedFileMutation{
				op: op, absSource: srcAbs, absTarget: dstAbs,
				oldContent: prevAddBytes, hadSource: hadPrevAdd, newContent: content,
			})

		case patchOpDelete:
			_, stagedBefore := virtualFS[srcAbs]
			origBytes, err := readVirtual(srcAbs)
			if err != nil {
				return ToolOutput{Title: op.path, IsError: true}, fmt.Errorf("cannot delete %s: %w", op.path, err)
			}
			if !stagedBefore {
				if err := checkEditWithPeerGuard(tc.FileRevisions, srcAbs, op.path, origBytes, peerModified(srcAbs)); err != nil {
					return ToolOutput{Title: op.path, IsError: true}, err
				}
			}
			if op.unified != nil {
				left, err := applyUnifiedHunks(strings.ReplaceAll(string(origBytes), "\r\n", "\n"), op.unified)
				if err != nil || left != "" {
					return ToolOutput{Title: op.path, IsError: true}, fmt.Errorf("%s: deletion patch does not match the whole file", op.path)
				}
			}
			delete(virtualFS, srcAbs)
			deletedFS[srcAbs] = true
			staged = append(staged, stagedFileMutation{
				op: op, absSource: srcAbs, absTarget: srcAbs,
				oldContent: origBytes, hadSource: true,
			})

		case patchOpUpdate:
			if len(op.hunks) == 0 && len(op.unified) == 0 && op.moveTo == "" {
				return ToolOutput{Title: op.path, IsError: true}, fmt.Errorf("%s: patch update contains no hunks", op.path)
			}
			var srcMode os.FileMode
			if st, sErr := os.Stat(srcAbs); sErr == nil && st.Mode().IsRegular() {
				srcMode = st.Mode().Perm()
			}
			_, stagedBefore := virtualFS[srcAbs]
			origBytes, err := readVirtual(srcAbs)
			if err != nil {
				return ToolOutput{Title: op.path, IsError: true}, fmt.Errorf("cannot read %s: %w", op.path, err)
			}
			if !stagedBefore {
				if err := checkEditWithPeerGuard(tc.FileRevisions, srcAbs, op.path, origBytes, peerModified(srcAbs)); err != nil {
					return ToolOutput{Title: op.path, IsError: true}, fmt.Errorf("%s: %w", op.path, err)
				}
			}
			if dstAbs != srcAbs {
				if st, err := os.Stat(dstAbs); err == nil && !st.Mode().IsRegular() {
					return ToolOutput{Title: op.moveTo, IsError: true}, fmt.Errorf("move destination %s is not a regular file", op.moveTo)
				} else if err != nil && !os.IsNotExist(err) {
					return ToolOutput{Title: op.moveTo, IsError: true}, err
				}
				if _, dstStaged := virtualFS[dstAbs]; !dstStaged && !deletedFS[dstAbs] {
					if err := tc.FileRevisions.CheckWrite(dstAbs, op.moveTo); err != nil {
						return ToolOutput{Title: op.moveTo, IsError: true}, err
					}
					if peerModified(dstAbs) {
						if diskBytes, rErr := os.ReadFile(dstAbs); rErr == nil {
							if err := checkEditWithPeerGuard(tc.FileRevisions, dstAbs, op.moveTo, diskBytes, true); err != nil {
								return ToolOutput{Title: op.moveTo, IsError: true}, err
							}
						}
					}
				}
			}
			useCRLF := strings.Contains(string(origBytes), "\r\n")
			normSrc := strings.ReplaceAll(string(origBytes), "\r\n", "\n")
			var updated string
			if op.unified != nil {
				updated, err = applyUnifiedHunks(normSrc, op.unified)
			} else {
				updated, err = applyPatchHunks(normSrc, op.hunks)
			}
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
			staged = append(staged, stagedFileMutation{
				op: op, absSource: srcAbs, absTarget: dstAbs,
				oldContent: origBytes, hadSource: true, newContent: outBytes, fileMode: srcMode,
			})
		}
	}

	var summaryLines []string
	var fileMeta []map[string]any
	var diagTargets []string
	var mutatedPaths []string
	seenDiag := make(map[string]bool)

	failed := func(i int, err error) (ToolOutput, error) {
		recordCompletedFileEdit(tc, mutatedPaths...)
		var pending []string
		for _, m := range staged[i+1:] {
			pending = append(pending, m.op.path)
		}
		failedPath := staged[i].op.path
		message := fmt.Sprintf("patch failed at %s: %v", failedPath, err)
		if len(summaryLines) > 0 {
			message += "\nAlready applied:\n" + strings.Join(summaryLines, "\n")
		}
		if len(pending) > 0 {
			message += "\nNot attempted: " + strings.Join(pending, ", ")
		}
		return ToolOutput{Title: failedPath, IsError: true, Output: message, Metadata: map[string]any{"files": fileMeta, "completedMutation": len(mutatedPaths) > 0, "failedPath": failedPath, "notAttempted": pending}}, err
	}
	for i, m := range staged {
		switch m.op.kind {
		case patchOpDelete:
			if err := os.Remove(m.absSource); err != nil && !os.IsNotExist(err) {
				return failed(i, err)
			}
			tc.FileRevisions.Forget(m.absSource)
			mutatedPaths = append(mutatedPaths, m.absSource)
			summaryLines = append(summaryLines, "D "+m.op.path)
			fileMeta = append(fileMeta, buildPatchFileMetaEntry(tc.WorktreeDir, m))
		case patchOpAdd, patchOpUpdate:
			if err := os.MkdirAll(filepath.Dir(m.absTarget), 0o755); err != nil {
				return failed(i, err)
			}
			if err := writeAtomicFileMode(m.absTarget, m.newContent, m.fileMode); err != nil {
				return failed(i, err)
			}
			if m.op.kind == patchOpAdd {
				tc.FileRevisions.RecordBytes(m.absTarget, m.newContent)
			} else {
				tc.FileRevisions.RecordTargetedMove(m.absSource, m.absTarget, m.oldContent, m.newContent)
			}
			if m.absTarget != m.absSource {
				if err := os.Remove(m.absSource); err != nil {
					mutatedPaths = append(mutatedPaths, m.absTarget)
					summaryLines = append(summaryLines, "W "+m.op.moveTo+" (source removal failed)")
					fileMeta = append(fileMeta, map[string]any{"filePath": m.absTarget, "type": "update", "relativePath": workspaceRelativeSlash(tc.WorktreeDir, m.absTarget)})
					return failed(i, err)
				}
				tc.FileRevisions.Forget(m.absSource)
				mutatedPaths = append(mutatedPaths, m.absSource)
				summaryLines = append(summaryLines, "R "+m.op.path+" -> "+m.op.moveTo)
			} else if m.op.kind == patchOpAdd {
				summaryLines = append(summaryLines, "A "+m.op.path)
			} else {
				summaryLines = append(summaryLines, "M "+m.op.path)
			}
			fileMeta = append(fileMeta, buildPatchFileMetaEntry(tc.WorktreeDir, m))
			mutatedPaths = append(mutatedPaths, m.absTarget)
			if !seenDiag[m.absTarget] {
				seenDiag[m.absTarget] = true
				diagTargets = append(diagTargets, m.absTarget)
			}
		}
	}
	recordCompletedFileEdit(tc, mutatedPaths...)
	release()

	out := fmt.Sprintf("applied patch across %d file operation(s):\n%s", len(staged), strings.Join(summaryLines, "\n"))
	if tc.Diagnostics != nil && len(diagTargets) > 0 {
		var diags []string
		seenDiagText := make(map[string]bool)
		for _, target := range deduplicatePatchDiagnosticTargets(tc.WorktreeDir, diagTargets) {
			if d := strings.TrimSpace(tc.Diagnostics(ctx, tc.WorktreeDir, target)); d != "" && !seenDiagText[d] {
				seenDiagText[d] = true
				diags = append(diags, d)
			}
		}
		if len(diags) > 0 {
			out += "\n\nDiagnostics:\n" + strings.Join(diags, "\n")
		}
	}
	for _, m := range staged {
		out = appendSubdirectoryRules(tc, m.absTarget, out)
	}
	return ToolOutput{
		Title:    fmt.Sprintf("%d files patched", len(staged)),
		Output:   out,
		Metadata: map[string]any{"files": fileMeta, "summary": summaryLines},
	}, nil
}

// deduplicatePatchDiagnosticTargets collapses multiple patched files that share
// the same package- or project-wide static checker (Go package for `go vet`,
// nearest tsconfig.json dir for `tsc --noEmit`, nearest Cargo.toml dir for
// `cargo check`) into a single representative file so multi-file patches do not
// run redundant full-project compiler passes or trigger checker cooldowns.
func deduplicatePatchDiagnosticTargets(worktreeDir string, targets []string) []string {
	if len(targets) <= 1 {
		return targets
	}
	seenScope := make(map[string]bool, len(targets))
	out := make([]string, 0, len(targets))
	for _, target := range targets {
		key := patchDiagnosticScopeKey(worktreeDir, target)
		if seenScope[key] {
			continue
		}
		seenScope[key] = true
		out = append(out, target)
	}
	return out
}

func patchDiagnosticScopeKey(worktreeDir, target string) string {
	ext := strings.ToLower(filepath.Ext(target))
	switch ext {
	case ".go":
		if root := findPatchAncestorFile(filepath.Dir(target), worktreeDir, "go.mod"); root != "" {
			return "go:" + filepath.Clean(filepath.Dir(target))
		}
	case ".ts", ".tsx", ".mts", ".cts":
		if root := findPatchAncestorFile(filepath.Dir(target), worktreeDir, "tsconfig.json"); root != "" {
			return "tsc:" + root
		}
	case ".rs":
		if root := findPatchAncestorFile(filepath.Dir(target), worktreeDir, "Cargo.toml"); root != "" {
			return "cargo:" + root
		}
	}
	return "file:" + filepath.Clean(target)
}

func findPatchAncestorFile(startDir, boundaryDir, filename string) string {
	cur, err := filepath.Abs(startDir)
	if err != nil {
		return ""
	}
	boundary := ""
	if strings.TrimSpace(boundaryDir) != "" {
		if b, bErr := filepath.Abs(boundaryDir); bErr == nil {
			boundary = filepath.Clean(b)
		}
	}
	for {
		if boundary != "" && !isLexicalPathWithin(boundary, cur) {
			return ""
		}
		cand := filepath.Join(cur, filename)
		if st, sErr := os.Stat(cand); sErr == nil && !st.IsDir() {
			return filepath.Clean(cur)
		}
		if boundary != "" && filepath.Clean(cur) == boundary {
			return ""
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

func buildPatchFileMetaEntry(worktreeDir string, m stagedFileMutation) map[string]any {
	primaryAbs := m.absTarget
	if m.op.kind == patchOpDelete {
		primaryAbs = m.absSource
	}
	rel := workspaceRelativeSlash(worktreeDir, primaryAbs)
	var beforeStr, afterStr string
	if m.hadSource {
		beforeStr = string(m.oldContent)
	}
	if m.op.kind != patchOpDelete {
		afterStr = string(m.newContent)
	}
	adds, dels := countPatchMutationLines(m, beforeStr, afterStr)
	entry := map[string]any{
		"filePath":     primaryAbs,
		"relativePath": rel,
		"type":         string(m.op.kind),
		"additions":    adds,
		"deletions":    dels,
	}
	if m.absTarget != m.absSource {
		entry["filePath"] = m.absSource
		entry["type"] = "move"
		entry["movePath"] = m.absTarget
	}
	if len(beforeStr)+len(afterStr) <= maxInlineDiffMetadataBytes {
		entry["before"] = beforeStr
		entry["after"] = afterStr
	}
	return entry
}

func countPatchMutationLines(m stagedFileMutation, beforeStr, afterStr string) (int, int) {
	if m.op.kind == patchOpUpdate && m.op.unified != nil {
		var added, removed int
		for _, h := range m.op.unified {
			added += len(h.after) - h.context
			removed += len(h.before) - h.context
		}
		return added, removed
	}
	if m.op.kind == patchOpUpdate && len(m.op.hunks) > 0 {
		adds, dels := 0, 0
		for _, h := range m.op.hunks {
			for _, hl := range h.lines {
				switch hl.prefix {
				case '+':
					adds++
				case '-':
					dels++
				}
			}
		}
		return adds, dels
	}
	return countTextDiffLines(beforeStr, afterStr)
}

func parseMultiFilePatch(raw string) ([]patchFileOp, error) {
	norm := strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(norm, "\n")
	for _, l := range lines {
		if strings.HasPrefix(l, "--- ") {
			break
		}
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "*** Begin Patch") || strings.HasPrefix(t, "*** Add File:") ||
			strings.HasPrefix(t, "*** Update File:") || strings.HasPrefix(t, "*** Delete File:") {
			return parseEnvelopePatch(strings.Split(strings.TrimSpace(norm), "\n"))
		}
	}
	return parseUnifiedDiffPatch(lines)
}

func parseEnvelopePatch(lines []string) ([]patchFileOp, error) {
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "*** Begin Patch" {
		if strings.TrimSpace(lines[len(lines)-1]) != "*** End Patch" {
			return nil, fmt.Errorf("missing final *** End Patch")
		}
		lines = lines[1 : len(lines)-1]
	}
	var ops []patchFileOp
	i := 0
	for i < len(lines) {
		trimmed := strings.TrimSpace(lines[i])
		switch {
		case trimmed == "":
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
			var hunks []envelopeHunk
			var cur envelopeHunk
			flushHunk := func() {
				if len(cur.lines) > 0 {
					hunks = append(hunks, cur)
					cur = envelopeHunk{}
				}
			}
			for i < len(lines) {
				if lines[i] == "*** End of File" {
					if len(cur.lines) == 0 || cur.eof {
						return nil, fmt.Errorf("%s: misplaced end-of-file marker", p)
					}
					cur.eof = true
					i++
					continue
				}
				if strings.HasPrefix(lines[i], "*** ") {
					break
				}
				rawLine := lines[i]
				if cur.eof {
					return nil, fmt.Errorf("%s: content after end-of-file marker", p)
				}
				if rawLine == "@@" || strings.HasPrefix(rawLine, "@@ ") {
					flushHunk()
					if strings.HasPrefix(rawLine, "@@ ") {
						cur.anchors = append(cur.anchors, rawLine[3:])
					}
					i++
					continue
				}
				if rawLine == "" {
					cur.lines = append(cur.lines, patchHunkLine{prefix: ' ', text: ""})
				} else {
					switch rawLine[0] {
					case ' ', '+', '-':
						cur.lines = append(cur.lines, patchHunkLine{prefix: rawLine[0], text: rawLine[1:]})
					default:
						cur.lines = append(cur.lines, patchHunkLine{prefix: ' ', text: rawLine})
					}
				}
				i++
			}
			if len(cur.anchors) > 0 && len(cur.lines) == 0 {
				return nil, fmt.Errorf("%s: missing anchored hunk body", p)
			}
			flushHunk()
			ops = append(ops, patchFileOp{kind: patchOpUpdate, path: p, moveTo: moveTo, hunks: hunks})
		default:
			return nil, fmt.Errorf("unexpected patch header %q", trimmed)
		}
	}
	return ops, nil
}

func applyPatchHunks(src string, hunks []envelopeHunk) (string, error) {
	lines := strings.Split(strings.TrimSuffix(src, "\n"), "\n")
	if src == "" {
		lines = nil
	}
	var out []string
	cursor := 0
	for n, h := range hunks {
		start := cursor
		for _, anchor := range h.anchors {
			pos, err := matchEnvelopeLines(lines, []string{anchor}, start, false, start > cursor)
			if err != nil {
				return "", fmt.Errorf("hunk %d anchor %q: %w", n+1, anchor, err)
			}
			start = pos + 1
		}
		var old []string
		for _, line := range h.lines {
			if line.prefix != '+' {
				old = append(old, line.text)
			}
		}
		pos := start
		if len(old) > 0 {
			matched, err := matchEnvelopeLines(lines, old, start, h.eof, len(h.anchors) > 0)
			if err != nil {
				return "", fmt.Errorf("hunk %d (%q): %w", n+1, old[0], err)
			}
			pos = matched
		} else if len(h.anchors) == 0 || h.eof {
			pos = len(lines)
		}
		out = append(out, lines[cursor:pos]...)
		cursor = pos
		for _, line := range h.lines {
			switch line.prefix {
			case ' ':
				out = append(out, lines[cursor])
				cursor++
			case '-':
				cursor++
			case '+':
				out = append(out, line.text)
			}
		}
	}
	out = append(out, lines[cursor:]...)
	result := strings.Join(out, "\n")
	if len(out) > 0 && strings.HasSuffix(src, "\n") {
		result += "\n"
	}
	return result, nil
}

// Prefer exact evidence. A weaker whitespace match must also be unique.
func matchEnvelopeLines(lines, pattern []string, start int, eof, anchored bool) (int, error) {
	last := len(lines) - len(pattern)
	if eof && last >= start {
		start = last
	}
	for _, normalize := range []func(string) string{func(s string) string { return s }, func(s string) string { return strings.TrimRight(s, " \t") }, strings.TrimSpace} {
		found := -1
		for i := start; i <= last; i++ {
			equal := true
			for j, s := range pattern {
				if normalize(lines[i+j]) != normalize(s) {
					equal = false
					break
				}
			}
			if equal {
				if anchored && i == start {
					return i, nil
				}
				if found >= 0 {
					return -1, fmt.Errorf("ambiguous match; add context lines")
				}
				found = i
			}
		}
		if found >= 0 {
			return found, nil
		}
	}
	return -1, fmt.Errorf("did not match file contents")
}
