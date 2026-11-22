package runner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"weak"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

// SessionBundleSpec describes optional vendor-neutral skill directories,
// toolchain/runtime PATH additions, non-secret environment variables, and
// read-only executable roots bound to a session.
type SessionBundleSpec struct {
	AppIDs            []string          `json:"appIds,omitempty"`
	ExtraSkillDirs    []string          `json:"extraSkillDirs,omitempty"`
	ExtraPathDirs     []string          `json:"extraPathDirs,omitempty"`
	ExtraEnv          map[string]string `json:"extraEnv,omitempty"`
	ReadOnlyExecRoots []string          `json:"readOnlyExecRoots,omitempty"`
}

// SessionBundleResolver resolves an optional SessionBundleSpec for a session
// and its working directory. Unbound sessions return an empty spec and nil error.
type SessionBundleResolver func(sessionID, directory string) (SessionBundleSpec, error)

type sessionBundleEntry struct {
	resolver SessionBundleResolver
	cleanup  runtime.Cleanup
}

var (
	sessionBundleMu        sync.RWMutex
	sessionBundleResolvers = make(map[weak.Pointer[Service]]sessionBundleEntry)

	envVarKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

	bundleSecretValuePattern = regexp.MustCompile(
		`(?i)^Bearer\s+\S+|^-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----|^hk-[A-Za-z0-9_-]{16,}|^AIza[0-9A-Za-z_-]{30,}|^eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`,
	)

	blockedBundleEnvExact = setOf(
		// Process / shell / path / user identity hijacking
		"PATH", "CDPATH", "FPATH", "MANPATH", "INFOPATH",
		"HOME", "USERPROFILE", "HOMEDRIVE", "HOMEPATH", "APPDATA", "LOCALAPPDATA",
		"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR",
		"USER", "LOGNAME", "USERNAME", "SHELL", "COMSPEC", "PATHEXT", "PSMODULEPATH",
		"IFS", "ENV", "BASH_ENV", "ZDOTDIR", "SHELLOPTS", "BASHOPTS", "PROMPT_COMMAND", "PS4", "HISTFILE", "GLOBIGNORE",
		"TMPDIR", "TMP", "TEMP", "PWD", "OLDPWD",
		// Proxy & TLS CA interception
		"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "FTP_PROXY",
		"SSL_CERT_FILE", "SSL_CERT_DIR", "CURL_CA_BUNDLE", "REQUESTS_CA_BUNDLE",
		"NODE_EXTRA_CA_CERTS", "DENO_CERT", "WEBSOCKET_CLIENT_CA_BUNDLE",
		// Language / runtime code injection hooks
		"NODE_OPTIONS", "NODE_PATH", "NODE_REPL_EXTERNAL_MODULE",
		"PYTHONSTARTUP", "PYTHONPATH", "PYTHONHOME", "PYTHONINSPECT", "PYTHONBREAKPOINT", "PYTHONEXECUTABLE", "PYTHONUSERBASE",
		"RUBYOPT", "RUBYLIB", "GEM_HOME", "GEM_PATH", "BUNDLE_GEMFILE",
		"PERL5OPT", "PERL5LIB", "PERLLIB", "PERL5DB",
		"PHP_INI_SCAN_DIR", "PHPRC",
		"JAVA_TOOL_OPTIONS", "_JAVA_OPTIONS", "JDK_JAVA_OPTIONS", "CLASSPATH",
		"GOFLAGS", "GOROOT", "GOPATH", "GOCACHE", "GOMODCACHE", "GONOSUMDB", "GOPROXY", "GOTOOLCHAIN",
		"CGO_CFLAGS", "CGO_CXXFLAGS", "CGO_LDFLAGS", "CC", "CXX", "AR", "LD",
		"RUSTC_WRAPPER", "RUSTFLAGS", "CARGO_BUILD_RUSTC_WRAPPER", "CARGO_HOME",
		// Interactive / pager / privilege escalation
		"EDITOR", "VISUAL", "PAGER", "LESSOPEN", "LESSCLOSE", "BROWSER",
		"SUDO_COMMAND", "SUDO_USER", "SUDO_UID", "SUDO_GID",
	)

	blockedBundleEnvPrefixes = []string{
		"DYLD_",
		"LD_",
		"BASH_FUNC_",
		"OPENCODE_",
		"CLAUDE_",
		"GEMINI_",
		"ANTIGRAVITY_",
		"AGY_",
		"CODEX_",
		"QODER_",
		"CODEBUDDY_",
		"ANTHROPIC_",
		"OPENAI_",
		"AWS_",
		"AZURE_",
		"GOOGLE_",
		"GH_",
		"GITHUB_",
		"GITLAB_",
		"NPM_CONFIG_",
		"GIT_",
	}
)

type bundleDirKind int

const (
	bundleDirSkill bundleDirKind = iota
	bundleDirPath
	bundleDirExecRoot
)

// SetSessionBundleResolver registers or clears (when fn is nil) the optional
// per-session bundle resolver on s in a thread-safe manner.
func (s *Service) SetSessionBundleResolver(fn SessionBundleResolver) {
	if s == nil {
		return
	}
	key := weak.Make(s)
	sessionBundleMu.Lock()
	defer sessionBundleMu.Unlock()

	if prev, exists := sessionBundleResolvers[key]; exists {
		prev.cleanup.Stop()
		delete(sessionBundleResolvers, key)
	}
	if fn == nil {
		return
	}
	cleanup := runtime.AddCleanup(s, func(k weak.Pointer[Service]) {
		sessionBundleMu.Lock()
		delete(sessionBundleResolvers, k)
		sessionBundleMu.Unlock()
	}, key)
	sessionBundleResolvers[key] = sessionBundleEntry{
		resolver: fn,
		cleanup:  cleanup,
	}
}

// ResolveSessionBundle resolves, validates, and sanitizes the optional
// SessionBundleSpec for (sessionID, directory). If no resolver is registered,
// it returns an empty SessionBundleSpec and nil error.
func (s *Service) ResolveSessionBundle(sessionID, directory string) (SessionBundleSpec, error) {
	if s == nil {
		return SessionBundleSpec{}, nil
	}
	key := weak.Make(s)
	sessionBundleMu.RLock()
	entry, ok := sessionBundleResolvers[key]
	sessionBundleMu.RUnlock()
	if !ok || entry.resolver == nil {
		return SessionBundleSpec{}, nil
	}

	trimmedSessionID := strings.TrimSpace(sessionID)
	effectiveDir := strings.TrimSpace(directory)

	if trimmedSessionID != "" {
		resolvedDir, matched, err := s.validateBundleSessionDirectory(trimmedSessionID, effectiveDir)
		if err != nil {
			return SessionBundleSpec{}, err
		}
		if matched && effectiveDir == "" {
			effectiveDir = resolvedDir
		}
	}

	raw, err := entry.resolver(trimmedSessionID, effectiveDir)
	if err != nil {
		return SessionBundleSpec{}, err
	}
	return SanitizeSessionBundleSpec(raw)
}

func (s *Service) validateBundleSessionDirectory(sessionID, directory string) (string, bool, error) {
	if s.store != nil {
		if rec, exists := s.lookupSessionMeta(sessionID); exists {
			bound := rec.WorktreePath
			if bound == "" {
				bound = rec.ProjectDir
			}
			if directory != "" && bound != "" &&
				normalizeComparePath(directory) != normalizeComparePath(bound) &&
				normalizeComparePath(directory) != normalizeComparePath(rec.ProjectDir) {
				return "", true, errors.New("session directory does not match its sandbox")
			}
			return bound, true, nil
		}
	}
	if s.worktrees != nil {
		if binding, bound, err := s.worktrees.BindingForSession(sessionID); err != nil {
			return "", true, err
		} else if bound && binding.Directory != "" {
			if directory != "" && normalizeComparePath(directory) != normalizeComparePath(binding.Directory) {
				return "", true, errors.New("session directory does not match its sandbox")
			}
			return binding.Directory, true, nil
		}
	}
	return directory, false, nil
}

// SanitizeSessionBundleSpec validates all bundle IDs and directory paths (failing
// closed on invalid, missing, symlink-escaping, temporary, or sensitive paths)
// and strips secret or dangerous environment keys/values from ExtraEnv.
func SanitizeSessionBundleSpec(spec SessionBundleSpec) (SessionBundleSpec, error) {
	var out SessionBundleSpec

	if len(spec.AppIDs) > 0 {
		seen := make(map[string]bool, len(spec.AppIDs))
		for _, rawID := range spec.AppIDs {
			id, err := hoxipaths.ValidateWorkAppID(rawID)
			if err != nil {
				return SessionBundleSpec{}, err
			}
			if !seen[id] {
				seen[id] = true
				out.AppIDs = append(out.AppIDs, id)
			}
		}
	}

	var err error
	if out.ExtraSkillDirs, err = validateBundleDirs(spec.ExtraSkillDirs, bundleDirSkill); err != nil {
		return SessionBundleSpec{}, fmt.Errorf("extraSkillDirs: %w", err)
	}
	if out.ExtraPathDirs, err = validateBundleDirs(spec.ExtraPathDirs, bundleDirPath); err != nil {
		return SessionBundleSpec{}, fmt.Errorf("extraPathDirs: %w", err)
	}
	if out.ReadOnlyExecRoots, err = validateBundleDirs(spec.ReadOnlyExecRoots, bundleDirExecRoot); err != nil {
		return SessionBundleSpec{}, fmt.Errorf("readOnlyExecRoots: %w", err)
	}
	out.ExtraEnv = SanitizeBundleEnv(spec.ExtraEnv)
	return out, nil
}

func (s *Service) applySessionBundleEnv(rec SessionRecord, base []string) []string {
	if s == nil || rec.ID == "" {
		return base
	}
	bundle, err := s.ResolveSessionBundle(rec.ID, rec.WorktreePath)
	if err != nil || bundle.IsEmpty() {
		return base
	}
	return bundle.ApplyEnv(base)
}

// IsBlockedBundleEnv reports whether an ExtraEnv key/value pair is invalid,
// secret/credential-bearing, or dangerous for process execution confinement.
func IsBlockedBundleEnv(key, value string) bool {
	if key == "" || strings.TrimSpace(key) != key || !envVarKeyPattern.MatchString(key) {
		return true
	}
	if strings.ContainsAny(value, "\x00\r\n") {
		return true
	}
	upper := strings.ToUpper(key)
	if blockedBundleEnvExact[upper] {
		return true
	}
	for _, prefix := range blockedBundleEnvPrefixes {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	// Block all HOXI_* control/hook/gateway overrides while permitting non-secret HOXI_WORK_* metadata.
	if strings.HasPrefix(upper, "HOXI_") && !strings.HasPrefix(upper, "HOXI_WORK_") {
		return true
	}
	if isCredentialEnvName(upper) {
		return true
	}
	trimmedVal := strings.TrimSpace(value)
	if credentialValueRe.MatchString(trimmedVal) || bundleSecretValuePattern.MatchString(trimmedVal) {
		return true
	}
	return false
}

// SanitizeBundleEnv returns a new map with all secret, credential, invalid, or
// dangerous environment entries stripped, or nil when no safe entries remain.
func SanitizeBundleEnv(env map[string]string) map[string]string {
	if len(env) == 0 {
		return nil
	}
	out := make(map[string]string, len(env))
	for k, v := range env {
		if IsBlockedBundleEnv(k, v) {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// IsEmpty reports whether the bundle carries no app IDs, directories, or env entries.
func (b SessionBundleSpec) IsEmpty() bool {
	return len(b.AppIDs) == 0 &&
		len(b.ExtraSkillDirs) == 0 &&
		len(b.ExtraPathDirs) == 0 &&
		len(b.ExtraEnv) == 0 &&
		len(b.ReadOnlyExecRoots) == 0
}

// Clone returns a deep copy of SessionBundleSpec.
func (b SessionBundleSpec) Clone() SessionBundleSpec {
	var envCopy map[string]string
	if len(b.ExtraEnv) > 0 {
		envCopy = make(map[string]string, len(b.ExtraEnv))
		for k, v := range b.ExtraEnv {
			envCopy[k] = v
		}
	}
	return SessionBundleSpec{
		AppIDs:            append([]string(nil), b.AppIDs...),
		ExtraSkillDirs:    append([]string(nil), b.ExtraSkillDirs...),
		ExtraPathDirs:     append([]string(nil), b.ExtraPathDirs...),
		ExtraEnv:          envCopy,
		ReadOnlyExecRoots: append([]string(nil), b.ReadOnlyExecRoots...),
	}
}

// ApplyEnv returns a copy of base (NAME=value entries) with ExtraPathDirs
// appended to the tail of PATH (never shadowing system binaries) and sanitized
// ExtraEnv entries merged in deterministic key order.
func (b SessionBundleSpec) ApplyEnv(base []string) []string {
	out := make([]string, 0, len(base)+len(b.ExtraEnv)+1)
	indexByKey := make(map[string]int, len(base))
	for _, kv := range base {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			continue
		}
		indexByKey[name] = len(out)
		out = append(out, kv)
	}

	var safePathDirs []string
	for _, dir := range b.ExtraPathDirs {
		if dir == "" || strings.TrimSpace(dir) != dir || !filepath.IsAbs(dir) || strings.ContainsRune(dir, os.PathListSeparator) {
			continue
		}
		safePathDirs = append(safePathDirs, filepath.Clean(dir))
	}
	if len(safePathDirs) > 0 {
		pathKey := "PATH"
		pathIdx, hasPath := indexByKey[pathKey]
		if !hasPath {
			for existingKey, idx := range indexByKey {
				if strings.EqualFold(existingKey, "PATH") {
					pathKey = existingKey
					pathIdx = idx
					hasPath = true
					break
				}
			}
		}
		var currentEntries []string
		if hasPath {
			_, currentVal, _ := strings.Cut(out[pathIdx], "=")
			if currentVal != "" {
				currentEntries = filepath.SplitList(currentVal)
			}
		}
		merged := appendUniquePathEntries(currentEntries, safePathDirs)
		joined := strings.Join(merged, string(os.PathListSeparator))
		if hasPath {
			out[pathIdx] = pathKey + "=" + joined
		} else {
			indexByKey["PATH"] = len(out)
			out = append(out, "PATH="+joined)
		}
	}

	safeEnv := SanitizeBundleEnv(b.ExtraEnv)
	if len(safeEnv) > 0 {
		keys := make([]string, 0, len(safeEnv))
		for k := range safeEnv {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			entry := k + "=" + safeEnv[k]
			if idx, exists := indexByKey[k]; exists {
				out[idx] = entry
			} else {
				indexByKey[k] = len(out)
				out = append(out, entry)
			}
		}
	}
	return out
}

func validateBundleDirs(dirs []string, kind bundleDirKind) ([]string, error) {
	if len(dirs) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool, len(dirs))
	var out []string
	for _, raw := range dirs {
		resolved, err := validateBundleDirectory(raw, kind)
		if err != nil {
			return nil, err
		}
		normKey := normalizeComparePath(resolved)
		if !seen[normKey] {
			seen[normKey] = true
			out = append(out, resolved)
		}
	}
	return out, nil
}

func validateBundleDirectory(raw string, kind bundleDirKind) (string, error) {
	if raw == "" || strings.TrimSpace(raw) != raw {
		return "", errors.New("bundle path must be non-empty without surrounding whitespace")
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("bundle path contains control character: %q", raw)
		}
	}
	if strings.ContainsRune(raw, os.PathListSeparator) {
		return "", fmt.Errorf("bundle path must not contain path list separator %q: %q", os.PathListSeparator, raw)
	}
	if strings.HasPrefix(raw, "~") || !filepath.IsAbs(raw) {
		return "", fmt.Errorf("bundle path must be an absolute path: %q", raw)
	}
	for _, seg := range strings.Split(filepath.ToSlash(raw), "/") {
		if seg == ".." || seg == ".git" {
			return "", fmt.Errorf("bundle path contains disallowed segment: %q", raw)
		}
	}

	st, err := os.Stat(raw)
	if err != nil || !st.IsDir() {
		return "", fmt.Errorf("bundle path must be an existing directory: %q", raw)
	}
	resolved, err := filepath.EvalSymlinks(raw)
	if err != nil {
		return "", fmt.Errorf("resolve bundle path symlinks: %w", err)
	}
	resolved = filepath.Clean(resolved)
	if isFilesystemRoot(resolved) {
		return "", fmt.Errorf("bundle path refuses filesystem root: %q", raw)
	}
	for _, seg := range strings.Split(filepath.ToSlash(resolved), "/") {
		if seg == ".git" {
			return "", fmt.Errorf("bundle path resolves inside .git: %q", raw)
		}
	}
	if hoxipaths.IsProtectedWorkStatePath(raw) || hoxipaths.IsProtectedWorkStatePath(resolved) {
		return "", fmt.Errorf("bundle path refuses protected work state: %q", raw)
	}

	hoxiHome := resolveExistingAncestors(hoxipaths.Home())
	if hoxiHome != "" {
		if isBundleSamePath(hoxiHome, resolved) {
			return "", fmt.Errorf("bundle path refuses hoxi home root: %q", raw)
		}
		workDir := resolveExistingAncestors(hoxipaths.WorkDir())
		pkgRoot := resolveExistingAncestors(hoxipaths.WorkPackagesDir())
		rtRoot := resolveExistingAncestors(hoxipaths.WorkRuntimesDir())
		tcRoot := resolveExistingAncestors(hoxipaths.WorkToolchainsDir())

		cleanedRaw := canonicalizeAboveHoxiHome(raw, hoxipaths.Home(), hoxiHome)
		// Prevent symlinks inside ~/.hoxi/work/{packages,runtimes,toolchains} from escaping their roots or sibling app dirs.
		if pkgRoot != "" && isBundleSameOrSubpath(pkgRoot, cleanedRaw) {
			if !isBundleStrictSubpath(pkgRoot, resolved) {
				return "", fmt.Errorf("bundle package path escapes packages root: %q", raw)
			}
			appBucket := firstSubdirUnder(pkgRoot, cleanedRaw)
			if appBucket != "" && !isBundleSameOrSubpath(filepath.Join(pkgRoot, appBucket), resolved) {
				return "", fmt.Errorf("bundle package path escapes its app package directory: %q", raw)
			}
		}
		if rtRoot != "" && isBundleSameOrSubpath(rtRoot, cleanedRaw) {
			if !isBundleStrictSubpath(rtRoot, resolved) {
				return "", fmt.Errorf("bundle runtime path escapes runtimes root: %q", raw)
			}
			appBucket := firstSubdirUnder(rtRoot, cleanedRaw)
			if appBucket != "" && !isBundleSameOrSubpath(filepath.Join(rtRoot, appBucket), resolved) {
				return "", fmt.Errorf("bundle runtime path escapes its app runtime directory: %q", raw)
			}
		}
		if tcRoot != "" && isBundleSameOrSubpath(tcRoot, cleanedRaw) && !isBundleSameOrSubpath(tcRoot, resolved) {
			return "", fmt.Errorf("bundle toolchain path escapes toolchains root: %q", raw)
		}

		if isBundleSameOrSubpath(hoxiHome, resolved) {
			allowedUnderHoxi := isBundleStrictSubpath(pkgRoot, resolved) ||
				isBundleStrictSubpath(rtRoot, resolved) ||
				isBundleSameOrSubpath(tcRoot, resolved)
			if !allowedUnderHoxi && kind == bundleDirSkill {
				skillsDir := resolveExistingAncestors(filepath.Join(hoxiHome, "skills"))
				allowedUnderHoxi = isBundleSameOrSubpath(skillsDir, resolved)
			}
			if !allowedUnderHoxi && kind == bundleDirPath {
				binDir := resolveExistingAncestors(hoxipaths.BinDir())
				allowedUnderHoxi = isBundleSameOrSubpath(binDir, resolved)
			}
			if isBundleSamePath(workDir, resolved) || !allowedUnderHoxi {
				return "", fmt.Errorf("bundle path under ~/.hoxi is not an allowed bundle root: %q", raw)
			}
		}
	}

	userHomes := candidateUserHomes()
	insideUserHome := false
	for _, uh := range userHomes {
		if isBundleSameOrSubpath(resolved, uh) {
			return "", fmt.Errorf("bundle path refuses user home or ancestor: %q", raw)
		}
		if isBundleSameOrSubpath(uh, resolved) {
			insideUserHome = true
		}
		for _, secretRel := range []string{
			".ssh", ".aws", ".gnupg", ".kube", ".azure", ".codex", ".claude", ".gemini", ".docker", ".cargo",
			filepath.Join(".config", "gh"),
			filepath.Join(".config", "gcloud"),
			filepath.Join(".config", "opencode"),
			filepath.Join(".local", "share", "opencode"),
			filepath.Join("Library", "Keychains"),
		} {
			secretAbs := resolveExistingAncestors(filepath.Join(uh, secretRel))
			if isBundleSameOrSubpath(secretAbs, resolved) {
				return "", fmt.Errorf("bundle path refuses credential directory: %q", raw)
			}
		}
	}

	insideHoxiHome := hoxiHome != "" && isBundleSameOrSubpath(hoxiHome, resolved)
	if !insideUserHome && !insideHoxiHome {
		for _, tmpDir := range []string{os.TempDir(), "/tmp", "/private/tmp", "/var/tmp", "/private/var/folders"} {
			if strings.TrimSpace(tmpDir) == "" {
				continue
			}
			tr := resolveExistingAncestors(tmpDir)
			if tr != "" && isBundleSameOrSubpath(tr, resolved) {
				return "", fmt.Errorf("bundle path refuses temporary directory: %q", raw)
			}
		}
	}

	return resolved, nil
}

func candidateUserHomes() []string {
	var out []string
	seen := make(map[string]bool, 2)
	add := func(p string) {
		if strings.TrimSpace(p) == "" || !filepath.IsAbs(p) {
			return
		}
		res := resolveExistingAncestors(p)
		if res == "" || isFilesystemRoot(res) {
			return
		}
		key := normalizeComparePath(res)
		if !seen[key] {
			seen[key] = true
			out = append(out, res)
		}
	}
	add(os.Getenv("HOME"))
	if uh, err := os.UserHomeDir(); err == nil {
		add(uh)
	}
	return out
}

func resolveExistingAncestors(p string) string {
	trimmed := strings.TrimSpace(p)
	if trimmed == "" {
		return ""
	}
	cleaned := filepath.Clean(trimmed)
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		return filepath.Clean(resolved)
	}
	cur := cleaned
	var tail []string
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			return cleaned
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return filepath.Clean(resolved)
		}
	}
}

func canonicalizeAboveHoxiHome(raw, rawHoxiHome, resolvedHoxiHome string) string {
	cleaned := filepath.Clean(strings.TrimSpace(raw))
	if cleaned == "" || rawHoxiHome == "" || resolvedHoxiHome == "" {
		return cleaned
	}
	rawRoot := filepath.Clean(rawHoxiHome)
	if isBundleSameOrSubpath(rawRoot, cleaned) {
		if rel, err := filepath.Rel(rawRoot, cleaned); err == nil {
			if rel == "." {
				return resolvedHoxiHome
			}
			return filepath.Join(resolvedHoxiHome, rel)
		}
	}
	return cleaned
}

func firstSubdirUnder(root, target string) string {
	cr, ct := lexicalCompareKey(root), lexicalCompareKey(target)
	rel, err := filepath.Rel(cr, ct)
	if err != nil {
		return ""
	}
	slash := filepath.ToSlash(rel)
	if slash == "." || slash == ".." || strings.HasPrefix(slash, "../") {
		return ""
	}
	// Preserve original casing from target's relative path.
	origRel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	if err == nil {
		parts := strings.Split(filepath.ToSlash(origRel), "/")
		if len(parts) > 0 && parts[0] != "." && parts[0] != ".." {
			return parts[0]
		}
	}
	parts := strings.Split(slash, "/")
	if len(parts) > 0 && parts[0] != "." && parts[0] != ".." {
		return parts[0]
	}
	return ""
}

func lexicalCompareKey(p string) string {
	cleaned := filepath.Clean(strings.TrimSpace(p))
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		return strings.ToLower(cleaned)
	}
	return cleaned
}

func isFilesystemRoot(p string) bool {
	cleaned := filepath.Clean(p)
	return cleaned == string(filepath.Separator) || filepath.Dir(cleaned) == cleaned
}

func isBundleSamePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return lexicalCompareKey(a) == lexicalCompareKey(b)
}

func isBundleSameOrSubpath(base, target string) bool {
	if base == "" || target == "" {
		return false
	}
	cb, ct := lexicalCompareKey(base), lexicalCompareKey(target)
	rel, err := filepath.Rel(cb, ct)
	if err != nil {
		return false
	}
	slash := filepath.ToSlash(rel)
	return slash == "." || (slash != ".." && !strings.HasPrefix(slash, "../") && !filepath.IsAbs(rel))
}

func isBundleStrictSubpath(base, target string) bool {
	return isBundleSameOrSubpath(base, target) && !isBundleSamePath(base, target)
}

func appendUniquePathEntries(existing, extras []string) []string {
	seen := make(map[string]bool, len(existing)+len(extras))
	out := make([]string, 0, len(existing)+len(extras))
	for _, list := range [][]string{existing, extras} {
		for _, item := range list {
			trimmed := strings.TrimSpace(item)
			if trimmed == "" {
				continue
			}
			key := normalizeComparePath(trimmed)
			if !seen[key] {
				seen[key] = true
				out = append(out, trimmed)
			}
		}
	}
	return out
}
