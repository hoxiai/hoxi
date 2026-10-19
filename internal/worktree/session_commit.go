package worktree

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// SessionDiff is the original OpenCode session diff, not a workspace diff.
// Pointers distinguish a missing snapshot from a valid empty file.
type SessionDiff struct {
	File   string  `json:"file"`
	Before *string `json:"before"`
	After  *string `json:"after"`
	Status string  `json:"status"`
}

type sessionEntry struct {
	mode string
	oid  string
	data []byte
}

type sessionCommitSnapshot struct {
	After  string `json:"after"`
	Status string `json:"status"`
	Commit string `json:"commit"`
	Blob   string `json:"blob"`
}

type sessionCommitCheckpoint struct {
	SessionID string                           `json:"sessionID"`
	Files     map[string]sessionCommitSnapshot `json:"files"`
}

func writeSessionCheckpoint(path string, checkpoint sessionCommitCheckpoint) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".checkpoint-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

// sessionGit preserves blob bytes and keeps stderr out of merge-file output.
func sessionGit(dir, index, gitDir string, input []byte, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	for _, value := range os.Environ() {
		key := strings.SplitN(value, "=", 2)[0]
		switch key {
		case "GIT_INDEX_FILE", "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES":
			continue
		}
		cmd.Env = append(cmd.Env, value)
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0", "GIT_LITERAL_PATHSPECS=1")
	if index != "" {
		cmd.Env = append(cmd.Env, "GIT_INDEX_FILE="+index)
	}
	if gitDir != "" {
		cmd.Env = append(cmd.Env, "GIT_DIR="+gitDir, "GIT_WORK_TREE="+dir)
	}
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %s: %w", args[0], strings.TrimSpace(stderr.String()), err)
	}
	return stdout.Bytes(), nil
}

func sessionPath(root, name string) (string, error) {
	if name == "" || strings.ContainsAny(name, "\x00\\\r\n") {
		return "", errors.New("invalid session file path")
	}
	// Reject traversal even when cleaning it would happen to remain in the repo.
	for _, part := range strings.Split(filepath.ToSlash(name), "/") {
		if part == ".." || strings.EqualFold(part, ".git") {
			return "", fmt.Errorf("unsafe session path %q", name)
		}
	}
	abs := name
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, abs)
	} else {
		// macOS exposes /var through /private/var. Resolve only a repository
		// root alias, never a symlink inside the requested file's path.
		for parent := filepath.Dir(abs); ; parent = filepath.Dir(parent) {
			canonical, err := filepath.EvalSymlinks(parent)
			info, statErr := os.Lstat(parent)
			if err == nil && statErr == nil && info.Mode()&os.ModeSymlink == 0 && canonical == root {
				suffix, _ := filepath.Rel(parent, abs)
				abs = filepath.Join(root, suffix)
				break
			}
			if filepath.Dir(parent) == parent {
				break
			}
		}
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("session path outside repository: %q", name)
	}
	path := root
	parts := strings.Split(rel, string(filepath.Separator))
	for i, part := range parts {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 || (i < len(parts)-1 && !info.IsDir()) || (i == len(parts)-1 && !info.Mode().IsRegular()) {
			return "", fmt.Errorf("unsupported session path %q", name)
		}
	}
	return filepath.ToSlash(rel), nil
}

func sessionHeadEntry(root, head, path string) (sessionEntry, error) {
	out, err := sessionGit(root, "", "", nil, "ls-tree", "-z", head, "--", path)
	if err != nil || len(out) == 0 {
		return sessionEntry{}, err
	}
	meta, name, ok := strings.Cut(strings.TrimSuffix(string(out), "\x00"), "\t")
	fields := strings.Fields(meta)
	if !ok || name != path || len(fields) != 3 || fields[1] != "blob" || (fields[0] != "100644" && fields[0] != "100755") {
		return sessionEntry{}, fmt.Errorf("unsupported HEAD entry: %q", path)
	}
	data, err := sessionGit(root, "", "", nil, "cat-file", "blob", fields[2])
	return sessionEntry{mode: fields[0], oid: fields[2], data: data}, err
}

func sessionMerge(root, temp string, current, base, incoming []byte) ([]byte, error) {
	paths := make([]string, 3)
	for i, data := range [][]byte{current, base, incoming} {
		paths[i] = filepath.Join(temp, fmt.Sprintf("merge-%d", i))
		if err := os.WriteFile(paths[i], data, 0600); err != nil {
			return nil, err
		}
	}
	return sessionGit(root, "", "", nil, "merge-file", "-p", "--", paths[0], paths[1], paths[2])
}

func sessionUpdateIndex(root, index, path string, entry sessionEntry) error {
	if entry.mode == "" {
		_, err := sessionGit(root, index, "", nil, "update-index", "--force-remove", "--", path)
		return err
	}
	_, err := sessionGit(root, index, "", nil, "update-index", "--add", "--cacheinfo", entry.mode, entry.oid, path)
	return err
}

// CommitSessionChanges commits only the supplied before/after changes. It never
// stages working files, and publishes the commit only after hooks and validation.
func (m *worktreeManager) CommitSessionChanges(sessionID, message string, push bool, diffs []SessionDiff) (CommitAndPushResult, error) {
	m.ops.Lock()
	defer m.ops.Unlock()
	fail := func(err error) (CommitAndPushResult, error) { return CommitAndPushResult{}, err }
	if strings.TrimSpace(sessionID) == "" {
		return fail(errors.New("sessionID is required for session commits"))
	}
	root, err := sessionGit(m.resolveWorkDir(sessionID), "", "", nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return fail(err)
	}
	dir := strings.TrimSpace(string(root))
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return fail(err)
	}
	git := func(args ...string) (string, error) {
		out, err := sessionGit(dir, "", "", nil, args...)
		return strings.TrimSpace(string(out)), err
	}
	// Capture expanded config in the real worktree so we can verify the private
	// detached gitdir preserves the exact effective configuration before commit.
	config, err := sessionGit(dir, "", "", nil, "config", "--includes", "--null", "--list")
	if err != nil {
		return fail(err)
	}
	gitDir, err := git("rev-parse", "--absolute-git-dir")
	if err != nil {
		return fail(err)
	}
	index := filepath.Join(gitDir, "index")
	lock, err := os.OpenFile(index+".lock", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fail(fmt.Errorf("lock real index: %w", err))
	}
	lockHeld := true
	defer func() {
		if lockHeld {
			os.Remove(index + ".lock")
		}
	}()
	defer lock.Close()
	checkpointPath := filepath.Join(gitDir, "hoxi-session-commits", fmt.Sprintf("%x.json", sha256.Sum256([]byte(sessionID))))
	checkpoint := sessionCommitCheckpoint{SessionID: sessionID, Files: make(map[string]sessionCommitSnapshot)}
	if data, readErr := os.ReadFile(checkpointPath); readErr == nil {
		if err := json.Unmarshal(data, &checkpoint); err != nil || checkpoint.SessionID != sessionID || checkpoint.Files == nil {
			return fail(errors.New("invalid session commit checkpoint"))
		}
	} else if !os.IsNotExist(readErr) {
		return fail(fmt.Errorf("read session commit checkpoint: %w", readErr))
	}
	head, err := git("rev-parse", "--verify", "HEAD")
	if err != nil {
		return fail(err) // Unborn repositories deliberately fail closed.
	}
	ref, err := git("symbolic-ref", "-q", "HEAD")
	if err != nil {
		return fail(errors.New("session commits require an attached branch"))
	}
	for _, state := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply", "sequencer"} {
		if _, err := os.Lstat(filepath.Join(gitDir, state)); !os.IsNotExist(err) {
			return fail(errors.New("session commit refused during an in-progress git operation"))
		}
	}
	unmerged, err := git("ls-files", "--unmerged")
	if err != nil || unmerged != "" {
		return fail(errors.New("session commit requires a resolved index"))
	}
	temp, err := os.MkdirTemp(gitDir, "hoxi-session-")
	if err != nil {
		return fail(err)
	}
	defer os.RemoveAll(temp)
	candidateIndex := filepath.Join(temp, "candidate-index")
	if _, err := sessionGit(dir, candidateIndex, "", nil, "read-tree", head); err != nil {
		return fail(err)
	}
	// Copy the original index; only session entries will be replaced after commit.
	original, err := os.ReadFile(index)
	if err != nil {
		return fail(err)
	}
	realIndex := filepath.Join(temp, "real-index")
	if err := os.WriteFile(realIndex, original, 0600); err != nil {
		return fail(err)
	}
	entries := make(map[string]sessionEntry)
	snapshots := make(map[string]sessionCommitSnapshot)
	for _, diff := range diffs {
		path, err := sessionPath(dir, diff.File)
		if err != nil {
			return fail(err)
		}
		if _, exists := entries[path]; exists {
			return fail(fmt.Errorf("duplicate session path: %s", path))
		}
		if diff.Before == nil || diff.After == nil {
			return fail(fmt.Errorf("missing before/after snapshot: %s", path))
		}
		before, after := []byte(*diff.Before), []byte(*diff.After)
		if diff.Status != "added" && diff.Status != "deleted" && diff.Status != "modified" {
			return fail(fmt.Errorf("unsupported session status: %q", diff.Status))
		}
		if (diff.Status == "added" && len(before) != 0) || (diff.Status == "deleted" && len(after) != 0) {
			return fail(fmt.Errorf("inconsistent add/delete snapshots: %s", path))
		}
		base, err := sessionHeadEntry(dir, head, path)
		if err != nil {
			return fail(err)
		}
		staged, err := sessionGit(dir, "", "", nil, "ls-files", "--stage", "-z", "--", path)
		expected := ""
		if base.mode != "" {
			expected = base.mode + " " + base.oid + " 0\t" + path + "\x00"
		}
		if err != nil || string(staged) != expected {
			return fail(fmt.Errorf("session file has staged changes: %s", path))
		}
		flags, err := sessionGit(dir, "", "", nil, "ls-files", "-v", "--", path)
		if err != nil || (len(flags) != 0 && flags[0] != 'H') {
			return fail(fmt.Errorf("unsupported index flags: %s", path))
		}
		current, err := os.ReadFile(filepath.Join(dir, path))
		exists := err == nil
		if err != nil && !os.IsNotExist(err) {
			return fail(err)
		}
		for _, data := range [][]byte{before, after, base.data, current} {
			if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
				return fail(fmt.Errorf("binary session file: %s", path))
			}
		}
		candidate := base
		if base.mode != "" && (diff.Status == "modified" || (diff.Status == "added" && !bytes.Equal(base.data, after))) {
			previous, ok := checkpoint.Files[path]
			if !ok && diff.Status == "added" {
				return fail(fmt.Errorf("added path already exists in HEAD without a trusted session checkpoint: %s", path))
			}
			if ok {
				if previous.Status != diff.Status || previous.Commit == "" || previous.Blob == "" {
					return fail(fmt.Errorf("invalid session checkpoint for %s", path))
				}
				if _, err := git("merge-base", "--is-ancestor", previous.Commit, head); err != nil {
					return fail(fmt.Errorf("session checkpoint is not an ancestor of HEAD: %s", path))
				}
				committed, err := sessionHeadEntry(dir, previous.Commit, path)
				if err != nil || committed.mode == "" || committed.oid != previous.Blob {
					return fail(fmt.Errorf("session checkpoint does not match committed file: %s", path))
				}
				before = []byte(previous.After)
			}
		}
		switch diff.Status {
		case "added":
			if base.mode == "" {
				candidate = sessionEntry{mode: "100644", data: after}
			} else {
				if !bytes.Equal(base.data, after) {
					candidate.data, err = sessionMerge(dir, temp, base.data, before, after)
					if err != nil {
						return fail(fmt.Errorf("cumulative addition conflicts with HEAD (%s): %w", path, err))
					}
				}
			}
		case "deleted":
			if base.mode != "" && !bytes.Equal(base.data, before) {
				return fail(fmt.Errorf("delete conflicts with HEAD: %s", path))
			}
			candidate = sessionEntry{}
		case "modified":
			if base.mode == "" {
				return fail(fmt.Errorf("modified path missing from HEAD: %s", path))
			}
			candidate.data, err = sessionMerge(dir, temp, base.data, before, after)
			if err != nil {
				return fail(fmt.Errorf("session change conflicts with HEAD (%s): %w", path, err))
			}
		}
		if candidate.mode == "" {
			if exists {
				return fail(fmt.Errorf("deleted session path still exists: %s", path))
			}
		} else {
			if !exists {
				return fail(fmt.Errorf("session path missing from workspace: %s", path))
			}
			info, err := os.Lstat(filepath.Join(dir, path))
			if err != nil || !info.Mode().IsRegular() {
				return fail(fmt.Errorf("unsupported workspace entry: %s", path))
			}
			mode := "100644"
			if info.Mode()&0111 != 0 {
				mode = "100755"
			}
			if mode != candidate.mode {
				return fail(fmt.Errorf("unsupported mode change: %s", path))
			}
			merged, err := sessionMerge(dir, temp, current, base.data, candidate.data)
			if err != nil || !bytes.Equal(merged, current) {
				return fail(fmt.Errorf("session change is stale or absent from workspace: %s", path))
			}
			oid, err := sessionGit(dir, "", "", candidate.data, "hash-object", "-w", "--stdin")
			if err != nil {
				return fail(err)
			}
			candidate.oid = strings.TrimSpace(string(oid))
		}
		entries[path] = candidate
		snapshots[path] = sessionCommitSnapshot{After: string(after), Status: diff.Status, Blob: candidate.oid}
		if err := sessionUpdateIndex(dir, candidateIndex, path, candidate); err != nil {
			return fail(err)
		}
		if err := sessionUpdateIndex(dir, realIndex, path, candidate); err != nil {
			return fail(err)
		}
	}
	treeBytes, err := sessionGit(dir, candidateIndex, "", nil, "write-tree")
	if err != nil {
		return fail(err)
	}
	tree := strings.TrimSpace(string(treeBytes))
	oldTree, err := git("rev-parse", head+"^{tree}")
	if err != nil {
		return fail(err)
	}
	newHead := head
	if tree == oldTree && !push {
		return fail(errors.New("nothing to commit, session changes already committed or empty"))
	}
	if tree != oldTree {
		if strings.TrimSpace(message) == "" {
			return fail(errors.New("commit message is required"))
		}
		// A private detached HEAD lets normal hooks run without publishing their
		// result until its tree and parent have been checked against our candidate.
		common, err := git("rev-parse", "--git-common-dir")
		if err != nil {
			return fail(err)
		}
		if !filepath.IsAbs(common) {
			common = filepath.Join(dir, common)
		}
		for name, value := range map[string]string{"HEAD": head + "\n", "commondir": common + "\n"} {
			if err := os.WriteFile(filepath.Join(temp, name), []byte(value), 0600); err != nil {
				return fail(err)
			}
		}
		if wtConfig, readErr := os.ReadFile(filepath.Join(gitDir, "config.worktree")); readErr == nil {
			if err := os.WriteFile(filepath.Join(temp, "config.worktree"), wtConfig, 0600); err != nil {
				return fail(err)
			}
		} else if !os.IsNotExist(readErr) {
			return fail(readErr)
		}
		tempConfig, err := sessionGit(dir, candidateIndex, temp, nil, "config", "--includes", "--null", "--list")
		if err != nil {
			return fail(err)
		}
		if !bytes.Equal(config, tempConfig) {
			return fail(errors.New("session commit cannot safely preserve worktreeConfig or conditional include configuration"))
		}
		if _, err := sessionGit(dir, candidateIndex, temp, nil, "-c", "core.bare=false", "commit", "-m", appendHoxiCoAuthor(message)); err != nil {
			return fail(err)
		}
		out, err := sessionGit(dir, candidateIndex, temp, nil, "log", "-1", "--format=%H %T %P")
		if err != nil {
			return fail(err)
		}
		parts := strings.Fields(string(out))
		if len(parts) != 3 || parts[1] != tree || parts[2] != head {
			return fail(errors.New("commit hook changed the candidate tree or parent; commit not published"))
		}
		body, err := sessionGit(dir, candidateIndex, temp, nil, "log", "-1", "--format=%B")
		if err != nil || !strings.Contains(string(body), HoxiCoAuthorTrailer) {
			return fail(errors.New("commit hook removed Hoxi attribution; commit not published"))
		}
		newHead = parts[0]
		updated, err := os.ReadFile(realIndex)
		if err != nil {
			return fail(err)
		}
		if _, err := lock.Write(updated); err != nil {
			return fail(err)
		}
		if err := lock.Sync(); err != nil {
			return fail(err)
		}
	}
	actualHead, err := git("rev-parse", "HEAD")
	actualRef, refErr := git("symbolic-ref", "-q", "HEAD")
	if err != nil || refErr != nil || actualHead != head || actualRef != ref {
		return fail(errors.New("external HEAD change; session commit not published"))
	}
	if newHead != head {
		if _, err := git("update-ref", "-m", "hoxi: session commit", ref, newHead, head); err != nil {
			return fail(err)
		}
		for path, snapshot := range snapshots {
			snapshot.Commit = newHead
			checkpoint.Files[path] = snapshot
		}
		checkpointErr := writeSessionCheckpoint(checkpointPath, checkpoint)
		if err := lock.Close(); err != nil {
			return fail(fmt.Errorf("commit %s created but index close failed: %w", newHead, err))
		}
		if err := os.Rename(index+".lock", index); err != nil {
			return fail(fmt.Errorf("commit %s created but index sync failed: %w", newHead, err))
		}
		lockHeld = false
		globalRepoDiffCache.invalidate(dir)
		if checkpointErr != nil {
			return CommitAndPushResult{Commit: newHead, Branch: strings.TrimPrefix(ref, "refs/heads/")}, fmt.Errorf("commit %s created but session checkpoint save failed: %w", newHead, checkpointErr)
		}
	}
	result := CommitAndPushResult{OK: true, Commit: newHead, Branch: strings.TrimPrefix(ref, "refs/heads/"), Message: strings.TrimSpace(message)}
	if push {
		result.Remote, err = runGitPush(dir, result.Branch)
		result.Pushed, result.OK = err == nil, err == nil
	}
	result.CompareURL = buildRemoteCompareURL(resolveGitRemoteURLFast(dir, result.Remote), "", result.Branch)
	return result, err
}
