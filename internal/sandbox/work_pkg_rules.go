package sandbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

// WorkAppPackageRules describes the explicit read+exec grants and write/read
// denials for one or more session-bound work app packages.
type WorkAppPackageRules struct {
	AllowRead []PathRule
	DenyWrite []PathRule
	DenyRead  []PathRule
}

var (
	homeShellRCFiles = []string{
		".zshrc",
		".zshenv",
		".zprofile",
		".zlogin",
		".zlogout",
		".bashrc",
		".bash_profile",
		".bash_login",
		".bash_logout",
		".profile",
		".shrc",
		".kshrc",
		".cshrc",
		".tcshrc",
		".login",
		".envrc",
		".gitconfig",
		".curlrc",
		".wgetrc",
		".npmrc",
		".pypirc",
		".netrc",
	}

	homeShellRCDirs = []string{
		".ssh",
		".gnupg",
		filepath.Join(".config", "fish"),
	}

	installerGlobalSystemDirs = []string{
		"/System",
		"/Library",
		"/Applications",
		"/usr",
		"/bin",
		"/sbin",
		"/etc",
		"/private/etc",
		"/opt",
		"/private/var/db",
		"/private/var/root",
	}
)

// BuildWorkAppPackageRules computes validated read+exec and immutable write-deny
// rules for session-bound work app bundles (packages/<appId>/current and runtimes/<appId>).
func BuildWorkAppPackageRules(appIDs ...string) (WorkAppPackageRules, error) {
	if len(appIDs) == 0 {
		return WorkAppPackageRules{}, errors.New("at least one work app id is required")
	}
	roots, err := validatedWorkRoots()
	if err != nil {
		return WorkAppPackageRules{}, err
	}

	var allowRead []PathRule
	var denyWrite []PathRule
	seenApps := make(map[string]bool, len(appIDs))

	for _, rawID := range appIDs {
		appID, err := hoxipaths.ValidateWorkAppID(rawID)
		if err != nil {
			return WorkAppPackageRules{}, err
		}
		if seenApps[appID] {
			continue
		}
		seenApps[appID] = true

		pkgAppLex := filepath.Join(roots.packages, appID)
		pkgAppRes := resolvePathSymlinks(pkgAppLex)
		if !isStrictSubpath(roots.packages, pkgAppRes) {
			return WorkAppPackageRules{}, fmt.Errorf("work package directory for %q escapes packages root", appID)
		}

		currentLex := filepath.Join(pkgAppRes, "current")
		currentRes := resolvePathSymlinks(currentLex)
		if !isSameOrSubpath(pkgAppRes, currentRes) || isSamePath(pkgAppRes, currentRes) {
			return WorkAppPackageRules{}, fmt.Errorf("work package current target for %q escapes package root", appID)
		}
		for _, p := range uniquePaths(currentLex, currentRes) {
			allowRead = appendUniqueRule(allowRead, PathRule{Path: p, Kind: PathSubtree})
			denyWrite = appendUniqueRule(denyWrite, PathRule{Path: p, Kind: PathSubtree})
		}

		runtimeLex := filepath.Join(roots.runtimes, appID)
		runtimeRes := resolvePathSymlinks(runtimeLex)
		if !isStrictSubpath(roots.runtimes, runtimeRes) {
			return WorkAppPackageRules{}, fmt.Errorf("work runtime directory for %q escapes runtimes root", appID)
		}
		for _, p := range uniquePaths(runtimeLex, runtimeRes) {
			allowRead = appendUniqueRule(allowRead, PathRule{Path: p, Kind: PathSubtree})
			denyWrite = appendUniqueRule(denyWrite, PathRule{Path: p, Kind: PathSubtree})
		}
	}

	// Enforce immutability across all work bundle/toolchain/store roots so an AI
	// session cannot mutate installed bundles even when ~/.hoxi is writable.
	for _, dir := range []string{roots.packages, roots.runtimes, roots.toolchains, roots.stores} {
		denyWrite = appendUniqueRule(denyWrite, PathRule{Path: dir, Kind: PathSubtree})
	}
	for _, file := range []string{roots.envProfile, roots.envProfile + ".lock"} {
		denyWrite = appendUniqueRule(denyWrite, PathRule{Path: file, Kind: PathLiteral})
	}

	denyRead := []PathRule{
		{Path: roots.stores, Kind: PathSubtree},
		{Path: roots.envProfile, Kind: PathLiteral},
		{Path: roots.envProfile + ".lock", Kind: PathLiteral},
	}

	return WorkAppPackageRules{
		AllowRead: allowRead,
		DenyWrite: denyWrite,
		DenyRead:  denyRead,
	}, nil
}

// WithWorkAppPackageAccess returns a copy of profile configured with read+exec
// access to packages/<appId>/current and runtimes/<appId> while strictly
// enforcing DenyWrite on app packages, runtimes, toolchains, and stores.
func WithWorkAppPackageAccess(profile Profile, appIDs ...string) (Profile, error) {
	rules, err := BuildWorkAppPackageRules(appIDs...)
	if err != nil {
		return Profile{}, err
	}
	roots, err := validatedWorkRoots()
	if err != nil {
		return Profile{}, err
	}
	out := cloneProfile(profile)
	immutableRoots := []string{roots.packages, roots.runtimes, roots.toolchains, roots.stores}
	out.Filesystem.AllowWrite = filterRulesOutsideSubtrees(out.Filesystem.AllowWrite, immutableRoots, roots.envProfile)
	out.Filesystem.NameDenyExemptions = filterRulesOutsideSubtrees(out.Filesystem.NameDenyExemptions, immutableRoots, roots.envProfile)
	out.Filesystem.DenyRead = unblockAllowedReadRoots(out.Filesystem.DenyRead, rules.AllowRead, roots.work, roots.packages, roots.runtimes)
	for _, rule := range rules.DenyRead {
		out.Filesystem.DenyRead = appendUniqueRule(out.Filesystem.DenyRead, rule)
	}
	for _, rule := range rules.DenyWrite {
		out.Filesystem.DenyWrite = appendUniqueRule(out.Filesystem.DenyWrite, rule)
	}
	out.Filesystem.DenyNames = ensureDenyNames(out.Filesystem.DenyNames, ".git", "hoxi.json")
	return out, nil
}

// WorkPackageSessionProfile builds a session DefaultProfile with opt-in read+exec
// access to the specified work app packages and strict DenyWrite bundle protection.
func WorkPackageSessionProfile(workspace string, network NetworkMode, appIDs []string, extraWritable ...string) (Profile, error) {
	base, err := DefaultProfile(workspace, network, extraWritable...)
	if err != nil {
		return Profile{}, err
	}
	return WithWorkAppPackageAccess(base, appIDs...)
}

// WithReadOnlyExecRoots returns a copy of profile granting read+exec access on
// validated read-only bundle roots while strictly enforcing DenyWrite on each root.
func WithReadOnlyExecRoots(profile Profile, execRoots ...string) (Profile, error) {
	if len(execRoots) == 0 {
		return Profile{}, errors.New("at least one read-only exec root is required")
	}
	roots, err := validatedWorkRoots()
	if err != nil {
		return Profile{}, err
	}
	var allowRead []PathRule
	var strippedSubtrees []string
	for _, raw := range execRoots {
		resolved, err := validateReadOnlyExecRoot(raw, roots)
		if err != nil {
			return Profile{}, err
		}
		allowRead = appendUniqueRule(allowRead, PathRule{Path: resolved, Kind: PathSubtree})
		strippedSubtrees = append(strippedSubtrees, resolved)
	}

	out := cloneProfile(profile)
	strippedSubtrees = append(strippedSubtrees, roots.packages, roots.runtimes, roots.toolchains, roots.stores)
	out.Filesystem.AllowWrite = filterRulesOutsideSubtrees(out.Filesystem.AllowWrite, strippedSubtrees, roots.envProfile)
	out.Filesystem.NameDenyExemptions = filterRulesOutsideSubtrees(out.Filesystem.NameDenyExemptions, strippedSubtrees, roots.envProfile)
	out.Filesystem.DenyRead = unblockAllowedReadRoots(out.Filesystem.DenyRead, allowRead, roots.work, roots.packages, roots.runtimes)

	for _, rule := range allowRead {
		out.Filesystem.DenyWrite = appendUniqueRule(out.Filesystem.DenyWrite, rule)
	}
	for _, dir := range []string{roots.packages, roots.runtimes, roots.toolchains, roots.stores} {
		out.Filesystem.DenyWrite = appendUniqueRule(out.Filesystem.DenyWrite, PathRule{Path: dir, Kind: PathSubtree})
	}
	for _, file := range []string{roots.envProfile, roots.envProfile + ".lock"} {
		out.Filesystem.DenyWrite = appendUniqueRule(out.Filesystem.DenyWrite, PathRule{Path: file, Kind: PathLiteral})
	}
	out.Filesystem.DenyRead = appendUniqueRule(out.Filesystem.DenyRead, PathRule{Path: roots.stores, Kind: PathSubtree})
	out.Filesystem.DenyRead = appendUniqueRule(out.Filesystem.DenyRead, PathRule{Path: roots.envProfile, Kind: PathLiteral})
	out.Filesystem.DenyRead = appendUniqueRule(out.Filesystem.DenyRead, PathRule{Path: roots.envProfile + ".lock", Kind: PathLiteral})
	out.Filesystem.DenyNames = ensureDenyNames(out.Filesystem.DenyNames, ".git", "hoxi.json")
	return out, nil
}

// WorkSetupInstallerProfile builds a strictly confined profile for an app's
// setup/runtime installer: AllowWrite is restricted strictly to
// ~/.hoxi/work/toolchains, ~/.hoxi/work/runtimes/<appId>, and temp dirs, while
// hard-denying ~/.hoxi protected files, .git, hoxi.json, home shell rc files,
// and global system directories.
func WorkSetupInstallerProfile(appID string, network NetworkMode) (Profile, error) {
	validAppID, err := hoxipaths.ValidateWorkAppID(appID)
	if err != nil {
		return Profile{}, err
	}
	mode, err := normalizeInstallerNetworkMode(network)
	if err != nil {
		return Profile{}, err
	}
	roots, err := validatedWorkRoots()
	if err != nil {
		return Profile{}, err
	}
	runtimeDir := resolvePathSymlinks(filepath.Join(roots.runtimes, validAppID))
	if !isStrictSubpath(roots.runtimes, runtimeDir) {
		return Profile{}, fmt.Errorf("work runtime directory for %q escapes runtimes root", validAppID)
	}
	return buildInstallerProfile(roots, runtimeDir, mode), nil
}

// WorkToolchainInstallerProfile builds a confined installer profile when setting
// up shared toolchains only (AllowWrite restricted strictly to ~/.hoxi/work/toolchains
// and temp dirs, with ~/.hoxi/work/runtimes also write-denied).
func WorkToolchainInstallerProfile(network NetworkMode) (Profile, error) {
	mode, err := normalizeInstallerNetworkMode(network)
	if err != nil {
		return Profile{}, err
	}
	roots, err := validatedWorkRoots()
	if err != nil {
		return Profile{}, err
	}
	return buildInstallerProfile(roots, "", mode), nil
}

// RestrictToWorkSetupInstaller narrows an existing Profile to setup-installer
// write confinement for appID while preserving valid network port/socket denials.
func RestrictToWorkSetupInstaller(profile Profile, appID string) (Profile, error) {
	mode := profile.Network.Mode
	if mode == "" {
		mode = NetworkDeny
	}
	installer, err := WorkSetupInstallerProfile(appID, mode)
	if err != nil {
		return Profile{}, err
	}
	out := cloneProfile(profile)
	out.Filesystem.AllowWrite = installer.Filesystem.AllowWrite
	out.Filesystem.NameDenyExemptions = nil
	out.Filesystem.TemporaryDirectory = installer.Filesystem.TemporaryDirectory
	out.Filesystem.DenyNames = ensureDenyNames(out.Filesystem.DenyNames, ".git", "hoxi.json")
	for _, rule := range installer.Filesystem.DenyWrite {
		out.Filesystem.DenyWrite = appendUniqueRule(out.Filesystem.DenyWrite, rule)
	}
	for _, rule := range installer.Filesystem.DenyRead {
		out.Filesystem.DenyRead = appendUniqueRule(out.Filesystem.DenyRead, rule)
	}
	out.Network.Mode = installer.Network.Mode
	for _, sock := range installer.Network.DenySockets {
		out.Network.DenySockets = appendUniqueRule(out.Network.DenySockets, sock)
	}
	return out, nil
}

type resolvedWorkRoots struct {
	hoxiHome   string
	work       string
	packages   string
	toolchains string
	runtimes   string
	workspace  string
	stores     string
	envProfile string
}

func validatedWorkRoots() (resolvedWorkRoots, error) {
	rawHome := strings.TrimSpace(hoxipaths.Home())
	if rawHome == "" {
		return resolvedWorkRoots{}, errors.New("hoxi home directory is unavailable")
	}
	hoxiHome := resolvePathSymlinks(rawHome)
	if hoxiHome == "" || hoxiHome == string(filepath.Separator) {
		return resolvedWorkRoots{}, errors.New("invalid hoxi home directory")
	}
	work := resolvePathSymlinks(hoxipaths.WorkDir())
	if !isStrictSubpath(hoxiHome, work) {
		return resolvedWorkRoots{}, errors.New("hoxi work directory escapes hoxi home")
	}
	packages := resolvePathSymlinks(hoxipaths.WorkPackagesDir())
	toolchains := resolvePathSymlinks(hoxipaths.WorkToolchainsDir())
	runtimes := resolvePathSymlinks(hoxipaths.WorkRuntimesDir())
	workspace := resolvePathSymlinks(hoxipaths.WorkWorkspaceDir())
	stores := resolvePathSymlinks(hoxipaths.WorkStoresDir())
	for name, sub := range map[string]string{
		"packages":   packages,
		"toolchains": toolchains,
		"runtimes":   runtimes,
		"workspace":  workspace,
		"stores":     stores,
	} {
		if !isStrictSubpath(work, sub) {
			return resolvedWorkRoots{}, fmt.Errorf("hoxi work %s directory escapes work root", name)
		}
	}
	envProfile := resolvePathSymlinks(hoxipaths.WorkEnvProfilePath())
	if !isStrictSubpath(work, envProfile) {
		return resolvedWorkRoots{}, errors.New("hoxi work env-profile.json escapes work root")
	}
	return resolvedWorkRoots{
		hoxiHome:   hoxiHome,
		work:       work,
		packages:   packages,
		toolchains: toolchains,
		runtimes:   runtimes,
		workspace:  workspace,
		stores:     stores,
		envProfile: envProfile,
	}, nil
}

func buildInstallerProfile(roots resolvedWorkRoots, runtimeDir string, mode NetworkMode) Profile {
	allowCandidates := []string{roots.toolchains}
	if runtimeDir != "" {
		allowCandidates = append(allowCandidates, runtimeDir)
	}
	allowCandidates = append(allowCandidates, os.TempDir(), "/tmp", "/private/tmp", "/private/var/folders")
	allow := canonicalSubtrees(allowCandidates)

	var denyWrite []PathRule
	for _, dir := range []string{
		DefaultRoot(),
		hoxipaths.BinDir(),
		hoxipaths.EngineDataDir(),
		hoxipaths.EngineStateDir(),
		filepath.Join(roots.hoxiHome, "sandbox"),
		filepath.Join(roots.hoxiHome, "browser-profile"),
		filepath.Join(roots.hoxiHome, "workspaces"),
		filepath.Join(roots.hoxiHome, "extensions"),
		roots.packages,
		roots.stores,
		roots.workspace,
	} {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		resolved := resolvePathSymlinks(dir)
		if resolved != "" && resolved != string(filepath.Separator) {
			denyWrite = appendUniqueRule(denyWrite, PathRule{Path: resolved, Kind: PathSubtree})
		}
	}
	if runtimeDir == "" {
		denyWrite = appendUniqueRule(denyWrite, PathRule{Path: roots.runtimes, Kind: PathSubtree})
	}
	for _, file := range []string{roots.envProfile, roots.envProfile + ".lock"} {
		denyWrite = appendUniqueRule(denyWrite, PathRule{Path: file, Kind: PathLiteral})
	}
	for _, rule := range protectedStateRules() {
		denyWrite = appendUniqueRule(denyWrite, normalizeRulePath(rule))
	}
	for _, rule := range laterExecutionCacheRules() {
		denyWrite = appendUniqueRule(denyWrite, rule)
	}
	for _, rule := range homeShellRCWriteDenyRules() {
		denyWrite = appendUniqueRule(denyWrite, rule)
	}
	for _, sysDir := range installerGlobalSystemDirs {
		denyWrite = appendUniqueRule(denyWrite, PathRule{Path: filepath.Clean(sysDir), Kind: PathSubtree})
	}

	var denyRead []PathRule
	for _, rule := range secretReadRules("") {
		denyRead = appendUniqueRule(denyRead, normalizeRulePath(rule))
	}
	for _, rule := range anywhereSecretFileRules() {
		denyRead = appendUniqueRule(denyRead, rule)
	}
	for _, rule := range agentCredentialRules() {
		denyRead = appendUniqueRule(denyRead, rule)
	}
	denyRead = appendUniqueRule(denyRead, PathRule{Path: roots.stores, Kind: PathSubtree})
	denyRead = appendUniqueRule(denyRead, PathRule{Path: roots.envProfile, Kind: PathLiteral})
	denyRead = appendUniqueRule(denyRead, PathRule{Path: roots.envProfile + ".lock", Kind: PathLiteral})

	return Profile{
		Filesystem: FilesystemProfile{
			AllowWrite:         allow,
			DenyWrite:          denyWrite,
			DenyRead:           denyRead,
			DenyNames:          []string{".git", "hoxi.json"},
			TemporaryDirectory: "/private/tmp",
		},
		Network: NetworkProfile{
			Mode:        mode,
			DenySockets: credentialSocketRules(),
		},
	}
}

func homeShellRCWriteDenyRules() []PathRule {
	var homes []string
	if envHome := strings.TrimSpace(os.Getenv("HOME")); filepath.IsAbs(envHome) {
		homes = append(homes, filepath.Clean(envHome), resolvePathSymlinks(envHome))
	}
	if usrHome, err := os.UserHomeDir(); err == nil && strings.TrimSpace(usrHome) != "" {
		homes = append(homes, filepath.Clean(usrHome), resolvePathSymlinks(usrHome))
	}
	var rules []PathRule
	for _, h := range uniquePaths(homes...) {
		if h == "" || h == string(filepath.Separator) {
			continue
		}
		for _, rc := range homeShellRCFiles {
			rules = appendUniqueRule(rules, PathRule{Path: filepath.Join(h, rc), Kind: PathLiteral})
		}
		for _, dir := range homeShellRCDirs {
			rules = appendUniqueRule(rules, PathRule{Path: filepath.Join(h, dir), Kind: PathSubtree})
		}
	}
	return rules
}

func validateReadOnlyExecRoot(raw string, roots resolvedWorkRoots) (string, error) {
	if raw == "" || strings.TrimSpace(raw) != raw {
		return "", errors.New("read-only exec root must be a non-empty trimmed path")
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("read-only exec root contains control character: %q", raw)
		}
	}
	if strings.HasPrefix(raw, "~") || !filepath.IsAbs(raw) {
		return "", fmt.Errorf("read-only exec root must be an absolute path: %q", raw)
	}
	for _, seg := range strings.Split(filepath.ToSlash(raw), "/") {
		if seg == ".." || seg == ".git" {
			return "", fmt.Errorf("read-only exec root contains disallowed segment %q", raw)
		}
	}
	st, err := os.Stat(raw)
	if err != nil || !st.IsDir() {
		return "", fmt.Errorf("read-only exec root is not an existing directory: %q", raw)
	}
	resolved := resolvePathSymlinks(raw)
	if resolved == "" || resolved == string(filepath.Separator) {
		return "", fmt.Errorf("read-only exec root refuses filesystem root: %q", raw)
	}
	for _, tempRoot := range []string{os.TempDir(), "/tmp", "/private/tmp", "/var/tmp", "/private/var/folders"} {
		if strings.TrimSpace(tempRoot) == "" {
			continue
		}
		tr := resolvePathSymlinks(tempRoot)
		// Allow HOXI_HOME under temp in tests when the target is explicitly inside validated work bundle roots.
		if isSameOrSubpath(tr, resolved) && !isStrictSubpath(roots.packages, resolved) && !isStrictSubpath(roots.runtimes, resolved) && !isSameOrSubpath(roots.toolchains, resolved) {
			return "", fmt.Errorf("read-only exec root refuses temporary directory: %q", raw)
		}
	}
	if hoxipaths.IsProtectedWorkStatePath(raw) || hoxipaths.IsProtectedWorkStatePath(resolved) {
		return "", fmt.Errorf("read-only exec root refuses protected work state path: %q", raw)
	}
	if isSameOrSubpath(roots.hoxiHome, resolved) {
		if !isStrictSubpath(roots.packages, resolved) && !isStrictSubpath(roots.runtimes, resolved) && !isSameOrSubpath(roots.toolchains, resolved) {
			return "", fmt.Errorf("read-only exec root under ~/.hoxi must reside in work packages, runtimes, or toolchains: %q", raw)
		}
	}
	if usrHome, err := os.UserHomeDir(); err == nil && strings.TrimSpace(usrHome) != "" {
		uh := resolvePathSymlinks(usrHome)
		if isSameOrSubpath(resolved, uh) {
			return "", fmt.Errorf("read-only exec root refuses user home or ancestor: %q", raw)
		}
		for _, secretDir := range []string{".ssh", ".aws", ".gnupg", ".kube", ".azure", ".codex", ".claude", ".gemini", ".docker", ".cargo"} {
			if isSameOrSubpath(filepath.Join(uh, secretDir), resolved) {
				return "", fmt.Errorf("read-only exec root refuses credential directory: %q", raw)
			}
		}
	}
	return resolved, nil
}

func normalizeInstallerNetworkMode(mode NetworkMode) (NetworkMode, error) {
	switch mode {
	case "":
		return NetworkDeny, nil
	case NetworkAllow, NetworkDeny, NetworkLocal:
		return mode, nil
	default:
		return "", fmt.Errorf("unsupported sandbox network mode: %s", mode)
	}
}

func normalizeRulePath(rule PathRule) PathRule {
	if rule.Kind == PathRegex || strings.TrimSpace(rule.Path) == "" {
		return rule
	}
	return PathRule{Path: resolvePathSymlinks(rule.Path), Kind: rule.Kind}
}

func cloneProfile(p Profile) Profile {
	return Profile{
		Filesystem: FilesystemProfile{
			AllowWrite:         append([]PathRule(nil), p.Filesystem.AllowWrite...),
			DenyWrite:          append([]PathRule(nil), p.Filesystem.DenyWrite...),
			DenyRead:           append([]PathRule(nil), p.Filesystem.DenyRead...),
			DenyNames:          append([]string(nil), p.Filesystem.DenyNames...),
			NameDenyExemptions: append([]PathRule(nil), p.Filesystem.NameDenyExemptions...),
			TemporaryDirectory: p.Filesystem.TemporaryDirectory,
		},
		Network: NetworkProfile{
			Mode:              p.Network.Mode,
			DenySockets:       append([]PathRule(nil), p.Network.DenySockets...),
			DenyLoopbackPorts: append([]int(nil), p.Network.DenyLoopbackPorts...),
			DenyLoopbackTCP:   append([]int(nil), p.Network.DenyLoopbackTCP...),
			DenyLoopbackUDP:   append([]int(nil), p.Network.DenyLoopbackUDP...),
			AllowUnixSockets:  append([]PathRule(nil), p.Network.AllowUnixSockets...),
		},
	}
}

func filterRulesOutsideSubtrees(rules []PathRule, subtrees []string, exactFiles ...string) []PathRule {
	var out []PathRule
	for _, r := range rules {
		if r.Kind != PathRegex {
			rp := resolvePathSymlinks(r.Path)
			blocked := false
			for _, sub := range subtrees {
				if sub != "" && isSameOrSubpath(sub, rp) {
					blocked = true
					break
				}
			}
			if !blocked {
				for _, f := range exactFiles {
					if f != "" && (isSamePath(f, rp) || isSamePath(f+".lock", rp)) {
						blocked = true
						break
					}
				}
			}
			if blocked {
				continue
			}
		}
		out = append(out, r)
	}
	return out
}

func unblockAllowedReadRoots(denyRead []PathRule, allowRead []PathRule, parentRoots ...string) []PathRule {
	var out []PathRule
	for _, dr := range denyRead {
		if dr.Kind == PathRegex {
			out = append(out, dr)
			continue
		}
		dp := resolvePathSymlinks(dr.Path)
		drop := false
		for _, ar := range allowRead {
			ap := resolvePathSymlinks(ar.Path)
			if dr.Kind == PathSubtree && isSameOrSubpath(dp, ap) {
				// Only drop broad work/packages/runtimes subtree blocks, never unrelated secret dirs.
				for _, pr := range parentRoots {
					if pr != "" && isSameOrSubpath(pr, dp) {
						drop = true
						break
					}
				}
			} else if dr.Kind == PathLiteral && isSamePath(dp, ap) {
				drop = true
			}
			if drop {
				break
			}
		}
		if !drop {
			out = append(out, dr)
		}
	}
	return out
}

func ensureDenyNames(names []string, required ...string) []string {
	seen := make(map[string]bool, len(names)+len(required))
	out := make([]string, 0, len(names)+len(required))
	for _, n := range names {
		if strings.TrimSpace(n) != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	for _, req := range required {
		if !seen[req] {
			seen[req] = true
			out = append(out, req)
		}
	}
	return out
}

func appendUniqueRule(rules []PathRule, candidate PathRule) []PathRule {
	if strings.TrimSpace(candidate.Path) == "" {
		return rules
	}
	for _, existing := range rules {
		if existing.Kind == candidate.Kind && existing.Path == candidate.Path {
			return rules
		}
	}
	return append(rules, candidate)
}

func uniquePaths(paths ...string) []string {
	seen := make(map[string]bool, len(paths))
	var out []string
	for _, p := range paths {
		cleaned := strings.TrimSpace(p)
		if cleaned == "" {
			continue
		}
		cleaned = filepath.Clean(cleaned)
		key := cleaned
		if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if !seen[key] {
			seen[key] = true
			out = append(out, cleaned)
		}
	}
	return out
}

func isSamePath(a, b string) bool {
	ca, cb := filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		return strings.EqualFold(ca, cb)
	}
	return ca == cb
}

func isSameOrSubpath(base, target string) bool {
	if base == "" || target == "" {
		return false
	}
	cb, ct := filepath.Clean(base), filepath.Clean(target)
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		cb, ct = strings.ToLower(cb), strings.ToLower(ct)
	}
	rel, err := filepath.Rel(cb, ct)
	if err != nil {
		return false
	}
	slash := filepath.ToSlash(rel)
	return slash == "." || (slash != ".." && !strings.HasPrefix(slash, "../") && !filepath.IsAbs(rel))
}

func isStrictSubpath(base, target string) bool {
	return isSameOrSubpath(base, target) && !isSamePath(base, target)
}
