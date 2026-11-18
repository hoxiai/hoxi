package runner

import (
	"context"
	"path/filepath"
	"testing"
)

func TestDeleteConversationCancelsRootAndChildProcessScans(t *testing.T) {
	repo := t.TempDir()
	svc := NewService(repo, nil, nil, filepath.Join(t.TempDir(), "sessions"))
	for _, rec := range []SessionRecord{
		{ID: "ses_cld_root", Runtime: string(RuntimeClaudeCode), ProjectDir: repo},
		{ID: "ses_cld_child", Runtime: string(RuntimeClaudeCode), ProjectDir: repo, ParentID: "ses_cld_root", Subagent: true},
	} {
		if err := svc.store.Put(rec); err != nil {
			t.Fatal(err)
		}
	}
	root := svc.ownedProcs.StartWatch("ses_cld_root", "root_call")
	child := svc.ownedProcs.StartWatch("ses_cld_child", "child_call")
	other := svc.ownedProcs.StartWatch("ses_other", "other_call")
	if err := svc.DeleteConversation(context.Background(), "ses_cld_root"); err != nil {
		t.Fatal(err)
	}
	for _, watch := range []*ownedProcessWatch{root, child} {
		select {
		case <-watch.Done():
		default:
			t.Fatal("conversation deletion left a process scanner running")
		}
	}
	select {
	case <-other.Done():
		t.Fatal("conversation deletion canceled an unrelated scanner")
	default:
	}
}

func TestTrustedWindowsKillPathAcceptsOnlyProgramFilesGit(t *testing.T) {
	getenv := func(k string) string {
		switch k {
		case "ProgramFiles":
			return `C:\Program Files`
		case "ProgramFiles(x86)":
			return `C:\Program Files (x86)`
		case "LOCALAPPDATA":
			return `C:\Users\dev\AppData\Local`
		}
		return ""
	}
	if !isTrustedWindowsKillPath(`C:\Program Files\Git\usr\bin\kill.exe`, getenv) {
		t.Fatal("expected ProgramFiles Git usr/bin/kill.exe to be trusted")
	}
	for _, bad := range []string{
		`C:\Users\dev\AppData\Local\Programs\Git\usr\bin\kill.exe`,
		`C:\Users\dev\scoop\apps\git\current\usr\bin\kill.exe`,
		`C:\Program Files\Git\usr\bin\pkill.exe`,
		`C:\Temp\kill.exe`,
	} {
		if isTrustedWindowsKillPath(bad, getenv) {
			t.Fatalf("expected %q to be rejected by isTrustedWindowsKillPath", bad)
		}
	}
}
