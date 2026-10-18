//go:build !darwin || !cgo

package statusbar

import "context"

func Start(_ context.Context) {}
func Update(_ Summary)        {}
func Stop()                   {}

func HoxiShowMainWindow()  {}
func HoxiQuitApplication() {}
