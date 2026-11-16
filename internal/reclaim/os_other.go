//go:build !darwin && !linux && !windows

package reclaim

import (
	"errors"
	"io/fs"
	"syscall"
	"time"
)

// Process identity cannot be verified here, so Open refuses and callers fall
// back to unrecorded temporary directories.
const supported = false

var errUnsupported = errors.New("reclaim: unsupported platform")

func inspectProcess(int) (procInfo, error) { return procInfo{}, errUnsupported }

func signalProcess(int, syscall.Signal) error { return errUnsupported }

func lockFile(string, time.Duration) (func(), error) { return nil, errUnsupported }

func ownedByUser(fs.FileInfo) bool { return false }
