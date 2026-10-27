package runner

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/hoxiai/hoxi/internal/llm"
)

type stubAIReviewLLM struct {
	calls    atomic.Int32
	ready    bool
	response string
	err      error
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

func (s *stubAIReviewLLM) Complete(_ context.Context, _ llm.Target, _ llm.Request) (string, error) {
	s.calls.Add(1)
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
		if ok, _ := f.svc.aiReviewCommand(context.Background(), item, f.dir); ok {
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
	if ok, _ := f.svc.aiReviewCommand(context.Background(), item, f.dir); ok {
		t.Fatalf("expected false when AIReview is disabled")
	}
	// 2. Manual mode -> never calls LLM even when AIReview is true.
	setTestApprovalPolicy(t, f.svc, ApprovalPolicy{
		Mode:          ApprovalModeManual,
		AIReview:      true,
		AIReviewModel: "openai/gpt-4o-mini",
	})
	if ok, _ := f.svc.aiReviewCommand(context.Background(), item, f.dir); ok {
		t.Fatalf("expected false in manual mode")
	}
	// 3. Bash override = ask -> never calls LLM.
	setTestApprovalPolicy(t, f.svc, ApprovalPolicy{
		Mode:          ApprovalModeAuto,
		Bash:          "ask",
		AIReview:      true,
		AIReviewModel: "openai/gpt-4o-mini",
	})
	if ok, _ := f.svc.aiReviewCommand(context.Background(), item, f.dir); ok {
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
	if ok, _ := f.svc.aiReviewCommand(context.Background(), item, f.dir); ok {
		t.Fatalf("expected false when LLM returns ask")
	}

	// 5. LLM returns invalid JSON or error -> fails closed.
	f.svc.aiReviews = newAIReviewCache()
	mockLLM.response = `not json at all`
	if ok, _ := f.svc.aiReviewCommand(context.Background(), item, f.dir); ok {
		t.Fatalf("expected false on invalid JSON")
	}

	f.svc.aiReviews = newAIReviewCache()
	mockLLM.err = errors.New("upstream timeout")
	if ok, _ := f.svc.aiReviewCommand(context.Background(), item, f.dir); ok {
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
}
