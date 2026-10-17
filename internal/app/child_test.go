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
