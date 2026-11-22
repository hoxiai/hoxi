package hoxipaths

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

const (
	// WorkDirName is the vendor-neutral per-user work runtime root under ~/.hoxi.
	WorkDirName = "work"
	// WorkPackagesDirName holds immutable installed app package bundles.
	WorkPackagesDirName = "packages"
	// WorkToolchainsDirName holds shared managed toolchains (e.g. Python, Node, uv).
	WorkToolchainsDirName = "toolchains"
	// WorkRuntimesDirName holds per-app isolated runtime environments (venvs, node_modules).
	WorkRuntimesDirName = "runtimes"
	// WorkWorkspaceDirName holds default scratch workspace state for work sessions.
	WorkWorkspaceDirName = "workspace"
	// WorkStoresDirName holds sensitive work store profiles and state.
	WorkStoresDirName = "stores"
	// WorkEnvProfileFileName is the managed work environment profile under ~/.hoxi/work.
	WorkEnvProfileFileName = "env-profile.json"
	// WorkAppRegistryFileName is the installed work app registry under ~/.hoxi.
	WorkAppRegistryFileName = "work-apps.json"
)

var (
	workIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

	workProtectedStateEntries = []string{
		"work-apps.json",
		"work-apps.json.lock",
		"env-profile.json",
		"env-profile.json.lock",
		"work/env-profile.json",
		"work/env-profile.json.lock",
		"work-stores.json",
		"work-stores.json.lock",
		"work/stores.json",
		"work/stores.json.lock",
		"work/packages.json",
		"work/packages.json.lock",
	}
)

func init() {
	existing := make(map[string]bool, len(protectedStateFiles))
	for _, entry := range protectedStateFiles {
		existing[strings.ToLower(entry)] = true
	}
	for _, entry := range workProtectedStateEntries {
		lower := strings.ToLower(entry)
		if !existing[lower] {
			protectedStateFiles = append(protectedStateFiles, lower)
			existing[lower] = true
		}
	}
}

// WorkDir returns ~/.hoxi/work (or $HOXI_HOME/work), or "" when Home() is unknown.
func WorkDir() string {
	return Join(WorkDirName)
}

// WorkPackagesDir returns ~/.hoxi/work/packages, or "" when Home() is unknown.
func WorkPackagesDir() string {
	return Join(WorkDirName, WorkPackagesDirName)
}

// WorkToolchainsDir returns ~/.hoxi/work/toolchains, or "" when Home() is unknown.
func WorkToolchainsDir() string {
	return Join(WorkDirName, WorkToolchainsDirName)
}

// WorkRuntimesDir returns ~/.hoxi/work/runtimes, or "" when Home() is unknown.
func WorkRuntimesDir() string {
	return Join(WorkDirName, WorkRuntimesDirName)
}

// WorkWorkspaceDir returns ~/.hoxi/work/workspace, or "" when Home() is unknown.
func WorkWorkspaceDir() string {
	return Join(WorkDirName, WorkWorkspaceDirName)
}

// WorkStoresDir returns ~/.hoxi/work/stores, or "" when Home() is unknown.
func WorkStoresDir() string {
	return Join(WorkDirName, WorkStoresDirName)
}

// WorkEnvProfilePath returns ~/.hoxi/work/env-profile.json, or "" when Home() is unknown.
func WorkEnvProfilePath() string {
	return Join(WorkDirName, WorkEnvProfileFileName)
}

// WorkAppRegistryPath returns ~/.hoxi/work-apps.json, or "" when Home() is unknown.
func WorkAppRegistryPath() string {
	return Join(WorkAppRegistryFileName)
}

// ValidateWorkSegment validates a single path segment (such as a version or
// toolchain identifier) without silently trimming or normalizing unsafe input.
func ValidateWorkSegment(seg string) (string, error) {
	if seg == "" {
		return "", errors.New("work identifier is required")
	}
	if strings.TrimSpace(seg) != seg {
		return "", fmt.Errorf("work identifier %q must not have leading or trailing whitespace", seg)
	}
	if strings.Contains(seg, "..") || strings.ContainsAny(seg, `/\`) {
		return "", fmt.Errorf("work identifier %q must be a single safe path segment", seg)
	}
	if !workIDPattern.MatchString(seg) {
		return "", fmt.Errorf("invalid work identifier %q", seg)
	}
	lower := strings.ToLower(seg)
	if lower == "hoxi.json" || IsProtectedStateFile(lower) {
		return "", fmt.Errorf("reserved work identifier %q", seg)
	}
	return seg, nil
}

// ValidateWorkAppID validates an app/package identifier used under
// ~/.hoxi/work/{packages,runtimes}/<appId>.
func ValidateWorkAppID(appID string) (string, error) {
	id, err := ValidateWorkSegment(appID)
	if err != nil {
		return "", err
	}
	switch strings.ToLower(id) {
	case "current", "versions":
		return "", fmt.Errorf("reserved work app id %q", appID)
	}
	return id, nil
}

// WorkPackageDir returns ~/.hoxi/work/packages/<appId>, or "" when appId or Home() is invalid.
func WorkPackageDir(appID string) string {
	id, err := ValidateWorkAppID(appID)
	if err != nil {
		return ""
	}
	return Join(WorkDirName, WorkPackagesDirName, id)
}

// WorkPackageCurrentDir returns ~/.hoxi/work/packages/<appId>/current, or "" when invalid.
func WorkPackageCurrentDir(appID string) string {
	pkgDir := WorkPackageDir(appID)
	if pkgDir == "" {
		return ""
	}
	return filepath.Join(pkgDir, "current")
}

// WorkPackageVersionsDir returns ~/.hoxi/work/packages/<appId>/versions, or "" when invalid.
func WorkPackageVersionsDir(appID string) string {
	pkgDir := WorkPackageDir(appID)
	if pkgDir == "" {
		return ""
	}
	return filepath.Join(pkgDir, "versions")
}

// WorkPackageVersionDir returns ~/.hoxi/work/packages/<appId>/versions/<version>, or "" when invalid.
func WorkPackageVersionDir(appID, version string) string {
	versionsDir := WorkPackageVersionsDir(appID)
	ver, err := ValidateWorkSegment(version)
	if versionsDir == "" || err != nil {
		return ""
	}
	return filepath.Join(versionsDir, ver)
}

// WorkRuntimeDir returns ~/.hoxi/work/runtimes/<appId>, or "" when appId or Home() is invalid.
func WorkRuntimeDir(appID string) string {
	id, err := ValidateWorkAppID(appID)
	if err != nil {
		return ""
	}
	return Join(WorkDirName, WorkRuntimesDirName, id)
}

// WorkToolchainDir returns ~/.hoxi/work/toolchains/<name>, or "" when name or Home() is invalid.
func WorkToolchainDir(name string) string {
	seg, err := ValidateWorkSegment(name)
	if err != nil {
		return ""
	}
	return Join(WorkDirName, WorkToolchainsDirName, seg)
}

// WorkStoreDir returns ~/.hoxi/work/stores/<storeId>, or "" when storeId or Home() is invalid.
func WorkStoreDir(storeID string) string {
	id, err := ValidateWorkSegment(storeID)
	if err != nil {
		return ""
	}
	return Join(WorkDirName, WorkStoresDirName, id)
}

// WorkStoreFilePath returns ~/.hoxi/work/stores/<storeId>.json, or "" when storeId or Home() is invalid.
func WorkStoreFilePath(storeID string) string {
	id, err := ValidateWorkSegment(storeID)
	if err != nil {
		return ""
	}
	return Join(WorkDirName, WorkStoresDirName, id+".json")
}

// ProtectedWorkStatePaths returns the concrete sensitive work state files and
// directories under Home() that confined sessions and untrusted tools must not read or mutate.
func ProtectedWorkStatePaths() []string {
	if Home() == "" {
		return nil
	}
	return []string{
		WorkAppRegistryPath(),
		WorkAppRegistryPath() + ".lock",
		WorkEnvProfilePath(),
		WorkEnvProfilePath() + ".lock",
		WorkStoresDir(),
		Join(WorkDirName, "stores.json"),
		Join(WorkDirName, "stores.json.lock"),
		Join(WorkDirName, "packages.json"),
		Join(WorkDirName, "packages.json.lock"),
	}
}

// IsProtectedWorkStatePath reports whether p points to or lies inside sensitive
// work state (such as ~/.hoxi/work-apps.json, ~/.hoxi/work/env-profile.json, or
// ~/.hoxi/work/stores/**).
func IsProtectedWorkStatePath(p string) bool {
	trimmed := strings.TrimSpace(p)
	if trimmed == "" {
		return false
	}
	cleaned := filepath.Clean(trimmed)
	if home := Home(); home != "" {
		for _, protected := range ProtectedWorkStatePaths() {
			if protected == "" {
				continue
			}
			if sameOrSubpath(protected, cleaned) {
				return true
			}
		}
	}
	slash := strings.ToLower(filepath.ToSlash(cleaned))
	for _, token := range []string{
		"/.hoxi/work-apps.json",
		"/.hoxi/work-apps.json.lock",
		"/.hoxi/work-stores.json",
		"/.hoxi/work-stores.json.lock",
		"/.hoxi/work/env-profile.json",
		"/.hoxi/work/env-profile.json.lock",
		"/.hoxi/work/stores.json",
		"/.hoxi/work/stores.json.lock",
		"/.hoxi/work/packages.json",
		"/.hoxi/work/packages.json.lock",
	} {
		if strings.HasSuffix(slash, token) || slash == strings.TrimPrefix(token, "/") {
			return true
		}
	}
	if strings.HasSuffix(slash, "/.hoxi/work/stores") ||
		strings.Contains(slash, "/.hoxi/work/stores/") ||
		slash == ".hoxi/work/stores" ||
		strings.HasPrefix(slash, ".hoxi/work/stores/") {
		return true
	}
	return false
}

func sameOrSubpath(base, target string) bool {
	baseClean := filepath.Clean(base)
	targetClean := filepath.Clean(target)
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		baseClean = strings.ToLower(baseClean)
		targetClean = strings.ToLower(targetClean)
	}
	rel, err := filepath.Rel(baseClean, targetClean)
	if err != nil {
		return false
	}
	slashRel := filepath.ToSlash(rel)
	return slashRel == "." || (slashRel != ".." && !strings.HasPrefix(slashRel, "../") && !filepath.IsAbs(rel))
}
