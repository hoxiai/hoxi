package agent

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hoxiai/hoxi/internal/subproc"
)

const (
	stdioMCPInitTimeout = 15 * time.Second
	stdioMCPIdleTTL     = 5 * time.Minute
)

var errDeadStdioSession = errors.New("stdio MCP session closed before request dispatch")

var (
	defaultStdioPool  = newStdioMCPPool(stdioMCPIdleTTL)
	stdioSpawnCounter atomic.Int64
)

// CloseStdioMCPPool terminates all pooled local stdio MCP server processes.
func CloseStdioMCPPool() {
	defaultStdioPool.closeAll()
}

// StdioMCPSpawnCountForTest returns the cumulative number of stdio MCP child
// processes started since the last reset.
func StdioMCPSpawnCountForTest() int64 {
	return stdioSpawnCounter.Load()
}

// ResetStdioMCPPoolForTest closes all pooled stdio MCP sessions and resets the
// spawn counter for deterministic unit testing.
func ResetStdioMCPPoolForTest() {
	defaultStdioPool.closeAll()
	stdioSpawnCounter.Store(0)
}

type syncLimitedBuffer struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int
}

func (b *syncLimitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit <= 0 || b.buf.Len() < b.limit {
		avail := len(p)
		if b.limit > 0 && b.buf.Len()+avail > b.limit {
			avail = b.limit - b.buf.Len()
		}
		_, _ = b.buf.Write(p[:avail])
	}
	return len(p), nil
}

func (b *syncLimitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(b.buf.String())
}

type rpcOutcome struct {
	result json.RawMessage
	err    error
}

type stdioMCPPool struct {
	mu       sync.Mutex
	sessions map[string]*stdioMCPSession
	idleTTL  time.Duration
}

func newStdioMCPPool(idleTTL time.Duration) *stdioMCPPool {
	return &stdioMCPPool{
		sessions: make(map[string]*stdioMCPSession),
		idleTTL:  idleTTL,
	}
}

func (p *stdioMCPPool) invoke(ctx context.Context, worktreeDir string, srv mcpServerSpec, method string, params map[string]any) (json.RawMessage, error) {
	if len(srv.Command) == 0 {
		return nil, fmt.Errorf("empty stdio command for MCP server %s", srv.Name)
	}
	cleanDir := strings.TrimSpace(worktreeDir)
	if cleanDir != "" {
		if abs, err := filepath.Abs(cleanDir); err == nil {
			cleanDir = abs
		}
	}
	slotKey := cleanDir + "\x00" + strings.ToLower(strings.TrimSpace(srv.Name))
	specHash := hashStdioMCPSpec(cleanDir, srv)

	sess, reused := p.acquire(slotKey, specHash, cleanDir, srv)
	res, err := sess.call(ctx, method, params)
	if err != nil && reused && errors.Is(err, errDeadStdioSession) {
		p.evictIfMatch(slotKey, sess)
		fresh, _ := p.acquire(slotKey, specHash, cleanDir, srv)
		return fresh.call(ctx, method, params)
	}
	return res, err
}

func (p *stdioMCPPool) acquire(slotKey, specHash, worktreeDir string, srv mcpServerSpec) (*stdioMCPSession, bool) {
	p.mu.Lock()
	var stale *stdioMCPSession
	if cur, ok := p.sessions[slotKey]; ok {
		if cur.specHash == specHash && !cur.isDead() {
			cur.touchIdle()
			p.mu.Unlock()
			return cur, true
		}
		delete(p.sessions, slotKey)
		stale = cur
	}
	next := newStdioMCPSession(p, slotKey, specHash, worktreeDir, srv)
	p.sessions[slotKey] = next
	p.mu.Unlock()

	if stale != nil {
		go stale.close()
	}
	return next, false
}

func (p *stdioMCPPool) evictIfMatch(slotKey string, target *stdioMCPSession) {
	p.mu.Lock()
	matched := false
	if cur, ok := p.sessions[slotKey]; ok && cur == target {
		delete(p.sessions, slotKey)
		matched = true
	}
	p.mu.Unlock()
	if matched {
		target.close()
	}
}

func (p *stdioMCPPool) closeAll() {
	p.mu.Lock()
	list := make([]*stdioMCPSession, 0, len(p.sessions))
	for k, s := range p.sessions {
		delete(p.sessions, k)
		list = append(list, s)
	}
	p.mu.Unlock()
	for _, s := range list {
		s.close()
	}
}

type stdioMCPSession struct {
	pool        *stdioMCPPool
	slotKey     string
	specHash    string
	worktreeDir string
	srv         mcpServerSpec

	initMu sync.Mutex
	ready  bool

	writeMu sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stderr  syncLimitedBuffer
	nextID  atomic.Int64

	mu        sync.Mutex
	closed    bool
	pending   map[int64]chan rpcOutcome
	idleTimer *time.Timer
	done      chan struct{}
	closeOnce sync.Once
}

func newStdioMCPSession(pool *stdioMCPPool, slotKey, specHash, worktreeDir string, srv mcpServerSpec) *stdioMCPSession {
	s := &stdioMCPSession{
		pool:        pool,
		slotKey:     slotKey,
		specHash:    specHash,
		worktreeDir: worktreeDir,
		srv:         srv,
		stderr:      syncLimitedBuffer{limit: 8192},
		pending:     make(map[int64]chan rpcOutcome),
		done:        make(chan struct{}),
	}
	s.nextID.Store(1)
	return s
}

func (s *stdioMCPSession) isDead() bool {
	select {
	case <-s.done:
		return true
	default:
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *stdioMCPSession) touchIdle() {
	if s.pool == nil || s.pool.idleTTL <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if s.idleTimer != nil {
		s.idleTimer.Reset(s.pool.idleTTL)
		return
	}
	s.idleTimer = time.AfterFunc(s.pool.idleTTL, func() {
		s.pool.evictIfMatch(s.slotKey, s)
	})
}

func (s *stdioMCPSession) ensureReady(ctx context.Context) error {
	s.initMu.Lock()
	defer s.initMu.Unlock()

	if s.isDead() {
		return errDeadStdioSession
	}
	if s.ready {
		return nil
	}

	cmd := subproc.Command(s.srv.Command[0], s.srv.Command[1:]...)
	if s.worktreeDir != "" {
		cmd.Dir = s.worktreeDir
	}
	if len(s.srv.Env) > 0 {
		env := os.Environ()
		keys := make([]string, 0, len(s.srv.Env))
		for k := range s.srv.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			env = append(env, k+"="+s.srv.Env[k])
		}
		cmd.Env = env
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		s.close()
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		s.close()
		return err
	}
	cmd.Stderr = &s.stderr

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		s.close()
		return err
	}
	stdioSpawnCounter.Add(1)
	s.cmd = cmd
	s.stdin = stdin

	go s.readLoop(stdout)
	go func() {
		_ = cmd.Wait()
		s.close()
	}()

	initCtx := ctx
	var cancel context.CancelFunc
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > stdioMCPInitTimeout {
		initCtx, cancel = context.WithTimeout(ctx, stdioMCPInitTimeout)
		defer cancel()
	}

	if _, err := s.roundTrip(initCtx, 1, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "hoxi-native", "version": "1.0.0"},
	}); err != nil {
		s.close()
		s.pool.evictIfMatch(s.slotKey, s)
		return err
	}
	_ = s.sendNotification("notifications/initialized")

	s.ready = true
	s.touchIdle()
	return nil
}

func (s *stdioMCPSession) call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	if err := s.ensureReady(ctx); err != nil {
		return nil, err
	}
	s.touchIdle()
	id := s.nextID.Add(1)
	return s.roundTrip(ctx, id, method, params)
}

func (s *stdioMCPSession) roundTrip(ctx context.Context, id int64, method string, params map[string]any) (json.RawMessage, error) {
	ch := make(chan rpcOutcome, 1)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, errDeadStdioSession
	}
	s.pending[id] = ch
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
	}()

	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return nil, err
	}

	s.writeMu.Lock()
	var writeErr error
	if s.stdin == nil {
		writeErr = errDeadStdioSession
	} else {
		_, writeErr = s.stdin.Write(append(payload, '\n'))
	}
	s.writeMu.Unlock()

	if writeErr != nil {
		s.close()
		s.pool.evictIfMatch(s.slotKey, s)
		return nil, fmt.Errorf("%w: %v", errDeadStdioSession, writeErr)
	}

	select {
	case out := <-ch:
		s.touchIdle()
		return out.result, out.err
	case <-s.done:
		// Check if readLoop delivered our response right before closing s.done.
		select {
		case out := <-ch:
			return out.result, out.err
		default:
		}
		s.pool.evictIfMatch(s.slotKey, s)
		return nil, fmt.Errorf("%w: MCP server %s closed stdout (%s)", errDeadStdioSession, s.srv.Name, s.stderr.String())
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *stdioMCPSession) sendNotification(method string) error {
	b, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	})
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.stdin == nil {
		return errDeadStdioSession
	}
	_, err = s.stdin.Write(append(b, '\n'))
	return err
}

func (s *stdioMCPSession) readLoop(stdout io.Reader) {
	defer func() {
		s.close()
		if s.pool != nil {
			s.pool.evictIfMatch(s.slotKey, s)
		}
	}()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 4<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line[0] != '{' {
			continue
		}
		var env jsonRPCEnvelope
		if json.Unmarshal([]byte(line), &env) != nil {
			continue
		}
		id, ok := parseJSONRPCIntID(env.ID)
		if !ok {
			continue
		}
		var out rpcOutcome
		if env.Error != nil {
			out.err = fmt.Errorf("MCP error %d: %s", env.Error.Code, env.Error.Message)
		} else {
			out.result = env.Result
		}
		s.mu.Lock()
		ch := s.pending[id]
		delete(s.pending, id)
		s.mu.Unlock()
		if ch != nil {
			ch <- out
		}
	}
}

func (s *stdioMCPSession) close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		if s.idleTimer != nil {
			s.idleTimer.Stop()
			s.idleTimer = nil
		}
		s.mu.Unlock()

		close(s.done)

		s.writeMu.Lock()
		stdin := s.stdin
		cmd := s.cmd
		s.stdin = nil
		s.writeMu.Unlock()

		if stdin != nil {
			_ = stdin.Close()
		}
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})
}

func parseJSONRPCIntID(raw any) (int64, bool) {
	switch v := raw.(type) {
	case float64:
		return int64(v), true
	case int64:
		return v, true
	case int:
		return int64(v), true
	default:
		return 0, false
	}
}

func hashStdioMCPSpec(worktreeDir string, srv mcpServerSpec) string {
	h := sha256.New()
	_, _ = h.Write([]byte(worktreeDir))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(srv.Name))
	_, _ = h.Write([]byte{0})
	for _, arg := range srv.Command {
		_, _ = h.Write([]byte(arg))
		_, _ = h.Write([]byte{0})
	}
	if len(srv.Env) > 0 {
		keys := make([]string, 0, len(srv.Env))
		for k := range srv.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			_, _ = h.Write([]byte(k + "=" + srv.Env[k]))
			_, _ = h.Write([]byte{0})
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}
