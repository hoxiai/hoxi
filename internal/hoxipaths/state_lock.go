package hoxipaths

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

// WithStateFileLock serializes read/modify/write transactions across host
// processes. Keep the lock file in place so waiters always lock the same inode.
func WithStateFileLock(path string, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	lock := flock.New(path+".lock", flock.SetPermissions(0o600))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	locked, err := lock.TryLockContext(ctx, 10*time.Millisecond)
	if err != nil {
		return err
	}
	if !locked {
		return context.DeadlineExceeded
	}
	defer lock.Close()
	return fn()
}
