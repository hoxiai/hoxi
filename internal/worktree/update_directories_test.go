package worktree

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestUpdateDirectoriesSkipsInvalidParents(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(root, "missing")
	dangling := filepath.Join(root, "dangling")
	if err := os.Symlink(missing, dangling); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"explicit", "manager", "session"} {
		t.Run(source, func(t *testing.T) {
			mgr := NewManager(root)
			invalid := []string{"", file, missing, dangling}
			var roots []string
			switch source {
			case "explicit":
				roots = invalid
			case "manager":
				for _, dir := range invalid {
					mgr.roots.managers[dir] = &worktreeManager{rootDir: dir}
				}
			case "session":
				for _, dir := range invalid {
					mgr.storeMeta(worktreeMeta{SessionID: dir, Directory: dir})
				}
			}
			got, err := mgr.UpdateDirectories(roots)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, []string{root}) {
				t.Fatalf("directories = %v, want [%s]", got, root)
			}
		})
	}
}

func TestUpdateDirectoriesSkipsMissingChildren(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	sandbox := filepath.Join(root, ".hoxi", "worktrees", "sandbox")
	if err := os.MkdirAll(sandbox, 0o700); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(sandbox)
	// A dangling symlink deterministically exercises the same not-found Stat
	// result as a child removed after ReadDir.
	if err := os.Symlink(filepath.Join(root, "removed"), filepath.Join(parent, "dangling")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(parent, "linked")
	if err := os.Symlink(t.TempDir(), linked); err != nil {
		t.Fatal(err)
	}
	bound := t.TempDir()
	mgr := NewManager(root)
	mgr.storeMeta(worktreeMeta{SessionID: "bound", Directory: bound})
	got, err := mgr.UpdateDirectories([]string{root, root})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{root, sandbox, linked, bound}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("directories = %v, want %v", got, want)
	}
}

func TestUpdateDirectoriesFailsClosedOnFilesystemErrors(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"parent-stat", "read-dir", "child-stat"} {
		t.Run(stage, func(t *testing.T) {
			root := t.TempDir()
			worktrees := filepath.Join(root, ".hoxi", "worktrees")
			if err := os.MkdirAll(worktrees, 0o700); err != nil {
				t.Fatal(err)
			}
			loop := filepath.Join(root, "loop")
			if err := os.Symlink(loop, loop); err != nil {
				t.Fatal(err)
			}
			mgr := NewManager(root)
			var roots []string
			switch stage {
			case "parent-stat":
				roots = []string{loop}
			case "read-dir":
				if err := os.Remove(worktrees); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(worktrees, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			case "child-stat":
				if err := os.Symlink(loop, filepath.Join(worktrees, "broken")); err != nil {
					t.Fatal(err)
				}
			}
			got, err := mgr.UpdateDirectories(roots)
			if err == nil || got != nil {
				t.Fatalf("directories = %v, error = %v; want nil and filesystem error", got, err)
			}
		})
	}
}

func TestUpdateDirectoriesFailsClosedOnPermissionErrors(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"parent-stat", "read-dir", "child-stat"} {
		t.Run(stage, func(t *testing.T) {
			root := t.TempDir()
			worktrees := filepath.Join(root, ".hoxi", "worktrees")
			if err := os.MkdirAll(worktrees, 0o700); err != nil {
				t.Fatal(err)
			}
			blocked := filepath.Join(root, "blocked")
			if err := os.Mkdir(blocked, 0o700); err != nil {
				t.Fatal(err)
			}
			mgr := NewManager(root)
			var roots []string
			switch stage {
			case "parent-stat":
				roots = []string{filepath.Join(blocked, "child")}
			case "read-dir":
				blocked = worktrees
			case "child-stat":
				if err := os.Symlink(filepath.Join(blocked, "child"), filepath.Join(worktrees, "linked")); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Chmod(blocked, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.Chmod(blocked, 0o700); err != nil {
					t.Error(err)
				}
			})
			if _, err := os.ReadDir(blocked); !os.IsPermission(err) {
				t.Skip("environment does not enforce directory permissions")
			}
			got, err := mgr.UpdateDirectories(roots)
			if !os.IsPermission(err) || got != nil {
				t.Fatalf("directories = %v, error = %v; want nil and permission error", got, err)
			}
		})
	}
}
