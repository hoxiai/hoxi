#Requires -Version 5.1
<#
.SYNOPSIS
  Builds Hoxi for Windows (lean single-binary Hoxi.exe + NSIS setup.exe + portable .zip; optional opencode.exe via -BundleOpenCode or HOXI_BUNDLE_OPENCODE=1).
.EXAMPLE
  .\scripts\build-windows.ps1
  .\scripts\build-windows.ps1 -Version 0.7.2
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

$OriginalHoxiWorkDir = $env:HOXI_WORK_DIR
$HadOriginalHoxiWorkDir = Test-Path Env:HOXI_WORK_DIR
$StagedSkills = @()
$StagedHostFiles = @()

function ConvertTo-GoWorkPath {
  param(
    [string]$TargetDir,
    [string]$HostRoot
  )
  $targetFull = ([System.IO.Path]::GetFullPath($TargetDir) -replace '\\', '/').TrimEnd('/')
  $hostFull = ([System.IO.Path]::GetFullPath($HostRoot) -replace '\\', '/').TrimEnd('/')
  $hostParent = ([System.IO.Path]::GetFullPath((Split-Path -Parent $HostRoot)) -replace '\\', '/').TrimEnd('/')
  if ($targetFull.StartsWith("$hostFull/", [System.StringComparison]::OrdinalIgnoreCase)) {
    return "./" + $targetFull.Substring($hostFull.Length + 1)
  }
  if ($targetFull.StartsWith("$hostParent/", [System.StringComparison]::OrdinalIgnoreCase)) {
    return "../" + $targetFull.Substring($hostParent.Length + 1)
  }
  return $targetFull
}

function Resolve-WorkRepoDir {
  param([string]$HostRoot)
  if ($env:HOXI_DISABLE_WORK_EXT -eq "1") {
    return ""
  }
  $candidates = @()
  if (-not [string]::IsNullOrWhiteSpace($env:HOXI_WORK_DIR)) {
    $envDir = $env:HOXI_WORK_DIR.Trim()
    if ($envDir -match '^/([a-zA-Z])/(.*)$') {
      $envDir = "$($Matches[1].ToUpperInvariant()):/$($Matches[2])"
    }
    $candidates += $envDir
  } else {
    $candidates += (Join-Path $HostRoot "work")
    try {
      $commonGit = (& git -C $HostRoot rev-parse --git-common-dir 2>$null)
      if ($LASTEXITCODE -eq 0 -and -not [string]::IsNullOrWhiteSpace($commonGit)) {
        $commonGitFull = if ([System.IO.Path]::IsPathRooted($commonGit.Trim())) {
          $commonGit.Trim()
        } else {
          [System.IO.Path]::GetFullPath((Join-Path $HostRoot $commonGit.Trim()))
        }
        $mainRoot = Split-Path -Parent $commonGitFull
        if (-not [string]::IsNullOrWhiteSpace($mainRoot)) {
          $candidates += (Join-Path $mainRoot "work")
        }
      }
    } catch {
      # Ignore git worktree lookup errors when git is unavailable
    }
    $candidates += [System.IO.Path]::GetFullPath((Join-Path $HostRoot "..\hoxi-ext"))
    $candidates += [System.IO.Path]::GetFullPath((Join-Path $HostRoot "..\..\..\..\hoxi-ext"))
  }

  foreach ($cand in $candidates) {
    if ([string]::IsNullOrWhiteSpace($cand)) { continue }
    $entry = Join-Path (Join-Path $cand "web") "index.tsx"
    if (Test-Path -Path $entry -PathType Leaf) {
      return [System.IO.Path]::GetFullPath($cand)
    }
  }
  return ""
}

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

  $WorkRepoDir = Resolve-WorkRepoDir -HostRoot $ProjectRoot
  $WorkEnabled = -not [string]::IsNullOrWhiteSpace($WorkRepoDir)
  if ($WorkEnabled) {
    $env:HOXI_WORK_DIR = $WorkRepoDir
    Write-Host "==> Bundling Work surface extension from $WorkRepoDir" -ForegroundColor Cyan

    # Stage Go workspace & host extension bridge natively (no Git Bash required)
    $WorkGoDir = Join-Path $WorkRepoDir "go"
    $BridgeTmpl = Join-Path $WorkGoDir "work_bridge.go.tmpl"
    if ((Test-Path -Path $WorkGoDir -PathType Container) -and (Test-Path -Path $BridgeTmpl -PathType Leaf)) {
      $StageHostScript = Join-Path $WorkGoDir "stage-host.sh"
      $RequiredWorkModules = @()
      if (Test-Path -Path $StageHostScript -PathType Leaf) {
        $stageHostRaw = Get-Content -Path $StageHostScript -Raw
        if ($stageHostRaw -match 'WORK_MODULES=\(([^)]+)\)') {
          $RequiredWorkModules = @($Matches[1].Trim() -split '\s+' | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
        }
      }
      foreach ($reqMod in $RequiredWorkModules) {
        $reqModFile = Join-Path (Join-Path $WorkGoDir $reqMod) "go.mod"
        if (-not (Test-Path -Path $reqModFile -PathType Leaf)) {
          throw "Missing required Work module: $reqModFile; restore the complete Work checkout"
        }
      }
      $WorkModDirs = @(Get-ChildItem -Path $WorkGoDir -Directory | Where-Object {
        Test-Path -Path (Join-Path $_.FullName "go.mod") -PathType Leaf
      } | Sort-Object Name)
      if ($WorkModDirs.Count -eq 0) {
        throw "No Go extension modules found under $WorkGoDir; restore the complete Work checkout"
      }
      if ($WorkModDirs.Count -gt 0) {
        $GoWorkGoDir = ConvertTo-GoWorkPath -TargetDir $WorkGoDir -HostRoot $ProjectRoot
        $GoWorkPath = Join-Path $ProjectRoot "go.work"
        $GoWorkSumPath = Join-Path $ProjectRoot "go.work.sum"
        if (-not (Test-Path -Path $GoWorkPath -PathType Leaf)) {
          $useLines = @("`t.")
          foreach ($modDir in $WorkModDirs) {
            $useLines += "`t`"$GoWorkGoDir/$($modDir.Name)`""
          }
          $goWorkContent = "go 1.25.0`n`nuse (`n" + ($useLines -join "`n") + "`n)`n"
          [System.IO.File]::WriteAllText($GoWorkPath, $goWorkContent, (New-Object System.Text.UTF8Encoding($false)))
          $StagedHostFiles += $GoWorkPath
          $StagedHostFiles += $GoWorkSumPath
        } else {
          $existingGoWork = Get-Content -Path $GoWorkPath -Raw
          foreach ($modDir in $WorkModDirs) {
            if ($existingGoWork -notmatch [regex]::Escape("/$($modDir.Name)")) {
              & go work use "$GoWorkGoDir/$($modDir.Name)"
              if ($LASTEXITCODE -ne 0) { throw "Failed to register Work module $($modDir.Name) in go.work" }
            }
          }
        }

        $BridgeDst = Join-Path (Join-Path (Join-Path $ProjectRoot "internal") "app") "work_bridge.go"
        $bridgeExisted = Test-Path -Path $BridgeDst -PathType Leaf
        $tmplBytes = [System.IO.File]::ReadAllBytes($BridgeTmpl)
        $needsWrite = -not $bridgeExisted
        if (-not $needsWrite) {
          $dstBytes = [System.IO.File]::ReadAllBytes($BridgeDst)
          if ($dstBytes.Length -ne $tmplBytes.Length) {
            $needsWrite = $true
          } else {
            for ($i = 0; $i -lt $tmplBytes.Length; $i++) {
              if ($dstBytes[$i] -ne $tmplBytes[$i]) { $needsWrite = $true; break }
            }
          }
        }
        if ($needsWrite) {
          [System.IO.File]::WriteAllBytes($BridgeDst, $tmplBytes)
          if (-not $bridgeExisted) {
            $StagedHostFiles += $BridgeDst
          }
        }
      }
    }

    # Stage private Work skills into internal/skills/defaults for embedding
    $WorkSkillsDir = Join-Path $WorkRepoDir "skills"
    if (Test-Path -Path $WorkSkillsDir -PathType Container) {
      $DefaultsRoot = Join-Path (Join-Path (Join-Path $ProjectRoot "internal") "skills") "defaults"
      foreach ($skillDir in (Get-ChildItem -Path $WorkSkillsDir -Directory)) {
        if (-not (Test-Path -Path (Join-Path $skillDir.FullName "SKILL.md") -PathType Leaf)) { continue }
        $targetDir = Join-Path $DefaultsRoot $skillDir.Name
        if (-not (Test-Path -Path $targetDir)) {
          Copy-Item -Path $skillDir.FullName -Destination $targetDir -Recurse -Force
          $StagedSkills += $targetDir
          Write-Host "==> Staged Work skill '$($skillDir.Name)' from $WorkRepoDir" -ForegroundColor Cyan
        }
      }
    }
  }

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
  if ($WorkEnabled) {
    $hasWorkBundle = Select-String -Path "web/dist/assets/*.js" -SimpleMatch "modeExecuteLabel" -Quiet
    if (-not $hasWorkBundle) {
      throw "Frontend build did not include Work surface extension (modeExecuteLabel missing from web/dist/assets/*.js)"
    }
  }

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

  $HostArch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64" -or $env:PROCESSOR_ARCHITEW6432 -eq "ARM64") { "arm64" } else { "amd64" }
  if ($HostArch -eq $Arch) {
    $BuiltVer = (& "build/bin/Hoxi.exe" --version).Trim()
    if ($LASTEXITCODE -ne 0 -or $BuiltVer -ne $Version) {
      throw "Built Hoxi.exe version mismatch: got '$BuiltVer', expected '$Version'"
    }
  } else {
    $verMeta = (& go version -m "build/bin/Hoxi.exe" | Out-String)
    if ($LASTEXITCODE -ne 0 -or $verMeta -notmatch [regex]::Escape("buildinfo.Version=$Version")) {
      throw "Built Hoxi.exe does not embed expected version '$Version'"
    }
  }
  $BridgeFile = Join-Path (Join-Path (Join-Path $ProjectRoot "internal") "app") "work_bridge.go"
  if ($WorkEnabled -and (Test-Path -Path $BridgeFile -PathType Leaf)) {
    $bridgeMatch = Select-String -Path $BridgeFile -Pattern '^\s*import\s+"([^"]+)"' | Select-Object -First 1
    if ($bridgeMatch -and $bridgeMatch.Matches.Count -gt 0) {
      $bridgePkg = $bridgeMatch.Matches[0].Groups[1].Value
      $buildMeta = (& go version -m "build/bin/Hoxi.exe" | Out-String)
      if ($LASTEXITCODE -ne 0 -or $buildMeta -notmatch [regex]::Escape($bridgePkg)) {
        throw "Built Hoxi.exe does not include staged Work backend module '$bridgePkg'"
      }
    }
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
  foreach ($skillDir in $StagedSkills) {
    if (-not [string]::IsNullOrWhiteSpace($skillDir) -and (Test-Path $skillDir)) {
      Remove-Item -Path $skillDir -Recurse -Force -ErrorAction SilentlyContinue
    }
  }
  foreach ($stagedFile in $StagedHostFiles) {
    if (-not [string]::IsNullOrWhiteSpace($stagedFile) -and (Test-Path $stagedFile)) {
      Remove-Item -Path $stagedFile -Force -ErrorAction SilentlyContinue
    }
  }
  if ($HadOriginalHoxiWorkDir) {
    $env:HOXI_WORK_DIR = $OriginalHoxiWorkDir
  } else {
    Remove-Item Env:HOXI_WORK_DIR -ErrorAction SilentlyContinue
  }
  Pop-Location
}
