package sandbox

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/subproc"
)

// OnDemandProfile confines one shell command that Hoxi would otherwise have
// had to ask about only because it could not read the command. It is
// narrower than a sandboxed session:
//
//   - writes: the workspace, temp directories and build caches. Never any
//     .git directory (history stays recoverable), Hoxi state, other agents'
//     state directories, or the OpenCode plugin and package caches that later
//     run outside the sandbox;
//   - reads: everything except credential directories, other agents' sign-in
//     tokens, .env files anywhere, and private key files;
//   - network: only servers the command starts itself. Every loopback port
//     already in use when the profile is written (local proxies, other
//     agents' and IDEs' servers, Hoxi's and OpenCode's control APIs, plus
//     denyPorts) is denied, and Unix sockets are reachable only inside the
//     workspace, so nothing it reads can leave the machine or the sandbox.
func OnDemandProfile(workspace string, denyPorts []int) (Profile, error) {
	workspace, err := canonicalDirectory(workspace)
	if err != nil {
		return Profile{}, err
	}
	if err := checkOnDemandWorkspace(workspace); err != nil {
		return Profile{}, err
	}
	roots := []string{workspace, os.TempDir(), "/private/tmp", "/private/var/folders"}
	roots = append(roots, buildCacheDirs()...)
	allow := canonicalSubtrees(roots)
	allow = append(allow, PathRule{Path: "/dev", Kind: PathSubtree})
	var deny []PathRule
	for _, dir := range []string{DefaultRoot(), hoxipaths.Home(), hoxipaths.BinDir(), hoxipaths.EngineDataDir(), hoxipaths.EngineStateDir()} {
		if dir != "" && dir != string(filepath.Separator) {
			deny = append(deny, PathRule{Path: resolvePathSymlinks(dir), Kind: PathSubtree})
		}
	}
	deny = append(deny, protectedStateRules()...)
	deny = append(deny, laterExecutionCacheRules()...)
	tcpInUse, udpInUse, err := ListeningLocalPorts()
	if err != nil {
		return Profile{}, fmt.Errorf("%w: %v", errNoPortSnapshot, err)
	}
	ports := append(append([]int(nil), denyPorts...), LocalProxyPorts()...)
	denyRead := append(secretReadRules(workspace), anywhereSecretFileRules()...)
	denyRead = append(denyRead, agentCredentialRules()...)
	return Profile{
		Filesystem: FilesystemProfile{
			AllowWrite:         allow,
			DenyWrite:          deny,
			DenyRead:           denyRead,
			DenyNames:          []string{".git"},
			TemporaryDirectory: "/private/tmp",
		},
		Network: NetworkProfile{
			Mode:              NetworkLocal,
			AllowUnixSockets:  []PathRule{{Path: workspace, Kind: PathSubtree}},
			DenySockets:       credentialSocketRules(),
			DenyLoopbackPorts: uniquePorts(ports),
			DenyLoopbackTCP:   tcpInUse,
			DenyLoopbackUDP:   udpInUse,
		},
	}, nil
}

// checkOnDemandWorkspace refuses a workspace that would make the profile's
// writable root meaningless (the filesystem root, the home directory or one
// of its ancestors) or reach credentials and app state (a dot directory or
// Library under home, such as ~/.ssh or ~/.hoxi).
func checkOnDemandWorkspace(workspace string) error {
	if workspace == string(filepath.Separator) {
		return errors.New("on-demand sandbox refuses the filesystem root")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return errors.New("on-demand sandbox needs a home directory")
	}
	home = resolvePathSymlinks(home)
	if rel, err := filepath.Rel(workspace, home); err == nil && (rel == "." || !strings.HasPrefix(rel, "..")) {
		return errors.New("on-demand sandbox refuses the home directory or its ancestors")
	}
	if rel, err := filepath.Rel(home, workspace); err == nil && !strings.HasPrefix(rel, "..") {
		first := strings.Split(filepath.ToSlash(rel), "/")[0]
		if strings.HasPrefix(first, ".") || first == "Library" {
			return errors.New("on-demand sandbox refuses a workspace inside a home dot directory or Library")
		}
	}
	return nil
}

func canonicalSubtrees(roots []string) []PathRule {
	var rules []PathRule
	seen := make(map[string]bool)
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		canonical, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		canonical = resolvePathSymlinks(canonical)
		if seen[canonical] {
			continue
		}
		seen[canonical] = true
		rules = append(rules, PathRule{Path: canonical, Kind: PathSubtree})
	}
	return rules
}

// laterExecutionCacheRules are writable-looking locations whose contents run
// later outside any sandbox: OpenCode's plugin and provider packages, npx's
// package cache, and the plugin files Hoxi hands the OpenCode daemon.
func laterExecutionCacheRules() []PathRule {
	rules := []PathRule{{Path: `/hoxi-opencode-plugin-[^/]*(/|$)`, Kind: PathRegex}}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return rules
	}
	home = resolvePathSymlinks(home)
	for _, dir := range []string{filepath.Join(".cache", "opencode"), filepath.Join(".npm", "_npx")} {
		rules = append(rules, PathRule{Path: filepath.Join(home, dir), Kind: PathSubtree})
	}
	return rules
}

// anywhereSecretFileRules deny secret files by name wherever they sit, not
// only under the workspace: every .env variant and private key or credential
// files.
func anywhereSecretFileRules() []PathRule {
	return []PathRule{
		{Path: `/\.env(\.[^/]*)?$`, Kind: PathRegex},
		{Path: `/\.envrc$`, Kind: PathRegex},
		{Path: `/id_(rsa|dsa|ecdsa|ed25519)$`, Kind: PathRegex},
		{Path: `\.(key|p12|pfx|jks|keystore|kdbx)$`, Kind: PathRegex},
		{Path: `/\.(netrc|npmrc|pypirc|git-credentials)$`, Kind: PathRegex},
	}
}

// buildCacheDirs are the package and build caches a confined build needs.
func buildCacheDirs() []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	dirs := []string{
		filepath.Join(home, ".cache"),
		filepath.Join(home, ".npm"),
		filepath.Join(home, "go", "pkg"),
		filepath.Join(home, "Library", "Caches"),
	}
	for _, envKey := range []string{"GOCACHE", "GOMODCACHE", "npm_config_cache"} {
		if v := strings.TrimSpace(os.Getenv(envKey)); v != "" && filepath.IsAbs(v) {
			dirs = append(dirs, v)
		}
	}
	return dirs
}

var (
	proxyPortCache struct {
		at    time.Time
		ports []int
	}
	proxyPortCacheMu sync.Mutex
	scutilPortRe     = regexp.MustCompile(`(?m)^\s*(?:HTTP|HTTPS|SOCKS)Port\s*:\s*(\d+)\s*$`)
)

// LocalProxyPorts are loopback ports of the proxies this machine uses to
// reach the internet (HTTP_PROXY / HTTPS_PROXY / ALL_PROXY and the macOS
// system proxy). A loopback-only sandbox must refuse them too.
func LocalProxyPorts() []int {
	proxyPortCacheMu.Lock()
	defer proxyPortCacheMu.Unlock()
	if !proxyPortCache.at.IsZero() && time.Since(proxyPortCache.at) < 30*time.Second {
		return append([]int(nil), proxyPortCache.ports...)
	}
	var ports []int
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		if p := loopbackPort(os.Getenv(key)); p > 0 {
			ports = append(ports, p)
		}
	}
	if out, err := subproc.Command("/usr/sbin/scutil", "--proxy").Output(); err == nil {
		for _, m := range scutilPortRe.FindAllSubmatch(out, -1) {
			if p, err := strconv.Atoi(string(m[1])); err == nil && p > 0 && p <= 65535 {
				ports = append(ports, p)
			}
		}
	}
	ports = uniquePorts(ports)
	proxyPortCache.at, proxyPortCache.ports = time.Now(), ports
	return append([]int(nil), ports...)
}

// loopbackPort returns the port of a proxy URL that points at this machine.
func loopbackPort(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return 0
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); !(host == "localhost" || (ip != nil && ip.IsLoopback())) {
		return 0
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port <= 0 || port > 65535 {
		return 0
	}
	return port
}

// PortOf returns the TCP port of an http(s) URL on this machine, or 0.
func PortOf(rawURL string) int {
	return loopbackPort(rawURL)
}

func uniquePorts(ports []int) []int {
	seen := make(map[int]bool)
	var out []int
	for _, p := range ports {
		if p > 0 && p <= 65535 && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Ints(out)
	return out
}

// onDemandWritten records the profiles this process wrote, by path, with the
// digest of their text. A wrapped command counts as confined only when its
// profile is one of them and is unchanged on disk.
var onDemandWritten sync.Map // path -> [32]byte

// OnDemandProfilePath is where the on-demand profile for workspace lives.
func (m *Manager) OnDemandProfilePath(workspace string) (string, error) {
	canonical, err := canonicalDirectory(workspace)
	if err != nil {
		return "", err
	}
	return filepath.Join(m.root, "profiles", "on-demand-"+hash(canonical)+".sb"), nil
}

// WriteOnDemand renders the on-demand profile for workspace and returns its
// path. The file is rewritten only when its text changed.
func (m *Manager) WriteOnDemand(workspace string, denyPorts []int) (string, error) {
	profile, err := OnDemandProfile(workspace, denyPorts)
	if err != nil {
		return "", err
	}
	text, err := ProfileText(profile)
	if err != nil {
		return "", err
	}
	path, err := m.OnDemandProfilePath(workspace)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if current, err := os.ReadFile(path); err != nil || !bytes.Equal(current, []byte(text)) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return "", fmt.Errorf("create sandbox profile directory: %w", err)
		}
		if err := writeAtomic(path, []byte(text), 0o600); err != nil {
			return "", fmt.Errorf("write on-demand sandbox profile: %w", err)
		}
	}
	onDemandWritten.Store(path, sha256.Sum256([]byte(text)))
	return path, nil
}

// OnDemandTrusted reports whether profilePath is the on-demand profile this
// process wrote for workspace and is still unchanged on disk.
func (m *Manager) OnDemandTrusted(profilePath, workspace string) bool {
	want, err := m.OnDemandProfilePath(workspace)
	if err != nil || profilePath != want {
		return false
	}
	digest, ok := onDemandWritten.Load(want)
	if !ok {
		return false
	}
	m.mu.Lock()
	current, err := os.ReadFile(want)
	m.mu.Unlock()
	return err == nil && sha256.Sum256(current) == digest.([32]byte)
}
