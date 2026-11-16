//go:build claude_e2e

// End-to-end check of the Claude Code approval gate: a real runner turn
// (startTurnProcess) against the installed `claude` CLI. It spends a few
// model requests, so it only runs on demand:
//
//	go test -tags claude_e2e -run ClaudeHookE2E -v ./internal/runner/
//
// The hook lives in the per-process --settings argument, so ~/.claude is
// never touched.
package runner

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type claudeE2EResult struct {
	asked    []string
	commands []string
	callIDs  []string
	events   string
	ws       string
}

func runClaudeTurnThroughGate(t *testing.T, prompt, reply string) claudeE2EResult {
	t.Helper()
	bin := ResolveClaudeBinary()
	if bin == "" {
		t.Skip("install claude to run this test")
	}
	model := firstNonEmptyStr(os.Getenv("HOXI_E2E_MODEL"), "claude-haiku-4-5-20251001")
	ws := t.TempDir()
	files := map[string]string{
		"sum.py":    "open('ran.txt', 'w').write(str(sum(range(1, 11))))\nprint('sum done')\n",
		"probe.txt": "hi",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(ws, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	svc := NewService(ws, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	rec := SessionRecord{ID: "ses_cc_e2e", Runtime: string(RuntimeClaudeCode), ProjectDir: ws, WorktreePath: ws}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc.SetHookBaseURL(srv.URL)

	events, unsub := svc.Subscribe(rec.ID)
	defer unsub()
	var (
		mu  sync.Mutex
		res = claudeE2EResult{ws: ws}
		log bytes.Buffer
	)
	go func() {
		for evt := range events {
			raw, _ := json.Marshal(evt)
			mu.Lock()
			log.Write(append(raw, '\n'))
			mu.Unlock()
			if evt.Type != "permission.asked" {
				continue
			}
			id, _ := evt.Properties["id"].(string)
			meta, _ := evt.Properties["metadata"].(map[string]interface{})
			cmd, _ := meta["command"].(string)
			tool, _ := evt.Properties["tool"].(map[string]interface{})
			callID, _ := tool["callID"].(string)
			mu.Lock()
			res.asked = append(res.asked, id)
			res.commands = append(res.commands, cmd)
			res.callIDs = append(res.callIDs, callID)
			mu.Unlock()
			body, _ := json.Marshal(map[string]string{"reply": reply, "message": "e2e says no"})
			if resp, err := http.Post(srv.URL+"/api/hoxi/runner/permission/"+id+"/reply", "application/json", bytes.NewReader(body)); err == nil {
				resp.Body.Close()
			}
		}
	}()

	userInfo := MessageInfo{ID: "msg_u_e2e", SessionID: rec.ID, Role: "user"}
	if err := svc.startTurnProcess(rec, bin, "build", model, "", prompt, userInfo, nil, "msg_a_e2e"); err != nil {
		t.Fatalf("start turn: %v", err)
	}
	svc.mu.Lock()
	ap := svc.procs[rec.ID]
	svc.mu.Unlock()
	select {
	case <-ap.done:
	case <-time.After(4 * time.Minute):
		ap.Abort()
		t.Fatal("claude turn timed out")
	}
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	res.events = log.String()
	if len(svc.listPendingApprovals("")) != 0 {
		t.Fatal("pending approvals left after the turn")
	}
	return res
}

func TestClaudeHookE2EApprovedPythonRuns(t *testing.T) {
	t.Parallel()
	res := runClaudeTurnThroughGate(t, "Run exactly this shell command with the Bash tool, do not change it and do not use any other tool, then report its output in one line: python3 sum.py", "once")
	if len(res.asked) == 0 || !strings.Contains(strings.Join(res.commands, "\n"), "python3 sum.py") {
		t.Fatalf("gate never asked for python3 sum.py (asked %v); events:\n%s", res.commands, res.events)
	}
	if res.callIDs[0] == "" || !strings.Contains(res.events, `"callID":"`+res.callIDs[0]+`","state"`) {
		t.Fatalf("approval %q is not linked to a tool part; events:\n%s", res.callIDs[0], res.events)
	}
	got, err := os.ReadFile(filepath.Join(res.ws, "ran.txt"))
	if err != nil || string(got) != "55" {
		t.Fatalf("approved command did not run (ran.txt %q, %v); events:\n%s", got, err, res.events)
	}
}

func TestClaudeHookE2ERejectedDestructiveIsBlocked(t *testing.T) {
	t.Parallel()
	res := runClaudeTurnThroughGate(t, "Run exactly this shell command with the Bash tool, do not change it, do not retry and do not use any other tool, then report the result in one line: rm -f probe.txt", "reject")
	if len(res.asked) == 0 {
		t.Fatalf("destructive command did not ask; events:\n%s", res.events)
	}
	if _, err := os.Stat(filepath.Join(res.ws, "probe.txt")); err != nil {
		t.Fatalf("rejected rm still ran: %v", err)
	}
	if !strings.Contains(res.events, "e2e says no") {
		t.Fatalf("user's reject reason did not reach the model; events:\n%s", res.events)
	}
}
