//go:build (!darwin && !windows) || (darwin && !cgo)

package computer

import (
	"context"
	"errors"
	"runtime"
)

type nativeBackend struct{}

func (nativeBackend) Permissions(context.Context, string) (Permissions, error) {
	return Permissions{Platform: runtime.GOOS, ForegroundOnly: true}, nil
}
func (nativeBackend) Apps(context.Context) ([]App, error) { return []App{}, nil }
func (nativeBackend) Inspect(context.Context, App, string, bool) (Snapshot, error) {
	return Snapshot{}, errors.New("computer_use_not_supported")
}
func (nativeBackend) Input(context.Context, App, Window, Action) error {
	return errors.New("computer_use_not_supported")
}
