package computer

import (
	"os"
	"path"
	"strings"
)

// blockedBundleIDs are macOS apps that are never granted: Hoxi itself, system
// security surfaces, and apps that run arbitrary shell commands or scripts.
// Typing into a terminal would bypass the shell approval gate and sandbox.
var blockedBundleIDs = map[string]bool{
	"com.apple.systempreferences": true,
	"com.apple.systemsettings":    true,
	"com.apple.securityagent":     true,
	"com.apple.loginwindow":       true,
	"com.apple.keychainaccess":    true,
	"com.apple.terminal":          true,
	"com.googlecode.iterm2":       true,
	"com.mitchellh.ghostty":       true,
	"net.kovidgoyal.kitty":        true,
	"org.alacritty":               true,
	"io.alacritty":                true,
	"com.github.wez.wezterm":      true,
	"co.zeit.hyper":               true,
	"org.tabby":                   true,
	"com.apple.scripteditor2":     true,
	"com.apple.automator":         true,
	"com.apple.shortcuts":         true,
}

// blockedBundlePrefixes cover families whose bundle IDs vary by channel.
var blockedBundlePrefixes = []string{"ai.hoxi.", "dev.warp."}

// blockedExecutables are Windows (and macOS helper) executables that are never
// granted: Hoxi, the secure desktop, and shells, terminals or script hosts.
var blockedExecutables = map[string]bool{
	"hoxi.exe": true, "hoxi dev.exe": true,
	"consent.exe": true, "logonui.exe": true, "credentialuibroker.exe": true, "systemsettings.exe": true,
	"windowsterminal.exe": true, "wt.exe": true, "openconsole.exe": true, "conhost.exe": true,
	"cmd.exe": true, "powershell.exe": true, "pwsh.exe": true, "powershell_ise.exe": true,
	"wsl.exe": true, "wslhost.exe": true, "bash.exe": true, "mintty.exe": true,
	"alacritty.exe": true, "wezterm-gui.exe": true, "tabby.exe": true, "hyper.exe": true, "warp.exe": true,
	"regedit.exe": true, "taskmgr.exe": true, "mmc.exe": true,
}

// blockedNameParts match password managers whatever their bundle ID.
var blockedNameParts = []string{"1password", "bitwarden"}

func BlockedApp(app App) bool {
	if app.PID == os.Getpid() {
		return true
	}
	id := strings.ToLower(app.BundleID)
	name := strings.ToLower(app.Name)
	file := strings.ToLower(strings.ReplaceAll(app.Path, "\\", "/"))
	if blockedBundleIDs[id] || blockedExecutables[path.Base(file)] {
		return true
	}
	for _, prefix := range blockedBundlePrefixes {
		if strings.HasPrefix(id, prefix) {
			return true
		}
	}
	for _, part := range blockedNameParts {
		if strings.Contains(id, part) || strings.Contains(name, part) {
			return true
		}
	}
	return name == "hoxi" || name == "hoxi dev" || strings.Contains(file, "/hoxi.app/") || strings.Contains(file, "/hoxi dev.app/")
}
