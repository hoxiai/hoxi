package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/llm"
)

func geminiCacheTestRequest() llm.StreamHTTPRequest {
	body, _ := json.Marshal(map[string]any{
		"systemInstruction": map[string]any{"parts": []any{map[string]string{"text": strings.Repeat("stable definition ", 8500)}}},
		"tools":             []any{map[string]any{"functionDeclarations": []any{map[string]string{"name": "read"}}}},
		"contents":          []any{map[string]any{"role": "user", "parts": []any{map[string]string{"text": "old"}}}, map[string]any{"role": "model", "parts": []any{map[string]string{"text": "answer"}}}, map[string]any{"role": "user", "parts": []any{map[string]string{"text": "new turn"}}}},
		"generationConfig":  map[string]int{"maxOutputTokens": 4096},
	})
	return llm.StreamHTTPRequest{Target: llm.StreamTarget{ProviderID: "google", ModelID: "gemini-2.5-pro", BaseURL: "https://generativelanguage.googleapis.com/v1beta", NPM: "@ai-sdk/google", Protocol: llm.ProtocolGoogle, HasEndpoint: true}, Body: body, SessionID: "session"}
}

func TestGeminiExplicitCacheTransportAndFullRequestRecovery(t *testing.T) {
	_, dir := setupIsolatedEnv(t)
	svc := NewService(0, "", dir, nil)
	req := geminiCacheTestRequest()
	creates, delta, full := 0, 0, 0
	SetStreamTransportForTest(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "generativelanguage.googleapis.com" {
			t.Fatalf("unexpected destination: %s", r.URL.Host)
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]json.RawMessage
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		status := 200
		result := "data: [DONE]\n\n"
		switch r.URL.Path {
		case "/v1beta/cachedContents":
			creates++
			if r.Method != "POST" || string(body["ttl"]) != "\"720s\"" || body["systemInstruction"] == nil || body["tools"] == nil || !strings.Contains(string(body["contents"]), "old") || strings.Contains(string(raw), "new turn") {
				t.Fatal("invalid cached prefix")
			}
			result = fmt.Sprintf(`{"name":"cachedContents/test","expireTime":%q}`, time.Now().Add(12*time.Minute).Format(time.RFC3339Nano))
		case "/v1beta/models/gemini-2.5-pro:streamGenerateContent":
			if body["cachedContent"] != nil {
				delta++
				if string(body["cachedContent"]) != "\"cachedContents/test\"" || body["generationConfig"] == nil || body["tools"] != nil || body["systemInstruction"] != nil || strings.Contains(string(body["contents"]), "old") || !strings.Contains(string(body["contents"]), "new turn") {
					t.Fatal("cache hit sent wrong suffix")
				}
				if delta == 2 {
					status = 404
					result = `{"error":{"message":"CachedContent not found"}}`
				}
			} else {
				full++
				if string(raw) != string(req.Body) {
					t.Fatal("fallback lost the original request")
				}
			}
		default:
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(result))}, nil
	}))
	t.Cleanup(func() { SetStreamTransportForTest(nil) })
	for range 3 {
		resp, err := svc.OpenStream(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	if creates != 1 || delta != 2 || full != 2 {
		t.Fatalf("create=%d delta=%d full=%d", creates, delta, full)
	}
}

func TestGeminiCachePrefixRenewalAndIsolation(t *testing.T) {
	var cache geminiContextCache
	req := geminiCacheTestRequest()
	now := time.Now()
	calls := 0
	send := func(_ context.Context, method, path string, raw []byte) (*http.Response, error) {
		calls++
		if calls == 2 && (method != http.MethodPatch || path != "cachedContents/ref?updateMask=ttl" || string(raw) != `{"ttl":"720s"}`) {
			t.Fatal("renewal resent context")
		}
		body := fmt.Sprintf(`{"name":"cachedContents/ref","expireTime":%q}`, now.Add(12*time.Minute).Format(time.RFC3339Nano))
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	}
	key := geminiCacheIdentity(req, req.Target.BaseURL, "key", nil)
	if _, name := cache.prepare(context.Background(), key, req, now, send); name == "" {
		t.Fatal("cache not created")
	}
	changed := req
	changed.Body = []byte(strings.Replace(string(req.Body), "definition", "correction", 1))
	if out, name := cache.prepare(context.Background(), key, changed, now, send); name != "" || string(out) != string(changed.Body) {
		t.Fatal("reused changed prefix")
	}
	now = now.Add(11 * time.Minute)
	if _, name := cache.prepare(context.Background(), key, req, now, send); name == "" || calls != 2 {
		t.Fatal("cache not renewed")
	}
	if geminiCacheIdentity(req, req.Target.BaseURL, "different", nil) == key {
		t.Fatal("different credentials share cache")
	}
	for _, base := range []string{"https://proxy.example", "https://us-aiplatform.googleapis.com", "http://generativelanguage.googleapis.com", "https://generativelanguage.googleapis.com/proxy"} {
		if allowGeminiContextCache(req, base) {
			t.Fatalf("accepted %q", base)
		}
	}
	for _, model := range []string{"gemini-4-carbon", "other"} {
		r := req
		r.Target.ModelID = model
		if allowGeminiContextCache(r, r.Target.BaseURL) {
			t.Fatal(model)
		}
	}
	req.Target.DisableGeminiExplicitCache = true
	if allowGeminiContextCache(req, req.Target.BaseURL) {
		t.Fatal("ignored disabled caching")
	}
}

func TestGeminiCacheParallelRequestsShareOneCreation(t *testing.T) {
	var cache geminiContextCache
	req := geminiCacheTestRequest()
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan string, 1)
	now := time.Now()
	go func() {
		_, name := cache.prepare(context.Background(), "same", req, now, func(context.Context, string, string, []byte) (*http.Response, error) {
			close(started)
			<-release
			body := fmt.Sprintf(`{"name":"cachedContents/ref","expireTime":%q}`, now.Add(12*time.Minute).Format(time.RFC3339Nano))
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})
		done <- name
	}()
	<-started
	concurrentCall := false
	out, name := cache.prepare(context.Background(), "same", req, now, func(context.Context, string, string, []byte) (*http.Response, error) {
		concurrentCall = true
		return nil, nil
	})
	close(release)
	first := <-done
	if concurrentCall || name != "" || string(out) != string(req.Body) || first != "cachedContents/ref" {
		t.Fatal("concurrent cache creation or dropped original request")
	}
	denied := &http.Response{StatusCode: 403, Body: io.NopCloser(strings.NewReader("invalid credentials"))}
	if geminiCacheRejected(denied) {
		t.Fatal("unrelated 403 retried")
	}
	raw, _ := io.ReadAll(denied.Body)
	if string(raw) != "invalid credentials" {
		t.Fatal("lost original error")
	}
}
