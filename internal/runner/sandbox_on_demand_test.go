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

	"github.com/hoxiai/hoxi/internal/sandbox"
	"github.com/hoxiai/hoxi/internal/statusbar"
)

func newOnDemandFixture(t *testing.T, mode string) (*Service, string) {
	t.Helper()
	if !sandbox.Available() {
		t.Skip("seatbelt backend unavailable")
	}
	svc := NewService(t.TempDir(), nil, statusbar.NewTracker(), filepath.Join(t.TempDir(), "sessions.json"))
	setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: mode})
	return svc, t.TempDir()
}

// wrappedRequest is the permission request OpenCode sends once the plugin has
// wrapped and tagged command, after Hoxi sanitizes it.
func wrappedRequest(profile, command string) map[string]interface{} {
	tagged := "# hoxi-stop:ses_oc:call_1\n" + openCodeStopTrap + "\n" + sandboxWrappedCommand(profile, command)
	item := openCodeBashItem("ses_oc", tagged)
	item["patterns"] = []interface{}{openCodeStopTrapUSR1, "trap : EXIT", sandboxWrappedCommand(profile, command)}
	sanitizeOpenCodePermissionItem(item)
	return item
}

// An opaque command Hoxi used to ask about runs unasked in auto mode once it
// is wrapped in this process's on-demand profile for its directory.
func TestAutoModeRunsOpaqueCommandUnderOnDemandSandbox(t *testing.T) {
	svc, workspace := newOnDemandFixture(t, ApprovalModeAuto)
	auto := ApprovalPolicy{Mode: ApprovalModeAuto}
	for _, command := range []string{
		`for f in $(ls); do wc -l "$f"; done`,
		`X=$(cat name.txt); grep -n "$X" README.md`,
		`F=$(cat path.txt); cat "$F"`,
		localeNodeCountCommand,
	} {
		if approvalAutoApproves(openCodeBashItem("ses_oc", command), auto, nil, workspace) {
			t.Fatalf("precondition: %q should ask without the sandbox", command)
		}
		profile := svc.onDemandSandboxProfile("ses_oc", workspace, command)
		if profile == "" {
			t.Fatalf("no on-demand profile for %q", command)
		}
		item := wrappedRequest(profile, command)
		if got := firstString(itemMetadata(item)["command"]); got != command {
			t.Fatalf("the user still sees the plain command, got %q", got)
		}
		if !approvalAutoApproves(item, auto, nil, workspace) {
			t.Errorf("auto mode should run %q under the on-demand sandbox", command)
		}
		if !approvalAutoApproves(item, ApprovalPolicy{Mode: ApprovalModeTrusted}, nil, workspace) {
			t.Errorf("trusted mode should run %q under the on-demand sandbox", command)
		}
		for _, mode := range []string{ApprovalModeSmart, ApprovalModeManual} {
			if approvalAutoApproves(item, ApprovalPolicy{Mode: mode}, nil, workspace) {
				t.Errorf("%s mode must still ask for %q", mode, command)
			}
		}
		if approvalAutoApproves(item, auto, nil, t.TempDir()) {
			t.Errorf("a profile for one directory must not cover a request in another: %q", command)
		}
	}
}

// Commands that ask for a reason the sandbox cannot answer get no profile,
// and wrapping them anyway does not make them run unasked.
func TestOnDemandSandboxRefusesWhatItCannotContain(t *testing.T) {
	svc, workspace := newOnDemandFixture(t, ApprovalModeAuto)
	auto := ApprovalPolicy{Mode: ApprovalModeAuto}
	profile, err := sandbox.NewManager("").WriteOnDemand(workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{
		`for f in $(ls); do rm -rf "$f"; done`,
		`F=$(cat path.txt); cat "$F" ~/.ssh/id_rsa`,
		`H=$(cat host.txt); ssh "root@$H" uptime`,
		`R=$(cat id.txt); gh run view "$R"`,
		`B=$(git branch --show-current); git push origin "$B"`,
		`eval "$(cat x.sh)"`,
		`X=$(cat v); sudo cp "$X" /etc/hosts`,
		`for d in en zh; do node -e "require('child_process').execSync('ls')"; done`,
		`for d in en zh; do node -e "console.log(process.env)"; done`,
		`for d in en zh; do node -e "console.log(require('fs').readFileSync('.env','utf8'))"; done`,
		`for d in en zh; do node -e "fetch('https://example.com/'+ '$d')"; done`,
		`for d in en zh; do node -e "console.log('$UNKNOWN')"; done`,
		`git log --pretty --output=.git/config`,
		`cd .git && git log --output=config`,
		`timeout 30s node -e "require('child_process').execSync('ls')"`,
	} {
		if got := svc.onDemandSandboxProfile("ses_oc", workspace, command); got != "" {
			t.Errorf("on-demand profile offered for %q", command)
		}
		if approvalAutoApproves(wrappedRequest(profile, command), auto, nil, workspace) {
			t.Errorf("a wrapped %q must still ask", command)
		}
	}
	// Commands the policy already runs stay unwrapped.
	for _, command := range []string{`git status`, `ls -la`} {
		if got := svc.onDemandSandboxProfile("ses_oc", workspace, command); got != "" {
			t.Errorf("a command auto mode already runs was wrapped: %q", command)
		}
	}
	// Smart mode never wraps; it asks for every opaque command.
	setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: ApprovalModeSmart})
	if got := svc.onDemandSandboxProfile("ses_oc", workspace, `for f in $(ls); do wc -l "$f"; done`); got != "" {
		t.Error("smart mode must not use the on-demand sandbox")
	}
}

// The wrapper must be the plugin's exact form around a profile this process
// wrote and that is unchanged; anything else is judged as it was before.
func TestOnDemandSandboxProofCannotBeForged(t *testing.T) {
	_, workspace := newOnDemandFixture(t, ApprovalModeAuto)
	auto := ApprovalPolicy{Mode: ApprovalModeAuto}
	command := `for f in $(ls); do wc -l "$f"; done`
	profile, err := sandbox.NewManager("").WriteOnDemand(workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	other, err := sandbox.NewManager("").WriteOnDemand(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(t.TempDir(), "allow.sb")
	if err := os.WriteFile(foreign, []byte("(version 1)(allow default)"), 0o600); err != nil {
		t.Fatal(err)
	}
	forged := []string{
		sandboxWrappedCommand(profile, command) + "; touch outside",
		"cd /tmp && " + sandboxWrappedCommand(profile, command),
		"sandbox-exec -f " + shellQuote(profile) + " -- /bin/sh -c " + shellQuote(command),
		"'/tmp/bin/sandbox-exec' -f " + shellQuote(profile) + " -- /bin/sh -c " + shellQuote(command),
		shellQuote(sandboxExecPath) + " -f " + shellQuote(profile) + " -- /bin/bash -c " + shellQuote(command),
		sandboxWrappedCommand(foreign, command),
		sandboxWrappedCommand(other, command),
	}
	for _, raw := range forged {
		item := openCodeBashItem("ses_oc", raw)
		sanitizeOpenCodePermissionItem(item)
		if approvalAutoApproves(item, auto, nil, workspace) {
			t.Errorf("forged wrapper ran unasked: %s", raw)
		}
	}
	// Two different profiles in one request are not one sandbox.
	mixed := openCodeBashItem("ses_oc", sandboxWrappedCommand(profile, command))
	mixed["patterns"] = []interface{}{sandboxWrappedCommand(profile, command), sandboxWrappedCommand(other, command)}
	sanitizeOpenCodePermissionItem(mixed)
	if approvalAutoApproves(mixed, auto, nil, workspace) {
		t.Error("a request mixing two profiles ran unasked")
	}
	// A proof decoded from JSON is not a proof.
	var decoded map[string]interface{}
	raw, _ := json.Marshal(map[string]interface{}{"permission": "bash", "patterns": []string{command}, "metadata": map[string]interface{}{"command": command}, sandboxProofKey: true})
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	sanitizeOpenCodePermissionItem(decoded)
	if _, kept := decoded[sandboxProofKey]; kept || approvalAutoApproves(decoded, auto, nil, workspace) {
		t.Error("a JSON hoxiSandboxed flag was trusted")
	}
	// A tampered profile stops covering the request; sanitizing again keeps
	// a real proof.
	item := wrappedRequest(profile, command)
	sanitizeOpenCodePermissionItem(item)
	if !approvalAutoApproves(item, auto, nil, workspace) {
		t.Fatal("re-sanitizing dropped the proof")
	}
	if out, _ := json.Marshal(item); !bytes.Contains(out, []byte(`"hoxiSandboxed":true`)) {
		t.Errorf("relayed request should say it was sandboxed: %s", out)
	}
	if err := os.WriteFile(profile, []byte("(version 1)(allow default)"), 0o600); err != nil {
		t.Fatal(err)
	}
	if approvalAutoApproves(item, auto, nil, workspace) {
		t.Error("a tampered on-demand profile still covered the request")
	}
}

func TestSandboxRouteAnswersThePlugin(t *testing.T) {
	svc, workspace := newOnDemandFixture(t, ApprovalModeAuto)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	post := func(command string) (int, string) {
		body, _ := json.Marshal(map[string]string{"sessionID": "ses_oc", "directory": workspace, "command": command})
		resp, err := http.Post(srv.URL+sandboxRoutePath, "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out struct {
			Profile string `json:"profile"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out.Profile
	}
	if code, profile := post(`for f in $(ls); do wc -l "$f"; done`); code != http.StatusOK || profile == "" {
		t.Fatalf("opaque command: status %d profile %q", code, profile)
	}
	if code, profile := post(`git status`); code != http.StatusOK || profile != "" {
		t.Fatalf("plain command: status %d profile %q", code, profile)
	}
	resp, err := http.Get(srv.URL + sandboxRoutePath)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET status %d", resp.StatusCode)
	}
}

// A session with CommandSandbox enabled uses a network-allow session profile
// (<hash(sessionID)>.sb) for ordinary commands, which OnDemandTrusted refuses
// for opaque ones. Tightening opaque commands to the on-demand profile lets
// auto mode approve them, while a fallback to the session profile still asks,
// and an explicit session "always" grant is not re-trapped in on-demand.
func TestOnDemandSandboxTightensCommandSandboxSessionAndRespectsGrants(t *testing.T) {
	svc, workspace := newOnDemandFixture(t, ApprovalModeAuto)
	workspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	otherDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rec := SessionRecord{
		ID:             "ses_hoxi_cmdsb",
		Runtime:        "opencode",
		ExternalID:     "oc_ext_cmdsb",
		ProjectDir:     workspace,
		WorktreePath:   workspace,
		CommandSandbox: true,
	}
	otherRec := SessionRecord{
		ID:             "ses_hoxi_other",
		Runtime:        "opencode",
		ExternalID:     "oc_ext_other",
		ProjectDir:     otherDir,
		WorktreePath:   otherDir,
		CommandSandbox: true,
	}
	for _, r := range []SessionRecord{rec, otherRec} {
		if err := svc.store.Put(r); err != nil {
			t.Fatal(err)
		}
	}
	snap, err := sandbox.NewManager(t.TempDir()).Set(rec.ExternalID, workspace, true, sandbox.NetworkAllow)
	if err != nil {
		t.Fatal(err)
	}
	opaqueCmd := `bash /opt/hoxi/shared/build.sh`
	// Session profile alone allows outbound network, so it must NOT cover opacity.
	if approvalAutoApproves(wrappedRequest(snap.ProfilePath, opaqueCmd), ApprovalPolicy{Mode: ApprovalModeAuto}, nil, workspace) {
		t.Fatal("session network-allow profile must not auto-approve opaque commands")
	}
	// Route offers the tighter on-demand profile for the ExternalID, which auto mode approves.
	tight := svc.onDemandSandboxProfile(rec.ExternalID, workspace, opaqueCmd)
	if tight == "" || tight == snap.ProfilePath {
		t.Fatalf("expected distinct on-demand profile, got %q (session %q)", tight, snap.ProfilePath)
	}
	if !approvalAutoApproves(wrappedRequest(tight, opaqueCmd), ApprovalPolicy{Mode: ApprovalModeAuto}, nil, workspace) {
		t.Fatal("tightened on-demand profile should auto-approve opaque command")
	}

	// Once the user grants "bash *" for this Hoxi session (e.g. after # hoxi:unsandboxed),
	// the route stops wrapping it in the no-network on-demand profile for this session only.
	svc.gate.addOpenCodeAlwaysGrants(rec.ID, "bash", []string{"bash *"}, "desktop")
	if got := svc.onDemandSandboxProfile(rec.ExternalID, workspace, opaqueCmd); got != "" {
		t.Fatalf("session-granted command should not be wrapped in on-demand sandbox, got %q", got)
	}
	if got := svc.onDemandSandboxProfile(otherRec.ExternalID, otherDir, opaqueCmd); got == "" {
		t.Fatal("another session without the grant must still get the on-demand profile")
	}
	// Spoofing rec.ExternalID against another directory must never apply rec's grants or offer a profile.
	if got := svc.onDemandSandboxProfile(rec.ExternalID, otherDir, opaqueCmd); got != "" {
		t.Fatalf("mismatched directory for known session must fail closed, got %q", got)
	}
}

func TestNativeEngineOnDemandSandboxAutoRunsOpaqueAndBlocksEscapes(t *testing.T) {
	svc, rawWorkspace := newOnDemandFixture(t, ApprovalModeAuto)
	workspace, err := filepath.EvalSymlinks(rawWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "hello.txt"), []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".env"), []byte("SECRET=topsecret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "target.txt"), []byte(".env\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := SessionRecord{
		ID:             "ses_nat_ondemand",
		Runtime:        string(RuntimeNative),
		ProjectDir:     workspace,
		WorktreePath:   workspace,
		CommandSandbox: true, // Also verifies tightening CommandSandbox without nested sandbox-exec!
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	cred := hookCredential{runtime: string(RuntimeNative), sessionID: rec.ID, messageID: "msg_a1"}

	// 1. Harmless opaque loop auto-approves via OnDemandProfile without prompting, wraps once (never nested), and audits Source="sandbox".
	loopCmd := `for f in $(ls hello.txt); do wc -l "$f"; done`
	reqCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpReq, _ := http.NewRequestWithContext(reqCtx, http.MethodPost, "/", nil)
	ok, reason := svc.gateCommand(httpReq, cred, rec, approvalAsk{
		command: loopCmd, cwd: workspace, callID: "call_loop", source: string(RuntimeNative),
	})
	if !ok {
		t.Fatalf("expected opaque harmless loop approved under on-demand sandbox, got deny: %s", reason)
	}
	entries := svc.audit.list("", 10)
	if len(entries) == 0 || entries[0].Source != approvalSourceSandbox {
		t.Fatalf("expected approval audit source=%q, got %+v", approvalSourceSandbox, entries)
	}

	canNestSeatbelt := exec.Command(sandboxExecPath, "-p", "(version 1)(allow default)", "/usr/bin/true").Run() == nil
	if canNestSeatbelt {
		out, code, runErr := svc.nativeCommandRunner(rec, nil)(reqCtx, workspace, loopCmd)
		if runErr != nil || code != 0 || !strings.Contains(out, "hello.txt") {
			t.Fatalf("expected sandboxed loop to succeed without nested sandbox-exec error, code=%d err=%v out=%q", code, runErr, out)
		}
	} else {
		// Inside an already-sandboxed runner (e.g. agy --sandbox), verify the exact single-wrap command argv & note helper.
		consumed := svc.gate.consumeNativeOnDemand(rec.ID, loopCmd)
		if consumed == "" {
			t.Fatal("expected stashed on-demand profile ticket")
		}
		bin, args, wrapErr := svc.confineNativeCommandWithOnDemand(rec, consumed, "/bin/sh", []string{"-c", loopCmd})
		if wrapErr != nil || bin != sandboxExecPath || len(args) != 6 || args[0] != "-f" || args[1] != consumed || args[3] != "/bin/sh" || args[4] != "-c" || args[5] != loopCmd {
			t.Fatalf("expected single non-nested sandbox-exec wrap, got bin=%q args=%#v err=%v", bin, args, wrapErr)
		}
		noted := appendOnDemandSandboxNote("cat: .env: Operation not permitted")
		if !strings.Contains(noted, unsandboxedMarker) {
			t.Fatalf("expected # hoxi:unsandboxed guidance appended on Seatbelt denial, got %q", noted)
		}
	}
	// Ticket is one-shot: consumed after execution.
	if leftover := svc.gate.consumeNativeOnDemand(rec.ID, loopCmd); leftover != "" {
		t.Fatalf("expected one-shot on-demand ticket consumed, got %q", leftover)
	}

	// 2. Indirect opaque read of .env inside OnDemandProfile is blocked by Seatbelt AND appends # hoxi:unsandboxed guidance.
	if canNestSeatbelt {
		indirectEnvCmd := `F=$(cat target.txt); cat "$F"`
		ok, reason = svc.gateCommand(httpReq, cred, rec, approvalAsk{
			command: indirectEnvCmd, cwd: workspace, callID: "call_env", source: string(RuntimeNative),
		})
		if !ok {
			t.Fatalf("expected indirect opaque read gated into on-demand sandbox, got: %s", reason)
		}
		envOut, envCode, _ := svc.nativeCommandRunner(rec, nil)(reqCtx, workspace, indirectEnvCmd)
		if envCode == 0 || strings.Contains(envOut, "topsecret") {
			t.Fatalf("Seatbelt on-demand profile failed to block .env read! code=%d out=%q", envCode, envOut)
		}
		if !strings.Contains(envOut, unsandboxedMarker) {
			t.Fatalf("expected # hoxi:unsandboxed hint appended on Seatbelt denial, got: %q", envOut)
		}
	}

	// 3. # hoxi:unsandboxed opt-out, destructive/hard-deny/host-only commands, and smart mode MUST NOT auto-approve via on-demand sandbox.
	for _, mustAsk := range []string{
		"# hoxi:unsandboxed\n" + loopCmd,
		`for f in $(ls); do rm -rf "$f"; done`,
		`H=$(cat target.txt); ssh "root@$H" uptime`,
		`B=$(git branch --show-current); git push origin "$B"`,
		`eval "$(cat target.txt)"`,
	} {
		shortCtx, shortCancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
		shortReq, _ := http.NewRequestWithContext(shortCtx, http.MethodPost, "/", nil)
		approved, _ := svc.gateCommand(shortReq, cred, rec, approvalAsk{
			command: mustAsk, cwd: workspace, callID: "call_ask", source: string(RuntimeNative),
		})
		shortCancel()
		if approved {
			t.Fatalf("command %q must NOT be auto-approved by on-demand sandbox", mustAsk)
		}
	}

	setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: ApprovalModeSmart})
	shortCtx, shortCancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	shortReq, _ := http.NewRequestWithContext(shortCtx, http.MethodPost, "/", nil)
	if approved, _ := svc.gateCommand(shortReq, cred, rec, approvalAsk{
		command: loopCmd, cwd: workspace, callID: "call_smart", source: string(RuntimeNative),
	}); approved {
		t.Fatal("smart mode must not auto-approve opaque command via on-demand sandbox")
	}
	shortCancel()

	// 4. Stashed tickets are cleaned up on turn end (cancelSessionApprovals), session delete (forgetSession),
	// and never leak when pre-exec validation (outside workdir or full background slots) rejects early.
	svc.gate.stashNativeOnDemand(rec.ID, loopCmd, "/tmp/fake.sb")
	svc.cancelSessionApprovals(rec.ID, "turn ended")
	if got := svc.gate.consumeNativeOnDemand(rec.ID, loopCmd); got != "" {
		t.Fatalf("expected cancelSessionApprovals to clear stashed tickets, got %q", got)
	}
	svc.gate.stashNativeOnDemand(rec.ID, loopCmd, "/tmp/fake.sb")
	svc.gate.forgetSession(rec.ID)
	if got := svc.gate.consumeNativeOnDemand(rec.ID, loopCmd); got != "" {
		t.Fatalf("expected forgetSession to clear stashed tickets, got %q", got)
	}

	setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: ApprovalModeAuto})
	outsideDir := t.TempDir()
	hookFn := svc.nativePermissionHook(rec, "msg_a2")
	badInput, _ := json.Marshal(map[string]any{"command": loopCmd, "workdir": outsideDir})
	if err := hookFn(reqCtx, NativePermissionRequest{ToolName: "bash", CallID: "call_out", Input: badInput}); err == nil {
		t.Fatal("expected nativePermissionHook to reject outside workdir upfront")
	}
	if leaked := svc.gate.consumeNativeOnDemand(rec.ID, loopCmd); leaked != "" {
		t.Fatalf("outside workdir leaked stashed ticket %q", leaked)
	}

	svc.gate.stashNativeOnDemand(rec.ID, loopCmd, "/tmp/fake_stream.sb")
	_, _, streamErr := svc.nativeStreamCommandRunner(rec, nil)(reqCtx, outsideDir, loopCmd, nil, nil)
	if streamErr == nil {
		t.Fatal("expected nativeStreamCommandRunner to reject outside dir")
	}
	if leaked := svc.gate.consumeNativeOnDemand(rec.ID, loopCmd); leaked != "" {
		t.Fatalf("early stream validation failure leaked ticket %q", leaked)
	}

	svc.bgProcs.mu.Lock()
	bgBucket := &nativeSessionBgBucket{jobs: make(map[string]*nativeBgJob)}
	for i := 0; i < maxNativeBgRunningPerSession; i++ {
		id := filepath.Base(t.TempDir())
		bgBucket.jobs[id] = &nativeBgJob{id: id, sessionID: rec.ID, running: true}
	}
	svc.bgProcs.sessions[rec.ID] = bgBucket
	svc.bgProcs.mu.Unlock()
	svc.gate.stashNativeOnDemand(rec.ID, loopCmd, "/tmp/fake_bg.sb")
	if _, bgErr := svc.nativeBackgroundManager(rec).Start(reqCtx, workspace, loopCmd); bgErr == nil {
		t.Fatal("expected Start to reject when maxNativeBgRunningPerSession is full")
	}
	if leaked := svc.gate.consumeNativeOnDemand(rec.ID, loopCmd); leaked != "" {
		t.Fatalf("full background slot failure leaked ticket %q", leaked)
	}
}
