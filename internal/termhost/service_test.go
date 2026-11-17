package termhost

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type memPipe struct {
	r *io.PipeReader
	w *io.PipeWriter
}

func (m *memPipe) Read(p []byte) (int, error)  { return m.r.Read(p) }
func (m *memPipe) Write(p []byte) (int, error) { return m.w.Write(p) }
func (m *memPipe) Close() error {
	_ = m.w.Close()
	return m.r.Close()
}

type pipeListener struct {
	ch     chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{
		ch:     make(chan net.Conn, 4),
		closed: make(chan struct{}),
	}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 80} }

func (l *pipeListener) DialContext(_ context.Context, _, _ string) (net.Conn, error) {
	c1, c2 := net.Pipe()
	select {
	case l.ch <- c2:
		return c1, nil
	case <-l.closed:
		_ = c1.Close()
		_ = c2.Close()
		return nil, net.ErrClosed
	}
}

var _ = bufio.NewReader

func TestTerminalServiceLifecycleAndWebSocket(t *testing.T) {
	var resizedMu sync.Mutex
	var gotCols, gotRows uint16

	svc := NewService(func(cwd, _ string, _, _ uint16) (*StreamHandle, error) {
		inR, inW := io.Pipe()
		outR, outW := io.Pipe()
		go func() {
			_, _ = outW.Write([]byte("welcome:" + cwd + "\n"))
		}()
		go func() {
			defer outW.Close()
			buf := make([]byte, 256)
			for {
				n, err := inR.Read(buf)
				if n > 0 {
					_, _ = outW.Write([]byte("echo:" + string(buf[:n])))
				}
				if err != nil {
					return
				}
			}
		}()
		return &StreamHandle{
			RW: &memPipe{r: outR, w: inW},
			Resize: func(c, r uint16) error {
				resizedMu.Lock()
				gotCols, gotRows = c, r
				resizedMu.Unlock()
				return nil
			},
			Kill: func() error {
				_ = inW.Close()
				return outR.Close()
			},
		}, nil
	})
	defer svc.CloseAll()

	// 1. Create PTY via in-memory recorder
	reqCreate := httptest.NewRequest(http.MethodPost, "/pty?directory=/tmp/proj", strings.NewReader(`{"title":"Dev Shell"}`))
	recCreate := httptest.NewRecorder()
	svc.ServeHTTP(recCreate, reqCreate)
	if recCreate.Code != http.StatusOK {
		t.Fatalf("create status = %d: %s", recCreate.Code, recCreate.Body.String())
	}
	var created struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Cwd   string `json:"cwd"`
	}
	if err := json.Unmarshal(recCreate.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.ID, "pty_") || created.Cwd != "/tmp/proj" {
		t.Fatalf("unexpected create response: %+v", created)
	}

	// 2. Resize PTY
	reqResize := httptest.NewRequest(http.MethodPut, "/pty/"+created.ID, bytes.NewBufferString(`{"size":{"cols":132,"rows":42}}`))
	recResize := httptest.NewRecorder()
	svc.ServeHTTP(recResize, reqResize)
	if recResize.Code != http.StatusOK {
		t.Fatalf("resize status = %d", recResize.Code)
	}
	resizedMu.Lock()
	c, r := gotCols, gotRows
	resizedMu.Unlock()
	if c != 132 || r != 42 {
		t.Fatalf("expected resize 132x42, got %dx%d", c, r)
	}

	// 3. Connect WebSocket over in-memory net.Pipe (works inside Seatbelt with zero TCP bind)
	pl := newPipeListener()
	defer pl.Close()
	httpSrv := &http.Server{Handler: svc}
	go func() { _ = httpSrv.Serve(pl) }()
	defer httpSrv.Close()

	dialer := websocket.Dialer{NetDialContext: pl.DialContext}
	conn, _, err := dialer.Dial("ws://127.0.0.1/pty/"+created.ID+"/connect", nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, initialData, err := conn.ReadMessage()
	if err != nil || !strings.Contains(string(initialData), "welcome:/tmp/proj") {
		t.Fatalf("expected replay welcome banner, got %q (err=%v)", string(initialData), err)
	}

	if err := conn.WriteMessage(websocket.TextMessage, []byte("pwd\n")); err != nil {
		t.Fatalf("ws write: %v", err)
	}
	_, echoed, err := conn.ReadMessage()
	if err != nil || !strings.Contains(string(echoed), "echo:pwd") {
		t.Fatalf("expected echoed input, got %q (err=%v)", string(echoed), err)
	}

	// 4. Delete PTY
	reqDel := httptest.NewRequest(http.MethodDelete, "/pty/"+created.ID, nil)
	recDel := httptest.NewRecorder()
	svc.ServeHTTP(recDel, reqDel)
	if recDel.Code != http.StatusOK {
		t.Fatalf("delete pty status = %d", recDel.Code)
	}
}
