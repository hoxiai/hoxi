#Requires -Version 5.1
<#
.SYNOPSIS
  Builds Hoxi for Windows (lean single-binary Hoxi.exe + NSIS setup.exe + portable .zip; optional opencode.exe via -BundleOpenCode or HOXI_BUNDLE_OPENCODE=1).
.EXAMPLE
  .\scripts\build-windows.ps1
  .\scripts\build-windows.ps1 -Version 0.7.1
  .\scripts\build-windows.ps1 -Arch arm64
  .\scripts\build-windows.ps1 -BundleOpenCode
#>
[CmdletBinding()]
param(
  [string]$Version = "",
  [ValidateSet("amd64", "arm64")]
  [string]$Arch = "amd64",
  [switch]$BundleOpenCode,
  [switch]$SkipInstaller
)

$ErrorActionPreference = "Stop"
$ProjectRoot = Split-Path -Parent $PSScriptRoot
Push-Location $ProjectRoot

try {
  if ([string]::IsNullOrWhiteSpace($Version)) {
    $Version = (& node scripts/release-artifact.mjs default-version).Trim()
  } else {
    $Version = (& node scripts/release-artifact.mjs version $Version).Trim()
  }
  if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($Version)) {
    throw "Failed to resolve build version"
  }

  $BundleMode = if ($BundleOpenCode) {
    "1"
  } elseif (-not [string]::IsNullOrWhiteSpace($env:HOXI_BUNDLE_OPENCODE)) {
    $env:HOXI_BUNDLE_OPENCODE.Trim().ToLowerInvariant()
  } else {
    "0"
  }
  switch ($BundleMode) {
    { $_ -in @("0", "false") } { $BundleMode = "0" }
    { $_ -in @("1", "true") }  { $BundleMode = "1" }
    "auto"                     { $BundleMode = "auto" }
    default {
      throw "Unsupported HOXI_BUNDLE_OPENCODE value: $BundleMode (expected 0, 1, or auto)"
    }
  }
  $env:HOXI_BUNDLE_OPENCODE = $BundleMode

  Write-Host "==> Building Hoxi v$Version for Windows (windows/$Arch, HOXI_BUNDLE_OPENCODE=$BundleMode)..." -ForegroundColor Cyan

  # Sync version metadata into build/windows/info.json and wails.json
  & node -e '
    const fs = require("node:fs");
    const ver = process.argv[1];
    const winVer = ver + ".0";
    const info = JSON.parse(fs.readFileSync("build/windows/info.json", "utf8"));
    info.fileVersion = winVer;
    info.productVersion = winVer;
    fs.writeFileSync("build/windows/info.json", JSON.stringify(info, null, 2) + "\n");
    const wailsCfg = JSON.parse(fs.readFileSync("wails.json", "utf8"));
    wailsCfg.info = wailsCfg.info || {};
    wailsCfg.info.productVersion = ver;
    fs.writeFileSync("wails.json", JSON.stringify(wailsCfg, null, 2) + "\n");
  ' $Version
  if ($LASTEXITCODE -ne 0) { throw "Failed to update Windows version metadata" }

  if (-not (Test-Path "web/node_modules")) {
    & npm --prefix web install
    if ($LASTEXITCODE -ne 0) { throw "npm install failed" }
  }
  & npm --prefix web run build
  if ($LASTEXITCODE -ne 0) { throw "Frontend build failed" }

  New-Item -ItemType Directory -Path "build/bin" -Force | Out-Null
  New-Item -ItemType Directory -Path "build/windows/installer/tmp" -Force | Out-Null
  Remove-Item -Force "build/bin/opencode.exe", "build/windows/installer/tmp/opencode.exe", "build/bin/sqlite3.exe", "build/windows/installer/tmp/sqlite3.exe" -ErrorAction SilentlyContinue

  $Platform = "windows/$Arch"
  if ($BundleMode -eq "0") {
    Write-Host "==> HOXI_BUNDLE_OPENCODE=0 (default): skipping opencode.exe sidecar for lean pure-Go Hoxi Native bundle (pass -BundleOpenCode or HOXI_BUNDLE_OPENCODE=1 to bundle)." -ForegroundColor Cyan
  } else {
  # Resolve or download + verify pinned opencode.exe sidecar for the target arch
  & node scripts/opencode-lock.mjs verify
  if ($LASTEXITCODE -ne 0) { throw "opencode.lock verification failed" }
  $OpenCodeSrc = $null
  if (-not [string]::IsNullOrWhiteSpace($env:HOXI_OPENCODE)) {
    if (-not (Test-Path $env:HOXI_OPENCODE)) {
      throw "HOXI_OPENCODE does not exist: $($env:HOXI_OPENCODE)"
    }
    & node scripts/opencode-lock.mjs verify-binary $env:HOXI_OPENCODE
    if ($LASTEXITCODE -ne 0) {
      throw "HOXI_OPENCODE failed version verification against opencode.lock: $($env:HOXI_OPENCODE)"
    }
    $OpenCodeSrc = $env:HOXI_OPENCODE
  } elseif ($Arch -eq "amd64") {
    # A cached host-local binary is only trusted for the native amd64 target.
    $OpenCodeCandidates = @(
      (Join-Path $HOME ".hoxi\bin\opencode.exe"),
      (Join-Path $HOME ".opencode\bin\opencode.exe")
    ) | Where-Object { -not [string]::IsNullOrWhiteSpace($_) -and (Test-Path $_) }
    foreach ($Candidate in $OpenCodeCandidates) {
      & node scripts/opencode-lock.mjs verify-binary $Candidate *> $null
      if ($LASTEXITCODE -eq 0) {
        $OpenCodeSrc = $Candidate
        break
      }
    }
  }

  if (-not [string]::IsNullOrWhiteSpace($OpenCodeSrc)) {
    Copy-Item -Path $OpenCodeSrc -Destination "build/bin/opencode.exe" -Force
    Copy-Item -Path $OpenCodeSrc -Destination "build/windows/installer/tmp/opencode.exe" -Force
    Write-Host "==> Using verified local opencode.exe from $OpenCodeSrc" -ForegroundColor Green
  } else {
    Write-Host "==> Downloading & verifying pinned OpenCode Windows runtime ($Platform)..." -ForegroundColor Cyan
    $OpenCodeUrl = (& node scripts/opencode-lock.mjs url $Platform).Trim()
    $OpenCodeZip = Join-Path "build/windows/installer/tmp" "opencode-windows-$Arch.zip"
    Invoke-WebRequest -Uri $OpenCodeUrl -OutFile $OpenCodeZip -UseBasicParsing
    & node scripts/opencode-lock.mjs verify-archive $Platform $OpenCodeZip
    if ($LASTEXITCODE -ne 0) { throw "Downloaded opencode archive failed SHA-256 verification" }
    Expand-Archive -Path $OpenCodeZip -DestinationPath "build/windows/installer/tmp" -Force
    Remove-Item -Force $OpenCodeZip -ErrorAction SilentlyContinue
    & node scripts/opencode-lock.mjs verify-binary "build/windows/installer/tmp/opencode.exe"
    if ($LASTEXITCODE -ne 0) { throw "Extracted opencode.exe failed version verification against opencode.lock" }
    Copy-Item -Path "build/windows/installer/tmp/opencode.exe" -Destination "build/bin/opencode.exe" -Force
    if ($Arch -eq "amd64") {
      $CacheDir = Join-Path $HOME ".hoxi\bin"
      New-Item -ItemType Directory -Path $CacheDir -Force | Out-Null
      Copy-Item -Path "build/windows/installer/tmp/opencode.exe" -Destination (Join-Path $CacheDir "opencode.exe") -Force
    }
  }
  }

  # Ensure NSIS (makensis) is on PATH when building the installer
  $WailsArgs = @(
    "build", "-platform", $Platform, "-s", "-m", "-nosyncgomod",
    "-ldflags", "-X github.com/hoxiai/hoxi/internal/app.BuildVariant=release -X github.com/hoxiai/hoxi/internal/buildinfo.Version=$Version"
  )
  if (-not $SkipInstaller) {
    if (-not (Get-Command makensis -ErrorAction SilentlyContinue)) {
      $DefaultNsis = "C:\Program Files (x86)\NSIS"
      if (Test-Path (Join-Path $DefaultNsis "makensis.exe")) {
        $env:PATH = "$DefaultNsis;$env:PATH"
      } else {
        throw "makensis (NSIS) not found. Install NSIS (`winget install NSIS.NSIS` or `choco install nsis -y`) or pass -SkipInstaller."
      }
    }
    $WailsArgs += "-nsis"
  }

  & go run github.com/wailsapp/wails/v2/cmd/wails@v2.13.0 @WailsArgs
  if ($LASTEXITCODE -ne 0) { throw "Wails Windows build failed" }

  if (Test-Path "build/windows/installer/tmp/opencode.exe") {
    Copy-Item -Path "build/windows/installer/tmp/opencode.exe" -Destination "build/bin/opencode.exe" -Force
  } else {
    Remove-Item -Force "build/bin/opencode.exe" -ErrorAction SilentlyContinue
  }
  Remove-Item -Force "build/bin/sqlite3.exe" -ErrorAction SilentlyContinue

  $BuiltVer = (& "build/bin/Hoxi.exe" --version).Trim()
  if ($BuiltVer -ne $Version) {
    throw "Built Hoxi.exe version mismatch: got '$BuiltVer', expected '$Version'"
  }

  if (-not $SkipInstaller) {
    $RawInstaller = "build/bin/Hoxi-$Arch-installer.exe"
    $SetupPath    = "build/bin/Hoxi-$Version-windows-$Arch-setup.exe"
    if (-not (Test-Path $RawInstaller)) {
      throw "Expected NSIS installer at $RawInstaller was not generated"
    }
    Move-Item -Path $RawInstaller -Destination $SetupPath -Force
    $SetupSha = (Get-FileHash -Path $SetupPath -Algorithm SHA256).Hash.ToLowerInvariant()
    Set-Content -Path "$SetupPath.sha256" -Value "$SetupSha  Hoxi-$Version-windows-$Arch-setup.exe`n" -NoNewline
    Write-Host "==> Built Windows Setup Installer: $SetupPath" -ForegroundColor Green
  }

  $ZipPath = "build/bin/Hoxi-$Version-windows-$Arch.zip"
  if (Test-Path $ZipPath) { Remove-Item -Force $ZipPath }
  $ZipEntries = @("build/bin/Hoxi.exe")
  if (Test-Path "build/bin/opencode.exe") {
    $ZipEntries += "build/bin/opencode.exe"
  }
  Compress-Archive -Path $ZipEntries -DestinationPath $ZipPath -Force
  $ZipSha = (Get-FileHash -Path $ZipPath -Algorithm SHA256).Hash.ToLowerInvariant()
  Set-Content -Path "$ZipPath.sha256" -Value "$ZipSha  Hoxi-$Version-windows-$Arch.zip`n" -NoNewline
  Write-Host "==> Built Windows Portable Archive: $ZipPath" -ForegroundColor Green
} finally {
  Pop-Location
}
