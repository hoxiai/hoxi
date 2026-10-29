#Requires -Version 5.1
<#
.SYNOPSIS
  One-line Windows installer for Hoxi Desktop.
.DESCRIPTION
  Downloads the official Hoxi Windows release from GitHub Releases, verifies its
  SHA-256 checksum against the release .sha256 sidecar, installs Hoxi (and its
  bundled opencode.exe sidecar) into %LOCALAPPDATA%\Programs\Hoxi, creates
  Desktop and Start Menu shortcuts, registers the hoxi:// URL protocol, adds
  Hoxi to the user PATH, and launches Hoxi.
.EXAMPLE
  irm https://raw.githubusercontent.com/hoxiai/hoxi/main/scripts/install.ps1 | iex
#>
[CmdletBinding()]
param(
  [string]$Version = "",
  [string]$InstallDir = (Join-Path $env:LOCALAPPDATA "Programs\Hoxi"),
  [switch]$Portable,
  [switch]$NoLaunch
)

$ErrorActionPreference = "Stop"
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

if ($env:PROCESSOR_ARCHITECTURE -ne "AMD64" -and $env:PROCESSOR_ARCHITEW6432 -ne "AMD64") {
  throw "Hoxi Windows installer currently requires 64-bit Windows (x64 / AMD64)."
}

$Repo = "hoxiai/hoxi"
$ApiUrl = if ([string]::IsNullOrWhiteSpace($Version)) {
  "https://api.github.com/repos/$Repo/releases/latest"
} else {
  $CleanVer = $Version.Trim().TrimStart("v", "V")
  "https://api.github.com/repos/$Repo/releases/tags/v$CleanVer"
}

Write-Host "==> Fetching Hoxi release metadata from GitHub..." -ForegroundColor Cyan
$Headers = @{ "User-Agent" = "Hoxi-Windows-Installer" }
$Release = Invoke-RestMethod -Uri $ApiUrl -Headers $Headers -Method Get
$TagName = [string]$Release.tag_name
$Ver = $TagName.TrimStart("v", "V")
if ([string]::IsNullOrWhiteSpace($Ver)) {
  throw "Unable to determine release version from $ApiUrl"
}

$SetupName = "Hoxi-$Ver-windows-amd64-setup.exe"
$ZipName   = "Hoxi-$Ver-windows-amd64.zip"

$SetupAsset = $Release.assets | Where-Object { $_.name -eq $SetupName } | Select-Object -First 1
$ZipAsset   = $Release.assets | Where-Object { $_.name -eq $ZipName }   | Select-Object -First 1

function Get-VerifiedDownload {
  param(
    [Parameter(Mandatory = $true)][string]$AssetUrl,
    [Parameter(Mandatory = $true)][string]$FileName
  )
  $TempDir = Join-Path ([System.IO.Path]::GetTempPath()) ("hoxi-install-" + [Guid]::NewGuid().ToString("N"))
  New-Item -ItemType Directory -Path $TempDir -Force | Out-Null
  $OutFile = Join-Path $TempDir $FileName
  $ShaFile = "$OutFile.sha256"
  $ShaUrl  = "$AssetUrl.sha256"

  Write-Host "==> Downloading $FileName..." -ForegroundColor Cyan
  Invoke-WebRequest -Uri $AssetUrl -OutFile $OutFile -UseBasicParsing
  Invoke-WebRequest -Uri $ShaUrl -OutFile $ShaFile -UseBasicParsing

  $RawSha = (Get-Content -Path $ShaFile -Raw).Trim()
  $ExpectedSha = ($RawSha -split "\s+" | Where-Object { $_ -match "^[0-9a-fA-F]{64}$" } | Select-Object -First 1)
  if ([string]::IsNullOrWhiteSpace($ExpectedSha)) {
    Remove-Item -Recurse -Force $TempDir -ErrorAction SilentlyContinue
    throw "Missing or invalid SHA-256 checksum in $ShaUrl"
  }

  $ActualSha = (Get-FileHash -Path $OutFile -Algorithm SHA256).Hash
  if ($ActualSha.ToLowerInvariant() -ne $ExpectedSha.ToLowerInvariant()) {
    Remove-Item -Recurse -Force $TempDir -ErrorAction SilentlyContinue
    throw "SHA-256 verification failed for $FileName (expected $ExpectedSha, got $ActualSha)"
  }
  Write-Host "==> Verified SHA-256: $($ActualSha.ToLowerInvariant())" -ForegroundColor Green
  return @{ TempDir = $TempDir; FilePath = $OutFile }
}

function Add-UserPathEntry {
  param([Parameter(Mandatory = $true)][string]$Dir)
  $CurrentUserPath = [Environment]::GetEnvironmentVariable("Path", "User")
  $Parts = if ([string]::IsNullOrWhiteSpace($CurrentUserPath)) { @() } else { $CurrentUserPath -split ";" }
  $Normalized = $Dir.TrimEnd("\")
  $AlreadyPresent = $false
  foreach ($p in $Parts) {
    if ($p.Trim().TrimEnd("\") -ieq $Normalized) {
      $AlreadyPresent = $true
      break
    }
  }
  if (-not $AlreadyPresent) {
    $NewPath = if ([string]::IsNullOrWhiteSpace($CurrentUserPath)) { $Dir } else { "$CurrentUserPath;$Dir" }
    [Environment]::SetEnvironmentVariable("Path", $NewPath, "User")
    $env:Path = "$env:Path;$Dir"
  }
}

if ($SetupAsset -and -not $Portable) {
  $Downloaded = Get-VerifiedDownload -AssetUrl $SetupAsset.browser_download_url -FileName $SetupName
  try {
    Write-Host "==> Running Hoxi Setup Installer ($Ver)..." -ForegroundColor Cyan
    $Proc = Start-Process -FilePath $Downloaded.FilePath -ArgumentList @("/S", "/D=$InstallDir") -Wait -PassThru
    if ($Proc.ExitCode -ne 0) {
      throw "Hoxi Setup exited with code $($Proc.ExitCode)"
    }
  } finally {
    Remove-Item -Recurse -Force $Downloaded.TempDir -ErrorAction SilentlyContinue
  }
} elseif ($ZipAsset) {
  $Downloaded = Get-VerifiedDownload -AssetUrl $ZipAsset.browser_download_url -FileName $ZipName
  try {
    Get-Process -Name "Hoxi" -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    Write-Host "==> Extracting Hoxi $Ver to $InstallDir..." -ForegroundColor Cyan
    Expand-Archive -Path $Downloaded.FilePath -DestinationPath $InstallDir -Force

    $ExePath = Join-Path $InstallDir "Hoxi.exe"
    if (-not (Test-Path $ExePath)) {
      throw "Extracted archive did not contain Hoxi.exe"
    }

    # Create Start Menu & Desktop shortcuts
    $WshShell = New-Object -ComObject WScript.Shell
    $StartMenuShortcut = Join-Path ([Environment]::GetFolderPath("Programs")) "Hoxi.lnk"
    $DesktopShortcut   = Join-Path ([Environment]::GetFolderPath("Desktop")) "Hoxi.lnk"
    foreach ($ShortcutPath in @($StartMenuShortcut, $DesktopShortcut)) {
      $Shortcut = $WshShell.CreateShortcut($ShortcutPath)
      $Shortcut.TargetPath = $ExePath
      $Shortcut.WorkingDirectory = $InstallDir
      $Shortcut.IconLocation = "$ExePath,0"
      $Shortcut.Description = "Hoxi — Native AI Engineering Command Center"
      $Shortcut.Save()
    }

    # Register hoxi:// custom URL scheme under HKCU
    $ProtoKey = "HKCU:\Software\Classes\hoxi"
    New-Item -Path "$ProtoKey\DefaultIcon" -Force | Out-Null
    New-Item -Path "$ProtoKey\shell\open\command" -Force | Out-Null
    Set-ItemProperty -Path $ProtoKey -Name "(Default)" -Value "URL:Hoxi Protocol"
    Set-ItemProperty -Path $ProtoKey -Name "URL Protocol" -Value ""
    Set-ItemProperty -Path "$ProtoKey\DefaultIcon" -Name "(Default)" -Value "`"$ExePath`",0"
    Set-ItemProperty -Path "$ProtoKey\shell\open\command" -Name "(Default)" -Value "`"$ExePath`" `"%1`""
  } finally {
    Remove-Item -Recurse -Force $Downloaded.TempDir -ErrorAction SilentlyContinue
  }
} else {
  throw "No Windows release asset found for Hoxi v$Ver"
}

Add-UserPathEntry -Dir $InstallDir

$InstalledExe = Join-Path $InstallDir "Hoxi.exe"
Write-Host "==> Hoxi v$Ver installed to $InstallDir" -ForegroundColor Green

if (-not $NoLaunch -and (Test-Path $InstalledExe)) {
  Write-Host "==> Launching Hoxi..." -ForegroundColor Cyan
  Start-Process -FilePath $InstalledExe
}
