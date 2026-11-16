package gateway

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/diagnostics"
	"github.com/hoxiai/hoxi/internal/runner"
)

func diagnosticEntries(t *testing.T, session string) []diagnostics.Entry {
	t.Helper()
	w := httptest.NewRecorder()
	diagnostics.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/hoxi/diagnostics?sessionID="+session, nil))
	var body struct {
		Entries []diagnostics.Entry `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	return body.Entries
}

func TestOpenCodeProxyErrorDiagnostic(t *testing.T) {
	const session = "ses_proxy_diagnostic"
	stream := "data: {\"type\":\"session.error\",\"properties\":{\"sessionID\":\"" + session + "\",\"error\":{\"name\":\"APIError\",\"data\":{\"statusCode\":429,\"providerID\":\"test-provider\",\"modelID\":\"test-model\",\"message\":\"PRIVATE_ERROR_BODY\"}}}}\n\n"
	requests := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, stream)
	}))
	defer upstream.Close()
	_, port, err := net.SplitHostPort(upstream.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	n, _ := strconv.Atoi(port)
	g := New(n, "password", t.TempDir(), nil, nil)
	defer g.CloseBrowser()
	w := httptest.NewRecorder()
	g.proxy.ServeHTTP(w, httptest.NewRequest("GET", "/event", nil))
	if w.Code != 200 || w.Body.String() != stream || requests != 1 {
		t.Fatalf("stream changed or duplicate requests: %d %d %s", w.Code, requests, w.Body.String())
	}
	entries := diagnosticEntries(t, session)
	if len(entries) != 1 {
		t.Fatalf("entries: %+v", entries)
	}
	e := entries[0]
	if e.Kind != "request" || e.Source != "opencode" || e.Message != "model provider rate limited" || e.Status != 429 || e.ProviderID != "test-provider" || e.ModelID != "test-model" {
		t.Fatalf("%+v", e)
	}
	// Verify the JSONL, not just the sanitized query response.
	files, err := filepath.Glob(filepath.Join(os.Getenv("HOME"), ".hoxi", "logs", "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "PRIVATE_ERROR_BODY") {
			t.Fatal("raw error body persisted")
		}
		if strings.Contains(string(b), session) {
			found = true
		}
	}
	if !found {
		t.Fatal("error not persisted to disk")
	}
}

func TestOpenCodeErrorTapFragmentsAndCancellation(t *testing.T) {
	for _, size := range []int{1, 7, 257} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			session := "ses_fragment_diagnostic_" + strconv.Itoa(size)
			failure := `{"type":"session.error","properties":{"sessionID":"` + session + `","providerID":"p","modelID":"m","error":{"name":"APIError","data":{"statusCode":503,"message":"PRIVATE_PROMPT"}}}}`
			stream := "data: " + strings.Repeat("x", maxSSETapLineSize+1) + "\n" +
				`data: {"type":"message.part.delta","properties":{"sessionID":"` + session + `","delta":"session.error PRIVATE_TOKEN"}}` + "\n\n" +
				`data: {"payload":` + failure + "}\r\n\n"
			for _, name := range []string{"AbortError", "MessageAbortedError"} {
				stream += `data: {"type":"session.error","properties":{"sessionID":"` + session + `","error":{"name":"` + name + `","data":{"statusCode":500}}}}` + "\n\n"
			}
			// A truncated last line is not a complete SSE event.
			stream += "data: " + failure
			tap := newSSETapReadCloser(io.NopCloser(strings.NewReader(stream)), inspectOpenCodeError)
			defer tap.Close()
			var out strings.Builder
			buf := make([]byte, size)
			for {
				n, err := tap.Read(buf)
				out.Write(buf[:n])
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if out.String() != stream {
				t.Fatal("tap changed streaming bytes")
			}
			entries := diagnosticEntries(t, session)
			if len(entries) != 1 || entries[0].Status != 503 || entries[0].ProviderID != "p" || entries[0].ModelID != "m" {
				t.Fatalf("%+v", entries)
			}
			b, _ := json.Marshal(entries)
			if strings.Contains(string(b), "PRIVATE") {
				t.Fatal("content captured")
			}
		})
	}
}

func TestOpenCodeManagedErrorNotRecordedTwice(t *testing.T) {
	root := t.TempDir()
	svc := runner.NewService(root, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	if err := svc.Store().Put(runner.SessionRecord{ID: "ses_visible_diagnostic", Runtime: "opencode", ProjectDir: root, WorktreePath: root, OpenCodeIDs: []string{"ses_engine_diagnostic"}}); err != nil {
		t.Fatal(err)
	}
	svc.ConfigureOpenCode("http://127.0.0.1:1", "")
	stream := "data: " + `{"type":"session.error","properties":{"sessionID":"ses_engine_diagnostic","error":{"name":"APIError"}}}` + "\n\n"
	resp := &http.Response{Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream))}
	if err := filterRunnerResponse(resp, svc); err != nil {
		t.Fatal(err)
	}
	tap := newSSETapReadCloser(resp.Body, inspectOpenCodeError)
	defer tap.Close()
	if _, err := io.Copy(io.Discard, tap); err != nil {
		t.Fatal(err)
	}
	if entries := diagnosticEntries(t, "ses_engine_diagnostic"); len(entries) != 0 {
		t.Fatalf("engine event recorded twice: %+v", entries)
	}
}
