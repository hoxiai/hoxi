package runner

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/statusbar"
)

func TestClaudeAndCodexFastModeCLIArgs(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir, nil, statusbar.NewTracker(), filepath.Join(t.TempDir(), "sessions.json"))
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	svc.SetHookBaseURL(srv.URL)

	// Build mode on Opus: single --settings JSON carrying BOTH fastMode=true AND PreToolUse hook.
	args, envList, revoke := svc.issueClaudeTurnSettings("build", "claude-opus-5-5", "ses_fast_1", "msg_fast_1", true)
	defer revoke()
	if len(args) != 2 || args[0] != "--settings" {
		t.Fatalf("expected single --settings pair, got %v", args)
	}
	for _, kv := range envList {
		k, v, _ := strings.Cut(kv, "=")
		if k == claudeHookTokenEnv && strings.Contains(args[1], v) {
			t.Fatal("hook token must never leak into --settings argv")
		}
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(args[1]), &doc); err != nil {
		t.Fatalf("unmarshal --settings JSON: %v", err)
	}
	if doc["fastMode"] != true {
		t.Fatalf("expected fastMode=true in --settings, got %+v", doc)
	}
	if _, hasHooks := doc["hooks"]; !hasHooks {
		t.Fatalf("expected PreToolUse hooks preserved alongside fastMode, got %+v", doc)
	}

	// Read-only plan mode on Opus: emits --settings '{"fastMode":true}' without hooks.
	planArgs, _, planRevoke := svc.issueClaudeTurnSettings("plan", "claude-opus-5-5", "ses_fast_1", "msg_fast_2", true)
	defer planRevoke()
	if len(planArgs) != 2 || planArgs[0] != "--settings" || !strings.Contains(planArgs[1], `"fastMode":true`) {
		t.Fatalf("expected read-only Opus fastMode settings, got %v", planArgs)
	}

	// Sonnet / Haiku: fastMode is suppressed so Claude CLI does not error or switch models.
	sonnetPlanArgs, _, sr := svc.issueClaudeTurnSettings("plan", "claude-sonnet-4-6", "ses_fast_1", "msg_fast_3", true)
	defer sr()
	if len(sonnetPlanArgs) != 0 {
		t.Fatalf("expected Sonnet plan mode to omit fastMode settings, got %v", sonnetPlanArgs)
	}

	// Codex CLI: appends -c service_tier="fast" on eligible models for both fresh and resumed threads.
	wantFresh := []string{"exec", "--json", "--skip-git-repo-check", "--full-auto", "-m", "gpt-5.4", "-c", `model_reasoning_effort="high"`, "-c", `service_tier="fast"`, "-"}
	if got := BuildCodexArgsWithFast("build", "gpt-5.4", "high", "", true); !reflect.DeepEqual(got, wantFresh) {
		t.Fatalf("BuildCodexArgsWithFast fresh = %v, want %v", got, wantFresh)
	}
	wantResume := []string{"exec", "resume", "thr_42", "--json", "--skip-git-repo-check", "-m", "gpt-5.4", "-c", `service_tier="fast"`, "-"}
	if got := BuildCodexArgsWithFast("build", "gpt-5.4", "", "thr_42", true); !reflect.DeepEqual(got, wantResume) {
		t.Fatalf("BuildCodexArgsWithFast resume = %v, want %v", got, wantResume)
	}
	if got := BuildCodexArgsWithFast("build", "gpt-5-mini", "", "", true); strings.Contains(strings.Join(got, " "), "service_tier") {
		t.Fatalf("expected mini model to omit service_tier, got %v", got)
	}
}

func TestClaudeStreamFastTierAndDigest(t *testing.T) {
	tr := NewTurnTranslator("ses_1", string(RuntimeClaudeCode), "msg_a", "msg_u", "claude-code", "claude-opus-5-5", "build", "")
	_ = tr.FeedLine(`{"type":"stream_event","event":{"type":"message_start","message":{"id":"m1","model":"claude-opus-5-5","usage":{"input_tokens":10,"output_tokens":5,"speed":"fast"}}}}`)
	if tr.MsgInfo.ServiceTier != "fast" {
		t.Fatalf("expected Claude translator ServiceTier=fast, got %q", tr.MsgInfo.ServiceTier)
	}

	dStd := PromptRequestDigest("ses_1", PromptRequest{Text: "fix bug", FastMode: false})
	dFast := PromptRequestDigest("ses_1", PromptRequest{Text: "fix bug", FastMode: true})
	if dStd == dFast {
		t.Fatal("PromptRequestDigest must distinguish FastMode=true from FastMode=false")
	}
}
