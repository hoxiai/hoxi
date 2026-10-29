package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestExtensionPairingOwnershipAndDisconnect(t *testing.T) {
	s := New()
	defer s.Close()
	s.SetAgentToken("secret")
	s.SetBridgeURL("ws://127.0.0.1:5420")
	dir := t.TempDir()
	if w := request(t, s, dir, "a", "action", `{"action":"pair"}`, true); w.Code != 400 {
		t.Fatal("agent created connection capability", w.Code)
	}
	pair := request(t, s, dir, "a", "action", `{"action":"pair"}`, false)
	if pair.Code != 200 {
		t.Fatal(pair.Code, pair.Body.String())
	}
	var payload struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(pair.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(payload.Code)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Split(string(decoded), "#")[1]
	if !strings.HasPrefix(string(decoded), "http://127.0.0.1:5420/api/hoxi/browser/extension#") {
		t.Fatal(string(decoded))
	}
	if w := request(t, s, dir, "a", "action", `{"action":"navigate","url":"about:blank"}`, true); w.Code != 409 {
		t.Fatal("pending pairing launched another browser", w.Code)
	}
	server := httptest.NewServer(http.HandlerFunc(s.ServeExtension))
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http")
	dial := func(token string) (*websocket.Conn, error) {
		t.Helper()
		conn, _, err := websocket.DefaultDialer.Dial(url, http.Header{"Origin": {"chrome-extension://" + strings.Repeat("a", 32)}})
		if err != nil {
			return nil, err
		}
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		if err := conn.WriteJSON(map[string]string{"token": token}); err != nil {
			conn.Close()
			return nil, err
		}
		var hello map[string]any
		if err := conn.ReadJSON(&hello); err != nil {
			conn.Close()
			return nil, err
		}
		if hello["connected"] != true {
			t.Fatal(hello)
		}
		return conn, nil
	}
	if conn, err := dial(strings.Repeat("f", 64)); err == nil {
		conn.Close()
		t.Fatal("wrong token accepted")
	}
	conn, err := dial(token)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if replay, err := dial(token); err == nil {
		replay.Close()
		t.Fatal("token replay accepted")
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			var a Action
			if conn.ReadJSON(&a) != nil {
				return
			}
			if conn.WriteJSON(extensionReply{Result: map[string]any{"tabID": "42", "url": "https://example.com", "tabs": []Tab{{ID: "42"}}}}) != nil {
				return
			}
		}
	}()
	for _, agent := range []bool{false, true} {
		w := request(t, s, dir, "a", "state", "", agent)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"mode":"chrome"`) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := request(t, s, dir, "b", "state", "", true); strings.Contains(w.Body.String(), `"42"`) {
		t.Fatal("scope leaked", w.Body.String())
	}
	request(t, s, dir, "a", "action", `{"action":"control","manual":true}`, false)
	if w := request(t, s, dir, "a", "action", `{"action":"snapshot"}`, true); w.Code != 403 {
		t.Fatal("manual ownership bypassed", w.Code)
	}
	if w := request(t, s, dir, "a", "action", `{"action":"close"}`, false); w.Code != 200 {
		t.Fatal(w.Code)
	}
	_ = conn.Close()
	<-done
	if len(s.scopes) != 0 {
		t.Fatal("disconnect retained scope")
	}
}

func TestExtensionRejectsOriginsExpiryAndBrowserReplacement(t *testing.T) {
	s := New()
	defer s.Close()
	s.SetBridgeURL("ws://127.0.0.1:5420")
	dir := t.TempDir()
	for _, origin := range []string{"", "https://evil.example", "chrome-extension://invalid", "chrome-extension://" + strings.Repeat("z", 32)} {
		r := httptest.NewRequest("GET", "http://localhost/api/hoxi/browser/extension", nil)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		s.ServeExtension(w, r)
		if w.Code != 403 {
			t.Fatal(origin, w.Code)
		}
	}
	r := httptest.NewRequest("GET", "/?directory="+url.QueryEscape(dir)+"&sessionID=a", nil)
	key, err := requestScope(r)
	if err != nil {
		t.Fatal(err)
	}
	sc, err := s.acquire(context.Background(), key, true)
	if err != nil {
		t.Fatal(err)
	}
	sc.pairToken = "expired"
	sc.pairExpires = time.Now().Add(-time.Second)
	sc.extension = &extensionBridge{done: make(chan struct{})}
	sc.extension.once.Do(func() { close(sc.extension.done) })
	<-sc.gate
	w := request(t, s, dir, "a", "state", "", false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"connected":false`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request(t, s, dir, "a", "action", `{"action":"pair"}`, false); w.Code != 409 {
		t.Fatal("replaced existing connection", w.Code)
	}
	// The synthetic disconnected bridge owns no websocket.
	sc, _ = s.acquire(context.Background(), key, false)
	sc.extension = nil
	<-sc.gate
}

func TestExtensionExpiredCodeAndStream(t *testing.T) {
	s := New()
	defer s.Close()
	s.SetAgentToken("secret")
	dir := t.TempDir()
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	s.SetBridgeURL(server.URL)
	s.SetUIOrigins(server.URL)
	pair := request(t, s, dir, "stream", "action", `{"action":"pair"}`, false)
	var payload struct {
		Code string `json:"code"`
	}
	if pair.Code != 200 || json.Unmarshal(pair.Body.Bytes(), &payload) != nil {
		t.Fatal(pair.Code, pair.Body.String())
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload.Code)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Split(string(raw), "#")[1]
	key, err := requestScope(httptest.NewRequest("GET", "/?directory="+url.QueryEscape(dir)+"&sessionID=stream", nil))
	if err != nil {
		t.Fatal(err)
	}
	sc, err := s.acquire(context.Background(), key, false)
	if err != nil {
		t.Fatal(err)
	}
	sc.pairExpires = time.Now().Add(-time.Second)
	<-sc.gate
	dial := func() *websocket.Conn {
		t.Helper()
		conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/hoxi/browser/extension",
			http.Header{"Origin": {"chrome-extension://" + strings.Repeat("a", 32)}})
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		if err := conn.WriteJSON(map[string]string{"token": token}); err != nil {
			t.Fatal(err)
		}
		return conn
	}
	expired := dial()
	var hello map[string]any
	if expired.ReadJSON(&hello) == nil {
		t.Fatal("expired code accepted")
	}
	_ = expired.Close()
	sc, err = s.acquire(context.Background(), key, false)
	if err != nil {
		t.Fatal(err)
	}
	sc.pairExpires = time.Now().Add(time.Minute)
	<-sc.gate
	extension := dial()
	defer extension.Close()
	if err := extension.ReadJSON(&hello); err != nil || hello["connected"] != true {
		t.Fatal(hello, err)
	}
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		for {
			var a Action
			if extension.ReadJSON(&a) != nil {
				return
			}
			result := map[string]any{"image": "YWJj", "width": 800, "height": 600, "tabID": "42", "tabs": []Tab{{ID: "42"}}}
			if extension.WriteJSON(extensionReply{Result: result}) != nil {
				return
			}
		}
	}()
	streamURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/hoxi/browser/stream?sessionID=stream&directory=" + url.QueryEscape(dir)
	stream, _, err := websocket.DefaultDialer.Dial(streamURL, http.Header{"Origin": {server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	_ = stream.SetReadDeadline(time.Now().Add(3 * time.Second))
	var frame streamFrame
	if err := stream.ReadJSON(&frame); err != nil {
		t.Fatal(err)
	}
	if frame.Image != "YWJj" || frame.Width != 800 || frame.TabID != "42" {
		t.Fatal(frame)
	}
	if err := stream.WriteMessage(websocket.TextMessage, []byte("ack")); err != nil {
		t.Fatal(err)
	}
	if err := stream.ReadJSON(&frame); err != nil || frame.Image == "" {
		t.Fatal("stream did not continue after ACK", frame, err)
	}
	_ = stream.Close()
	// Disconnecting the inspector must preserve the real browser connection.
	if w := request(t, s, dir, "stream", "state", "", false); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	request(t, s, dir, "stream", "action", `{"action":"close"}`, false)
	_ = extension.Close()
	<-workerDone
}

func TestExtensionPersistentLinkTokenReconnect(t *testing.T) {
	s := New()
	defer s.Close()
	profileDir := filepath.Join(t.TempDir(), "browser-profile")
	s.SetProfileRoot(profileDir)
	s.SetBridgeURL("ws://127.0.0.1:5420")
	dir := t.TempDir()
	pair := request(t, s, dir, "a", "action", `{"action":"pair"}`, false)
	if pair.Code != 200 {
		t.Fatal(pair.Code, pair.Body.String())
	}
	var payload struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(pair.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(payload.Code)
	if err != nil {
		t.Fatal(err)
	}
	oneTimeToken := strings.Split(string(decoded), "#")[1]
	server := httptest.NewServer(http.HandlerFunc(s.ServeExtension))
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	dial := func(token string) (*websocket.Conn, map[string]any) {
		t.Helper()
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, http.Header{"Origin": {"chrome-extension://" + strings.Repeat("a", 32)}})
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		if err := conn.WriteJSON(map[string]string{"token": token}); err != nil {
			conn.Close()
			t.Fatal(err)
		}
		var hello map[string]any
		if err := conn.ReadJSON(&hello); err != nil {
			conn.Close()
			t.Fatal(err)
		}
		return conn, hello
	}
	conn1, hello1 := dial(oneTimeToken)
	linkToken, _ := hello1["linkToken"].(string)
	if len(linkToken) != 64 {
		conn1.Close()
		t.Fatalf("expected 64-char persistent linkToken, got %q", linkToken)
	}
	_ = conn1.Close()
	// Wait for the first bridge to mark itself disconnected, then reconnect using
	// the persistent linkToken without generating a new 2-minute pairing code.
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		w := request(t, s, dir, "a", "state", "", false)
		if strings.Contains(w.Body.String(), `"connected":false`) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	conn2, hello2 := dial(linkToken)
	defer conn2.Close()
	if hello2["connected"] != true {
		t.Fatalf("expected persistent linkToken reconnect to succeed, got %v", hello2)
	}
}

