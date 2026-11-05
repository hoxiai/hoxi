//go:build !windows

package subproc

func decodePlatformLegacyBytes(_ []byte) (string, bool) {
	return "", false
}
