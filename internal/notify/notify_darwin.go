//go:build darwin && cgo

package notify

/*
#cgo CFLAGS: -fblocks
#cgo LDFLAGS: -framework Cocoa -framework UserNotifications
#include <stdlib.h>

void hoxi_notify_start(void);
int hoxi_notify_show(const char* title, const char* body, const char* sessionID, const char* identifier);
*/
import "C"

import (
	"fmt"
	"sync/atomic"
	"time"
	"unsafe"
)

var notificationSequence atomic.Uint64

func platformStart() {
	C.hoxi_notify_start()
}

func platformShow(req Request) error {
	title := C.CString(req.Title)
	body := C.CString(req.Body)
	sessionID := C.CString(req.SessionID)
	identifier := C.CString(fmt.Sprintf(
		"hoxi-%d-%d",
		time.Now().UnixNano(),
		notificationSequence.Add(1),
	))
	defer C.free(unsafe.Pointer(title))
	defer C.free(unsafe.Pointer(body))
	defer C.free(unsafe.Pointer(sessionID))
	defer C.free(unsafe.Pointer(identifier))
	if C.hoxi_notify_show(title, body, sessionID, identifier) == 0 {
		return ErrUnavailable
	}
	return nil
}

//export HoxiNotificationClicked
func HoxiNotificationClicked(sessionID *C.char) {
	if sessionID == nil {
		return
	}
	go dispatchClick(C.GoString(sessionID))
}
