package runner

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/hoxiai/hoxi/internal/sandbox"
)

// confineRuntimeCommand wraps a native runtime subprocess in the OS sandbox.
// The runtime itself still needs outbound network access for model calls, so
// only the filesystem boundary is enforced here.
func (s *Service) confineRuntimeCommand(rec SessionRecord, bin string, args []string) (string, []string, error) {
	if !rec.CommandSandbox {
		return bin, args, nil
	}
	if !sandbox.Available() {
		return "", nil, errors.New(sandbox.UnavailableReason())
	}
	profile, err := sandbox.DefaultProfile(rec.WorktreePath, sandbox.NetworkAllow, writableGitDir(rec.ProjectDir))
	if err != nil {
		return "", nil, err
	}
	return sandbox.WrapExec(profile, bin, args)
}

// writableGitDir keeps git metadata writable for worktree sessions, whose
// actual git directory lives under the main project instead of the worktree.
func writableGitDir(projectDir string) string {
	dir := filepath.Join(projectDir, ".git")
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		return dir
	}
	return ""
}
