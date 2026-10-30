// Package buildinfo exposes the version embedded by the desktop build.
package buildinfo

// Version is overridden by build-desktop.sh with the validated release version.
var Version = "0.6.1"

// UserAgent returns the standard User-Agent header for Hoxi desktop requests.
func UserAgent() string {
	return "HoxiDesktop/" + Version
}
