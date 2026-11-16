// Package subproc creates child processes for Hoxi.
//
// Hoxi Desktop on Windows is a GUI program with no console. When it starts a
// console program (git, sqlite3, powershell, opencode, …), Windows gives that
// child a new console, and when Windows Terminal is the default terminal that
// console shows up as a window even with HideWindow set. Every child process
// is therefore created through Command / CommandContext, which ask Windows for
// a console without a window. Elsewhere they behave exactly like os/exec.
package subproc

import (
	"context"
	"os/exec"
)

// Command is exec.Command with HideWindow applied.
func Command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	HideWindow(cmd)
	return cmd
}

// CommandContext is exec.CommandContext with HideWindow applied.
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	HideWindow(cmd)
	return cmd
}

// HideWindow keeps cmd from opening a console window. It merges into an
// existing SysProcAttr, so callers that need other process flags set them by
// modifying cmd.SysProcAttr after this call rather than replacing it. It is a
// no-op outside Windows.
func HideWindow(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	hideWindow(cmd)
}
