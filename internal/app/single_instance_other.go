//go:build !windows

package app

// newInstanceProbe returns nil off Windows: macOS hands repeat launches and
// hoxi:// links to the running app through LaunchServices without starting a
// new process, and Wails' own lock covers the rest.
func newInstanceProbe(string) instanceProbe { return nil }
