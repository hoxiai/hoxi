package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	grepModeContent          = "content"
	grepModeFilesWithMatches = "files_with_matches"
	grepModeCount            = "count"
)

type grepTool struct{}

func (t *grepTool) Name() string { return "grep" }
func (t *grepTool) Description() string {
	return "Search file contents in the workspace using a regular expression or literal string. Prefer mode='files_with_matches' or 'count' for broad discovery before requesting matching content lines."
}
func (t *grepTool) IsReadOnly() bool { return true }
func (t *grepTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern":         map[string]any{"type": "string", "description": "Regex or text pattern to search for"},
			"path":            map[string]any{"type": "string", "description": "Optional file or directory to search"},
			"include":         map[string]any{"type": "string", "description": "Optional filename glob filter (e.g. *.go)"},
			"mode":            map[string]any{"type": "string", "enum": []string{grepModeContent, grepModeFilesWithMatches, grepModeCount}, "description": "Output mode: 'content' (default matching lines), 'files_with_matches' (file paths only), or 'count' (match counts per file)"},
			"contextLines":    map[string]any{"type": "integer", "minimum": 0, "maximum": 10, "description": "Lines of surrounding context before and after each match in 'content' mode (0-10)"},
			"caseInsensitive": map[string]any{"type": "boolean", "description": "Perform case-insensitive matching"},
			"offset":          map[string]any{"type": "integer", "minimum": 0, "description": "Skip this many matches (lines in 'content' mode, files in 'files_with_matches'/'count' mode) in stable path/line order; use returned nextOffset to continue"},
		},
		"required": []string{"pattern"},
	}
}

func compileGrepRegex(pattern string, caseInsensitive bool) *regexp.Regexp {
	prefix := ""
	if caseInsensitive {
		prefix = "(?i)"
	}
	re, err := regexp.Compile(prefix + pattern)
	if err != nil {
		re = regexp.MustCompile(prefix + regexp.QuoteMeta(pattern))
	}
	return re
}

func (t *grepTool) Execute(ctx context.Context, tc ToolContext, input json.RawMessage) (ToolOutput, error) {
	var args struct {
		Pattern         string `json:"pattern"`
		Path            string `json:"path"`
		Include         string `json:"include"`
		Mode            string `json:"mode"`
		ContextLines    int    `json:"contextLines"`
		CaseInsensitive bool   `json:"caseInsensitive"`
		Offset          int    `json:"offset"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return ToolOutput{Title: "grep", IsError: true}, err
	}
	if args.Offset < 0 {
		return ToolOutput{IsError: true}, fmt.Errorf("offset must be nonnegative")
	}
	mode := strings.ToLower(strings.TrimSpace(args.Mode))
	if mode == "" {
		mode = grepModeContent
	}
	switch mode {
	case grepModeContent, grepModeFilesWithMatches, grepModeCount:
	default:
		return ToolOutput{Title: "grep", IsError: true}, fmt.Errorf("unsupported grep mode %q (expected content, files_with_matches, or count)", args.Mode)
	}
	ctxLines := args.ContextLines
	if ctxLines < 0 {
		ctxLines = 0
	} else if ctxLines > 10 {
		ctxLines = 10
	}

	re := compileGrepRegex(args.Pattern, args.CaseInsensitive)
	root := tc.WorktreeDir
	if strings.TrimSpace(args.Path) != "" {
		resolved, err := resolveWorkspacePath(tc.WorktreeDir, args.Path, false)
		if err != nil {
			return ToolOutput{Title: args.Pattern, IsError: true}, err
		}
		root = resolved
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return ToolOutput{Title: args.Pattern, IsError: true}, err
	}
	rootInfo, err := os.Stat(realRoot)
	if err != nil {
		return ToolOutput{Title: args.Pattern, IsError: true}, err
	}
	if !tc.PermissionVetted {
		if !isRealPathWithin(tc.WorktreeDir, realRoot) {
			return ToolOutput{Title: args.Pattern, IsError: true}, fmt.Errorf("grep path %q resolves outside workspace", args.Path)
		}
		if !rootInfo.IsDir() && (isSensitiveAgentPath(root) || isSensitiveAgentPath(realRoot)) {
			return ToolOutput{Title: args.Pattern, IsError: true}, fmt.Errorf("searching sensitive credential or key file %q is blocked", filepath.Base(realRoot))
		}
	}

	skipped := 0
	const (
		maxGrepFileSize = 2 << 20
		maxContentHits  = 150
		maxFileHits     = 200
		outputBudget    = maxToolOutputBytes - 512
	)
	// WalkDir visits filenames lexically. Keep this order when resuming a page.
	var out strings.Builder
	seen, shown := 0, 0
	hasMore := false

	scan := func(displayPath, openPath string, info os.FileInfo) error {
		rel, err := filepath.Rel(tc.WorktreeDir, displayPath)
		if err != nil || strings.HasPrefix(rel, "..") {
			rel = displayPath
		}
		rel = filepath.ToSlash(rel)

		switch mode {
		case grepModeFilesWithMatches:
			matchedFile := false
			err = scanGrepFile(ctx, openPath, info, maxGrepFileSize, func(_ int, b []byte) bool {
				if re.FindIndex(b) != nil {
					matchedFile = true
					return false
				}
				return true
			})
			if err == nil && matchedFile {
				if seen < args.Offset {
					seen++
				} else {
					row := rel + "\n"
					if shown == maxFileHits || (shown > 0 && out.Len()+len(row) > outputBudget) {
						hasMore = true
					} else {
						out.WriteString(row)
						shown++
					}
				}
			}
		case grepModeCount:
			count := 0
			err = scanGrepFile(ctx, openPath, info, maxGrepFileSize, func(_ int, b []byte) bool {
				if re.FindIndex(b) != nil {
					count++
				}
				return true
			})
			if err == nil && count > 0 {
				if seen < args.Offset {
					seen++
				} else {
					row := fmt.Sprintf("%s:%d\n", rel, count)
					if shown == maxFileHits || (shown > 0 && out.Len()+len(row) > outputBudget) {
						hasMore = true
					} else {
						out.WriteString(row)
						shown++
					}
				}
			}
		default: // grepModeContent
			if ctxLines == 0 {
				err = scanGrepFile(ctx, openPath, info, maxGrepFileSize, func(lineNo int, b []byte) bool {
					loc := re.FindIndex(b)
					if loc == nil {
						return true
					}
					if seen < args.Offset {
						seen++
						return true
					}
					row := fmt.Sprintf("%s:%d: %s\n", rel, lineNo, formatGrepMatchSnippet(b, loc[0], loc[1], maxGrepSnippetBytes))
					if shown == maxContentHits || (shown > 0 && out.Len()+len(row) > outputBudget) {
						hasMore = true
						return false
					}
					out.WriteString(row)
					shown++
					return true
				})
			} else {
				err = scanGrepFileWithContext(ctx, openPath, info, maxGrepFileSize, rel, re, ctxLines, args.Offset, maxContentHits, outputBudget, &seen, &shown, &hasMore, &out)
			}
		}

		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			skipped++
		}
		if hasMore {
			return fs.SkipAll
		}
		return nil
	}

	if !rootInfo.IsDir() {
		if rootInfo.Size() > maxGrepFileSize {
			skipped++
		} else if rootInfo.Mode().IsRegular() {
			if args.Include == "" || matchGlob(args.Include, filepath.Base(root)) {
				err = scan(root, realRoot, rootInfo)
			}
		}
	} else {
		ignorer := newWorkspaceIgnorer(tc.WorktreeDir, root)
		err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if walkErr != nil {
				skipped++
				return nil
			}
			if d.IsDir() {
				if p != root {
					if shouldSkipSearchDir(d.Name()) {
						return filepath.SkipDir
					}
					if ignorer != nil && ignorer.IsIgnored(p, true) && !ignorer.CanReincludeUnderDir(p) {
						return filepath.SkipDir
					}
				}
				ignorer.EnterDir(p)
				return nil
			}
			if isSensitiveAgentPathLexical(p) {
				return nil
			}
			if ignorer != nil && ignorer.IsIgnored(p, false) {
				return nil
			}
			if args.Include != "" {
				relInc, relErr := filepath.Rel(root, p)
				relIncSlash := d.Name()
				if relErr == nil && relInc != "." {
					relIncSlash = filepath.ToSlash(relInc)
				}
				if !matchGlob(args.Include, relIncSlash) {
					return nil
				}
			}
			realFile, rErr := filepath.EvalSymlinks(p)
			if rErr != nil {
				skipped++
				return nil
			}
			if !isLexicalPathWithin(realRoot, realFile) || hasSkippedSearchSegment(realRoot, realFile) {
				return nil
			}
			if isSensitiveAgentPathLexical(realFile) {
				return nil
			}
			if realSt, sErr := os.Stat(realFile); sErr != nil || realSt.Size() > maxGrepFileSize {
				skipped++
			} else if realSt.Mode().IsRegular() {
				return scan(p, realFile, realSt)
			}
			return nil
		})
	}

	if err != nil && err != fs.SkipAll {
		return ToolOutput{IsError: true}, err
	}
	meta := map[string]any{}
	if shown == 0 {
		if args.Offset > 0 {
			out.WriteString("(no more matches at this offset)")
		} else if skipped > 0 {
			out.WriteString("(no matches in scanned files)")
		} else {
			out.WriteString("(no matches found)")
		}
	}
	if hasMore {
		next := args.Offset + shown
		meta["nextOffset"] = next
		fmt.Fprintf(&out, "\n(truncated; repeat grep with the same options and offset=%d)", next)
	}
	if skipped > 0 {
		meta["skippedFiles"] = skipped
		fmt.Fprintf(&out, "\n(incomplete scan: %d oversized, unreadable or changed entries skipped)", skipped)
	}
	return ToolOutput{Title: args.Pattern, Output: out.String(), Metadata: meta}, nil
}

func readValidatedGrepBytes(path string, expected fs.FileInfo, maxBytes int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || !os.SameFile(st, expected) || st.Size() > maxBytes {
		return nil, fmt.Errorf("file changed before scan")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("file grew during scan")
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, nil
	}
	return data, nil
}

func scanGrepFile(ctx context.Context, path string, expected fs.FileInfo, maxBytes int64, visit func(int, []byte) bool) error {
	data, err := readValidatedGrepBytes(path, expected, maxBytes)
	if err != nil || len(data) == 0 {
		return err
	}
	for lineNo := 1; len(data) > 0; lineNo++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, rest, _ := bytes.Cut(data, []byte{'\n'})
		if !visit(lineNo, bytes.TrimSuffix(line, []byte{'\r'})) {
			break
		}
		data = rest
	}
	return nil
}

func scanGrepFileWithContext(
	ctx context.Context,
	openPath string,
	expected fs.FileInfo,
	maxBytes int64,
	rel string,
	re *regexp.Regexp,
	ctxLines, offset, maxHits, outputBudget int,
	seen, shown *int,
	hasMore *bool,
	out *strings.Builder,
) error {
	data, err := readValidatedGrepBytes(openPath, expected, maxBytes)
	if err != nil || len(data) == 0 {
		return err
	}
	rawLines := bytes.Split(data, []byte{'\n'})
	// Drop synthetic empty trailing element caused by final newline.
	if len(rawLines) > 0 && len(rawLines[len(rawLines)-1]) == 0 {
		rawLines = rawLines[:len(rawLines)-1]
	}
	for i := range rawLines {
		rawLines[i] = bytes.TrimSuffix(rawLines[i], []byte{'\r'})
	}

	type lineMatch struct {
		idx int
		loc []int
	}
	var matches []lineMatch
	for i, line := range rawLines {
		if err := ctx.Err(); err != nil {
			return err
		}
		if loc := re.FindIndex(line); loc != nil {
			matches = append(matches, lineMatch{idx: i, loc: loc})
		}
	}
	if len(matches) == 0 {
		return nil
	}

	// Emit matches + surrounding context windows in order, deduplicating overlapping lines.
	lastEmittedLine := -1

	for mPos, m := range matches {
		if *seen < offset {
			*seen++
			continue
		}
		winStart := max(0, m.idx-ctxLines)
		winEnd := min(len(rawLines)-1, m.idx+ctxLines)
		// On a resumed page, the previous match belongs to the preceding page.
		if mPos > 0 {
			winStart = max(winStart, matches[mPos-1].idx+1)
		}
		if mPos+1 < len(matches) {
			winEnd = min(winEnd, matches[mPos+1].idx-1)
		}

		var chunk strings.Builder
		if lastEmittedLine >= 0 && winStart > lastEmittedLine+1 {
			chunk.WriteString("--\n")
		}
		from := winStart
		if lastEmittedLine >= from {
			from = lastEmittedLine + 1
		}
		for ln := from; ln <= winEnd; ln++ {
			if ln == m.idx {
				fmt.Fprintf(&chunk, "%s:%d: %s\n", rel, ln+1, formatGrepMatchSnippet(rawLines[ln], m.loc[0], m.loc[1], maxGrepSnippetBytes))
			} else {
				fmt.Fprintf(&chunk, "%s-%d- %s\n", rel, ln+1, formatGrepMatchSnippet(rawLines[ln], 0, 0, maxGrepSnippetBytes))
			}
		}
		row := chunk.String()
		if *shown == maxHits || (*shown > 0 && out.Len()+len(row) > outputBudget) {
			*hasMore = true
			return nil
		}
		out.WriteString(row)
		lastEmittedLine = winEnd
		*shown++
	}
	return nil
}
