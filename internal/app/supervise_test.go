package app

import (
	"context"
	"io"
	"log/slog"
	"os/exec"
	"testing"
	"time"
)

// Registering a new daemon must stop the previous one instead of orphaning
// it, and the replaced daemon's exit must not trigger a respawn.
func TestSetChildStopsPreviousDaemon(t *testing.T) {
	a := &DesktopApp{}
	defer a.killChild()
	respawned := make(chan struct{}, 1)
	a.respawn = &respawner{start: func() (*exec.Cmd, error) {
		respawned <- struct{}{}
		return nil, context.Canceled
	}}
	first := exec.Command("sleep", "30")
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	a.setChild(first)
	firstDone := a.childDone

	second := exec.Command("sleep", "30")
	if err := second.Start(); err != nil {
		t.Fatal(err)
	}
	a.setChild(second)
	select {
	case <-firstDone:
	case <-time.After(5 * time.Second):
		t.Fatal("previous daemon was orphaned")
	}
	if first.ProcessState == nil {
		t.Fatal("previous daemon was not reaped")
	}
	a.childMu.Lock()
	current := a.child
	a.childMu.Unlock()
	if current != second {
		t.Fatal("new daemon was not registered")
	}
	select {
	case <-respawned:
		t.Fatal("replacing a daemon triggered an automatic respawn")
	case <-time.After(1500 * time.Millisecond):
	}
}

// The background bootstrap shares the engine mutex with EnsureEngine and
// does nothing once a daemon is already running.
func TestBootstrapSkipsWhenDaemonRunningAndHoldsEngineMutex(t *testing.T) {
	a := &DesktopApp{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	defer a.killChild()
	live := exec.Command("sleep", "30")
	if err := live.Start(); err != nil {
		t.Fatal(err)
	}
	a.setChild(live)

	a.engineMu.Lock()
	done := make(chan struct{})
	go func() {
		a.BootstrapOpenCodeAsync(context.Background(), 0, "pass")
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("bootstrap ran while EnsureEngine held the engine mutex")
	case <-time.After(200 * time.Millisecond):
	}
	a.engineMu.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("bootstrap did not return")
	}
	a.childMu.Lock()
	current := a.child
	a.childMu.Unlock()
	if current != live {
		t.Fatal("bootstrap replaced an already running daemon")
	}
}
