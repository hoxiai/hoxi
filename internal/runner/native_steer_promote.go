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

	"github.com/hoxiai/hoxi/internal/llm"
	"github.com/hoxiai/hoxi/internal/subproc"
)

const defaultRunnerSteerPromoteGrace = 250 * time.Millisecond

var (
	runnerSteerPromoteGraceNs atomic.Int64
	closedSteerNotifyCh       = func() chan struct{} {
		ch := make(chan struct{})
		close(ch)
		return ch
	}()
)

func init() {
	runnerSteerPromoteGraceNs.Store(int64(defaultRunnerSteerPromoteGrace))
}

// SetRunnerSteerPromoteGraceForTest overrides the grace window before a running
// foreground bash command is promoted to background when user steering arrives.
func SetRunnerSteerPromoteGraceForTest(d time.Duration) func() {
	prev := runnerSteerPromoteGraceNs.Swap(int64(d))
	return func() {
		runnerSteerPromoteGraceNs.Store(prev)
	}
}

func currentRunnerSteerPromoteGrace() time.Duration {
	ns := runnerSteerPromoteGraceNs.Load()
	if ns <= 0 {
		return 0
	}
	return time.Duration(ns)
}

// NativeBackgroundPromotedError signals that a running foreground bash command
// was seamlessly adopted into s.bgProcs (on user steering or foreground timeout).
type NativeBackgroundPromotedError struct {
	Proc   NativeBackgroundProc
	Reason string
}

func (e *NativeBackgroundPromotedError) Error() string {
	if e == nil {
		return "command promoted to background"
	}
	return fmt.Sprintf("command promoted to background (%s)", e.Proc.ProcID)
}

func (q *nativeSteerQueue) HasPending() bool {
	if q == nil {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items) > 0
}

func (q *nativeSteerQueue) NotifyCh() <-chan struct{} {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) > 0 {
		return closedSteerNotifyCh
	}
	if q.notify == nil {
		q.notify = make(chan struct{})
	}
	return q.notify
}

type runnerFgMode uint8

const (
	runnerFgModeActive runnerFgMode = iota
	runnerFgModePromoted
	runnerFgModeCancelled
)

func (s *Service) nativePromotableCommandRunner(rec SessionRecord, ap *activeProcess) func(ctx context.Context, dir, command string) (string, int, error) {
	streamFn := s.nativePromotableStreamCommandRunner(rec, ap)
	return func(ctx context.Context, dir, command string) (string, int, error) {
		return streamFn(ctx, dir, command, nil, nil)
	}
}

func (s *Service) nativePromotableStreamCommandRunner(
	rec SessionRecord,
	ap *activeProcess,
) func(ctx context.Context, dir, command string, capture io.Writer, onOutput func(string)) (string, int, error) {
	if s == nil || ap == nil || ap.steerQueue == nil || s.bgProcs == nil {
		return s.nativeStreamCommandRunner(rec, ap)
	}
	return func(ctx context.Context, dir, command string, capture io.Writer, onOutput func(string)) (string, int, error) {
		onDemandProfile := ""
		if s.gate != nil {
			onDemandProfile = s.gate.consumeNativeOnDemand(rec.ID, command)
		}
		workDir := strings.TrimSpace(dir)
		if workDir == "" {
			workDir = rec.WorktreePath
		}
		if rec.WorktreePath != "" && !pathWithin(rec.WorktreePath, workDir) {
			err := errors.New("command working directory is outside this session's directory")
			return err.Error(), 1, err
		}

		shellBin, shellArgs := nativeShellCommand(command)
		bin, args, err := s.confineNativeCommandWithOnDemand(rec, onDemandProfile, shellBin, shellArgs)
		if err != nil {
			return err.Error(), 1, err
		}

		cmd := subproc.Command(bin, args...)
		cmd.Dir = workDir
		cmd.Env = s.applySessionBundleEnv(rec, nativeSubprocessEnv(rec))
		setProcessGroup(cmd)

		outR, outW, err := os.Pipe()
		if err != nil {
			return err.Error(), 1, err
		}
		cmd.Stdout = outW
		cmd.Stderr = outW
		if err := cmd.Start(); err != nil {
			_ = outR.Close()
			_ = outW.Close()
			return err.Error(), 1, err
		}
		_ = outW.Close()
		startedAt := time.Now()

		ap.mu.Lock()
		ap.cmd = cmd
		ap.mu.Unlock()
		clearForegroundCmd := func() {
			ap.mu.Lock()
			if ap.cmd == cmd {
				ap.cmd = nil
			}
			ap.mu.Unlock()
		}
		defer clearForegroundCmd()

		if s.ownedProcs != nil && cmd.Process != nil {
			if runtime.GOOS == "windows" {
				s.ownedProcs.RecordWindowsSubtree(rec.ID, cmd.Process.Pid)
			} else {
				s.ownedProcs.Record(rec.ID, cmd.Process.Pid, command)
			}
		}

		var (
			stateMu    sync.Mutex
			mode       = runnerFgModeActive
			exited     bool
			waitErr    error
			fgWriter   = &nativeLimitedWriter{limit: nativeCmdMaxOutputBytes, capture: capture, onProgress: onOutput}
			adoptedJob *nativeBgJob
		)
		waitDone := make(chan struct{})
		readerDone := make(chan struct{})
		settledDone := make(chan struct{})

		go func() {
			defer close(readerDone)
			buf := make([]byte, 4096)
			for {
				n, rErr := outR.Read(buf)
				if n > 0 {
					chunk := buf[:n]
					stateMu.Lock()
					job := adoptedJob
					if job == nil {
						_, _ = fgWriter.Write(chunk)
					}
					stateMu.Unlock()
					if job != nil {
						job.appendBytes(chunk)
					}
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
			close(waitDone)
			stateMu.Lock()
			waitErr = wErr
			exited = true
			job := adoptedJob
			stateMu.Unlock()

			if job == nil {
				reapOrphanedOutput(cmd.Process, outR, readerDone)
				close(settledDone)
				return
			}

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
			case <-readerDone:
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
			case <-readerDone:
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
			close(settledDone)
		}()

		tryPromote := func() (NativeBackgroundProc, bool) {
			if ap.wasAborted() {
				return NativeBackgroundProc{}, false
			}
			s.bgProcs.mu.Lock()
			defer s.bgProcs.mu.Unlock()

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
				return NativeBackgroundProc{}, false
			}

			stateMu.Lock()
			if exited || mode != runnerFgModeActive {
				stateMu.Unlock()
				return NativeBackgroundProc{}, false
			}
			var seed []byte
			if fgWriter.totalBytes <= int64(fgWriter.limit) {
				seed = append([]byte(nil), fgWriter.head.Bytes()...)
			} else {
				seed = []byte(fgWriter.String())
			}
			if len(seed) > maxNativeBgBufferBytes {
				seed = append([]byte(nil), seed[len(seed)-maxNativeBgBufferBytes:]...)
			}
			id := fmt.Sprintf("bg_%d", bucket.seq.Add(1))
			job := &nativeBgJob{
				id:          id,
				sessionID:   rec.ID,
				command:     command,
				startedAt:   startedAt,
				proc:        cmd.Process,
				outR:        outR,
				stopReq:     make(chan struct{}),
				waitDone:    waitDone,
				readerDone:  readerDone,
				settledDone: settledDone,
				buf:         seed,
				totalBytes:  int64(len(seed)),
				readCursor:  int64(len(seed)),
				running:     true,
				notify:      make(chan struct{}),
			}
			adoptedJob = job
			mode = runnerFgModePromoted
			stateMu.Unlock()

			clearForegroundCmd()

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

			pid := 0
			if cmd.Process != nil {
				pid = cmd.Process.Pid
			}
			return NativeBackgroundProc{
				ProcID:    id,
				Command:   command,
				PID:       pid,
				Running:   true,
				StartedAt: startedAt,
				Output:    subproc.DecodeOutputBytes(seed),
			}, true
		}

		steerNotify := ap.steerQueue.NotifyCh()
		var graceCh <-chan time.Time
		var graceTimer *time.Timer
		defer func() {
			if graceTimer != nil {
				graceTimer.Stop()
			}
		}()

		finishForeground := func() (string, int, error) {
			stateMu.Lock()
			output := fgWriter.String()
			wErr := waitErr
			stateMu.Unlock()
			if onDemandProfile != "" {
				output = appendOnDemandSandboxNote(output)
			}
			if wErr == nil {
				return output, 0, nil
			}
			code := 1
			var exitErr *exec.ExitError
			if errors.As(wErr, &exitErr) {
				if c := exitErr.ExitCode(); c > 0 {
					code = c
				} else if ctx.Err() != nil {
					code = 130
				} else {
					code = 137
				}
			} else if ctx.Err() != nil {
				code = 130
			}
			return output, code, wErr
		}

		for {
			select {
			case <-settledDone:
				return finishForeground()

			case <-steerNotify:
				steerNotify = nil
				grace := currentRunnerSteerPromoteGrace()
				if grace <= 0 {
					if proc, ok := tryPromote(); ok {
						return proc.Output, 0, &NativeBackgroundPromotedError{Proc: proc}
					}
				} else {
					graceTimer = time.NewTimer(grace)
					graceCh = graceTimer.C
				}

			case <-graceCh:
				graceCh = nil
				if proc, ok := tryPromote(); ok {
					return proc.Output, 0, &NativeBackgroundPromotedError{Proc: proc}
				}

			case <-ctx.Done():
				if errors.Is(context.Cause(ctx), llm.ErrForegroundCommandTimeout) {
					if proc, ok := tryPromote(); ok {
						return proc.Output, 0, &NativeBackgroundPromotedError{Proc: proc, Reason: "timeout"}
					}
				}
				stateMu.Lock()
				canCancel := !exited && mode == runnerFgModeActive
				if canCancel {
					mode = runnerFgModeCancelled
				}
				stateMu.Unlock()
				if canCancel {
					terminateNativeCommandTree(cmd.Process, waitDone)
				}
				<-settledDone
				return finishForeground()
			}
		}
	}
}
