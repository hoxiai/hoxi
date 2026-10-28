//go:build !windows

package statusbar

func startOSKeepAwake() (func(), bool) {
	return nil, false
}
