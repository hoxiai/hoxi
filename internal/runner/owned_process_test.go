package runner

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestOwnedProcessForgetCancelsPendingScans(t *testing.T) {
	l := newOwnedProcessLedger()
	watch := l.startWatch("ses_a", "call_1")
	other := l.startWatch("ses_b", "call_1")
	if watch == nil || other == nil {
		t.Fatal("expected watches for both sessions")
	}
	if l.startWatch("ses_a", "call_1") != nil {
		t.Fatal("duplicate scans must be suppressed")
	}
	l.recordWatched("ses_a", "call_1", watch, 4242, "server a")
	l.recordWatched("ses_b", "call_1", other, 5353, "server b")
	done := make(chan bool, 1)
	go func() { done <- watch.wait(time.Hour) }()
	l.forget("ses_a")
	select {
	case elapsed := <-done:
		if elapsed {
			t.Fatal("deleted session's scan delay must be canceled")
		}
	case <-time.After(time.Second):
		t.Fatal("deleted session's scanner is still waiting")
	}
	if watch.wait(0) {
		t.Fatal("deleted session's zero-delay scan must not proceed")
	}
	// Repeated deletion must not close an already closed watch.
	l.forget("ses_a")
	select {
	case <-other.done:
		t.Fatal("another session's scanner must survive")
	default:
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.groups["ses_a"]) != 0 || len(l.groups["ses_b"]) != 1 {
		t.Fatalf("unexpected process ownership after deletion: %v", l.groups)
	}
}

func TestOwnedProcessForgottenScanCannotRestoreOwnership(t *testing.T) {
	l := newOwnedProcessLedger()
	stale := l.startWatch("ses_a", "call_1")
	l.forget("ses_a")
	// The scanner had already read a snapshot when deletion canceled it.
	l.recordWatched("ses_a", "call_1", stale, 4242, "stale server")
	if len(l.groups) != 0 {
		t.Fatal("an in-flight scan restored deleted ownership")
	}

	current := l.startWatch("ses_a", "call_1")
	if current == nil {
		t.Fatal("expected a new watch after cleanup")
	}
	l.recordWatched("ses_a", "call_1", stale, 4242, "stale server")
	l.endWatch("ses_a", "call_1", stale)
	if l.startWatch("ses_a", "call_1") != nil {
		t.Fatal("stale scanner cleanup removed the replacement watch")
	}
	l.recordWatched("ses_a", "call_1", current, 5353, "current server")
	l.endWatch("ses_a", "call_1", current)
	l.recordWatched("ses_a", "call_1", current, 6464, "finished scan")
	groups := l.groups["ses_a"]
	if len(groups) != 1 || groups[0].pgid != 5353 {
		t.Fatalf("only the current scanner may record ownership: %v", groups)
	}
}

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
	root := svc.ownedProcs.startWatch("ses_cld_root", "root_call")
	child := svc.ownedProcs.startWatch("ses_cld_child", "child_call")
	other := svc.ownedProcs.startWatch("ses_other", "other_call")
	if err := svc.DeleteConversation(context.Background(), "ses_cld_root"); err != nil {
		t.Fatal(err)
	}
	for _, watch := range []*ownedProcessWatch{root, child} {
		select {
		case <-watch.done:
		default:
			t.Fatal("conversation deletion left a process scanner running")
		}
	}
	select {
	case <-other.done:
		t.Fatal("conversation deletion canceled an unrelated scanner")
	default:
	}
}
