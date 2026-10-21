package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// peer is one live WebSocket to GoPanel. GoPanel opens streams; each stream
// becomes one in-process request against the configured handler.
type peer struct {
	conn      *websocket.Conn
	handler   http.Handler
	writeMu   sync.Mutex
	streams   sync.Map // uint64 -> *stream
	closed    chan struct{}
	closeOnce sync.Once
}

type chunk struct {
	payload []byte
	end     bool
}

type stream struct {
	bodyReader *io.PipeReader
	bodyWriter *io.PipeWriter
	incoming   chan chunk
	cancel     context.CancelFunc
	closed     chan struct{}
	closeOnce  sync.Once
}

func newPeer(conn *websocket.Conn, handler http.Handler) *peer {
	conn.SetReadLimit(maxFrameSize)
	return &peer{conn: conn, handler: handler, closed: make(chan struct{})}
}

func (p *peer) close() {
	p.closeOnce.Do(func() {
		close(p.closed)
		_ = p.conn.Close()
		p.streams.Range(func(key, value any) bool {
			value.(*stream).close()
			p.streams.Delete(key)
			return true
		})
	})
}

func (p *peer) send(f frame) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_ = p.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	return p.conn.WriteMessage(websocket.BinaryMessage, encodeFrame(f))
}

func (p *peer) reset(streamID uint64, reason string) {
	_ = p.send(frame{Type: frameReset, StreamID: streamID, Payload: []byte(reason)})
}

func (p *peer) sendServiceSync(hostname, version string, targetPort int) error {
	f, err := jsonFrame(frameServiceSync, 0, serviceSync{
		Hostname: hostname,
		Version:  version,
		Services: []serviceRecord{{
			PublicID: ServicePublicID, Name: "Hoxi Desktop Gateway", Scheme: "http",
			TargetHost: "127.0.0.1", TargetPort: targetPort, Enabled: true,
		}},
	})
	if err != nil {
		return err
	}
	return p.send(f)
}

func (p *peer) heartbeat(ctx context.Context) {
	ticker := time.NewTicker(heartbeatPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			p.close()
			return
		case <-p.closed:
			return
		case now := <-ticker.C:
			if p.send(frame{Type: framePing, Payload: []byte(now.Format(time.RFC3339Nano))}) != nil {
				p.close()
				return
			}
		}
	}
}

// readLoop dispatches frames until the connection fails or ctx ends.
func (p *peer) readLoop(ctx context.Context) error {
	_ = p.conn.SetReadDeadline(time.Now().Add(readTimeout))
	for {
		messageType, data, err := p.conn.ReadMessage()
		if err != nil {
			return err
		}
		_ = p.conn.SetReadDeadline(time.Now().Add(readTimeout))
		if messageType != websocket.BinaryMessage {
			continue
		}
		f, err := decodeFrame(data)
		if err != nil {
			continue
		}
		switch f.Type {
		case frameOpen:
			p.handleOpen(ctx, f)
		case frameData:
			p.handleData(f)
		case frameEnd:
			p.push(f.StreamID, chunk{end: true})
		case frameReset:
			p.closeStream(f.StreamID)
		case framePing:
			_ = p.send(frame{Type: framePong, Payload: f.Payload})
		}
	}
}

func (p *peer) handleOpen(ctx context.Context, f frame) {
	var open openFrame
	if json.Unmarshal(f.Payload, &open) != nil {
		p.reset(f.StreamID, "invalid open frame")
		return
	}
	streamCtx, cancel := context.WithCancel(ctx)
	s := &stream{incoming: make(chan chunk, 16), cancel: cancel, closed: make(chan struct{})}
	s.bodyReader, s.bodyWriter = io.Pipe()
	p.streams.Store(f.StreamID, s)
	go s.pumpBody()
	go p.serve(streamCtx, f.StreamID, s, open)
}

func (p *peer) handleData(f frame) {
	_, payload, err := decodeData(f.Payload)
	if err != nil {
		p.closeStream(f.StreamID)
		return
	}
	p.push(f.StreamID, chunk{payload: append([]byte(nil), payload...)})
}

func (p *peer) push(streamID uint64, c chunk) {
	value, ok := p.streams.Load(streamID)
	if !ok {
		return
	}
	select {
	case value.(*stream).incoming <- c:
	default:
		p.closeStream(streamID)
		p.reset(streamID, "hoxi tunnel stream buffer exceeded")
	}
}

func (p *peer) closeStream(streamID uint64) {
	if value, ok := p.streams.LoadAndDelete(streamID); ok {
		value.(*stream).close()
	}
}

func (s *stream) close() {
	s.closeOnce.Do(func() {
		close(s.closed)
		s.cancel()
		_ = s.bodyWriter.CloseWithError(errors.New("tunnel stream closed"))
	})
}

func (s *stream) pumpBody() {
	for {
		select {
		case <-s.closed:
			return
		case c := <-s.incoming:
			if c.end {
				_ = s.bodyWriter.Close()
				return
			}
			if _, err := s.bodyWriter.Write(c.payload); err != nil {
				return
			}
		}
	}
}

func (p *peer) serve(ctx context.Context, streamID uint64, s *stream, open openFrame) {
	defer func() {
		p.streams.Delete(streamID)
		s.close()
	}()
	if open.ServiceID != ServicePublicID {
		p.reset(streamID, "hoxi tunnel service is unavailable")
		return
	}
	if open.Upgrade {
		p.reset(streamID, "websocket is not supported by the hoxi tunnel")
		return
	}
	req, err := buildRequest(ctx, open, s.bodyReader)
	if err != nil {
		p.reset(streamID, err.Error())
		return
	}
	w := &responseWriter{peer: p, streamID: streamID, header: http.Header{}}
	if !serveRecovering(p.handler, w, req) {
		p.reset(streamID, "hoxi gateway aborted the request")
		return
	}
	if ctx.Err() != nil {
		return
	}
	w.finish()
}

// serveRecovering reports false when the handler panicked, including the
// http.ErrAbortHandler that ReverseProxy uses to abort a response mid-stream.
func serveRecovering(handler http.Handler, w http.ResponseWriter, r *http.Request) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	handler.ServeHTTP(w, r)
	return true
}

// Headers GoPanel or the phone might carry that must never reach the
// loopback gateway: hop-by-hop fields, browser identity and credentials.
func droppedRequestHeader(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer",
		"transfer-encoding", "upgrade", "host", "origin", "referer", "cookie", "authorization", "x-hoxi-token":
		return true
	}
	lower := strings.ToLower(key)
	return strings.HasPrefix(lower, "sec-") || strings.HasPrefix(lower, "x-forwarded-") || strings.HasPrefix(lower, "x-gopanel-")
}

func buildRequest(ctx context.Context, open openFrame, body io.Reader) (*http.Request, error) {
	requestURI := strings.TrimSpace(open.RequestURI)
	if requestURI == "" || requestURI[0] != '/' || strings.HasPrefix(requestURI, "//") {
		return nil, errors.New("invalid tunnel request uri")
	}
	target, err := url.ParseRequestURI(requestURI)
	if err != nil {
		return nil, fmt.Errorf("invalid tunnel request uri: %w", err)
	}
	method := strings.ToUpper(strings.TrimSpace(open.Method))
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://127.0.0.1"+target.RequestURI(), body)
	if err != nil {
		return nil, err
	}
	for key, values := range open.Headers {
		if droppedRequestHeader(key) {
			continue
		}
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	req.Host = "127.0.0.1"
	req.RemoteAddr = "tunnel"
	req.ContentLength = -1
	if n, err := strconv.ParseInt(req.Header.Get("Content-Length"), 10, 64); err == nil && n >= 0 {
		req.ContentLength = n
	}
	if req.ContentLength == 0 {
		req.Body = http.NoBody
	}
	return req, nil
}

// responseWriter turns handler output into Response/Data frames. Every Write
// is sent immediately, so SSE needs no extra buffering and Flush is a no-op.
type responseWriter struct {
	peer        *peer
	streamID    uint64
	header      http.Header
	wroteHeader bool
	err         error
}

func (w *responseWriter) Header() http.Header { return w.header }

func (w *responseWriter) WriteHeader(status int) {
	if w.wroteHeader || w.err != nil || status < 200 {
		return
	}
	w.wroteHeader = true
	f, err := jsonFrame(frameResponse, w.streamID, responseFrame{Status: status, Headers: w.header.Clone()})
	if err != nil {
		w.err = err
		return
	}
	w.err = w.peer.send(f)
}

func (w *responseWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		if w.header.Get("Content-Type") == "" && len(data) > 0 {
			w.header.Set("Content-Type", http.DetectContentType(data))
		}
		w.WriteHeader(http.StatusOK)
	}
	if w.err != nil {
		return 0, w.err
	}
	for offset := 0; offset < len(data); offset += chunkSize {
		end := min(offset+chunkSize, len(data))
		if err := w.peer.send(frame{Type: frameData, StreamID: w.streamID, Payload: encodeData(dataHTTP, data[offset:end])}); err != nil {
			w.err = err
			return offset, err
		}
	}
	return len(data), nil
}

func (w *responseWriter) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
}

func (w *responseWriter) finish() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.err == nil {
		_ = w.peer.send(frame{Type: frameEnd, StreamID: w.streamID})
	}
}
