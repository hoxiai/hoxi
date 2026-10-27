package browser

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/gorilla/websocket"
)

func TestStreamCallbackBounds(t *testing.T) {
	for _, oversized := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		st := &screencast{ctx: ctx, cancel: cancel, events: make(chan *page.EventScreencastFrame, 1)}
		if oversized {
			st.receive(&page.EventScreencastFrame{Data: strings.Repeat("x", maxStreamImage+1)})
		} else {
			st.receive(&page.EventScreencastFrame{Data: "first"})
			st.receive(&page.EventScreencastFrame{Data: "overflow"})
		}
		if ctx.Err() == nil {
			t.Fatal("unbounded CDP event accepted")
		}
		cancel()
	}
}

func TestStreamSecurityAndLazyStartup(t *testing.T) {
	s := New()
	defer s.Close()
	s.SetAgentToken("secret")
	s.SetUIOrigins("http://localhost:5173")
	dir := t.TempDir()
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	for _, tc := range []struct {
		host, origin, auth, marker string
		status                     int
	}{
		{"localhost", "wails://wails", "", "", 404},
		{"localhost", "http://localhost:5173", "", "", 404},
		{"localhost", "http://localhost:5174", "", "", 403},
		{"evil.example", "wails://wails", "", "", 403},
		{"localhost", "null", "", "", 403},
		{"localhost", "", "", "true", 403},
		{"localhost", "", "Bearer secret", "", 403},
		{"localhost", "wails://wails", "Bearer secret", "", 403},
	} {
		r := httptest.NewRequest("GET", "http://localhost/api/hoxi/browser/stream?sessionID=stream&directory="+url.QueryEscape(dir), nil)
		r.Host = tc.host
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Authorization", tc.auth)
		r.Header.Set("X-Hoxi-Browser", tc.marker)
		if tc.auth != "" {
			r.Header.Set("X-Hoxi-Agent", "true")
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%+v: %d %s", tc, w.Code, w.Body.String())
		}
	}
	if len(s.scopes) != 0 {
		t.Fatal("stream reserved scope")
	}
	request(t, s, dir, "stream", "action", `{"action":"resize","width":640,"height":480}`, false)
	if w := request(t, s, dir, "stream", "stream", "", false); w.Code != 404 {
		t.Fatal(w.Code)
	}
	for _, sc := range s.scopes {
		if sc.browser != nil || sc.profile != "" {
			t.Fatal("stream started Chrome")
		}
	}
}

func TestStreamLatestAndDisplayACK(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := &screencast{ctx: ctx, cancel: cancel, frame: streamFrame{Width: 640, Height: 480, TabID: "tab"}, latest: make(chan string, 1)}
	finished := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		defer close(finished)
		stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
		defer stop()
		st.serve(conn)
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	st.latest <- "first"
	var frame streamFrame
	if err := conn.ReadJSON(&frame); err != nil || frame.Image != "first" || frame.Width != 640 {
		t.Fatal(frame, err)
	}
	st.latest <- "old"
	<-st.latest
	st.latest <- "newest"
	// Keep a reader active: no second frame may arrive before the display ACK.
	next := make(chan error, 1)
	go func() { next <- conn.ReadJSON(&frame) }()
	select {
	case err := <-next:
		t.Fatalf("frame sent without display ACK: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte("ack")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-next:
		if err != nil || frame.Image != "newest" {
			t.Fatal(frame, err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("display ACK did not release latest frame")
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte("invalid")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("invalid input did not tear down stream")
	}
}

func TestChromeStreamLifecycleAndDensity(t *testing.T) {
	if os.Getenv("HOXI_BROWSER_TEST") != "1" {
		t.Skip("set HOXI_BROWSER_TEST=1 for Chrome integration")
	}
	s := New()
	defer s.Close()
	s.SetAgentToken("secret")
	dir := t.TempDir()
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	pageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<style>body{margin:0}</style><script>let n=0;setInterval(()=>{document.body.style.background='rgb('+(n++%255)+',20,80)'},30)</script>`))
	}))
	defer pageServer.Close()
	call := func(body string, agent bool) map[string]any {
		t.Helper()
		w := request(t, s, dir, "stream", "action", body, agent)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	call(`{"action":"resize","width":640,"height":480,"deviceScaleFactor":2}`, false)
	first := call(fmt.Sprintf(`{"action":"navigate","url":%q}`, pageServer.URL), true)["tabID"].(string)
	if got := call(`{"action":"evaluate","text":"[innerWidth,innerHeight,devicePixelRatio]"}`, true)["result"]; got != "[640,480,2]" {
		t.Fatal(got)
	}
	shot := call(`{"action":"screenshot"}`, true)["image"].(string)
	data, _ := base64.StdEncoding.DecodeString(shot)
	img, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil || img.Width != 1280 || img.Height != 960 {
		t.Fatal(img, err)
	}
	dial := func() *websocket.Conn {
		t.Helper()
		u := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/hoxi/browser/stream?sessionID=stream&directory=" + url.QueryEscape(dir)
		conn, _, err := websocket.DefaultDialer.Dial(u, http.Header{"Origin": []string{"wails://wails"}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}
	read := func(conn *websocket.Conn, width, height int, tab string) {
		t.Helper()
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		var frame streamFrame
		if err := conn.ReadJSON(&frame); err != nil {
			t.Fatal(err)
		}
		if frame.Width != width || frame.Height != height || frame.TabID != tab {
			t.Fatal(frame.Width, frame.Height, frame.TabID)
		}
		data, err := base64.StdEncoding.DecodeString(frame.Image)
		if err != nil {
			t.Fatal(err)
		}
		img, err := jpeg.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		// The UI maps clicks over the whole image, so it must show the whole
		// viewport, not the headless window's content area.
		if diff := img.Width*height - img.Height*width; diff > max(width, height) || -diff > max(width, height) {
			t.Fatalf("frame %dx%d does not cover viewport %dx%d", img.Width, img.Height, width, height)
		}
	}
	closed := func(conn *websocket.Conn) {
		t.Helper()
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				if e, ok := err.(interface{ Timeout() bool }); ok && e.Timeout() {
					t.Fatal("stream did not close")
				}
				return
			}
		}
	}
	conn := dial()
	read(conn, 640, 480, first)
	// Input remains responsive and CDP continues producing frames while UI withholds ACK.
	call(`{"action":"evaluate","text":"document.title='still responsive';1"}`, true)
	time.Sleep(200 * time.Millisecond)
	if err := conn.WriteMessage(websocket.TextMessage, []byte("ack")); err != nil {
		t.Fatal(err)
	}
	read(conn, 640, 480, first)
	replacement := dial()
	closed(conn)
	read(replacement, 640, 480, first)
	call(`{"action":"resize","width":800,"height":600}`, false)
	closed(replacement)
	resized := dial()
	read(resized, 800, 600, first)
	second := call(`{"action":"tab_new"}`, true)["tabID"].(string)
	closed(resized)
	switched := dial()
	read(switched, 800, 600, second)
	call(fmt.Sprintf(`{"action":"tab_select","tabID":%q}`, first), true)
	closed(switched)
	last := dial()
	read(last, 800, 600, first)
	call(`{"action":"close"}`, false)
	closed(last)
	call(`{"action":"navigate","url":"about:blank"}`, true)
	last = dial()
	s.Close()
	closed(last)
}

// A narrow panel is below the headless window's minimum width, and a static page
// never repaints on its own, so the first frame shown must already be correct.
func TestChromeStreamStaticNarrowPageCoversViewport(t *testing.T) {
	if os.Getenv("HOXI_BROWSER_TEST") != "1" {
		t.Skip("set HOXI_BROWSER_TEST=1 for Chrome integration")
	}
	s := New()
	defer s.Close()
	dir := t.TempDir()
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	pageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<style>body{margin:0;height:100vh;background:linear-gradient(red,blue)}</style>`))
	}))
	defer pageServer.Close()
	for _, body := range []string{
		`{"action":"resize","width":420,"height":756,"deviceScaleFactor":2}`,
		fmt.Sprintf(`{"action":"navigate","url":%q}`, pageServer.URL),
	} {
		if w := request(t, s, dir, "static", "action", body, false); w.Code != 200 {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body.String())
		}
	}
	u := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/hoxi/browser/stream?sessionID=static&directory=" + url.QueryEscape(dir)
	conn, _, err := websocket.DefaultDialer.Dial(u, http.Header{"Origin": []string{"wails://wails"}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var frame streamFrame
	if err := conn.ReadJSON(&frame); err != nil {
		t.Fatal(err)
	}
	data, err := base64.StdEncoding.DecodeString(frame.Image)
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if frame.Width != 420 || frame.Height != 756 || img.Width*756 != img.Height*420 {
		t.Fatalf("frame %dx%d (css %dx%d) does not cover the 420x756 viewport", img.Width, img.Height, frame.Width, frame.Height)
	}
}
