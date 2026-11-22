package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/codeindex"
	"github.com/hoxiai/hoxi/internal/llm"
	"github.com/hoxiai/hoxi/internal/runner"
)

type stubGatewayNativeStream struct {
	step int
}

func (s *stubGatewayNativeStream) ResolveStreamTarget(providerID, modelID string) llm.StreamTarget {
	return llm.StreamTarget{
		ProviderID:   providerID,
		ModelID:      modelID,
		BaseURL:      "https://api.openai.com/v1",
		Protocol:     llm.ProtocolOpenAI,
		HasEndpoint:  true,
		ContextLimit: 128000,
		OutputLimit:  4096,
	}
}

func (s *stubGatewayNativeStream) OpenStream(_ context.Context, _ llm.StreamHTTPRequest) (*http.Response, error) {
	s.step++
	var sse string
	switch s.step {
	case 1:
		sse = `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"r1","function":{"name":"read","arguments":"{\"filePath\":\"svc.go\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\ndata: [DONE]\n\n"
	case 2:
		sse = `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"e1","function":{"name":"edit","arguments":"{\"filePath\":\"svc.go\",\"oldString\":\"OldHandler\",\"newString\":\"FreshHandler\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\ndata: [DONE]\n\n"
	default:
		sse = `data: {"choices":[{"delta":{"content":"Renamed handler."},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(sse)),
		Header:     make(http.Header),
	}, nil
}

func TestGatewayNativeEditImmediatelyInvalidatesCodeIndexAndWiresRepoMap(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "svc.go"), []byte("package svc\n\nfunc OldHandler() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	idx := codeindex.NewService(t.TempDir())
	// Warm the 60s cache before the agent edits svc.go.
	before, err := idx.Search(ws, "OldHandler", 5)
	if err != nil || len(before.Results) == 0 {
		t.Fatalf("expected OldHandler in initial index, err=%v res=%+v", err, before.Results)
	}

	searchHook, inspectHook, impactHook := buildNativeCodeIndexHooks(idx)
	native := newGatewayNativeAgent(&stubGatewayNativeStream{}, searchHook, inspectHook, impactHook, nil, nil)
	native.repoMap = buildNativeRepoMapHook(idx)
	native.codeIndexDirty = idx.MarkDirty

	// Verify buildNativeRepoMapHook works directly.
	rmapOut, err := native.repoMap(context.Background(), ws, "", "", 10)
	if err != nil || !strings.Contains(rmapOut, "svc.go") || !strings.Contains(rmapOut, "OldHandler") {
		t.Fatalf("unexpected repoMap output: err=%v out=%q", err, rmapOut)
	}

	var recordedPaths []string
	if err := native.RunTurn(context.Background(), runner.NativeTurnRequest{
		SessionID:   "ses_gw_dirty_idx",
		WorktreeDir: ws,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Rename OldHandler to FreshHandler",
		RecordFileEdit: func(paths []string) {
			recordedPaths = append(recordedPaths, paths...)
		},
	}); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if len(recordedPaths) == 0 {
		t.Fatal("expected original RecordFileEdit hook to still be called")
	}

	// Immediately query codeIndex within the same second: FreshHandler must be indexed right away!
	after, err := idx.Search(ws, "FreshHandler", 5)
	if err != nil || len(after.Results) == 0 {
		t.Fatalf("expected codeIndex to see FreshHandler immediately after native edit, err=%v res=%+v", err, after.Results)
	}
	// Verify repoMap also sees FreshHandler immediately.
	rmapAfter, err := native.repoMap(context.Background(), ws, "", "", 10)
	if err != nil || !strings.Contains(rmapAfter, "FreshHandler") {
		t.Fatalf("expected repoMap to see FreshHandler immediately, err=%v out=%q", err, rmapAfter)
	}
	_ = json.RawMessage(nil)
}
