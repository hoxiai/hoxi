//go:build windows

package subproc

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/windows"
)

var (
	kernel32DLL            = windows.NewLazySystemDLL("kernel32.dll")
	procSetConsoleCP       = kernel32DLL.NewProc("SetConsoleCP")
	procSetConsoleOutputCP = kernel32DLL.NewProc("SetConsoleOutputCP")
	procGetOEMCP           = kernel32DLL.NewProc("GetOEMCP")
	procGetACP             = kernel32DLL.NewProc("GetACP")
)

const (
	cpACP             = 0
	cpUTF8            = 65001
	mbErrInvalidChars = 0x00000008
)

func decodePlatformLegacyBytes(b []byte) (string, bool) {
	if len(b) == 0 {
		return "", true
	}
	codePages := []uint32{cpACP}
	if procGetOEMCP.Find() == nil {
		if oem, _, _ := procGetOEMCP.Call(); oem != 0 && oem != cpUTF8 {
			codePages = append(codePages, uint32(oem))
		}
	}
	if procGetACP.Find() == nil {
		if acp, _, _ := procGetACP.Call(); acp != 0 && acp != cpUTF8 {
			codePages = append(codePages, uint32(acp))
		}
	}
	for _, cp := range codePages {
		if s, ok := multiByteToUTF8(cp, b); ok {
			return s, true
		}
	}
	return "", false
}

func multiByteToUTF8(codePage uint32, b []byte) (string, bool) {
	if len(b) == 0 {
		return "", true
	}
	n, err := windows.MultiByteToWideChar(codePage, mbErrInvalidChars, &b[0], int32(len(b)), nil, 0)
	if err != nil || n <= 0 {
		return "", false
	}
	buf := make([]uint16, n)
	n2, err := windows.MultiByteToWideChar(codePage, mbErrInvalidChars, &b[0], int32(len(b)), &buf[0], n)
	if err != nil || n2 <= 0 {
		return "", false
	}
	s := windows.UTF16ToString(buf[:n2])
	if !utf8.ValidString(s) || strings.ContainsRune(s, utf8.RuneError) {
		return "", false
	}
	return s, true
}
