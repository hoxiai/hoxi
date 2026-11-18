package runner

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/subproc"
)

type activeProcess struct {
	sessionID     string
	runtime       string
	cmd           *exec.Cmd
	stdin         io.WriteCloser
	done          chan struct{}
	abortCh       chan struct{}
	aborted       bool
	steered       bool
	restarting    bool
	retrying      bool
	idleCh        chan struct{}
	cancel        func()
	steerQueue    *nativeSteerQueue
	mu            sync.Mutex
	stall         stallState
	toolFails     toolFailureStreak
	reasoningLoop openCodeReasoningGuard
	pendingErr    *SSEEvent
	openCodeRec   SessionRecord
	// openCodeChildren holds the OpenCode subagent session IDs spawned by
	// this turn, so their activity counts toward the parent's watchdog.
	openCodeChildren syncMap[string, bool]
	fileURLs         syncMap[string, string]
}

func (ap *activeProcess) setOpenCodeRecord(rec SessionRecord) {
	if ap == nil {
		return
	}
	ap.mu.Lock()
	ap.openCodeRec = rec
	ap.mu.Unlock()
}

func (ap *activeProcess) openCodeRecord() SessionRecord {
	if ap == nil {
		return SessionRecord{}
	}
	ap.mu.Lock()
	rec := ap.openCodeRec
	ap.mu.Unlock()
	return rec
}

var abortInterruptGrace = 2 * time.Second

func (ap *activeProcess) Abort() {
	ap.mu.Lock()
	alreadyAborted := ap.aborted
	ap.aborted = true
	abortCh := ap.abortCh
	var proc *os.Process
	if ap.cmd != nil {
		proc = ap.cmd.Process
	}
	stdin := ap.stdin
	cancel := ap.cancel
	ap.mu.Unlock()
	if !alreadyAborted && abortCh != nil {
		close(abortCh)
	}
	if cancel != nil {
		cancel()
	}

	if stdin != nil {
		_ = stdin.Close()
	}
	if proc == nil {
		return
	}
	// Stop means the whole turn: whatever survives the interrupt (background
	// jobs that ignore SIGINT, tool commands in their own process groups) is
	// killed with the group and the process tree captured before it.
	tree := descendantPIDs(proc.Pid)
	_ = signalProcessGroup(proc, os.Interrupt)
	select {
	case <-ap.done:
	case <-time.After(abortInterruptGrace):
		tree = append(tree, descendantPIDs(proc.Pid)...)
	}
	_ = signalProcessGroup(proc, os.Kill)
	killPIDs(tree)
}

func (ap *activeProcess) wasAborted() bool {
	ap.mu.Lock()
	defer ap.mu.Unlock()
	return ap.aborted
}

func (ap *activeProcess) markSteered() {
	ap.mu.Lock()
	ap.steered = true
	ap.mu.Unlock()
}

func (ap *activeProcess) wasSteered() bool {
	ap.mu.Lock()
	defer ap.mu.Unlock()
	return ap.steered
}

func (ap *activeProcess) markRestarting() {
	ap.mu.Lock()
	ap.restarting = true
	ap.mu.Unlock()
}

func (ap *activeProcess) wasRestarting() bool {
	ap.mu.Lock()
	defer ap.mu.Unlock()
	return ap.restarting
}

func (ap *activeProcess) notifyIdle() {
	if ap.idleCh == nil {
		return
	}
	select {
	case ap.idleCh <- struct{}{}:
	default:
	}
}

func (ap *activeProcess) setPendingError(evt SSEEvent) {
	ap.mu.Lock()
	ap.pendingErr = &evt
	ap.mu.Unlock()
}

func (ap *activeProcess) hasPendingError() bool {
	ap.mu.Lock()
	defer ap.mu.Unlock()
	return ap.pendingErr != nil
}

func (ap *activeProcess) takePendingError() (SSEEvent, bool) {
	ap.mu.Lock()
	defer ap.mu.Unlock()
	if ap.pendingErr == nil {
		return SSEEvent{}, false
	}
	evt := *ap.pendingErr
	ap.pendingErr = nil
	return evt, true
}

func (s *Service) releaseActiveProcess(sessionID string, ap *activeProcess) {
	s.mu.Lock()
	stillActive := false
	if s.procs[sessionID] == ap {
		delete(s.procs, sessionID)
	} else if s.procs[sessionID] != nil {
		stillActive = true
	}
	s.mu.Unlock()
	if !stillActive && (ap == nil || (!ap.wasSteered() && !ap.wasRestarting())) && s.fileCoord != nil {
		s.fileCoord.ReleaseTurn(s.rootLeaseOwner(sessionID))
	}
	if s.status != nil {
		if ap == nil || !ap.wasSteered() {
			s.status.SetSessionBusy(sessionID, false)
		}
		if ap != nil {
			ap.openCodeChildren.Range(func(childID string, _ bool) bool {
				s.status.SetSessionBusy(childID, false)
				return true
			})
		}
	}
}

func readStreamLines(r io.Reader, onLine func(string)) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		onLine(subproc.DecodeOutputBytes(scanner.Bytes()))
	}
}
