#Requires -Version 5.1
<#
.SYNOPSIS
  Builds Hoxi for Windows (Hoxi.exe + bundled opencode.exe + NSIS setup.exe + portable .zip).
.EXAMPLE
  .\scripts\build-windows.ps1
  .\scripts\build-windows.ps1 -Version 0.5.1
  .\scripts\build-windows.ps1 -Arch arm64
#>
[CmdletBinding()]
param(
  [string]$Version = "",
  [ValidateSet("amd64", "arm64")]
  [string]$Arch = "amd64",
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

  Write-Host "==> Building Hoxi v$Version for Windows (windows/$Arch)..." -ForegroundColor Cyan

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

  # Bundle the official SQLite CLI for account import, history and statistics.
  $SQLiteZip = Join-Path "build/windows/installer/tmp" "sqlite-tools.zip"
  $SQLiteURL = (& node scripts/sqlite-tools.mjs url $Arch).Trim()
  if ($LASTEXITCODE -ne 0) { throw "Failed to resolve SQLite URL" }
  Invoke-WebRequest -Uri $SQLiteURL -OutFile $SQLiteZip -UseBasicParsing
  & node scripts/sqlite-tools.mjs verify $SQLiteZip $Arch
  if ($LASTEXITCODE -ne 0) { throw "Downloaded SQLite archive failed SHA3-256 verification" }
  Expand-Archive -Path $SQLiteZip -DestinationPath "build/windows/installer/tmp/sqlite" -Force
  Remove-Item -Force $SQLiteZip
  Copy-Item -Path "build/windows/installer/tmp/sqlite/sqlite3.exe" -Destination "build/bin/sqlite3.exe" -Force

  # Resolve or download + verify pinned opencode.exe sidecar for the target arch
  & node scripts/opencode-lock.mjs verify
  if ($LASTEXITCODE -ne 0) { throw "opencode.lock verification failed" }

  $Platform = "windows/$Arch"
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

  Copy-Item -Path "build/windows/installer/tmp/opencode.exe" -Destination "build/bin/opencode.exe" -Force
  Copy-Item -Path "build/windows/installer/tmp/sqlite/sqlite3.exe" -Destination "build/bin/sqlite3.exe" -Force

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
  Compress-Archive -Path @("build/bin/Hoxi.exe", "build/bin/opencode.exe", "build/bin/sqlite3.exe") -DestinationPath $ZipPath -Force
  $ZipSha = (Get-FileHash -Path $ZipPath -Algorithm SHA256).Hash.ToLowerInvariant()
  Set-Content -Path "$ZipPath.sha256" -Value "$ZipSha  Hoxi-$Version-windows-$Arch.zip`n" -NoNewline
  Write-Host "==> Built Windows Portable Archive: $ZipPath" -ForegroundColor Green
} finally {
  Pop-Location
}
