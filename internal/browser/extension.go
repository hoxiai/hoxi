package browser

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// A disconnected bridge remains bound until explicitly closed, so a failed
// external connection never silently falls back to an isolated browser.
type extensionBridge struct {
	conn      *websocket.Conn
	done      chan struct{}
	once      sync.Once
	mu        sync.Mutex
	responses chan extensionReply
	// The extension accepts "pointer" mouse input; older ones only click.
	pointer bool
}

type extensionReply struct {
	Result    map[string]any `json:"result"`
	Error     string         `json:"error"`
	Heartbeat bool           `json:"heartbeat"`
}

func (s *Service) SetBridgeURL(base string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	base = strings.Replace(base, "wss://", "https://", 1)
	base = strings.Replace(base, "ws://", "http://", 1)
	s.bridgeURL = strings.TrimRight(base, "/") + "/api/hoxi/browser/extension"
}

func (s *Service) servePair(w http.ResponseWriter, sc *scope) {
	if sc.browser != nil || sc.extension != nil {
		fail(w, 409, errors.New("disconnect the current browser before pairing Chrome"))
		return
	}
	s.mu.Lock()
	base := s.bridgeURL
	s.mu.Unlock()
	if base == "" {
		fail(w, 503, errors.New("Chrome extension bridge is unavailable"))
		return
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		fail(w, 500, errors.New("cannot create Chrome connection code"))
		return
	}
	sc.pairToken = hex.EncodeToString(secret)
	sc.pairExpires = time.Now().Add(2 * time.Minute)
	// This one-use capability is scoped to the UI session, never the service's
	// agent credential, and never returned to an agent request.
	code := base64.RawURLEncoding.EncodeToString([]byte(base + "#" + sc.pairToken))
	writeJSON(w, 200, map[string]any{"code": code, "expiresIn": 120})
}

var extensionOrigin = regexp.MustCompile(`^chrome-extension://[a-p]{32}$`)

// ServeExtension is the sole gateway exception for extension Origins. It grants
// no access to UI APIs; the socket is authenticated by its first message.
func (s *Service) ServeExtension(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != "GET" || !trustedAuthority(r.Host) || len(r.Header.Values("Origin")) != 1 ||
		!extensionOrigin.MatchString(r.Header.Get("Origin")) || r.URL.RawQuery != "" ||
		r.Header.Get("Authorization") != "" || r.Header.Get("X-Hoxi-Agent") != "" {
		fail(w, 403, errors.New("Chrome extension handshake required"))
		return
	}
	u := websocket.Upgrader{HandshakeTimeout: 5 * time.Second, CheckOrigin: func(r *http.Request) bool {
		return extensionOrigin.MatchString(r.Header.Get("Origin"))
	}}
	conn, err := u.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetReadLimit(1024)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var hello struct {
		Token    string   `json:"token"`
		Features []string `json:"features"`
	}
	if conn.ReadJSON(&hello) != nil || len(hello.Token) != 64 {
		return
	}
	s.mu.Lock()
	candidates := make([]*scope, 0, len(s.scopes))
	for _, sc := range s.scopes {
		candidates = append(candidates, sc)
	}
	s.mu.Unlock()
	var owner *scope
	for _, sc := range candidates {
		select {
		case sc.gate <- struct{}{}:
		default:
			continue
		}
		if !sc.closed && sc.browser == nil && sc.extension == nil && time.Now().Before(sc.pairExpires) &&
			subtle.ConstantTimeCompare([]byte(hello.Token), []byte(sc.pairToken)) == 1 {
			owner = sc
			break
		}
		<-sc.gate
	}
	if owner == nil {
		return
	}
	b := &extensionBridge{conn: conn, done: make(chan struct{}), responses: make(chan extensionReply, 1),
		pointer: slices.Contains(hello.Features, "pointer")}
	owner.extension, owner.pairToken = b, ""
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if conn.WriteJSON(map[string]bool{"connected": true}) != nil {
		<-owner.gate
		b.close()
		return
	}
	<-owner.gate
	defer b.close()
	conn.SetReadLimit(20 << 20)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(50 * time.Second))
		var reply extensionReply
		if conn.ReadJSON(&reply) != nil {
			return
		}
		if reply.Heartbeat {
			continue
		}
		select {
		case b.responses <- reply:
		case <-b.done:
			return
		}
	}
}

func (b *extensionBridge) close() {
	b.once.Do(func() {
		close(b.done)
		if b.conn != nil {
			_ = b.conn.Close()
		}
	})
}

func (b *extensionBridge) call(ctx context.Context, action any) (map[string]any, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	select {
	case <-b.done:
		return nil, errors.New("Chrome disconnected; disconnect here before reconnecting")
	default:
	}
	_ = b.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := b.conn.WriteJSON(action); err != nil {
		b.close()
		return nil, err
	}
	select {
	case reply := <-b.responses:
		if reply.Error != "" {
			return nil, errors.New(reply.Error)
		}
		if reply.Result == nil {
			b.close()
			return nil, errors.New("invalid Chrome extension response")
		}
		return reply.Result, nil
	case <-b.done:
		return nil, errors.New("Chrome connection ended")
	case <-ctx.Done():
		b.close()
		return nil, ctx.Err()
	}
}

func (s *Service) serveExtensionAction(w http.ResponseWriter, ctx context.Context, sc *scope, path string, a Action) {
	select {
	case <-sc.extension.done:
		if path == "state" {
			writeJSON(w, 200, map[string]any{"mode": "chrome", "connected": false, "manual": sc.manual, "tabs": []Tab{}, "tabID": "", "url": "", "title": ""})
			return
		}
	default:
	}
	var result map[string]any
	var err error
	if path == "action" && a.Action == "pointer" {
		result, err = sc.pointer(ctx, a)
		a = Action{Action: "state"}
	} else if path == "action" && releasesPointer(a.Action) {
		sc.dropPointer(ctx)
	}
	if path == "state" || a.Action == "control" || a.Action == "resize" {
		a = Action{Action: "state"}
	}
	if path == "frame" {
		a = Action{Action: "screenshot"}
	}
	if err == nil && result == nil {
		result, err = sc.extension.call(ctx, a)
	}
	if err != nil {
		fail(w, 502, err)
		return
	}
	result["manual"], result["mode"], result["connected"] = sc.manual, "chrome", true
	if path == "frame" {
		image, _ := result["image"].(string)
		data, err := base64.StdEncoding.DecodeString(image)
		if err != nil {
			fail(w, 502, err)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(data)
		return
	}
	writeJSON(w, 200, result)
}

// Real Chrome uses periodic captures with the same bounded UI ACK protocol.
// Its viewport is never resized to fit Hoxi's inspector.
func (s *Service) serveExtensionStream(w http.ResponseWriter, r *http.Request, sc *scope) {
	ctx, cancel := context.WithCancel(sc.ctx)
	defer cancel()
	st := &screencast{ctx: ctx, cancel: cancel, latest: make(chan string, 1)}
	select {
	case sc.gate <- struct{}{}:
	case <-r.Context().Done():
		return
	}
	if sc.closed || sc.extension == nil {
		<-sc.gate
		fail(w, 404, errors.New("Chrome not connected"))
		return
	}
	sc.stopStream()
	sc.stream = st
	b := sc.extension
	// Closing an inspector must not cancel an in-flight RPC and detach Chrome.
	// Only scope disposal or a genuine command timeout closes the bridge.
	capture, done := context.WithTimeout(sc.ctx, 5*time.Second)
	initial, err := b.call(capture, Action{Action: "screenshot"})
	done()
	<-sc.gate
	defer func() {
		sc.gate <- struct{}{}
		if sc.stream == st {
			sc.stream = nil
		}
		<-sc.gate
	}()
	if err != nil {
		fail(w, 502, err)
		return
	}
	first, err := extensionFrame(initial)
	if err != nil {
		fail(w, 502, err)
		return
	}
	firstImage := first.Image
	st.frame = first
	st.frame.Image = ""
	st.latest <- firstImage
	u := websocket.Upgrader{HandshakeTimeout: 5 * time.Second, CheckOrigin: func(r *http.Request) bool {
		agent, err := s.authenticate(r)
		return err == nil && !agent && r.Header.Get("Origin") != ""
	}}
	conn, err := u.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	go func() {
		ticker := time.NewTicker(750 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-b.done:
				cancel()
				return
			case <-ticker.C:
			}
			select {
			case sc.gate <- struct{}{}:
			case <-ctx.Done():
				return
			}
			capture, done := context.WithTimeout(sc.ctx, 5*time.Second)
			result, err := b.call(capture, Action{Action: "screenshot"})
			done()
			<-sc.gate
			if err != nil {
				cancel()
				return
			}
			frame, err := extensionFrame(result)
			if err != nil || st.frame.Width != frame.Width || st.frame.Height != frame.Height || st.frame.TabID != frame.TabID {
				cancel()
				return
			}
			select {
			case <-st.latest:
			default:
			}
			select {
			case st.latest <- frame.Image:
			case <-ctx.Done():
				return
			}
		}
	}()
	st.serve(conn)
}

func extensionFrame(result map[string]any) (streamFrame, error) {
	data, err := json.Marshal(result)
	var frame streamFrame
	if err != nil {
		return frame, err
	}
	if json.Unmarshal(data, &frame) != nil || frame.Width < 1 || frame.Height < 1 || frame.Width > 16384 || frame.Height > 16384 ||
		frame.Width*frame.Height > 8388608 || frame.Image == "" || frame.TabID == "" || len(frame.Image) > maxStreamImage {
		return frame, errors.New("invalid Chrome screenshot")
	}
	return frame, nil
}
