package updater

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
)

const maxUpdateArchiveBytes = 512 << 20 // 512 MiB

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

func newUpdateHTTPClient() *http.Client {
	var tr *http.Transport
	if base, ok := http.DefaultTransport.(*http.Transport); ok && base != nil {
		tr = base.Clone()
	} else {
		tr = &http.Transport{Proxy: http.ProxyFromEnvironment}
	}
	tr.DialContext = (&net.Dialer{
		Timeout:   15 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext
	tr.TLSHandshakeTimeout = 15 * time.Second
	tr.ResponseHeaderTimeout = 30 * time.Second
	return &http.Client{
		Timeout:   10 * time.Minute,
		Transport: tr,
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
	if res == nil {
		return ""
	}
	arch := windowsReleaseArch()
	for _, cand := range []string{res.DownloadURL, res.Mirrors.Global} {
		trimmed := strings.TrimSpace(cand)
		lower := strings.ToLower(trimmed)
		if trimmed != "" && strings.HasSuffix(lower, ".zip") {
			return alignWindowsArchiveArch(trimmed, arch)
		}
		if trimmed != "" && strings.HasSuffix(lower, "-setup.exe") {
			return alignWindowsArchiveArch(trimmed[:len(trimmed)-len("-setup.exe")]+".zip", arch)
		}
	}
	ver := strings.TrimPrefix(strings.TrimSpace(res.LatestVersion), "v")
	if ver != "" {
		return fmt.Sprintf("https://github.com/hoxiai/hoxi/releases/download/v%s/Hoxi-%s-windows-%s.zip", ver, ver, arch)
	}
	if d := strings.TrimSpace(res.DownloadURL); d != "" {
		return d
	}
	return strings.TrimSpace(res.Mirrors.Global)
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
	if res == nil {
		return ""
	}
	arch := darwinReleaseArch()
	for _, cand := range []string{res.DownloadURL, res.Mirrors.Global} {
		trimmed := strings.TrimSpace(cand)
		if trimmed != "" && strings.HasSuffix(strings.ToLower(trimmed), ".dmg") {
			return alignDarwinDMGArch(trimmed, arch)
		}
	}
	ver := strings.TrimPrefix(strings.TrimSpace(res.LatestVersion), "v")
	if ver != "" {
		return fmt.Sprintf("https://github.com/hoxiai/hoxi/releases/download/v%s/Hoxi-%s-darwin-%s.dmg", ver, ver, arch)
	}
	if d := strings.TrimSpace(res.DownloadURL); d != "" {
		return d
	}
	return strings.TrimSpace(res.Mirrors.Global)
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
	if !isAllowedUpdateURL(dmgURL) {
		return errors.New("update download URL must use HTTPS")
	}
	if client == nil {
		client = newUpdateHTTPClient()
	}
	expectedSHA, err := resolveArchiveSHA256(ctx, client, dmgURL, fallbackSHA256)
	if err != nil {
		return err
	}

	reqCtx, reqCancel := context.WithCancelCause(ctx)
	defer reqCancel(nil)

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, dmgURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download update DMG: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("update download returned HTTP %d", resp.StatusCode)
	}

	if err := os.MkdirAll(filepath.Dir(dstPath), 0o700); err != nil {
		return err
	}
	outFile, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	hasher := sha256.New()
	bodyReader := &stallTimeoutReader{r: resp.Body, timeout: downloadStallTimeout, cancel: reqCancel}
	written, copyErr := io.Copy(withDownloadProgress(io.MultiWriter(outFile, hasher), resp.ContentLength, report), io.LimitReader(bodyReader, maxUpdateArchiveBytes+1))
	closeErr := outFile.Close()
	if copyErr != nil {
		_ = os.Remove(dstPath)
		if cause := context.Cause(reqCtx); cause != nil && errors.Is(copyErr, context.Canceled) {
			return cause
		}
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(dstPath)
		return closeErr
	}
	if written > maxUpdateArchiveBytes {
		_ = os.Remove(dstPath)
		return errors.New("update DMG exceeds maximum allowed size")
	}

	if report != nil {
		report(InstallProgress{Phase: "verifying", DownloadedBytes: written, TotalBytes: written})
	}
	actualSHA := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(actualSHA, expectedSHA) {
		_ = os.Remove(dstPath)
		return fmt.Errorf("update DMG SHA-256 mismatch: got %s, want %s", actualSHA, expectedSHA)
	}
	return nil
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
	if isAllowedUpdateURL(sidecarURL) {
		sidecarCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		req, err := http.NewRequestWithContext(sidecarCtx, http.MethodGet, sidecarURL, nil)
		if err == nil {
			resp, err := client.Do(req)
			if err == nil {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
				_ = resp.Body.Close()
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
	if sum := parseSHA256Text(fallbackSHA256); sum != "" {
		return sum, nil
	}
	return "", errors.New("missing SHA-256 checksum for update archive")
}

// downloadAndStageWindowsZip downloads the release zip from archiveURL, verifies
// its SHA-256 against archiveURL+".sha256" (or fallbackSHA256), and extracts
// Hoxi.exe and optional opencode.exe / sqlite3.exe into stageDir.
func downloadAndStageWindowsZip(ctx context.Context, client *http.Client, archiveURL, fallbackSHA256, stageDir string, report func(InstallProgress)) error {
	if !isAllowedUpdateURL(archiveURL) {
		return errors.New("update download URL must use HTTPS")
	}
	if client == nil {
		client = newUpdateHTTPClient()
	}
	expectedSHA, err := resolveArchiveSHA256(ctx, client, archiveURL, fallbackSHA256)
	if err != nil {
		return err
	}

	reqCtx, reqCancel := context.WithCancelCause(ctx)
	defer reqCancel(nil)

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, archiveURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download update archive: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("update download returned HTTP %d", resp.StatusCode)
	}

	if err := os.MkdirAll(stageDir, 0o700); err != nil {
		return err
	}
	zipPath := filepath.Join(stageDir, "update.zip")
	outFile, err := os.OpenFile(zipPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	hasher := sha256.New()
	bodyReader := &stallTimeoutReader{r: resp.Body, timeout: downloadStallTimeout, cancel: reqCancel}
	written, copyErr := io.Copy(withDownloadProgress(io.MultiWriter(outFile, hasher), resp.ContentLength, report), io.LimitReader(bodyReader, maxUpdateArchiveBytes+1))
	closeErr := outFile.Close()
	if copyErr != nil {
		if cause := context.Cause(reqCtx); cause != nil && errors.Is(copyErr, context.Canceled) {
			return cause
		}
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written > maxUpdateArchiveBytes {
		return errors.New("update archive exceeds maximum allowed size")
	}

	if report != nil {
		report(InstallProgress{Phase: "verifying", DownloadedBytes: written, TotalBytes: written})
	}
	actualSHA := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(actualSHA, expectedSHA) {
		return fmt.Errorf("update archive SHA-256 mismatch: got %s, want %s", actualSHA, expectedSHA)
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
