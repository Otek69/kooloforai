; Koolo AI installer (NSIS 3). Built by scripts/build-release.sh (Linux) or build-installer.bat (Windows).
; Installs per user into %LOCALAPPDATA%\Programs\KooloAI (no admin needed to install; Koolo asks for
; admin itself when it starts, because it reads the game's memory). Your settings, logs and AI memory are
; kept on update and, unless you choose otherwise, on uninstall.

Unicode true
!include "MUI2.nsh"
!include "LogicLib.nsh"

!ifndef VERSION
  !define VERSION "dev"
!endif
!ifndef BUILD_DIR
  !define BUILD_DIR "..\build"
!endif
!ifndef OUT_FILE
  !define OUT_FILE "..\dist\KooloAI-Setup-${VERSION}.exe"
!endif

!define APP "Koolo AI"
!define UNINST_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\KooloAI"

Name "${APP} ${VERSION}"
OutFile "${OUT_FILE}"
InstallDir "$LOCALAPPDATA\Programs\KooloAI"
InstallDirRegKey HKCU "Software\KooloAI" "InstallDir"
RequestExecutionLevel user
SetCompressor /SOLID lzma
BrandingText "${APP} ${VERSION}"

!define MUI_ICON "..\cmd\koolo\koolo.ico"
!define MUI_UNICON "..\cmd\koolo\koolo.ico"
!define MUI_ABORTWARNING
!define MUI_WELCOMEPAGE_TEXT "This installs ${APP} ${VERSION}: Koolo with an AI brain (Claude or a free local model).$\r$\n$\r$\nUse it with OFFLINE characters only.$\r$\n$\r$\nIncluded: Koolo AI, a debug mode with a live log console, and Koolo Doctor, which checks your setup and makes a debug bundle when something goes wrong."
!define MUI_FINISHPAGE_RUN "$INSTDIR\koolo-doctor.exe"
!define MUI_FINISHPAGE_RUN_TEXT "Run Koolo Doctor to check the setup"
!define MUI_FINISHPAGE_SHOWREADME "$INSTDIR\AI-BRAIN.md"
!define MUI_FINISHPAGE_SHOWREADME_TEXT "Open the AI setup guide"

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_LICENSE "..\LICENSE"
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_COMPONENTS
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

VIProductVersion "0.0.0.0"
VIAddVersionKey "ProductName" "${APP}"
VIAddVersionKey "FileDescription" "${APP} installer"
VIAddVersionKey "ProductVersion" "${VERSION}"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "LegalCopyright" "MIT License"

; Copy a config template only if the user doesn't have that file yet (never overwrite settings).
!macro InstallConfigOnce SRC DEST
  ${IfNot} ${FileExists} "$INSTDIR\config\${DEST}"
    File "/oname=config\${DEST}" "${SRC}"
  ${EndIf}
!macroend

Section "Koolo AI (required)" SecMain
  SectionIn RO
  SetOutPath "$INSTDIR"
  File "${BUILD_DIR}\koolo.exe"
  File "${BUILD_DIR}\koolo-debug.exe"
  File "${BUILD_DIR}\koolo-doctor.exe"
  File "${BUILD_DIR}\koolo-extras.exe"
  File "..\koolo-doctor-scan.bat"
  File "..\README.md"
  File "..\AI-BRAIN.md"
  File "..\DEBUGGING.md"
  File "..\CLAUDE-DESKTOP.md"
  File "..\EXTRAS.md"
  File "/oname=brain-playbook.md" "..\internal\brain\playbook.md"
  File "..\LICENSE"

  SetOutPath "$INSTDIR\tools"
  File /r "..\tools\*.*"

  SetOutPath "$INSTDIR\config"
  File "..\config\Settings.json"
  SetOutPath "$INSTDIR\config\template"
  File /r "..\config\template\*.*"
  SetOutPath "$INSTDIR"
  !insertmacro InstallConfigOnce "..\config\koolo.yaml.dist" "koolo.yaml"
  !insertmacro InstallConfigOnce "..\config\brain.yaml.dist" "brain.yaml"
  !insertmacro InstallConfigOnce "..\config\extras.yaml.dist" "extras.yaml"
  File "/oname=config\extras.yaml.dist" "..\config\extras.yaml.dist"
  File "/oname=config\brain.yaml.dist" "..\config\brain.yaml.dist"

  CreateDirectory "$INSTDIR\logs"
  CreateDirectory "$INSTDIR\diagnostics"

  ; Start menu
  CreateDirectory "$SMPROGRAMS\${APP}"
  CreateShortcut "$SMPROGRAMS\${APP}\Koolo AI.lnk" "$INSTDIR\koolo.exe" "" "$INSTDIR\koolo.exe" 0
  CreateShortcut "$SMPROGRAMS\${APP}\Koolo AI (debug mode).lnk" "$INSTDIR\koolo-debug.exe" "--debug" "$INSTDIR\koolo.exe" 0
  CreateShortcut "$SMPROGRAMS\${APP}\Koolo Doctor.lnk" "$INSTDIR\koolo-doctor.exe" "" "$INSTDIR\koolo.exe" 0
  CreateShortcut "$SMPROGRAMS\${APP}\Koolo Doctor - make debug bundle.lnk" "$INSTDIR\koolo-doctor.exe" "--bundle" "$INSTDIR\koolo.exe" 0
  CreateShortcut "$SMPROGRAMS\${APP}\Koolo Doctor - deep scan.lnk" "$INSTDIR\koolo-doctor.exe" "--scan" "$INSTDIR\koolo.exe" 0
  CreateShortcut "$SMPROGRAMS\${APP}\Open Koolo AI folder.lnk" "$INSTDIR"
  CreateShortcut "$SMPROGRAMS\${APP}\AI setup guide.lnk" "$INSTDIR\AI-BRAIN.md"
  CreateShortcut "$SMPROGRAMS\${APP}\Debugging help.lnk" "$INSTDIR\DEBUGGING.md"
  CreateShortcut "$SMPROGRAMS\${APP}\Claude Desktop brain setup.lnk" "$INSTDIR\CLAUDE-DESKTOP.md"
  CreateShortcut "$SMPROGRAMS\${APP}\Koolo AI Extras.lnk" "$INSTDIR\koolo-extras.exe" "" "$INSTDIR\koolo.exe" 0
  CreateShortcut "$SMPROGRAMS\${APP}\Extras dashboard.lnk" "http://localhost:8088/"
  CreateShortcut "$SMPROGRAMS\${APP}\Extras guide.lnk" "$INSTDIR\EXTRAS.md"
  CreateShortcut "$SMPROGRAMS\${APP}\Uninstall Koolo AI.lnk" "$INSTDIR\uninstall.exe"

  ; Uninstall entry (per user)
  WriteUninstaller "$INSTDIR\uninstall.exe"
  WriteRegStr HKCU "Software\KooloAI" "InstallDir" "$INSTDIR"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayName" "${APP}"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINST_KEY}" "Publisher" "kooloforai"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayIcon" "$INSTDIR\koolo.exe"
  WriteRegStr HKCU "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINST_KEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegDWORD HKCU "${UNINST_KEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINST_KEY}" "NoRepair" 1
SectionEnd

Section "Desktop shortcut" SecDesktop
  CreateShortcut "$DESKTOP\Koolo AI.lnk" "$INSTDIR\koolo.exe" "" "$INSTDIR\koolo.exe" 0
SectionEnd

LangString DESC_SecMain ${LANG_ENGLISH} "Koolo AI, debug mode, Koolo Doctor, guides and config templates."
LangString DESC_SecDesktop ${LANG_ENGLISH} "A Koolo AI shortcut on the desktop."
!insertmacro MUI_FUNCTION_DESCRIPTION_BEGIN
  !insertmacro MUI_DESCRIPTION_TEXT ${SecMain} $(DESC_SecMain)
  !insertmacro MUI_DESCRIPTION_TEXT ${SecDesktop} $(DESC_SecDesktop)
!insertmacro MUI_FUNCTION_DESCRIPTION_END

Section "Uninstall"
  Delete "$INSTDIR\koolo.exe"
  Delete "$INSTDIR\koolo-debug.exe"
  Delete "$INSTDIR\koolo-doctor.exe"
  Delete "$INSTDIR\koolo-extras.exe"
  Delete "$INSTDIR\koolo-doctor-scan.bat"
  Delete "$INSTDIR\EXTRAS.md"
  Delete "$INSTDIR\config\extras.yaml.dist"
  Delete "$INSTDIR\README.md"
  Delete "$INSTDIR\AI-BRAIN.md"
  Delete "$INSTDIR\DEBUGGING.md"
  Delete "$INSTDIR\CLAUDE-DESKTOP.md"
  Delete "$INSTDIR\brain-playbook.md"
  Delete "$INSTDIR\LICENSE"
  Delete "$INSTDIR\uninstall.exe"
  RMDir /r "$INSTDIR\tools"
  RMDir /r "$INSTDIR\config\template"
  Delete "$INSTDIR\config\Settings.json"
  Delete "$INSTDIR\config\brain.yaml.dist"

  MessageBox MB_YESNO|MB_ICONQUESTION|MB_DEFBUTTON2 "Also delete your settings, characters, logs, debug reports and the AI's memory?$\r$\n$\r$\nChoose No to keep them for a later reinstall." IDNO keep
    RMDir /r "$INSTDIR\config"
    RMDir /r "$INSTDIR\logs"
    RMDir /r "$INSTDIR\diagnostics"
    RMDir /r "$INSTDIR\crash-reports"
    RMDir /r "$INSTDIR\screenshots"
    RMDir /r "$INSTDIR\brain"
    RMDir /r "$INSTDIR\extras"
  keep:
  RMDir "$INSTDIR"

  RMDir /r "$SMPROGRAMS\${APP}"
  Delete "$DESKTOP\Koolo AI.lnk"
  DeleteRegKey HKCU "${UNINST_KEY}"
  DeleteRegKey HKCU "Software\KooloAI"
SectionEnd
