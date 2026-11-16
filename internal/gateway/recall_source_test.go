package gateway

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestClipRunesKeepsUTF8Intact(t *testing.T) {
	value := strings.Repeat("中文", 5)
	clipped := clipRunes(value, 4)
	if !utf8.ValidString(clipped) {
		t.Fatalf("clipRunes produced invalid UTF-8: %q", clipped)
	}
	if clipped != "中文中文" {
		t.Fatalf("clipRunes = %q", clipped)
	}
	if short := clipRunes("abc", 10); short != "abc" {
		t.Fatalf("short value changed: %q", short)
	}
}
