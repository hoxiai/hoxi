package updater

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const maxUpdateArchiveBytes = 512 << 20 // 512 MiB

func resolveWindowsArchiveURL(res *UpdateCheckResult) string {
	if res == nil {
		return ""
	}
	for _, cand := range []string{res.DownloadURL, res.Mirrors.Global} {
		trimmed := strings.TrimSpace(cand)
		lower := strings.ToLower(trimmed)
		if trimmed != "" && strings.HasSuffix(lower, ".zip") {
			return trimmed
		}
		if trimmed != "" && strings.HasSuffix(lower, "-setup.exe") {
			return trimmed[:len(trimmed)-len("-setup.exe")] + ".zip"
		}
	}
	ver := strings.TrimPrefix(strings.TrimSpace(res.LatestVersion), "v")
	if ver != "" {
		return fmt.Sprintf("https://github.com/hoxiai/hoxi/releases/download/v%s/Hoxi-%s-windows-amd64.zip", ver, ver)
	}
	if d := strings.TrimSpace(res.DownloadURL); d != "" {
		return d
	}
	return strings.TrimSpace(res.Mirrors.Global)
}

// darwinReleaseArch maps the running Go architecture to the macOS DMG release
// asset suffix so Intel and Apple Silicon clients fetch their own installer.
func darwinReleaseArch() string {
	if runtime.GOARCH == "amd64" {
		return "amd64"
	}
	return "arm64"
}

func resolveDarwinDMGURL(res *UpdateCheckResult) string {
	if res == nil {
		return ""
	}
	for _, cand := range []string{res.DownloadURL, res.Mirrors.Global} {
		trimmed := strings.TrimSpace(cand)
		if trimmed != "" && strings.HasSuffix(strings.ToLower(trimmed), ".dmg") {
			return trimmed
		}
	}
	ver := strings.TrimPrefix(strings.TrimSpace(res.LatestVersion), "v")
	if ver != "" {
		return fmt.Sprintf("https://github.com/hoxiai/hoxi/releases/download/v%s/Hoxi-%s-darwin-%s.dmg", ver, ver, darwinReleaseArch())
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
		client = &http.Client{Timeout: 10 * time.Minute}
	}
	expectedSHA, err := resolveArchiveSHA256(ctx, client, dmgURL, fallbackSHA256)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dmgURL, nil)
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
	written, copyErr := io.Copy(withDownloadProgress(io.MultiWriter(outFile, hasher), resp.ContentLength, report), io.LimitReader(resp.Body, maxUpdateArchiveBytes+1))
	closeErr := outFile.Close()
	if copyErr != nil {
		_ = os.Remove(dstPath)
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
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, sidecarURL, nil)
		if err == nil {
			resp, err := client.Do(req)
			if err == nil {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					if sum := parseSHA256Text(string(body)); sum != "" {
						return sum, nil
					}
				}
			}
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
func downloadAndStageWindowsZip(ctx context.Context, client *http.Client, archiveURL, fallbackSHA256, stageDir string) error {
	if !isAllowedUpdateURL(archiveURL) {
		return errors.New("update download URL must use HTTPS")
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}
	expectedSHA, err := resolveArchiveSHA256(ctx, client, archiveURL, fallbackSHA256)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, archiveURL, nil)
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
	written, copyErr := io.Copy(io.MultiWriter(outFile, hasher), io.LimitReader(resp.Body, maxUpdateArchiveBytes+1))
	closeErr := outFile.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written > maxUpdateArchiveBytes {
		return errors.New("update archive exceeds maximum allowed size")
	}

	actualSHA := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(actualSHA, expectedSHA) {
		return fmt.Errorf("update archive SHA-256 mismatch: got %s, want %s", actualSHA, expectedSHA)
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
