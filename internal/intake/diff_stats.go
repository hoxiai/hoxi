package intake

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
)

func isEditToolName(lower string) bool {
	switch lower {
	case "edit", "edit_file", "write", "write_file", "write_to_file",
		"multiedit", "multi_edit", "patch", "apply_patch",
		"replace_file_content", "multi_replace_file_content",
		"str_replace_editor", "str_replace_based_edit_tool":
		return true
	default:
		return false
	}
}

func extractNonNegInt(v interface{}) (int, bool) {
	switch n := v.(type) {
	case int:
		if n < 0 {
			return 0, true
		}
		return n, true
	case int32:
		if n < 0 {
			return 0, true
		}
		return int(n), true
	case int64:
		if n < 0 {
			return 0, true
		}
		return int(n), true
	case uint:
		return int(n), true
	case uint32:
		return int(n), true
	case uint64:
		return int(n), true
	case float32:
		if n < 0 {
			return 0, true
		}
		return int(n), true
	case float64:
		if n < 0 {
			return 0, true
		}
		return int(n), true
	case json.Number:
		if i, err := n.Int64(); err == nil {
			if i < 0 {
				return 0, true
			}
			return int(i), true
		}
		if f, err := n.Float64(); err == nil {
			if f < 0 {
				return 0, true
			}
			return int(f), true
		}
	}
	return 0, false
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

func unquoteDiffPath(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		if u, err := strconv.Unquote(s); err == nil {
			return u
		}
		return strings.Trim(s, `"`)
	}
	return s
}

func stripUnifiedDiffPrefix(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if idx := strings.IndexByte(trimmed, '\t'); idx >= 0 {
		trimmed = strings.TrimSpace(trimmed[:idx])
	}
	trimmed = unquoteDiffPath(trimmed)
	if trimmed == "/dev/null" {
		return ""
	}
	if strings.HasPrefix(trimmed, "a/") || strings.HasPrefix(trimmed, "b/") {
		trimmed = trimmed[2:]
	}
	return safeBasename(trimmed)
}

func scanQuotedDiffToken(s string) (string, string, bool) {
	if len(s) < 2 || s[0] != '"' {
		return "", "", false
	}
	escaped := false
	for i := 1; i < len(s); i++ {
		if escaped {
			escaped = false
			continue
		}
		if s[i] == '\\' {
			escaped = true
			continue
		}
		if s[i] == '"' {
			return s[:i+1], strings.TrimSpace(s[i+1:]), true
		}
	}
	return "", "", false
}

func parseDiffGitHeader(line string) string {
	rest := strings.TrimSpace(strings.TrimPrefix(line, "diff --git "))
	if rest == "" {
		return ""
	}
	if first, rem, ok := scanQuotedDiffToken(rest); ok {
		if rem != "" {
			if second, _, ok2 := scanQuotedDiffToken(rem); ok2 {
				if name := stripUnifiedDiffPrefix(second); name != "" {
					return name
				}
			} else if name := stripUnifiedDiffPrefix(rem); name != "" {
				return name
			}
		}
		return stripUnifiedDiffPrefix(first)
	}
	// Identical unquoted paths with arbitrary spaces: "a/<p> b/<p>".
	if strings.HasPrefix(rest, "a/") && (len(rest)-1)%2 == 0 {
		mid := len(rest) / 2
		if rest[mid] == ' ' && strings.HasPrefix(rest[mid+1:], "b/") && rest[2:mid] == rest[mid+3:] {
			return stripUnifiedDiffPrefix(rest[mid+1:])
		}
	}
	if idx := strings.Index(rest, ` "b/`); idx >= 0 {
		if second, _, ok := scanQuotedDiffToken(rest[idx+1:]); ok {
			if name := stripUnifiedDiffPrefix(second); name != "" {
				return name
			}
		}
	}
	if idx := strings.Index(rest, " b/"); idx >= 0 {
		if name := stripUnifiedDiffPrefix(rest[idx+1:]); name != "" {
			return name
		}
	}
	parts := strings.Fields(rest)
	if len(parts) >= 2 {
		if name := stripUnifiedDiffPrefix(parts[len(parts)-1]); name != "" {
			return name
		}
		return stripUnifiedDiffPrefix(parts[0])
	}
	return ""
}

func parseHunkHeaderBounds(line string) (int, int, bool) {
	if !strings.HasPrefix(line, "@@ -") {
		return 0, 0, false
	}
	rest := line[4:]
	sp := strings.IndexByte(rest, ' ')
	if sp <= 0 {
		return 0, 0, false
	}
	oldPart := rest[:sp]
	rest = strings.TrimSpace(rest[sp+1:])
	if !strings.HasPrefix(rest, "+") {
		return 0, 0, false
	}
	rest = rest[1:]
	sp2 := strings.IndexByte(rest, ' ')
	if sp2 <= 0 {
		return 0, 0, false
	}
	newPart := rest[:sp2]
	if !strings.HasPrefix(strings.TrimSpace(rest[sp2+1:]), "@@") {
		return 0, 0, false
	}
	parseRangeCount := func(spec string) (int, bool) {
		if comma := strings.IndexByte(spec, ','); comma >= 0 {
			if _, err := strconv.Atoi(spec[:comma]); err != nil {
				return 0, false
			}
			n, err := strconv.Atoi(spec[comma+1:])
			return n, err == nil && n >= 0
		}
		if _, err := strconv.Atoi(spec); err != nil {
			return 0, false
		}
		return 1, true
	}
	oldCount, ok1 := parseRangeCount(oldPart)
	newCount, ok2 := parseRangeCount(newPart)
	if !ok1 || !ok2 {
		return 0, 0, false
	}
	return oldCount, newCount, true
}

func mergeSanitizedFileChanges(items []SanitizedFileChange) []SanitizedFileChange {
	if len(items) == 0 {
		return nil
	}
	order := make([]string, 0, len(items))
	merged := make(map[string]SanitizedFileChange, len(items))
	for _, it := range items {
		name := safeBasename(it.File)
		if name == "" {
			continue
		}
		if existing, ok := merged[name]; ok {
			existing.Additions += max(0, it.Additions)
			existing.Deletions += max(0, it.Deletions)
			merged[name] = existing
		} else {
			order = append(order, name)
			merged[name] = SanitizedFileChange{
				File:      name,
				Additions: max(0, it.Additions),
				Deletions: max(0, it.Deletions),
			}
		}
	}
	if len(order) == 0 {
		return nil
	}
	out := make([]SanitizedFileChange, 0, len(order))
	for _, name := range order {
		out = append(out, merged[name])
	}
	return out
}

func parsePatchFileChanges(patch string) ([]SanitizedFileChange, int, int) {
	if strings.TrimSpace(patch) == "" {
		return nil, 0, 0
	}
	lines := strings.Split(strings.ReplaceAll(patch, "\r\n", "\n"), "\n")
	var files []SanitizedFileChange
	curIdx := -1
	totalAdds, totalDels := 0, 0
	inHunk := false
	hasHunkBounds := false
	oldRem, newRem := 0, 0
	pendingHeader := false
	sawHeaderOrHunk := false

	selectFile := func(name string) {
		if name == "" {
			return
		}
		files = append(files, SanitizedFileChange{File: name})
		curIdx = len(files) - 1
	}

	for i, line := range lines {
		switch {
		case strings.HasPrefix(line, "*** Update File:"):
			inHunk, hasHunkBounds, pendingHeader, sawHeaderOrHunk = false, false, false, true
			selectFile(safeBasename(strings.TrimPrefix(line, "*** Update File:")))
			continue
		case strings.HasPrefix(line, "*** Add File:"):
			inHunk, hasHunkBounds, pendingHeader, sawHeaderOrHunk = true, false, false, true
			selectFile(safeBasename(strings.TrimPrefix(line, "*** Add File:")))
			continue
		case strings.HasPrefix(line, "*** Delete File:"):
			inHunk, hasHunkBounds, pendingHeader, sawHeaderOrHunk = true, false, false, true
			selectFile(safeBasename(strings.TrimPrefix(line, "*** Delete File:")))
			continue
		case strings.HasPrefix(line, "*** Move to:"):
			inHunk, hasHunkBounds, pendingHeader, sawHeaderOrHunk = false, false, false, true
			if moved := safeBasename(strings.TrimPrefix(line, "*** Move to:")); moved != "" {
				if curIdx >= 0 {
					files[curIdx].File = moved
				} else {
					selectFile(moved)
				}
			}
			continue
		case strings.HasPrefix(line, "*** "):
			inHunk, hasHunkBounds, pendingHeader, sawHeaderOrHunk = false, false, false, true
			continue
		case strings.HasPrefix(line, "diff --git "):
			inHunk, hasHunkBounds, pendingHeader, sawHeaderOrHunk = false, false, true, true
			curIdx = -1
			selectFile(parseDiffGitHeader(line))
			continue
		case strings.HasPrefix(line, "@@"):
			oldC, newC, ok := parseHunkHeaderBounds(line)
			inHunk, hasHunkBounds, pendingHeader, sawHeaderOrHunk = true, ok, false, true
			oldRem, newRem = oldC, newC
			continue
		}

		if inHunk && strings.HasPrefix(line, "--- ") && i+1 < len(lines) && strings.HasPrefix(lines[i+1], "+++ ") {
			atBoundary := false
			if hasHunkBounds {
				atBoundary = oldRem <= 0 && newRem <= 0
			} else if i+2 < len(lines) && strings.HasPrefix(lines[i+2], "@@") {
				atBoundary = true
			}
			if atBoundary {
				inHunk, hasHunkBounds, pendingHeader = false, false, false
			}
		}

		if !inHunk {
			if strings.HasPrefix(line, "--- ") {
				sawHeaderOrHunk = true
				name := stripUnifiedDiffPrefix(strings.TrimPrefix(line, "--- "))
				if pendingHeader && curIdx >= 0 && files[curIdx].Additions == 0 && files[curIdx].Deletions == 0 {
					if name != "" {
						files[curIdx].File = name
					}
				} else {
					curIdx = -1
					selectFile(name)
					pendingHeader = true
				}
				continue
			}
			if strings.HasPrefix(line, "+++ ") {
				sawHeaderOrHunk = true
				name := stripUnifiedDiffPrefix(strings.TrimPrefix(line, "+++ "))
				if name != "" {
					if pendingHeader && curIdx >= 0 && files[curIdx].Additions == 0 && files[curIdx].Deletions == 0 {
						files[curIdx].File = name
					} else {
						selectFile(name)
					}
				}
				pendingHeader = false
				continue
			}
			if sawHeaderOrHunk {
				continue
			}
			if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
				totalAdds++
				if curIdx >= 0 {
					files[curIdx].Additions++
				}
			} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
				totalDels++
				if curIdx >= 0 {
					files[curIdx].Deletions++
				}
			}
			continue
		}

		if strings.HasPrefix(line, "+") {
			totalAdds++
			newRem--
			if curIdx >= 0 {
				files[curIdx].Additions++
			}
		} else if strings.HasPrefix(line, "-") {
			totalDels++
			oldRem--
			if curIdx >= 0 {
				files[curIdx].Deletions++
			}
		} else if strings.HasPrefix(line, " ") || line == "" {
			oldRem--
			newRem--
		} else if !strings.HasPrefix(line, `\`) && hasHunkBounds && oldRem <= 0 && newRem <= 0 {
			inHunk, hasHunkBounds = false, false
		}
	}

	return mergeSanitizedFileChanges(files), totalAdds, totalDels
}

func firstNonEmptyMapString(m map[string]interface{}, keys ...string) (string, bool) {
	if len(m) == 0 {
		return "", false
	}
	for _, k := range keys {
		if v, ok := m[k].(string); ok {
			return v, true
		}
	}
	return "", false
}

func extractFileChangeFromMap(m map[string]interface{}) (SanitizedFileChange, bool) {
	if len(m) == 0 {
		return SanitizedFileChange{}, false
	}
	var base string
	for _, k := range []string{"movePath", "relativePath", "filePath", "file", "path"} {
		if raw, ok := m[k].(string); ok {
			if b := safeBasename(raw); b != "" {
				base = b
				break
			}
		}
	}
	adds, hasA := extractNonNegInt(m["additions"])
	dels, hasD := extractNonNegInt(m["deletions"])
	hasCounts := hasA || hasD
	if !hasCounts {
		if diffStr, ok := m["diff"].(string); ok && strings.TrimSpace(diffStr) != "" {
			_, adds, dels = parsePatchFileChanges(diffStr)
			hasCounts = adds > 0 || dels > 0
		} else {
			before, hasB := m["before"].(string)
			after, hasAf := m["after"].(string)
			if hasB || hasAf {
				adds, dels = countTextDiffLines(before, after)
				hasCounts = adds > 0 || dels > 0
			}
		}
	}
	if base == "" && !hasCounts {
		return SanitizedFileChange{}, false
	}
	return SanitizedFileChange{
		File:      base,
		Additions: adds,
		Deletions: dels,
	}, true
}

func extractMetadataFilesList(metadata map[string]interface{}) []map[string]interface{} {
	if len(metadata) == 0 {
		return nil
	}
	switch raw := metadata["files"].(type) {
	case []map[string]interface{}:
		return raw
	case []interface{}:
		out := make([]map[string]interface{}, 0, len(raw))
		for _, item := range raw {
			if m, ok := item.(map[string]interface{}); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

func extractInputChunks(input map[string]interface{}) []map[string]interface{} {
	if len(input) == 0 {
		return nil
	}
	for _, k := range []string{"ReplacementChunks", "edits"} {
		switch raw := input[k].(type) {
		case []map[string]interface{}:
			if len(raw) > 0 {
				return raw
			}
		case []interface{}:
			out := make([]map[string]interface{}, 0, len(raw))
			for _, item := range raw {
				if m, ok := item.(map[string]interface{}); ok {
					out = append(out, m)
				}
			}
			if len(out) > 0 {
				return out
			}
		}
	}
	return nil
}

func fallbackEditedFilename(input, metadata map[string]interface{}) string {
	for _, k := range []string{"filepath", "filePath", "file", "path"} {
		if raw, ok := metadata[k].(string); ok {
			if b := safeBasename(raw); b != "" {
				return b
			}
		}
	}
	base, _ := extractInputFilename(input)
	return base
}

// ExtractToolFileChanges computes code-free per-file basenames and line counts (+additions / -deletions)
// for edit tools without ever returning source code, diff text, or local directory paths.
func ExtractToolFileChanges(toolName string, input, metadata map[string]interface{}) ([]SanitizedFileChange, int, int) {
	lower := strings.ToLower(strings.TrimSpace(filepath.Base(toolName)))
	if !isEditToolName(lower) {
		return nil, 0, 0
	}

	// 1. Multi-file metadata["files"] (e.g. apply_patch / native patch tool).
	if items := extractMetadataFilesList(metadata); len(items) > 0 {
		var files []SanitizedFileChange
		totalAdds, totalDels := 0, 0
		for _, item := range items {
			fc, ok := extractFileChangeFromMap(item)
			if !ok {
				continue
			}
			totalAdds += fc.Additions
			totalDels += fc.Deletions
			if fc.File != "" {
				files = append(files, fc)
			}
		}
		merged := mergeSanitizedFileChanges(files)
		if len(merged) > 0 || totalAdds > 0 || totalDels > 0 {
			return merged, totalAdds, totalDels
		}
	}

	// 2. Single-file metadata["filediff"] (native edit/write/multiedit & OpenCode).
	if fd, ok := metadata["filediff"].(map[string]interface{}); ok {
		fc, valid := extractFileChangeFromMap(fd)
		if valid {
			if fc.File == "" {
				fc.File = fallbackEditedFilename(input, metadata)
			}
			if fc.File != "" {
				return []SanitizedFileChange{fc}, fc.Additions, fc.Deletions
			}
			if fc.Additions > 0 || fc.Deletions > 0 {
				return nil, fc.Additions, fc.Deletions
			}
		}
	}

	// 3. Raw unified diff in metadata["diff"] / metadata["patch"].
	for _, k := range []string{"diff", "patch"} {
		if raw, ok := metadata[k].(string); ok && strings.TrimSpace(raw) != "" {
			files, adds, dels := parsePatchFileChanges(raw)
			if len(files) == 0 && (adds > 0 || dels > 0) {
				if base := fallbackEditedFilename(input, metadata); base != "" {
					files = []SanitizedFileChange{{File: base, Additions: adds, Deletions: dels}}
				}
			}
			if len(files) > 0 || adds > 0 || dels > 0 {
				return files, adds, dels
			}
		}
	}

	// 4. Patch input (apply_patch patchText / patch / diff).
	for _, k := range []string{"patchText", "patch", "diff"} {
		if raw, ok := input[k].(string); ok && strings.TrimSpace(raw) != "" {
			files, adds, dels := parsePatchFileChanges(raw)
			if len(files) == 0 && (adds > 0 || dels > 0) {
				if base := fallbackEditedFilename(input, metadata); base != "" {
					files = []SanitizedFileChange{{File: base, Additions: adds, Deletions: dels}}
				}
			}
			if len(files) > 0 || adds > 0 || dels > 0 {
				return files, adds, dels
			}
		}
	}

	// 5. Multi-chunk replacement inputs (ReplacementChunks / edits).
	if chunks := extractInputChunks(input); len(chunks) > 0 {
		totalAdds, totalDels := 0, 0
		for _, ch := range chunks {
			before, hasB := firstNonEmptyMapString(ch, "TargetContent", "targetContent", "oldString", "old_string")
			after, hasA := firstNonEmptyMapString(ch, "ReplacementContent", "replacementContent", "newString", "new_string")
			if hasB || hasA {
				a, d := countTextDiffLines(before, after)
				totalAdds += a
				totalDels += d
			}
		}
		if totalAdds > 0 || totalDels > 0 {
			if base := fallbackEditedFilename(input, metadata); base != "" {
				return []SanitizedFileChange{{File: base, Additions: totalAdds, Deletions: totalDels}}, totalAdds, totalDels
			}
			return nil, totalAdds, totalDels
		}
	}

	// 6. Single-file before/after input payloads.
	before, hasB := firstNonEmptyMapString(input, "oldString", "old_string", "TargetContent", "targetContent", "Target")
	after, hasA := firstNonEmptyMapString(input, "newString", "new_string", "ReplacementContent", "replacementContent", "Replacement", "Content", "content", "file_text", "CodeContent", "codeContent")
	if hasB || hasA {
		adds, dels := countTextDiffLines(before, after)
		if adds > 0 || dels > 0 {
			if base := fallbackEditedFilename(input, metadata); base != "" {
				return []SanitizedFileChange{{File: base, Additions: adds, Deletions: dels}}, adds, dels
			}
			return nil, adds, dels
		}
	}

	return nil, 0, 0
}
