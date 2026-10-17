package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// enrichSystemPath ensures macOS GUI launches (Finder / Dock / Spotlight) include
// standard developer tool paths (/opt/homebrew/bin, /usr/local/bin, ~/.opencode/bin, ~/.hoxi/bin, etc.).
func enrichSystemPath() {
	currentPath := os.Getenv("PATH")
	existing := make(map[string]bool)
	var parts []string
	for _, p := range filepath.SplitList(currentPath) {
		p = strings.TrimSpace(p)
		if p != "" && !existing[p] {
			existing[p] = true
			parts = append(parts, p)
		}
	}

	var extra []string
	if exe, err := os.Executable(); err == nil && exe != "" {
		exeDir := filepath.Dir(exe)
		extra = append(extra, exeDir, filepath.Clean(filepath.Join(exeDir, "..", "Resources")))
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		extra = append(extra,
			filepath.Join(home, ".hoxi", "bin"),
			filepath.Join(home, ".opencode", "bin"),
			filepath.Join(home, ".bun", "bin"),
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, ".cargo", "bin"),
		)
	}
	extra = append(extra,
		"/opt/homebrew/bin",
		"/opt/homebrew/sbin",
		"/usr/local/bin",
		"/usr/bin",
		"/bin",
		"/usr/sbin",
		"/sbin",
	)

	// Prepend missing extra directories so bundled sidecars and Homebrew tools take precedence
	var combined []string
	for _, p := range extra {
		if p != "" && !existing[p] {
			existing[p] = true
			combined = append(combined, p)
		}
	}
	combined = append(combined, parts...)
	_ = os.Setenv("PATH", strings.Join(combined, string(os.PathListSeparator)))
}

func isExecutableFile(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode()&0o111 != 0
}

// findOpenCodeBinary searches for the opencode binary across:
// 1. Explicit flag / HOXI_OPENCODE path
// 2. App bundle sidecar (Hoxi.app/Contents/MacOS/opencode or Contents/Resources/opencode)
// 3. Hoxi managed bin (~/.hoxi/bin/opencode)
// 4. Common install paths (~/.opencode/bin, /opt/homebrew/bin, /usr/local/bin, ~/.bun/bin)
// 5. System PATH
func findOpenCodeBinary(configured string) string {
	binName := "opencode"
	if runtime.GOOS == "windows" {
		binName = "opencode.exe"
	}

	trimmed := strings.TrimSpace(configured)
	if trimmed != "" && trimmed != "opencode" && trimmed != "opencode.exe" {
		if isExecutableFile(trimmed) {
			return trimmed
		}
		if p, err := exec.LookPath(trimmed); err == nil && isExecutableFile(p) {
			return p
		}
	}

	var candidates []string
	if exe, err := os.Executable(); err == nil && exe != "" {
		if realExe, err := filepath.EvalSymlinks(exe); err == nil {
			exe = realExe
		}
		exeDir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(exeDir, binName),
			filepath.Clean(filepath.Join(exeDir, "..", "Resources", binName)),
		)
	}

	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".hoxi", "bin", binName),
			filepath.Join(home, ".opencode", "bin", binName),
			filepath.Join(home, ".bun", "bin", binName),
			filepath.Join(home, ".local", "bin", binName),
		)
	}

	candidates = append(candidates,
		filepath.Join("/opt/homebrew/bin", binName),
		filepath.Join("/usr/local/bin", binName),
	)

	for _, c := range candidates {
		if isExecutableFile(c) {
			return c
		}
	}

	if p, err := exec.LookPath(binName); err == nil && isExecutableFile(p) {
		return p
	}
	return ""
}

func resolveOrInstallOpenCode(ctx context.Context, configured string, logger *slog.Logger) (string, error) {
	if found := findOpenCodeBinary(configured); found != "" {
		return found, nil
	}

	logger.Info("opencode binary not found locally; downloading official release into ~/.hoxi/bin...")
	installed, err := downloadOfficialOpenCode(ctx, logger)
	if err != nil {
		return "", fmt.Errorf("opencode not found and auto-download failed: %w", err)
	}
	return installed, nil
}

func officialOpenCodeReleaseURL(goos, goarch string) (url string, isZip bool, err error) {
	var osName string
	switch goos {
	case "darwin":
		osName = "darwin"
		isZip = true
	case "linux":
		osName = "linux"
		isZip = false
	case "windows":
		osName = "windows"
		isZip = true
	default:
		return "", false, fmt.Errorf("unsupported OS: %s", goos)
	}

	var archName string
	switch goarch {
	case "arm64":
		archName = "arm64"
	case "amd64":
		archName = "x64"
	default:
		return "", false, fmt.Errorf("unsupported architecture: %s", goarch)
	}

	ext := ".tar.gz"
	if isZip {
		ext = ".zip"
	}
	asset := fmt.Sprintf("opencode-%s-%s%s", osName, archName, ext)
	return "https://github.com/anomalyco/opencode/releases/latest/download/" + asset, isZip, nil
}

func downloadOfficialOpenCode(ctx context.Context, logger *slog.Logger) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", errors.New("cannot determine user home directory")
	}
	binDir := filepath.Join(home, ".hoxi", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", err
	}

	binName := "opencode"
	if runtime.GOOS == "windows" {
		binName = "opencode.exe"
	}
	targetPath := filepath.Join(binDir, binName)

	dlURL, isZip, err := officialOpenCodeReleaseURL(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", err
	}

	dlCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(dlCtx, http.MethodGet, dlURL, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("download %s returned HTTP %d", dlURL, resp.StatusCode)
	}

	archiveBytes, err := io.ReadAll(io.LimitReader(resp.Body, 250*1024*1024))
	if err != nil {
		return "", err
	}

	var binBytes []byte
	if isZip {
		binBytes, err = extractBinaryFromZip(archiveBytes, binName)
	} else {
		binBytes, err = extractBinaryFromTarGz(archiveBytes, binName)
	}
	if err != nil {
		return "", err
	}

	tmpPath := targetPath + ".tmp"
	if err := os.WriteFile(tmpPath, binBytes, 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		return "", err
	}
	logger.Info("installed official opencode binary", "path", targetPath)
	return targetPath, nil
}

func extractBinaryFromZip(raw []byte, binName string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, err
	}
	for _, f := range zr.File {
		if filepath.Base(f.Name) == binName && !f.FileInfo().IsDir() {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("%s not found in zip archive", binName)
}

func extractBinaryFromTarGz(raw []byte, binName string) ([]byte, error) {
	gzr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	defer gzr.Close()
	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if filepath.Base(hdr.Name) == binName && hdr.Typeflag == tar.TypeReg {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("%s not found in tar.gz archive", binName)
}
