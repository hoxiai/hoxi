package worktree

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBindingForSessionAcrossRootsAndRecovery(t *testing.T) {
	root := initTempGitRepo(t)
	other := initTempGitRepo(t)
	mgr := NewManager(root)
	first, err := mgr.Create("ses_scope_a")
	if err != nil {
		t.Fatal(err)
	}
	otherMgr := mgr.ForDirectory(other)
	second, err := otherMgr.Create("ses_scope_b")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id, want string
	}{
		{"ses_scope_a", first.Directory},
		{"ses_scope_b", second.Directory},
	} {
		binding, known, err := mgr.BindingForSession(tc.id)
		if err != nil || !known || binding.Directory != tc.want {
			t.Fatalf("binding %s = %+v, known=%v, err=%v", tc.id, binding, known, err)
		}
	}
	if _, known, err := mgr.BindingForSession("ses_unknown"); err != nil || known {
		t.Fatalf("unknown binding = known=%v err=%v", known, err)
	}

	fresh := NewManager(root)
	binding, known, err := fresh.BindingForSession("ses_scope_a")
	if err != nil || !known || binding.Directory != first.Directory {
		t.Fatalf("recovered binding = %+v, known=%v, err=%v", binding, known, err)
	}
	if err := os.RemoveAll(first.Directory); err != nil {
		t.Fatal(err)
	}
	if _, known, err := mgr.BindingForSession("ses_scope_a"); err == nil || !known {
		t.Fatalf("deleted sandbox should fail closed: known=%v err=%v", known, err)
	}
}

func TestBindingCanonicalizesSymlinkAndRejectsSiblingAmbiguity(t *testing.T) {
	root := t.TempDir()
	sandbox := filepath.Join(root, ".hoxi", "worktrees", "ses_link")
	if err := os.MkdirAll(sandbox, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "sandbox-link")
	if err := os.Symlink(sandbox, link); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(root)
	binding, known, err := mgr.BindingForSession("ses_link")
	canonicalSandbox, canonicalErr := filepath.EvalSymlinks(sandbox)
	if canonicalErr != nil {
		t.Fatal(canonicalErr)
	}
	if err != nil || !known || binding.Directory != canonicalSandbox {
		t.Fatalf("symlink binding = %+v, known=%v, err=%v", binding, known, err)
	}

	otherRoot := t.TempDir()
	otherSandbox := filepath.Join(otherRoot, ".hoxi", "worktrees", "ses_link")
	if err := os.MkdirAll(otherSandbox, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = mgr.ForDirectory(otherRoot)
	if _, known, err := mgr.BindingForSession("ses_link"); err == nil || !known {
		t.Fatalf("sibling ambiguity should fail closed: known=%v err=%v", known, err)
	}
}
