; Nexmesh Windows installer. Built with NSIS (makensis) by scripts/build-release.sh.
; The installer is self-contained: nexmesh.exe already embeds the web app and the
; technology catalog, so no Go, Node.js or npm is needed to run it. The optional
; sections install developer tools through winget when you want to build or
; extend Nexmesh itself.
Unicode true
!include "MUI2.nsh"
!include "LogicLib.nsh"
!include "FileFunc.nsh"

!ifndef VERSION
  !define VERSION "0.0.0-dev"
!endif
!ifndef SRCDIR
  !define SRCDIR "..\..\dist\windows"
!endif
!define APP "Nexmesh"
!define UNINST_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\Nexmesh"

Name "${APP} ${VERSION}"
OutFile "${OUTFILE}"
InstallDir "$LOCALAPPDATA\Programs\Nexmesh"
InstallDirRegKey HKCU "Software\Nexmesh" "InstallDir"
RequestExecutionLevel user
SetCompressor /SOLID lzma
BrandingText "Nexmesh ${VERSION}"

!define MUI_ICON "nexmesh.ico"
!define MUI_UNICON "nexmesh.ico"
!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_RUN "$INSTDIR\nexmesh.exe"
!define MUI_FINISHPAGE_RUN_PARAMETERS "--local"
!define MUI_FINISHPAGE_RUN_TEXT "Start Nexmesh now"
!define MUI_FINISHPAGE_SHOWREADME "$INSTDIR\docs\INSTALL.md"
!define MUI_FINISHPAGE_SHOWREADME_TEXT "Open the setup guide"

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_LICENSE "${SRCDIR}\LICENSE"
!insertmacro MUI_PAGE_COMPONENTS
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

Section "Nexmesh (required)" SecApp
  SectionIn RO
  SetOutPath "$INSTDIR"
  ; Stop a running copy so its files can be replaced.
  nsExec::Exec 'taskkill /F /IM nexmesh.exe'
  File "${SRCDIR}\nexmesh.exe"
  File "${SRCDIR}\LICENSE"
  File "${SRCDIR}\README.md"
  File "nexmesh.ico"
  SetOutPath "$INSTDIR\docs"
  File /r "${SRCDIR}\docs\*.*"

  CreateDirectory "$SMPROGRAMS\Nexmesh"
  CreateShortcut "$SMPROGRAMS\Nexmesh\Nexmesh.lnk" "$INSTDIR\nexmesh.exe" "--local" "$INSTDIR\nexmesh.ico"
  CreateShortcut "$SMPROGRAMS\Nexmesh\Setup guide.lnk" "$INSTDIR\docs\INSTALL.md"
  CreateShortcut "$SMPROGRAMS\Nexmesh\Uninstall Nexmesh.lnk" "$INSTDIR\uninstall.exe"
  WriteUninstaller "$INSTDIR\uninstall.exe"

  WriteRegStr HKCU "Software\Nexmesh" "InstallDir" "$INSTDIR"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayName" "Nexmesh"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINST_KEY}" "Publisher" "Nexio Developer Group"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayIcon" "$INSTDIR\nexmesh.ico"
  WriteRegStr HKCU "${UNINST_KEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegStr HKCU "${UNINST_KEY}" "QuietUninstallString" '"$INSTDIR\uninstall.exe" /S'
  WriteRegDWORD HKCU "${UNINST_KEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINST_KEY}" "NoRepair" 1
  ${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
  IntFmt $0 "0x%08X" $0
  WriteRegDWORD HKCU "${UNINST_KEY}" "EstimatedSize" "$0"
SectionEnd

Section "Desktop shortcut" SecDesktop
  CreateShortcut "$DESKTOP\Nexmesh.lnk" "$INSTDIR\nexmesh.exe" "--local" "$INSTDIR\nexmesh.ico"
SectionEnd

SectionGroup /e "Developer tools (optional, through winget)" SecDev
  Section /o "Git" SecGit
    Call RequireWinget
    nsExec::ExecToLog 'winget install -e --id Git.Git --silent --accept-package-agreements --accept-source-agreements'
    Pop $0
    DetailPrint "Git installer finished with code $0"
  SectionEnd
  Section /o "Go" SecGo
    Call RequireWinget
    nsExec::ExecToLog 'winget install -e --id GoLang.Go --silent --accept-package-agreements --accept-source-agreements'
    Pop $0
    DetailPrint "Go installer finished with code $0"
  SectionEnd
  Section /o "Node.js LTS and pnpm" SecNode
    Call RequireWinget
    nsExec::ExecToLog 'winget install -e --id OpenJS.NodeJS.LTS --silent --accept-package-agreements --accept-source-agreements'
    Pop $0
    DetailPrint "Node.js installer finished with code $0"
    nsExec::ExecToLog 'cmd /C corepack enable'
    Pop $0
  SectionEnd
  Section /o "Docker Desktop (real containers instead of the simulated runtime)" SecDocker
    Call RequireWinget
    nsExec::ExecToLog 'winget install -e --id Docker.DockerDesktop --silent --accept-package-agreements --accept-source-agreements'
    Pop $0
    DetailPrint "Docker Desktop installer finished with code $0 (a restart may be needed)"
  SectionEnd
SectionGroupEnd

Function RequireWinget
  nsExec::ExecToStack 'winget --version'
  Pop $0
  ${If} $0 != 0
    MessageBox MB_OK|MB_ICONEXCLAMATION "winget (App Installer) was not found, so this tool could not be installed automatically. Install it from the Microsoft Store, or install the tool by hand; Nexmesh itself does not need it."
    Abort
  ${EndIf}
FunctionEnd

LangString DESC_App ${LANG_ENGLISH} "Nexmesh, its web app and technology catalog, and the documentation."
LangString DESC_Desktop ${LANG_ENGLISH} "A Nexmesh icon on your desktop."
LangString DESC_Dev ${LANG_ENGLISH} "Only needed to build or modify Nexmesh from source. Installed with winget and may ask for permission."
LangString DESC_Docker ${LANG_ENGLISH} "Lets workspaces run in real containers. Large download; needs virtualization and may require a restart."
!insertmacro MUI_FUNCTION_DESCRIPTION_BEGIN
  !insertmacro MUI_DESCRIPTION_TEXT ${SecApp} $(DESC_App)
  !insertmacro MUI_DESCRIPTION_TEXT ${SecDesktop} $(DESC_Desktop)
  !insertmacro MUI_DESCRIPTION_TEXT ${SecDev} $(DESC_Dev)
  !insertmacro MUI_DESCRIPTION_TEXT ${SecDocker} $(DESC_Docker)
!insertmacro MUI_FUNCTION_DESCRIPTION_END

Section "Uninstall"
  nsExec::Exec 'taskkill /F /IM nexmesh.exe'
  Delete "$DESKTOP\Nexmesh.lnk"
  RMDir /r "$SMPROGRAMS\Nexmesh"
  RMDir /r "$INSTDIR\docs"
  Delete "$INSTDIR\nexmesh.exe"
  Delete "$INSTDIR\LICENSE"
  Delete "$INSTDIR\README.md"
  Delete "$INSTDIR\nexmesh.ico"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"
  DeleteRegKey HKCU "${UNINST_KEY}"
  DeleteRegKey HKCU "Software\Nexmesh"
  ; Your workspaces and settings live in %LOCALAPPDATA%\Nexmesh and are kept.
  MessageBox MB_YESNO|MB_ICONQUESTION "Also delete your Nexmesh data (workspaces, files, saved settings and the master key)?$\r$\n$\r$\nChoose No to keep it for a later reinstall." /SD IDNO IDNO keep
    RMDir /r "$LOCALAPPDATA\Nexmesh"
  keep:
SectionEnd
