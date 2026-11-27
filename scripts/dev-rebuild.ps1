# Rebuild Koolo AI from this repo, replace the installed exes and restart Koolo.
# Used by Claude Code's fix loop (see CLAUDE.md). Needs Go installed.
#   powershell -ExecutionPolicy Bypass -File scripts/dev-rebuild.ps1 [-InstallDir <folder>] [-NoStart]
param(
  [string]$InstallDir = "$env:LOCALAPPDATA\Programs\KooloAI",
  [switch]$NoStart
)
$ErrorActionPreference = "Stop"
Set-Location (Split-Path $PSScriptRoot -Parent)

$version = "dev-" + (git rev-parse --short HEAD 2>$null)
$ldflags = "-X 'github.com/hectorgimenez/koolo/internal/config.Version=$version'"
New-Item -ItemType Directory -Force build | Out-Null

Write-Host "Building $version..."
go build -trimpath -ldflags "-s -w -H windowsgui $ldflags" -o build\koolo.exe .\cmd\koolo
if ($LASTEXITCODE -ne 0) { throw "go build koolo failed" }
go build -trimpath -ldflags "$ldflags" -o build\koolo-debug.exe .\cmd\koolo
go build -trimpath -ldflags "-s -w $ldflags" -o build\koolo-doctor.exe .\cmd\koolo-doctor
if ($LASTEXITCODE -ne 0) { throw "go build koolo-doctor failed" }
go build -trimpath -ldflags "-s -w $ldflags" -o build\koolo-extras.new.exe .\cmd\koolo-extras
if ($LASTEXITCODE -ne 0) { throw "go build koolo-extras failed" }

Write-Host "Stopping Koolo..."
Get-Process koolo, koolo-debug -ErrorAction SilentlyContinue | Stop-Process -Force
Start-Sleep -Seconds 2

if (-not (Test-Path $InstallDir)) { throw "Install folder not found: $InstallDir (use -InstallDir)" }
Copy-Item build\koolo.exe, build\koolo-debug.exe, build\koolo-doctor.exe $InstallDir -Force
# koolo-extras may be the program running this script (auto-fix), so it is replaced on its next start.
Copy-Item build\koolo-extras.new.exe "$InstallDir\koolo-extras.new.exe" -Force
Write-Host "Installed into $InstallDir"

if (-not $NoStart) {
  # Koolo asks for administrator rights (it reads game memory): accept the UAC prompt,
  # or run Claude Code from an administrator terminal to avoid it.
  Start-Process -FilePath "$InstallDir\koolo.exe" -WorkingDirectory $InstallDir
  Write-Host "Koolo restarted. The koolo MCP tools are back in a few seconds."
}
