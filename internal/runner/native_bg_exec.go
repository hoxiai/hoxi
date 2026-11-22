package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/hoxiai/hoxi/internal/subproc"
)

const (
	maxNativeBgRunningPerSession  = 8
	maxNativeBgRetainedPerSession = 24
	maxNativeBgBufferBytes        = 256 * 1024
	nativeBgStartupGrace          = 450 * time.Millisecond
	maxNativeBgPollWait           = 10 * time.Second
)

// NativeBackgroundProc describes the live or terminal state and unread output
// of a session-owned background command managed by runner.Service.
type NativeBackgroundProc struct {
	ProcID    string
	Command   string
	PID       int
	Running   bool
	ExitCode  int
	StartedAt time.Time
	Output    string
	Truncated bool
}

// NativeBackgroundManager exposes session-scoped background command lifecycle
// operations to the native agent engine without leaking runner internals.
type NativeBackgroundManager interface {
	Start(ctx context.Context, dir, command string) (NativeBackgroundProc, error)
	Output(ctx context.Context, procID string, wait time.Duration) (NativeBackgroundProc, error)
	Stop(ctx context.Context, procID string) (NativeBackgroundProc, error)
	List(ctx context.Context) ([]NativeBackgroundProc, error)
}

type nativeBgJob struct {
	id          string
	sessionID   string
	command     string
	startedAt   time.Time
	proc        *os.Process
	outR        *os.File
	closeOnce   sync.Once
	stopOnce    sync.Once
	stopReq     chan struct{}
	waitDone    chan struct{}
	readerDone  chan struct{}
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

func (j *nativeBgJob) appendBytes(p []byte) {
	if len(p) == 0 {
		return
	}
	j.mu.Lock()
	j.buf = append(j.buf, p...)
	j.totalBytes += int64(len(p))
	if len(j.buf) > maxNativeBgBufferBytes {
		drop := len(j.buf) - maxNativeBgBufferBytes
		j.buf = append([]byte(nil), j.buf[drop:]...)
		j.baseOffset += int64(drop)
	}
	ch := j.notify
	j.notify = make(chan struct{})
	j.mu.Unlock()
	close(ch)
}

func (j *nativeBgJob) markExited(code int) {
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

func (j *nativeBgJob) consumeUnread() (out string, truncated, running bool, exitCode int) {
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
		out = subproc.DecodeOutputBytes(raw)
		j.readCursor = j.totalBytes
	}
	return out, truncated, running, exitCode
}

func (j *nativeBgJob) closePipe() {
	j.closeOnce.Do(func() {
		if j.outR != nil {
			_ = j.outR.Close()
		}
	})
}

func (j *nativeBgJob) stop() {
	if j == nil || j.proc == nil {
		return
	}
	j.stopOnce.Do(func() {
		close(j.stopReq)
	})
	j.mu.Lock()
	running := j.running
	j.mu.Unlock()
	if !running && !processGroupAlive(j.proc) {
		return
	}
	tree := descendantPIDs(j.proc.Pid)
	_ = signalProcessGroup(j.proc, syscall.SIGTERM)
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if !processGroupAlive(j.proc) {
			j.closePipe()
			select {
			case <-j.settledDone:
			case <-time.After(250 * time.Millisecond):
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	tree = append(tree, descendantPIDs(j.proc.Pid)...)
	_ = signalProcessGroup(j.proc, os.Kill)
	killPIDs(tree)
	j.closePipe()
	select {
	case <-j.settledDone:
	case <-time.After(time.Second):
	}
}

type nativeSessionBgBucket struct {
	seq   atomic.Int64
	jobs  map[string]*nativeBgJob
	order []string
}

type nativeBgRegistry struct {
	mu       sync.Mutex
	sessions map[string]*nativeSessionBgBucket
}

func newNativeBgRegistry() *nativeBgRegistry {
	return &nativeBgRegistry{
		sessions: make(map[string]*nativeSessionBgBucket),
	}
}

func (r *nativeBgRegistry) owns(sessionID, procID string) bool {
	if r == nil {
		return false
	}
	sid := strings.TrimSpace(sessionID)
	pid := strings.TrimSpace(procID)
	if sid == "" || pid == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.sessions[sid]
	return b != nil && b.jobs[pid] != nil
}

func (r *nativeBgRegistry) lookup(sessionID, procID string) (*nativeBgJob, error) {
	if r == nil {
		return nil, errors.New("background command registry is unavailable")
	}
	sid := strings.TrimSpace(sessionID)
	pid := strings.TrimSpace(procID)
	if pid == "" {
		return nil, errors.New("procId is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.sessions[sid]
	if b == nil || b.jobs[pid] == nil {
		return nil, fmt.Errorf("background command %q was not found in this session", pid)
	}
	return b.jobs[pid], nil
}

func (r *nativeBgRegistry) removeJob(sessionID, procID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.sessions[sessionID]
	if b == nil {
		return
	}
	delete(b.jobs, procID)
	for i, k := range b.order {
		if k == procID {
			b.order = append(b.order[:i], b.order[i+1:]...)
			break
		}
	}
}

func (r *nativeBgRegistry) stopSession(sessionID string) {
	if r == nil {
		return
	}
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return
	}
	r.mu.Lock()
	b := r.sessions[sid]
	delete(r.sessions, sid)
	var jobs []*nativeBgJob
	if b != nil {
		for _, j := range b.jobs {
			if j != nil {
				jobs = append(jobs, j)
			}
		}
	}
	r.mu.Unlock()
	for _, j := range jobs {
		j.stop()
	}
}

func (r *nativeBgRegistry) stopAll() {
	if r == nil {
		return
	}
	r.mu.Lock()
	var jobs []*nativeBgJob
	for sid, b := range r.sessions {
		delete(r.sessions, sid)
		if b == nil {
			continue
		}
		for _, j := range b.jobs {
			if j != nil {
				jobs = append(jobs, j)
			}
		}
	}
	r.mu.Unlock()
	for _, j := range jobs {
		j.stop()
	}
}

type sessionNativeBgManager struct {
	svc *Service
	rec SessionRecord
}

func (s *Service) nativeBackgroundManager(rec SessionRecord) NativeBackgroundManager {
	if s == nil {
		return nil
	}
	return sessionNativeBgManager{svc: s, rec: rec}
}

func (m sessionNativeBgManager) Start(ctx context.Context, dir, command string) (NativeBackgroundProc, error) {
	s := m.svc
	rec := m.rec
	onDemandProfile := ""
	if s != nil && s.gate != nil {
		onDemandProfile = s.gate.consumeNativeOnDemand(rec.ID, command)
	}
	if s == nil || s.bgProcs == nil {
		return NativeBackgroundProc{}, errors.New("background command registry is unavailable")
	}
	workDir := strings.TrimSpace(dir)
	if workDir == "" {
		workDir = rec.WorktreePath
	}
	if rec.WorktreePath != "" && !pathWithin(rec.WorktreePath, workDir) {
		return NativeBackgroundProc{}, errors.New("command working directory is outside this session's directory")
	}

	s.bgProcs.mu.Lock()
	bucket := s.bgProcs.sessions[rec.ID]
	if bucket == nil {
		bucket = &nativeSessionBgBucket{jobs: make(map[string]*nativeBgJob)}
		s.bgProcs.sessions[rec.ID] = bucket
	}
	runningCount := 0
	for _, j := range bucket.jobs {
		j.mu.Lock()
		if j.running {
			runningCount++
		}
		j.mu.Unlock()
	}
	if runningCount >= maxNativeBgRunningPerSession {
		s.bgProcs.mu.Unlock()
		return NativeBackgroundProc{}, fmt.Errorf(
			"maximum concurrent background commands (%d) reached in this session; call bash with action=\"stop\" on an unneeded procId first",
			maxNativeBgRunningPerSession,
		)
	}
	s.bgProcs.mu.Unlock()

	shellBin, shellArgs := nativeShellCommand(command)
	bin, args, err := s.confineNativeCommandWithOnDemand(rec, onDemandProfile, shellBin, shellArgs)
	if err != nil {
		return NativeBackgroundProc{}, err
	}

	cmd := subproc.Command(bin, args...)
	cmd.Dir = workDir
	cmd.Env = s.applySessionBundleEnv(rec, nativeSubprocessEnv(rec))
	setProcessGroup(cmd)

	outR, outW, err := os.Pipe()
	if err != nil {
		return NativeBackgroundProc{}, err
	}
	cmd.Stdout = outW
	cmd.Stderr = outW
	if err := cmd.Start(); err != nil {
		_ = outR.Close()
		_ = outW.Close()
		return NativeBackgroundProc{}, err
	}
	_ = outW.Close()

	if s.ownedProcs != nil && cmd.Process != nil {
		if runtime.GOOS == "windows" {
			s.ownedProcs.RecordWindowsSubtree(rec.ID, cmd.Process.Pid)
		} else {
			s.ownedProcs.Record(rec.ID, cmd.Process.Pid, command)
		}
	}

	s.bgProcs.mu.Lock()
	bucket = s.bgProcs.sessions[rec.ID]
	if bucket == nil {
		bucket = &nativeSessionBgBucket{jobs: make(map[string]*nativeBgJob)}
		s.bgProcs.sessions[rec.ID] = bucket
	}
	id := fmt.Sprintf("bg_%d", bucket.seq.Add(1))
	job := &nativeBgJob{
		id:          id,
		sessionID:   rec.ID,
		command:     command,
		startedAt:   time.Now(),
		proc:        cmd.Process,
		outR:        outR,
		stopReq:     make(chan struct{}),
		waitDone:    make(chan struct{}),
		readerDone:  make(chan struct{}),
		settledDone: make(chan struct{}),
		running:     true,
		notify:      make(chan struct{}),
	}
	for len(bucket.order) >= maxNativeBgRetainedPerSession {
		evicted := false
		for i, k := range bucket.order {
			cand := bucket.jobs[k]
			cand.mu.Lock()
			done := !cand.running
			cand.mu.Unlock()
			if done {
				delete(bucket.jobs, k)
				bucket.order = append(bucket.order[:i], bucket.order[i+1:]...)
				evicted = true
				break
			}
		}
		if !evicted {
			break
		}
	}
	bucket.jobs[id] = job
	bucket.order = append(bucket.order, id)
	s.bgProcs.mu.Unlock()

	go func() {
		defer close(job.readerDone)
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
		// Wait briefly for buffered stdout/stderr pipes to flush on natural exit;
		// if a detached child keeps stdout open after shell exit, keep reading
		// until EOF or explicit Stop() so background dev servers keep logging.
		code := 0
		if wErr != nil {
			code = 1
			var exitErr *exec.ExitError
			if errors.As(wErr, &exitErr) {
				if c := exitErr.ExitCode(); c > 0 {
					code = c
				} else {
					code = 137
				}
			}
		}
		select {
		case <-job.readerDone:
		case <-time.After(120 * time.Millisecond):
		}
		if code == 0 && processGroupAlive(job.proc) {
			ticker := time.NewTicker(150 * time.Millisecond)
			for processGroupAlive(job.proc) {
				select {
				case <-job.stopReq:
				case <-ticker.C:
				}
				if !processGroupAlive(job.proc) {
					break
				}
				if hasStopRequested(job.stopReq) {
					// Give j.stop() up to 400ms to finish signaling the group.
					time.Sleep(25 * time.Millisecond)
				}
			}
			ticker.Stop()
		} else if processGroupAlive(job.proc) && !hasStopRequested(job.stopReq) {
			tree := descendantPIDs(job.proc.Pid)
			_ = signalProcessGroup(job.proc, syscall.SIGTERM)
			deadline := time.Now().Add(150 * time.Millisecond)
			for time.Now().Before(deadline) && processGroupAlive(job.proc) {
				time.Sleep(20 * time.Millisecond)
			}
			if processGroupAlive(job.proc) {
				tree = append(tree, descendantPIDs(job.proc.Pid)...)
				_ = signalProcessGroup(job.proc, os.Kill)
				killPIDs(tree)
			}
		}
		job.closePipe()
		select {
		case <-job.readerDone:
		case <-time.After(150 * time.Millisecond):
		}
		if onDemandProfile != "" {
			job.mu.Lock()
			curOut := subproc.DecodeOutputBytes(job.buf)
			job.mu.Unlock()
			if withNote := appendOnDemandSandboxNote(curOut); withNote != curOut {
				job.appendBytes([]byte(onDemandSandboxNote))
			}
		}
		job.markExited(code)
		close(job.settledDone)
	}()

	timer := time.NewTimer(nativeBgStartupGrace)
	select {
	case <-ctx.Done():
		timer.Stop()
		job.stop()
		s.bgProcs.removeJob(rec.ID, id)
		return NativeBackgroundProc{}, ctx.Err()
	case <-job.settledDone:
		timer.Stop()
	case <-timer.C:
	}

	out, trunc, running, exitCode := job.consumeUnread()
	pid := 0
	if job.proc != nil {
		pid = job.proc.Pid
	}
	return NativeBackgroundProc{
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

func hasStopRequested(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func (m sessionNativeBgManager) Output(ctx context.Context, procID string, wait time.Duration) (NativeBackgroundProc, error) {
	if m.svc == nil {
		return NativeBackgroundProc{}, errors.New("runner service unavailable")
	}
	job, err := m.svc.bgProcs.lookup(m.rec.ID, procID)
	if err != nil {
		return NativeBackgroundProc{}, err
	}
	if wait > maxNativeBgPollWait {
		wait = maxNativeBgPollWait
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
				return NativeBackgroundProc{}, ctx.Err()
			case <-deadline.C:
				goto collect
			case <-ch:
			}
		}
	}
collect:
	out, trunc, running, exitCode := job.consumeUnread()
	pid := 0
	if job.proc != nil {
		pid = job.proc.Pid
	}
	return NativeBackgroundProc{
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

func (m sessionNativeBgManager) Stop(_ context.Context, procID string) (NativeBackgroundProc, error) {
	if m.svc == nil {
		return NativeBackgroundProc{}, errors.New("runner service unavailable")
	}
	job, err := m.svc.bgProcs.lookup(m.rec.ID, procID)
	if err != nil {
		return NativeBackgroundProc{}, err
	}
	job.stop()
	out, trunc, running, exitCode := job.consumeUnread()
	pid := 0
	if job.proc != nil {
		pid = job.proc.Pid
	}
	return NativeBackgroundProc{
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

func (m sessionNativeBgManager) List(_ context.Context) ([]NativeBackgroundProc, error) {
	if m.svc == nil || m.svc.bgProcs == nil {
		return nil, nil
	}
	m.svc.bgProcs.mu.Lock()
	b := m.svc.bgProcs.sessions[m.rec.ID]
	var jobs []*nativeBgJob
	if b != nil {
		for _, id := range b.order {
			if j := b.jobs[id]; j != nil {
				jobs = append(jobs, j)
			}
		}
	}
	m.svc.bgProcs.mu.Unlock()

	res := make([]NativeBackgroundProc, 0, len(jobs))
	for _, j := range jobs {
		j.mu.Lock()
		running := j.running
		code := j.exitCode
		j.mu.Unlock()
		pid := 0
		if j.proc != nil {
			pid = j.proc.Pid
		}
		res = append(res, NativeBackgroundProc{
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
