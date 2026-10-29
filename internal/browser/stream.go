package browser

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image/jpeg"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/gorilla/websocket"
)

// The bound applies to the base64 image, before the small JSON envelope.
const maxStreamImage = 16 << 20

type streamFrame struct {
	Image  string `json:"image"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	TabID  string `json:"tabID"`
}

type screencast struct {
	ctx    context.Context
	cancel context.CancelFunc
	tab    context.Context
	window bool
	frame  streamFrame
	events chan *page.EventScreencastFrame
	latest chan string
}

// Called only under the scope gate. Cancellation never waits on a socket write.
func (sc *scope) stopStream() {
	st := sc.stream
	if st == nil {
		return
	}
	sc.stream = nil
	st.cancel()
	if st.tab == nil {
		return
	}
	ctx, cancel := context.WithTimeout(st.tab, 3*time.Second)
	defer cancel()
	_ = chromedp.Run(ctx, page.StopScreencast())
}

func (st *screencast) receive(event any) {
	if ev, ok := event.(*page.EventScreencastFrame); ok {
		if len(ev.Data) > maxStreamImage {
			st.cancel()
			return
		}
		select {
		case <-st.ctx.Done():
			return
		default:
		}
		select {
		case st.events <- ev:
		default:
			// Fail closed if CDP ever exceeds the bounded ACK worker queue.
			st.cancel()
		}
	}
}

func (st *screencast) pump() {
	for {
		select {
		case <-st.ctx.Done():
			return
		case ev := <-st.events:
			ctx, cancel := context.WithTimeout(st.ctx, 3*time.Second)
			err := chromedp.Run(ctx, page.ScreencastFrameAck(ev.SessionID))
			cancel()
			if err != nil || len(ev.Data) > maxStreamImage {
				st.cancel()
				return
			}
			// Chrome may emit frames at the stale view size before it re-derives
			// the emulated one. The UI maps clicks over the whole image, so never
			// show a frame that does not cover the viewport.
			if st.window {
				w, h := st.decodeDimensions(ev.Data)
				if w <= 0 || h <= 0 {
					continue
				}
				st.frame.Width, st.frame.Height = w, h
			} else if !st.coversViewport(ev.Data) {
				continue
			}
			// CDP ACKs are independent of display ACKs. Keep only the newest image.
			select {
			case <-st.latest:
			default:
			}
			select {
			case st.latest <- ev.Data:
			case <-st.ctx.Done():
				return
			}
		}
	}
}

func (st *screencast) decodeDimensions(data string) (int, int) {
	image, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return 0, 0
	}
	config, err := jpeg.DecodeConfig(bytes.NewReader(image))
	if err != nil {
		return 0, 0
	}
	return config.Width, config.Height
}

// coversViewport reports whether a frame has the viewport's aspect ratio,
// allowing for rounding when Chrome scales it.
func (st *screencast) coversViewport(data string) bool {
	w, h := st.decodeDimensions(data)
	if w <= 0 || h <= 0 {
		return false
	}
	width, height := st.frame.Width, st.frame.Height
	diff := w*height - h*width
	return max(diff, -diff) <= max(w, h, width, height)
}

func (s *Service) serveStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	agent, err := s.authenticate(r)
	if err != nil || agent || r.Header.Get("Origin") == "" || len(r.Header.Values("Origin")) != 1 {
		fail(w, http.StatusForbidden, errors.New("stream requires an explicit trusted UI Origin"))
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		fail(w, 405, errors.New("method not allowed"))
		return
	}
	key, err := s.requestScope(r)
	if err != nil {
		fail(w, 400, err)
		return
	}
	setup, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	sc, err := s.acquire(setup, key, false)
	if err != nil {
		fail(w, 503, err)
		return
	}
	if sc == nil {
		fail(w, 404, errors.New("browser not started"))
		return
	}
	if sc.browser == nil {
		if sc.extension != nil {
			<-sc.gate
			s.serveExtensionStream(w, r, sc)
			return
		}
		<-sc.gate
		fail(w, 404, errors.New("browser not started"))
		return
	}
	if !websocket.IsWebSocketUpgrade(r) {
		<-sc.gate
		fail(w, 400, errors.New("WebSocket upgrade required"))
		return
	}
	// Attach/apply metrics and start CDP under the gate, never upgrade or write there.
	sc.stopStream()
	err = sc.withTab(setup)
	if err == nil && !sc.window {
		// Chrome sizes screencast frames from the browser view, which can still be
		// the headless window's content area rather than the emulated viewport
		// (always so right after launch). Only a real metrics change re-derives it,
		// and identical overrides are ignored, so step one pixel away and back.
		nudge := sc.viewport()
		if nudge.height > 1 {
			nudge.height--
		} else {
			nudge.height++
		}
		if err = sc.withTab(setup, nudge.action()); err != nil {
			delete(sc.viewports, sc.tabID)
		} else {
			sc.viewports[sc.tabID] = nudge
			err = sc.withTab(setup)
		}
	}
	var st *screencast
	if err == nil {
		tab := sc.tabs[sc.tabID]
		ctx, stop := context.WithCancel(tab)
		st = &screencast{ctx: ctx, cancel: stop, tab: tab, window: sc.window,
			frame:  streamFrame{Width: sc.width, Height: sc.height, TabID: sc.tabID},
			events: make(chan *page.EventScreencastFrame, 8), latest: make(chan string, 1)}
		chromedp.ListenTarget(ctx, st.receive)
		sc.stream = st
		go st.pump()
		err = sc.withTab(setup, page.StartScreencast().WithFormat(page.ScreencastFormatJpeg).WithQuality(92))
	}
	if err != nil && st != nil {
		sc.stopStream()
	}
	// An open stream keeps the browser; idle time starts when it ends.
	sc.lastUsed = s.now()
	<-sc.gate
	if err != nil {
		fail(w, 502, err)
		return
	}
	defer func() {
		st.cancel()
		sc.gate <- struct{}{}
		if sc.stream == st {
			sc.stopStream()
		}
		sc.lastUsed = s.now()
		<-sc.gate
	}()
	upgrader := websocket.Upgrader{HandshakeTimeout: 5 * time.Second, CheckOrigin: func(r *http.Request) bool {
		agent, err := s.authenticate(r)
		return err == nil && !agent && r.Header.Get("Origin") != ""
	}}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	stopClose := context.AfterFunc(st.ctx, func() { _ = conn.Close() })
	defer stopClose()
	st.serve(conn)
}

func (st *screencast) serve(conn *websocket.Conn) {
	acks := make(chan struct{}, 1)
	var awaiting atomic.Bool
	conn.SetReadLimit(16)
	_ = conn.SetReadDeadline(time.Now().Add(45 * time.Second))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(45 * time.Second)) })
	go func() {
		defer st.cancel()
		for {
			kind, data, err := conn.ReadMessage()
			if err != nil || kind != websocket.TextMessage || string(data) != "ack" || !awaiting.Swap(false) {
				return
			}
			select {
			case acks <- struct{}{}:
			case <-st.ctx.Done():
				return
			}
		}
	}()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	var ackTimer <-chan time.Time
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	timer.Stop()
	ready := st.latest
	for {
		select {
		case <-st.ctx.Done():
			return
		case <-ackTimer:
			return
		case <-ticker.C:
			if conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)) != nil {
				return
			}
		case <-acks:
			timer.Stop()
			ackTimer = nil
			ready = st.latest
		case image := <-ready:
			frame := st.frame
			frame.Image = image
			awaiting.Store(true)
			_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if conn.WriteJSON(frame) != nil {
				return
			}
			ready = nil
			timer.Reset(30 * time.Second)
			ackTimer = timer.C
		}
	}
}
