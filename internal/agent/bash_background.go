package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/hoxiai/hoxi/internal/subproc"
)

const (
	maxFallbackBgSessions   = 64
	maxFallbackBgRunning    = 8
	maxFallbackBgRetained   = 24
	maxFallbackBgBufferSize = 256 * 1024
	defaultBgStartupGrace   = 450 * time.Millisecond
	maxBgPollWait           = 10 * time.Second
)

// BackgroundProcStatus describes the state and incremental output of a managed
// session-scoped background command.
type BackgroundProcStatus struct {
	ProcID    string
	Command   string
	PID       int
	Running   bool
	ExitCode  int
	StartedAt time.Time
	Output    string
	Truncated bool
}

// BackgroundCommandManager manages session-scoped background processes (such as
// dev servers or file watchers) with bounded output buffering and automatic
// cleanup when the owning session is deleted or the host shuts down.
type BackgroundCommandManager interface {
	Start(ctx context.Context, dir, command string) (BackgroundProcStatus, error)
	Output(ctx context.Context, procID string, wait time.Duration) (BackgroundProcStatus, error)
	Stop(ctx context.Context, procID string) (BackgroundProcStatus, error)
	List(ctx context.Context) ([]BackgroundProcStatus, error)
}

type fallbackBgJob struct {
	id          string
	command     string
	startedAt   time.Time
	cmd         *exec.Cmd
	outR        *os.File
	closeOnce   sync.Once
	waitDone    chan struct{}
	settledDone chan struct{}

	mu         sync.Mutex
	buf        []byte
	baseOffset int64
	totalBytes int64
	readCursor int64
	running    bool
	exitCode   int
	notify     chan struct{}
}

func (j *fallbackBgJob) appendBytes(p []byte) {
	if len(p) == 0 {
		return
	}
	j.mu.Lock()
	j.buf = append(j.buf, p...)
	j.totalBytes += int64(len(p))
	if len(j.buf) > maxFallbackBgBufferSize {
		drop := len(j.buf) - maxFallbackBgBufferSize
		j.buf = append([]byte(nil), j.buf[drop:]...)
		j.baseOffset += int64(drop)
	}
	ch := j.notify
	j.notify = make(chan struct{})
	j.mu.Unlock()
	close(ch)
}

func (j *fallbackBgJob) markExited(code int) {
	j.mu.Lock()
	if !j.running {
		j.mu.Unlock()
		return
	}
	j.running = false
	j.exitCode = code
	ch := j.notify
	j.notify = make(chan struct{})
	j.mu.Unlock()
	close(ch)
}

func (j *fallbackBgJob) consumeUnread() (out string, truncated, running bool, exitCode int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	running = j.running
	exitCode = j.exitCode
	startOff := j.readCursor
	if startOff < j.baseOffset {
		startOff = j.baseOffset
		truncated = true
	}
	if startOff < j.totalBytes {
		rel := int(startOff - j.baseOffset)
		raw := j.buf[rel:]
		for len(raw) > 0 && !utf8.RuneStart(raw[0]) {
			raw = raw[1:]
		}
		out = stripTerminalControlNoise(subproc.DecodeOutputBytes(raw))
		j.readCursor = j.totalBytes
	}
	return out, truncated, running, exitCode
}

type fallbackSessionBgStore struct {
	mu    sync.Mutex
	seq   atomic.Int64
	jobs  map[string]*fallbackBgJob
	order []string
}

var (
	fallbackBgMu    sync.Mutex
	fallbackBgMap   = make(map[string]*fallbackSessionBgStore)
	fallbackBgOrder []string
)

func fallbackBackgroundManagerForSession(sessionID string) BackgroundCommandManager {
	sid := normalizeSessionTodoKey(sessionID)
	fallbackBgMu.Lock()
	st, ok := fallbackBgMap[sid]
	if !ok {
		for len(fallbackBgOrder) >= maxFallbackBgSessions {
			oldest := fallbackBgOrder[0]
			fallbackBgOrder = fallbackBgOrder[1:]
			if evicted := fallbackBgMap[oldest]; evicted != nil {
				evicted.stopAll()
			}
			delete(fallbackBgMap, oldest)
		}
		st = &fallbackSessionBgStore{jobs: make(map[string]*fallbackBgJob)}
		fallbackBgMap[sid] = st
		fallbackBgOrder = append(fallbackBgOrder, sid)
	}
	fallbackBgMu.Unlock()
	return st
}

// DeleteSessionBackgroundProcs terminates and removes any fallback background
// commands owned by a deleted session.
func DeleteSessionBackgroundProcs(sessionID string) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return
	}
	fallbackBgMu.Lock()
	st, ok := fallbackBgMap[sid]
	if ok {
		delete(fallbackBgMap, sid)
		for i, k := range fallbackBgOrder {
			if k == sid {
				fallbackBgOrder = append(fallbackBgOrder[:i], fallbackBgOrder[i+1:]...)
				break
			}
		}
	}
	fallbackBgMu.Unlock()
	if st != nil {
		st.stopAll()
	}
}

func (s *fallbackSessionBgStore) Start(ctx context.Context, dir, command string) (BackgroundProcStatus, error) {
	s.mu.Lock()
	runningCount := 0
	for _, j := range s.jobs {
		j.mu.Lock()
		if j.running {
			runningCount++
		}
		j.mu.Unlock()
	}
	if runningCount >= maxFallbackBgRunning {
		s.mu.Unlock()
		return BackgroundProcStatus{}, fmt.Errorf("maximum concurrent background commands (%d) reached in this session; stop an existing background command first", maxFallbackBgRunning)
	}
	s.mu.Unlock()

	shell, shellArgs := defaultShellCommand(command)
	cmd := subproc.Command(shell, shellArgs...)
	cmd.Dir = dir
	outR, outW, err := os.Pipe()
	if err != nil {
		return BackgroundProcStatus{}, err
	}
	cmd.Stdout = outW
	cmd.Stderr = outW
	if err := cmd.Start(); err != nil {
		_ = outR.Close()
		_ = outW.Close()
		return BackgroundProcStatus{}, err
	}
	_ = outW.Close()

	id := fmt.Sprintf("bg_%d", s.seq.Add(1))
	job := &fallbackBgJob{
		id:          id,
		command:     command,
		startedAt:   time.Now(),
		cmd:         cmd,
		outR:        outR,
		waitDone:    make(chan struct{}),
		settledDone: make(chan struct{}),
		running:     true,
		notify:      make(chan struct{}),
	}

	s.mu.Lock()
	for len(s.order) >= maxFallbackBgRetained {
		evicted := false
		for i, k := range s.order {
			cand := s.jobs[k]
			cand.mu.Lock()
			done := !cand.running
			cand.mu.Unlock()
			if done {
				delete(s.jobs, k)
				s.order = append(s.order[:i], s.order[i+1:]...)
				evicted = true
				break
			}
		}
		if !evicted {
			break
		}
	}
	s.jobs[id] = job
	s.order = append(s.order, id)
	s.mu.Unlock()

	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		buf := make([]byte, 4096)
		for {
			n, rErr := outR.Read(buf)
			if n > 0 {
				job.appendBytes(buf[:n])
			}
			if rErr != nil {
				if !errors.Is(rErr, io.EOF) {
					_ = rErr
				}
				return
			}
		}
	}()

	go func() {
		wErr := cmd.Wait()
		close(job.waitDone)
		select {
		case <-readerDone:
		case <-time.After(150 * time.Millisecond):
			job.closePipe()
			<-readerDone
		}
		code := 0
		if wErr != nil {
			code = 1
			var exitErr *exec.ExitError
			if errors.As(wErr, &exitErr) && exitErr.ExitCode() > 0 {
				code = exitErr.ExitCode()
			}
		}
		job.markExited(code)
		close(job.settledDone)
	}()

	timer := time.NewTimer(defaultBgStartupGrace)
	select {
	case <-ctx.Done():
		timer.Stop()
		job.stop()
		s.mu.Lock()
		delete(s.jobs, id)
		for i, k := range s.order {
			if k == id {
				s.order = append(s.order[:i], s.order[i+1:]...)
				break
			}
		}
		s.mu.Unlock()
		return BackgroundProcStatus{}, ctx.Err()
	case <-job.settledDone:
		timer.Stop()
	case <-timer.C:
	}

	out, trunc, running, exitCode := job.consumeUnread()
	pid := 0
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}
	return BackgroundProcStatus{
		ProcID:    id,
		Command:   command,
		PID:       pid,
		Running:   running,
		ExitCode:  exitCode,
		StartedAt: job.startedAt,
		Output:    out,
		Truncated: trunc,
	}, nil
}

func (s *fallbackSessionBgStore) Output(ctx context.Context, procID string, wait time.Duration) (BackgroundProcStatus, error) {
	job, err := s.lookup(procID)
	if err != nil {
		return BackgroundProcStatus{}, err
	}
	if wait > maxBgPollWait {
		wait = maxBgPollWait
	}
	if wait > 0 {
		deadline := time.NewTimer(wait)
		defer deadline.Stop()
		for {
			job.mu.Lock()
			hasUnread := job.totalBytes > job.readCursor
			running := job.running
			ch := job.notify
			job.mu.Unlock()
			if hasUnread || !running {
				break
			}
			select {
			case <-ctx.Done():
				return BackgroundProcStatus{}, ctx.Err()
			case <-deadline.C:
				goto collect
			case <-ch:
			}
		}
	}
collect:
	out, trunc, running, exitCode := job.consumeUnread()
	pid := 0
	if job.cmd != nil && job.cmd.Process != nil {
		pid = job.cmd.Process.Pid
	}
	return BackgroundProcStatus{
		ProcID:    job.id,
		Command:   job.command,
		PID:       pid,
		Running:   running,
		ExitCode:  exitCode,
		StartedAt: job.startedAt,
		Output:    out,
		Truncated: trunc,
	}, nil
}

func (s *fallbackSessionBgStore) Stop(_ context.Context, procID string) (BackgroundProcStatus, error) {
	job, err := s.lookup(procID)
	if err != nil {
		return BackgroundProcStatus{}, err
	}
	job.stop()
	out, trunc, running, exitCode := job.consumeUnread()
	pid := 0
	if job.cmd != nil && job.cmd.Process != nil {
		pid = job.cmd.Process.Pid
	}
	return BackgroundProcStatus{
		ProcID:    job.id,
		Command:   job.command,
		PID:       pid,
		Running:   running,
		ExitCode:  exitCode,
		StartedAt: job.startedAt,
		Output:    out,
		Truncated: trunc,
	}, nil
}

func (s *fallbackSessionBgStore) List(_ context.Context) ([]BackgroundProcStatus, error) {
	s.mu.Lock()
	ids := append([]string(nil), s.order...)
	jobs := make([]*fallbackBgJob, 0, len(ids))
	for _, id := range ids {
		if j := s.jobs[id]; j != nil {
			jobs = append(jobs, j)
		}
	}
	s.mu.Unlock()

	res := make([]BackgroundProcStatus, 0, len(jobs))
	for _, j := range jobs {
		j.mu.Lock()
		running := j.running
		code := j.exitCode
		j.mu.Unlock()
		pid := 0
		if j.cmd != nil && j.cmd.Process != nil {
			pid = j.cmd.Process.Pid
		}
		res = append(res, BackgroundProcStatus{
			ProcID:    j.id,
			Command:   j.command,
			PID:       pid,
			Running:   running,
			ExitCode:  code,
			StartedAt: j.startedAt,
		})
	}
	return res, nil
}

func (s *fallbackSessionBgStore) lookup(procID string) (*fallbackBgJob, error) {
	id := strings.TrimSpace(procID)
	if id == "" {
		return nil, fmt.Errorf("procId is required")
	}
	s.mu.Lock()
	job := s.jobs[id]
	s.mu.Unlock()
	if job == nil {
		return nil, fmt.Errorf("background command %q was not found in this session", id)
	}
	return job, nil
}

func (s *fallbackSessionBgStore) stopAll() {
	s.mu.Lock()
	jobs := make([]*fallbackBgJob, 0, len(s.jobs))
	for _, j := range s.jobs {
		if j != nil {
			jobs = append(jobs, j)
		}
	}
	s.mu.Unlock()
	for _, j := range jobs {
		j.stop()
	}
}

func (j *fallbackBgJob) closePipe() {
	j.closeOnce.Do(func() {
		if j.outR != nil {
			_ = j.outR.Close()
		}
	})
}

func (j *fallbackBgJob) stop() {
	j.mu.Lock()
	running := j.running
	j.mu.Unlock()
	if !running || j.cmd == nil || j.cmd.Process == nil {
		return
	}
	_ = j.cmd.Process.Signal(os.Interrupt)
	select {
	case <-j.settledDone:
		return
	case <-time.After(200 * time.Millisecond):
	}
	_ = j.cmd.Process.Kill()
	j.closePipe()
	select {
	case <-j.settledDone:
	case <-time.After(time.Second):
	}
}

func formatBackgroundStatusReport(verb string, st BackgroundProcStatus) string {
	state := fmt.Sprintf("exited (code %d)", st.ExitCode)
	if st.Running {
		state = fmt.Sprintf("running (pid %d)", st.PID)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[background %s] procId=%s status=%s command=%q", verb, st.ProcID, state, st.Command)
	if st.Truncated {
		b.WriteString("\n(earlier buffered output was truncated at 256 KiB)")
	}
	trimmed := strings.TrimRight(st.Output, "\n")
	if trimmed != "" {
		b.WriteString("\n")
		b.WriteString(trimmed)
	} else if verb == "output" {
		b.WriteString("\n(no new output since last check)")
	}
	return b.String()
}

func formatBackgroundListReport(items []BackgroundProcStatus) string {
	if len(items) == 0 {
		return "No background commands in this session."
	}
	var b strings.Builder
	for i, it := range items {
		if i > 0 {
			b.WriteByte('\n')
		}
		state := fmt.Sprintf("exited (code %d)", it.ExitCode)
		if it.Running {
			state = fmt.Sprintf("running (pid %d)", it.PID)
		}
		fmt.Fprintf(&b, "- %s: %s — %s", it.ProcID, state, it.Command)
	}
	return b.String()
}
