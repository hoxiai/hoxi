//go:build windows

package subproc

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/sys/windows"
	"golang.org/x/text/encoding/simplifiedchinese"
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
	// GitHub's Windows runners use a Western ANSI code page, so the ACP path
	// decodes GBK bytes byte-for-byte into mojibake. When the buffer really is
	// Chinese legacy output, prefer the GB18030 reading (a superset of
	// CP936/GBK) over that lossy result.
	gbs, gbOK := decodeGB18030(b)
	for _, cp := range codePages {
		if s, ok := multiByteToUTF8(cp, b); ok {
			if gbOK && hasHan(gbs) && !hasHan(s) {
				return gbs, true
			}
			return s, true
		}
	}
	if gbOK {
		return gbs, true
	}
	return "", false
}

func decodeGB18030(b []byte) (string, bool) {
	decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(b)
	if err != nil || len(decoded) == 0 || !utf8.Valid(decoded) || strings.ContainsRune(string(decoded), utf8.RuneError) {
		return "", false
	}
	return string(decoded), true
}

// hasHan reports whether s carries at least one CJK ideograph, which is how a
// genuine CP936/GB18030 payload is told apart from Western single-byte text.
func hasHan(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
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
