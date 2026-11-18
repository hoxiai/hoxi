//go:build (!darwin || !cgo) && !windows

package app

import "context"

func primaryScreenWorkArea() screenWorkArea {
	return screenWorkArea{}
}

func startPlatformWindowObserver(_ context.Context, _ *windowGeometryTracker) {}

func stopPlatformWindowObserver() {}

func capturePlatformWindowState(_ context.Context, _ *windowGeometryTracker) {}
