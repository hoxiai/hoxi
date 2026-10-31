//go:build windows

package app

import (
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modUser32                    = windows.NewLazySystemDLL("user32.dll")
	procFindWindowExW            = modUser32.NewProc("FindWindowExW")
	procSendMessageTimeoutW      = modUser32.NewProc("SendMessageTimeoutW")
	procAllowSetForegroundWindow = modUser32.NewProc("AllowSetForegroundWindow")
)

const (
	hwndMessage     = ^uintptr(2) // HWND_MESSAGE, (HWND)-3
	wmCopyData      = 0x004A
	smtoAbortIfHung = 0x0002
	forwardTimeout  = 5000 // milliseconds
	// WMCOPYDATA_SINGLE_INSTANCE_DATA in Wails v2.
	wailsCopyDataID = 1542
)

type copyDataStruct struct {
	dwData uintptr
	cbData uint32
	lpData uintptr
}

type windowsInstanceProbe struct {
	className  string
	windowName string
	claimName  string
	mutex      windows.Handle
}

func newInstanceProbe(uniqueID string) instanceProbe {
	className, windowName := wailsInstanceNames(uniqueID)
	return &windowsInstanceProbe{className: className, windowName: windowName, claimName: launchClaimName(uniqueID)}
}

// claim owns a named mutex for the life of the process. A mutex (not just its
// existence) is used so a crashed primary leaves it abandoned and the next
// launch takes over instead of waiting for a window that never comes.
func (p *windowsInstanceProbe) claim() bool {
	if p.mutex == 0 {
		name, err := windows.UTF16PtrFromString(p.claimName)
		if err != nil {
			return true
		}
		h, _ := windows.CreateMutex(nil, false, name)
		if h == 0 {
			return true // no probe possible; keep the old startup path
		}
		p.mutex = h
	}
	// Mutex ownership belongs to the OS thread. Keep this goroutine (Run's
	// main goroutine, which also runs the Wails UI loop) on it for good so the
	// thread never exits and abandons the claim while Hoxi is running.
	runtime.LockOSThread()
	event, err := windows.WaitForSingleObject(p.mutex, 0)
	switch {
	case err == nil && (event == windows.WAIT_OBJECT_0 || event == windows.WAIT_ABANDONED):
		return true
	case err == nil && event == uint32(windows.WAIT_TIMEOUT):
		_ = windows.CloseHandle(p.mutex)
		p.mutex = 0
		runtime.UnlockOSThread()
		return false
	default:
		runtime.UnlockOSThread()
		return true
	}
}

func (p *windowsInstanceProbe) forward(payload string) (bool, error) {
	className, err := windows.UTF16PtrFromString(p.className)
	if err != nil {
		return false, err
	}
	windowName, err := windows.UTF16PtrFromString(p.windowName)
	if err != nil {
		return false, err
	}
	hwnd, _, _ := procFindWindowExW.Call(hwndMessage, 0,
		uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(windowName)))
	if hwnd == 0 {
		return false, nil
	}
	// This process was just started by the user or by Windows, so it may move
	// the foreground; pass that right on so the running window can come forward.
	var pid uint32
	if _, err := windows.GetWindowThreadProcessId(windows.HWND(hwnd), &pid); err == nil && pid != 0 {
		_, _, _ = procAllowSetForegroundWindow.Call(uintptr(pid))
	}
	data, err := windows.UTF16FromString(payload)
	if err != nil {
		return true, err
	}
	cds := copyDataStruct{
		dwData: wailsCopyDataID,
		cbData: uint32(len(data) * 2), // includes the terminating NUL
		lpData: uintptr(unsafe.Pointer(&data[0])),
	}
	var result uintptr
	ok, _, callErr := procSendMessageTimeoutW.Call(hwnd, wmCopyData, 0, uintptr(unsafe.Pointer(&cds)),
		smtoAbortIfHung, forwardTimeout, uintptr(unsafe.Pointer(&result)))
	runtime.KeepAlive(data)
	if ok == 0 {
		return true, fmt.Errorf("send launch arguments: %w", callErr)
	}
	return true, nil
}
