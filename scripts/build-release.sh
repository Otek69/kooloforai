#!/usr/bin/env bash
# Build Koolo AI for Windows and package it, from Linux (needs Go and NSIS: apt install nsis).
#   scripts/build-release.sh [version]
# Produces dist/KooloAI-Setup-<version>.exe (installer) and dist/KooloAI-<version>-portable.zip.
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${1:-$(git describe --tags --always 2>/dev/null || echo dev)}"
LDFLAGS="-X 'github.com/hectorgimenez/koolo/internal/config.Version=${VERSION}'"
export GOOS=windows GOARCH=amd64 CGO_ENABLED=0

rm -rf build && mkdir -p build dist
echo "Building Koolo AI ${VERSION}..."
go build -trimpath -ldflags="-s -w -H windowsgui ${LDFLAGS}" -o build/koolo.exe ./cmd/koolo
# Debug build: console window with the live log, symbols kept for readable stack traces.
go build -trimpath -ldflags="${LDFLAGS}" -o build/koolo-debug.exe ./cmd/koolo
go build -trimpath -ldflags="-s -w ${LDFLAGS}" -o build/koolo-doctor.exe ./cmd/koolo-doctor

echo "Building installer..."
makensis -V2 -DVERSION="${VERSION}" -DBUILD_DIR="../build" -DOUT_FILE="../dist/KooloAI-Setup-${VERSION}.exe" installer/koolo-ai.nsi

echo "Building portable zip..."
PORT="build/portable/KooloAI"
mkdir -p "$PORT/config"
cp build/koolo.exe build/koolo-debug.exe build/koolo-doctor.exe README.md AI-BRAIN.md DEBUGGING.md CLAUDE-DESKTOP.md LICENSE "$PORT/"
cp internal/brain/playbook.md "$PORT/brain-playbook.md"
cp -r tools "$PORT/"
cp config/Settings.json "$PORT/config/"
cp -r config/template "$PORT/config/"
cp config/koolo.yaml.dist "$PORT/config/koolo.yaml"
cp config/brain.yaml.dist "$PORT/config/brain.yaml"
cp config/brain.yaml.dist "$PORT/config/"
(cd build/portable && zip -qr "../../dist/KooloAI-${VERSION}-portable.zip" KooloAI)

ls -la dist
