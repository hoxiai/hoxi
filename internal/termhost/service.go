package termhost

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
)

const maxReplayBytes = 128 * 1024

// StreamHandle encapsulates one allocated terminal stream.
type StreamHandle struct {
	RW     io.ReadWriteCloser
	Resize func(cols, rows uint16) error
	Kill   func() error
	Wait   func() error
}

// Opener allocates a StreamHandle for a requested working directory and title.
type Opener func(cwd, title string, cols, rows uint16) (*StreamHandle, error)

// Session represents one active terminal session managed by Service.
type Session struct {
	id     string
	title  string
	cwd    string
	handle *StreamHandle

	mu      sync.Mutex
	replay  []byte
	clients map[*wsClient]struct{}
	closed  bool
}

type wsClient struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (c *wsClient) send(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteMessage(websocket.BinaryMessage, data)
}

// Service manages terminal sessions over HTTP and WebSocket (/pty routes).
type Service struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	opener   Opener
	upgrader websocket.Upgrader
}

// NewService constructs a terminal Service with a pluggable Opener.
func NewService(opener Opener) *Service {
	return &Service{
		sessions: make(map[string]*Session),
		opener:   opener,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  16 * 1024,
			WriteBufferSize: 16 * 1024,
			CheckOrigin:     func(_ *http.Request) bool { return true },
		},
	}
}

// CloseAll terminates all open terminal sessions.
func (s *Service) CloseAll() {
	if s == nil {
		return
	}
	s.mu.Lock()
	list := make([]*Session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		list = append(list, sess)
	}
	s.sessions = make(map[string]*Session)
	s.mu.Unlock()

	for _, sess := range list {
		sess.shutdown()
	}
}

// ServeHTTP handles /api/hoxi/pty, /api/hoxi/pty/{id}, and /api/hoxi/pty/{id}/connect
// (plus legacy /pty/* and /api/pty/* routes).
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cleanPath := r.URL.Path
	switch {
	case strings.HasPrefix(cleanPath, "/api/hoxi/pty"):
		cleanPath = strings.TrimPrefix(cleanPath, "/api/hoxi")
	case strings.HasPrefix(cleanPath, "/api/pty"):
		cleanPath = strings.TrimPrefix(cleanPath, "/api")
	}
	trimmed := strings.Trim(strings.TrimPrefix(cleanPath, "/pty"), "/")
	if trimmed == "" {
		switch r.Method {
		case http.MethodGet:
			s.handleList(w)
		case http.MethodPost:
			s.handleCreate(w, r)
		default:
			writePTYError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	parts := strings.Split(trimmed, "/")
	id := strings.TrimSpace(parts[0])
	if len(parts) == 2 && parts[1] == "connect" {
		s.handleConnect(w, r, id)
		return
	}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			s.handleGet(w, id)
		case http.MethodPut, http.MethodPatch:
			s.handleUpdate(w, r, id)
		case http.MethodDelete:
			s.handleDelete(w, id)
		default:
			writePTYError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}
	writePTYError(w, http.StatusNotFound, "not found")
}

func (s *Service) handleList(w http.ResponseWriter) {
	s.mu.RLock()
	out := make([]map[string]any, 0, len(s.sessions))
	for _, sess := range s.sessions {
		sess.mu.Lock()
		title := sess.title
		sess.mu.Unlock()
		out = append(out, map[string]any{
			"id":     sess.id,
			"title":  title,
			"cwd":    sess.cwd,
			"status": "running",
		})
	}
	s.mu.RUnlock()
	writePTYJSON(w, http.StatusOK, out)
}

func (s *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	if s.opener == nil {
		writePTYError(w, http.StatusServiceUnavailable, "terminal opener is not configured")
		return
	}
	var body struct {
		Cwd   string `json:"cwd"`
		Title string `json:"title"`
		Cols  uint16 `json:"cols"`
		Rows  uint16 `json:"rows"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 32<<10)).Decode(&body)
	cwd := strings.TrimSpace(body.Cwd)
	if cwd == "" {
		cwd = strings.TrimSpace(r.URL.Query().Get("directory"))
	}
	title := strings.TrimSpace(body.Title)
	if title == "" {
		title = "Hoxi Terminal"
	}
	handle, err := s.opener(cwd, title, body.Cols, body.Rows)
	if err != nil {
		writePTYError(w, http.StatusInternalServerError, err.Error())
		return
	}
	id := newPTYID()
	sess := &Session{
		id:      id,
		title:   title,
		cwd:     cwd,
		handle:  handle,
		clients: make(map[*wsClient]struct{}),
	}
	s.mu.Lock()
	s.sessions[id] = sess
	s.mu.Unlock()

	go s.pumpOutput(sess)
	if handle.Wait != nil {
		go func() {
			_ = handle.Wait()
			s.removeSession(id)
		}()
	}
	writePTYJSON(w, http.StatusOK, map[string]any{
		"id":     id,
		"title":  title,
		"cwd":    cwd,
		"status": "running",
	})
}

func (s *Service) handleGet(w http.ResponseWriter, id string) {
	s.mu.RLock()
	sess := s.sessions[id]
	s.mu.RUnlock()
	if sess == nil {
		writePTYError(w, http.StatusNotFound, "terminal session not found")
		return
	}
	sess.mu.Lock()
	title := sess.title
	sess.mu.Unlock()
	writePTYJSON(w, http.StatusOK, map[string]any{
		"id":     sess.id,
		"title":  title,
		"cwd":    sess.cwd,
		"status": "running",
	})
}

func (s *Service) handleUpdate(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.RLock()
	sess := s.sessions[id]
	s.mu.RUnlock()
	if sess == nil {
		writePTYError(w, http.StatusNotFound, "terminal session not found")
		return
	}
	var body struct {
		Title string `json:"title"`
		Size  *struct {
			Cols uint16 `json:"cols"`
			Rows uint16 `json:"rows"`
		} `json:"size"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writePTYError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(body.Title) != "" {
		sess.mu.Lock()
		sess.title = strings.TrimSpace(body.Title)
		sess.mu.Unlock()
	}
	if body.Size != nil && sess.handle != nil && sess.handle.Resize != nil {
		_ = sess.handle.Resize(body.Size.Cols, body.Size.Rows)
	}
	writePTYJSON(w, http.StatusOK, map[string]any{"ok": true, "id": sess.id})
}

func (s *Service) handleDelete(w http.ResponseWriter, id string) {
	s.removeSession(id)
	writePTYJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Service) handleConnect(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.RLock()
	sess := s.sessions[id]
	s.mu.RUnlock()
	if sess == nil {
		writePTYError(w, http.StatusNotFound, "terminal session not found")
		return
	}
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	client := &wsClient{conn: conn}

	sess.mu.Lock()
	if sess.closed {
		sess.mu.Unlock()
		_ = conn.Close()
		return
	}
	snapshot := append([]byte(nil), sess.replay...)
	sess.clients[client] = struct{}{}
	sess.mu.Unlock()

	if len(snapshot) > 0 {
		_ = client.send(snapshot)
	}

	defer func() {
		sess.mu.Lock()
		delete(sess.clients, client)
		sess.mu.Unlock()
		_ = conn.Close()
	}()

	for {
		msgType, data, readErr := conn.ReadMessage()
		if readErr != nil {
			return
		}
		if (msgType == websocket.TextMessage || msgType == websocket.BinaryMessage) && len(data) > 0 {
			if sess.handle != nil && sess.handle.RW != nil {
				_, _ = sess.handle.RW.Write(data)
			}
		}
	}
}

func (s *Service) pumpOutput(sess *Session) {
	if sess.handle == nil || sess.handle.RW == nil {
		return
	}
	buf := make([]byte, 8192)
	for {
		n, err := sess.handle.RW.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			sess.mu.Lock()
			sess.replay = append(sess.replay, chunk...)
			if len(sess.replay) > maxReplayBytes {
				sess.replay = append([]byte(nil), sess.replay[len(sess.replay)-maxReplayBytes:]...)
			}
			clients := make([]*wsClient, 0, len(sess.clients))
			for c := range sess.clients {
				clients = append(clients, c)
			}
			sess.mu.Unlock()
			for _, c := range clients {
				_ = c.send(chunk)
			}
		}
		if err != nil {
			s.removeSession(sess.id)
			return
		}
	}
}

func (s *Service) removeSession(id string) {
	s.mu.Lock()
	sess, ok := s.sessions[id]
	if ok {
		delete(s.sessions, id)
	}
	s.mu.Unlock()
	if ok && sess != nil {
		sess.shutdown()
	}
}

func (sess *Session) shutdown() {
	sess.mu.Lock()
	if sess.closed {
		sess.mu.Unlock()
		return
	}
	sess.closed = true
	clients := make([]*wsClient, 0, len(sess.clients))
	for c := range sess.clients {
		clients = append(clients, c)
	}
	sess.clients = make(map[*wsClient]struct{})
	sess.mu.Unlock()

	for _, c := range clients {
		_ = c.conn.Close()
	}
	if sess.handle != nil && sess.handle.Kill != nil {
		_ = sess.handle.Kill()
	}
}

func newPTYID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "pty_" + hex.EncodeToString(b[:])
}

func writePTYJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writePTYError(w http.ResponseWriter, status int, msg string) {
	writePTYJSON(w, status, map[string]any{"error": msg})
}
