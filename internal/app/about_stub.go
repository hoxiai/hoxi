//go:build !darwin || !cgo

package app

import (
	"context"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

func installNativeAbout(_, _ string) {}

func emitDesktopIntent(ctx context.Context) {
	if ctx == nil {
		return
	}
	wailsruntime.EventsEmit(ctx, desktopIntentEvent)
}
