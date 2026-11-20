#!/bin/sh
# Kiadás összeállítása: dist/RealPassGAL.zip + .sha256 + telepito.bat
set -e
VER=${1:-v1.0.0}
rm -rf dist && mkdir -p dist/pkg/hang dist/pkg/winfsp
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-H windowsgui -s -w" -o dist/pkg/RealPassGAL.exe .
cp hang/*.mp3 hang/felolvasando_szovegek.txt dist/pkg/hang/
cp docs/utmutato.html dist/pkg/RealPassGAL_utmutato.html
cp winfsp/*.msi dist/pkg/winfsp/
cp LICENSE HARMADIK_FELEK.md dist/pkg/
(cd dist/pkg && zip -qr ../RealPassGAL.zip .)
(cd dist && sha256sum RealPassGAL.zip > RealPassGAL.zip.sha256)
cp telepito.bat dist/
echo "Kész: dist/ ($VER)"; ls -la dist
