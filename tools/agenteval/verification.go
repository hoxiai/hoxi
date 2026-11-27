package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/hoxiai/hoxi/internal/subproc"
)

const maxVerifyOutputBytes = 64 * 1024

// VerifyTask runs the task's compiler or tests against an independent grading copy.
func VerifyTask(ctx context.Context, task Task, dir string) (bool, string, error) {
	if len(task.VerifyCmd) == 0 {
		return false, "", fmt.Errorf("task %s has no verify command", task.ID)
	}
	gradingDir, err := os.MkdirTemp("", "hoxi-agenteval-grade-*")
	if err != nil {
		return false, "", err
	}
	defer os.RemoveAll(gradingDir)
	if err := prepareGradingDirectory(ctx, task, dir, gradingDir); err != nil {
		return false, "", fmt.Errorf("prepare verification for %s: %w", task.ID, err)
	}
	cmd := subproc.CommandContext(ctx, task.VerifyCmd[0], task.VerifyCmd[1:]...)
	cmd.Dir = gradingDir
	cmd.Env = sanitizedEvalEnv()
	var buf bytes.Buffer
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		_ = pr.Close()
		return false, "", err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(&buf, io.LimitReader(pr, maxVerifyOutputBytes))
		_, _ = io.Copy(io.Discard, pr)
	}()
	waitErr := cmd.Wait()
	_ = pw.Close()
	<-done
	_ = pr.Close()
	out := strings.TrimSpace(buf.String())
	if waitErr != nil {
		return false, out, nil
	}
	return true, out, nil
}

// Each grading run gets the submitted implementation and a fresh copy of the
// author's assertions. Files created by an earlier test run stay in that run.
func prepareGradingDirectory(ctx context.Context, task Task, workDir, gradingDir string) error {
	if err := writeFixtureFiles(gradingDir, task.AcceptanceTests); err != nil {
		return err
	}
	source, err := os.OpenRoot(workDir)
	if err != nil {
		return err
	}
	defer source.Close()
	return fs.WalkDir(source.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		local := filepath.FromSlash(name)
		dest := filepath.Join(gradingDir, local)
		// The authored files are already present. Checking the destination also
		// catches case-only renames on case-insensitive grading volumes.
		existing, err := os.Lstat(dest)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if (existing != nil && existing.Mode().IsRegular()) || entry.Name() == ".git" || entry.Name() == "__pycache__" {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		// Go compiles every *_test.go in the package, even when selecting
		// tests by name. Node and Python select the authored suite explicitly.
		if task.Language == "go" && len(task.AcceptanceTests) > 0 && strings.HasSuffix(name, "_test.go") {
			return nil
		}
		if entry.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := source.Readlink(local)
			if err != nil {
				return err
			}
			// Only recreate links whose targets belong to the submitted tree.
			if _, err := source.Stat(local); err != nil {
				return err
			}
			return os.Symlink(target, dest)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("cannot grade special file %s", name)
		}
		in, err := source.Open(local)
		if err != nil {
			return err
		}
		defer in.Close()
		info, err := in.Stat()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600|info.Mode().Perm()&0o111)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}
