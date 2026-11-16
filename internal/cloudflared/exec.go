package cloudflared

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"sync"

	"github.com/hoxiai/hoxi/internal/subproc"
)

// handle is the subset of a running connector process the manager needs.
type handle interface {
	Wait() error
	Kill() error
}

// runFunc spawns the connector and streams its output line by line.
type runFunc func(ctx context.Context, bin string, env, args []string, onLine func(string)) (handle, error)

type cmdHandle struct{ cmd *exec.Cmd }

func (h cmdHandle) Wait() error { return h.cmd.Wait() }

func (h cmdHandle) Kill() error {
	if h.cmd.Process == nil {
		return nil
	}
	return h.cmd.Process.Kill()
}

// execRun starts cloudflared with a sanitized, non-interactive environment. The
// connector token is passed through env (TUNNEL_TOKEN), never on argv, so it
// cannot be read from the process list.
func execRun(ctx context.Context, bin string, env, args []string, onLine func(string)) (handle, error) {
	cmd := subproc.CommandContext(ctx, bin, args...)
	writer := &lineWriter{onLine: onLine}
	cmd.Stdout = writer
	cmd.Stderr = writer
	cmd.Env = append(append(os.Environ(), "NO_COLOR=1"), env...)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmdHandle{cmd: cmd}, nil
}

const maxLineBufferBytes = 64 << 10

// lineWriter splits a process output stream into lines and forwards each one.
type lineWriter struct {
	mu     sync.Mutex
	buf    []byte
	onLine func(string)
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := string(bytes.TrimRight(w.buf[:i], "\r"))
		w.buf = w.buf[i+1:]
		if w.onLine != nil {
			w.onLine(line)
		}
	}
	for len(w.buf) > maxLineBufferBytes {
		line := string(bytes.TrimRight(w.buf[:maxLineBufferBytes], "\r"))
		w.buf = w.buf[maxLineBufferBytes:]
		if w.onLine != nil {
			w.onLine(line)
		}
	}
	return len(p), nil
}
