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
$visibleInteractive = New-Object 'System.Collections.Generic.List[object]'
$visibleContent = New-Object 'System.Collections.Generic.List[object]'
$walker = [System.Windows.Automation.TreeWalker]::ControlViewWalker
$winRect = $root.Current.BoundingRectangle
$script:visited = 0
$maxVisited = 4000
$interactiveTypes = @('ControlType.Button','ControlType.Edit','ControlType.ComboBox','ControlType.CheckBox','ControlType.RadioButton','ControlType.MenuItem','ControlType.TabItem','ControlType.Hyperlink','ControlType.ListItem','ControlType.Slider','ControlType.SplitButton')
$containerTypes = @('ControlType.Window','ControlType.Pane','ControlType.Group','ControlType.Custom','ControlType.Document','ControlType.ScrollBar','ControlType.List','ControlType.Table','ControlType.Tree','ControlType.ToolBar','ControlType.Tab','ControlType.DataGrid','ControlType.TitleBar','ControlType.MenuBar')
function Walk-Control($element, [int]$depth) {
  if ($null -eq $element -or $depth -gt 32 -or $script:visited -ge $maxVisited) { return }
  $script:visited++
  try {
    $current = $element.Current
    if ($current.ProcessId -ne [int]$request.process) { return }
    $role = [string]$current.ControlType.ProgrammaticName
    if ($role -eq 'ControlType.ScrollBar') { return }
    $secure = [bool]$current.IsPassword
    $interactive = $secure -or ($interactiveTypes -contains $role)
    $name = ''; $value = ''
    if (-not $secure) {
      $name = [string]$current.Name
      if ($name.Length -gt 256) { $name = $name.Substring(0,256) }
      $pattern = $null
      if ($element.TryGetCurrentPattern([System.Windows.Automation.ValuePattern]::Pattern, [ref]$pattern)) {
        $value = [string]$pattern.Current.Value
        if ($value.Length -gt 1000) { $value = $value.Substring(0,496) + ' [...] ' + $value.Substring($value.Length - 497) }
      }
      if ($name.Length -gt 0 -and $value.Length -gt 0 -and $name -eq $value) { $value = '' }
    }
    $bounds = $current.BoundingRectangle
    $hasSize = (-not $bounds.IsEmpty) -and $bounds.Width -gt 0.5 -and $bounds.Height -gt 0.5
    $visible = $hasSize -and ($winRect.IsEmpty -or ($bounds.Left -lt $winRect.Right -and $bounds.Right -gt $winRect.Left -and $bounds.Top -lt $winRect.Bottom -and $bounds.Bottom -gt $winRect.Top))
    $thinNameless = $interactive -and (-not $secure) -and ($name.Length -eq 0) -and ($value.Length -eq 0) -and ($bounds.Width -le 12 -or $bounds.Height -le 12)
    $shouldEmit = $secure -or ($interactive -and $visible -and (-not $thinNameless)) -or ((-not ($containerTypes -contains $role)) -and $visible -and ($name.Length -gt 0 -or $value.Length -gt 0)) -or ($visible -and $name.Length -gt 0 -and $value.Length -gt 0)
    if ($shouldEmit) {
      $bx = if ($hasSize) { $bounds.X } else { 0 }
      $by = if ($hasSize) { $bounds.Y } else { 0 }
      $bw = if ($hasSize) { $bounds.Width } else { 0 }
      $bh = if ($hasSize) { $bounds.Height } else { 0 }
      $entry = @{ role=$role;name=$name;value=$value;secure=$secure;bounds=@{x=$bx;y=$by;width=$bw;height=$bh} }
      if ([bool]$current.HasKeyboardFocus) { $entry['focused'] = $true }
      if ($interactive -and (-not [bool]$current.IsEnabled)) { $entry['disabled'] = $true }
      if ($interactive) { $visibleInteractive.Add($entry) }
      elseif ($visibleContent.Count -lt 900) { $visibleContent.Add($entry) }
    }
    if ($secure) { return }
    if ($depth -gt 0 -and $hasSize -and (-not $visible) -and (-not $winRect.IsEmpty) -and $role -ne 'ControlType.Window' -and $role -ne 'ControlType.Menu') { return }
    $child = $walker.GetFirstChild($element)
    while ($null -ne $child -and $script:visited -lt $maxVisited) { Walk-Control $child ($depth+1); $child = $walker.GetNextSibling($child) }
  } catch { }
}
Walk-Control $root 0
$rows = New-Object 'System.Collections.Generic.List[object]'
if ($visibleInteractive.Count -le 300) {
  foreach ($item in $visibleInteractive) { $rows.Add($item) }
} else {
  for ($i = 0; $i -lt 150; $i++) { $rows.Add($visibleInteractive[$i]) }
  for ($i = $visibleInteractive.Count - 150; $i -lt $visibleInteractive.Count; $i++) { $rows.Add($visibleInteractive[$i]) }
}
if ($rows.Count -lt 300 -and $visibleContent.Count -gt 0) {
  $remain = 300 - $rows.Count
  $start = [Math]::Max(0, $visibleContent.Count - $remain)
  for ($i = $start; $i -lt $visibleContent.Count; $i++) { $rows.Add($visibleContent[$i]) }
}
for ($i = 0; $i -lt $rows.Count; $i++) { $rows[$i]['id'] = $i + 1 }
@{elements=@($rows.ToArray())} | ConvertTo-Json -Depth 8 -Compress
