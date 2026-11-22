package hoxipaths

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// CLILinkStatus describes the installation state of the `hoxi` command in the
// user's terminal PATH.
type CLILinkStatus struct {
	Installed    bool   `json:"installed"`
	Current      bool   `json:"current"`
	External     bool   `json:"external,omitempty"`
	InPath       bool   `json:"inPath"`
	LinkPath     string `json:"linkPath,omitempty"`
	TargetPath   string `json:"targetPath,omitempty"`
	Executable   string `json:"executable,omitempty"`
	SuggestedDir string `json:"suggestedDir,omitempty"`
	PathHint     string `json:"pathHint,omitempty"`
	Error        string `json:"error,omitempty"`
}

const windowsShimMarker = "REM hoxi-managed-cli-shim"

// IsTransientGoBuildPath reports whether p points inside a temporary `go run`
// or `go test` build cache directory that vanishes when the command exits.
func IsTransientGoBuildPath(p string) bool {
	slash := strings.ToLower(filepath.ToSlash(strings.TrimSpace(p)))
	return strings.Contains(slash, "/go-build")
}

// ResolveStableExecutable returns a clean, stable path for exePath. When PATH
// already exposes a symlink (for example /opt/homebrew/bin/hoxi or a Nix
// profile link) that resolves to the exact same underlying file as exePath,
// the stable link path is preferred over a versioned Cellar/store path.
func ResolveStableExecutable(exePath, pathEnv string) (string, os.FileInfo, error) {
	trimmed := strings.TrimSpace(exePath)
	if trimmed == "" {
		return "", nil, errors.New("empty executable path")
	}
	if abs, err := filepath.Abs(trimmed); err == nil && abs != "" {
		trimmed = abs
	}
	resolved := trimmed
	if realPath, err := filepath.EvalSymlinks(trimmed); err == nil && realPath != "" {
		resolved = realPath
	}
	info, err := os.Stat(resolved)
	if err != nil || info.IsDir() {
		return "", nil, fmt.Errorf("executable path %s is not a valid file", trimmed)
	}
	if IsTransientGoBuildPath(trimmed) || IsTransientGoBuildPath(resolved) {
		return resolved, info, fmt.Errorf("cannot register temporary go-build executable %s", resolved)
	}

	slashResolved := strings.ToLower(filepath.ToSlash(resolved))
	isVolatileStore := strings.Contains(slashResolved, "/cellar/") || strings.HasPrefix(slashResolved, "/nix/store/")
	if isVolatileStore && pathEnv != "" {
		binName := cliBinaryName()
		for _, dir := range filepath.SplitList(pathEnv) {
			dir = strings.TrimSpace(dir)
			if dir == "" {
				continue
			}
			cand := filepath.Join(dir, binName)
			if candInfo, statErr := os.Stat(cand); statErr == nil && !candInfo.IsDir() && os.SameFile(info, candInfo) {
				if !strings.Contains(strings.ToLower(filepath.ToSlash(cand)), "/cellar/") &&
					!strings.HasPrefix(strings.ToLower(filepath.ToSlash(cand)), "/nix/store/") {
					return filepath.Clean(cand), info, nil
				}
			}
		}
	}
	return filepath.Clean(resolved), info, nil
}

func cliBinaryName() string {
	if runtime.GOOS == "windows" {
		return "hoxi.cmd"
	}
	return "hoxi"
}

func defaultCLICandidateDirs() []string {
	if runtime.GOOS == "windows" {
		if b := BinDir(); b != "" {
			return []string{b}
		}
		return nil
	}
	dirs := []string{"/usr/local/bin"}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		dirs = append(dirs, filepath.Join(home, ".local", "bin"))
	}
	return dirs
}

func inspectSearchDirs(pathEnv string) []string {
	seen := make(map[string]bool)
	var out []string
	add := func(d string) {
		d = strings.TrimSpace(d)
		if d == "" {
			return
		}
		clean := filepath.Clean(d)
		key := clean
		if runtime.GOOS == "windows" {
			key = strings.ToLower(clean)
		}
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, clean)
	}
	for _, d := range filepath.SplitList(pathEnv) {
		add(d)
	}
	if runtime.GOOS != "windows" {
		add("/usr/local/bin")
		add("/opt/homebrew/bin")
		if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
			add(filepath.Join(home, ".local", "bin"))
		}
	}
	add(BinDir())
	return out
}

func isDirInPath(targetDir, pathEnv string) bool {
	targetClean := filepath.Clean(strings.TrimSpace(targetDir))
	if targetClean == "" {
		return false
	}
	targetReal := targetClean
	if r, err := filepath.EvalSymlinks(targetClean); err == nil && r != "" {
		targetReal = filepath.Clean(r)
	}
	for _, entry := range filepath.SplitList(pathEnv) {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		clean := filepath.Clean(entry)
		if samePathString(clean, targetClean) || samePathString(clean, targetReal) {
			return true
		}
		if r, err := filepath.EvalSymlinks(clean); err == nil && r != "" {
			if samePathString(filepath.Clean(r), targetReal) {
				return true
			}
		}
	}
	return false
}

func samePathString(a, b string) bool {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func formatPathHint(dir string) string {
	if dir == "" {
		return ""
	}
	if runtime.GOOS == "windows" {
		return fmt.Sprintf(`$env:Path = "%s;" + $env:Path`, dir)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		cleanHome := filepath.Clean(home)
		cleanDir := filepath.Clean(dir)
		if strings.HasPrefix(cleanDir, cleanHome+string(os.PathSeparator)) {
			rel := filepath.ToSlash(strings.TrimPrefix(cleanDir, cleanHome))
			return fmt.Sprintf(`export PATH="$HOME%s:$PATH"`, rel)
		}
	}
	return fmt.Sprintf(`export PATH="%s:$PATH"`, dir)
}

func isHoxiTargetName(target string) bool {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(target)))
	base := filepath.Base(lower)
	return base == "hoxi" || base == "hoxi.exe" || strings.Contains(lower, "/hoxi.app/") || strings.Contains(lower, "/hoxi dev.app/")
}

func readManagedWindowsShimTarget(shimPath string) (string, bool) {
	raw, err := os.ReadFile(shimPath)
	if err != nil {
		return "", false
	}
	text := string(raw)
	if !strings.Contains(text, windowsShimMarker) {
		return "", false
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, `@"`) {
			rest := strings.TrimPrefix(line, `@"`)
			if idx := strings.IndexByte(rest, '"'); idx > 0 {
				return rest[:idx], true
			}
		}
	}
	return "", true
}

// InspectCLILink checks whether `hoxi` is installed in standard bin directories
// or PATH and whether it points to exePath.
func InspectCLILink(exePath, pathEnv string) CLILinkStatus {
	candidates := defaultCLICandidateDirs()
	suggestedDir := ""
	if len(candidates) > 0 {
		suggestedDir = candidates[len(candidates)-1]
	}

	stableExe, exeInfo, err := ResolveStableExecutable(exePath, pathEnv)
	if err != nil {
		return CLILinkStatus{
			Executable:   strings.TrimSpace(exePath),
			SuggestedDir: suggestedDir,
			Error:        err.Error(),
		}
	}

	status := CLILinkStatus{
		Executable:   stableExe,
		SuggestedDir: suggestedDir,
	}

	var outdatedCandidate *CLILinkStatus
	for _, dir := range inspectSearchDirs(pathEnv) {
		names := []string{"hoxi"}
		if runtime.GOOS == "windows" {
			names = []string{"hoxi.cmd", "Hoxi.exe", "hoxi.exe"}
		}
		for _, name := range names {
			cand := filepath.Join(dir, name)
			lstat, lerr := os.Lstat(cand)
			if lerr != nil {
				continue
			}
			inPath := isDirInPath(dir, pathEnv)
			hint := ""
			if !inPath {
				hint = formatPathHint(dir)
			}

			if lstat.Mode()&os.ModeSymlink != 0 {
				rawTarget, _ := os.Readlink(cand)
				resolvedTarget := rawTarget
				if rawTarget != "" && !filepath.IsAbs(rawTarget) {
					resolvedTarget = filepath.Join(dir, rawTarget)
				}
				if targetStat, serr := os.Stat(cand); serr == nil && os.SameFile(exeInfo, targetStat) {
					return CLILinkStatus{
						Installed:    true,
						Current:      true,
						InPath:       inPath,
						LinkPath:     cand,
						TargetPath:   resolvedTarget,
						Executable:   stableExe,
						SuggestedDir: suggestedDir,
						PathHint:     hint,
					}
				}
				if outdatedCandidate == nil && isHoxiTargetName(rawTarget) {
					outdatedCandidate = &CLILinkStatus{
						Installed:    true,
						Current:      false,
						InPath:       inPath,
						LinkPath:     cand,
						TargetPath:   resolvedTarget,
						Executable:   stableExe,
						SuggestedDir: suggestedDir,
						PathHint:     hint,
					}
				}
				continue
			}

			if runtime.GOOS == "windows" && strings.EqualFold(name, "hoxi.cmd") {
				if shimTarget, ok := readManagedWindowsShimTarget(cand); ok {
					if targetStat, serr := os.Stat(shimTarget); serr == nil && os.SameFile(exeInfo, targetStat) {
						return CLILinkStatus{
							Installed:    true,
							Current:      true,
							InPath:       inPath,
							LinkPath:     cand,
							TargetPath:   shimTarget,
							Executable:   stableExe,
							SuggestedDir: suggestedDir,
							PathHint:     hint,
						}
					}
					if outdatedCandidate == nil {
						outdatedCandidate = &CLILinkStatus{
							Installed:    true,
							Current:      false,
							InPath:       inPath,
							LinkPath:     cand,
							TargetPath:   shimTarget,
							Executable:   stableExe,
							SuggestedDir: suggestedDir,
							PathHint:     hint,
						}
					}
					continue
				}
			}

			if !lstat.IsDir() && os.SameFile(exeInfo, lstat) {
				return CLILinkStatus{
					Installed:    true,
					Current:      true,
					External:     true,
					InPath:       inPath,
					LinkPath:     cand,
					TargetPath:   stableExe,
					Executable:   stableExe,
					SuggestedDir: suggestedDir,
					PathHint:     hint,
				}
			}
			if outdatedCandidate == nil && isLegacyHoxiBinaryFile(cand, lstat) {
				outdatedCandidate = &CLILinkStatus{
					Installed:    true,
					Current:      false,
					InPath:       inPath,
					LinkPath:     cand,
					TargetPath:   cand,
					Executable:   stableExe,
					SuggestedDir: suggestedDir,
					PathHint:     hint,
				}
			}
		}
	}

	if outdatedCandidate != nil {
		return *outdatedCandidate
	}
	return status
}

// InstallCLISymlink creates or updates a `hoxi` symlink (or managed `.cmd` shim
// on Windows when symlinks require elevation) pointing to exePath.
func InstallCLISymlink(exePath, preferredBinDir string) (string, error) {
	stableExe, exeInfo, err := ResolveStableExecutable(exePath, os.Getenv("PATH"))
	if err != nil {
		return "", err
	}

	var candidates []string
	if strings.TrimSpace(preferredBinDir) != "" {
		candidates = append(candidates, strings.TrimSpace(preferredBinDir))
	} else {
		candidates = defaultCLICandidateDirs()
	}
	if len(candidates) == 0 {
		return "", errors.New("no target bin directory available")
	}

	var lastErr error
	for _, dir := range candidates {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			lastErr = err
			continue
		}
		if runtime.GOOS == "windows" {
			dest, winErr := installWindowsCLIShim(stableExe, exeInfo, dir)
			if winErr != nil {
				lastErr = winErr
				continue
			}
			return dest, nil
		}

		dest := filepath.Join(dir, "hoxi")
		if resolvedDir, evalErr := filepath.EvalSymlinks(dir); evalErr == nil && resolvedDir != "" {
			if filepath.Clean(filepath.Join(resolvedDir, "hoxi")) == filepath.Clean(stableExe) {
				return dest, nil
			}
		}
		if lstat, statErr := os.Lstat(dest); statErr == nil {
			if lstat.Mode()&os.ModeSymlink == 0 {
				if os.SameFile(exeInfo, lstat) {
					return dest, nil
				}
				if !isLegacyHoxiBinaryFile(dest, lstat) {
					lastErr = fmt.Errorf("refusing to overwrite existing non-symlink file %s", dest)
					continue
				}
			}
			if cur, readErr := os.Readlink(dest); readErr == nil && cur == stableExe {
				return dest, nil
			}
		}
		tmp := filepath.Join(dir, fmt.Sprintf(".hoxi-link.%d.tmp", os.Getpid()))
		_ = os.Remove(tmp)
		if err := os.Symlink(stableExe, tmp); err != nil {
			lastErr = err
			continue
		}
		if err := os.Rename(tmp, dest); err != nil {
			_ = os.Remove(tmp)
			lastErr = err
			continue
		}
		return dest, nil
	}
	return "", lastErr
}

func installWindowsCLIShim(stableExe string, exeInfo os.FileInfo, dir string) (string, error) {
	for _, existingExe := range []string{"Hoxi.exe", "hoxi.exe"} {
		cand := filepath.Join(dir, existingExe)
		if st, err := os.Stat(cand); err == nil && !st.IsDir() && os.SameFile(exeInfo, st) {
			return cand, nil
		}
	}
	dest := filepath.Join(dir, "hoxi.cmd")
	if lstat, err := os.Lstat(dest); err == nil && lstat.Mode()&os.ModeSymlink == 0 {
		if _, managed := readManagedWindowsShimTarget(dest); !managed {
			return "", fmt.Errorf("refusing to overwrite existing non-managed file %s", dest)
		}
	}
	content := fmt.Sprintf("@echo off\r\n%s\r\n@\"%s\" %%*\r\n", windowsShimMarker, stableExe)
	tmp := filepath.Join(dir, fmt.Sprintf(".hoxi-shim.%d.tmp", os.Getpid()))
	if err := os.WriteFile(tmp, []byte(content), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return dest, nil
}

// UninstallCLISymlink removes Hoxi-managed `hoxi` symlinks (or Windows `.cmd`
// shims) from preferredBinDir or the standard user/local bin directories, never
// touching regular binaries or unrelated symlinks.
func UninstallCLISymlink(exePath, preferredBinDir string) ([]string, error) {
	var exeInfo os.FileInfo
	if trimmed := strings.TrimSpace(exePath); trimmed != "" {
		if resolved, err := filepath.EvalSymlinks(trimmed); err == nil && resolved != "" {
			exeInfo, _ = os.Stat(resolved)
		} else {
			exeInfo, _ = os.Stat(trimmed)
		}
	}

	var dirs []string
	if strings.TrimSpace(preferredBinDir) != "" {
		dirs = []string{strings.TrimSpace(preferredBinDir)}
	} else {
		dirs = inspectSearchDirs(os.Getenv("PATH"))
	}

	var removed []string
	var firstErr error
	for _, dir := range dirs {
		if runtime.GOOS == "windows" {
			shim := filepath.Join(dir, "hoxi.cmd")
			if _, ok := readManagedWindowsShimTarget(shim); ok {
				if err := os.Remove(shim); err != nil && !os.IsNotExist(err) {
					if firstErr == nil {
						firstErr = err
					}
					continue
				}
				removed = append(removed, shim)
			}
			continue
		}

		cand := filepath.Join(dir, "hoxi")
		lstat, err := os.Lstat(cand)
		if err != nil || lstat.Mode()&os.ModeSymlink == 0 {
			continue
		}
		rawTarget, err := os.Readlink(cand)
		if err != nil {
			continue
		}
		matchesExe := false
		if exeInfo != nil {
			if targetStat, serr := os.Stat(cand); serr == nil && os.SameFile(exeInfo, targetStat) {
				matchesExe = true
			}
		}
		if !matchesExe && !isHoxiTargetName(rawTarget) {
			continue
		}
		if err := os.Remove(cand); err != nil && !os.IsNotExist(err) {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		removed = append(removed, cand)
	}
	return removed, firstErr
}

// RefreshOutdatedCLILink atomically upgrades an existing, outdated Hoxi CLI
// symlink or legacy copied Hoxi binary on pathEnv without creating a new link
// if the user never installed the CLI.
func RefreshOutdatedCLILink(exePath, pathEnv string) {
	st := InspectCLILink(exePath, pathEnv)
	if !st.Installed || st.Current || st.Error != "" || st.LinkPath == "" {
		return
	}
	_, _ = InstallCLISymlink(exePath, filepath.Dir(st.LinkPath))
}

func isLegacyHoxiBinaryFile(path string, info os.FileInfo) bool {
	if info == nil || !info.Mode().IsRegular() {
		return false
	}
	const maxScanSize = 128 << 20
	if info.Size() <= 0 || info.Size() > maxScanSize {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	var (
		modMarker = []byte("github.com/hoxiai/hoxi")
		cliMarker = []byte("Hoxi CLI (")
		foundMod  bool
		foundCLI  bool
		buf       = make([]byte, 64<<10)
		tail      []byte
	)
	for {
		n, rErr := f.Read(buf)
		if n > 0 {
			window := append(tail, buf[:n]...)
			if !foundMod && bytes.Contains(window, modMarker) {
				foundMod = true
			}
			if !foundCLI && bytes.Contains(window, cliMarker) {
				foundCLI = true
			}
			if foundMod && foundCLI {
				return true
			}
			if len(window) > 64 {
				tail = append(tail[:0], window[len(window)-64:]...)
			} else {
				tail = append(tail[:0], window...)
			}
		}
		if rErr == io.EOF || rErr != nil {
			break
		}
	}
	return false
}
