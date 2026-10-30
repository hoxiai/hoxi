Unicode true

####
## Hoxi Windows NSIS Installer (build/windows/installer/project.nsi)
##
## Defaults to per-user installation (%LOCALAPPDATA%\Programs\Hoxi) so that:
##   1. Installation does not require UAC Administrator elevation.
##   2. Hoxi's in-app Windows self-updater (internal/updater/native_windows.go)
##      can stage and atomically replace Hoxi.exe and opencode.exe in $INSTDIR.
##   3. Both Hoxi.exe and the verified opencode.exe sidecar are bundled together.
####

!ifndef INFO_COMPANYNAME
    !define INFO_COMPANYNAME "Hoxi"
!endif
!ifndef UNINST_KEY_NAME
    !define UNINST_KEY_NAME "Hoxi"
!endif
!ifndef WAILS_INSTALL_SCOPE
    !define WAILS_INSTALL_SCOPE "user"
!endif
!ifndef REQUEST_EXECUTION_LEVEL
    !define REQUEST_EXECUTION_LEVEL "user"
!endif

!ifndef ARG_HOXI_OPENCODE_BINARY
  !if /FileExists "..\..\bin\opencode.exe"
    !define ARG_HOXI_OPENCODE_BINARY "..\..\bin\opencode.exe"
  !else if /FileExists "tmp\opencode.exe"
    !define ARG_HOXI_OPENCODE_BINARY "tmp\opencode.exe"
  !else
    !error "Hoxi installer requires opencode.exe in build/bin/opencode.exe or build/windows/installer/tmp/opencode.exe"
  !endif
!endif

!ifndef ARG_HOXI_SQLITE_BINARY
  !if /FileExists "..\..\bin\sqlite3.exe"
    !define ARG_HOXI_SQLITE_BINARY "..\..\bin\sqlite3.exe"
  !else if /FileExists "tmp\sqlite3.exe"
    !define ARG_HOXI_SQLITE_BINARY "tmp\sqlite3.exe"
  !else if /FileExists "tmp\sqlite\sqlite3.exe"
    !define ARG_HOXI_SQLITE_BINARY "tmp\sqlite\sqlite3.exe"
  !else
    !error "Hoxi installer requires the verified sqlite3.exe sidecar"
  !endif
!endif

!include "wails_tools.nsh"

# The version information for these two must consist of 4 numeric parts
VIProductVersion "${INFO_PRODUCTVERSION}.0"
VIFileVersion    "${INFO_PRODUCTVERSION}.0"

VIAddVersionKey "CompanyName"     "${INFO_COMPANYNAME}"
VIAddVersionKey "FileDescription" "${INFO_PRODUCTNAME} Setup"
VIAddVersionKey "ProductVersion"  "${INFO_PRODUCTVERSION}"
VIAddVersionKey "FileVersion"     "${INFO_PRODUCTVERSION}"
VIAddVersionKey "LegalCopyright"  "${INFO_COPYRIGHT}"
VIAddVersionKey "ProductName"     "${INFO_PRODUCTNAME}"

# Enable HiDPI support
ManifestDPIAware true

!include "MUI.nsh"

!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_RUN "$INSTDIR\${PRODUCT_EXECUTABLE}"

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "English"
!insertmacro MUI_LANGUAGE "SimpChinese"

Name "${INFO_PRODUCTNAME}"
OutFile "..\..\bin\${INFO_PROJECTNAME}-${ARCH}-installer.exe"
!ifdef WAILS_INSTALL_SCOPE
  !if "${WAILS_INSTALL_SCOPE}" == "user"
    InstallDir "$LOCALAPPDATA\Programs\${INFO_PRODUCTNAME}"
  !else
    InstallDir "$PROGRAMFILES64\${INFO_COMPANYNAME}\${INFO_PRODUCTNAME}"
  !endif
!else
  InstallDir "$LOCALAPPDATA\Programs\${INFO_PRODUCTNAME}"
!endif
ShowInstDetails show

Function .onInit
    !insertmacro wails.checkArchitecture
FunctionEnd

# Stop Hoxi.exe, opencode.exe and sqlite3.exe that run from $INSTDIR and wait
# for them to exit. Killing Hoxi.exe alone leaves its `opencode serve` child
# running, which keeps $INSTDIR\opencode.exe locked: the uninstaller then
# cannot remove it and the next install cannot overwrite it. Matching is by
# full path in $INSTDIR, never by name, so opencode.exe installed elsewhere
# by the user is left alone. Hoxi.exe goes first so it cannot respawn
# opencode; a few rounds cover a respawn that slips in anyway.
!macro hoxi.stopInstalledProcesses
    Push $0
    InitPluginsDir
    FileOpen $0 "$PLUGINSDIR\hoxi-stop.ps1" w
    FileWrite $0 "param([string]$$Dir)$\r$\n"
    FileWrite $0 "$$ErrorActionPreference = 'SilentlyContinue'$\r$\n"
    FileWrite $0 "if (-not $$Dir) { exit 0 }$\r$\n"
    FileWrite $0 "$$root = [IO.Path]::GetFullPath($$Dir).TrimEnd('\')$\r$\n"
    FileWrite $0 "$$names = @('${PRODUCT_EXECUTABLE}', 'opencode.exe', 'sqlite3.exe')$\r$\n"
    FileWrite $0 "for ($$i = 0; $$i -lt 5; $$i++) {$\r$\n"
    FileWrite $0 "  $$procs = @(Get-CimInstance Win32_Process | Where-Object { $$_.ExecutablePath -and ($$names -contains [IO.Path]::GetFileName($$_.ExecutablePath)) -and ([IO.Path]::GetDirectoryName($$_.ExecutablePath) -ieq $$root) } | Sort-Object { [IO.Path]::GetFileName($$_.ExecutablePath) -ine '${PRODUCT_EXECUTABLE}' })$\r$\n"
    FileWrite $0 "  if ($$procs.Count -eq 0) { exit 0 }$\r$\n"
    FileWrite $0 "  foreach ($$p in $$procs) { Write-Output ('Stopping ' + $$p.ExecutablePath + ' (PID ' + $$p.ProcessId + ')'); Stop-Process -Id $$p.ProcessId -Force }$\r$\n"
    FileWrite $0 "  Wait-Process -Id @($$procs | ForEach-Object { $$_.ProcessId }) -Timeout 10$\r$\n"
    FileWrite $0 "}$\r$\n"
    FileClose $0
    nsExec::ExecToLog '"$SYSDIR\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "$PLUGINSDIR\hoxi-stop.ps1" "$INSTDIR"'
    Pop $0
    Pop $0
!macroend

Section
    !insertmacro wails.setShellContext

    !insertmacro wails.webview2runtime

    SetOutPath $INSTDIR

    # Stop any running Hoxi instance and the opencode / sqlite3 sidecars it
    # started from $INSTDIR before replacing binaries
    nsExec::ExecToLog 'taskkill /F /IM "${PRODUCT_EXECUTABLE}"'
    !insertmacro hoxi.stopInstalledProcesses

    !insertmacro wails.files
    File "/oname=opencode.exe" "${ARG_HOXI_OPENCODE_BINARY}"
    File "/oname=sqlite3.exe" "${ARG_HOXI_SQLITE_BINARY}"
    File /nonfatal "..\..\..\LICENSE"
    File /nonfatal "..\..\..\NOTICE"
    File /nonfatal "..\..\..\THIRD_PARTY_NOTICES.md"

    CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    CreateShortCut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"

    !insertmacro wails.associateFiles
    !insertmacro wails.associateCustomProtocols

    !insertmacro wails.writeUninstaller
SectionEnd

Section "uninstall"
    !insertmacro wails.setShellContext

    nsExec::ExecToLog 'taskkill /F /IM "${PRODUCT_EXECUTABLE}"'
    !insertmacro hoxi.stopInstalledProcesses

    RMDir /r "$AppData\${PRODUCT_EXECUTABLE}"
    RMDir /r $INSTDIR

    Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
    Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"

    !insertmacro wails.unassociateFiles
    !insertmacro wails.unassociateCustomProtocols

    !insertmacro wails.deleteUninstaller
SectionEnd
