package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cdpbrowser "github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	cdpruntime "github.com/chromedp/cdproto/runtime"
)

func workflowHTTP(t *testing.T, s *Service, dir, session, path, method, contentType string, body io.Reader, agent bool) *httptest.ResponseRecorder {
	t.Helper()
	parts := strings.SplitN(path, "?", 2)
	address := "http://127.0.0.1/api/hoxi/browser/" + parts[0] + "?directory=" + url.QueryEscape(dir) + "&sessionID=" + session
	if len(parts) == 2 {
		address += "&" + parts[1]
	}
	req := httptest.NewRequest(method, address, body)
	if agent {
		req.Header.Set("X-Hoxi-Agent", "true")
		req.Header.Set("Authorization", "Bearer secret")
	} else {
		req.Header.Set("Origin", "wails://wails")
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func TestWorkflowBoundariesAndDownloadBudgets(t *testing.T) {
	s := New()
	defer s.Close()
	s.SetAgentToken("secret")
	dir := t.TempDir()
	for _, path := range []string{"workflows", "diagnostics", "upload", "download-save", "download-open", "download-reveal", "download-cancel"} {
		for _, method := range []string{"GET", "POST"} {
			if w := workflowHTTP(t, s, dir, "files", path, method, "application/json", strings.NewReader(`{}`), true); w.Code != 403 {
				t.Fatal("agent workflow accepted", path, w.Code)
			}
		}
	}
	if len(s.scopes) != 0 {
		t.Fatal("workflow read started browser")
	}
	p := &workflowTracker{uploads: make(map[string]UploadRequest), dialogs: make(map[string]PageDialog)}
	p.receivePage("tab", &page.EventFileChooserOpened{FrameID: "child", BackendNodeID: 10, Mode: page.FileChooserOpenedModeSelectMultiple})
	if p.snapshot("other").Upload != nil || !p.snapshot("tab").Upload.Multiple {
		t.Fatal("file chooser leaked across tabs")
	}
	p.receivePage("tab", &page.EventFrameNavigated{Frame: &cdp.Frame{ID: "child", ParentID: "main"}})
	if p.snapshot("tab").Upload != nil {
		t.Fatal("child navigation kept stale file request")
	}
	guid := "12345678-1234-1234-1234-123456789012"
	p.receiveDownload(&cdpbrowser.EventDownloadWillBegin{GUID: guid, SuggestedFilename: "../../unsafe.txt"})
	if p.downloads[0].Filename != "unsafe.txt" {
		t.Fatal(p.downloads)
	}
	if p.receiveDownload(&cdpbrowser.EventDownloadProgress{GUID: guid, TotalBytes: maxDownloadBytes + 1}) != guid || p.downloads[0].Error != "download_limit" {
		t.Fatal("oversized download not canceled")
	}
	if safeDocument("/tmp/script.command") || safeDocument("/tmp/picture.svg") || !safeDocument("/tmp/note.txt") {
		t.Fatal("unsafe document opener")
	}
	source := filepath.Join(t.TempDir(), "source")
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(source, []byte("saved bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if saveDownloadedFile(ctx, source, target) == nil {
		t.Fatal("canceled save succeeded")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("canceled save created file")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}
	if saveDownloadedFile(context.Background(), link, target) == nil || saveDownloadedFile(context.Background(), source, link) == nil {
		t.Fatal("symlink download accepted")
	}
}

func TestBrowserDiagnosticsRedactionAndBounds(t *testing.T) {
	d := &diagnosticTracker{requests: make(map[network.RequestID]string)}
	d.receive(&network.EventRequestWillBeSent{RequestID: "one", Request: &network.Request{URL: "https://user:password@example.com/api?token=secret#private"}})
	d.receive(&network.EventLoadingFailed{RequestID: "one", ErrorText: "private page text net::ERR_CONNECTION_REFUSED"})
	rows := d.snapshot(false)
	if len(rows) != 1 || rows[0].URL != "https://example.com/api" || rows[0].Message != "net::ERR_CONNECTION_REFUSED" {
		t.Fatal(rows)
	}
	for i := 0; i < 120; i++ {
		d.receive(&cdpruntime.EventConsoleAPICalled{Type: cdpruntime.APITypeError, Args: []*cdpruntime.RemoteObject{{Type: cdpruntime.TypeString, Value: []byte(`"fixture error"`)}}})
	}
	if len(d.snapshot(false)) != 100 || len(d.requests) != 0 || len(d.snapshot(true)) != 0 {
		t.Fatal("unbounded diagnostics")
	}
}

func TestChromeFileUploadDownloadDialogsAndDiagnostics(t *testing.T) {
	if os.Getenv("HOXI_BROWSER_TEST") != "1" {
		t.Skip("set HOXI_BROWSER_TEST=1 for Chrome integration")
	}
	s := New()
	defer s.Close()
	s.SetAgentToken("secret")
	dir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/file" {
			w.Header().Set("Content-Disposition", `attachment; filename="note.txt"`)
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("download bytes"))
			return
		}
		if r.URL.Path == "/missing" {
			http.Error(w, "missing", 404)
			return
		}
		_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8"><input id="files" type="file" multiple><a id="download" href="/file" download>Download</a><button id="prompt" onclick="window.answer=prompt('Your name?','default')">Prompt</button><script>console.error('fixture console error');fetch('/missing?token=private')</script>`))
	}))
	defer server.Close()
	call := func(action Action, agent bool) map[string]any {
		t.Helper()
		w := request(t, s, dir, "files", "action", string(mustJSON(t, action)), agent)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", action.Action, w.Code, w.Body.String())
		}
		var out map[string]any
		if json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatal(w.Body.String())
		}
		return out
	}
	initial := call(Action{Action: "navigate", URL: server.URL}, false)
	workflowsPath := "workflows?tabID=" + url.QueryEscape(initial["tabID"].(string))
	call(Action{Action: "click", Selector: "#files"}, false)
	var state WorkflowState
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		w := workflowHTTP(t, s, dir, "files", workflowsPath, "GET", "", nil, false)
		if json.Unmarshal(w.Body.Bytes(), &state) == nil && state.Upload != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if state.Upload == nil || !state.Upload.Multiple {
		t.Fatal("no file chooser", state, call(Action{Action: "evaluate", Text: `({active:document.activeElement.id,input:document.querySelector("#files").outerHTML})`}, true))
	}
	id := state.Upload.ID
	form := func(id string) (*bytes.Buffer, string) {
		b := new(bytes.Buffer)
		writer := multipart.NewWriter(b)
		_ = writer.WriteField("id", id)
		part, _ := writer.CreateFormFile("files", "selected.txt")
		_, _ = part.Write([]byte("explicitly selected bytes"))
		_ = writer.Close()
		return b, writer.FormDataContentType()
	}
	b, kind := form(id)
	if w := workflowHTTP(t, s, dir, "files", "upload", "POST", kind, b, false); w.Code != 409 {
		t.Fatal("upload without manual control accepted", w.Code, w.Body.String())
	}
	manual := true
	call(Action{Action: "control", Manual: &manual}, false)
	b, kind = form("stale")
	if w := workflowHTTP(t, s, dir, "files", "upload", "POST", kind, b, false); w.Code != 400 {
		t.Fatal("stale upload accepted", w.Code, w.Body.String())
	}
	b, kind = form(id)
	if w := workflowHTTP(t, s, dir, "files", "upload", "POST", kind, b, false); w.Code != 200 {
		t.Fatal("upload failed", w.Code, w.Body.String())
	}
	manual = false
	call(Action{Action: "control", Manual: &manual}, false)
	if got := call(Action{Action: "evaluate", Text: `({name:files.files[0].name,size:files.files[0].size})`}, true)["result"]; got != `{"name":"selected.txt","size":25}` {
		t.Fatal("selected file mismatch", got)
	}
	call(Action{Action: "click", Selector: "#download"}, false)
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		w := workflowHTTP(t, s, dir, "files", workflowsPath, "GET", "", nil, false)
		_ = json.Unmarshal(w.Body.Bytes(), &state)
		if len(state.Downloads) > 0 && state.Downloads[0].Status == "completed" {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if len(state.Downloads) != 1 || state.Downloads[0].Status != "completed" {
		t.Fatal("download did not complete", state)
	}
	destination := filepath.Join(t.TempDir(), "saved.txt")
	opened := ""
	s.SetFileUI(func(name, title string) (string, error) {
		if name != "note.txt" {
			t.Fatal(name)
		}
		return destination, nil
	}, func(path string, reveal bool) error { opened = path; return nil })
	data := string(mustJSON(t, map[string]string{"id": state.Downloads[0].ID}))
	if w := workflowHTTP(t, s, dir, "files", "download-save", "POST", "application/json", strings.NewReader(data), false); w.Code != 200 {
		t.Fatal("save failed", w.Code, w.Body.String())
	}
	if bytes, err := os.ReadFile(destination); err != nil || string(bytes) != "download bytes" {
		t.Fatal("saved bytes mismatch", err, string(bytes))
	}
	if w := workflowHTTP(t, s, dir, "other", "download-open", "POST", "application/json", strings.NewReader(data), false); w.Code != 409 {
		t.Fatal("download escaped browser scope", w.Code)
	}
	if w := workflowHTTP(t, s, dir, "files", "download-open", "POST", "application/json", strings.NewReader(data), false); w.Code != 200 || opened != destination {
		t.Fatal("saved file not opened", w.Code, w.Body.String(), opened)
	}
	w := workflowHTTP(t, s, dir, "files", "diagnostics", "GET", "", nil, false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "fixture console error") || !strings.Contains(w.Body.String(), "HTTP 404") || strings.Contains(w.Body.String(), "token=private") {
		t.Fatal("missing or unredacted diagnostics", w.Code, w.Body.String())
	}
	// A prompt may keep the input request busy. Its answer must still reach
	// the exact dialog independently of that request's page gate.
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- request(t, s, dir, "files", "action", `{"action":"click","selector":"#prompt"}`, false)
	}()
	deadline = time.Now().Add(5 * time.Second)
	var tab string
	for time.Now().Before(deadline) {
		s.mu.Lock()
		for _, sc := range s.scopes {
			sc.workflows.mu.Lock()
			for candidate := range sc.workflows.dialogs {
				tab = candidate
			}
			sc.workflows.mu.Unlock()
		}
		s.mu.Unlock()
		if tab != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if tab == "" {
		t.Fatal("no prompt event")
	}
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	req := httptest.NewRequest("GET", "http://127.0.0.1/api/hoxi/browser/workflows?directory="+url.QueryEscape(dir)+"&sessionID=files&tabID="+url.QueryEscape(tab), nil)
	req.Header.Set("Origin", "wails://wails")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	_ = json.Unmarshal(w.Body.Bytes(), &state)
	if state.Dialog == nil {
		t.Fatal("prompt status unavailable", w.Body.String())
	}
	answer := string(mustJSON(t, map[string]any{"id": state.Dialog.ID, "accept": true, "text": "Hoxi"}))
	req = httptest.NewRequest("POST", req.URL.String(), strings.NewReader(answer))
	req.Header.Set("Origin", "wails://wails")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatal("prompt answer failed", w.Code, w.Body.String())
	}
	select {
	case w = <-done:
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("prompt kept input command blocked")
	}
	if got := call(Action{Action: "evaluate", Text: `window.answer`}, true)["result"]; got != `"Hoxi"` {
		t.Fatal("wrong prompt answer", got)
	}
}
