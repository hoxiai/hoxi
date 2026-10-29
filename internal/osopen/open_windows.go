//go:build windows

// Package osopen opens local files with the operating system's file handler.
package osopen

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"

	"golang.org/x/sys/windows"
)

var shellExecute = windows.ShellExecute

// Open opens path, or reveals it in Explorer when reveal is true.
func Open(ctx context.Context, path string, reveal bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if reveal {
		err := exec.CommandContext(ctx, "explorer.exe", "/select,"+filepath.Clean(path)).Run()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil
		}
		return err
	}
	file, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	// Passing the path directly avoids cmd.exe interpreting &, %, and other
	// characters in a file name as commands or environment expansion.
	return shellExecute(0, nil, file, nil, nil, windows.SW_SHOWNORMAL)
}
