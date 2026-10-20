package app

import (
	"context"
	"os/exec"
	"time"

	"github.com/hoxiai/hoxi/internal/opencode"
	"github.com/hoxiai/hoxi/internal/provider"
	"github.com/hoxiai/hoxi/internal/skills"
)

// The OpenCode daemon is restarted when it exits on its own. A daemon that
// keeps dying is left down after a few attempts, so a broken binary or config
// cannot spin the machine; the status bar then reports it unavailable.

const (
	respawnLimit  = 3
	respawnWindow = 5 * time.Minute
	respawnDelay  = time.Second
)

// respawner restarts the daemon with the binary, port and password it was
// first started with, so the gateway's upstream URL stays valid.
type respawner func() (*exec.Cmd, error)

// startOpenCode starts the daemon and remembers how to restart it.
func (a *DesktopApp) startOpenCode(ctx context.Context, binPath string, port int, password string) (*exec.Cmd, error) {
	start := func() (*exec.Cmd, error) {
		if seeded, err := skills.SeedDefaults(); err != nil {
			a.logger.Warn("failed to seed default skills", "error", err)
		} else if seeded > 0 {
			a.logger.Info("seeded default skills", "count", seeded)
		}
		cfgPath := provider.EnsureHoxiModelsConfig(a.cfg.RootDir)
		return opencode.StartDaemonWithBin(ctx, binPath, a.cfg.RootDir, cfgPath, port, password, a.logger, a.browserEnv...)
	}
	child, err := start()
	if err != nil {
		return nil, err
	}
	a.childMu.Lock()
	a.respawn = func() (*exec.Cmd, error) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return start()
	}
	a.childMu.Unlock()
	return child, nil
}

// respawnBudget reports the backoff for the next restart, or false once the
// daemon has crashed respawnLimit times within respawnWindow.
func (a *DesktopApp) respawnBudget(now time.Time) (time.Duration, bool) {
	a.childMu.Lock()
	defer a.childMu.Unlock()
	recent := a.respawns[:0]
	for _, at := range a.respawns {
		if now.Sub(at) < respawnWindow {
			recent = append(recent, at)
		}
	}
	a.respawns = recent
	if len(recent) >= respawnLimit {
		return 0, false
	}
	a.respawns = append(a.respawns, now)
	return respawnDelay << len(recent), true
}

// respawnChild runs after an unexpected daemon exit.
func (a *DesktopApp) respawnChild(restart respawner) {
	delay, ok := a.respawnBudget(time.Now())
	if !ok {
		if a.logger != nil {
			a.logger.Error("opencode daemon keeps exiting; not restarting", "source", "opencode", "limit", respawnLimit, "window", respawnWindow)
		}
		return
	}
	time.Sleep(delay)
	a.childMu.Lock()
	stopped := a.childStopped
	a.childMu.Unlock()
	if stopped {
		return
	}
	child, err := restart()
	if err != nil {
		if a.logger != nil {
			a.logger.Error("opencode daemon restart failed", "source", "opencode", "error", err)
		}
		return
	}
	if a.logger != nil {
		a.logger.Info("opencode daemon restarted", "source", "opencode", "pid", child.Process.Pid)
	}
	a.setChild(child)
}
