//go:build windows

// Package osopen opens local files with the operating system's file handler.
package osopen

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

var shellExecute = windows.ShellExecute

// Open opens path, or reveals it in Explorer when reveal is true.
func Open(ctx context.Context, path string, reveal bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if reveal {
		cleaned := filepath.Clean(path)
		if strings.ContainsAny(cleaned, "\x00\"") {
			return errors.New("invalid path")
		}
		exe, err := windows.UTF16PtrFromString("explorer.exe")
		if err != nil {
			return err
		}
		// Explorer requires `/select,` outside the quotes around paths with spaces;
		// Go's exec.Command would wrap the whole arg in quotes ("/select,C:\..."),
		// causing Explorer to open the default folder instead of selecting the file.
		// Bare drive roots (e.g. `C:\`) cannot be selected in a parent folder, so
		// pass them directly without `/select,` or quotes.
		argStr := `/select,"` + strings.TrimRight(cleaned, `\`) + `"`
		if len(cleaned) == 3 && ((cleaned[0] >= 'a' && cleaned[0] <= 'z') || (cleaned[0] >= 'A' && cleaned[0] <= 'Z')) && cleaned[1] == ':' && cleaned[2] == '\\' {
			argStr = cleaned
		}
		args, err := windows.UTF16PtrFromString(argStr)
		if err != nil {
			return err
		}
		return shellExecute(0, nil, exe, args, nil, windows.SW_SHOWNORMAL)
	}
	file, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	// Passing the path directly avoids cmd.exe interpreting &, %, and other
	// characters in a file name as commands or environment expansion.
	return shellExecute(0, nil, file, nil, nil, windows.SW_SHOWNORMAL)
}
