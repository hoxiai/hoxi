package runner

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestOfflineReplayAgentAcceptanceFixtures(t *testing.T) {
	fixtures := []string{
		"steer_then_stop.json",
		"approval_cross_device_race.json",
		"subagent_write_interrupted.json",
		"sse_disconnect_catchup.json",
	}

	for _, name := range fixtures {
		name := name
		t.Run(strings.TrimSuffix(name, ".json"), func(t *testing.T) {
			fxPath := filepath.Join("testdata", "replay", name)
			fx, err := LoadReplayFixture(fxPath)
			if err != nil {
				t.Fatalf("LoadReplayFixture(%s): %v", name, err)
			}

			repoDir := initTestGitRepo(t)
			storeDir := filepath.Join(t.TempDir(), "sessions")
			report, err := RunReplayFixture(context.Background(), fx, repoDir, storeDir)
			if err != nil {
				t.Fatalf("RunReplayFixture(%s) failed: %v", name, err)
			}
			if report.FinalSeq == 0 {
				t.Fatalf("expected non-zero SSE sequence in replay report for %s", name)
			}
		})
	}
}

func TestScrubReplayTextRedactsPathsAndDigests(t *testing.T) {
	raw := `{"cwd":"/Users/alice/workspace-demo","hex":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}`
	scrubbed := ScrubReplayText(raw, map[string]string{
		"/Users/alice/workspace-demo": "<WORKSPACE>",
	})
	for _, item := range []string{
		"/Users/alice/workspace-demo",
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	} {
		if strings.Contains(scrubbed, item) {
			t.Fatalf("ScrubReplayText leaked %q in %s", item, scrubbed)
		}
	}
	if !strings.Contains(scrubbed, "<WORKSPACE>") || !strings.Contains(scrubbed, "<HEX64_REDACTED>") {
		t.Fatalf("unexpected scrubbed output: %s", scrubbed)
	}
}
