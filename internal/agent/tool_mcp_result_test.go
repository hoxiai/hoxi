package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/llm"
)

const richMCPResult = `{
  "content": [
    {"type":"text","text":"Chart analysis"},
    {"type":"image","mimeType":"image/png","data":"iVBORw0KGgo="},
    {"type":"audio","mimeType":"audio/wav","data":"UklGRg=="},
    {"type":"resource","resource":{"uri":"report://document","mimeType":"application/pdf","blob":"JVBERi0xLjQK"}},
    {"type":"resource","resource":{"uri":"report://notes","mimeType":"text/plain","text":"critical embedded detail"}},
    {"type":"resource_link","uri":"file:///unapproved/secret","name":"reference","description":"Never automatically read this"}
  ],
  "structuredContent":{"answer":42,"nested":{"status":"verified"}}
}`

func TestMCPResultPreservesStructuredResourcesAndMedia(t *testing.T) {
	out, err := decodeMCPToolResult("server:report", json.RawMessage(richMCPResult))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"answer":42`, "critical embedded detail", "file:///unapproved/secret", "not fetched", "Chart analysis"} {
		if !strings.Contains(out.Output, want) {
			t.Errorf("missing %s: %s", want, out.Output)
		}
	}
	if len(out.Media) != 3 || out.Media[0].MimeType != "image/png" || out.Media[1].MimeType != "audio/wav" || out.Media[2].MimeType != "application/pdf" {
		t.Fatalf("lost rich media: %+v", out.Media)
	}
	if string(out.Metadata["mcpResult"].(json.RawMessage)) != richMCPResult {
		t.Fatal("raw result was not retained")
	}
	for _, proto := range []llm.StreamProtocol{llm.ProtocolAnthropic, llm.ProtocolOpenAI, llm.ProtocolOpenAIResponses, llm.ProtocolGoogle} {
		drv, _ := newDriverFactory().Create(proto)
		blocks := append([]ContentBlock{{Type: ContentText, Text: out.Output}}, out.Media...)
		req, err := drv.BuildRequest(ModelRequest{
			Target: llm.StreamTarget{ModelID: "selected"}, MaxTokens: 4096,
			Messages: []Message{{Role: RoleUser, Content: blocks}},
		})
		if err != nil {
			t.Fatal(err)
		}
		body := string(req.Body)
		for _, want := range []string{"iVBORw0KGgo=", "JVBERi0xLjQK", "critical embedded detail"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s lost %s", proto, want)
			}
		}
		if proto == llm.ProtocolGoogle {
			if !strings.Contains(body, "UklGRg==") {
				t.Fatal("Gemini lost audio content")
			}
		} else if strings.Contains(body, "UklGRg==") || !strings.Contains(body, "contents have not been inspected") {
			t.Errorf("%s must report unsupported audio, not mislabel it as an image: %s", proto, body)
		}
	}
}

func TestMCPResultErrorsAreNotHiddenByEmptyContent(t *testing.T) {
	for _, raw := range []string{
		`{"isError":true,"content":[]}`,
		`{"isError":true,"structuredContent":{"reason":"revoked"}}`,
		`{"content":[{"type":"image","mimeType":"image/png","data":"invalid!!!"}]}`,
		`{"content":[{"type":"audio","data":"UklGRg=="}]}`,
		`{"content":[{"type":"resource","resource":{"uri":"file:///unapproved/secret"}}]}`,
		`null`, `not json`,
	} {
		out, err := decodeMCPToolResult("test", json.RawMessage(raw))
		if err == nil || !out.IsError || strings.TrimSpace(out.Output) == "" {
			t.Fatalf("invalid/error result treated as success: %s => %+v, %v", raw, out, err)
		}
	}
	for _, raw := range []string{
		`{"structuredContent":{"answer":42}}`,
		`{"content":[{"type":"future_block","payload":{"answer":42}}]}`,
	} {
		out, err := decodeMCPToolResult("test", json.RawMessage(raw))
		if err != nil || !strings.Contains(out.Output, `"answer":42`) {
			t.Fatalf("lost structured/future result: %+v %v", out, err)
		}
	}
}

type mcpResultStreamClient struct{ calls [][]byte }

func (*mcpResultStreamClient) ResolveStreamTarget(p, m string) llm.StreamTarget {
	return llm.StreamTarget{ProviderID: p, ModelID: m, Protocol: llm.ProtocolOpenAI, HasEndpoint: true}
}
func (c *mcpResultStreamClient) OpenStream(_ context.Context, req llm.StreamHTTPRequest) (*http.Response, error) {
	c.calls = append(c.calls, req.Body)
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"Inspected report\"}}]}\n\ndata: [DONE]\n\n"
	if len(c.calls) == 1 {
		sse = `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"mcp-1","function":{"name":"mcp_call_tool","arguments":"{\"server\":\"reports\",\"tool\":\"report\"}"}}]}}]}` + "\n\ndata: [DONE]\n\n"
	}
	return &http.Response{Body: io.NopCloser(strings.NewReader(sse))}, nil
}

func TestMCPMediaReachesNextModelStepAndToolEvent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HOXI_HOME", t.TempDir())
	SetMCPHTTPTransportForTest(mcpRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "reports.test" {
			t.Errorf("unexpected resource fetch: %s", r.URL)
		}
		var rpc struct{ Method string }
		if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil {
			return nil, err
		}
		switch rpc.Method {
		case "initialize":
			return mcpJSONResp(200, nil, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-03-26"}}`), nil
		case "notifications/initialized":
			return mcpJSONResp(202, nil, ""), nil
		case "tools/call":
			return mcpJSONResp(200, nil, `{"jsonrpc":"2.0","id":2,"result":`+richMCPResult+`}`), nil
		default:
			return nil, fmt.Errorf("unexpected MCP method %s", rpc.Method)
		}
	}))
	defer SetMCPHTTPTransportForTest(nil)
	c := &mcpResultStreamClient{}
	var completed EngineEvent
	approvals := 0
	err := NewDefaultEngine(c).RunTurn(context.Background(), TurnRequest{
		WorktreeDir: t.TempDir(), ProviderID: "selected", ModelID: "selected", UserPrompt: "Read report",
		MCPConfig: func() map[string]any {
			return map[string]any{"mcp": map[string]any{"reports": map[string]any{"type": "remote", "url": "https://reports.test/mcp"}}}
		},
		PermissionHook: func(_ context.Context, req PermissionRequest) error {
			if req.ToolName == "mcp_call_tool" {
				approvals++
			}
			return nil
		},
		Sink: EventSinkFunc(func(evt EngineEvent) {
			if evt.Kind == EngineToolCompleted {
				completed = evt
			}
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.calls) != 2 || approvals != 1 || len(completed.Media) != 3 || completed.Metadata["mcpResult"] == nil {
		t.Fatalf("lost tool result or approval: calls=%d approvals=%d media=%d", len(c.calls), approvals, len(completed.Media))
	}
	for _, want := range []string{"iVBORw0KGgo=", "JVBERi0xLjQK", "critical embedded detail", "structuredContent"} {
		// structuredContent is rendered as a JSON result rather than its envelope.
		if want == "structuredContent" {
			want = `\"answer\":42`
		}
		if !strings.Contains(string(c.calls[1]), want) {
			t.Errorf("next model request missing %s", want)
		}
	}
}
