package updater

import (
	"os"
	"path/filepath"
	"strings"
)

// ManagedInstall describes an external package manager or portable marker
// governing the current Hoxi installation so in-app self-update does not
// clobber package-manager-owned directories.
type ManagedInstall struct {
	Manager        string `json:"manager,omitempty"`
	UpgradeCommand string `json:"upgradeCommand,omitempty"`
}

// DetectManagedInstall inspects an executable path (before or after symlink
// resolution) and optional sibling files to identify Homebrew, Nix, Scoop, or
// WinGet managed installations.
func DetectManagedInstall(exePath string) ManagedInstall {
	cleaned := strings.TrimSpace(exePath)
	if cleaned == "" {
		return ManagedInstall{}
	}
	slash := strings.ToLower(strings.ReplaceAll(cleaned, `\`, `/`))

	switch {
	case strings.Contains(slash, "/caskroom/hoxi/") || strings.Contains(slash, "/caskroom/hoxi-app/"):
		return ManagedInstall{
			Manager:        "homebrew",
			UpgradeCommand: "brew upgrade --cask hoxi",
		}
	case strings.Contains(slash, "/cellar/hoxi/"):
		return ManagedInstall{
			Manager:        "homebrew",
			UpgradeCommand: "brew upgrade hoxi",
		}
	case strings.HasPrefix(slash, "/nix/store/"):
		return ManagedInstall{
			Manager:        "nix",
			UpgradeCommand: "nix profile upgrade hoxi",
		}
	case strings.Contains(slash, "/scoop/apps/hoxi/"):
		return ManagedInstall{
			Manager:        "scoop",
			UpgradeCommand: "scoop update hoxi",
		}
	case strings.Contains(slash, "/microsoft/winget/packages/"):
		return ManagedInstall{
			Manager:        "winget",
			UpgradeCommand: "winget upgrade Hoxi.Hoxi",
		}
	}
	return ManagedInstall{}
}

// CurrentManagedInstall resolves the current process executable (checking both
// raw and symlink-resolved paths) to detect package-manager ownership.
func CurrentManagedInstall() ManagedInstall {
	exe, err := os.Executable()
	if err != nil || strings.TrimSpace(exe) == "" {
		return ManagedInstall{}
	}
	if m := DetectManagedInstall(exe); m.Manager != "" {
		return m
	}
	if realExe, evalErr := filepath.EvalSymlinks(exe); evalErr == nil && realExe != "" {
		if m := DetectManagedInstall(realExe); m.Manager != "" {
			return m
		}
	}
	return ManagedInstall{}
}
