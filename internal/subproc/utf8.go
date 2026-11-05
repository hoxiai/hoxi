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
// Valid UTF-8 is returned directly; lines emitted in Windows legacy code pages
// (such as CP936/GBK/GB18030 on Chinese Windows) are decoded to UTF-8.
func DecodeOutputBytes(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	if utf8.Valid(b) {
		return string(b)
	}
	if trimmed, ok := trimIncompleteUTF8Edges(b); ok && utf8.Valid(trimmed) {
		return string(trimmed)
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
			sb.Write(line)
			isFirst = false
			continue
		}
		if isFirst || isLast {
			if trimmed, ok := trimIncompleteUTF8Edges(line); ok && utf8.Valid(trimmed) {
				sb.Write(trimmed)
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
