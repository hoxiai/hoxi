//go:build darwin && cgo

package app

/*
#cgo LDFLAGS: -framework Cocoa -framework WebKit -framework CoreGraphics

void hoxi_primary_screen_work_area(int* outWidth, int* outHeight);
void hoxi_window_observer_start(void);
void hoxi_window_observer_stop(void);
*/
import "C"

import (
	"context"
	"sync/atomic"
)

var activeWindowTracker atomic.Pointer[windowGeometryTracker]

func primaryScreenWorkArea() screenWorkArea {
	var w, h C.int
	C.hoxi_primary_screen_work_area(&w, &h)
	return screenWorkArea{
		Width:  int(w),
		Height: int(h),
	}
}

func startPlatformWindowObserver(_ context.Context, tracker *windowGeometryTracker) {
	if tracker == nil {
		return
	}
	activeWindowTracker.Store(tracker)
	C.hoxi_window_observer_start()
}

func stopPlatformWindowObserver() {
	activeWindowTracker.Store(nil)
	C.hoxi_window_observer_stop()
}

func capturePlatformWindowState(_ context.Context, _ *windowGeometryTracker) {
	// On macOS, NSWindowDidResizeNotification pushes every normal window resize
	// synchronously from the Cocoa main thread into activeWindowTracker.
}

//export hoxiGoWindowCalibrateInitialFrame
func hoxiGoWindowCalibrateInitialFrame(width, height C.int) {
	if tracker := activeWindowTracker.Load(); tracker != nil {
		tracker.CalibrateInitialFrame(int(width), int(height))
	}
}

//export hoxiGoWindowDidResize
func hoxiGoWindowDidResize(width, height C.int) {
	if tracker := activeWindowTracker.Load(); tracker != nil {
		tracker.RecordNativeFrameSize(int(width), int(height))
	}
}
