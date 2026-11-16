$ErrorActionPreference = 'Stop'
[Console]::InputEncoding = New-Object System.Text.UTF8Encoding($false)
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
$request = [Console]::In.ReadToEnd() | ConvertFrom-Json
Add-Type -AssemblyName UIAutomationClient
Add-Type -AssemblyName UIAutomationTypes
$root = [System.Windows.Automation.AutomationElement]::FromHandle([IntPtr][long]$request.window)
if ($root.Current.ProcessId -ne [int]$request.process) { throw 'target_app_changed' }
if ($request.operation -eq 'capture') {
  Add-Type -AssemblyName System.Drawing
  Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public static class HoxiWindowCapture {
  [StructLayout(LayoutKind.Sequential)] public struct Rect { public int Left,Top,Right,Bottom; }
  [DllImport("user32.dll")] public static extern bool PrintWindow(IntPtr window,IntPtr dc,uint flags);
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr window,out Rect rect);
  [DllImport("user32.dll")] public static extern IntPtr SetThreadDpiAwarenessContext(IntPtr context);
}
'@
  $previousDPI = [HoxiWindowCapture]::SetThreadDpiAwarenessContext([IntPtr](-4))
  $bounds = New-Object HoxiWindowCapture+Rect
  if (-not [HoxiWindowCapture]::GetWindowRect([IntPtr][long]$request.window,[ref]$bounds)) { throw 'window_closed' }
  $width = $bounds.Right-$bounds.Left; $height = $bounds.Bottom-$bounds.Top
  if ($width -lt 1 -or $height -lt 1 -or [long]$width*$height -gt 16000000) { throw 'capture_exceeds_budget' }
  $bitmap = [System.Drawing.Bitmap]::new($width,$height)
  $graphics = [System.Drawing.Graphics]::FromImage($bitmap)
  try {
    $dc = $graphics.GetHdc()
    try { $ok = [HoxiWindowCapture]::PrintWindow([IntPtr][long]$request.window,$dc,2) }
    finally { $graphics.ReleaseHdc($dc) }
    if (-not $ok) { throw 'window_capture_unavailable' }
    $scale = [Math]::Min(1.0,1600.0/[Math]::Max($width,$height))
    $small = [System.Drawing.Bitmap]::new($bitmap,[int][Math]::Max(1,$width*$scale),[int][Math]::Max(1,$height*$scale))
    $stream = New-Object System.IO.MemoryStream
    try {
      $small.Save($stream,[System.Drawing.Imaging.ImageFormat]::Jpeg)
      @{image=[Convert]::ToBase64String($stream.ToArray());width=$small.Width;height=$small.Height} | ConvertTo-Json -Compress
    } finally { $stream.Dispose(); $small.Dispose() }
  } finally { $graphics.Dispose(); $bitmap.Dispose() }
  exit
}
if ($request.operation -eq 'focused') {
  $focused = [System.Windows.Automation.AutomationElement]::FocusedElement
  if ($null -eq $focused -or $focused.Current.ProcessId -ne [int]$request.process) { throw 'target_app_changed' }
  @{ secure = [bool]$focused.Current.IsPassword } | ConvertTo-Json -Compress
  exit
}
$rows = New-Object 'System.Collections.Generic.List[object]'
$walker = [System.Windows.Automation.TreeWalker]::ControlViewWalker
function Walk-Control($element, [int]$depth) {
  if ($null -eq $element -or $depth -gt 14 -or $rows.Count -ge 300) { return }
  try {
    $current = $element.Current
    if ($current.ProcessId -ne [int]$request.process) { return }
    $secure = [bool]$current.IsPassword
    $name = ''; $value = ''
    if (-not $secure) {
      $name = [string]$current.Name
      if ($name.Length -gt 256) { $name = $name.Substring(0,256) }
      $pattern = $null
      if ($element.TryGetCurrentPattern([System.Windows.Automation.ValuePattern]::Pattern, [ref]$pattern)) {
        $value = [string]$pattern.Current.Value
        if ($value.Length -gt 1000) { $value = $value.Substring(0,1000) }
      }
    }
    $bounds = $current.BoundingRectangle
    if (-not $bounds.IsEmpty) {
      $rows.Add(@{ id=$rows.Count+1;role=[string]$current.ControlType.ProgrammaticName;name=$name;value=$value;secure=$secure;bounds=@{x=$bounds.X;y=$bounds.Y;width=$bounds.Width;height=$bounds.Height} })
    }
    $child = $walker.GetFirstChild($element)
    while ($null -ne $child -and $rows.Count -lt 300) { Walk-Control $child ($depth+1); $child = $walker.GetNextSibling($child) }
  } catch { }
}
Walk-Control $root 0
@{elements=@($rows.ToArray())} | ConvertTo-Json -Depth 8 -Compress
