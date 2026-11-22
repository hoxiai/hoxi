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

func TestOpenWindowsRevealQuotesPathsWithSpacesAndHandlesDriveRoot(t *testing.T) {
	orig := shellExecute
	t.Cleanup(func() { shellExecute = orig })

	var gotFile, gotArgs string
	shellExecute = func(_ windows.Handle, verb, file, args, cwd *uint16, _ int32) error {
		if verb != nil || cwd != nil {
			t.Fatal("unexpected verb or cwd")
		}
		gotFile = windows.UTF16PtrToString(file)
		gotArgs = windows.UTF16PtrToString(args)
		return nil
	}

	if err := Open(context.Background(), `C:\Users\My User\My Repo\a & b.txt`, true); err != nil {
		t.Fatalf("Open reveal with spaces failed: %v", err)
	}
	if gotFile != "explorer.exe" || gotArgs != `/select,"C:\Users\My User\My Repo\a & b.txt"` {
		t.Fatalf("unexpected ShellExecute call: file=%q args=%q", gotFile, gotArgs)
	}

	if err := Open(context.Background(), `C:\`, true); err != nil {
		t.Fatalf("Open reveal drive root failed: %v", err)
	}
	if gotFile != "explorer.exe" || gotArgs != `C:\` {
		t.Fatalf("unexpected drive root call: file=%q args=%q", gotFile, gotArgs)
	}

	for _, bad := range []string{`C:\Users\bad"quote.txt`, "C:\\Users\\bad\x00nul.txt"} {
		if err := Open(context.Background(), bad, true); err == nil {
			t.Fatalf("expected invalid path %q to be rejected", bad)
		}
	}
}
