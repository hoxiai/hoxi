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
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/hoxiai/hoxi/internal/subproc"
)

var (
	winNativeReady       atomic.Bool
	winNativeMu          sync.Mutex
	winCanInstall        func(force bool) bool
	winRelease           func()
	winInstalling        bool
	winExecutable        string
	winConfirmBusyUpdate = confirmWindowsBusyUpdate

	user32DLL                = windows.NewLazySystemDLL("user32.dll")
	procMessageBoxW          = user32DLL.NewProc("MessageBoxW")
	kernel32DLL              = windows.NewLazySystemDLL("kernel32.dll")
	procGetUserDefaultUILang = kernel32DLL.NewProc("GetUserDefaultUILanguage")
)

const (
	mbYesNo         = 0x00000004
	mbIconWarning   = 0x00000030
	mbSetForeground = 0x00010000
	mbTopMost       = 0x00040000
	idYes           = 6
	langChinese     = 0x04
)

func isWindowsChineseLocale() bool {
	if procGetUserDefaultUILang.Find() == nil {
		langID, _, _ := procGetUserDefaultUILang.Call()
		if langID&0x3ff == langChinese {
			return true
		}
	}
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(os.Getenv(key))), "zh") {
			return true
		}
	}
	return false
}

func confirmWindowsBusyUpdate() bool {
	if procMessageBoxW.Find() != nil {
		return false
	}
	title := "Hoxi is still running tasks"
	body := "Hoxi will finish the current tool step, save task checkpoints, and resume them automatically after restarting. Update now?"
	if isWindowsChineseLocale() {
		title = "Hoxi 有任务正在运行"
		body = "Hoxi 将等待当前工具步落地并保存检查点，重启后自动断点续跑。现在更新并重启吗？"
	}
	titlePtr, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return false
	}
	bodyPtr, err := windows.UTF16PtrFromString(body)
	if err != nil {
		return false
	}
	traceUpdate("updater busy confirm shown", 0, 0)
	ret, _, _ := procMessageBoxW.Call(
		0,
		uintptr(unsafe.Pointer(bodyPtr)),
		uintptr(unsafe.Pointer(titlePtr)),
		uintptr(mbYesNo|mbIconWarning|mbSetForeground|mbTopMost),
	)
	traceUpdate("updater busy confirm answered", 0, 0)
	return ret == idYes
}

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

func CheckNative() (installErr error) {
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
	setInstallPhase("preparing")
	defer func() {
		finishInstallProgress(installErr)
	}()

	cleanupAbort := func() {
		winNativeMu.Lock()
		winInstalling = false
		winNativeMu.Unlock()
		if release != nil {
			release()
		}
	}

	if !admitSelfUpdate(canInstall, winConfirmBusyUpdate) {
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
	setInstallPhase("downloading")
	if err := downloadAndStageWindowsZip(ctx, nil, archiveURL, res.SHA256, stageDir, func(p InstallProgress) {
		installProgress.Store(&p)
	}); err != nil {
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

	setInstallPhase("restarting")
	runBeforeRestart()

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
