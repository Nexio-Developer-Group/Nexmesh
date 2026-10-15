; Nexmesh Windows installer (NSIS, built by scripts/build-release.sh).
;
; This installs the REAL system: PostgreSQL and the Nexmesh control plane run in
; Docker Desktop, and every workspace is a real Linux container. The installer
; bundles the prebuilt Linux binaries and the compose files; Docker Desktop is
; installed with winget when it is missing. No Go, Node.js or npm is needed.
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
!define MUI_FINISHPAGE_RUN "$INSTDIR\Start Nexmesh.cmd"
!define MUI_FINISHPAGE_RUN_TEXT "Start Nexmesh now (first start builds the images and takes a few minutes)"
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
  ; Stop a running copy so its files can be replaced. Workspace data is kept in Docker volumes.
  nsExec::Exec 'powershell -NoProfile -ExecutionPolicy Bypass -File "$INSTDIR\nexmesh.ps1" stop'
  File "${SRCDIR}\nexmesh.ps1"
  File "${SRCDIR}\Start Nexmesh.cmd"
  File "${SRCDIR}\Stop Nexmesh.cmd"
  File "${SRCDIR}\Nexmesh logs.cmd"
  File "${SRCDIR}\LICENSE"
  File "${SRCDIR}\README.md"
  File "nexmesh.ico"
  SetOutPath "$INSTDIR\stack"
  File /r "${SRCDIR}\stack\*.*"
  SetOutPath "$INSTDIR\docs"
  File /r "${SRCDIR}\docs\*.*"

  CreateDirectory "$SMPROGRAMS\Nexmesh"
  CreateShortcut "$SMPROGRAMS\Nexmesh\Nexmesh.lnk" "$INSTDIR\Start Nexmesh.cmd" "" "$INSTDIR\nexmesh.ico"
  CreateShortcut "$SMPROGRAMS\Nexmesh\Stop Nexmesh.lnk" "$INSTDIR\Stop Nexmesh.cmd" "" "$INSTDIR\nexmesh.ico"
  CreateShortcut "$SMPROGRAMS\Nexmesh\Nexmesh logs.lnk" "$INSTDIR\Nexmesh logs.cmd" "" "$INSTDIR\nexmesh.ico"
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

Section "Docker Desktop (required for workspaces; skipped if already installed)" SecDocker
  nsExec::ExecToStack 'docker --version'
  Pop $0
  ${If} $0 == 0
    DetailPrint "Docker is already installed."
  ${Else}
    nsExec::ExecToStack 'winget --version'
    Pop $0
    ${If} $0 != 0
      MessageBox MB_OK|MB_ICONEXCLAMATION "Docker Desktop is required and could not be installed automatically (winget was not found).$\r$\n$\r$\nInstall it from https://www.docker.com/products/docker-desktop, restart Windows if it asks, then start Nexmesh from the Start menu."
    ${Else}
      DetailPrint "Installing Docker Desktop with winget (large download; it may ask for permission)..."
      nsExec::ExecToLog 'winget install -e --id Docker.DockerDesktop --silent --accept-package-agreements --accept-source-agreements'
      Pop $0
      DetailPrint "Docker Desktop installer finished with code $0"
      MessageBox MB_OK|MB_ICONINFORMATION "Docker Desktop was installed. Windows may ask you to restart, and Docker Desktop must be started once and its terms accepted before Nexmesh can run workspaces.$\r$\n$\r$\nAfter that, start Nexmesh from the Start menu."
    ${EndIf}
  ${EndIf}
SectionEnd

Section "Desktop shortcut" SecDesktop
  CreateShortcut "$DESKTOP\Nexmesh.lnk" "$INSTDIR\Start Nexmesh.cmd" "" "$INSTDIR\nexmesh.ico"
SectionEnd

LangString DESC_App ${LANG_ENGLISH} "Nexmesh launcher, the prebuilt control plane and workspace agent, the technology catalog and the documentation."
LangString DESC_Docker ${LANG_ENGLISH} "Workspaces and services run in real containers. Installed with winget if missing. Large download; needs virtualization and may require a restart."
LangString DESC_Desktop ${LANG_ENGLISH} "A Nexmesh icon on your desktop."
!insertmacro MUI_FUNCTION_DESCRIPTION_BEGIN
  !insertmacro MUI_DESCRIPTION_TEXT ${SecApp} $(DESC_App)
  !insertmacro MUI_DESCRIPTION_TEXT ${SecDocker} $(DESC_Docker)
  !insertmacro MUI_DESCRIPTION_TEXT ${SecDesktop} $(DESC_Desktop)
!insertmacro MUI_FUNCTION_DESCRIPTION_END

Section "Uninstall"
  ; Optionally delete every workspace and all data first (it needs the launcher still in place).
  MessageBox MB_YESNO|MB_ICONQUESTION "Also delete ALL your Nexmesh data (every workspace, its files, the database and the keys)?$\r$\n$\r$\nChoose No to keep it for a later reinstall." /SD IDNO IDNO keep
    nsExec::ExecToLog 'powershell -NoProfile -ExecutionPolicy Bypass -File "$INSTDIR\nexmesh.ps1" reset -Yes'
    Goto removed
  keep:
    nsExec::ExecToLog 'powershell -NoProfile -ExecutionPolicy Bypass -File "$INSTDIR\nexmesh.ps1" stop'
  removed:
  Delete "$DESKTOP\Nexmesh.lnk"
  RMDir /r "$SMPROGRAMS\Nexmesh"
  RMDir /r "$INSTDIR\docs"
  RMDir /r "$INSTDIR\stack"
  Delete "$INSTDIR\nexmesh.ps1"
  Delete "$INSTDIR\Start Nexmesh.cmd"
  Delete "$INSTDIR\Stop Nexmesh.cmd"
  Delete "$INSTDIR\Nexmesh logs.cmd"
  Delete "$INSTDIR\LICENSE"
  Delete "$INSTDIR\README.md"
  Delete "$INSTDIR\nexmesh.ico"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"
  DeleteRegKey HKCU "${UNINST_KEY}"
  DeleteRegKey HKCU "Software\Nexmesh"
SectionEnd
