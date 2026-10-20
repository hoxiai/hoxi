//go:build darwin && cgo

package app

/*
#include <stdlib.h>

void hoxi_about_install(const char* applicationName, const char* version);
*/
import "C"

import "unsafe"

func installNativeAbout(applicationName, version string) {
	cName := C.CString(applicationName)
	defer C.free(unsafe.Pointer(cName))
	cVersion := C.CString(version)
	defer C.free(unsafe.Pointer(cVersion))
	C.hoxi_about_install(cName, cVersion)
}
