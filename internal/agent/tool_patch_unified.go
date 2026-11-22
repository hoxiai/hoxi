package agent

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Lines retain their terminator so a no-newline marker is matched and applied
// on the correct side of the patch.
type unifiedHunk struct {
	oldIndex, newIndex int
	before, after      []string
	context            int
}

var unifiedHeader = regexp.MustCompile(`^@@ -([0-9]+)(?:,([0-9]+))? \+([0-9]+)(?:,([0-9]+))? @@(?: .*)?$`)

func unifiedRange(start, count string) (index, size int, err error) {
	index, err = strconv.Atoi(start)
	if err != nil {
		return 0, 0, err
	}
	size = 1
	if count != "" {
		size, err = strconv.Atoi(count)
		if err != nil {
			return 0, 0, err
		}
	}
	// A zero-length range denotes the gap after the named line.
	if size != 0 {
		if index == 0 {
			return 0, 0, fmt.Errorf("nonempty unified diff range starts at zero")
		}
		index--
	}
	if index > math.MaxInt-size {
		return 0, 0, fmt.Errorf("unified diff range exceeds line number limit")
	}
	return index, size, nil
}

func parseUnifiedHunk(lines []string) (unifiedHunk, int, error) {
	var h unifiedHunk
	m := unifiedHeader.FindStringSubmatch(lines[0])
	if m == nil {
		return h, 0, fmt.Errorf("invalid unified diff header %q", lines[0])
	}
	oldIndex, oldSize, err := unifiedRange(m[1], m[2])
	if err != nil {
		return h, 0, err
	}
	newIndex, newSize, err := unifiedRange(m[3], m[4])
	if err != nil {
		return h, 0, err
	}
	h.oldIndex, h.newIndex = oldIndex, newIndex
	i := 1
	for len(h.before) < oldSize || len(h.after) < newSize {
		if i >= len(lines) || lines[i] == "" {
			return h, 0, fmt.Errorf("incomplete unified diff hunk %s", lines[0])
		}
		kind, body := lines[i][0], lines[i][1:]+"\n"
		i++
		if i < len(lines) && lines[i] == `\ No newline at end of file` {
			body = strings.TrimSuffix(body, "\n")
			i++
		}
		switch kind {
		case ' ':
			h.before = append(h.before, body)
			h.after = append(h.after, body)
			h.context++
		case '-':
			h.before = append(h.before, body)
		case '+':
			h.after = append(h.after, body)
		default:
			return h, 0, fmt.Errorf("invalid unified diff hunk line %q", kind)
		}
	}
	if len(h.before) != oldSize || len(h.after) != newSize {
		return h, 0, fmt.Errorf("line counts disagree with %s", lines[0])
	}
	return h, i, nil
}

func unifiedFilePath(label string) string {
	name, _, _ := strings.Cut(label, "\t")
	name = strings.TrimSpace(name)
	if strings.HasPrefix(name, "a/") || strings.HasPrefix(name, "b/") {
		return name[2:]
	}
	return name
}

func parseUnifiedDiffPatch(lines []string) ([]patchFileOp, error) {
	var ops []patchFileOp
	for i := 0; i < len(lines); {
		line := lines[i]
		i++
		if !strings.HasPrefix(line, "--- ") {
			if strings.HasPrefix(line, "@@") || (len(ops) > 0 && line != "" && strings.ContainsRune(" +-\\", rune(line[0]))) {
				return nil, fmt.Errorf("unexpected line outside unified diff hunk: %q", line)
			}
			continue
		}
		oldPath := unifiedFilePath(line[4:])
		if i == len(lines) || !strings.HasPrefix(lines[i], "+++ ") {
			return nil, fmt.Errorf("missing new path for %s", oldPath)
		}
		newPath := unifiedFilePath(lines[i][4:])
		i++
		if oldPath == "" || newPath == "" || (oldPath == "/dev/null" && newPath == "/dev/null") {
			return nil, fmt.Errorf("invalid unified diff file paths")
		}
		op := patchFileOp{kind: patchOpUpdate, path: oldPath, unified: []unifiedHunk{}}
		switch {
		case oldPath == "/dev/null":
			op.kind, op.path = patchOpAdd, newPath
		case newPath == "/dev/null":
			op.kind = patchOpDelete
		case newPath != oldPath:
			op.moveTo = newPath
		}
		for i < len(lines) && strings.HasPrefix(lines[i], "@@") {
			h, n, err := parseUnifiedHunk(lines[i:])
			if err != nil {
				return nil, fmt.Errorf("%s: %w", op.path, err)
			}
			op.unified = append(op.unified, h)
			i += n
		}
		ops = append(ops, op)
	}
	return ops, nil
}

func unifiedHunkPosition(src []string, h unifiedHunk, cursor, expected int) (int, error) {
	last := len(src) - len(h.before)
	if expected >= cursor && expected <= last && slices.Equal(src[expected:expected+len(h.before)], h.before) {
		return expected, nil
	}
	if len(h.before) == 0 {
		return -1, fmt.Errorf("insertion position is outside the source range")
	}
	found := -1
	for i := cursor; i <= last; i++ {
		if slices.Equal(src[i:i+len(h.before)], h.before) {
			if found >= 0 {
				return -1, fmt.Errorf("ambiguous hunk context for source line %d", h.oldIndex+1)
			}
			found = i
		}
	}
	if found < 0 {
		return -1, fmt.Errorf("hunk at source line %d did not match file contents", h.oldIndex+1)
	}
	return found, nil
}

func applyUnifiedHunks(source string, hunks []unifiedHunk) (string, error) {
	src := strings.SplitAfter(source, "\n")
	if src[len(src)-1] == "" {
		src = src[:len(src)-1]
	}
	var result []string
	cursor, drift, oldEnd, newEnd := 0, 0, 0, 0
	for _, h := range hunks {
		if h.oldIndex < oldEnd || h.newIndex < newEnd || h.oldIndex-oldEnd != h.newIndex-newEnd {
			return "", fmt.Errorf("out-of-order or inconsistent unified diff ranges")
		}
		pos, err := unifiedHunkPosition(src, h, cursor, h.oldIndex+drift)
		if err != nil {
			return "", err
		}
		result = append(result, src[cursor:pos]...)
		result = append(result, h.after...)
		cursor = pos + len(h.before)
		drift = pos - h.oldIndex
		oldEnd, newEnd = h.oldIndex+len(h.before), h.newIndex+len(h.after)
	}
	result = append(result, src[cursor:]...)
	for i, line := range result {
		if i+1 < len(result) && !strings.HasSuffix(line, "\n") {
			return "", fmt.Errorf("missing line terminator before end of patched file")
		}
	}
	return strings.Join(result, ""), nil
}
