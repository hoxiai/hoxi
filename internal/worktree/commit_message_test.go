package worktree

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/llm"
)

type fakeLLM struct {
	text        string
	err         error
	noEndpoint  bool
	lastRequest llm.Request
}

func (f *fakeLLM) Resolve(providerID, modelID, baseURL, npm string) llm.Target {
	return llm.Target{ProviderID: providerID, ModelID: modelID, BaseURL: baseURL, NPM: npm, HasEndpoint: !f.noEndpoint}
}

func (f *fakeLLM) Complete(_ context.Context, target llm.Target, req llm.Request) (string, error) {
	f.lastRequest = req
	if !target.Ready() {
		return "", llm.ErrUnavailable
	}
	return f.text, f.err
}

func postJSON(t *testing.T, h http.HandlerFunc, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(body)
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/", &buf))
	return rec
}

func TestCleanGeneratedCommitMessage(t *testing.T) {
	t.Parallel()
	truncatedReasoningJSON := `{"choices":[{"finish_reason":"length","index":0,"logprobs":null,"message":{"content":"","reasoning_content":"The user wants a git commit message..."}}]}`
	if msg := cleanGeneratedCommitMessage(truncatedReasoningJSON); msg != "" {
		t.Fatalf("expected cleanGeneratedCommitMessage to reject raw JSON, got %q", msg)
	}
	if got := cleanGeneratedCommitMessage("feat(git): 新增底部一键提交并推送功能"); got != "feat(git): 新增底部一键提交并推送功能" {
		t.Fatalf("unexpected cleaned commit message: %q", got)
	}
	if got := cleanGeneratedCommitMessage("<think>plan</think>\nCommit message: fix(runner): guard nil session."); got != "fix(runner): guard nil session" {
		t.Fatalf("unexpected cleaned commit message: %q", got)
	}
}

func TestHandleGenerateCommitMessage(t *testing.T) {
	t.Parallel()
	m := NewManager(t.TempDir())
	client := &fakeLLM{text: "<think>x</think>\nfeat(git): add one-click commit"}
	m.SetLLM(client)
	rec := postJSON(t, m.HandleGenerateCommitMessage, generateCommitMessageRequest{ProviderID: "p", ModelID: "m", DiffSummary: "M a.go"})
	if !strings.Contains(rec.Body.String(), `"message":"feat(git): add one-click commit"`) {
		t.Fatalf("unexpected commit message response: %s", rec.Body.String())
	}
	if client.lastRequest.Purpose != llm.PurposeInteractive || client.lastRequest.MaxTokens != 2048 {
		t.Fatalf("commit message must use the interactive profile: %+v", client.lastRequest)
	}

	client.noEndpoint = true
	rec = postJSON(t, m.HandleGenerateCommitMessage, generateCommitMessageRequest{ProviderID: "p", ModelID: "m", DiffSummary: "M a.go"})
	if !strings.Contains(rec.Body.String(), "LLM commit synthesis unavailable") {
		t.Fatalf("unreachable model must report unavailable: %s", rec.Body.String())
	}
	rec = postJSON(t, m.HandleGenerateCommitMessage, generateCommitMessageRequest{})
	if !strings.Contains(rec.Body.String(), "Diff summary or session context is required") {
		t.Fatalf("empty context must be rejected: %s", rec.Body.String())
	}
}
