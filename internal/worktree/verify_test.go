package worktree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorktreeSetupFailureCaptureAndRetry(t *testing.T) {
	t.Parallel()
	repoDir := initTempGitRepo(t)
	if err := os.MkdirAll(filepath.Join(repoDir, ".hoxi"), 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(repoDir, ".hoxi", "setup.sh")
	// First setup script fails intentionally
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\necho \"missing dependency xyz\" >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(repoDir)
	meta, err := mgr.Create("ses_setup_fail")
	if err != nil {
		t.Fatalf("Create should succeed while recording setup failure: %v", err)
	}
	defer func() { _ = mgr.Remove("ses_setup_fail", true) }()

	if meta.SetupStatus != "failed" || !strings.Contains(meta.SetupError, "missing dependency xyz") {
		t.Fatalf("expected SetupStatus=failed with output in SetupError, got %+v", meta)
	}
	if meta.AllocatedPort < 3100 {
		t.Fatalf("expected AllocatedPort >= 3100, got %d", meta.AllocatedPort)
	}

	// Fix setup script and retry
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\necho \"setup ok on port $HOXI_PORT\"\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	vState, err := mgr.RetrySetup("ses_setup_fail")
	if err != nil {
		t.Fatalf("RetrySetup failed: %v", err)
	}
	if vState.SetupStatus != "succeeded" || vState.SetupError != "" || !strings.Contains(vState.SetupOutput, "setup ok on port") {
		t.Fatalf("expected succeeded setup after retry, got %+v", vState)
	}

	st, err := mgr.Status("ses_setup_fail")
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if st.SetupStatus != "succeeded" {
		t.Fatalf("expected Status.SetupStatus=succeeded, got %+v", st)
	}
}

func TestWorktreeVerifyRunTestAndPreviewURLDetection(t *testing.T) {
	t.Parallel()
	repoDir := initTempGitRepo(t)
	mgr := NewManager(repoDir)
	meta, err := mgr.Create("ses_verify_run")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	defer func() { _ = mgr.Remove("ses_verify_run", true) }()

	cfg, err := mgr.SaveVerifyConfig(WorktreeVerifyConfig{
		RunCommand:  "printf 'Listening on http://127.0.0.1:%s/app\\n' \"$HOXI_PORT\"",
		TestCommand: "printf 'PASS all tests in %s\\n' \"$HOXI_SESSION_ID\"",
	})
	if err != nil {
		t.Fatalf("SaveVerifyConfig failed: %v", err)
	}
	if cfg.RunCommand == "" || cfg.TestCommand == "" {
		t.Fatalf("unexpected saved config: %+v", cfg)
	}

	testState, err := mgr.StartVerify("ses_verify_run", "test", "")
	if err != nil {
		t.Fatalf("StartVerify(test) failed: %v", err)
	}
	if testState.RunStatus != "succeeded" || !strings.Contains(testState.Output, "PASS all tests in ses_verify_run") {
		t.Fatalf("expected test command to succeed, got %+v", testState)
	}

	runState, err := mgr.StartVerify("ses_verify_run", "run", "")
	if err != nil {
		t.Fatalf("StartVerify(run) failed: %v", err)
	}
	if !strings.HasPrefix(runState.PreviewURL, "http://127.0.0.1:") || !strings.HasSuffix(runState.PreviewURL, "/app") {
		t.Fatalf("expected detected PreviewURL with port and path, got %+v (dir=%s)", runState, meta.Directory)
	}
}
