#!/bin/sh
# Kiadás összeállítása: dist/RealPassGAL.zip + .sha256 + telepito.bat + VERZIO.txt
# A verzió a version.go-ból jön (const Version = "x.y.z").
set -e
VER=$(sed -n 's/^const Version = "\([0-9]*\.[0-9]*\.[0-9]*\)".*/\1/p' version.go)
[ -n "$VER" ] || { echo "Nem találom a verziót a version.go-ban"; exit 1; }
rm -rf dist && mkdir -p dist/pkg/hang dist/pkg/winfsp
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-H windowsgui -s -w" -o dist/pkg/RealPassGAL.exe .
cp hang/*.mp3 hang/felolvasando_szovegek.txt dist/pkg/hang/
cp docs/utmutato.html dist/pkg/RealPassGAL_utmutato.html
cp winfsp/*.msi dist/pkg/winfsp/
cp LICENSE HARMADIK_FELEK.md VALTOZASOK.txt dist/pkg/
printf '%s\n' "$VER" > dist/pkg/VERZIO.txt
(cd dist/pkg && zip -qrX ../RealPassGAL.zip .)
(cd dist && sha256sum RealPassGAL.zip > RealPassGAL.zip.sha256)
cp telepito.bat dist/
printf '%s\n' "$VER" > dist/VERZIO.txt
echo "Kész: dist/ ($VER)"; ls -la dist
