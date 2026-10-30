package browser

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResizeValidationAndLazyStartup(t *testing.T) {
	s := New()
	defer s.Close()
	s.SetAgentToken("secret")
	dir := t.TempDir()
	for _, body := range []string{
		`{"action":"resize"}`,
		`{"action":"resize","width":800}`,
		`{"action":"resize","height":600}`,
		`{"action":"resize","width":null,"height":600}`,
		`{"action":"resize","width":0,"height":600}`,
		`{"action":"resize","width":800,"height":-1}`,
		`{"action":"resize","width":4097,"height":1}`,
		`{"action":"resize","width":1,"height":4097}`,
		`{"action":"resize","width":4096,"height":2049}`,
		`{"action":"resize","width":800.5,"height":600}`,
		`{"action":"resize","width":800,"height":600.5}`,
		`{"action":"resize","width":"800","height":600}`,
		`{"action":"resize","width":800,"height":600,"manual":true}`,
		`{"action":"resize","width":800,"height":600,"deviceScaleFactor":0.9}`,
		`{"action":"resize","width":800,"height":600,"deviceScaleFactor":2.1}`,
		`{"action":"resize","width":4096,"height":2048,"deviceScaleFactor":2}`,
	} {
		if w := request(t, s, dir, "resize", "action", body, false); w.Code != 400 {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body.String())
		}
	}
	for _, action := range []string{"navigate", "back", "forward", "reload", "click", "type", "key", "scroll", "snapshot", "screenshot", "evaluate", "tab_new", "tab_select", "tab_close", "control", "close"} {
		for _, dimension := range []string{"width", "height", "deviceScaleFactor"} {
			body := fmt.Sprintf(`{"action":%q,"url":"about:blank","key":"Enter","text":"1","tabID":"tab","manual":false,%q:0}`, action, dimension)
			if w := request(t, s, dir, "resize", "action", body, false); w.Code != 400 || !strings.Contains(w.Body.String(), "width and height are only valid for resize") {
				t.Fatalf("dimensions accepted on %s: %d", action, w.Code)
			}
		}
	}
	if w := request(t, s, dir, "resize", "action", `{"action":"resize","width":800,"height":600}`, true); w.Code != 400 {
		t.Fatal("agent resize accepted", w.Code)
	}
	if len(s.scopes) != 0 {
		t.Fatal("invalid resize reserved a scope")
	}
	for _, size := range [][2]int{{1, 1}, {4096, 2048}, {2048, 4096}, {800, 600}} {
		body := fmt.Sprintf(`{"action":"resize","width":%d,"height":%d}`, size[0], size[1])
		if w := request(t, s, dir, "resize", "action", body, false); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		for _, sc := range s.scopes {
			if sc.width != size[0] || sc.height != size[1] || sc.browser != nil || sc.profile != "" || sc.allocatorCancel != nil {
				t.Fatal("resize did not save size lazily")
			}
		}
	}
	if w := request(t, s, dir, "resize", "frame", "", false); w.Code != 404 {
		t.Fatal("resize started browser", w.Code)
	}
	request(t, s, dir, "resize", "action", `{"action":"control","manual":true}`, false)
	if w := request(t, s, dir, "resize", "action", `{"action":"resize","width":640,"height":480}`, false); w.Code != 200 {
		t.Fatal("UI resize blocked in manual mode", w.Code)
	}
	if w := request(t, s, dir, "resize", "state", "", true); w.Code != 403 {
		t.Fatal("resize cleared manual gate", w.Code)
	}
	request(t, s, dir, "default", "action", `{"action":"control","manual":false}`, false)
	for key, sc := range s.scopes {
		if key.session == "default" && (sc.width != 1280 || sc.height != 800) {
			t.Fatal("wrong default viewport")
		}
	}
	if w := request(t, s, dir, "resize", "action", `{"action":"close"}`, false); w.Code != 200 || len(s.scopes) != 1 {
		t.Fatal("close did not release resize reservation", w.Code)
	}
}

func TestChromeDynamicViewport(t *testing.T) {
	if os.Getenv("HOXI_BROWSER_TEST") != "1" {
		t.Skip("set HOXI_BROWSER_TEST=1 for Chrome integration")
	}
	s := New()
	defer s.Close()
	s.SetAgentToken("secret")
	dir := t.TempDir()
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<style>body{background:red}@media(max-width:700px){body{background:blue}}</style>`))
	}))
	defer page.Close()
	call := func(body string, agent bool) map[string]any {
		t.Helper()
		w := request(t, s, dir, "viewport", "action", body, agent)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	resize := func(width, height int) {
		t.Helper()
		call(fmt.Sprintf(`{"action":"resize","width":%d,"height":%d}`, width, height), false)
	}
	assertSize := func(width, height int) {
		t.Helper()
		want := fmt.Sprintf(`[%d,%d,%t]`, width, height, width <= 700)
		got := call(`{"action":"evaluate","text":"[innerWidth,innerHeight,matchMedia('(max-width:700px)').matches]"}`, true)["result"]
		if got != want {
			t.Fatalf("responsive viewport: got %v want %s", got, want)
		}
		assertJPEG := func(data []byte) {
			t.Helper()
			img, err := jpeg.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			if img.Bounds().Dx() != width || img.Bounds().Dy() != height {
				t.Fatalf("frame bounds: %v want %dx%d", img.Bounds(), width, height)
			}
		}
		shot := call(`{"action":"screenshot"}`, true)
		data, err := base64.StdEncoding.DecodeString(shot["image"].(string))
		if err != nil {
			t.Fatal(err)
		}
		assertJPEG(data)
		w := request(t, s, dir, "viewport", "frame", "", false)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		assertJPEG(w.Body.Bytes())
	}
	resize(640, 480)
	first := call(fmt.Sprintf(`{"action":"navigate","url":%q}`, page.URL), true)["tabID"].(string)
	assertSize(640, 480)
	second := call(fmt.Sprintf(`{"action":"tab_new","url":%q}`, page.URL), true)["tabID"].(string)
	assertSize(640, 480)
	resize(1024, 768)
	assertSize(1024, 768)
	call(fmt.Sprintf(`{"action":"tab_select","tabID":%q}`, first), true)
	assertSize(1024, 768)
	resize(375, 667)
	assertSize(375, 667)
	call(fmt.Sprintf(`{"action":"tab_select","tabID":%q}`, second), true)
	assertSize(375, 667)
	// Popup targets exercise attachment independently of tab_new.
	call(`{"action":"evaluate","text":"window.open('about:blank','_blank');1"}`, true)
	state := call(`{"action":"snapshot"}`, true)
	var popup string
	for _, tab := range state["tabs"].([]any) {
		id := tab.(map[string]any)["id"].(string)
		if id != first && id != second {
			popup = id
		}
	}
	if popup == "" {
		t.Fatal("popup not discovered")
	}
	call(fmt.Sprintf(`{"action":"tab_select","tabID":%q}`, popup), true)
	assertSize(375, 667)
	call(fmt.Sprintf(`{"action":"tab_select","tabID":%q}`, first), true)
	assertSize(375, 667)
}

func TestResizeFullPageValidation(t *testing.T) {
	s := New()
	defer s.Close()
	s.SetAgentToken("secret")
	dir := t.TempDir()
	// fullPage is meaningless outside resize and must never reserve a scope.
	for _, body := range []string{
		`{"action":"navigate","url":"about:blank","fullPage":true}`,
		`{"action":"control","manual":true,"fullPage":false}`,
		`{"action":"snapshot","fullPage":true}`,
	} {
		if w := request(t, s, dir, "resize", "action", body, false); w.Code != 400 {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body.String())
		}
	}
	if len(s.scopes) != 0 {
		t.Fatal("invalid fullPage reserved a scope")
	}
	// A fullPage resize still only saves size lazily and never starts Chrome;
	// the content height is measured later, once a page is loaded.
	if w := request(t, s, dir, "resize", "action", `{"action":"resize","width":800,"height":600,"fullPage":true}`, false); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	found := false
	for _, sc := range s.scopes {
		found = true
		if sc.width != 800 || sc.browser != nil || !sc.fullPage {
			t.Fatal("fullPage resize did not save lazily")
		}
	}
	if !found {
		t.Fatal("fullPage resize reserved no scope")
	}
	// Turning it back off keeps the scope but clears the flag.
	if w := request(t, s, dir, "resize", "action", `{"action":"resize","width":800,"height":600,"fullPage":false}`, false); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, sc := range s.scopes {
		if sc.fullPage {
			t.Fatal("fullPage not cleared")
		}
	}
}

func TestViewportOnlyScopesDoNotExhaustLimit(t *testing.T) {
	s := New()
	defer s.Close()
	dir := t.TempDir()
	// Opening the Browser tab in many sessions only saves viewport sizes.
	request(t, s, dir, "ses_manual", "action", `{"action":"control","manual":true}`, false)
	for i := 0; i < maxScopes*2; i++ {
		body := `{"action":"resize","width":800,"height":600}`
		if w := request(t, s, dir, fmt.Sprintf("ses_%d", i), "action", body, false); w.Code != 200 {
			t.Fatalf("resize %d: %d %s", i, w.Code, w.Body.String())
		}
	}
	if len(s.scopes) > maxScopes {
		t.Fatalf("scope count %d exceeds limit", len(s.scopes))
	}
	manual := false
	for key, sc := range s.scopes {
		if key.session == "ses_manual" && sc.manual {
			manual = true
		}
	}
	if !manual {
		t.Fatal("manual-control scope was evicted")
	}
	resolved, _ := filepath.EvalSymlinks(dir)
	if _, ok := s.scopes[scopeKey{resolved, fmt.Sprintf("ses_%d", maxScopes*2-1)}]; !ok {
		t.Fatal("latest viewport scope was not kept")
	}
}
