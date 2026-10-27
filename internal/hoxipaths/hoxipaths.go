// Package hoxipaths is the single source of truth for Hoxi's per-user state
// locations under ~/.hoxi and the private OpenCode engine directories.
//
// It is a foundation package: it depends only on the standard library and
// must never import a business domain package.
//
// Environment overrides, highest priority first:
//
//	Engine database (hoxi.db): OPENCODE_DB > HOXI_DB_PATH > ~/.hoxi/hoxi.db
//	Engine data dir:           HOXI_ENGINE_DATA_DIR > ~/.hoxi/engine-data
//	Engine state dir:          HOXI_ENGINE_DATA_DIR/state > ~/.hoxi/engine-state
//
// OPENCODE_DB wins because the supervised `opencode serve` child inherits it
// verbatim; Hoxi only injects its own path when the variable is unset.
package hoxipaths

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	// DefaultServerBaseURL is the canonical Hoxi cloud endpoint when HOXI_SERVER_URL is unset.
	DefaultServerBaseURL = "https://hoxi.ai"
	// DefaultDevServerBaseURL is the local hoxi.ai endpoint (apay dev server on 127.0.0.1:5400)
	// preferred by Hoxi Dev when running locally.
	DefaultDevServerBaseURL = "http://127.0.0.1:5400"
	// DefaultDevServerHostPort is the loopback host:port of the local hoxi.ai dev server.
	DefaultDevServerHostPort = "127.0.0.1:5400"
	// DefaultReleaseBridgeAddr is the default loopback gateway address for release builds.
	DefaultReleaseBridgeAddr = "127.0.0.1:5410"
	// DefaultDevBridgeAddr is the default loopback gateway address for dev builds.
	DefaultDevBridgeAddr = "127.0.0.1:5420"
	// GrokCLIBridgePath is the OpenAI-compatible Grok CLI bridge every Hoxi
	// gateway serves. ~/.hoxi/models.json is shared by all Hoxi processes, so it
	// stores this path on the release address only; each process points its own
	// OpenCode and internal LLM calls at its own gateway at runtime.
	GrokCLIBridgePath = "/api/hoxi/bridge/grok-cli/v1"
	// DefaultLANPairingPort is the LAN port the mobile app pairs over when it
	// scans the desktop's local-network QR code. The desktop listens on it,
	// gated by the pairing token and restricted to local-network sources.
	DefaultLANPairingPort = 5417
	// DefaultRemoteIngressAddr is the loopback address a local reverse proxy
	// (for example cloudflared for a Cloudflare Tunnel) forwards remote requests
	// to. It must stay on loopback: the pairing token still gates every request.
	DefaultRemoteIngressAddr = "127.0.0.1:5418"
)

var protectedStateFiles = []string{
	"models.json",
	"auto-pools.json",
	"workspaces.json",
	"auth.json",
	"device-id",
	"telemetry.json",
	"tunnel.json",
	"tunnel-dev.json",
	"pairing.json",
	"cloudflared.json",
	"approval-audit.json",
	"approval-policy.json",
	"relay-keys.json",
	"git-attribution.json",
	"work-apps.json",
	// The reclaim ledger tells the next Hoxi start which processes to signal
	// and which directories to delete; it must not be writable by agents.
	"reclaim.json",
	"reclaim.json.lock",
}

// Home returns ~/.hoxi (or $HOXI_HOME when set), or "" when the user home directory is unknown.
func Home() string {
	if custom := env("HOXI_HOME"); custom != "" {
		return filepath.Clean(custom)
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".hoxi")
}

// Join returns filepath.Join(Home(), elem...), or "" when Home() is unknown.
func Join(elem ...string) string {
	return join(Home(), elem...)
}

// ServerBaseURL returns the configured Hoxi server base URL without a trailing slash.
func ServerBaseURL() string {
	if custom := strings.TrimRight(env("HOXI_SERVER_URL"), "/"); custom != "" {
		return custom
	}
	return DefaultServerBaseURL
}

// NormalizedPlatform maps runtime.GOOS values to Hoxi API platform names.
func NormalizedPlatform(goos string) string {
	if goos == "darwin" {
		return "mac"
	}
	return goos
}

// NormalizedArch maps runtime.GOARCH values to Hoxi API architecture names.
func NormalizedArch(goarch string) string {
	if goarch == "amd64" {
		return "x64"
	}
	return goarch
}

// ProtectedStateFiles returns the filenames under ~/.hoxi that sandboxed child
// processes and untrusted file readers must not overwrite or exfiltrate.
func ProtectedStateFiles() []string {
	out := make([]string, len(protectedStateFiles))
	copy(out, protectedStateFiles)
	return out
}

// IsProtectedStateFile reports whether baseName is a protected ~/.hoxi state file.
func IsProtectedStateFile(baseName string) bool {
	lower := strings.ToLower(strings.TrimSpace(baseName))
	for _, f := range protectedStateFiles {
		if lower == f {
			return true
		}
	}
	return false
}

// TunnelConfigPath returns the GoPanel tunnel credential file. Dev and release
// builds pair separately so both can run without evicting each other's tunnel.
func TunnelConfigPath(variant string) string {
	if variant == "dev" {
		return Join("tunnel-dev.json")
	}
	return Join("tunnel.json")
}

// PairingTokenPath returns ~/.hoxi/pairing.json, the stable token encoded in
// the desktop's LAN pairing QR code. It is written 0600 and never leaves the
// machine except inside the QR the user chooses to display.
func PairingTokenPath() string {
	return Join("pairing.json")
}

// ReclaimLedgerPath returns ~/.hoxi/reclaim.json, the shared record of the
// headless Chrome processes, profiles and plugin overlays each running Hoxi
// process owns, so the next start can clean up after a crash.
func ReclaimLedgerPath() string {
	return Join("reclaim.json")
}

// CloudflaredConfigPath returns ~/.hoxi/cloudflared.json, the Cloudflare Tunnel
// connector settings (enabled flag, connector token, hostname). Written 0600;
// the token is never returned by the HTTP API.
func CloudflaredConfigPath() string {
	return Join("cloudflared.json")
}

// ApprovalPolicyPath returns ~/.hoxi/approval-policy.json, the approval mode
// and per-tool overrides every runtime's approval gate reads.
func ApprovalPolicyPath() string {
	return Join("approval-policy.json")
}

// BinDir returns ~/.hoxi/bin, where Hoxi installs its managed binaries.
func BinDir() string {
	return join(Home(), "bin")
}

// DefaultWorkspaceDir returns ~/.hoxi/workspaces/default, the fallback local
// workspace directory used when a session does not target a specific repository.
func DefaultWorkspaceDir() string {
	return join(Home(), "workspaces", "default")
}

// EnsureDefaultWorkspaceDir creates DefaultWorkspaceDir (plus starter operations
// subdirectories and README.md when absent) if needed and returns it.
func EnsureDefaultWorkspaceDir() string {
	dir := DefaultWorkspaceDir()
	if dir == "" {
		return ""
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	for _, sub := range []string{"docs", "exports", "campaigns", "reports"} {
		_ = os.MkdirAll(filepath.Join(dir, sub), 0o755)
	}
	readmePath := filepath.Join(dir, "README.md")
	if _, err := os.Stat(readmePath); os.IsNotExist(err) {
		_ = os.WriteFile(readmePath, []byte(defaultWorkspaceReadme), 0o644)
	}
	return dir
}

const defaultWorkspaceReadme = `# Hoxi Default Operations Workspace

This directory (~/.hoxi/workspaces/default) is Hoxi's built-in workspace for
daily e-commerce operations, content creation, and data analysis when no git
repository is selected.

## Recommended Folders

- docs/      - Brand guidelines, SOPs, product copy, and campaign briefs
- exports/   - Exported CSV/XLSX reports (Shopify, TikTok Shop, Amazon, Ads)
- campaigns/ - Ad creatives, influencer seeding trackers, and launch assets
- reports/   - Weekly/monthly operations diagnostics and audit summaries
`

// DBPath returns the SQLite database the managed OpenCode engine writes to.
func DBPath() string {
	if custom := env("OPENCODE_DB"); custom != "" {
		return filepath.Clean(custom)
	}
	if custom := env("HOXI_DB_PATH"); custom != "" {
		return filepath.Clean(custom)
	}
	return join(Home(), "hoxi.db")
}

// DBFiles returns the database path plus its SQLite sidecar files.
func DBFiles() []string {
	db := DBPath()
	if db == "" {
		return nil
	}
	return []string{db, db + "-wal", db + "-shm", db + "-journal"}
}

// EngineDataDir returns the XDG data home Hoxi hands to the managed engine.
func EngineDataDir() string {
	if custom := env("HOXI_ENGINE_DATA_DIR"); custom != "" {
		return filepath.Clean(custom)
	}
	return join(Home(), "engine-data")
}

// EngineStateDir returns the XDG state home Hoxi hands to the managed engine.
func EngineStateDir() string {
	if custom := env("HOXI_ENGINE_DATA_DIR"); custom != "" {
		return filepath.Join(filepath.Clean(custom), "state")
	}
	return join(Home(), "engine-state")
}

// Per-project memory lives in the project's own .hoxi directory, never in the
// upstream .opencode config directory. Creating .opencode/ in a project makes
// OpenCode treat it as a config root and install @opencode-ai/plugin into it,
// which is why Hoxi keeps its state under .hoxi/ and only reads the legacy
// .opencode/memory location for sessions created before the move.
const (
	// ProjectMemoryDirName is the project-level Hoxi state directory.
	ProjectMemoryDirName = ".hoxi"
	// ProjectMemoryDataDirName is the memory subdirectory under ProjectMemoryDirName.
	ProjectMemoryDataDirName = "memory"
	// LegacyOpenCodeDirName is the upstream OpenCode config directory name.
	LegacyOpenCodeDirName = ".opencode"
)

// ProjectMemoryDir returns <projectRoot>/.hoxi/memory.
func ProjectMemoryDir(projectRoot string) string {
	if strings.TrimSpace(projectRoot) == "" {
		return ""
	}
	return filepath.Join(projectRoot, ProjectMemoryDirName, ProjectMemoryDataDirName)
}

// LegacyProjectMemoryDir returns <projectRoot>/.opencode/memory, the location
// Hoxi used before memory moved under .hoxi. Read-only compatibility only.
func LegacyProjectMemoryDir(projectRoot string) string {
	if strings.TrimSpace(projectRoot) == "" {
		return ""
	}
	return filepath.Join(projectRoot, LegacyOpenCodeDirName, ProjectMemoryDataDirName)
}

// ProjectMemoryDirs returns the memory roots to consult for a project, current
// location first and the legacy one second.
func ProjectMemoryDirs(projectRoot string) []string {
	current := ProjectMemoryDir(projectRoot)
	legacy := LegacyProjectMemoryDir(projectRoot)
	if current == "" {
		return nil
	}
	return []string{current, legacy}
}

// ProjectMemoryFile returns the project-level memory file for projectRoot.
func ProjectMemoryFile(projectRoot string) string {
	return join(ProjectMemoryDir(projectRoot), "project.md")
}

// LegacyProjectMemoryFile returns the legacy project-level memory file.
func LegacyProjectMemoryFile(projectRoot string) string {
	return join(LegacyProjectMemoryDir(projectRoot), "project.md")
}

// ProjectSessionMemoryFile returns the session memory file for sessionID.
func ProjectSessionMemoryFile(projectRoot, sessionID string) string {
	return join(ProjectMemoryDir(projectRoot), "sessions", sessionID+".md")
}

// LegacyProjectSessionMemoryFile returns the legacy session memory file.
func LegacyProjectSessionMemoryFile(projectRoot, sessionID string) string {
	return join(LegacyProjectMemoryDir(projectRoot), "sessions", sessionID+".md")
}

// ProjectMemoryRelPath returns the current, project-relative memory directory.
func ProjectMemoryRelPath() string {
	return filepath.Join(ProjectMemoryDirName, ProjectMemoryDataDirName)
}

// ProjectRootForWorktree returns the project root owning an isolated session
// worktree (<root>/.hoxi/worktrees/<session-id>), or "" when dir is not one.
// Memory and other per-project state must be addressed at the project root so
// removing a worktree never discards them.
func ProjectRootForWorktree(dir string) string {
	cleaned := filepath.Clean(strings.TrimSpace(dir))
	if cleaned == "" || !filepath.IsAbs(cleaned) {
		return ""
	}
	slash := filepath.ToSlash(cleaned)
	marker := "/" + filepath.ToSlash(ProjectMemoryDirName) + "/worktrees/"
	idx := strings.LastIndex(slash, marker)
	if idx < 0 {
		return ""
	}
	rest := slash[idx+len(marker):]
	if rest == "" || strings.Contains(rest, "/") {
		return ""
	}
	return filepath.FromSlash(slash[:idx])
}

// EngineOpenCodeDir returns the engine's OpenCode data directory, which holds
// the engine-scoped auth.json.
func EngineOpenCodeDir() string {
	return join(EngineDataDir(), "opencode")
}

// EngineAuthPath returns the engine-scoped OpenCode auth.json.
func EngineAuthPath() string {
	return join(EngineOpenCodeDir(), "auth.json")
}

// EngineLegacyDBPath is the engine database from before hoxi.db existed.
func EngineLegacyDBPath() string {
	return join(EngineOpenCodeDir(), "opencode.db")
}

func env(name string) string {
	return strings.TrimSpace(os.Getenv(name))
}

func join(base string, elem ...string) string {
	if base == "" {
		return ""
	}
	return filepath.Join(append([]string{base}, elem...)...)
}
