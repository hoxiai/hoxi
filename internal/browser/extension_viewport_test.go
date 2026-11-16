package browser

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// linkChrome pairs scope "a" with a fake extension announcing features and
// returns the actions the extension received.
func linkChrome(t *testing.T, s *Service, dir string, features []string) func() []Action {
	t.Helper()
	pair := request(t, s, dir, "a", "action", `{"action":"pair"}`, false)
	var payload struct {
		Code string `json:"code"`
	}
	if pair.Code != 200 || json.Unmarshal(pair.Body.Bytes(), &payload) != nil {
		t.Fatal(pair.Code, pair.Body.String())
	}
	decoded, err := base64.RawURLEncoding.DecodeString(payload.Code)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(s.ServeExtension))
	t.Cleanup(server.Close)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"),
		http.Header{"Origin": {"chrome-extension://" + strings.Repeat("a", 32)}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if err := conn.WriteJSON(map[string]any{"token": strings.Split(string(decoded), "#")[1], "features": features}); err != nil {
		t.Fatal(err)
	}
	var hello map[string]any
	if err := conn.ReadJSON(&hello); err != nil || hello["connected"] != true {
		t.Fatal(err, hello)
	}
	_ = conn.SetReadDeadline(time.Time{})
	var mu sync.Mutex
	var received []Action
	go func() {
		for {
			var a Action
			if conn.ReadJSON(&a) != nil {
				return
			}
			mu.Lock()
			received = append(received, a)
			mu.Unlock()
			if conn.WriteJSON(extensionReply{Result: map[string]any{"tabID": "42", "url": "https://example.com", "tabs": []Tab{{ID: "42"}}}}) != nil {
				return
			}
		}
	}()
	return func() []Action {
		mu.Lock()
		defer mu.Unlock()
		return append([]Action(nil), received...)
	}
}

func TestExtensionViewportFollowsPanel(t *testing.T) {
	s := New()
	defer s.Close()
	s.SetAgentToken("secret")
	s.SetBridgeURL("ws://127.0.0.1:5420")
	dir := t.TempDir()
	received := linkChrome(t, s, dir, []string{"viewport"})
	if w := request(t, s, dir, "a", "action", `{"action":"resize","width":480,"height":720}`, false); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request(t, s, dir, "a", "action", `{"action":"viewport_reset"}`, false); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	got := received()
	if len(got) != 2 || got[0].Action != "resize" || *got[0].Width != 480 || *got[0].Height != 720 || got[1].Action != "viewport_reset" {
		t.Fatalf("extension received %+v", got)
	}
	for _, body := range []string{`{"action":"viewport_reset"}`, `{"action":"resize","width":480,"height":720}`} {
		if w := request(t, s, dir, "a", "action", body, true); w.Code != 400 {
			t.Fatal("agent changed the Chrome Link viewport", body, w.Code)
		}
	}
	if w := request(t, s, dir, "a", "action", `{"action":"viewport_reset","width":1}`, false); w.Code != 400 {
		t.Fatal("viewport_reset accepted dimensions", w.Code)
	}
	if len(received()) != 2 {
		t.Fatal("rejected viewport actions reached Chrome")
	}
}

func TestOlderExtensionKeepsItsViewport(t *testing.T) {
	s := New()
	defer s.Close()
	s.SetAgentToken("secret")
	s.SetBridgeURL("ws://127.0.0.1:5420")
	dir := t.TempDir()
	received := linkChrome(t, s, dir, []string{"pointer"})
	for _, body := range []string{`{"action":"resize","width":480,"height":720}`, `{"action":"viewport_reset"}`} {
		if w := request(t, s, dir, "a", "action", body, false); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, a := range received() {
		if a.Action != "state" {
			t.Fatalf("older extension received %q", a.Action)
		}
	}
}

func TestViewportResetNeverStartsChrome(t *testing.T) {
	s := New()
	defer s.Close()
	dir := t.TempDir()
	request(t, s, dir, "a", "action", `{"action":"viewport_reset"}`, false)
	for _, sc := range s.scopes {
		if sc.browser != nil || sc.profile != "" {
			t.Fatal("viewport_reset launched Chrome")
		}
	}
}
