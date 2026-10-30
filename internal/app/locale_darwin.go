//go:build darwin && cgo

package app

/*
#include <stdlib.h>

char* hoxi_preferred_language(void);
*/
import "C"

import (
	"strings"
	"unsafe"
)

// systemLocale maps the user's first macOS preferred language to a Hoxi UI
// locale, for native dialogs raised before the web UI reported its locale.
func systemLocale() string {
	raw := C.hoxi_preferred_language()
	if raw == nil {
		return "en-US"
	}
	defer C.free(unsafe.Pointer(raw))
	if strings.HasPrefix(C.GoString(raw), "zh") {
		return "zh-CN"
	}
	return "en-US"
}
