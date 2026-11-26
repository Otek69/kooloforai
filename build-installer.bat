@echo off
REM Build Koolo AI + installer on Windows. Needs Go and NSIS (https://nsis.sourceforge.io) in PATH.
cd /d "%~dp0"
if "%1"=="" (set VERSION=dev) else (set VERSION=%1)
set LDFLAGS=-X 'github.com/hectorgimenez/koolo/internal/config.Version=%VERSION%'
if exist build rmdir /s /q build
mkdir build 2>NUL & mkdir dist 2>NUL
go build -trimpath -ldflags="-s -w -H windowsgui %LDFLAGS%" -o build\koolo.exe .\cmd\koolo || goto :error
go build -trimpath -ldflags="%LDFLAGS%" -o build\koolo-debug.exe .\cmd\koolo || goto :error
go build -trimpath -ldflags="-s -w %LDFLAGS%" -o build\koolo-doctor.exe .\cmd\koolo-doctor || goto :error
makensis -V2 -DVERSION=%VERSION% -DBUILD_DIR=..\build -DOUT_FILE=..\dist\KooloAI-Setup-%VERSION%.exe installer\koolo-ai.nsi || goto :error
echo Done: dist\KooloAI-Setup-%VERSION%.exe
exit /b 0
:error
echo Build failed.
exit /b 1
