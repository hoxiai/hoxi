package app

import (
	"os/exec"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/statusbar"
)

func TestChildReapedOnExitAndAfterShutdown(t *testing.T) {
	a := &DesktopApp{}
	child := exec.Command("sh", "-c", "exit 0")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	a.setChild(child)
	select {
	case <-a.childDone:
		if child.ProcessState == nil || !child.ProcessState.Success() {
			t.Fatal("child was not reaped")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("exited child was not reaped promptly")
	}
	a.killChild()
	late := exec.Command("sleep", "30")
	if err := late.Start(); err != nil {
		t.Fatal(err)
	}
	a.setChild(late)
	if late.ProcessState == nil {
		t.Fatal("late child was not reaped after shutdown")
	}
	if a.child != nil {
		t.Fatal("late child registered after shutdown")
	}
}

func TestChildAvailabilityFollowsLifecycle(t *testing.T) {
	tracker := statusbar.NewTracker()
	a := &DesktopApp{status: tracker}
	defer a.killChild()
	for i := 0; i < 20; i++ {
		child := exec.Command("sh", "-c", "exit 0")
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		a.setChild(child)
		select {
		case <-a.childDone:
		case <-time.After(5 * time.Second):
			t.Fatal("child did not exit")
		}
		if tracker.Snapshot().Available {
			t.Fatal("exited child left service available")
		}
	}
	live := exec.Command("sleep", "30")
	if err := live.Start(); err != nil {
		t.Fatal(err)
	}
	a.setChild(live)
	if !tracker.Snapshot().Available {
		t.Fatal("running child did not mark service available")
	}
	a.killChild()
	if tracker.Snapshot().Available {
		t.Fatal("shutdown left service available")
	}
	late := exec.Command("sleep", "30")
	if err := late.Start(); err != nil {
		t.Fatal(err)
	}
	a.setChild(late)
	if tracker.Snapshot().Available {
		t.Fatal("late startup restored availability after shutdown")
	}
}

func TestUnexpectedExitRespawnsDaemon(t *testing.T) {
	tracker := statusbar.NewTracker()
	a := &DesktopApp{status: tracker}
	defer a.killChild()
	restarted := make(chan *exec.Cmd, 1)
	a.respawn = &respawner{start: func() (*exec.Cmd, error) {
		child := exec.Command("sleep", "30")
		if err := child.Start(); err != nil {
			return nil, err
		}
		restarted <- child
		return child, nil
	}}
	crash := exec.Command("sh", "-c", "exit 1")
	if err := crash.Start(); err != nil {
		t.Fatal(err)
	}
	a.setChild(crash)
	var next *exec.Cmd
	select {
	case next = <-restarted:
	case <-time.After(5 * time.Second):
		t.Fatal("crashed daemon was not restarted")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		a.childMu.Lock()
		current := a.child
		a.childMu.Unlock()
		if current == next {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("restarted daemon was not registered")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !tracker.Snapshot().Available {
		t.Fatal("restarted daemon did not restore availability")
	}
}

func TestShutdownDoesNotRespawnDaemon(t *testing.T) {
	a := &DesktopApp{}
	called := make(chan struct{}, 1)
	a.respawn = &respawner{start: func() (*exec.Cmd, error) {
		called <- struct{}{}
		return nil, nil
	}}
	child := exec.Command("sleep", "30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	a.setChild(child)
	a.killChild()
	select {
	case <-called:
		t.Fatal("an intentional shutdown must not restart the daemon")
	case <-time.After(1500 * time.Millisecond):
	}
}

func TestRespawnBudgetStopsCrashLoops(t *testing.T) {
	a := &DesktopApp{}
	now := time.Now()
	var delays []time.Duration
	for i := 0; i < respawnLimit; i++ {
		d, ok := a.respawnBudget(now)
		if !ok {
			t.Fatalf("restart %d refused", i+1)
		}
		delays = append(delays, d)
	}
	if delays[0] != respawnDelay || delays[2] != 4*respawnDelay {
		t.Fatalf("restarts must back off: %v", delays)
	}
	if _, ok := a.respawnBudget(now); ok {
		t.Fatal("a crash loop must stop restarting")
	}
	if _, ok := a.respawnBudget(now.Add(respawnWindow)); !ok {
		t.Fatal("the budget must recover once the window passes")
	}
}
