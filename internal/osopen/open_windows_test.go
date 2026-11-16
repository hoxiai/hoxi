//go:build windows

package osopen

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/sys/windows"
)

func TestOpenPassesWindowsPathDirectlyToShellExecute(t *testing.T) {
	previous := shellExecute
	t.Cleanup(func() { shellExecute = previous })
	var got string
	shellExecute = func(_ windows.Handle, verb, file, args, cwd *uint16, _ int32) error {
		if verb != nil || args != nil || cwd != nil {
			t.Fatal("unexpected command arguments")
		}
		got = windows.UTF16PtrToString(file)
		return nil
	}
	path := `C:\Users\name\a & %PATH% (中文).txt`
	if err := Open(context.Background(), path, false); err != nil {
		t.Fatal(err)
	}
	if got != path {
		t.Fatalf("opened %q, want %q", got, path)
	}
	if err := Open(context.Background(), "bad\x00path", false); err == nil {
		t.Fatal("expected invalid path to be rejected")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Open(ctx, path, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context: %v", err)
	}
}
