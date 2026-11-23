package updater

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/agentcli"
)

const maxUpdateArchiveBytes = 512 << 20 // 512 MiB

// windowsSwapAndRestartPS runs detached after Hoxi.exe exits on Windows.
// ErrorActionPreference must be Stop so Move-Item file-lock failures trigger
// the retry catch block instead of silently breaking out and deleting stageDir.
const windowsSwapAndRestartPS = `
$ErrorActionPreference = 'Stop'
$targetPid = [int]$env:HOXI_UPD_PID
$stageDir  = $env:HOXI_UPD_STAGE_DIR
$targetDir = $env:HOXI_UPD_TARGET_DIR
$exeName   = $env:HOXI_UPD_EXE

if ($targetPid -gt 0) {
  try { Wait-Process -Id $targetPid -Timeout 30 -ErrorAction SilentlyContinue } catch {}
}

$stagedOpenCode = Join-Path $stageDir 'opencode.exe'
$stagedSqlite   = Join-Path $stageDir 'sqlite3.exe'
$stagedHoxi     = Join-Path $stageDir 'Hoxi.exe'
$hasOpenCode    = Test-Path -LiteralPath $stagedOpenCode
$hasSqlite      = Test-Path -LiteralPath $stagedSqlite
$swapped        = $false

for ($i = 0; $i -lt 30; $i++) {
  try {
    # Swap Hoxi.exe first: it is the process most likely still releasing its lock,
    # so retrying here avoids replacing sidecars while Hoxi.exe stays on the old build.
    if (Test-Path -LiteralPath $stagedHoxi) {
      Move-Item -Force -ErrorAction Stop -LiteralPath $stagedHoxi -Destination (Join-Path $targetDir $exeName)
    }
    if ($hasOpenCode) {
      if (Test-Path -LiteralPath $stagedOpenCode) {
        Move-Item -Force -ErrorAction Stop -LiteralPath $stagedOpenCode -Destination (Join-Path $targetDir 'opencode.exe')
      }
    } else {
      Remove-Item -Force -ErrorAction SilentlyContinue -LiteralPath (Join-Path $targetDir 'opencode.exe')
    }
    if ($hasSqlite) {
      if (Test-Path -LiteralPath $stagedSqlite) {
        Move-Item -Force -ErrorAction Stop -LiteralPath $stagedSqlite -Destination (Join-Path $targetDir 'sqlite3.exe')
      }
    } else {
      Remove-Item -Force -ErrorAction SilentlyContinue -LiteralPath (Join-Path $targetDir 'sqlite3.exe')
    }
    $swapped = $true
    break
  } catch {
    Start-Sleep -Milliseconds 300
  }
}

if ($swapped) {
  Remove-Item -Recurse -Force -ErrorAction SilentlyContinue -LiteralPath $stageDir
}
Start-Process -FilePath (Join-Path $targetDir $exeName)
`

var (
	downloadStallTimeout = 45 * time.Second
	beforeRestartMu      sync.Mutex
	beforeRestartHook    func()
)

// SetBeforeRestart registers an optional callback invoked right before a
// self-update exits to relaunch, allowing active tasks to drain their current
// tool step and persist restart checkpoints.
func SetBeforeRestart(fn func()) {
	beforeRestartMu.Lock()
	beforeRestartHook = fn
	beforeRestartMu.Unlock()
}

func runBeforeRestart() {
	beforeRestartMu.Lock()
	fn := beforeRestartHook
	beforeRestartMu.Unlock()
	if fn != nil {
		fn()
	}
}

func admitSelfUpdate(canInstall func(force bool) bool, confirmBusy func() bool) bool {
	if canInstall == nil {
		return false
	}
	started := time.Now()
	if canInstall(false) {
		traceUpdate("updater admission checked", 200, time.Since(started))
		return true
	}
	traceUpdate("updater admission checked", 409, time.Since(started))
	if confirmBusy == nil || !confirmBusy() {
		return false
	}
	started = time.Now()
	if canInstall(true) {
		traceUpdate("updater admission checked", 200, time.Since(started))
		return true
	}
	traceUpdate("updater admission checked", 409, time.Since(started))
	return false
}

type stallTimeoutReader struct {
	r       io.Reader
	timeout time.Duration
	cancel  context.CancelCauseFunc
}

func (s *stallTimeoutReader) Read(p []byte) (int, error) {
	if s.timeout <= 0 {
		return s.r.Read(p)
	}
	timer := time.AfterFunc(s.timeout, func() {
		s.cancel(fmt.Errorf("update download stalled (no data received for %s)", s.timeout))
	})
	n, err := s.r.Read(p)
	timer.Stop()
	if err != nil && n <= 0 {
		return n, err
	}
	return n, err
}

func newUpdateTransport() *http.Transport {
	var tr *http.Transport
	if base, ok := http.DefaultTransport.(*http.Transport); ok && base != nil {
		tr = base.Clone()
	} else {
		tr = &http.Transport{}
	}
	tr.Proxy = agentcli.SystemProxyURL
	tr.DialContext = (&net.Dialer{
		Timeout:   15 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext
	tr.TLSHandshakeTimeout = 15 * time.Second
	tr.ResponseHeaderTimeout = 30 * time.Second
	return tr
}

func newUpdateHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   10 * time.Minute,
		Transport: newUpdateTransport(),
	}
}

func alignWindowsArchiveArch(rawURL, arch string) string {
	if arch == "" {
		return rawURL
	}
	for _, other := range []string{"amd64", "arm64"} {
		if other == arch {
			continue
		}
		oldSuffix := "-windows-" + other + ".zip"
		if strings.HasSuffix(strings.ToLower(rawURL), oldSuffix) {
			return rawURL[:len(rawURL)-len(oldSuffix)] + "-windows-" + arch + ".zip"
		}
	}
	return rawURL
}

func resolveWindowsArchiveURL(res *UpdateCheckResult) string {
	candidates := resolveWindowsArchiveCandidates(res, DownloadSourceAuto)
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0].URL
}

// windowsReleaseArch maps the running Go architecture to the Windows portable
// zip release asset suffix so arm64 and x64 clients fetch their own installer.
func windowsReleaseArch() string {
	if runtime.GOARCH == "arm64" {
		return "arm64"
	}
	return "amd64"
}

// darwinReleaseArch maps the running Go architecture to the macOS DMG release
// asset suffix so Intel and Apple Silicon clients fetch their own installer.
func darwinReleaseArch() string {
	if runtime.GOARCH == "amd64" {
		return "amd64"
	}
	return "arm64"
}

func alignDarwinDMGArch(rawURL, arch string) string {
	if arch == "" {
		return rawURL
	}
	for _, other := range []string{"arm64", "amd64"} {
		if other == arch {
			continue
		}
		oldSuffix := "-darwin-" + other + ".dmg"
		if strings.HasSuffix(strings.ToLower(rawURL), oldSuffix) {
			return rawURL[:len(rawURL)-len(oldSuffix)] + "-darwin-" + arch + ".dmg"
		}
	}
	return rawURL
}

func resolveDarwinDMGURL(res *UpdateCheckResult) string {
	candidates := resolveDarwinDMGCandidates(res, DownloadSourceAuto)
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0].URL
}

// resolveDarwinAppBundlePath parses POSIX macOS bundle paths regardless of the
// host OS, so it uses path (always "/") rather than filepath (OS separators).
func resolveDarwinAppBundlePath(exePath string) (string, error) {
	cleaned := path.Clean(strings.TrimSpace(exePath))
	if cleaned == "" || cleaned == "." {
		return "", errors.New("empty executable path")
	}
	macOSDir := path.Dir(cleaned)
	contentsDir := path.Dir(macOSDir)
	appBundle := path.Dir(contentsDir)
	if path.Base(macOSDir) != "MacOS" || path.Base(contentsDir) != "Contents" ||
		!strings.HasSuffix(strings.ToLower(appBundle), ".app") {
		return "", errors.New("executable is not inside a macOS .app bundle")
	}
	if strings.EqualFold(path.Base(appBundle), "Hoxi Dev.app") {
		return "", errors.New("in-app self-update is disabled for Hoxi Dev.app")
	}
	return appBundle, nil
}

func downloadAndVerifyDMG(ctx context.Context, client *http.Client, dmgURL, fallbackSHA256, dstPath string, report func(InstallProgress)) error {
	return downloadAndVerifyDMGCandidates(ctx, client, []downloadCandidate{{
		URL:    dmgURL,
		Source: classifyUpdateSource(dmgURL),
	}}, fallbackSHA256, dstPath, report)
}

func downloadAndVerifyDMGCandidates(ctx context.Context, client *http.Client, candidates []downloadCandidate, fallbackSHA256, dstPath string, report func(InstallProgress)) error {
	return downloadFileWithFallback(ctx, client, candidates, fallbackSHA256, dstPath, report)
}

func isAllowedUpdateURL(rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	if u.Scheme == "http" {
		host := strings.ToLower(u.Hostname())
		return host == "127.0.0.1" || host == "localhost" || host == "::1"
	}
	return false
}

func parseSHA256Text(raw string) string {
	for _, field := range strings.Fields(strings.TrimSpace(raw)) {
		candidate := strings.ToLower(strings.TrimSpace(field))
		if len(candidate) == 64 && isHexString(candidate) {
			return candidate
		}
	}
	return ""
}

func parseSHA256ForFilename(raw, filename string) string {
	target := strings.TrimSpace(filename)
	if target == "" {
		return ""
	}
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		sum := strings.ToLower(strings.TrimSpace(fields[0]))
		if len(sum) != 64 || !isHexString(sum) {
			continue
		}
		for _, field := range fields[1:] {
			cleaned := strings.TrimPrefix(strings.TrimSpace(field), "*")
			if path.Base(cleaned) == target {
				return sum
			}
		}
	}
	return ""
}

func resolveChecksumsURL(archiveURL string) (string, string) {
	u, err := url.Parse(strings.TrimSpace(archiveURL))
	if err != nil || u.Host == "" {
		return "", ""
	}
	filename := path.Base(u.Path)
	if filename == "" || filename == "." || filename == "/" {
		return "", ""
	}
	u.Path = path.Join(path.Dir(u.Path), "checksums.txt")
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), filename
}

func isHexString(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return len(s) > 0
}

func resolveArchiveSHA256(ctx context.Context, client *http.Client, archiveURL, fallbackSHA256 string) (string, error) {
	sidecarURL := archiveURL + ".sha256"
	hostReachable := true
	if isAllowedUpdateURL(sidecarURL) {
		sidecarCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		req, err := http.NewRequestWithContext(sidecarCtx, http.MethodGet, sidecarURL, nil)
		if err == nil {
			resp, err := client.Do(req)
			if err != nil {
				hostReachable = false
			} else {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
				_ = resp.Body.Close()
				if resp.StatusCode >= 500 {
					hostReachable = false
				}
				if resp.StatusCode == http.StatusOK {
					if sum := parseSHA256Text(string(body)); sum != "" {
						cancel()
						return sum, nil
					}
				}
			}
		}
		cancel()
	}
	if hostReachable {
		if checksumsURL, filename := resolveChecksumsURL(archiveURL); isAllowedUpdateURL(checksumsURL) && filename != "" {
			checksumsCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			req, err := http.NewRequestWithContext(checksumsCtx, http.MethodGet, checksumsURL, nil)
			if err == nil {
				resp, err := client.Do(req)
				if err == nil {
					body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
					_ = resp.Body.Close()
					if resp.StatusCode == http.StatusOK {
						if sum := parseSHA256ForFilename(string(body), filename); sum != "" {
							cancel()
							return sum, nil
						}
					}
				}
			}
			cancel()
		}
	}
	if sum := parseSHA256Text(fallbackSHA256); sum != "" {
		return sum, nil
	}
	return "", errors.New("missing SHA-256 checksum for update archive")
}

// downloadAndStageWindowsZip downloads the release zip from archiveURL, verifies
// its SHA-256 against archiveURL+".sha256" (or fallbackSHA256), and extracts
// Hoxi.exe and optional opencode.exe / sqlite3.exe into stageDir.
func downloadAndStageWindowsZip(ctx context.Context, client *http.Client, archiveURL, fallbackSHA256, stageDir string, report func(InstallProgress)) error {
	return downloadAndStageWindowsZipCandidates(ctx, client, []downloadCandidate{{
		URL:    archiveURL,
		Source: classifyUpdateSource(archiveURL),
	}}, fallbackSHA256, stageDir, report)
}

func downloadAndStageWindowsZipCandidates(ctx context.Context, client *http.Client, candidates []downloadCandidate, fallbackSHA256, stageDir string, report func(InstallProgress)) error {
	if err := os.MkdirAll(stageDir, 0o700); err != nil {
		return err
	}
	zipPath := filepath.Join(stageDir, "update.zip")
	if err := downloadFileWithFallback(ctx, client, candidates, fallbackSHA256, zipPath, report); err != nil {
		_ = os.Remove(zipPath)
		return err
	}
	var written int64
	if info, err := os.Stat(zipPath); err == nil {
		written = info.Size()
	}

	if report != nil {
		report(InstallProgress{Phase: "staging", DownloadedBytes: written, TotalBytes: written})
	}
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		_ = os.Remove(zipPath)
		return fmt.Errorf("open update zip: %w", err)
	}
	defer func() {
		_ = zr.Close()
		_ = os.Remove(zipPath)
	}()

	foundHoxiExe := false
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		base := filepath.Base(strings.ReplaceAll(f.Name, `\`, `/`))
		var targetName string
		switch {
		case strings.EqualFold(base, "Hoxi.exe"):
			targetName = "Hoxi.exe"
			foundHoxiExe = true
		case strings.EqualFold(base, "opencode.exe"):
			targetName = "opencode.exe"
		case strings.EqualFold(base, "sqlite3.exe"):
			targetName = "sqlite3.exe"
		default:
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		dstPath := filepath.Join(stageDir, targetName)
		dstFile, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700)
		if err != nil {
			_ = rc.Close()
			return err
		}
		_, err = io.Copy(dstFile, io.LimitReader(rc, maxUpdateArchiveBytes))
		_ = rc.Close()
		if closeErr := dstFile.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
	}
	_ = zr.Close()
	_ = os.Remove(zipPath)
	if !foundHoxiExe {
		return errors.New("update archive does not contain Hoxi.exe")
	}
	return nil
}
