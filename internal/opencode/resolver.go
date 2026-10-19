package opencode

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

// EnrichSystemPath ensures macOS GUI launches (Finder / Dock / Spotlight) include
// standard developer tool paths (/opt/homebrew/bin, /usr/local/bin, ~/.opencode/bin, ~/.hoxi/bin, etc.).
func EnrichSystemPath() {
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
			filepath.Join(home, "scoop", "shims"),
		)
	}
	if localApp := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localApp != "" {
		extra = append(extra,
			filepath.Join(localApp, "opencode", "bin"),
			filepath.Join(localApp, "Programs", "opencode"),
			filepath.Join(localApp, "Microsoft", "WinGet", "Links"),
		)
	}
	if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
		extra = append(extra, filepath.Join(appData, "npm"))
	}
	if runtime.GOOS != "windows" {
		extra = append(extra,
			"/opt/homebrew/bin",
			"/opt/homebrew/sbin",
			"/usr/local/bin",
			"/usr/bin",
			"/bin",
			"/usr/sbin",
			"/sbin",
		)
	}

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

// FindBinary searches for the opencode binary across:
// 1. Explicit flag / HOXI_OPENCODE path
// 2. App bundle sidecar (Hoxi.app/Contents/MacOS/opencode or Hoxi.exe directory)
// 3. Hoxi managed bin (~/.hoxi/bin/opencode)
// 4. Common install paths (~/.opencode/bin, %LOCALAPPDATA%\opencode\bin, /opt/homebrew/bin, etc.)
// 5. System PATH
func FindBinary(configured string) string {
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
			filepath.Join(home, "scoop", "shims", binName),
		)
	}
	if localApp := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localApp != "" {
		candidates = append(candidates,
			filepath.Join(localApp, "opencode", "bin", binName),
			filepath.Join(localApp, "Programs", "opencode", binName),
			filepath.Join(localApp, "Microsoft", "WinGet", "Links", binName),
		)
	}
	if runtime.GOOS != "windows" {
		candidates = append(candidates,
			filepath.Join("/opt/homebrew/bin", binName),
			filepath.Join("/usr/local/bin", binName),
		)
	}

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

func ResolveOrInstall(ctx context.Context, configured string, logger *slog.Logger) (string, error) {
	if found := FindBinary(configured); found != "" {
		return found, nil
	}

	logger.Info("opencode binary not found locally; downloading official release into ~/.hoxi/bin...")
	installed, err := downloadOfficialOpenCode(ctx, logger)
	if err != nil {
		return "", fmt.Errorf("opencode not found and auto-download failed: %w", err)
	}
	return installed, nil
}

func OfficialReleaseURL(goos, goarch string) (url string, isZip bool, err error) {
	urls, zip, err := OfficialReleaseURLs(goos, goarch)
	if err != nil || len(urls) == 0 {
		return "", zip, err
	}
	// 默认返回官方 GitHub 源（列表最后一个）或首选源
	return urls[len(urls)-1], zip, nil
}

func OfficialReleaseURLs(goos, goarch string) (urls []string, isZip bool, err error) {
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
		return nil, false, fmt.Errorf("unsupported OS: %s", goos)
	}

	var archName string
	switch goarch {
	case "arm64":
		archName = "arm64"
	case "amd64":
		archName = "x64"
	default:
		return nil, false, fmt.Errorf("unsupported architecture: %s", goarch)
	}

	ext := ".tar.gz"
	if isZip {
		ext = ".zip"
	}
	asset := fmt.Sprintf("opencode-%s-%s%s", osName, archName, ext)

	// 支持通过环境变量指定镜像加速
	var candidates []string
	if custom := strings.TrimRight(os.Getenv("HOXI_OPENCODE_MIRROR"), "/"); custom != "" {
		candidates = append(candidates, custom+"/"+asset)
	}
	// GitCode / 国内镜像候选
	candidates = append(candidates, "https://gitcode.com/hoxiai/hoxi/releases/download/latest/"+asset)
	// GitHub 官方源
	candidates = append(candidates, "https://github.com/anomalyco/opencode/releases/latest/download/"+asset)

	return candidates, isZip, nil
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

	dlURLs, isZip, err := OfficialReleaseURLs(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", err
	}

	client := &http.Client{Timeout: 90 * time.Second}
	var archiveBytes []byte

	// 顺序尝试镜像源，直到成功
	var lastErr error
	for _, dlURL := range dlURLs {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, dlURL, nil)
		if reqErr != nil {
			lastErr = reqErr
			continue
		}
		resp, doErr := client.Do(req)
		if doErr != nil {
			lastErr = doErr
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			lastErr = fmt.Errorf("download %s returned HTTP %d", dlURL, resp.StatusCode)
			continue
		}

		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 250*1024*1024))
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}

		archiveBytes = data
		lastErr = nil
		break
	}

	if len(archiveBytes) == 0 {
		return "", fmt.Errorf("failed to download opencode from all mirrors: %w", lastErr)
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

const maxExtractedBinaryBytes = 250 * 1024 * 1024

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
			return io.ReadAll(io.LimitReader(rc, maxExtractedBinaryBytes))
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
			return io.ReadAll(io.LimitReader(tr, maxExtractedBinaryBytes))
		}
	}
	return nil, fmt.Errorf("%s not found in tar.gz archive", binName)
}
