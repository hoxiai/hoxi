//go:build darwin && cgo

package notify

/*
#cgo CFLAGS: -fblocks
#cgo LDFLAGS: -framework Cocoa -framework UserNotifications
#include <stdlib.h>

void hoxi_notify_start(void);
int hoxi_notify_show(const char* title, const char* body, const char* sessionID, const char* identifier);
int hoxi_notify_authorization_status(void);
int hoxi_notify_request_authorization(void);
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

func platformStatus() PermissionStatus {
	return permissionStatusFromCode(int(C.hoxi_notify_authorization_status()))
}

func platformRequestPermission() PermissionStatus {
	return permissionStatusFromCode(int(C.hoxi_notify_request_authorization()))
}

// permissionStatusFromCode maps a UNAuthorizationStatus raw value (or the -1
// sentinel for "no native transport") to a PermissionStatus.
func permissionStatusFromCode(code int) PermissionStatus {
	switch code {
	case 0:
		return PermissionNotDetermined
	case 1:
		return PermissionDenied
	case 2:
		return PermissionAuthorized
	case 3:
		return PermissionProvisional
	case 4:
		return PermissionEphemeral
	default:
		return PermissionUnavailable
	}
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
