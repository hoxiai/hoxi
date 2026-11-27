package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyUsesOriginalCasesAfterTestRename(t *testing.T) {
	for _, tc := range []struct {
		task               Task
		testFile, evidence string
	}{
		{taskJSSemverSort(), "semver.test.mjs", "sorts numeric minor/patch"},
		{taskGoCrossFileLimit(), "service_test.go", "service_test.go"},
		{taskPyRateLimiter(), "test_limiter.py", "test_exact_window_boundary_expires"},
	} {
		t.Run(tc.task.ID, func(t *testing.T) {
			if _, err := exec.LookPath(tc.task.RequireTool); err != nil {
				t.Skip(err)
			}
			dir := t.TempDir()
			if err := Materialize(tc.task, dir); err != nil {
				t.Fatal(err)
			}
			testFile := filepath.Join(dir, tc.testFile)
			if err := os.Rename(testFile, testFile+".saved"); err != nil {
				t.Fatal(err)
			}
			passed, output, err := VerifyTask(t.Context(), tc.task, dir)
			if err != nil || passed || !strings.Contains(output, tc.evidence) {
				t.Fatalf("broken solution must fail its original cases: passed=%v err=%v\n%s", passed, err, output)
			}
			if _, err := os.Lstat(testFile); !os.IsNotExist(err) {
				t.Fatalf("grading changed the submitted directory: %v", err)
			}
		})
	}
}

func TestVerifyGradesSubmittedCodeAndPreservesTestEdits(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip(err)
	}
	task := taskPyRateLimiter()
	dir := t.TempDir()
	if err := Materialize(task, dir); err != nil {
		t.Fatal(err)
	}
	testFile := filepath.Join(dir, "test_limiter.py")
	const edited = "# WIP: move these assertions to a new suite\n"
	if err := os.WriteFile(testFile, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	check := func(want bool) {
		t.Helper()
		passed, out, err := VerifyTask(t.Context(), task, dir)
		if err != nil || passed != want {
			t.Fatalf("passed=%v, want %v; err=%v\n%s", passed, want, err, out)
		}
		if data, err := os.ReadFile(testFile); err != nil || string(data) != edited {
			t.Fatalf("submitted test changed: %q, %v", data, err)
		}
	}
	check(false)
	// The fix uses a new module outside the original fixture manifest.
	code := strings.ReplaceAll(task.Files["limiter.py"], "ts >= cutoff", "ts > cutoff")
	code = strings.Replace(code, "        return removed", "        self._events = {key: bucket for key, bucket in self._events.items() if bucket}\n        return removed", 1)
	if err := os.WriteFile(filepath.Join(dir, "fixed_limiter.py"), []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "limiter.py"), []byte("from fixed_limiter import SlidingWindowLimiter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	check(true)
}

func TestInlineVerificationUsesDisposableDirectory(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	task := taskKeyMigration()
	if err := Materialize(task, dir); err != nil {
		t.Fatal(err)
	}
	const script = `import {writeFileSync} from "node:fs";
writeFileSync("generated.txt", "result", {flag: "wx"});
console.log(process.cwd());
process.exitCode = Number(process.argv[1]);`
	for _, code := range []string{"0", "1"} {
		task.VerifyCmd = []string{"node", "--input-type=module", "-e", script, code}
		passed, gradingDir, err := VerifyTask(t.Context(), task, dir)
		if err != nil || passed != (code == "0") || !filepath.IsAbs(gradingDir) {
			t.Fatalf("passed=%v, err=%v, output=%q", passed, err, gradingDir)
		}
		if _, err := os.Stat(gradingDir); !os.IsNotExist(err) {
			t.Fatalf("grading directory survived verification: %s (%v)", gradingDir, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "generated.txt")); !os.IsNotExist(err) {
			t.Fatalf("verification wrote into the submitted workspace: %v", err)
		}
	}
}

func TestGradingLinksStayInsideSubmittedTree(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "helper"), []byte("submitted"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("helper", filepath.Join(dir, "local")); err != nil {
		t.Skipf("filesystem does not support symlinks: %v", err)
	}
	grading := t.TempDir()
	if err := prepareGradingDirectory(t.Context(), Task{}, dir, grading); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(grading, "local")); err != nil || string(data) != "submitted" {
		t.Fatalf("submitted helper link failed in grading copy: %q, %v", data, err)
	}
	external := t.TempDir()
	target, err := filepath.Rel(dir, external)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "outside")); err != nil {
		t.Fatal(err)
	}
	if err := prepareGradingDirectory(t.Context(), Task{}, dir, t.TempDir()); err == nil {
		t.Fatal("out-of-tree symlink accepted in grading copy")
	}
}
