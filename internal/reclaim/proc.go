package reclaim

import (
	"errors"
	"slices"
	"syscall"
)

// Identity names one process incarnation. PIDs are recycled, so a PID only
// identifies the process that was recorded while its start token still matches.
type Identity struct {
	PID   int    `json:"pid"`
	Start string `json:"start"`
}

func (id Identity) valid() bool { return id.PID > 1 && id.Start != "" }

// procInfo is what the platform reports about a live process table entry.
type procInfo struct {
	start  string
	zombie bool
	// args is nil when the argument vector could not be read.
	args []string
}

// errNoProcess means the PID is not in the process table at all.
var errNoProcess = errors.New("no such process")

type procState int

const (
	// stateUnknown: the process could not be inspected; never act on it.
	stateUnknown procState = iota
	// stateGone: the recorded incarnation no longer exists (exited, zombie, or PID reused).
	stateGone
	// stateMismatch: the recorded incarnation is alive but lacks the recorded arguments.
	stateMismatch
	// stateMatch: the recorded incarnation is alive and carries the recorded arguments.
	stateMatch
)

// processState compares a recorded process with the live process table. A
// start token mismatch means the PID was reused, so the recorded process is
// gone. Non-empty args must appear in the live argv as one consecutive run,
// element for element; nil args check identity only.
func processState(inspect func(int) (procInfo, error), id Identity, args []string) procState {
	info, err := inspect(id.PID)
	switch {
	case errors.Is(err, errNoProcess):
		return stateGone
	case err != nil:
		return stateUnknown
	case info.zombie || info.start != id.Start:
		return stateGone
	case len(args) == 0:
		return stateMatch
	case info.args == nil:
		return stateUnknown
	}
	for i := 0; i+len(args) <= len(info.args); i++ {
		if slices.Equal(info.args[i:i+len(args)], args) {
			return stateMatch
		}
	}
	return stateMismatch
}

// signalFunc delivers a signal to a PID; it is replaced in tests.
type signalFunc func(pid int, sig syscall.Signal) error
