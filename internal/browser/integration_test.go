package browser

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBrowserHeader(t *testing.T) {
	s := New()
	defer s.Close()
	s.SetAgentToken("secret")
	s.SetUIOrigins("http://localhost")
	dir := t.TempDir()
	for _, tc := range []struct {
		origin, marker, site, auth string
		allowed                    bool
	}{
		{"", "true", "", "", false},
		{"", "true", "same-origin", "", true},
		{"", "true", "same-site", "", false},
		{"", "true", "cross-site", "", false},
		{"", "true", "none", "", false},
		{"", "", "same-origin", "", false},
		{"", "false", "same-origin", "", false},
		{"https://evil.example", "true", "same-origin", "", false},
		{"http://localhost", "", "same-origin", "", true},
		{"", "true", "", "Bearer secret", false},
	} {
		for _, path := range []string{"state", "frame"} {
			r := httptest.NewRequest("GET", "http://localhost/api/hoxi/browser/"+path+"?directory="+url.QueryEscape(dir)+"&sessionID=a", nil)
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("X-Hoxi-Browser", tc.marker)
			r.Header.Set("Sec-Fetch-Site", tc.site)
			if tc.auth != "" {
				r.Header.Set("Authorization", tc.auth)
				r.Header.Set("X-Hoxi-Agent", "true")
			}
			w := httptest.NewRecorder()
			s.serveHTTP(w, r)
			want := 403
			if tc.allowed {
				want = 200
				if path == "frame" {
					want = 404
				}
			}
			if w.Code != want {
				t.Errorf("%+v %s: %d %s", tc, path, w.Code, w.Body.String())
			}
		}
	}
}

func TestKeyboardValidation(t *testing.T) {
	for _, key := range []string{"Control+a", "Meta+a", "Shift+ArrowLeft", "Alt+Shift+ArrowRight", "Enter", "+", "Control++", "a"} {
		if _, err := keyAction(key); err != nil {
			t.Errorf("%s: %v", key, err)
		}
	}
	for _, key := range []string{"", "Control+", "Hyper+a", "Control+Control+a", "Control+NotAKey"} {
		if _, err := keyAction(key); err == nil {
			t.Errorf("accepted %s", key)
		}
	}
	if err := validateAction(Action{Action: "type", Text: "paste"}, false); err != nil {
		t.Fatal(err)
	}
}

// Back/forward must return promptly even when Chrome restores the entry from
// its back/forward cache, which never fires a fresh document load.
func TestChromeHistoryNavigationReturns(t *testing.T) {
	if os.Getenv("HOXI_BROWSER_TEST") != "1" {
		t.Skip("set HOXI_BROWSER_TEST=1 for Chrome integration")
	}
	s := New()
	defer s.Close()
	dir := t.TempDir()
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<title>" + r.URL.Path + "</title><p>" + r.URL.Path))
	}))
	defer page.Close()
	call := func(a Action) map[string]any {
		t.Helper()
		start := time.Now()
		w := request(t, s, dir, "history", "action", string(mustJSON(t, a)), false)
		if w.Code != 200 {
			t.Fatalf("%s after %s: %d %s", a.Action, time.Since(start).Round(time.Millisecond), w.Code, w.Body.String())
		}
		if elapsed := time.Since(start); elapsed > 10*time.Second {
			t.Fatalf("%s took %s", a.Action, elapsed)
		}
		var result map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	call(Action{Action: "navigate", URL: page.URL + "/a"})
	call(Action{Action: "navigate", URL: page.URL + "/b"})
	for _, step := range []struct{ action, path string }{{"back", "/a"}, {"forward", "/b"}, {"back", "/a"}} {
		if got := call(Action{Action: step.action})["url"]; got != page.URL+step.path {
			t.Fatalf("%s: url %v, want %s", step.action, got, page.URL+step.path)
		}
	}
}

func TestChromeInputViewportAndLastTab(t *testing.T) {
	if os.Getenv("HOXI_BROWSER_TEST") != "1" {
		t.Skip("set HOXI_BROWSER_TEST=1 for Chrome integration")
	}
	s := New()
	defer s.Close()
	s.SetAgentToken("secret")
	dir := t.TempDir()
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<style>body{margin:0}section{height:1600px;background:rgb(240,20,20)}#bottom{background:rgb(20,20,240)}</style><section><input id="name"><script>window.keys=[];addEventListener('keydown',e=>keys.push({key:e.key,ctrl:e.ctrlKey,meta:e.metaKey,shift:e.shiftKey}));</script></section><section id="bottom"></section>`))
	}))
	defer page.Close()
	call := func(a Action, agent bool) map[string]any {
		t.Helper()
		body := string(mustJSON(t, a))
		w := request(t, s, dir, "input", "action", body, agent)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	eval := func(text string) string {
		t.Helper()
		return call(Action{Action: "evaluate", Text: text}, true)["result"].(string)
	}
	first := call(Action{Action: "navigate", URL: page.URL}, false)
	call(Action{Action: "click", Selector: "#name"}, false)
	call(Action{Action: "type", Text: "hello"}, false)
	call(Action{Action: "type", Text: " world"}, false)
	if got := eval(`document.querySelector('#name').value`); got != `"hello world"` {
		t.Fatal(got)
	}
	// Exercise both modifier encodings, and the platform-native select-all.
	for _, key := range []string{"Control+a", "Meta+a"} {
		call(Action{Action: "key", Key: key}, false)
	}
	selectAll := "Control+a"
	if runtime.GOOS == "darwin" {
		selectAll = "Meta+a"
	}
	call(Action{Action: "key", Key: selectAll}, false)
	if got := eval(`(()=>{const e=document.querySelector('#name');return [e.selectionStart,e.selectionEnd]})()`); got != `[0,11]` {
		t.Fatal("select-all: " + got)
	}
	call(Action{Action: "type", Text: "pasted"}, false)
	call(Action{Action: "key", Key: "Shift+ArrowLeft"}, false)
	if got := eval(`(()=>{const e=document.querySelector('#name');return [e.selectionStart,e.selectionEnd]})()`); got != `[5,6]` {
		t.Fatal("shift selection: " + got)
	}
	call(Action{Action: "type", Text: "!"}, false)
	if got := eval(`document.querySelector('#name').value`); got != `"paste!"` {
		t.Fatal(got)
	}
	if got := eval(`keys.some(e=>e.key==='a'&&e.ctrl)&&keys.some(e=>e.key==='a'&&e.meta)&&keys.some(e=>e.key==='ArrowLeft'&&e.shift)`); got != "true" {
		t.Fatal("modifier dispatch: " + got)
	}
	call(Action{Action: "type", Selector: "#name", Text: " appended"}, false)
	if got := eval(`document.querySelector('#name').value`); got != `"paste! appended"` {
		t.Fatal("selector insert: " + got)
	}
	assertImage := func(data []byte, blue bool) {
		t.Helper()
		img, err := jpeg.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if img.Bounds().Dx() != 1280 || img.Bounds().Dy() != 800 {
			t.Fatal(img.Bounds())
		}
		r, _, b, _ := img.At(640, 400).RGBA()
		if (blue && b < r+30000) || (!blue && r < b+30000) {
			t.Fatalf("wrong viewport color: red=%d blue=%d wantBlue=%v", r, b, blue)
		}
	}
	shot := call(Action{Action: "screenshot"}, false)
	data, err := base64.StdEncoding.DecodeString(shot["image"].(string))
	if err != nil {
		t.Fatal(err)
	}
	assertImage(data, false)
	call(Action{Action: "scroll", X: 600, Y: 400, DeltaY: 1800}, false)
	for i := 0; i < 50; i++ {
		if eval(`scrollY>=1600`) == "true" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := eval(`scrollY>=1600`); got != "true" {
		t.Fatal("page did not scroll")
	}
	shot = call(Action{Action: "screenshot"}, false)
	data, err = base64.StdEncoding.DecodeString(shot["image"].(string))
	if err != nil {
		t.Fatal(err)
	}
	assertImage(data, true)
	w := request(t, s, dir, "input", "frame", "", false)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	assertImage(w.Body.Bytes(), true)
	closed := call(Action{Action: "tab_close", TabID: first["tabID"].(string)}, false)
	if len(closed["tabs"].([]any)) != 1 || closed["tabID"] == first["tabID"] || closed["url"] != "about:blank" {
		t.Fatal("last tab not replaced", closed)
	}
	call(Action{Action: "navigate", URL: page.URL}, false)
	call(Action{Action: "type", Selector: "#name", Text: "after close"}, false)
	if got := eval(`document.querySelector('#name').value`); !strings.Contains(got, "after close") {
		t.Fatal(got)
	}
}
