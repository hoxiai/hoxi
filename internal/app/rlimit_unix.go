//go:build !windows

package app

import "syscall"

// targetOpenFileLimit raises the low default macOS LaunchServices soft limit
// (often 256 or 2560) for Hoxi and its child runtimes while keeping a per-process
// ceiling well below the system-wide kern.maxfiles table.
const targetOpenFileLimit uint64 = 65536

func raiseOpenFileLimit() {
	var rlim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rlim); err != nil {
		return
	}
	target := targetOpenFileLimit
	if rlim.Max > 0 && rlim.Max < target {
		target = rlim.Max
	}
	if rlim.Cur >= target {
		return
	}
	rlim.Cur = target
	_ = syscall.Setrlimit(syscall.RLIMIT_NOFILE, &rlim)
}
