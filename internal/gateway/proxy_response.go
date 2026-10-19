package gateway

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/hoxiai/hoxi/internal/runner"
)

// Runner engine handles are implementation details, not additional chats.
func filterRunnerResponse(resp *http.Response, svc *runner.Service) error {
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		resp.Body = &runnerEventReader{source: resp.Body, reader: bufio.NewReader(resp.Body), allow: svc.AllowOpenCodeSSELine}
		resp.ContentLength = -1
		resp.Header.Del("Content-Length")
		return nil
	}
	if resp.StatusCode != http.StatusOK || resp.Request == nil || resp.Request.URL.Path != "/session" {
		return nil
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	var sessions []json.RawMessage
	if json.Unmarshal(raw, &sessions) == nil {
		kept := make([]json.RawMessage, 0, len(sessions))
		for _, item := range sessions {
			var info struct {
				ID       string                 `json:"id"`
				Metadata map[string]interface{} `json:"metadata"`
			}
			if json.Unmarshal(item, &info) == nil && (svc.IsManagedOpenCodeSession(info.ID) || info.Metadata["hoxiConversation"] != nil) {
				continue
			}
			kept = append(kept, item)
		}
		raw, err = json.Marshal(kept)
		if err != nil {
			return err
		}
	}
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	resp.ContentLength = int64(len(raw))
	resp.Header.Del("Content-Length")
	return nil
}

type runnerEventReader struct {
	source  io.ReadCloser
	reader  *bufio.Reader
	allow   func(string) bool
	pending []byte
}

func (r *runnerEventReader) Read(p []byte) (int, error) {
	for len(r.pending) == 0 {
		line, err := r.reader.ReadBytes('\n')
		if len(line) > 0 && r.allow(string(line)) {
			r.pending = line
		}
		if err != nil && len(r.pending) == 0 {
			return 0, err
		}
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}
func (r *runnerEventReader) Close() error { return r.source.Close() }

type sseTapReadCloser struct {
	rc       io.ReadCloser
	onLine   func(string)
	buf      []byte
	dropping bool
}

const maxSSETapLineSize = 64 * 1024

func newSSETapReadCloser(rc io.ReadCloser, onLine func(string)) io.ReadCloser {
	return &sseTapReadCloser{rc: rc, onLine: onLine}
}

func (t *sseTapReadCloser) Read(p []byte) (int, error) {
	n, err := t.rc.Read(p)
	if n > 0 && t.onLine != nil {
		for remaining := p[:n]; len(remaining) > 0; {
			end := bytes.IndexByte(remaining, '\n')
			fragment := remaining
			if end >= 0 {
				fragment = remaining[:end]
			}
			if !t.dropping {
				if len(t.buf)+len(fragment) > maxSSETapLineSize {
					t.dropping = true
					t.buf = t.buf[:0]
				} else {
					t.buf = append(t.buf, fragment...)
				}
			}
			if end < 0 {
				break
			}
			if !t.dropping {
				t.onLine(strings.TrimRight(string(t.buf), "\r"))
			}
			t.buf = t.buf[:0]
			t.dropping = false
			remaining = remaining[end+1:]
		}
	}
	return n, err
}

func (t *sseTapReadCloser) Close() error {
	return t.rc.Close()
}
