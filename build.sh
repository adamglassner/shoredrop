#!/bin/sh
# Cross-builds the Windows binaries from Linux or macOS. Requires Go 1.24+.
set -e
cd "$(dirname "$0")"
mkdir -p build
go run ./tools/mkres -o cmd/shoredrop/rsrc_windows_amd64.syso -ico build/ShoreDrop.ico -name ShoreDrop -desc "ShoreDrop - FTP, FTPS and SFTP client" -file ShoreDrop.exe
go run ./tools/mkres -o cmd/setup/rsrc_windows_amd64.syso -name ShoreDrop -desc "ShoreDrop Setup" -file ShoreDrop-Setup.exe
export CGO_ENABLED=0 GOOS=windows GOARCH=amd64
go build -trimpath -ldflags "-H windowsgui -s -w" -o build/ShoreDrop.exe ./cmd/shoredrop
cp build/ShoreDrop.exe cmd/setup/payload/ShoreDrop.exe
go build -trimpath -ldflags "-H windowsgui -s -w" -o build/ShoreDrop-Setup.exe ./cmd/setup
ls -l build/*.exe
