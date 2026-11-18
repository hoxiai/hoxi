package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/sandbox"
	"github.com/hoxiai/hoxi/internal/statusbar"
	"github.com/hoxiai/hoxi/internal/worktree"
)

type multiRootStubResolver struct {
	id    string
	roots []string
}

func (r multiRootStubResolver) ProjectIDForDirectory(dir string) (string, bool) {
	for _, root := range r.roots {
		if normalizeComparePath(root) == normalizeComparePath(dir) {
			return r.id, true
		}
	}
	return "", false
}

func (r multiRootStubResolver) EnsureProjectForDirectory(dir string) (string, error) {
	return r.id, nil
}

func (r multiRootStubResolver) ProjectRoots(projectID string) []string {
	if projectID == r.id {
		return r.roots
	}
	return nil
}

func TestNativeAutoModeCheckpointRecoverableAndRevertRestoresInLocalMode(t *testing.T) {
	repo := initTestGitRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(repo, "src", "legacy.go")
	handlerPath := filepath.Join(repo, "src", "handler.go")
	externalPath := filepath.Join(repo, "src", "external.go")
	untouchedPath := filepath.Join(repo, "src", "untouched.go")
	for p, body := range map[string]string{
		legacyPath:    "package src\nconst Legacy = 1\n",
		handlerPath:   "package src\nconst Handler = 1\n",
		externalPath:  "package src\nconst External = 1\n",
		untouchedPath: "package src\nconst Untouched = 1\n",
	} {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := worktree.RunGit(repo, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.RunGit(repo, "commit", "-m", "seed src"); err != nil {
		t.Fatal(err)
	}
	// Uncommitted pre-turn user edit on handler.go that must also remain recoverable.
	const dirtyHandler = "package src\nconst Handler = 42 // user pre-turn edit\n"
	if err := os.WriteFile(handlerPath, []byte(dirtyHandler), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".env"), []byte("SECRET=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "hoxi.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := NewService(repo, nil, statusbar.NewTracker(), filepath.Join(t.TempDir(), "sessions.json"))
	setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: ApprovalModeAuto})
	svc.gate.waitLimit = 50 * time.Millisecond

	const sid = "ses_nat_ckpt"
	const userMsgID = "msg_u_1"
	const asstMsgID = "msg_a_1"
	rec := SessionRecord{
		ID:           sid,
		Runtime:      string(RuntimeNative),
		SandboxMode:  "local",
		ProjectDir:   repo,
		WorktreePath: repo,
		Turns: []MessageTurn{
			{
				Info:  MessageInfo{ID: userMsgID, SessionID: sid, Role: "user"},
				Parts: []MessagePart{{ID: "prt_u_1", SessionID: sid, MessageID: userMsgID, Type: "text", Text: "clean up"}},
			},
		},
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	svc.recordTurnSnapshot(sid, userMsgID)
	rec, _ = svc.store.Get(sid)
	cred := hookCredential{runtime: string(RuntimeNative), sessionID: sid, messageID: asstMsgID}

	// 1. Concrete tracked/snapshotted file delete & git checkout discard auto-approve via checkpoint.
	cmd := "rm src/legacy.go && git checkout -- src/handler.go"
	httpReq := httptest.NewRequest(http.MethodPost, "/", nil)
	ok, reason := svc.gateCommand(httpReq, cred, rec, approvalAsk{
		command: cmd, cwd: repo, callID: "call_ckpt_1", source: string(RuntimeNative),
	})
	if !ok {
		t.Fatalf("expected checkpoint-recoverable command approved, got deny: %s", reason)
	}
	entries := svc.audit.list("", 5)
	if len(entries) == 0 || entries[0].Source != approvalSourceCheckpoint {
		t.Fatalf("expected audit source=%q, got %+v", approvalSourceCheckpoint, entries)
	}

	// Simulate execution and emit completed bash tool part through noteToolEvents + store.
	if err := os.Remove(legacyPath); err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.RunGit(repo, "checkout", "--", "src/handler.go"); err != nil {
		t.Fatal(err)
	}
	// Simulate an unrelated user edit in the shared local checkout that revert must NOT clobber.
	const userUntouchedEdit = "package src\nconst Untouched = 999 // user concurrent edit\n"
	if err := os.WriteFile(untouchedPath, []byte(userUntouchedEdit), 0o644); err != nil {
		t.Fatal(err)
	}

	part := MessagePart{
		ID: "prt_bash_1", SessionID: sid, MessageID: asstMsgID,
		Type: "tool", Tool: "bash", CallID: "call_ckpt_1",
		State: &ToolState{Status: "completed", Input: map[string]interface{}{"command": cmd}},
	}
	evts := []SSEEvent{{Type: "message.part.updated", Properties: map[string]interface{}{"part": part}}}
	svc.noteToolEvents(sid, evts)
	persistedPart := evts[0].Properties["part"].(MessagePart)
	if got := stringList(persistedPart.State.Metadata["checkpointFiles"]); len(got) != 2 {
		t.Fatalf("expected checkpointFiles attached on completed bash part, got %#v", persistedPart.State.Metadata)
	}
	_, _, err := svc.store.Update(sid, func(r *SessionRecord) {
		r.Turns = append(r.Turns, MessageTurn{
			Info:  MessageInfo{ID: asstMsgID, SessionID: sid, Role: "assistant", ParentID: userMsgID},
			Parts: []MessagePart{persistedPart},
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	// Revert the turn in local mode: legacy.go and dirty handler.go must come back, untouched.go stays as user edited!
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	revBody, _ := json.Marshal(map[string]string{"messageID": userMsgID})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions/"+sid+"/revert?directory="+repo, bytes.NewReader(revBody)))
	if w.Code != http.StatusOK {
		t.Fatalf("revert failed: %d %s", w.Code, w.Body.String())
	}
	if data, err := os.ReadFile(legacyPath); err != nil || !strings.Contains(string(data), "Legacy = 1") {
		t.Fatalf("expected legacy.go restored by local revert, got %q err=%v", string(data), err)
	}
	if data, err := os.ReadFile(handlerPath); err != nil || string(data) != dirtyHandler {
		t.Fatalf("expected pre-turn dirty handler.go restored, got %q err=%v", string(data), err)
	}
	if data, err := os.ReadFile(untouchedPath); err != nil || string(data) != userUntouchedEdit {
		t.Fatalf("local revert clobbered unrelated file! got %q err=%v", string(data), err)
	}

	// 2. Adversarial & boundary cases MUST still ask (fail closed).
	svc.recordTurnSnapshot(sid, userMsgID)
	rec, _ = svc.store.Get(sid)
	// Modify external.go AFTER snapshot outside fileCoord so curOID != snapOID.
	if err := os.WriteFile(externalPath, []byte("package src\nconst External = 777 // mid-turn user edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(t.TempDir(), "outside.txt")
	_ = os.WriteFile(outsideFile, []byte("out"), 0o644)
	if runtime.GOOS != "windows" {
		_ = os.Symlink(outsideFile, filepath.Join(repo, "src", "link_out.go"))
	}

	mustAsk := []string{
		"rm src/external.go",
		"rm .env",
		"rm .git/config",
		"rm hoxi.json",
		"rm -rf .",
		"rm -rf *",
		"rm src/*.go",
		"rm 'src/'legacy.go",
		"rm $(echo src/legacy.go)",
		"rm src/legacy.go && sudo true",
		"echo x | rm src/legacy.go",
		"git reset --hard HEAD",
		"git checkout .",
		"git checkout -f main",
		"git clean -fd",
	}
	if runtime.GOOS != "windows" {
		mustAsk = append(mustAsk, "rm src/link_out.go")
	}
	for _, bad := range mustAsk {
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
		r, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/", nil)
		approved, _ := svc.gateCommand(r, cred, rec, approvalAsk{
			command: bad, cwd: repo, callID: "call_bad", source: string(RuntimeNative),
		})
		cancel()
		if approved {
			t.Fatalf("adversarial command %q must NOT be auto-approved", bad)
		}
	}

	// Smart mode and bash=ask override must still ask even for valid checkpoint targets.
	for _, pol := range []ApprovalPolicy{
		{Mode: ApprovalModeSmart},
		{Mode: ApprovalModeManual},
		{Mode: ApprovalModeAuto, Bash: "ask"},
		{Mode: ApprovalModeAuto, Bash: "smart"},
	} {
		setTestApprovalPolicy(t, svc, pol)
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
		r, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/", nil)
		if approved, _ := svc.gateCommand(r, cred, rec, approvalAsk{
			command: "rm src/legacy.go", cwd: repo, callID: "call_pol", source: string(RuntimeNative),
		}); approved {
			cancel()
			t.Fatalf("policy %+v must still ask for rm src/legacy.go", pol)
		}
		cancel()
	}
}

func TestNativeSessionCreatedFileAndDisposableBuildArtifactCleanup(t *testing.T) {
	repo := initTestGitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("web/dist/\nnotes/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Tracked file inside `build/` so `rm -rf build` is NOT disposable!
	if err := os.MkdirAll(filepath.Join(repo, "build"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "build", "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.RunGit(repo, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.RunGit(repo, "commit", "-m", "add gitignore and tracked build"); err != nil {
		t.Fatal(err)
	}

	svc := NewService(repo, nil, statusbar.NewTracker(), filepath.Join(t.TempDir(), "sessions.json"))
	setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: ApprovalModeAuto})
	svc.gate.waitLimit = 40 * time.Millisecond

	const sid = "ses_nat_created"
	rec := SessionRecord{
		ID:           sid,
		Runtime:      string(RuntimeNative),
		SandboxMode:  "local",
		ProjectDir:   repo,
		WorktreePath: repo,
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	cred := hookCredential{runtime: string(RuntimeNative), sessionID: sid, messageID: "msg_a1"}

	// 1. Coordinate creation of a brand-new scratch file (no Git snapshot needed).
	scratchFile := filepath.Join(repo, "repro_scratch_test.go")
	coord, err := svc.nativeCoordinateFileEdit(rec, nil)(context.Background(), []string{scratchFile}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scratchFile, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	coord.Release()
	svc.nativeRecordFileEdit(rec)([]string{scratchFile})

	if ok, reason := svc.gateCommand(httptest.NewRequest(http.MethodPost, "/", nil), cred, rec, approvalAsk{
		command: "rm -f repro_scratch_test.go", cwd: repo, callID: "c1", source: string(RuntimeNative),
	}); !ok {
		t.Fatalf("expected session-created file rm approved, got: %s", reason)
	}

	// 2. Gitignored web/dist (both non-existent and populated with build bundle) is approved.
	if ok, reason := svc.gateCommand(httptest.NewRequest(http.MethodPost, "/", nil), cred, rec, approvalAsk{
		command: "rm -rf web/dist", cwd: repo, callID: "c2", source: string(RuntimeNative),
	}); !ok {
		t.Fatalf("expected non-existent gitignored web/dist cleanup approved, got: %s", reason)
	}
	distDir := filepath.Join(repo, "web", "dist")
	if err := os.MkdirAll(distDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(distDir, "app.js"), []byte("console.log(1)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, reason := svc.gateCommand(httptest.NewRequest(http.MethodPost, "/", nil), cred, rec, approvalAsk{
		command: "rm -rf web/dist", cwd: repo, callID: "c3", source: string(RuntimeNative),
	}); !ok {
		t.Fatalf("expected populated gitignored web/dist cleanup approved, got: %s", reason)
	}

	// 3. Rejections: tracked build/ dir, secret inside dist/, and non-build gitignored user dir notes/.
	if err := os.MkdirAll(filepath.Join(repo, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "notes", "todo.md"), []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, mustAsk := range []string{"rm -rf build", "rm -rf notes"} {
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
		r, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/", nil)
		if approved, _ := svc.gateCommand(r, cred, rec, approvalAsk{
			command: mustAsk, cwd: repo, callID: "c_ask", source: string(RuntimeNative),
		}); approved {
			cancel()
			t.Fatalf("%q must NOT auto-approve", mustAsk)
		}
		cancel()
	}

	// Secret inside web/dist blocks disposable cleanup.
	if err := os.WriteFile(filepath.Join(distDir, ".env.local"), []byte("KEY=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
	r, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/", nil)
	if approved, _ := svc.gateCommand(r, cred, rec, approvalAsk{
		command: "rm -rf web/dist", cwd: repo, callID: "c_secret", source: string(RuntimeNative),
	}); approved {
		cancel()
		t.Fatal("web/dist containing .env.local must NOT auto-approve")
	}
	cancel()

	// Session cleanup clears created-file & checkpoint state.
	svc.gate.stashNativeCheckpointPaths(sid, "call_x", []string{"a.go"})
	svc.gate.forgetSession(sid)
	if svc.gate.isSessionCreatedFile(sid, scratchFile) || svc.gate.consumeNativeCheckpointPaths(sid, "call_x") != nil {
		t.Fatal("forgetSession did not clear sessionCreated / checkpointPaths")
	}
}

func TestNativeProtectedFileSessionGrantAndRefusals(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	skillFile := filepath.Join(dir, ".hoxi", "skills", "demo", "SKILL.md")
	otherSkill := filepath.Join(dir, ".hoxi", "skills", "other", "SKILL.md")
	gitConfig := filepath.Join(dir, ".git", "config")
	hoxiCfg := filepath.Join(dir, "hoxi.json")
	for _, p := range []string{skillFile, otherSkill, gitConfig, hoxiCfg} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("init\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	svc := NewService(dir, nil, statusbar.NewTracker(), filepath.Join(t.TempDir(), "sessions.json"))
	setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: ApprovalModeAuto})
	const sid = "ses_nat_fgrant"
	rec := SessionRecord{ID: sid, Runtime: string(RuntimeNative), SandboxMode: "local", ProjectDir: dir, WorktreePath: dir}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	postReply := func(permID string, payload map[string]interface{}) int {
		raw, _ := json.Marshal(payload)
		recW := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/hoxi/permission/"+permID+"/reply?directory="+dir, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "127.0.0.1:54321"
		mux.ServeHTTP(recW, req)
		return recW.Code
	}

	events, unsub := svc.Subscribe(sid)
	defer unsub()
	hook := svc.nativePermissionHook(rec, "msg_a1")

	// First edit to .hoxi/skills/demo/SKILL.md asks AND offers file:<abs> grant class.
	errCh := make(chan error, 1)
	rawSkillArgs, _ := json.Marshal(map[string]string{"filePath": ".hoxi/skills/demo/SKILL.md"})
	go func() {
		errCh <- hook(context.Background(), NativePermissionRequest{
			SessionID: sid, CallID: "call_1", ToolName: "edit", Input: rawSkillArgs,
		})
	}()
	asked := waitForEvent(t, events, "permission.asked")
	meta, _ := asked.Properties["metadata"].(map[string]interface{})
	classes := stringList(meta[grantClassesMetaKey])
	wantClass := fileGrantPrefix + skillFile
	if len(classes) != 1 || classes[0] != wantClass {
		t.Fatalf("expected grant class [%s], got %#v", wantClass, classes)
	}
	permID, _ := asked.Properties["id"].(string)
	if code := postReply(permID, map[string]interface{}{"reply": "once", "grantClass": true}); code != http.StatusOK {
		t.Fatalf("grant reply status %d", code)
	}
	if hErr := <-errCh; hErr != nil {
		t.Fatalf("first edit failed after grant reply: %v", hErr)
	}

	// Second edit to the SAME file auto-approves via session grant without prompting!
	ctx2, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel2()
	if err := hook(ctx2, NativePermissionRequest{
		SessionID: sid, CallID: "call_2", ToolName: "edit", Input: rawSkillArgs,
	}); err != nil {
		t.Fatalf("second edit to granted file should auto-approve, got: %v", err)
	}

	// Different skill file, .git/config, and hoxi.json MUST still ask; .git/config & hoxi.json must NOT offer file grant!
	svc.gate.waitLimit = 50 * time.Millisecond
	for _, ungrantable := range []string{".git/config", "hoxi.json"} {
		rawBad, _ := json.Marshal(map[string]string{"filePath": ungrantable})
		done := make(chan error, 1)
		go func() {
			done <- hook(context.Background(), NativePermissionRequest{
				SessionID: sid, CallID: "call_bad_" + ungrantable, ToolName: "edit", Input: rawBad,
			})
		}()
		badAsked := waitForEvent(t, events, "permission.asked")
		badMeta, _ := badAsked.Properties["metadata"].(map[string]interface{})
		if got := stringList(badMeta[grantClassesMetaKey]); len(got) != 0 {
			t.Fatalf("%s must NEVER offer file grant classes, got %#v", ungrantable, got)
		}
		badID, _ := badAsked.Properties["id"].(string)
		if code := postReply(badID, map[string]interface{}{"reply": "once", "grantClass": true}); code == http.StatusOK {
			t.Fatalf("grantClass reply for %s must fail!", ungrantable)
		}
		_ = postReply(badID, map[string]interface{}{"reply": "reject"})
		<-done
	}
}

func TestNativeAdditionalWorkspaceFoldersAndMCPListTools(t *testing.T) {
	primary, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	isolatedDir := filepath.Join(primary, ".hoxi", "worktrees", "ses_iso")
	for _, d := range []string{primary, sibling, isolatedDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(sibling, "pkg.go"), []byte("package sib\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sibling, ".env"), []byte("S=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	svc := NewService(primary, nil, statusbar.NewTracker(), filepath.Join(t.TempDir(), "sessions.json"))
	svc.SetProjectResolver(multiRootStubResolver{id: "proj_multi", roots: []string{primary, sibling}})
	setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: ApprovalModeSmart})
	svc.gate.waitLimit = 40 * time.Millisecond

	localRec := SessionRecord{
		ID: "ses_local_multi", ProjectID: "proj_multi", Runtime: string(RuntimeNative),
		SandboxMode: "local", ProjectDir: primary, WorktreePath: primary,
	}
	isoRec := SessionRecord{
		ID: "ses_iso", ProjectID: "proj_multi", Runtime: string(RuntimeNative),
		SandboxMode: "worktree", ProjectDir: primary, WorktreePath: isolatedDir,
	}
	for _, r := range []SessionRecord{localRec, isoRec} {
		if err := svc.store.Put(r); err != nil {
			t.Fatal(err)
		}
	}

	localHook := svc.nativePermissionHook(localRec, "msg_1")
	isoHook := svc.nativePermissionHook(isoRec, "msg_2")

	sibFileJSON, _ := json.Marshal(map[string]string{"filePath": filepath.Join(sibling, "pkg.go")})
	sibEnvJSON, _ := json.Marshal(map[string]string{"filePath": filepath.Join(sibling, ".env")})

	// Local mode: reading & editing sibling workspace folder auto-approves in smart mode, while sibling .env still asks!
	if err := localHook(context.Background(), NativePermissionRequest{SessionID: localRec.ID, CallID: "r1", ToolName: "read", IsRead: true, Input: sibFileJSON}); err != nil {
		t.Fatalf("local read in sibling folder failed: %v", err)
	}
	if err := localHook(context.Background(), NativePermissionRequest{SessionID: localRec.ID, CallID: "e1", ToolName: "edit", Input: sibFileJSON}); err != nil {
		t.Fatalf("local edit in sibling folder failed: %v", err)
	}
	if err := localHook(context.Background(), NativePermissionRequest{SessionID: localRec.ID, CallID: "env1", ToolName: "read", IsRead: true, Input: sibEnvJSON}); err == nil {
		t.Fatal("reading .env in sibling folder MUST still ask and time out")
	}

	// Isolated worktree mode: reading sibling folder auto-approves, but editing sibling folder still asks!
	if err := isoHook(context.Background(), NativePermissionRequest{SessionID: isoRec.ID, CallID: "ir1", ToolName: "read", IsRead: true, Input: sibFileJSON}); err != nil {
		t.Fatalf("isolated read of sibling workspace folder failed: %v", err)
	}
	if err := isoHook(context.Background(), NativePermissionRequest{SessionID: isoRec.ID, CallID: "ie1", ToolName: "edit", Input: sibFileJSON}); err == nil {
		t.Fatal("isolated worktree session MUST still ask before editing sibling folder in smart mode")
	}

	// mcp_list_tools auto-allows in smart mode when !ForceAsk, but asks when ForceAsk=true (repo-local hoxi.json MCP) or Manual mode!
	if err := localHook(context.Background(), NativePermissionRequest{SessionID: localRec.ID, CallID: "mcp1", ToolName: "mcp_list_tools", IsRead: true, Input: []byte(`{}`)}); err != nil {
		t.Fatalf("user-configured mcp_list_tools should auto-allow in smart mode, got: %v", err)
	}
	if err := localHook(context.Background(), NativePermissionRequest{SessionID: localRec.ID, CallID: "mcp2", ToolName: "mcp_list_tools", IsRead: true, ForceAsk: true, Input: []byte(`{}`)}); err == nil {
		t.Fatal("ForceAsk mcp_list_tools (repo-local hoxi.json) MUST ask")
	}
	setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: ApprovalModeManual})
	if err := localHook(context.Background(), NativePermissionRequest{SessionID: localRec.ID, CallID: "mcp3", ToolName: "mcp_list_tools", IsRead: true, Input: []byte(`{}`)}); err == nil {
		t.Fatal("manual mode mcp_list_tools MUST ask")
	}
}

func TestNativeOnDemandSandboxRunsTemplateAndStringInlineScripts(t *testing.T) {
	svc, rawWorkspace := newOnDemandFixture(t, ApprovalModeAuto)
	workspace, err := filepath.EvalSymlinks(rawWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	if !sandbox.Available() {
		t.Skip("seatbelt unavailable")
	}
	svc.gate.waitLimit = 40 * time.Millisecond
	rec := SessionRecord{
		ID: "ses_nat_tpl", Runtime: string(RuntimeNative),
		ProjectDir: workspace, WorktreePath: workspace,
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	cred := hookCredential{runtime: string(RuntimeNative), sessionID: rec.ID, messageID: "msg_tpl"}

	// File-writing Node inline script with template literal backticks and "system"/"http" inside string data runs under on-demand sandbox!
	safeWriteScript := "node -e 'const fs=require(\"fs\"); const n=3; fs.writeFileSync(\"out.txt\", `count=${n}\\nsystem http ready\\n`)'"
	if ok, reason := svc.gateCommand(httptest.NewRequest(http.MethodPost, "/", nil), cred, rec, approvalAsk{
		command: safeWriteScript, cwd: workspace, callID: "c_tpl_ok", source: string(RuntimeNative),
	}); !ok {
		t.Fatalf("expected template-literal Node write script wrapped in on-demand sandbox, got: %s", reason)
	}

	// Hex-escaped child_process and bracket process["env"] MUST still be refused by on-demand sandbox and prompt!
	for _, bypass := range []string{
		`node -e 'require("\x63\x68\x69\x6c\x64\x5f\x70\x72\x6f\x63\x65\x73\x73")'`,
		`node -e 'const fs=require("fs"); fs.writeFileSync("out.txt", process["env"].HOME)'`,
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
		r, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/", nil)
		if approved, _ := svc.gateCommand(r, cred, rec, approvalAsk{
			command: bypass, cwd: workspace, callID: "c_tpl_bad", source: string(RuntimeNative),
		}); approved {
			cancel()
			t.Fatalf("bypass inline script %q must NOT be wrapped in on-demand sandbox", bypass)
		}
		cancel()
	}
}
