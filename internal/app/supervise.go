package app

import (
	"context"
	"errors"
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
// first started with, so the gateway's upstream URL stays valid. A pending
// restart is abandoned once the generation changes, so a manual EnsureEngine
// retry cannot be raced into a second daemon by an older supervisor.
type respawner struct {
	generation uint64
	start      func() (*exec.Cmd, error)
}

// startOpenCode starts the daemon and remembers how to restart it. Every
// explicit start gets a new generation, invalidating respawns of older ones.
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
	a.childMu.Lock()
	a.childGen++
	generation := a.childGen
	a.childMu.Unlock()
	child, err := start()
	if err != nil {
		return nil, err
	}
	a.childMu.Lock()
	a.respawn = &respawner{
		generation: generation,
		start: func() (*exec.Cmd, error) {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return start()
		},
	}
	a.childMu.Unlock()
	return child, nil
}

// startResolvedEngine starts binPath and rolls back to the previous managed
// binary when a freshly installed one cannot serve. A backup only exists after
// an install replaced an older binary, so external binaries never roll back.
func (a *DesktopApp) startResolvedEngine(ctx context.Context, binPath string, port int, password string) (*exec.Cmd, error) {
	child, err := a.startOpenCode(ctx, binPath, port, password)
	if err == nil {
		opencode.DiscardBinaryBackup(binPath)
		return child, nil
	}
	restored, restoreErr := opencode.RestoreBinaryBackup(binPath)
	if restoreErr != nil || !restored {
		return nil, err
	}
	if a.logger != nil {
		a.logger.Warn("new opencode binary failed to start; rolled back to the previous one", "error", err)
	}
	return a.startOpenCode(ctx, binPath, port, password)
}

// EnsureEngine resolves (downloading and verifying when needed) the OpenCode
// binary and makes sure the supervised daemon is healthy on the boot-time
// loopback port. The settings page uses it to install or retry after a failed
// background bootstrap. It reports the resolved binary path.
func (a *DesktopApp) EnsureEngine(ctx context.Context) (string, error) {
	return a.ensureEngine(ctx, false)
}

// RestartEngine replaces a running daemon with a fresh process on the same
// endpoint, reusing the installed binary and its credentials.
func (a *DesktopApp) RestartEngine(ctx context.Context) (string, error) {
	return a.ensureEngine(ctx, true)
}

func (a *DesktopApp) ensureEngine(ctx context.Context, restart bool) (string, error) {
	a.engineMu.Lock()
	defer a.engineMu.Unlock()

	if a.openCodePort == 0 || a.openCodePass == "" {
		return "", errors.New("opencode endpoint is not configured")
	}
	binPath, err := opencode.ResolveOrInstall(ctx, a.cfg.OpenCode, a.logger)
	if err != nil {
		return "", err
	}
	if !restart {
		if err := opencode.WaitForHealth(ctx, a.openCodePort, a.openCodePass, 2*time.Second); err == nil {
			return binPath, nil
		}
	}

	// Detach the stale child before killing it: its waiter then sees a newer
	// owner and neither clears the status nor triggers an automatic respawn.
	// That waiter is the only one reaping it; wait for it so the port is free.
	a.childMu.Lock()
	stale := a.child
	staleDone := a.childDone
	a.child = nil
	a.respawns = nil
	a.childMu.Unlock()
	if stale != nil && stale.Process != nil {
		_ = stale.Process.Kill()
		if staleDone != nil {
			select {
			case <-staleDone:
			case <-time.After(5 * time.Second):
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
	}

	startCtx := a.runCtx
	if startCtx == nil {
		startCtx = ctx
	}
	child, err := a.startResolvedEngine(startCtx, binPath, a.openCodePort, a.openCodePass)
	if err != nil {
		return "", err
	}
	a.setChild(child)
	return binPath, nil
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
func (a *DesktopApp) respawnChild(restart *respawner) {
	delay, ok := a.respawnBudget(time.Now())
	if !ok {
		if a.logger != nil {
			a.logger.Error("opencode daemon keeps exiting; not restarting", "source", "opencode", "limit", respawnLimit, "window", respawnWindow)
		}
		return
	}
	time.Sleep(delay)
	// Serialise with EnsureEngine / BootstrapOpenCodeAsync: if one of them has
	// already brought up a new daemon, there is nothing to restart.
	a.engineMu.Lock()
	defer a.engineMu.Unlock()
	a.childMu.Lock()
	stale := a.childStopped || a.child != nil || a.childGen != restart.generation
	a.childMu.Unlock()
	if stale {
		return
	}
	child, err := restart.start()
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
