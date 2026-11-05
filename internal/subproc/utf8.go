package subproc

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

const windowsBashUTF8InitScript = `if command -v chcp.com >/dev/null 2>&1; then
  chcp.com 65001 >/dev/null 2>&1 || true
elif [ -x "/c/Windows/System32/chcp.com" ]; then
  /c/Windows/System32/chcp.com 65001 >/dev/null 2>&1 || true
fi
export LANG="${LANG:-C.UTF-8}"
export LC_CTYPE="${LC_CTYPE:-UTF-8}"
export PYTHONUTF8="${PYTHONUTF8:-1}"
export PYTHONIOENCODING="${PYTHONIOENCODING:-utf-8}"
export LESSCHARSET="${LESSCHARSET:-utf-8}"
`

var (
	windowsBashEnvOnce sync.Once
	windowsBashEnvPath string
)

// WindowsBashEnvScriptPath ensures the non-interactive bash UTF-8 console init
// script exists in the temporary directory and returns its forward-slash path.
func WindowsBashEnvScriptPath() string {
	windowsBashEnvOnce.Do(func() {
		p := filepath.Join(os.TempDir(), "hoxi-bash-utf8.sh")
		if existing, err := os.ReadFile(p); err != nil || string(existing) != windowsBashUTF8InitScript {
			_ = os.WriteFile(p, []byte(windowsBashUTF8InitScript), 0o644)
		}
		windowsBashEnvPath = filepath.ToSlash(p)
	})
	return windowsBashEnvPath
}

// WithUTF8Env ensures UTF-8 locale and runtime output encoding variables are
// present in env without overwriting values already set by the caller or user.
func WithUTF8Env(env []string) []string {
	hasKey := func(name string) bool {
		prefix := name + "="
		for _, kv := range env {
			if len(kv) >= len(prefix) && strings.EqualFold(kv[:len(prefix)], prefix) && strings.TrimSpace(kv[len(prefix):]) != "" {
				return true
			}
		}
		return false
	}

	out := append([]string(nil), env...)
	hasLCAll := hasKey("LC_ALL")
	if !hasLCAll && !hasKey("LANG") {
		out = append(out, "LANG=C.UTF-8")
	}
	if !hasLCAll && !hasKey("LC_CTYPE") {
		out = append(out, "LC_CTYPE=UTF-8")
	}
	if !hasKey("PYTHONUTF8") {
		out = append(out, "PYTHONUTF8=1")
	}
	if !hasKey("PYTHONIOENCODING") {
		out = append(out, "PYTHONIOENCODING=utf-8")
	}
	if !hasKey("LESSCHARSET") {
		out = append(out, "LESSCHARSET=utf-8")
	}
	if runtime.GOOS == "windows" && !hasKey("BASH_ENV") {
		if scriptPath := WindowsBashEnvScriptPath(); scriptPath != "" {
			out = append(out, "BASH_ENV="+scriptPath)
		}
	}
	return out
}

// TruncateUTF8Tail keeps the last maxBytes of b while advancing past any
// leading UTF-8 continuation bytes so a multi-byte character is not sliced in half.
func TruncateUTF8Tail(b []byte, maxBytes int) []byte {
	if maxBytes <= 0 || len(b) <= maxBytes {
		return b
	}
	keep := b[len(b)-maxBytes:]
	for i := 0; i < 3 && len(keep) > 0 && isUTF8Continuation(keep[0]); i++ {
		keep = keep[1:]
	}
	return keep
}

// DecodeOutputBytes converts process output bytes to a valid UTF-8 string.
// Valid UTF-8 is returned directly (with CP936-decoded-UTF8 Mojibake repaired);
// lines emitted in Windows legacy code pages (such as CP936/GBK/GB18030 on
// Chinese Windows) are decoded to UTF-8.
func DecodeOutputBytes(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	if utf8.Valid(b) {
		return RepairUTF8Mojibake(string(b))
	}
	if trimmed, ok := trimIncompleteUTF8Edges(b); ok && utf8.Valid(trimmed) {
		return RepairUTF8Mojibake(string(trimmed))
	}
	if bytes.IndexByte(b, 0) >= 0 {
		return strings.ToValidUTF8(string(b), "\uFFFD")
	}

	var sb strings.Builder
	sb.Grow(len(b))
	rest := b
	isFirst := true
	for len(rest) > 0 {
		var line []byte
		idx := bytes.IndexByte(rest, '\n')
		isLast := idx < 0
		if isLast {
			line = rest
			rest = nil
		} else {
			line = rest[:idx+1]
			rest = rest[idx+1:]
		}
		if utf8.Valid(line) {
			sb.WriteString(RepairUTF8Mojibake(string(line)))
			isFirst = false
			continue
		}
		if isFirst || isLast {
			if trimmed, ok := trimIncompleteUTF8Edges(line); ok && utf8.Valid(trimmed) {
				sb.WriteString(RepairUTF8Mojibake(string(trimmed)))
				isFirst = false
				continue
			}
		}
		sb.WriteString(decodeLegacyLine(line))
		isFirst = false
	}
	return sb.String()
}

func decodeLegacyLine(line []byte) string {
	if s, ok := decodePlatformLegacyBytes(line); ok {
		return s
	}
	if decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(line); err == nil && utf8.Valid(decoded) && !bytes.ContainsRune(decoded, utf8.RuneError) {
		return string(decoded)
	}
	return strings.ToValidUTF8(string(line), "\uFFFD")
}

func isUTF8Continuation(b byte) bool {
	return b >= 0x80 && b < 0xC0
}

func expectedUTF8SeqLen(lead byte) int {
	switch {
	case lead >= 0xC2 && lead <= 0xDF:
		return 2
	case lead >= 0xE0 && lead <= 0xEF:
		return 3
	case lead >= 0xF0 && lead <= 0xF4:
		return 4
	default:
		return 0
	}
}

// trimIncompleteUTF8Edges strips up to 3 leading UTF-8 continuation bytes and
// up to 3 trailing incomplete UTF-8 starter/continuation bytes at chunk boundaries.
func trimIncompleteUTF8Edges(b []byte) ([]byte, bool) {
	lead := 0
	for lead < len(b) && lead < 3 && isUTF8Continuation(b[lead]) {
		lead++
	}
	sub := b[lead:]
	trimmed, tailTrimmed := trimIncompleteUTF8Tail(sub)
	if lead > 0 || tailTrimmed {
		return trimmed, true
	}
	return b, false
}

// trimIncompleteUTF8Tail strips 1 to 3 trailing bytes if they form an
// unfinished multi-byte UTF-8 sequence at the end of a streaming chunk.
func trimIncompleteUTF8Tail(b []byte) ([]byte, bool) {
	n := len(b)
	for tail := 1; tail <= 3 && tail <= n; tail++ {
		start := n - tail
		lead := b[start]
		if isUTF8Continuation(lead) {
			continue
		}
		want := expectedUTF8SeqLen(lead)
		if want > tail {
			return b[:start], true
		}
		return b, false
	}
	return b, false
}

var (
	gbkReverseOnce  sync.Once
	gbkReverseTable [65536]uint16
)

var cp936PUAOverrides = [][2]uint16{
	{0x20ac, 0x0080}, {0xe78d, 0xa6d9}, {0xe78e, 0xa6da}, {0xe78f, 0xa6db}, {0xe790, 0xa6dc},
	{0xe791, 0xa6dd}, {0xe792, 0xa6de}, {0xe793, 0xa6df}, {0xe794, 0xa6ec}, {0xe795, 0xa6ed},
	{0xe796, 0xa6f3}, {0xe81e, 0xfe59}, {0xe826, 0xfe61}, {0xe82b, 0xfe66}, {0xe82c, 0xfe67},
	{0xe832, 0xfe6d}, {0xe843, 0xfe7e}, {0xe854, 0xfe90}, {0xe864, 0xfea0},
}

func ensureGBKReverseTable() *[65536]uint16 {
	gbkReverseOnce.Do(func() {
		dec := simplifiedchinese.GB18030.NewDecoder()
		var pair [2]byte
		for b1 := 0x81; b1 <= 0xfe; b1++ {
			pair[0] = byte(b1)
			for b2 := 0x40; b2 <= 0xfe; b2++ {
				if b2 == 0x7f {
					continue
				}
				pair[1] = byte(b2)
				dec.Reset()
				out, err := dec.Bytes(pair[:])
				if err != nil {
					continue
				}
				r, size := utf8.DecodeRune(out)
				if size == len(out) && r >= 0x80 && r < 0x10000 && r != utf8.RuneError && gbkReverseTable[r] == 0 {
					gbkReverseTable[r] = uint16((b1 << 8) | b2)
				}
			}
		}
		// Populate CP936/GBK user-defined PUA ranges (U+E000..U+E765) that Windows
		// MultiByteToWideChar(936) and WHATWG gb18030 emit for unassigned 2-byte sequences.
		for b1 := 0xaa; b1 <= 0xaf; b1++ {
			for b2 := 0xa1; b2 <= 0xfe; b2++ {
				r := 0xe000 + (b1-0xaa)*94 + (b2 - 0xa1)
				gbkReverseTable[r] = uint16((b1 << 8) | b2)
			}
		}
		for b1 := 0xf8; b1 <= 0xfe; b1++ {
			for b2 := 0xa1; b2 <= 0xfe; b2++ {
				r := 0xe234 + (b1-0xf8)*94 + (b2 - 0xa1)
				gbkReverseTable[r] = uint16((b1 << 8) | b2)
			}
		}
		for b1 := 0xa1; b1 <= 0xa7; b1++ {
			for b2 := 0x40; b2 <= 0xa0; b2++ {
				if b2 == 0x7f {
					continue
				}
				off := b2 - 0x40
				if b2 > 0x7f {
					off--
				}
				r := 0xe4c6 + (b1-0xa1)*96 + off
				gbkReverseTable[r] = uint16((b1 << 8) | b2)
			}
		}
		for b2 := 0xc5; b2 <= 0xe9; b2++ {
			gbkReverseTable[0x3105+(b2-0xc5)] = uint16((0xa8 << 8) | b2)
		}
		for _, ov := range cp936PUAOverrides {
			if ov[0] == 0x20ac || gbkReverseTable[ov[0]] == 0 {
				gbkReverseTable[ov[0]] = ov[1]
			}
		}
	})
	return &gbkReverseTable
}

func isAllowedRecoveredUTF8(s string) bool {
	if s == "" || !utf8.ValidString(s) {
		return false
	}
	cjk := 0
	for _, r := range s {
		allowed := r == 0x09 || r == 0x0a || r == 0x0d || (r >= 0x20 && r <= 0x7e) ||
			r == 0x00b7 || r == 0x00d7 || r == 0x00f7 ||
			(r >= 0x2010 && r <= 0x26ff) || (r >= 0x3000 && r <= 0x303f) ||
			(r >= 0x4e00 && r <= 0x9fa5) || (r >= 0xff01 && r <= 0xff5e)
		if !allowed {
			return false
		}
		if (r >= 0x4e00 && r <= 0x9fa5) || (r >= 0x3000 && r <= 0x303f) || (r >= 0xff01 && r <= 0xff5e) {
			cjk++
		}
	}
	return cjk > 0
}

// RepairUTF8Mojibake recovers UTF-8 text that was mistakenly decoded as CP936/GBK
// (e.g. "涓枃" -> "中文", "娴嬭瘯鎴愬姛" -> "测试成功", "涓€" -> "一") while leaving
// normal Chinese, English, symbols and code untouched.
func RepairUTF8Mojibake(s string) string {
	hasNonASCII := false
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			hasNonASCII = true
			break
		}
	}
	if !hasNonASCII {
		return s
	}
	table := ensureGBKReverseTable()
	runes := []rune(s)
	if len(runes) < 2 {
		return s
	}
	var sb strings.Builder
	anyChanged := false
	i := 0
	for i < len(runes) {
		if runes[i] < 0x80 || runes[i] >= 0x10000 || table[runes[i]] == 0 {
			if anyChanged {
				sb.WriteRune(runes[i])
			}
			i++
			continue
		}
		j := i + 1
		for j < len(runes) && runes[j] >= 0x80 && runes[j] < 0x10000 && table[runes[j]] != 0 {
			j++
		}
		if j-i < 2 {
			if anyChanged {
				sb.WriteRune(runes[i])
			}
			i = j
			continue
		}
		run := runes[i:j]
		recovered, changed := tryRecoverGBKRun(run, table)
		if changed && !anyChanged {
			sb.Grow(len(s))
			sb.WriteString(string(runes[:i]))
			anyChanged = true
		}
		if anyChanged {
			if changed {
				sb.WriteString(recovered)
			} else {
				sb.WriteString(string(run))
			}
		}
		i = j
	}
	if !anyChanged {
		return s
	}
	return sb.String()
}

func tryRecoverGBKRun(run []rune, table *[65536]uint16) (string, bool) {
	raw := make([]byte, 0, len(run)*2)
	offsets := make([]int, len(run)+1)
	for idx, r := range run {
		offsets[idx] = len(raw)
		v := table[r]
		if v <= 0xff {
			raw = append(raw, byte(v))
		} else {
			raw = append(raw, byte(v>>8), byte(v))
		}
	}
	offsets[len(run)] = len(raw)

	var sb strings.Builder
	pos := 0
	changed := false
	for pos < len(run) {
		matchedEnd := -1
		matchedStr := ""
		for end := len(run); end >= pos+2; end-- {
			bStart := offsets[pos]
			bEnd := offsets[end]
			if bEnd-bStart < 3 {
				continue
			}
			slice := raw[bStart:bEnd]
			if !utf8.Valid(slice) {
				continue
			}
			candidate := string(slice)
			if candidate != string(run[pos:end]) && isAllowedRecoveredUTF8(candidate) {
				matchedEnd = end
				matchedStr = candidate
				break
			}
		}
		if matchedEnd != -1 {
			if !changed {
				sb.WriteString(string(run[:pos]))
				changed = true
			}
			sb.WriteString(matchedStr)
			pos = matchedEnd
		} else {
			if changed {
				sb.WriteRune(run[pos])
			}
			pos++
		}
	}
	if !changed {
		return "", false
	}
	return sb.String(), true
}
