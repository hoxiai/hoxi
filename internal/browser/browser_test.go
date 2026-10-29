package browser

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func request(t *testing.T, s *Service, dir, session, path, body string, agent bool) *httptest.ResponseRecorder {
	t.Helper()
	method := "GET"
	if path == "action" {
		method = "POST"
	}
	r := httptest.NewRequest(method, "http://localhost/api/hoxi/browser/"+path+"?directory="+url.QueryEscape(dir)+"&sessionID="+url.QueryEscape(session), strings.NewReader(body))
	if agent {
		r.Header.Set("Authorization", "Bearer secret")
		r.Header.Set("X-Hoxi-Agent", "true")
	} else {
		r.Header.Set("Origin", "wails://wails.localhost")
	}
	if method == "POST" {
		r.Header.Set("Content-Type", "application/json")
	}
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestValidation(t *testing.T) {
	for _, raw := range []string{"https://example.com", "http://127.0.0.1:123", "about:blank"} {
		if !navigationURL(raw) {
			t.Errorf("rejected %q", raw)
		}
	}
	for _, raw := range []string{"javascript:alert(1)", "file:///etc/passwd", "data:text/html,hi", "about:config", "https://", "//example.com"} {
		if navigationURL(raw) {
			t.Errorf("accepted %q", raw)
		}
	}
	for _, a := range []Action{{Action: "oops"}, {Action: "control"}, {Action: "evaluate", Text: "1"}, {Action: "key", Key: "Control+"}, {Action: "navigate", URL: "file:///tmp"}} {
		if validateAction(a, false) == nil {
			t.Errorf("accepted %+v", a)
		}
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/?directory="+url.QueryEscape(link)+"&sessionID=ses_123", nil)
	key, err := requestScope(r)
	want, _ := filepath.EvalSymlinks(dir)
	if err != nil || key.directory != want {
		t.Fatalf("canonical scope: %+v %v", key, err)
	}
	for _, query := range []string{"directory=relative&sessionID=a", "directory=/no-such-dir&sessionID=a", "directory=" + url.QueryEscape(dir) + "&sessionID=../x", "directory=" + url.QueryEscape(dir) + "&sessionID=a&sessionID=b"} {
		if _, err := requestScope(httptest.NewRequest("GET", "/?"+query, nil)); err == nil {
			t.Errorf("accepted %s", query)
		}
	}
}

func TestAuthentication(t *testing.T) {
	s := New()
	defer s.Close()
	s.SetAgentToken("secret")
	s.SetUIOrigins("http://localhost:5173", "https://wails.localhost")
	for _, tc := range []struct {
		host, origin, auth, agent, fetch string
		ok, isAgent                      bool
	}{
		{"localhost:8080", "http://localhost:5173", "", "", "", true, false},
		{"wails.localhost", "wails://wails.localhost", "", "", "", true, false},
		{"wails", "wails://wails", "", "", "", true, false},
		{"localhost:8080", "http://localhost:5174", "", "", "", false, false},
		{"127.0.0.1", "", "Bearer secret", "true", "", true, true},
		{"[::1]:123", "https://wails.localhost", "", "", "", true, false},
		{"evil.example", "http://localhost", "", "", "", false, false},
		{"localhost", "https://evil.example", "Bearer secret", "true", "", false, false},
		{"localhost", "http://localhost", "Bearer secret", "true", "", false, false},
		{"localhost", "", "Bearer secret", "true", "same-origin", false, false},
		{"localhost", "", "Bearer wrong", "true", "", false, false},
		{"localhost", "", "", "", "", false, false},
		{"localhost.attacker.org", "http://localhost", "", "", "", false, false},
		{"localhost", "null", "", "", "", false, false},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Host = tc.host
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Authorization", tc.auth)
		r.Header.Set("X-Hoxi-Agent", tc.agent)
		r.Header.Set("Sec-Fetch-Site", tc.fetch)
		agent, err := s.authenticate(r)
		if (err == nil) != tc.ok || agent != tc.isAgent {
			t.Errorf("%+v: agent=%v err=%v", tc, agent, err)
		}
	}
}

func TestLazyManualAndStrictJSON(t *testing.T) {
	s := New()
	defer s.Close()
	s.SetAgentToken("secret")
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if w := request(t, s, dir, "a", "state", "", false); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if len(s.scopes) != 0 {
		t.Fatal("GET created scope")
	}
	if w := request(t, s, dir, "a", "frame", "", false); w.Code != 404 {
		t.Fatal(w.Code)
	}
	for _, body := range []string{`{"action":"control","manual":true,"unknown":1}`, `{"action":"close"} {}`, `null`, `{"action":"navigate","url":"javascript:1"}`} {
		if w := request(t, s, dir, "a", "action", body, false); w.Code != 400 {
			t.Fatalf("%s: %d", body, w.Code)
		}
	}
	if w := request(t, s, dir, "a", "action", `{"action":"control","manual":true}`, false); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := request(t, s, dir, "a", "action", `{"action":"screenshot"}`, true); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := request(t, s, dir, "a", "state", "", true); w.Code != 403 {
		t.Fatal(w.Code)
	}
	for _, value := range []string{"true", "false"} {
		if w := request(t, s, dir, "a", "action", `{"action":"control","manual":`+value+`}`, true); w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	if w := request(t, s, dir, "a", "action", `{"action":"control","manual":false}`, false); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request(t, s, dir, "a", "frame", "", false); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w := request(t, s, dir, "a", "state", "", true); w.Code != 200 {
		t.Fatal(w.Code)
	}
	for _, sc := range s.scopes {
		if sc.browser != nil || sc.profile != "" {
			t.Fatal("control launched Chrome")
		}
	}
	if w := request(t, s, dir, "a", "action", `{"action":"close"}`, false); w.Code != 200 || len(s.scopes) != 0 {
		t.Fatal("close did not free scope")
	}
}

func TestScopesAndCancellation(t *testing.T) {
	s := New()
	defer s.Close()
	ctx := context.Background()
	for i := 0; i < maxScopes; i++ {
		sc, err := s.acquire(ctx, scopeKey{"/tmp", string(rune('a' + i))}, true)
		if err != nil {
			t.Fatal(err)
		}
		// Mark the slot as running Chrome; idle viewport-only scopes are evictable.
		sc.browser = ctx
		<-sc.gate
	}
	if _, err := s.acquire(ctx, scopeKey{"/tmp", "extra"}, true); err == nil {
		t.Fatal("limit ignored")
	}
	key := scopeKey{"/tmp", "a"}
	sc, _ := s.acquire(ctx, key, false)
	wait, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()
	if _, err := s.acquire(wait, key, false); err == nil {
		t.Fatal("lock ignored cancellation")
	}
	s.remove(key, sc)
	<-sc.gate
	other, err := s.acquire(ctx, scopeKey{"/tmp", "extra"}, true)
	if err != nil {
		t.Fatal(err)
	}
	<-other.gate
	s.Close()
	s.Close()
	if _, err := s.acquire(ctx, key, true); err == nil {
		t.Fatal("closed service accepted scope")
	}
}

func TestChromeSmoke(t *testing.T) {
	if os.Getenv("HOXI_BROWSER_TEST") != "1" {
		t.Skip("set HOXI_BROWSER_TEST=1 for Chrome integration")
	}
	s := New()
	defer s.Close()
	s.SetAgentToken("secret")
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<title>Hoxi Smoke</title><button id="go" onclick="document.title='Clicked'">Go</button><input id="name">`))
	}))
	defer page.Close()
	call := func(session, body string) map[string]any {
		t.Helper()
		w := request(t, s, dir, session, "action", body, true)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := call("a", `{"action":"navigate","url":"`+page.URL+`"}`)
	if first["title"] != "Hoxi Smoke" {
		t.Fatal(first)
	}
	call("a", `{"action":"type","selector":"#name","text":"hello"}`)
	call("a", `{"action":"click","selector":"#go"}`)
	shot := call("a", `{"action":"screenshot"}`)
	data, err := base64.StdEncoding.DecodeString(shot["image"].(string))
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil || img.Bounds().Dx() != 1280 || img.Bounds().Dy() != 800 {
		t.Fatalf("JPEG dimensions: %v", err)
	}
	snap := call("a", `{"action":"snapshot"}`)
	if !strings.Contains(string(mustJSON(t, snap)), "#go") {
		t.Fatal(snap)
	}
	call("a", `{"action":"evaluate","text":"window.open('about:blank','_blank'); 1"}`)
	var state map[string]any
	for i := 0; i < 30; i++ {
		state = call("a", `{"action":"snapshot"}`)
		if len(state["tabs"].([]any)) >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(state["tabs"].([]any)) < 2 {
		t.Fatal("popup not listed")
	}
	newTab := call("a", `{"action":"tab_new"}`)
	id := newTab["tabID"].(string)
	call("a", `{"action":"navigate","url":"`+page.URL+`"}`)
	call("a", `{"action":"snapshot"}`)
	call("a", `{"action":"click","selector":"#go"}`)
	call("a", `{"action":"tab_select","tabID":"`+first["tabID"].(string)+`"}`)
	call("a", `{"action":"tab_close","tabID":"`+id+`"}`)
	second := call("b", `{"action":"navigate","url":"about:blank"}`)
	if second["title"] == "Clicked" {
		t.Fatal("scope leaked")
	}
	s.mu.Lock()
	var profiles []string
	for _, sc := range s.scopes {
		profiles = append(profiles, sc.profile)
	}
	s.mu.Unlock()
	call("a", `{"action":"close"}`)
	s.Close()
	for _, profile := range profiles {
		if _, err := os.Stat(profile); !os.IsNotExist(err) {
			t.Fatalf("profile remains: %s %v", profile, err)
		}
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestWindowModeAndPersistentProfile(t *testing.T) {
	s := New()
	defer s.Close()
	s.SetAgentToken("secret")
	profileDir := filepath.Join(t.TempDir(), "browser-profile")
	s.SetProfileRoot(profileDir)
	s.SetDefaultWindowMode(true)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// Idle state reflects default window mode without creating a scope.
	w := request(t, s, dir, "a", "state", "", false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"mode":"window"`) {
		t.Fatalf("expected window mode in idle state, got %d %s", w.Code, w.Body.String())
	}
	// Agent cannot change browser mode or call focus.
	for _, body := range []string{`{"action":"mode","window":false}`, `{"action":"focus"}`, `{"action":"navigate","url":"https://example.com","window":false}`} {
		if resp := request(t, s, dir, "a", "action", body, true); resp.Code != 400 {
			t.Fatalf("agent should not be allowed %s: %d", body, resp.Code)
		}
	}
	// UI can switch between window and headless mode lazily without starting Chrome.
	if resp := request(t, s, dir, "a", "action", `{"action":"mode","window":false}`, false); resp.Code != 200 || strings.Contains(resp.Body.String(), `"mode":"window"`) {
		t.Fatalf("expected headless mode after mode switch: %d %s", resp.Code, resp.Body.String())
	}
	if resp := request(t, s, dir, "a", "action", `{"action":"mode","window":true}`, false); resp.Code != 200 || !strings.Contains(resp.Body.String(), `"mode":"window"`) {
		t.Fatalf("expected window mode after mode switch: %d %s", resp.Code, resp.Body.String())
	}
	// Verify persistent profile directory is preserved across resetBrowser and Close.
	sc, err := s.acquire(context.Background(), scopeKey{dir, "a"}, false)
	if err != nil || sc == nil {
		t.Fatalf("acquire scope: %v %v", sc, err)
	}
	gotProfile, err := s.acquirePersistentProfile(sc)
	if err != nil || gotProfile != profileDir {
		<-sc.gate
		t.Fatalf("acquirePersistentProfile = %q, %v; want %q", gotProfile, err, profileDir)
	}
	sc.profile = gotProfile
	sc.persistentProfile = true
	cookieMarker := filepath.Join(profileDir, "Cookies")
	if err := os.WriteFile(cookieMarker, []byte("keep-login"), 0o600); err != nil {
		<-sc.gate
		t.Fatal(err)
	}
	sc.resetBrowser()
	<-sc.gate
	if _, err := os.Stat(cookieMarker); err != nil {
		t.Fatalf("persistent profile should not be deleted on resetBrowser: %v", err)
	}
	s.Close()
	if _, err := os.Stat(cookieMarker); err != nil {
		t.Fatalf("persistent profile should not be deleted on Close: %v", err)
	}
}

