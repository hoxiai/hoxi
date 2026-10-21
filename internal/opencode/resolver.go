package opencode

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
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

	"github.com/hoxiai/hoxi/internal/hoxipaths"
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
	if binDir := hoxipaths.BinDir(); binDir != "" {
		extra = append(extra, binDir)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		extra = append(extra,
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

	if binDir := hoxipaths.BinDir(); binDir != "" {
		candidates = append(candidates, filepath.Join(binDir, binName))
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates,
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

	logger.Info("opencode binary not found locally; downloading official release into ~/.hoxi/bin",
		"version", releaseVersion())
	installed, err := downloadOfficialOpenCode(ctx, logger)
	if err != nil {
		return "", fmt.Errorf("opencode not found and auto-download failed: %w", err)
	}
	return installed, nil
}

// releaseVersion reports the pinned official release, or the HOXI_OPENCODE_VERSION
// override used when maintainers verify a newer archive.
func releaseVersion() string {
	if custom := strings.TrimPrefix(strings.TrimSpace(os.Getenv("HOXI_OPENCODE_VERSION")), "v"); custom != "" {
		return custom
	}
	return pinnedReleaseVersion
}

// expectedArchiveSHA256 resolves the trusted checksum for the current platform.
// Admin-provided archives must supply HOXI_OPENCODE_SHA256; we never fall back
// to unverified downloads. A HOXI_OPENCODE_VERSION override without its own
// checksum fails up front: the pinned digests only match the pinned release.
func expectedArchiveSHA256(goos, goarch string) (string, error) {
	if override := strings.ToLower(strings.TrimSpace(os.Getenv("HOXI_OPENCODE_SHA256"))); override != "" {
		if !isSHA256Hex(override) {
			return "", errors.New("HOXI_OPENCODE_SHA256 must be a 64-character hex SHA-256 digest")
		}
		return override, nil
	}
	if version := releaseVersion(); version != pinnedReleaseVersion {
		return "", fmt.Errorf("HOXI_OPENCODE_VERSION=%s overrides the pinned OpenCode %s; set HOXI_OPENCODE_SHA256 to the SHA-256 of the %s/%s archive for that version", version, pinnedReleaseVersion, goos, goarch)
	}
	if pinned, ok := pinnedReleaseSHA256[goos+"/"+goarch]; ok && pinned != "" {
		return pinned, nil
	}
	return "", fmt.Errorf("no pinned SHA-256 for %s/%s; set HOXI_OPENCODE_SHA256 to verify a custom archive", goos, goarch)
}

func isSHA256Hex(v string) bool {
	if len(v) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(v)
	return err == nil && len(decoded) == sha256.Size
}

func verifyArchiveSHA256(raw []byte, expected string) error {
	sum := sha256.Sum256(raw)
	actual := hex.EncodeToString(sum[:])
	if subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) != 1 {
		return fmt.Errorf("opencode archive checksum mismatch: expected %s, got %s", expected, actual)
	}
	return nil
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
	asset, err := lockedReleaseAsset(goos, goarch)
	if err != nil {
		return nil, false, err
	}
	name, isZip, err := releaseAssetFile(goos, goarch)
	if err != nil {
		return nil, false, err
	}

	version := releaseVersion()
	// 支持通过环境变量指定镜像加速（仅版本目录布局；无版本的旧布局无法区分版本，已移除）
	var candidates []string
	if custom := strings.TrimRight(os.Getenv("HOXI_OPENCODE_MIRROR"), "/"); custom != "" {
		candidates = append(candidates, custom+"/v"+version+"/"+name)
	}
	// GitHub 官方源：固定版本，配合 opencode.lock 的 SHA-256 校验
	officialURL := asset.URL
	if version != pinnedReleaseVersion {
		officialURL, err = releaseURLForVersion(asset.URL, version)
		if err != nil {
			return nil, false, err
		}
	}
	candidates = append(candidates, officialURL)

	return candidates, isZip, nil
}

func downloadOfficialOpenCode(ctx context.Context, logger *slog.Logger) (string, error) {
	binDir := hoxipaths.BinDir()
	if binDir == "" {
		return "", errors.New("cannot determine user home directory")
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", err
	}

	binName := "opencode"
	if runtime.GOOS == "windows" {
		binName = "opencode.exe"
	}

	dlURLs, isZip, err := OfficialReleaseURLs(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", err
	}
	expectedSHA, err := expectedArchiveSHA256(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", err
	}

	client := &http.Client{Timeout: 90 * time.Second}
	targetPath, err := installFromMirrors(ctx, client, dlURLs, isZip, expectedSHA, binDir, binName)
	if err != nil {
		return "", err
	}
	logger.Info("installed official opencode binary", "path", targetPath, "version", releaseVersion())
	return targetPath, nil
}

// installFromMirrors tries each mirror in order. An archive is accepted only
// if its SHA-256 matches expectedSHA; a mismatching or broken mirror falls
// through to the next candidate. The binary is written to a unique temp file
// in binDir and renamed into place, so concurrent installs never interleave.
func installFromMirrors(ctx context.Context, client *http.Client, dlURLs []string, isZip bool, expectedSHA, binDir, binName string) (string, error) {
	var lastErr error
	for _, dlURL := range dlURLs {
		archive, err := fetchArchive(ctx, client, dlURL)
		if err != nil {
			lastErr = err
			continue
		}
		if err := verifyArchiveSHA256(archive, expectedSHA); err != nil {
			lastErr = fmt.Errorf("%s: %w", dlURL, err)
			continue
		}
		var binBytes []byte
		if isZip {
			binBytes, err = extractBinaryFromZip(archive, binName)
		} else {
			binBytes, err = extractBinaryFromTarGz(archive, binName)
		}
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", dlURL, err)
			continue
		}
		return writeBinaryAtomically(binDir, binName, binBytes)
	}
	if lastErr == nil {
		lastErr = errors.New("no download candidates")
	}
	return "", fmt.Errorf("failed to download opencode from all mirrors: %w", lastErr)
}

func fetchArchive(ctx context.Context, client *http.Client, dlURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dlURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download %s returned HTTP %d", dlURL, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 250*1024*1024))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("download %s returned an empty body", dlURL)
	}
	return data, nil
}

func writeBinaryAtomically(binDir, binName string, binBytes []byte) (string, error) {
	targetPath := filepath.Join(binDir, binName)
	tmp, err := os.CreateTemp(binDir, "."+binName+".*.tmp")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(binBytes); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return "", err
	}
	// Keep the binary we are replacing until the new one proves it can serve,
	// so a broken release can be rolled back instead of leaving the machine
	// with an engine that no longer starts.
	if err := keepBinaryBackup(targetPath); err != nil {
		return "", err
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		return "", err
	}
	committed = true
	return targetPath, nil
}

// binaryBackupSuffix names the rollback copy of the managed binary.
const binaryBackupSuffix = ".previous"

// BinaryBackupPath reports where the previous managed binary is kept.
func BinaryBackupPath(binPath string) string {
	binPath = strings.TrimSpace(binPath)
	if binPath == "" {
		return ""
	}
	return binPath + binaryBackupSuffix
}

func keepBinaryBackup(targetPath string) error {
	if !isExecutableFile(targetPath) {
		return nil
	}
	backupPath := BinaryBackupPath(targetPath)
	_ = os.Remove(backupPath)
	if err := os.Rename(targetPath, backupPath); err != nil {
		return fmt.Errorf("keep previous opencode binary: %w", err)
	}
	return nil
}

// RestoreBinaryBackup puts the previous managed binary back in place after the
// new one failed to start. It reports whether a backup was restored.
func RestoreBinaryBackup(binPath string) (bool, error) {
	backupPath := BinaryBackupPath(binPath)
	if backupPath == "" || !isExecutableFile(backupPath) {
		return false, nil
	}
	if err := os.Remove(binPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := os.Rename(backupPath, binPath); err != nil {
		return false, err
	}
	return true, nil
}

// DiscardBinaryBackup drops the rollback copy once the new binary is healthy.
func DiscardBinaryBackup(binPath string) {
	if backupPath := BinaryBackupPath(binPath); backupPath != "" {
		_ = os.Remove(backupPath)
	}
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
