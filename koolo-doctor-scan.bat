@echo off
REM Koolo Doctor deep scan: start D2R, enter a game with an offline character, then double-click this file.
REM Put it in the same folder as koolo-doctor.exe. It asks for administrator rights (needed to read the game).
cd /d "%~dp0"
if not exist koolo-doctor.exe (
  echo koolo-doctor.exe was not found next to this file: %~dp0
  pause
  exit /b 1
)
echo Running Koolo Doctor deep scan. This takes up to a minute...
koolo-doctor.exe --scan
