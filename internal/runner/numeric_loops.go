package runner

import (
	"regexp"
	"strconv"
	"strings"
)

// Numeric sequences are judgment-only literal lists, never executed by the
// policy. Only unquoted complete shell loops qualify. Mirrors numeric-loops.js.
var numericLoopHeaderRe = regexp.MustCompile(`^(for[ \t]+[A-Za-z_][A-Za-z0-9_]*[ \t]+in[ \t]+)(\{-?\d+\.\.-?\d+(?:\.\.-?\d+)?\}|\$\(seq[ \t]+-?\d+(?:[ \t]+-?\d+){0,2}[ \t]*\))([ \t]*(?:;|\r?\n))`)
var braceIntegerRangeRe = regexp.MustCompile(`^\{(-?\d+)\.\.(-?\d+)(?:\.\.(-?\d+))?\}$`)
var seqIntegerRangeRe = regexp.MustCompile(`^\$\(seq[ \t]+(-?\d+)(?:[ \t]+(-?\d+))?(?:[ \t]+(-?\d+))?[ \t]*\)$`)
var canonicalLoopIntegerRe = regexp.MustCompile(`^-?(?:0|[1-9]\d{0,8})$`)

func normalizeNumericForHeaders(s string) string {
	if strings.Contains(s, "<<") {
		return s // heredoc bodies are data; their own caller may strip them first
	}
	var out strings.Builder
	var quote byte
	prev := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote == '\'' {
			if c == '\'' {
				quote = 0
			}
			continue
		}
		if c == '\\' && i+1 < len(s) {
			i++
			continue
		}
		if c == '"' {
			if quote == '"' {
				quote = 0
			} else {
				quote = '"'
			}
			continue
		}
		if quote != 0 {
			continue
		}
		if c == '\'' {
			quote = c
			continue
		}
		if c == '#' && (i == 0 || isWordBoundary(s[i-1])) {
			for i < len(s) && s[i] != '\n' {
				i++
			}
			continue
		}
		if !wordAt(s, i, "for") {
			continue
		}
		m := numericLoopHeaderRe.FindStringSubmatch(s[i:])
		if m == nil {
			continue
		}
		if !wordAt(s, skipShellSpace(s, i+len(m[0])), "do") {
			continue
		}
		if _, _, ok := findLoopDone(s, i+len(m[0])); !ok {
			continue
		}
		values, ok := numericLoopValues(m[2])
		if !ok {
			continue
		}
		out.WriteString(s[prev:i])
		out.WriteString(m[1] + strings.Join(values, " ") + m[3])
		i += len(m[0]) - 1
		prev = i + 1
	}
	out.WriteString(s[prev:])
	return out.String()
}

func numericLoopValues(expr string) ([]string, bool) {
	brace := braceIntegerRangeRe.FindStringSubmatch(expr)
	parts := brace
	if parts == nil {
		parts = seqIntegerRangeRe.FindStringSubmatch(expr)
	}
	if parts == nil {
		return nil, false
	}
	nums := []int64{}
	for _, part := range parts[1:] {
		if part == "" {
			continue
		}
		if !canonicalLoopIntegerRe.MatchString(part) || part == "-0" {
			return nil, false
		}
		n, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return nil, false
		}
		nums = append(nums, n)
	}
	start, step, end := int64(1), int64(1), nums[0]
	if brace != nil {
		start, end = nums[0], nums[1]
		if len(nums) == 3 {
			step = nums[2]
		}
		if step <= 0 {
			return nil, false
		}
		if start > end {
			step = -step
		}
	} else if len(nums) == 2 {
		start, end = nums[0], nums[1]
	} else if len(nums) == 3 {
		start, step, end = nums[0], nums[1], nums[2]
	}
	if step == 0 || (start < end && step < 0) || (start > end && step > 0) {
		return nil, false
	}
	count := (end-start)/step + 1
	if count > maxLiteralLoopItems {
		return []string{seqLoopPlaceholder}, true // keep the existing read-only-loop fallback
	}
	values := make([]string, 0, int(count))
	for i := int64(0); i < count; i++ {
		values = append(values, strconv.FormatInt(start+i*step, 10))
	}
	return values, true
}
