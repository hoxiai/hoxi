//go:build darwin && cgo

package app

/*
#cgo LDFLAGS: -framework Cocoa -framework WebKit

#include <stdlib.h>

void hoxi_webview_downloads_install(void);
char* hoxi_test_sanitize_download_filename(const char* suggested, const char* respSuggested, const char* mimeType);
int hoxi_test_is_attachment_disposition(const char* headerValue);
*/
import "C"

import "unsafe"

func installNativeWebViewDownloads() {
	C.hoxi_webview_downloads_install()
}

func sanitizeNativeDownloadFilename(suggested, responseSuggested, mimeType string) string {
	cSuggested := C.CString(suggested)
	defer C.free(unsafe.Pointer(cSuggested))
	cResp := C.CString(responseSuggested)
	defer C.free(unsafe.Pointer(cResp))
	cMime := C.CString(mimeType)
	defer C.free(unsafe.Pointer(cMime))
	out := C.hoxi_test_sanitize_download_filename(cSuggested, cResp, cMime)
	if out == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(out))
	return C.GoString(out)
}

func isAttachmentContentDisposition(header string) bool {
	cHeader := C.CString(header)
	defer C.free(unsafe.Pointer(cHeader))
	return C.hoxi_test_is_attachment_disposition(cHeader) != 0
}
