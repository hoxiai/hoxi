package orchestration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hoxiai/hoxi/internal/llm"
)

type draftLLM struct {
	text       string
	noEndpoint bool
	calls      int
}

func (f *draftLLM) Resolve(providerID, modelID, baseURL, npm string) llm.Target {
	return llm.Target{ProviderID: providerID, ModelID: modelID, HasEndpoint: !f.noEndpoint}
}

func (f *draftLLM) Complete(_ context.Context, target llm.Target, _ llm.Request) (string, error) {
	f.calls++
	if !target.Ready() {
		return "", llm.ErrUnavailable
	}
	return f.text, nil
}

func postDraft(t *testing.T, svc *Service, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatalf("encode draft request: %v", err)
	}
	rec := httptest.NewRecorder()
	svc.HandleDraft(rec, httptest.NewRequest(http.MethodPost, "/api/hoxi/orchestration/draft", &buf))
	return rec
}

func TestDraftNormalizesModelPlanWithoutStartingIt(t *testing.T) {
	client := &draftLLM{text: "```json\n" + `{
		"title": "Shipping flow",
		"tasks": [
			{"id": "API Work", "prompt": "Implement the shipping API", "dependsOn": []},
			{"id": "UI work", "prompt": "Build the shipping UI", "dependsOn": ["API Work", "ghost", "UI work"]}
		]
	}` + "\n```"}
	svc := NewService(nil, WithLLM(client))
	rec := postDraft(t, svc, draftRequest{
		Directory: "/tmp/project", Goal: "Ship shipping", ProviderID: "p", ModelID: "m",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", rec.Code, rec.Body.String())
	}
	var draft PlanRequest
	if err := json.Unmarshal(rec.Body.Bytes(), &draft); err != nil {
		t.Fatalf("decode draft: %v", err)
	}
	if draft.Title != "Shipping flow" || draft.Provider != "p" || draft.Model != "m" || draft.Mode != "build" {
		t.Fatalf("unexpected draft metadata: %+v", draft)
	}
	if len(draft.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %+v", draft.Tasks)
	}
	if draft.Tasks[0].ID != "api-work" || draft.Tasks[1].ID != "ui-work" {
		t.Fatalf("task ids were not sanitized: %+v", draft.Tasks)
	}
	deps := draft.Tasks[1].DependsOn
	if len(deps) != 1 || deps[0] != "api-work" {
		t.Fatalf("dependencies were not remapped: %+v", deps)
	}
	if plans := svc.List(""); len(plans) != 0 {
		t.Fatalf("drafting must not start a plan: %+v", plans)
	}
}

func TestDraftRejectsUnavailableModel(t *testing.T) {
	client := &draftLLM{text: "{}", noEndpoint: true}
	svc := NewService(nil, WithLLM(client))
	rec := postDraft(t, svc, draftRequest{Directory: "/tmp/project", Goal: "Do work", ProviderID: "p", ModelID: "m"})
	if rec.Code != http.StatusBadRequest || client.calls != 0 {
		t.Fatalf("unreachable model must be rejected before calling it: %d %s", rec.Code, rec.Body.String())
	}
}

func TestDraftRejectsNonJSONAnswer(t *testing.T) {
	svc := NewService(nil, WithLLM(&draftLLM{text: "I would start by reading the files."}))
	rec := postDraft(t, svc, draftRequest{Directory: "/tmp/project", Goal: "Do work", ProviderID: "p", ModelID: "m"})
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("non-JSON answer should fail the draft: %d %s", rec.Code, rec.Body.String())
	}
}

func TestDraftRequiresGoal(t *testing.T) {
	svc := NewService(nil, WithLLM(&draftLLM{text: "{}"}))
	rec := postDraft(t, svc, draftRequest{Directory: "/tmp/project", Goal: "   ", ProviderID: "p", ModelID: "m"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty goal should be rejected: %d", rec.Code)
	}
}
