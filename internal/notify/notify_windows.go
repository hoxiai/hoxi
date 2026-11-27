//go:build windows

package notify

import (
	"context"
	"os"
	"os/exec"
	"time"

	"github.com/hoxiai/hoxi/internal/subproc"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const windowsToastScript = `
$ErrorActionPreference = 'Stop'
$title = [string]$env:HOXI_NOTIFY_TITLE
$body  = [string]$env:HOXI_NOTIFY_BODY
try {
  [Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
  [Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime] | Out-Null
  $xml = [Windows.Data.Xml.Dom.XmlDocument]::new()
  $xml.LoadXml($env:HOXI_NOTIFY_XML)
  $toast = [Windows.UI.Notifications.ToastNotification]::new($xml)
  $notifier = [Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('Hoxi')
  $notifier.Show($toast)
} catch {
  Add-Type -AssemblyName System.Windows.Forms
  Add-Type -AssemblyName System.Drawing
  $ni = New-Object System.Windows.Forms.NotifyIcon
  $ni.Icon = [System.Drawing.SystemIcons]::Application
  $ni.BalloonTipTitle = $title
  $ni.BalloonTipText = if ($body) { $body } else { $title }
  $ni.Visible = $true
  $ni.ShowBalloonTip(5000)
  Start-Sleep -Milliseconds 250
  $ni.Dispose()
}
`

func platformStart() {}

func platformStatus() PermissionStatus {
	if _, err := exec.LookPath("powershell.exe"); err == nil {
		return PermissionAuthorized
	}
	if _, err := exec.LookPath("powershell"); err == nil {
		return PermissionAuthorized
	}
	return PermissionUnavailable
}

func platformRequestPermission() PermissionStatus {
	return platformStatus()
}

// A session link must not send a portable build's session to a differently
// installed Hoxi (or launch an unrelated stale protocol handler).
func windowsToastCanOpenSession() bool {
	key, err := registry.OpenKey(registry.CLASSES_ROOT, `hoxi\shell\open\command`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	command, _, err := key.GetStringValue("")
	if err != nil {
		return false
	}
	exe, err := os.Executable()
	return err == nil && windowsToastProtocolTargetsExecutable(command, exe)
}

func windowsToastProtocolTargetsExecutable(command, exe string) bool {
	args, err := windows.DecomposeCommandLine(command)
	if err != nil || len(args) != 2 || args[1] != "%1" {
		return false
	}
	self, err := os.Stat(exe)
	if err != nil {
		return false
	}
	handler, err := os.Stat(args[0])
	return err == nil && os.SameFile(self, handler)
}

func platformShow(req Request) error {
	psBin, err := exec.LookPath("powershell.exe")
	if err != nil {
		psBin, err = exec.LookPath("powershell")
		if err != nil {
			return ErrUnavailable
		}
	}
	payload, err := windowsToastXML(req, windowsToastCanOpenSession())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	cmd := subproc.CommandContext(ctx, psBin, "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", windowsToastScript)
	cmd.Env = append(os.Environ(),
		"HOXI_NOTIFY_TITLE="+req.Title,
		"HOXI_NOTIFY_BODY="+req.Body,
		"HOXI_NOTIFY_XML="+payload,
	)
	if err := cmd.Run(); err != nil {
		return ErrUnavailable
	}
	return nil
}
