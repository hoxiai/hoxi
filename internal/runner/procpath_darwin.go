//go:build darwin && cgo

package runner

/*
#include <libproc.h>
#include <unistd.h>
*/
import "C"
import "unsafe"

// liveProcessExecutablePath asks XNU for the current vnode path of the running
// executable. Unlike os.Executable on Darwin (which caches the argv/apple path
// captured at execve time), proc_pidpath follows same-volume Finder moves of a
// running .app bundle, and returns empty when the original vnode was unlinked.
func liveProcessExecutablePath() string {
	var buf [C.PROC_PIDPATHINFO_MAXSIZE]C.char
	if C.proc_pidpath(C.getpid(), unsafe.Pointer(&buf[0]), C.uint32_t(len(buf))) <= 0 {
		return ""
	}
	return C.GoString(&buf[0])
}
