package agent_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/agent"
	"github.com/hoxiai/hoxi/internal/llm"
)

type codexInspectStreamClient struct {
	proto    llm.StreamProtocol
	baseURL  string
	lastReq  llm.StreamHTTPRequest
	response string
}

func (c *codexInspectStreamClient) ResolveStreamTarget(providerID, modelID string) llm.StreamTarget {
	return llm.StreamTarget{
		ProviderID:   providerID,
		ModelID:      modelID,
		BaseURL:      c.baseURL,
		Protocol:     c.proto,
		HasEndpoint:  true,
		ContextLimit: 400000,
		OutputLimit:  16384,
	}
}

func (c *codexInspectStreamClient) OpenStream(_ context.Context, req llm.StreamHTTPRequest) (*http.Response, error) {
	c.lastReq = req
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(c.response)),
		Header:     make(http.Header),
	}, nil
}

func TestOpenAIResponsesDriverAdaptsPayloadForCodexChatGPTBackend(t *testing.T) {
	sse := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"done\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":8,\"output_tokens\":2}}}\n\n"

	codexClient := &codexInspectStreamClient{
		proto:    llm.ProtocolOpenAIResponses,
		baseURL:  "https://chatgpt.com/backend-api/codex",
		response: sse,
	}
	eng := agent.NewDefaultEngine(codexClient)
	err := eng.RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_codex_oauth",
		WorktreeDir: t.TempDir(),
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-6.2-astra",
		Effort:      "high",
		UserBlocks:  []agent.ContentBlock{{Type: agent.ContentText, Text: "Fix local import"}},
	})
	if err != nil {
		t.Fatalf("Codex backend RunTurn failed: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(codexClient.lastReq.Body, &payload); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	if _, hasMax := payload["max_output_tokens"]; hasMax {
		t.Fatalf("Codex ChatGPT backend must not receive max_output_tokens: %s", string(codexClient.lastReq.Body))
	}
	instr, _ := payload["instructions"].(string)
	if strings.TrimSpace(instr) == "" {
		t.Fatalf("Codex ChatGPT backend requires non-empty instructions")
	}
	rMap, _ := payload["reasoning"].(map[string]any)
	if rMap["effort"] != "high" || rMap["summary"] != "auto" {
		t.Fatalf("expected reasoning effort=high summary=auto, got %+v", rMap)
	}
	inc, _ := payload["include"].([]any)
	if len(inc) == 0 || inc[0] != "reasoning.encrypted_content" {
		t.Fatalf("expected reasoning.encrypted_content include, got %+v", inc)
	}
	inp, _ := payload["input"].([]any)
	if len(inp) == 0 {
		t.Fatalf("expected structured input list")
	}
	firstMsg, _ := inp[0].(map[string]any)
	if firstMsg["type"] != "message" || firstMsg["role"] != "user" {
		t.Fatalf("expected structured message item for Codex backend, got %+v", firstMsg)
	}

	// Standard public OpenAI API (/v1/responses) must retain max_output_tokens
	publicClient := &codexInspectStreamClient{
		proto:    llm.ProtocolOpenAIResponses,
		baseURL:  "https://api.openai.com/v1",
		response: sse,
	}
	pubEng := agent.NewDefaultEngine(publicClient)
	if err := pubEng.RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_pub_oai",
		WorktreeDir: t.TempDir(),
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-6.2-astra",
		UserBlocks:  []agent.ContentBlock{{Type: agent.ContentText, Text: "Hello public API"}},
	}); err != nil {
		t.Fatalf("public OpenAI RunTurn failed: %v", err)
	}
	var pubPayload map[string]any
	_ = json.Unmarshal(publicClient.lastReq.Body, &pubPayload)
	if _, hasMax := pubPayload["max_output_tokens"]; !hasMax {
		t.Fatalf("public /v1/responses should retain max_output_tokens, got %s", string(publicClient.lastReq.Body))
	}
}
