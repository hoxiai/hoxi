//go:build windows

package updater

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hoxiai/hoxi/internal/subproc"
)

var (
	winNativeReady atomic.Bool
	winNativeMu    sync.Mutex
	winCanInstall  func(force bool) bool
	winRelease     func()
	winInstalling  bool
	winExecutable  string
)

const windowsSwapAndRestartPS = `
$ErrorActionPreference = 'SilentlyContinue'
$targetPid = [int]$env:HOXI_UPD_PID
$stageDir  = $env:HOXI_UPD_STAGE_DIR
$targetDir = $env:HOXI_UPD_TARGET_DIR
$exeName   = $env:HOXI_UPD_EXE

if ($targetPid -gt 0) {
  try { Wait-Process -Id $targetPid -Timeout 30 } catch {}
}

for ($i = 0; $i -lt 20; $i++) {
  try {
    if (Test-Path (Join-Path $stageDir 'opencode.exe')) {
      Move-Item -Force (Join-Path $stageDir 'opencode.exe') (Join-Path $targetDir 'opencode.exe')
    }
    if (Test-Path (Join-Path $stageDir 'sqlite3.exe')) {
      Move-Item -Force (Join-Path $stageDir 'sqlite3.exe') (Join-Path $targetDir 'sqlite3.exe')
    }
    Move-Item -Force (Join-Path $stageDir 'Hoxi.exe') (Join-Path $targetDir $exeName)
    break
  } catch {
    Start-Sleep -Milliseconds 300
  }
}

Remove-Item -Recurse -Force $stageDir
Start-Process -FilePath (Join-Path $targetDir $exeName)
`

func StartNative(canInstall func(force bool) bool, release func()) error {
	if canInstall == nil || release == nil {
		return errors.New("native updater requires admission and release callbacks")
	}
	exe, err := os.Executable()
	if err != nil || strings.TrimSpace(exe) == "" {
		return errors.New("cannot resolve current executable path")
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil && real != "" {
		exe = real
	}
	winNativeMu.Lock()
	winCanInstall = canInstall
	winRelease = release
	winExecutable = exe
	winInstalling = false
	winNativeMu.Unlock()
	winNativeReady.Store(true)
	return nil
}

func NativeAvailable() bool {
	return winNativeReady.Load()
}

func CheckNative() error {
	if !NativeAvailable() {
		return errors.New("native updater is unavailable")
	}
	winNativeMu.Lock()
	if winInstalling {
		winNativeMu.Unlock()
		return errors.New("update installation is already in progress")
	}
	canInstall := winCanInstall
	release := winRelease
	exePath := winExecutable
	winInstalling = true
	winNativeMu.Unlock()

	cleanupAbort := func() {
		winNativeMu.Lock()
		winInstalling = false
		winNativeMu.Unlock()
		if release != nil {
			release()
		}
	}

	if canInstall == nil || !canInstall(false) {
		cleanupAbort()
		return errors.New("cannot install update while agent sessions are active")
	}

	checker := activeChecker.Load()
	if checker == nil {
		cleanupAbort()
		return errors.New("update checker is not initialized")
	}
	res := checker.LastResult()
	if res == nil || !res.NeedUpdate {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		checked, err := checker.Check(ctx)
		cancel()
		if err != nil {
			cleanupAbort()
			return err
		}
		res = checked
	}
	if res == nil || !res.NeedUpdate {
		cleanupAbort()
		return errors.New("application is already up to date")
	}

	archiveURL := resolveWindowsArchiveURL(res)
	if archiveURL == "" {
		cleanupAbort()
		return errors.New("update download URL is missing")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	targetDir := filepath.Dir(exePath)
	stageDir, err := os.MkdirTemp(targetDir, ".hoxi-update-staging-*")
	if err != nil {
		cleanupAbort()
		return fmt.Errorf("create staging directory: %w", err)
	}
	if err := downloadAndStageWindowsZip(ctx, nil, archiveURL, res.SHA256, stageDir); err != nil {
		_ = os.RemoveAll(stageDir)
		cleanupAbort()
		return err
	}

	stagedExe := filepath.Join(stageDir, "Hoxi.exe")
	verCtx, verCancel := context.WithTimeout(context.Background(), 10*time.Second)
	out, err := subproc.CommandContext(verCtx, stagedExe, "--version").Output()
	verCancel()
	if err != nil || strings.TrimSpace(string(out)) != strings.TrimPrefix(strings.TrimSpace(res.LatestVersion), "v") {
		_ = os.RemoveAll(stageDir)
		cleanupAbort()
		return errors.New("staged Hoxi.exe version verification failed")
	}

	psBin, err := exec.LookPath("powershell.exe")
	if err != nil {
		psBin = "powershell.exe"
	}
	cmd := subproc.Command(psBin, "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", windowsSwapAndRestartPS)
	cmd.Env = append(os.Environ(),
		"HOXI_UPD_PID="+strconv.Itoa(os.Getpid()),
		"HOXI_UPD_STAGE_DIR="+stageDir,
		"HOXI_UPD_TARGET_DIR="+targetDir,
		"HOXI_UPD_EXE="+filepath.Base(exePath),
	)
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(stageDir)
		cleanupAbort()
		return fmt.Errorf("launch update installer: %w", err)
	}
	_ = cmd.Process.Release()
	go func() {
		time.Sleep(250 * time.Millisecond)
		os.Exit(0)
	}()

	return nil
}

func StopNative() {
	if winNativeReady.Swap(false) {
		winNativeMu.Lock()
		release := winRelease
		winCanInstall = nil
		winRelease = nil
		winInstalling = false
		winNativeMu.Unlock()
		if release != nil {
			release()
		}
	}
}

var _ = fmt.Sprintf
