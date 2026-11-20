@echo off
rem RealPassGAL fordítása Windowson (kell hozzá: Go 1.24+ telepítve)
go install github.com/akavel/rsrc@latest
%USERPROFILE%\go\bin\rsrc -arch amd64 -manifest realpassgal.manifest -o rsrc_windows_amd64.syso
go test ./core
go build -trimpath -ldflags "-H windowsgui -s -w" -o RealPassGAL.exe .
echo Kesz: RealPassGAL.exe
