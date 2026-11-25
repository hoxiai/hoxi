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

	"github.com/hoxiai/hoxi/internal/llm"
	"github.com/hoxiai/hoxi/internal/subproc"
)

const defaultSteerPromoteGrace = 250 * time.Millisecond

var steerPromoteGraceNs atomic.Int64

func init() {
	steerPromoteGraceNs.Store(int64(defaultSteerPromoteGrace))
}

// SetBashSteerPromoteGraceForTest overrides the grace duration before a running
// foreground bash command is promoted to background when user steering is queued.
func SetBashSteerPromoteGraceForTest(d time.Duration) func() {
	prev := steerPromoteGraceNs.Swap(int64(d))
	return func() {
		steerPromoteGraceNs.Store(prev)
	}
}

func currentSteerPromoteGrace() time.Duration {
	ns := steerPromoteGraceNs.Load()
	if ns <= 0 {
		return 0
	}
	return time.Duration(ns)
}

func formatPromotedBackgroundReport(st BackgroundProcStatus) string {
	return formatPromotedBackgroundReportWithReason(st, "")
}

func formatPromotedBackgroundReportWithReason(st BackgroundProcStatus, reason string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[command promoted to background] procId=%s status=running (pid %d) command=%q\n", st.ProcID, st.PID, st.Command)
	if reason == "timeout" {
		b.WriteString("This foreground command reached its timeout while still running, so it was moved to the background without terminating it.\n")
		b.WriteString("Call bash with action=\"output\" and procId=\"" + st.ProcID + "\" (optionally with waitMs) to check progress/logs, or action=\"stop\" to cancel it before making conflicting edits.")
	} else {
		b.WriteString("A new user steering message arrived while this foreground command was still running, so it was moved to the background without terminating it.\n")
		b.WriteString("Address the user's new steering instructions first; call bash with action=\"output\" and procId=\"" + st.ProcID + "\" to check progress/logs, or action=\"stop\" to cancel it before making conflicting edits.")
	}
	trimmed := strings.TrimRight(stripTerminalControlNoise(st.Output), "\n")
	if trimmed != "" {
		b.WriteString("\n\n--- Output before background promotion ---\n")
		b.WriteString(trimmed)
	}
	return b.String()
}

func promotedToolOutput(cmdStr string, st BackgroundProcStatus) ToolOutput {
	return promotedToolOutputWithReason(cmdStr, st, "")
}

func promotedToolOutputWithReason(cmdStr string, st BackgroundProcStatus, reason string) ToolOutput {
	meta := map[string]any{
		"procId":   st.ProcID,
		"promoted": true,
		"running":  true,
		"pid":      st.PID,
	}
	if reason != "" {
		meta["reason"] = reason
	}
	return ToolOutput{
		Title:    fmt.Sprintf("%s [%s]", cmdStr, st.ProcID),
		Output:   formatPromotedBackgroundReportWithReason(st, reason),
		Metadata: meta,
	}
}

type directCmdMode uint8

const (
	directModeForeground directCmdMode = iota
	directModePromoted
	directModeCancelled
)

func runDirectForegroundBash(runCtx context.Context, tc ToolContext, cmdStr string, capture io.Writer) (ToolOutput, error) {
	shell, shellArgs := defaultShellCommand(cmdStr)
	cmd := subproc.Command(shell, shellArgs...)
	cmd.Dir = tc.WorktreeDir

	outR, outW, err := os.Pipe()
	if err != nil {
		return ToolOutput{Title: cmdStr, IsError: true}, err
	}
	cmd.Stdout = outW
	cmd.Stderr = outW
	if err := cmd.Start(); err != nil {
		_ = outR.Close()
		_ = outW.Close()
		return ToolOutput{Title: cmdStr, IsError: true}, err
	}
	_ = outW.Close()
	startedAt := time.Now()

	var (
		stateMu   sync.Mutex
		mode      = directModeForeground
		exited    bool
		fgBuf     limitedCmdBuffer
		adoptedBg *fallbackBgJob
	)
	fgBuf.limit = 128 * 1024

	waitDone := make(chan struct{})
	readerDone := make(chan struct{})
	settledDone := make(chan struct{})
	var waitErr error

	go func() {
		defer close(readerDone)
		buf := make([]byte, 4096)
		for {
			n, rErr := outR.Read(buf)
			if n > 0 {
				chunk := buf[:n]
				stateMu.Lock()
				bg := adoptedBg
				if bg == nil {
					_, _ = fgBuf.Write(chunk)
					if capture != nil {
						_, _ = capture.Write(chunk)
					}
				}
				stateMu.Unlock()
				if bg != nil {
					bg.appendBytes(chunk)
				} else if tc.OnToolOutput != nil {
					tc.OnToolOutput(stripTerminalControlNoise(subproc.DecodeOutputBytes(chunk)))
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
		bg := adoptedBg
		stateMu.Unlock()

		select {
		case <-readerDone:
		case <-time.After(150 * time.Millisecond):
			if bg != nil {
				bg.closePipe()
			} else {
				_ = outR.Close()
			}
			<-readerDone
		}

		if bg != nil {
			code := 0
			if wErr != nil {
				code = 1
				var exitErr *exec.ExitError
				if errors.As(wErr, &exitErr) && exitErr.ExitCode() > 0 {
					code = exitErr.ExitCode()
				}
			}
			bg.markExited(code)
		}
		close(settledDone)
	}()

	tryPromote := func() (BackgroundProcStatus, bool) {
		store, ok := fallbackBackgroundManagerForSession(tc.SessionID).(*fallbackSessionBgStore)
		if !ok || store == nil {
			return BackgroundProcStatus{}, false
		}
		store.mu.Lock()
		defer store.mu.Unlock()

		runningCount := 0
		for _, j := range store.jobs {
			j.mu.Lock()
			if j.running {
				runningCount++
			}
			j.mu.Unlock()
		}
		if runningCount >= maxFallbackBgRunning {
			return BackgroundProcStatus{}, false
		}

		stateMu.Lock()
		if exited || mode != directModeForeground {
			stateMu.Unlock()
			return BackgroundProcStatus{}, false
		}
		id := fmt.Sprintf("bg_%d", store.seq.Add(1))
		seed := append([]byte(nil), fgBuf.Bytes()...)
		job := &fallbackBgJob{
			id:          id,
			command:     cmdStr,
			startedAt:   startedAt,
			cmd:         cmd,
			outR:        outR,
			waitDone:    waitDone,
			settledDone: settledDone,
			buf:         seed,
			totalBytes:  int64(len(seed)),
			readCursor:  int64(len(seed)),
			running:     true,
			notify:      make(chan struct{}),
		}
		adoptedBg = job
		mode = directModePromoted
		stateMu.Unlock()

		for len(store.order) >= maxFallbackBgRetained {
			evicted := false
			for i, k := range store.order {
				cand := store.jobs[k]
				cand.mu.Lock()
				done := !cand.running
				cand.mu.Unlock()
				if done {
					delete(store.jobs, k)
					store.order = append(store.order[:i], store.order[i+1:]...)
					evicted = true
					break
				}
			}
			if !evicted {
				break
			}
		}
		store.jobs[id] = job
		store.order = append(store.order, id)

		pid := 0
		if cmd.Process != nil {
			pid = cmd.Process.Pid
		}
		return BackgroundProcStatus{
			ProcID:    id,
			Command:   cmdStr,
			PID:       pid,
			Running:   true,
			StartedAt: startedAt,
			Output:    subproc.DecodeOutputBytes(seed),
		}, true
	}

	var steerNotify <-chan struct{}
	if waiter, ok := tc.Steer.(SteerWaitSource); ok && waiter != nil {
		steerNotify = waiter.NotifyCh()
	}

	var graceCh <-chan time.Time
	var graceTimer *time.Timer
	stopGrace := func() {
		if graceTimer != nil {
			graceTimer.Stop()
		}
	}
	defer stopGrace()

	for {
		select {
		case <-settledDone:
			stateMu.Lock()
			out := subproc.DecodeOutputBytes(fgBuf.Bytes())
			wErr := waitErr
			stateMu.Unlock()
			if wErr != nil {
				return ToolOutput{
					Title:   cmdStr,
					Output:  strings.TrimSpace(out + "\n" + wErr.Error()),
					IsError: true,
				}, wErr
			}
			return ToolOutput{Title: cmdStr, Output: out}, nil

		case <-steerNotify:
			steerNotify = nil
			grace := currentSteerPromoteGrace()
			if grace <= 0 {
				if st, ok := tryPromote(); ok {
					return promotedToolOutput(cmdStr, st), nil
				}
			} else {
				graceTimer = time.NewTimer(grace)
				graceCh = graceTimer.C
			}

		case <-graceCh:
			graceCh = nil
			if st, ok := tryPromote(); ok {
				return promotedToolOutput(cmdStr, st), nil
			}

		case <-runCtx.Done():
			if errors.Is(context.Cause(runCtx), llm.ErrForegroundCommandTimeout) {
				if st, ok := tryPromote(); ok {
					return promotedToolOutputWithReason(cmdStr, st, "timeout"), nil
				}
			}
			stateMu.Lock()
			canCancel := !exited && mode == directModeForeground
			if canCancel {
				mode = directModeCancelled
			}
			stateMu.Unlock()
			if canCancel && cmd.Process != nil {
				_ = cmd.Process.Signal(os.Interrupt)
				select {
				case <-settledDone:
				case <-time.After(200 * time.Millisecond):
					_ = cmd.Process.Kill()
					_ = outR.Close()
				}
			}
			<-settledDone
			stateMu.Lock()
			out := subproc.DecodeOutputBytes(fgBuf.Bytes())
			wErr := waitErr
			stateMu.Unlock()
			if wErr == nil {
				wErr = runCtx.Err()
			}
			return ToolOutput{
				Title:   cmdStr,
				Output:  strings.TrimSpace(out + "\n" + wErr.Error()),
				IsError: true,
			}, wErr
		}
	}
}
