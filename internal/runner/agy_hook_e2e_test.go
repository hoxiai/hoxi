//go:build agy_e2e

// End-to-end check of the Antigravity approval gate against the real agy CLI
// and a built `hoxi` binary. It spends a few model requests, so it only runs
// on demand:
//
//	go build -o /tmp/hoxi-e2e ./cmd/hoxi
//	HOXI_E2E_BIN=/tmp/hoxi-e2e go test -tags agy_e2e -run AgyHookE2E -v ./internal/runner/
//
// The hook is installed in a temp workspace's .agents/hooks.json, so the
// user's global ~/.gemini/config/hooks.json is never touched.
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runAgyThroughGate(t *testing.T, reply string) (string, string) {
	t.Helper()
	bin, agy := os.Getenv("HOXI_E2E_BIN"), ResolveAgyBinary()
	if bin == "" || agy == "" {
		t.Skip("set HOXI_E2E_BIN and install agy to run this test")
	}
	model := firstNonEmptyStr(os.Getenv("HOXI_E2E_MODEL"), "gemini-3.8-flash-low")
	ws := t.TempDir()
	probe := filepath.Join(ws, "probe.txt")
	if err := os.WriteFile(probe, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	hooks := map[string]json.RawMessage{agyHookName: desiredAgyHook(bin)}
	if err := writeAgyHooksDoc(filepath.Join(ws, ".agents", "hooks.json"), hooks, 0o644); err != nil {
		t.Fatal(err)
	}

	svc := NewService(ws, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	if err := svc.store.Put(SessionRecord{ID: "ses_agy_e2e", Runtime: "antigravity", ProjectDir: ws, WorktreePath: ws}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc.SetHookBaseURL(srv.URL)
	env, revoke := svc.issueHookEnv("ses_agy_e2e", "msg_e2e")
	defer revoke()

	events, unsub := svc.Subscribe("ses_agy_e2e")
	defer unsub()
	asked := make(chan string, 4)
	go func() {
		for evt := range events {
			if evt.Type != "permission.asked" {
				continue
			}
			id, _ := evt.Properties["id"].(string)
			asked <- id
			body, _ := json.Marshal(map[string]string{"reply": reply, "message": "e2e says no"})
			resp, err := http.Post(srv.URL+"/api/hoxi/runner/permission/"+id+"/reply", "application/json", bytes.NewReader(body))
			if err == nil {
				resp.Body.Close()
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, agy, BuildAgyArgs("build", model, "", "")...)
	cmd.Dir = ws
	cmd.Env = append(os.Environ(), env...)
	input, _ := EncodeStreamInputTurn("antigravity", "Run exactly this shell command with the run_command tool, do not change it and do not use any other tool, then report the result in one line: rm -f probe.txt")
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("agy failed: %v\nstderr: %s", err, stderr.String())
	}
	select {
	case <-asked:
	default:
		t.Fatalf("gate never asked for approval; output:\n%s\nstderr: %s", out, stderr.String())
	}
	_, statErr := os.Stat(probe)
	return string(out), map[bool]string{true: "deleted", false: "present"}[os.IsNotExist(statErr)]
}

func TestAgyHookE2EOnceRunsCommand(t *testing.T) {
	t.Parallel()
	out, probe := runAgyThroughGate(t, "once")
	if probe != "deleted" || strings.Contains(out, "denied_actions") {
		t.Fatalf("approved rm did not run (probe %s):\n%s", probe, out)
	}
}

func TestAgyHookE2ERejectBlocksCommand(t *testing.T) {
	t.Parallel()
	out, probe := runAgyThroughGate(t, "reject")
	if probe != "present" || !strings.Contains(out, "denied by pre-tool hook") || !strings.Contains(out, "e2e says no") {
		t.Fatalf("rejected rm was not blocked with the user's reason (probe %s):\n%s", probe, out)
	}
}
