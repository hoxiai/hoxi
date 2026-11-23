package agent

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

type mcpRoundTripFunc func(*http.Request) (*http.Response, error)

func (f mcpRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func mcpJSONResp(status int, headers map[string]string, body string) *http.Response {
	h := make(http.Header)
	h.Set("Content-Type", "application/json")
	for k, v := range headers {
		h.Set(k, v)
	}
	return &http.Response{
		StatusCode: status,
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestRemoteMCPStreamableHTTPSessionReinitAndOAuth(t *testing.T) {
	homeDir := t.TempDir()
	wsDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("HOXI_HOME", homeDir)

	// Seed stored MCP OAuth token in EngineAuthPath().
	authPath := hoxipaths.EngineAuthPath()
	if err := os.MkdirAll(filepath.Dir(authPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authPath, []byte(`{
  "mcp:cloud-docs": {"type":"oauth","access":"mcp_oauth_tok_ok"},
  "mcp:revoked-srv": {"type":"oauth","revoked":true,"revokedReason":"token revoked by user"}
}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var methodsSeen []string
	sessionGen := 0
	activeSession := ""
	evictNextCall := false

	SetMCPHTTPTransportForTest(mcpRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "unauthed.mcp.test" {
			return mcpJSONResp(http.StatusUnauthorized, map[string]string{
				"WWW-Authenticate": `Bearer realm="mcp"`,
			}, `{"error":"unauthorized"}`), nil
		}
		if gotAuth := r.Header.Get("Authorization"); gotAuth != "Bearer mcp_oauth_tok_ok" {
			return mcpJSONResp(http.StatusUnauthorized, nil, `{"error":"missing_bearer"}`), nil
		}

		raw, _ := io.ReadAll(r.Body)
		var rpc struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal(raw, &rpc)

		mu.Lock()
		defer mu.Unlock()
		methodsSeen = append(methodsSeen, rpc.Method)

		switch rpc.Method {
		case "initialize":
			sessionGen++
			activeSession = "mcp-sess-" + string(rune('0'+sessionGen))
			return mcpJSONResp(http.StatusOK, map[string]string{
				"Mcp-Session-Id": activeSession,
			}, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-03-26","capabilities":{"tools":{}}}}`), nil

		case "notifications/initialized":
			if r.Header.Get("Mcp-Session-Id") != activeSession || r.Header.Get("MCP-Protocol-Version") != "2025-03-26" {
				return mcpJSONResp(http.StatusBadRequest, nil, `{"error":"missing session or protocol header"}`), nil
			}
			return mcpJSONResp(http.StatusAccepted, nil, ``), nil

		case "tools/list":
			if r.Header.Get("Mcp-Session-Id") != activeSession {
				return mcpJSONResp(http.StatusBadRequest, nil, `{"error":"invalid session id"}`), nil
			}
			return mcpJSONResp(http.StatusOK, nil, `{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"search_docs","description":"Search docs","inputSchema":{"type":"object"}}]}}`), nil

		case "tools/call":
			if evictNextCall {
				evictNextCall = false
				activeSession = ""
				return mcpJSONResp(http.StatusNotFound, nil, `{"error":"session expired"}`), nil
			}
			if r.Header.Get("Mcp-Session-Id") != activeSession {
				return mcpJSONResp(http.StatusNotFound, nil, `{"error":"unknown session"}`), nil
			}
			return mcpJSONResp(http.StatusOK, nil, `{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"doc_hit_42"}]}}`), nil
		}
		return mcpJSONResp(http.StatusBadRequest, nil, `{"error":"unexpected method"}`), nil
	}))
	defer SetMCPHTTPTransportForTest(nil)

	tc := ToolContext{
		WorktreeDir: wsDir,
		MCPConfig: func() map[string]any {
			return map[string]any{
				"mcp": map[string]any{
					"cloud-docs": map[string]any{
						"type":    "remote",
						"url":     "https://docs.mcp.test/mcp",
						"enabled": true,
					},
					"revoked-srv": map[string]any{
						"type":    "remote",
						"url":     "https://revoked.mcp.test/mcp",
						"enabled": true,
					},
					"unauthed-srv": map[string]any{
						"type":    "remote",
						"url":     "https://unauthed.mcp.test/mcp",
						"enabled": true,
					},
				},
			}
		},
	}

	// 1. tools/list runs initialize + notifications/initialized + tools/list with stored OAuth Bearer!
	listTool := &mcpListToolsTool{}
	listOut, err := listTool.Execute(context.Background(), tc, json.RawMessage(`{"server":"cloud-docs"}`))
	if err != nil || !strings.Contains(listOut.Output, "search_docs") {
		t.Fatalf("mcp_list_tools failed: out=%+v err=%v", listOut, err)
	}

	// 2. Subsequent tools/call reuses cached Mcp-Session-Id without redundant initialize;
	// when server returns 404 (session evicted after server restart), client transparently re-initializes!
	mu.Lock()
	evictNextCall = true
	mu.Unlock()

	callTool := &mcpCallTool{}
	callOut, err := callTool.Execute(context.Background(), tc, json.RawMessage(`{"server":"cloud-docs","tool":"search_docs","arguments":{"q":"oauth"}}`))
	if err != nil || !strings.Contains(callOut.Output, "doc_hit_42") {
		t.Fatalf("mcp_call_tool across 404 session re-init failed: out=%+v err=%v", callOut, err)
	}

	mu.Lock()
	gotGen := sessionGen
	gotMethods := append([]string(nil), methodsSeen...)
	mu.Unlock()
	if gotGen != 2 {
		t.Fatalf("expected 2 session initializations (initial + 404 recovery), got %d (methods=%v)", gotGen, gotMethods)
	}

	// 3. Revoked local OAuth credential surfaces explicit re-authorization message!
	revOut, revErr := callTool.Execute(context.Background(), tc, json.RawMessage(`{"server":"revoked-srv","tool":"ping"}`))
	if revErr == nil || !strings.Contains(revErr.Error(), "revoked") {
		t.Fatalf("expected explicit revoked MCP OAuth error, got out=%+v err=%v", revOut, revErr)
	}

	// 4. Remote 401 surfaces explicit OAuth authorization / reconnect diagnostic!
	_, unauthErr := callTool.Execute(context.Background(), tc, json.RawMessage(`{"server":"unauthed-srv","tool":"ping"}`))
	if unauthErr == nil || !strings.Contains(unauthErr.Error(), "requires OAuth authorization") {
		t.Fatalf("expected explicit 401 MCP OAuth diagnostic, got %v", unauthErr)
	}
}

func TestRemoteMCPSSRFBlocksMetadataBehindHTTPProxyAndTrailingDot(t *testing.T) {
	for _, raw := range []string{
		"http://metadata.google.internal./mcp",
		"http://metadata.goog./mcp",
		"http://./mcp",
	} {
		if _, err := validateRemoteMCPURL(raw); err == nil {
			t.Fatalf("expected validateRemoteMCPURL(%q) to block metadata/empty host", raw)
		}
	}

	var proxyHits atomic.Int32
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer proxySrv.Close()

	t.Setenv("HTTP_PROXY", proxySrv.URL)
	t.Setenv("HTTPS_PROXY", proxySrv.URL)
	t.Setenv("NO_PROXY", "")

	origLookup := lookupMCPHostIPs
	t.Cleanup(func() { lookupMCPHostIPs = origLookup })

	targetURL, _ := url.Parse("http://mcp-metadata.example/mcp")
	client := newSafeMCPHTTPClient(5*time.Second, targetURL, nil)
	for _, blockedIP := range []string{"169.254.169.254", "0.0.0.0"} {
		lookupMCPHostIPs = func(_ context.Context, _ string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP(blockedIP)}}, nil
		}
		resp, err := client.Get(targetURL.String())
		if resp != nil {
			_ = resp.Body.Close()
		}
		if err == nil || !strings.Contains(err.Error(), "blocked MCP connection to IP") {
			t.Fatalf("expected MCP SSRF block for resolved IP %s behind proxy, got err=%v", blockedIP, err)
		}
	}
	if hits := proxyHits.Load(); hits != 0 {
		t.Fatalf("expected 0 requests forwarded to HTTP proxy for blocked MCP targets, got %d", hits)
	}
}
