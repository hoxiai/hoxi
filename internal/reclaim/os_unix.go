//go:build darwin || linux

package reclaim

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
	"time"
)

const supported = true

func signalProcess(pid int, sig syscall.Signal) error { return syscall.Kill(pid, sig) }

// lockFile takes an exclusive flock on path, polling so a stuck holder can
// delay startup by at most timeout. The lock file is never removed.
func lockFile(path string, timeout time.Duration) (func(), error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	fd := int(f.Fd())
	deadline := time.Now().Add(timeout)
	for {
		err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(fd, syscall.LOCK_UN)
				_ = f.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || time.Now().After(deadline) {
			_ = f.Close()
			return nil, err
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// ownedByUser reports whether info belongs to the current user.
func ownedByUser(info fs.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}
