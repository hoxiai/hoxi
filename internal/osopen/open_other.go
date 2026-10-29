//go:build !windows

// Package osopen opens local files with the operating system's file handler.
package osopen

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
)

// Open opens path, or reveals it in the file manager when reveal is true.
func Open(ctx context.Context, path string, reveal bool) error {
	if runtime.GOOS == "darwin" {
		if reveal {
			return exec.CommandContext(ctx, "open", "-R", path).Run()
		}
		return exec.CommandContext(ctx, "open", path).Run()
	}
	if reveal {
		path = filepath.Dir(path)
	}
	return exec.CommandContext(ctx, "xdg-open", path).Run()
}
