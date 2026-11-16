//go:build (!darwin || !cgo) && !windows

package updater

import "errors"

func StartNative(canInstall func(force bool) bool, release func()) error {
	return errors.New("native updater requires macOS with cgo and a configured application bundle")
}

func NativeAvailable() bool { return false }

func CheckNative() error { return errors.New("native updater is unavailable") }

func StopNative() {}
