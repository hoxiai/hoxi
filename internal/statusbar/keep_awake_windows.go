//go:build windows

package statusbar

import (
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modkernel32              = windows.NewLazySystemDLL("kernel32.dll")
	procPowerCreateRequest   = modkernel32.NewProc("PowerCreateRequest")
	procPowerSetRequest      = modkernel32.NewProc("PowerSetRequest")
	procPowerClearRequest    = modkernel32.NewProc("PowerClearRequest")
	procSetThreadExecState   = modkernel32.NewProc("SetThreadExecutionState")
)

const (
	powerRequestContextVersion      = 0
	powerRequestContextSimpleString = 0x00000001
	powerRequestSystemRequired      = 1

	esContinuous     = 0x80000000
	esSystemRequired = 0x00000001
)

// reasonContext matches Win32 REASON_CONTEXT (including union padding for Detailed).
type reasonContext struct {
	Version            uint32
	Flags              uint32
	SimpleReasonString *uint16
	_pad               [3]uintptr
}

func startOSKeepAwake() (func(), bool) {
	reason, _ := syscall.UTF16PtrFromString("Hoxi agent session is running")
	ctx := reasonContext{
		Version:            powerRequestContextVersion,
		Flags:              powerRequestContextSimpleString,
		SimpleReasonString: reason,
	}
	if err := procPowerCreateRequest.Find(); err == nil &&
		procPowerSetRequest.Find() == nil &&
		procPowerClearRequest.Find() == nil {
		h, _, _ := procPowerCreateRequest.Call(uintptr(unsafe.Pointer(&ctx)))
		if h != 0 && windows.Handle(h) != windows.InvalidHandle {
			r1, _, _ := procPowerSetRequest.Call(h, powerRequestSystemRequired)
			if r1 != 0 {
				var once sync.Once
				return func() {
					once.Do(func() {
						_, _, _ = procPowerClearRequest.Call(h, powerRequestSystemRequired)
						_ = windows.CloseHandle(windows.Handle(h))
					})
				}, true
			}
			_ = windows.CloseHandle(windows.Handle(h))
		}
	}

	// Fallback: SetThreadExecutionState is per-thread, so pin a dedicated OS thread
	// until stop() is called.
	if err := procSetThreadExecState.Find(); err != nil {
		return nil, false
	}
	ready := make(chan bool, 1)
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(doneCh)
		r1, _, _ := procSetThreadExecState.Call(esContinuous | esSystemRequired)
		if r1 == 0 {
			ready <- false
			return
		}
		ready <- true
		<-stopCh
		_, _, _ = procSetThreadExecState.Call(esContinuous)
	}()
	if !<-ready {
		return nil, false
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			close(stopCh)
			<-doneCh
		})
	}, true
}
