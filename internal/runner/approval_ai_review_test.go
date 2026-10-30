package runner

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hoxiai/hoxi/internal/llm"
	"github.com/hoxiai/hoxi/internal/statusbar"
)

type stubAIReviewLLM struct {
	calls    atomic.Int32
	ready    bool
	response string
	err      error
	mu       sync.Mutex
	last     llm.Request
}

func (s *stubAIReviewLLM) lastRequest() llm.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

func (s *stubAIReviewLLM) Resolve(providerID, modelID, baseURL, npm string) llm.Target {
	if !s.ready {
		return llm.Target{}
	}
	return llm.Target{
		ProviderID:  providerID,
		ModelID:     modelID,
		BaseURL:     "https://example.test/v1",
		HasEndpoint: true,
	}
}

func (s *stubAIReviewLLM) Complete(_ context.Context, _ llm.Target, req llm.Request) (string, error) {
	s.calls.Add(1)
	s.mu.Lock()
	s.last = req
	s.mu.Unlock()
	if s.err != nil {
		return "", s.err
	}
	return s.response, nil
}

const shoplyInspectionScript = `bash -c '
cd /Users/hugh/code/kuaiqin/shoply
find . -name .git -not -path "*/node_modules/*" -not -path "*/vendor/*" -not -path "*/.claude/*" | sort | while read -r gitdir; do
  repo=$(dirname "$gitdir")
  [ "$repo" = "." ] && repo="."
  b=$(git -C "$repo" rev-parse --abbrev-ref HEAD 2>/dev/null)
  has_origin=$(git -C "$repo" remote 2>/dev/null | grep -qx origin && echo yes || echo no)
  up=$(git -C "$repo" rev-parse --abbrev-ref --symbolic-full-name @{u} 2>/dev/null || echo none)
  s=$(git -C "$repo" status --porcelain 2>/dev/null | wc -l | tr -d " ")
  if [ "$b" = "HEAD" ] || [ "$has_origin" = "no" ] || [ "$up" = "none" ]; then
    echo "PROBLEM | $repo | branch=$b | origin=$has_origin | upstream=$up | changes=$s"
  fi
done
echo "=== check done ==="
'`

func TestAIReviewApprovesOpaqueReadOnlyInspectionAndCaches(t *testing.T) {
	f := newGateFixture(t)
	defer f.revoke()

	mockLLM := &stubAIReviewLLM{
		ready:    true,
		response: `{"decision":"allow","reason":"read-only multi-repo git status check"}`,
	}
	f.svc.SetLLM(mockLLM)
	setTestApprovalPolicy(t, f.svc, ApprovalPolicy{
		Mode:          ApprovalModeAuto,
		AIReview:      true,
		AIReviewModel: "openai/gpt-4o-mini",
	})

	d1 := f.callHook(t, f.payload(shoplyInspectionScript, f.dir, "conv-1"))
	if d1.Decision != hookDecisionApprove {
		t.Fatalf("expected approve from AI review, got %+v", d1)
	}
	if got := mockLLM.calls.Load(); got != 1 {
		t.Fatalf("expected 1 LLM call, got %d", got)
	}

	// Second call with identical command and workspace must hit the in-memory cache.
	d2 := f.callHook(t, f.payload(shoplyInspectionScript, f.dir, "conv-1"))
	if d2.Decision != hookDecisionApprove {
		t.Fatalf("expected cached approve from AI review, got %+v", d2)
	}
	if got := mockLLM.calls.Load(); got != 1 {
		t.Fatalf("expected cached verdict without extra LLM call, got %d calls", got)
	}

	entries := f.svc.audit.list("ses_agy_gate", 10)
	if len(entries) == 0 {
		t.Fatalf("expected audit entry for AI-reviewed approval")
	}
	if entries[0].Source != approvalSourceAIReview || entries[0].Decision != "once" {
		t.Fatalf("unexpected audit entry: %+v", entries[0])
	}
}

func TestAIReviewNeverCalledForDestructiveOrHardDenyCommands(t *testing.T) {
	f := newGateFixture(t)
	defer f.revoke()

	mockLLM := &stubAIReviewLLM{
		ready:    true,
		response: `{"decision":"allow","reason":"maliciously trying to allow"}`,
	}
	f.svc.SetLLM(mockLLM)
	pol := ApprovalPolicy{
		Mode:          ApprovalModeAuto,
		AIReview:      true,
		AIReviewModel: "openai/gpt-4o-mini",
	}
	setTestApprovalPolicy(t, f.svc, pol)

	refused := []string{
		"rm -rf ./dist",
		"bash -c 'rm -rf ./dist'",
		"git reset --hard HEAD~1",
		"git push --force origin main",
		"git push origin main",
		"sudo ls -la",
		"cat .env",
		"bash -c 'cat ~/.ssh/id_rsa'",
		`eval "$USER_CMD"`,
		`bash -c '$CMD --help'`,
		"kill -9 1234",
		"echo sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJ",
	}

	for _, cmd := range refused {
		item := map[string]interface{}{
			"permission": "bash",
			"patterns":   []string{cmd},
			"metadata":   map[string]interface{}{"command": cmd},
		}
		if f.svc.aiReviewCommand(context.Background(), item, f.dir).allowed() {
			t.Fatalf("expected %q to be refused before AI review", cmd)
		}
	}
	if got := mockLLM.calls.Load(); got != 0 {
		t.Fatalf("expected 0 LLM calls for destructive/hard-deny commands, got %d", got)
	}
}

func TestAIReviewRespectsModeAndFailsClosed(t *testing.T) {
	f := newGateFixture(t)
	defer f.revoke()

	mockLLM := &stubAIReviewLLM{
		ready:    true,
		response: `{"decision":"allow","reason":"read-only"}`,
	}
	f.svc.SetLLM(mockLLM)

	item := map[string]interface{}{
		"permission": "bash",
		"patterns":   []string{shoplyInspectionScript},
		"metadata":   map[string]interface{}{"command": shoplyInspectionScript},
	}

	// 1. Disabled by default -> never calls LLM.
	setTestApprovalPolicy(t, f.svc, ApprovalPolicy{Mode: ApprovalModeAuto})
	if f.svc.aiReviewCommand(context.Background(), item, f.dir).allowed() {
		t.Fatalf("expected false when AIReview is disabled")
	}
	// 2. Manual mode -> never calls LLM even when AIReview is true.
	setTestApprovalPolicy(t, f.svc, ApprovalPolicy{
		Mode:          ApprovalModeManual,
		AIReview:      true,
		AIReviewModel: "openai/gpt-4o-mini",
	})
	if f.svc.aiReviewCommand(context.Background(), item, f.dir).allowed() {
		t.Fatalf("expected false in manual mode")
	}
	// 3. Bash override = ask -> never calls LLM.
	setTestApprovalPolicy(t, f.svc, ApprovalPolicy{
		Mode:          ApprovalModeAuto,
		Bash:          "ask",
		AIReview:      true,
		AIReviewModel: "openai/gpt-4o-mini",
	})
	if f.svc.aiReviewCommand(context.Background(), item, f.dir).allowed() {
		t.Fatalf("expected false when bash rule is ask")
	}
	if got := mockLLM.calls.Load(); got != 0 {
		t.Fatalf("expected 0 LLM calls when gated by mode/rule, got %d", got)
	}

	// 4. LLM returns "ask" -> fails closed.
	setTestApprovalPolicy(t, f.svc, ApprovalPolicy{
		Mode:          ApprovalModeAuto,
		AIReview:      true,
		AIReviewModel: "openai/gpt-4o-mini",
	})
	mockLLM.response = `{"decision":"ask","reason":"mutates state"}`
	if f.svc.aiReviewCommand(context.Background(), item, f.dir).allowed() {
		t.Fatalf("expected false when LLM returns ask")
	}

	// 5. LLM returns invalid JSON or error -> fails closed.
	f.svc.aiReviews = newAIReviewCache()
	mockLLM.response = `not json at all`
	if f.svc.aiReviewCommand(context.Background(), item, f.dir).allowed() {
		t.Fatalf("expected false on invalid JSON")
	}

	f.svc.aiReviews = newAIReviewCache()
	mockLLM.err = errors.New("upstream timeout")
	if f.svc.aiReviewCommand(context.Background(), item, f.dir).allowed() {
		t.Fatalf("expected false on LLM error")
	}
}

func TestApprovalPolicyValidateAIReviewModel(t *testing.T) {
	valid := ApprovalPolicy{
		Mode:          ApprovalModeAuto,
		AIReview:      true,
		AIReviewModel: "hoxi-auto/free",
	}
	if err := valid.validate(); err != nil {
		t.Fatalf("expected valid policy, got %v", err)
	}

	invalidSpecs := []string{
		"just-a-model",
		"/missing-provider",
		"missing-model/",
		"openai/gpt 4o",
		"../openai/gpt-4o",
		"openai\\gpt-4o",
	}
	for _, spec := range invalidSpecs {
		p := ApprovalPolicy{Mode: ApprovalModeAuto, AIReview: true, AIReviewModel: spec}
		if err := p.validate(); err == nil {
			t.Fatalf("expected error for invalid aiReviewModel %q", spec)
		}
	}

	// Setting AIReview without a model normalizes AIReview to false so the
	// policy never claims AI review is active when no model is configured.
	store := newApprovalPolicyStore(t.TempDir() + "/sessions.json")
	saved, err := store.set(ApprovalPolicy{Mode: ApprovalModeAuto, AIReview: true, AIReviewModel: "  "})
	if err != nil {
		t.Fatalf("unexpected error saving policy: %v", err)
	}
	if saved.AIReview || saved.AIReviewModel != "" {
		t.Fatalf("expected AIReview=false when AIReviewModel is empty, got %+v", saved)
	}

	if got := aiReviewPromptForPolicy(ApprovalPolicy{Mode: ApprovalModeAuto}); got != aiReviewAutoPrompt {
		t.Fatalf("expected auto prompt in auto mode")
	}
	if got := aiReviewPromptForPolicy(ApprovalPolicy{Mode: ApprovalModeSmart}); got != aiReviewSmartPrompt {
		t.Fatalf("expected smart prompt in smart mode")
	}
	if got := aiReviewPromptForPolicy(ApprovalPolicy{Mode: ApprovalModeAuto, Edit: "ask"}); got != aiReviewSmartPrompt {
		t.Fatalf("expected smart prompt when edit override is ask")
	}
}

// Reasoning models think before the verdict; a 120-token cap left
// DeepSeek-class flash models with an empty answer on every real command.
func TestAIReviewLeavesRoomForReasoningModels(t *testing.T) {
	f := newGateFixture(t)
	defer f.revoke()
	mockLLM := &stubAIReviewLLM{ready: true, response: `{"decision":"allow","reason":"read-only"}`}
	f.svc.SetLLM(mockLLM)
	setTestApprovalPolicy(t, f.svc, ApprovalPolicy{Mode: ApprovalModeAuto, AIReview: true, AIReviewModel: "subapi/flash"})
	item := map[string]interface{}{
		"permission": "bash",
		"patterns":   []string{shoplyInspectionScript},
		"metadata":   map[string]interface{}{"command": shoplyInspectionScript},
	}
	if !f.svc.aiReviewCommand(context.Background(), item, f.dir).allowed() {
		t.Fatalf("expected allow")
	}
	req := mockLLM.lastRequest()
	if req.MaxTokens < 1024 || req.ReasoningEffort != "low" || req.Timeout < 10e9 {
		t.Fatalf("review request leaves no room to think: max=%d effort=%q timeout=%s", req.MaxTokens, req.ReasoningEffort, req.Timeout)
	}
}

func TestAIReviewOutcomeSaysWhyItDidNotAllow(t *testing.T) {
	f := newGateFixture(t)
	defer f.revoke()
	setTestApprovalPolicy(t, f.svc, ApprovalPolicy{Mode: ApprovalModeAuto, AIReview: true, AIReviewModel: "subapi/flash"})
	item := map[string]interface{}{
		"permission": "bash",
		"patterns":   []string{shoplyInspectionScript},
		"metadata":   map[string]interface{}{"command": shoplyInspectionScript},
	}
	cases := []struct {
		name     string
		stub     *stubAIReviewLLM
		status   string
		failure  string
		contains string
	}{
		{"empty answer", &stubAIReviewLLM{ready: true, response: ""}, aiReviewUnavailable, aiReviewFailEmpty, ""},
		{"only thinking", &stubAIReviewLLM{ready: true, response: "<think>is it read-only? maybe"}, aiReviewUnavailable, aiReviewFailEmpty, ""},
		{"not a verdict", &stubAIReviewLLM{ready: true, response: "sure, looks fine"}, aiReviewUnavailable, aiReviewFailInvalid, ""},
		{"timeout", &stubAIReviewLLM{ready: true, err: fmt.Errorf("post: %w", context.DeadlineExceeded)}, aiReviewUnavailable, aiReviewFailTimeout, ""},
		{"http error", &stubAIReviewLLM{ready: true, err: errors.New("HTTP 503: upstream overloaded")}, aiReviewUnavailable, aiReviewFailError, "503"},
		{"model missing", &stubAIReviewLLM{ready: false}, aiReviewUnavailable, aiReviewFailUnready, ""},
		{"model asks", &stubAIReviewLLM{ready: true, response: `{"decision":"ask","reason":"writes files"}`}, aiReviewAsk, "", "writes files"},
	}
	for _, tc := range cases {
		f.svc.aiReviews = newAIReviewCache()
		f.svc.SetLLM(tc.stub)
		got := f.svc.aiReviewCommand(context.Background(), item, f.dir)
		if got.allowed() || got.status != tc.status || got.failure != tc.failure {
			t.Fatalf("%s: got %+v, want status=%s failure=%s", tc.name, got, tc.status, tc.failure)
		}
		if tc.contains != "" && !strings.Contains(got.detail+got.reason, tc.contains) {
			t.Fatalf("%s: expected %q in %+v", tc.name, tc.contains, got)
		}
		// A failed review is cached briefly with the same explanation.
		if again := f.svc.aiReviewCommand(context.Background(), item, f.dir); again != got {
			t.Fatalf("%s: cached outcome %+v differs from %+v", tc.name, again, got)
		}
	}
}

func TestAIReviewFailureReachesApprovalCard(t *testing.T) {
	f := newGateFixture(t)
	defer f.revoke()
	f.svc.SetLLM(&stubAIReviewLLM{ready: true, response: ""})
	setTestApprovalPolicy(t, f.svc, ApprovalPolicy{Mode: ApprovalModeAuto, AIReview: true, AIReviewModel: "subapi/flash"})
	events, unsub := f.svc.Subscribe("ses_agy_gate")
	defer unsub()

	result := f.asyncHook(t, f.payload(shoplyInspectionScript, f.dir, "conv-1"))
	asked := waitForEvent(t, events, "permission.asked")
	note, _ := itemMetadata(asked.Properties)[aiReviewMetaKey].(map[string]interface{})
	if note["status"] != aiReviewUnavailable || note["failure"] != aiReviewFailEmpty {
		t.Fatalf("expected the card to say AI review gave no verdict, got %#v", asked.Properties["metadata"])
	}
	id, _ := asked.Properties["id"].(string)
	if code := f.reply(t, id, "reject", ""); code != http.StatusOK {
		t.Fatalf("reply status %d", code)
	}
	if d := waitDecision(t, result, "rejected hook"); d.Decision == hookDecisionApprove {
		t.Fatalf("a failed review must not approve: %+v", d)
	}
}

func TestAnnotateAIReviewSkipsAllowedAndIneligible(t *testing.T) {
	for _, outcome := range []aiReviewOutcome{{}, {status: aiReviewAllow, reason: "ok"}} {
		item := map[string]interface{}{"permission": "bash"}
		annotateAIReview(item, outcome)
		if _, ok := itemMetadata(item)[aiReviewMetaKey]; ok {
			t.Fatalf("outcome %+v must not annotate the card", outcome)
		}
	}
}

// OpenCode requests are listed again on every refresh; the review note must
// survive on the fresh copy so the card keeps explaining why it is shown.
func TestOpenCodeAIReviewFailureStaysOnListedRequest(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir, nil, statusbar.NewTracker(), filepath.Join(t.TempDir(), "sessions.json"))
	svc.SetLLM(&stubAIReviewLLM{ready: true, response: ""})
	setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: ApprovalModeAuto, AIReview: true, AIReviewModel: "subapi/flash"})
	item := map[string]interface{}{
		"id": "per_oc_review", "sessionID": "oc-review", "permission": "bash",
		"patterns": []interface{}{shoplyInspectionScript},
		"metadata": map[string]interface{}{"command": shoplyInspectionScript},
	}
	fake := &flakyOpenCodePermissions{pending: []map[string]interface{}{item}}
	server := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer server.Close()
	svc.ConfigureOpenCode(server.URL, "secret")
	if err := svc.store.Put(SessionRecord{ID: "ses_oc_review", Runtime: "opencode", ProjectDir: dir, WorktreePath: dir, ExternalID: "oc-review"}); err != nil {
		t.Fatal(err)
	}

	if got := listedIDs(svc.listUnifiedPermissions(context.Background(), dir)); len(got) != 0 {
		t.Fatalf("first listing = %v, want the request held while AI review runs", got)
	}
	waitFor(t, "failed AI review", func() bool {
		return svc.openCodeAutoReplyState("per_oc_review") == openCodeAutoReplyFailed
	})
	listed := svc.listUnifiedPermissions(context.Background(), dir)
	if len(listed) != 1 {
		t.Fatalf("expected the request shown to the user, got %v", listedIDs(listed))
	}
	note, _ := itemMetadata(listed[0])[aiReviewMetaKey].(map[string]interface{})
	if note["status"] != aiReviewUnavailable || note["failure"] != aiReviewFailEmpty {
		t.Fatalf("listed request lost the AI review note: %#v", listed[0]["metadata"])
	}
	if sent := fake.sent(); len(sent) != 0 {
		t.Fatalf("a failed review must not answer OpenCode, sent %v", sent)
	}
}
