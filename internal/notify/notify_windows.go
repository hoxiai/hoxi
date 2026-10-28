//go:build windows

package notify

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"time"
)

const windowsToastScript = `
$ErrorActionPreference = 'Stop'
$title = [string]$env:HOXI_NOTIFY_TITLE
$body  = [string]$env:HOXI_NOTIFY_BODY
try {
  [Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
  [Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime] | Out-Null
  $template = [Windows.UI.Notifications.ToastTemplateType]::ToastText02
  $xml = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent($template)
  $nodes = $xml.GetElementsByTagName('text')
  if ($nodes.Count -ge 1) { [void]$nodes.Item(0).AppendChild($xml.CreateTextNode($title)) }
  if ($nodes.Count -ge 2) { [void]$nodes.Item(1).AppendChild($xml.CreateTextNode($body)) }
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

func platformShow(req Request) error {
	psBin, err := exec.LookPath("powershell.exe")
	if err != nil {
		psBin, err = exec.LookPath("powershell")
		if err != nil {
			return ErrUnavailable
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, psBin, "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", windowsToastScript)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Env = append(os.Environ(),
		"HOXI_NOTIFY_TITLE="+req.Title,
		"HOXI_NOTIFY_BODY="+req.Body,
	)
	if err := cmd.Run(); err != nil {
		return ErrUnavailable
	}
	return nil
}
