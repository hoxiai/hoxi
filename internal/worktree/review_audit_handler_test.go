package worktree

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

func TestHandleAuditRequiresValidObserverScore(t *testing.T) {
	for _, tc := range []struct {
		name       string
		content    string
		err        error
		noEndpoint bool
		ok         bool
	}{
		{name: "zero", content: `{"score":0,"summary":"Incorrect implementation"}`, ok: true},
		{name: "malformed", content: "Looks good overall"},
		{name: "missing score", content: `{"summary":"Looks good"}`},
		{name: "out of range", content: `{"score":101}`},
		{name: "nonfinite", content: `{"score":NaN}`},
		{name: "observer failure", err: errors.New("HTTP 503")},
		{name: "invalid endpoint", noEndpoint: true},
	} {
		for _, lspErrors := range []int{0, 1} {
			t.Run(tc.name+strings.Repeat(" with static deduction", lspErrors), func(t *testing.T) {
				client := &fakeLLM{text: tc.content, err: tc.err, noEndpoint: tc.noEndpoint}
				var recorded []int
				m := NewManager(t.TempDir())
				m.SetReviewLLM(client, func(pid, mid string, score int, pass bool, summary string) {
					if pid != "worker" || mid != "model" {
						t.Fatalf("audit must be recorded for the worker model, got %s/%s", pid, mid)
					}
					recorded = append(recorded, score)
				})
				rec := postJSON(t, m.HandleAudit, auditCodeRequest{
					WorkerProviderID: "worker", WorkerModelID: "model",
					ObserverProviderID: "observer", ObserverModelID: "model",
					LspErrorCount: lspErrors,
				})
				var result auditCodeResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if rec.Code != http.StatusOK || result.OK != tc.ok {
					t.Fatalf("unexpected audit response: %d %s", rec.Code, rec.Body.String())
				}
				if !tc.ok {
					if result.Error == "" || len(recorded) != 0 {
						t.Fatalf("failed observer must return an error and record nothing: %s, %v", rec.Body.String(), recorded)
					}
					return
				}
				if result.Score != 0 || result.Pass || len(recorded) != 1 || recorded[0] != 0 {
					t.Fatalf("zero score was not recorded correctly: %+v, %v", result, recorded)
				}
				if client.lastRequest.Purpose != llm.PurposeReview || !strings.Contains(client.lastRequest.Prompt, "Senior Code Auditor") {
					t.Fatalf("observer prompt must use the review profile: %+v", client.lastRequest)
				}
			})
		}
	}
}

func TestHandleAuditWithoutClientOrModel(t *testing.T) {
	m := NewManager(t.TempDir())
	rec := postJSON(t, m.HandleAudit, auditCodeRequest{ObserverProviderID: "observer", ObserverModelID: "model"})
	if !strings.Contains(rec.Body.String(), "Observer model is not configured") {
		t.Fatalf("missing client must report an unconfigured observer: %s", rec.Body.String())
	}
	m.SetReviewLLM(&fakeLLM{}, nil)
	rec = postJSON(t, m.HandleAudit, auditCodeRequest{})
	if !strings.Contains(rec.Body.String(), "Observer model is not configured") {
		t.Fatalf("missing observer model must be rejected: %s", rec.Body.String())
	}
}

func TestHandleGenerateCommitMessage(t *testing.T) {
	m := NewManager(t.TempDir())
	client := &fakeLLM{text: "<think>x</think>\nfeat(git): add one-click commit"}
	m.SetReviewLLM(client, nil)
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
