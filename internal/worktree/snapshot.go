package worktree

import (
	"fmt"
	"strings"
)

func snapshotWorktree(dir, sessionID string) error {
	statusOut, err := runGit(dir, "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("read worktree status: %w", err)
	}
	if strings.TrimSpace(statusOut) == "" {
		return nil
	}
	if _, err = runGit(dir, "add", "-A"); err != nil {
		return fmt.Errorf("stage worktree snapshot: %w", err)
	}
	if _, err = runGit(dir, "commit", "-m", appendHoxiCoAuthor("chore(hoxi): snapshot before apply ("+sessionID+")")); err != nil {
		return fmt.Errorf("commit worktree snapshot: %w", err)
	}
	return nil
}
