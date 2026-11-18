//go:build windows

package termhost

import (
	"strings"
	"testing"
	"unicode/utf16"
)

func TestClampConPTYDimAndUTF16EnvBlock(t *testing.T) {
	if got := clampConPTYDim(0, 120); got != 120 {
		t.Fatalf("clampConPTYDim(0, 120) = %d, want 120", got)
	}
	if got := clampConPTYDim(60000, 120); got != 32767 {
		t.Fatalf("clampConPTYDim(60000, 120) = %d, want 32767", got)
	}

	block := buildUTF16EnvBlock([]string{"A=1", "BAD\x00ENTRY=2", "TERM=xterm-256color"})
	if len(block) < 4 || block[len(block)-1] != 0 || block[len(block)-2] != 0 {
		t.Fatalf("expected double-NUL terminated UTF-16 env block, got len=%d", len(block))
	}
	decoded := string(utf16.Decode(block))
	if !strings.Contains(decoded, "A=1\x00") || !strings.Contains(decoded, "TERM=xterm-256color\x00") {
		t.Fatalf("unexpected decoded env block: %q", decoded)
	}
	if strings.Contains(decoded, "BAD") {
		t.Fatalf("expected NUL-containing entry to be filtered out: %q", decoded)
	}
}

func TestDefaultOpenerWindowsLifecycle(t *testing.T) {
	dir := t.TempDir()
	h, err := DefaultOpener(dir, "WinShell", 100, 30)
	if err != nil {
		t.Fatalf("DefaultOpener failed: %v", err)
	}
	defer func() { _ = h.Kill() }()

	if err := h.Resize(132, 40); err != nil {
		t.Fatalf("Resize failed: %v", err)
	}
	if _, err := h.RW.Write([]byte("exit\r\n")); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if err := h.Kill(); err != nil {
		t.Fatalf("Kill failed: %v", err)
	}
}
