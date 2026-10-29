//go:build windows

package computer

import (
	"testing"
	"unsafe"
)

func TestWindowsInputABI(t *testing.T) {
	// Win64 INPUT is a DWORD, four alignment bytes, then a 32-byte union.
	// A wrong layout compiles but makes SendInput fail on real Windows.
	var input desktopInput
	if unsafe.Sizeof(input) != 40 || unsafe.Offsetof(input.Data) != 8 {
		t.Fatal("invalid Win64 INPUT layout")
	}
}
