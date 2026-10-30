package computer

import "testing"

// Shells, terminals and script hosts would let typed text run any command
// outside the shell approval gate and the sandbox, so they are never granted.
func TestBlockedAppRejectsTerminalsAndScriptHosts(t *testing.T) {
	for _, app := range []App{
		{Name: "Terminal", BundleID: "com.apple.Terminal", Path: "/System/Applications/Utilities/Terminal.app/Contents/MacOS/Terminal"},
		{Name: "iTerm2", BundleID: "com.googlecode.iterm2"},
		{Name: "Warp", BundleID: "dev.warp.Warp-Stable"},
		{Name: "Ghostty", BundleID: "com.mitchellh.ghostty"},
		{Name: "Script Editor", BundleID: "com.apple.ScriptEditor2"},
		{Name: "Shortcuts", BundleID: "com.apple.shortcuts"},
		{Name: "System Settings", BundleID: "com.apple.systempreferences"},
		{Name: "Hoxi Dev", Path: "/Applications/Hoxi Dev.app/Contents/MacOS/Hoxi Dev"},
		{Name: "1Password 8", BundleID: "com.1password.1password"},
		{Name: "Windows Terminal", Path: `C:\Program Files\WindowsApps\Microsoft.WindowsTerminal\WindowsTerminal.exe`},
		{Name: "PowerShell", Path: `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`},
		{Name: "pwsh", Path: `C:\Program Files\PowerShell\7\pwsh.exe`},
		{Name: "Command Prompt", Path: `C:\Windows\System32\cmd.exe`},
		{Name: "Task Manager", Path: `C:\Windows\System32\Taskmgr.exe`},
	} {
		if !BlockedApp(app) {
			t.Errorf("app allowed: %+v", app)
		}
	}
	for _, app := range []App{
		{Name: "Calculator", BundleID: "com.apple.calculator", Path: "/System/Applications/Calculator.app/Contents/MacOS/Calculator"},
		{Name: "Safari", BundleID: "com.apple.Safari"},
		{Name: "Notepad", Path: `C:\Windows\System32\notepad.exe`},
	} {
		if BlockedApp(app) {
			t.Errorf("ordinary app blocked: %+v", app)
		}
	}
}
