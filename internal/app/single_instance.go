package app

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/wailsapp/wails/v2/pkg/options"
)

// Wails only checks its single-instance lock inside wails.Run, after Run has
// already started the OpenCode daemon and bound the bridge ports. On Windows
// every second launch (double-clicking the icon, Windows opening a hoxi://
// link) would pay that cost and leak a daemon. This probe runs before any of
// that and hands the launch to the running window the same way Wails does, so
// onSecondInstance still receives the arguments.

const (
	// How long a launch waits for an instance that holds the claim but has not
	// opened its Wails window yet (it is still starting up).
	instanceStartupWait = 30 * time.Second
	instancePollEvery   = 100 * time.Millisecond
)

// instanceProbe is the per-platform single-instance check.
type instanceProbe interface {
	// claim makes this process the primary instance. false means another
	// Hoxi of the same variant holds the claim (running or still starting).
	claim() bool
	// forward sends payload to the running instance's Wails message window.
	// found is false when that window does not exist (yet).
	forward(payload string) (found bool, err error)
}

type launchOutcome int

const (
	launchPrimary   launchOutcome = iota // continue normal startup
	launchForwarded                      // the running instance took the launch
	launchAbandoned                      // another instance holds the claim but never opened its window
)

// wailsInstanceNames mirrors SetupSingleInstance in Wails v2
// (internal/frontend/desktop/windows/single_instance.go): the running
// instance listens on a message-only window with these class and window names.
func wailsInstanceNames(uniqueID string) (className, windowName string) {
	id := "wails-app-" + uniqueID
	return id + "-sic", id + "-siw"
}

// launchClaimName is Hoxi's own lock, taken before any heavy startup. Wails'
// mutex is created much later, so it cannot tell a starting instance apart
// from no instance.
func launchClaimName(uniqueID string) string {
	return "hoxi-launch-" + uniqueID
}

// secondInstancePayload is the JSON Wails' WM_COPYDATA handler decodes into
// options.SecondInstanceData before calling OnSecondInstanceLaunch.
func secondInstancePayload(args []string, workingDir string) (string, error) {
	data, err := json.Marshal(options.SecondInstanceData{Args: args, WorkingDirectory: workingDir})
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// handOffLaunch decides whether this launch becomes the primary instance or
// hands its arguments to one that is already running or starting.
func handOffLaunch(p instanceProbe, payload string, wait, every time.Duration, sleep func(time.Duration)) (launchOutcome, error) {
	// A window first: this also covers a running build that predates the claim.
	if found, err := p.forward(payload); found {
		return launchForwarded, err
	}
	if p.claim() {
		return launchPrimary, nil
	}
	for waited := time.Duration(0); waited < wait; waited += every {
		sleep(every)
		if found, err := p.forward(payload); found {
			return launchForwarded, err
		}
		// The other instance exited (or crashed) before opening its window.
		if p.claim() {
			return launchPrimary, nil
		}
	}
	return launchAbandoned, nil
}

// handOffToRunningInstance reports whether another Hoxi instance took this
// launch, in which case Run must return without starting anything.
func handOffToRunningInstance(uniqueID string) bool {
	p := newInstanceProbe(uniqueID)
	if p == nil {
		return false
	}
	wd, _ := os.Getwd()
	payload, err := secondInstancePayload(os.Args[1:], wd)
	if err != nil {
		return false
	}
	outcome, err := handOffLaunch(p, payload, instanceStartupWait, instancePollEvery, time.Sleep)
	switch outcome {
	case launchForwarded:
		if err != nil {
			fmt.Fprintf(os.Stderr, "hoxi: running instance did not accept the launch: %v\n", err)
		}
		return true
	case launchAbandoned:
		// Starting a second full instance would fight over ports and the
		// daemon; the one already starting will open its window.
		fmt.Fprintln(os.Stderr, "hoxi: another instance is still starting; exiting")
		return true
	default:
		return false
	}
}
