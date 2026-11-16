//go:build darwin && cgo

package app

/*
#cgo LDFLAGS: -framework Cocoa -framework WebKit

#include <stdlib.h>

void hoxi_about_install(const char* applicationName, const char* version);
void hoxi_emit_desktop_intent(void);
*/
import "C"

import (
	"context"
	"unsafe"
)

func installNativeAbout(applicationName, version string) {
	cName := C.CString(applicationName)
	defer C.free(unsafe.Pointer(cName))
	cVersion := C.CString(version)
	defer C.free(unsafe.Pointer(cVersion))
	C.hoxi_about_install(cName, cVersion)
}

func emitDesktopIntent(ctx context.Context) {
	if ctx == nil {
		return
	}
	C.hoxi_emit_desktop_intent()
}
