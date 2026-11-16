Unicode true

####
## Hoxi Windows NSIS Installer (build/windows/installer/project.nsi)
##
## QQ-style minimalist 1-click installer built on MUI2 + nsDialogs:
##   - Clean white & Hoxi brand-blue card layout with a single prominent
##     " / Install Now" action button.
##   - Collapsible "  / Custom Options " drawer for changing
##     the install directory and desktop shortcut preference.
##   - Minimalist single-bar installation progress (`ShowInstDetails nevershow`)
##     followed by a one-click " / Launch Hoxi" finish screen.
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
!include "MUI2.nsh"
!include "nsDialogs.nsh"
!include "LogicLib.nsh"
!include "WinMessages.nsh"

# The version information for these two must consist of 4 numeric parts
VIProductVersion "${INFO_PRODUCTVERSION}.0"
VIFileVersion    "${INFO_PRODUCTVERSION}.0"

VIAddVersionKey "CompanyName"     "${INFO_COMPANYNAME}"
VIAddVersionKey "FileDescription" "${INFO_PRODUCTNAME} Setup"
VIAddVersionKey "ProductVersion"  "${INFO_PRODUCTVERSION}"
VIAddVersionKey "FileVersion"     "${INFO_PRODUCTVERSION}"
VIAddVersionKey "LegalCopyright"  "${INFO_COPYRIGHT}"
VIAddVersionKey "ProductName"     "${INFO_PRODUCTNAME}"

# Enable HiDPI support & flat modern visual theme
ManifestDPIAware true
XPStyle on
BrandingText " "

# Modern UI 2 light minimalist styling
!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
!define MUI_BGCOLOR "FFFFFF"
!define MUI_TEXTCOLOR "1E293B"
!define MUI_HEADERIMAGE
!define MUI_HEADERIMAGE_RIGHT
!define MUI_HEADERIMAGE_BITMAP "header.bmp"
!define MUI_WELCOMEFINISHPAGE_BITMAP "wizard.bmp"
!define MUI_INSTFILESPAGE_COLORS "FFFFFF 1E293B"

# Variables for the QQ-style 1-click custom page & finish page
Var Dialog
Var BannerCtrl
Var BannerBmp
Var TitleLabel
Var SubtitleLabel
Var InstallBtn
Var CustomToggleBtn
Var DirEdit
Var BrowseBtn
Var DesktopShortcutCheck
Var AddPathCheck
Var LaunchBigBtn
Var TitleFont
Var SubtitleFont
Var BtnFont
Var CustomExpanded
Var CreateDesktopShortcut
Var AddToUserPath

# Custom 1-click install page -> Minimalist progress bar -> Custom 1-click finish page
Page custom HoxiQuickInstallPageCreate HoxiQuickInstallPageLeave
!define MUI_PAGE_CUSTOMFUNCTION_SHOW HoxiInstFilesPageShow
!insertmacro MUI_PAGE_INSTFILES
Page custom HoxiFinishPageCreate HoxiFinishPageLeave

!insertmacro MUI_UNPAGE_INSTFILES

# SimpChinese first so Chinese Windows defaults naturally to Chinese copy, with full English support
!insertmacro MUI_LANGUAGE "SimpChinese"
!insertmacro MUI_LANGUAGE "English"

LangString HOXI_SUBTITLE ${LANG_SIMPCHINESE} "$\{U+6781\}$\{U+901F\}$\{U+3001\}$\{U+8F7B\}$\{U+91CF\}$\{U+7684\}$\{U+539F\}$\{U+751F\} AI $\{U+5DE5\}$\{U+7A0B\}$\{U+6307\}$\{U+6325\}$\{U+4E2D\}$\{U+5FC3\}"
LangString HOXI_SUBTITLE ${LANG_ENGLISH}     "Fast, minimalist command center for parallel AI engineering"

LangString HOXI_INSTALL_NOW ${LANG_SIMPCHINESE} "$\{U+7ACB\}$\{U+5373\}$\{U+5B89\}$\{U+88C5\}"
LangString HOXI_INSTALL_NOW ${LANG_ENGLISH}     "Install Now"

LangString HOXI_CUSTOM_COLLAPSED ${LANG_SIMPCHINESE} "$\{U+81EA\}$\{U+5B9A\}$\{U+4E49\}$\{U+9009\}$\{U+9879\} $\{U+25BE\}"
LangString HOXI_CUSTOM_COLLAPSED ${LANG_ENGLISH}     "Custom Options $\{U+25BE\}"

LangString HOXI_CUSTOM_EXPANDED ${LANG_SIMPCHINESE} "$\{U+6536\}$\{U+8D77\}$\{U+9009\}$\{U+9879\} $\{U+25B4\}"
LangString HOXI_CUSTOM_EXPANDED ${LANG_ENGLISH}     "Hide Options $\{U+25B4\}"

LangString HOXI_BROWSE_BTN ${LANG_SIMPCHINESE} "$\{U+6D4F\}$\{U+89C8\}..."
LangString HOXI_BROWSE_BTN ${LANG_ENGLISH}     "Browse..."

LangString HOXI_BROWSE_TITLE ${LANG_SIMPCHINESE} "$\{U+9009\}$\{U+62E9\} ${INFO_PRODUCTNAME} $\{U+5B89\}$\{U+88C5\}$\{U+76EE\}$\{U+5F55\}"
LangString HOXI_BROWSE_TITLE ${LANG_ENGLISH}     "Select ${INFO_PRODUCTNAME} installation folder"

LangString HOXI_OPT_DESKTOP ${LANG_SIMPCHINESE} "$\{U+521B\}$\{U+5EFA\}$\{U+684C\}$\{U+9762\}$\{U+5FEB\}$\{U+6377\}$\{U+65B9\}$\{U+5F0F\}"
LangString HOXI_OPT_DESKTOP ${LANG_ENGLISH}     "Create desktop shortcut"

LangString HOXI_OPT_PATH ${LANG_SIMPCHINESE} "$\{U+6DFB\}$\{U+52A0\}$\{U+5230\}$\{U+7528\}$\{U+6237\} PATH $\{U+73AF\}$\{U+5883\}$\{U+53D8\}$\{U+91CF\}"
LangString HOXI_OPT_PATH ${LANG_ENGLISH}     "Add to user PATH environment variable"

LangString HOXI_INSTALLING_TITLE ${LANG_SIMPCHINESE} "$\{U+6B63\}$\{U+5728\}$\{U+5B89\}$\{U+88C5\} ${INFO_PRODUCTNAME}..."
LangString HOXI_INSTALLING_TITLE ${LANG_ENGLISH}     "Installing ${INFO_PRODUCTNAME}..."

LangString HOXI_INSTALLING_SUBTITLE ${LANG_SIMPCHINESE} "$\{U+6B63\}$\{U+5728\}$\{U+914D\}$\{U+7F6E\}$\{U+8FD0\}$\{U+884C\}$\{U+73AF\}$\{U+5883\}$\{U+FF0C\}$\{U+8BF7\}$\{U+7A0D\}$\{U+5019\}..."
LangString HOXI_INSTALLING_SUBTITLE ${LANG_ENGLISH}     "Setting up your workspace, please wait..."

LangString HOXI_FINISH_TITLE ${LANG_SIMPCHINESE} "${INFO_PRODUCTNAME} $\{U+5B89\}$\{U+88C5\}$\{U+5B8C\}$\{U+6210\}"
LangString HOXI_FINISH_TITLE ${LANG_ENGLISH}     "${INFO_PRODUCTNAME} is Ready"

LangString HOXI_FINISH_SUBTITLE ${LANG_SIMPCHINESE} "$\{U+4E00\}$\{U+5207\}$\{U+51C6\}$\{U+5907\}$\{U+5C31\}$\{U+7EEA\}$\{U+FF0C\}$\{U+5373\}$\{U+523B\}$\{U+5F00\}$\{U+542F\}$\{U+9AD8\}$\{U+6548\} AI $\{U+5DE5\}$\{U+7A0B\}$\{U+4F53\}$\{U+9A8C\}"
LangString HOXI_FINISH_SUBTITLE ${LANG_ENGLISH}     "Everything is set up and ready to go"

LangString HOXI_LAUNCH_NOW ${LANG_SIMPCHINESE} "$\{U+7ACB\}$\{U+5373\}$\{U+4F53\}$\{U+9A8C\}"
LangString HOXI_LAUNCH_NOW ${LANG_ENGLISH}     "Launch ${INFO_PRODUCTNAME}"

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

# Hide verbose file extraction log for a clean QQ-style single progress bar
ShowInstDetails nevershow
ShowUninstDetails nevershow

Function .onInit
    !insertmacro wails.checkArchitecture
    InitPluginsDir
    File "/oname=$PLUGINSDIR\banner.bmp" "banner.bmp"
    StrCpy $CustomExpanded "0"
    StrCpy $CreateDesktopShortcut "${BST_CHECKED}"
    StrCpy $AddToUserPath "${BST_CHECKED}"
FunctionEnd

!macro hoxi.hideStandardWizardFooter
    # Hide standard Back / Next / Cancel footer buttons and bottom divider
    # so the custom page renders as a clean full-bleed QQ-style card.
    GetDlgItem $0 $HWNDPARENT 1
    ShowWindow $0 ${SW_HIDE}
    GetDlgItem $0 $HWNDPARENT 2
    ShowWindow $0 ${SW_HIDE}
    GetDlgItem $0 $HWNDPARENT 3
    ShowWindow $0 ${SW_HIDE}
    GetDlgItem $0 $HWNDPARENT 1034
    ShowWindow $0 ${SW_HIDE}
    GetDlgItem $0 $HWNDPARENT 1035
    ShowWindow $0 ${SW_HIDE}
    GetDlgItem $0 $HWNDPARENT 1036
    ShowWindow $0 ${SW_HIDE}
    GetDlgItem $0 $HWNDPARENT 1037
    ShowWindow $0 ${SW_HIDE}
    GetDlgItem $0 $HWNDPARENT 1038
    ShowWindow $0 ${SW_HIDE}
    GetDlgItem $0 $HWNDPARENT 1039
    ShowWindow $0 ${SW_HIDE}
    GetDlgItem $0 $HWNDPARENT 1045
    ShowWindow $0 ${SW_HIDE}
    GetDlgItem $0 $HWNDPARENT 1256
    ShowWindow $0 ${SW_HIDE}
!macroend

Function HoxiQuickInstallPageCreate
    !insertmacro hoxi.hideStandardWizardFooter

    # Use dialog 1044 (full-window area) for a unified borderless white surface
    nsDialogs::Create 1044
    Pop $Dialog
    ${If} $Dialog == error
        Abort
    ${EndIf}
    SetCtlColors $Dialog 0x1E293B 0xFFFFFF
    SetCtlColors $HWNDPARENT 0x1E293B 0xFFFFFF

    # Top hero brand banner (stretched cleanly across top of card)
    ${NSD_CreateBitmap} 0u 0u 100% 82u ""
    Pop $BannerCtrl
    ${NSD_SetStretchedImage} $BannerCtrl "$PLUGINSDIR\banner.bmp" $BannerBmp

    # Product title & version
    ${NSD_CreateLabel} 0u 88u 100% 20u "${INFO_PRODUCTNAME}  v${INFO_PRODUCTVERSION}"
    Pop $TitleLabel
    ${NSD_AddStyle} $TitleLabel ${SS_CENTER}
    CreateFont $TitleFont "Microsoft YaHei UI" "15" "700"
    SendMessage $TitleLabel ${WM_SETFONT} $TitleFont 1
    SetCtlColors $TitleLabel 0x0F172A 0xFFFFFF

    # Subtitle tagline
    ${NSD_CreateLabel} 0u 110u 100% 14u "$(HOXI_SUBTITLE)"
    Pop $SubtitleLabel
    ${NSD_AddStyle} $SubtitleLabel ${SS_CENTER}
    CreateFont $SubtitleFont "Microsoft YaHei UI" "9" "400"
    SendMessage $SubtitleLabel ${WM_SETFONT} $SubtitleFont 1
    SetCtlColors $SubtitleLabel 0x64748B 0xFFFFFF

    # Prominent centered 1-click " / Install Now" button
    ${NSD_CreateButton} 102u 132u 126u 26u "$(HOXI_INSTALL_NOW)"
    Pop $InstallBtn
    CreateFont $BtnFont "Microsoft YaHei UI" "11" "700"
    SendMessage $InstallBtn ${WM_SETFONT} $BtnFont 1
    ${NSD_OnClick} $InstallBtn HoxiOnClickInstallNow

    # Subtle bottom toggle for "  / Custom Options "
    ${NSD_CreateButton} 125u 164u 80u 14u "$(HOXI_CUSTOM_COLLAPSED)"
    Pop $CustomToggleBtn
    SendMessage $CustomToggleBtn ${WM_SETFONT} $SubtitleFont 1
    ${NSD_OnClick} $CustomToggleBtn HoxiOnToggleCustomOptions

    # Collapsible custom options drawer (hidden until user clicks " ")
    ${NSD_CreateDirRequest} 28u 184u 222u 14u "$INSTDIR"
    Pop $DirEdit
    SendMessage $DirEdit ${WM_SETFONT} $SubtitleFont 1
    ShowWindow $DirEdit ${SW_HIDE}

    ${NSD_CreateBrowseButton} 254u 183u 48u 16u "$(HOXI_BROWSE_BTN)"
    Pop $BrowseBtn
    SendMessage $BrowseBtn ${WM_SETFONT} $SubtitleFont 1
    ${NSD_OnClick} $BrowseBtn HoxiOnClickBrowseDir
    ShowWindow $BrowseBtn ${SW_HIDE}

    ${NSD_CreateCheckBox} 28u 203u 125u 12u "$(HOXI_OPT_DESKTOP)"
    Pop $DesktopShortcutCheck
    SendMessage $DesktopShortcutCheck ${WM_SETFONT} $SubtitleFont 1
    SetCtlColors $DesktopShortcutCheck 0x475569 0xFFFFFF
    ${NSD_SetState} $DesktopShortcutCheck $CreateDesktopShortcut
    ShowWindow $DesktopShortcutCheck ${SW_HIDE}

    ${NSD_CreateCheckBox} 160u 203u 142u 12u "$(HOXI_OPT_PATH)"
    Pop $AddPathCheck
    SendMessage $AddPathCheck ${WM_SETFONT} $SubtitleFont 1
    SetCtlColors $AddPathCheck 0x475569 0xFFFFFF
    ${NSD_SetState} $AddPathCheck $AddToUserPath
    ShowWindow $AddPathCheck ${SW_HIDE}

    ${NSD_SetFocus} $InstallBtn
    nsDialogs::Show
    ${NSD_FreeImage} $BannerBmp
FunctionEnd

Function HoxiOnToggleCustomOptions
    Pop $0
    ${If} $CustomExpanded == "0"
        StrCpy $CustomExpanded "1"
        ${NSD_SetText} $CustomToggleBtn "$(HOXI_CUSTOM_EXPANDED)"
        ShowWindow $DirEdit ${SW_SHOW}
        ShowWindow $BrowseBtn ${SW_SHOW}
        ShowWindow $DesktopShortcutCheck ${SW_SHOW}
        ShowWindow $AddPathCheck ${SW_SHOW}
    ${Else}
        StrCpy $CustomExpanded "0"
        ${NSD_SetText} $CustomToggleBtn "$(HOXI_CUSTOM_COLLAPSED)"
        ShowWindow $DirEdit ${SW_HIDE}
        ShowWindow $BrowseBtn ${SW_HIDE}
        ShowWindow $DesktopShortcutCheck ${SW_HIDE}
        ShowWindow $AddPathCheck ${SW_HIDE}
    ${EndIf}
FunctionEnd

Function HoxiOnClickBrowseDir
    Pop $0
    ${NSD_GetText} $DirEdit $1
    nsDialogs::SelectFolderDialog "$(HOXI_BROWSE_TITLE)" "$1"
    Pop $2
    ${If} $2 != "error"
    ${AndIf} $2 != ""
        ${NSD_SetText} $DirEdit "$2"
    ${EndIf}
FunctionEnd

Function HoxiOnClickInstallNow
    Pop $0
    ${NSD_GetText} $DirEdit $INSTDIR
    ${NSD_GetState} $DesktopShortcutCheck $CreateDesktopShortcut
    ${NSD_GetState} $AddPathCheck $AddToUserPath
    # Trigger standard Next navigation to advance directly into the progress page
    SendMessage $HWNDPARENT ${WM_COMMAND} 1 0
FunctionEnd

Function HoxiQuickInstallPageLeave
    ${NSD_GetText} $DirEdit $INSTDIR
    ${NSD_GetState} $DesktopShortcutCheck $CreateDesktopShortcut
    ${NSD_GetState} $AddPathCheck $AddToUserPath
FunctionEnd

Function HoxiInstFilesPageShow
    !insertmacro MUI_HEADER_TEXT "$(HOXI_INSTALLING_TITLE)" "$(HOXI_INSTALLING_SUBTITLE)"
    # Keep progress page clean: hide Back/Next/Cancel while installing
    GetDlgItem $0 $HWNDPARENT 1
    ShowWindow $0 ${SW_HIDE}
    GetDlgItem $0 $HWNDPARENT 2
    ShowWindow $0 ${SW_HIDE}
    GetDlgItem $0 $HWNDPARENT 3
    ShowWindow $0 ${SW_HIDE}
FunctionEnd

Function HoxiFinishPageCreate
    !insertmacro hoxi.hideStandardWizardFooter

    nsDialogs::Create 1044
    Pop $Dialog
    ${If} $Dialog == error
        Abort
    ${EndIf}
    SetCtlColors $Dialog 0x1E293B 0xFFFFFF
    SetCtlColors $HWNDPARENT 0x1E293B 0xFFFFFF

    ${NSD_CreateBitmap} 0u 0u 100% 82u ""
    Pop $BannerCtrl
    ${NSD_SetStretchedImage} $BannerCtrl "$PLUGINSDIR\banner.bmp" $BannerBmp

    ${NSD_CreateLabel} 0u 92u 100% 20u "$(HOXI_FINISH_TITLE)"
    Pop $TitleLabel
    ${NSD_AddStyle} $TitleLabel ${SS_CENTER}
    CreateFont $TitleFont "Microsoft YaHei UI" "15" "700"
    SendMessage $TitleLabel ${WM_SETFONT} $TitleFont 1
    SetCtlColors $TitleLabel 0x0F172A 0xFFFFFF

    ${NSD_CreateLabel} 0u 115u 100% 14u "$(HOXI_FINISH_SUBTITLE)"
    Pop $SubtitleLabel
    ${NSD_AddStyle} $SubtitleLabel ${SS_CENTER}
    CreateFont $SubtitleFont "Microsoft YaHei UI" "9" "400"
    SendMessage $SubtitleLabel ${WM_SETFONT} $SubtitleFont 1
    SetCtlColors $SubtitleLabel 0x64748B 0xFFFFFF

    ${NSD_CreateButton} 102u 142u 126u 26u "$(HOXI_LAUNCH_NOW)"
    Pop $LaunchBigBtn
    CreateFont $BtnFont "Microsoft YaHei UI" "11" "700"
    SendMessage $LaunchBigBtn ${WM_SETFONT} $BtnFont 1
    ${NSD_OnClick} $LaunchBigBtn HoxiOnClickLaunchNow

    ${NSD_SetFocus} $LaunchBigBtn
    nsDialogs::Show
    ${NSD_FreeImage} $BannerBmp
FunctionEnd

Function HoxiOnClickLaunchNow
    Pop $0
    ExecShell "" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    SendMessage $HWNDPARENT ${WM_COMMAND} 1 0
FunctionEnd

Function HoxiFinishPageLeave
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

!macro hoxi.addInstallDirToUserPath
    Push $0
    InitPluginsDir
    FileOpen $0 "$PLUGINSDIR\hoxi-path.ps1" w
    FileWrite $0 "param([string]$$Dir)$\r$\n"
    FileWrite $0 "$$ErrorActionPreference = 'SilentlyContinue'$\r$\n"
    FileWrite $0 "if (-not $$Dir) { exit 0 }$\r$\n"
    FileWrite $0 "$$norm = $$Dir.Trim().TrimEnd('\')$\r$\n"
    FileWrite $0 "$$cur = [Environment]::GetEnvironmentVariable('Path', 'User')$\r$\n"
    FileWrite $0 "$$parts = if ([string]::IsNullOrWhiteSpace($$cur)) { @() } else { $$cur -split ';' }$\r$\n"
    FileWrite $0 "foreach ($$p in $$parts) { if ($$p.Trim().TrimEnd('\') -ieq $$norm) { exit 0 } }$\r$\n"
    FileWrite $0 "$$next = if ([string]::IsNullOrWhiteSpace($$cur)) { $$Dir } else { $$cur.TrimEnd(';') + ';' + $$Dir }$\r$\n"
    FileWrite $0 "[Environment]::SetEnvironmentVariable('Path', $$next, 'User')$\r$\n"
    FileClose $0
    nsExec::ExecToLog '"$SYSDIR\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "$PLUGINSDIR\hoxi-path.ps1" "$INSTDIR"'
    Pop $0
    Pop $0
!macroend

Section
    SetAutoClose true
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
    ${If} $CreateDesktopShortcut == "${BST_CHECKED}"
        CreateShortCut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    ${EndIf}
    ${If} $AddToUserPath == "${BST_CHECKED}"
        !insertmacro hoxi.addInstallDirToUserPath
    ${EndIf}

    !insertmacro wails.associateFiles
    !insertmacro wails.associateCustomProtocols

    !insertmacro wails.writeUninstaller
SectionEnd

Section "uninstall"
    SetAutoClose true
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
