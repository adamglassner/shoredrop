# Builds ShoreDrop.exe and ShoreDrop-Setup.exe on Windows.
# Requires Go 1.24 or newer (https://go.dev/dl/) — only for building; users need nothing.
$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot
New-Item -ItemType Directory -Force build | Out-Null
$env:CGO_ENABLED = "0"
$env:GOOS = "windows"
$env:GOARCH = "amd64"

Write-Host "Generating icon, manifest and version resources..."
go run ./tools/mkres -o cmd/shoredrop/rsrc_windows_amd64.syso -ico build/ShoreDrop.ico -name ShoreDrop -desc "ShoreDrop - FTP, FTPS and SFTP client" -file ShoreDrop.exe
go run ./tools/mkres -o cmd/setup/rsrc_windows_amd64.syso -name ShoreDrop -desc "ShoreDrop Setup" -file ShoreDrop-Setup.exe

Write-Host "Building ShoreDrop.exe..."
go build -trimpath -ldflags "-H windowsgui -s -w" -o build/ShoreDrop.exe ./cmd/shoredrop

Write-Host "Building ShoreDrop-Setup.exe..."
Copy-Item build/ShoreDrop.exe cmd/setup/payload/ShoreDrop.exe -Force
go build -trimpath -ldflags "-H windowsgui -s -w" -o build/ShoreDrop-Setup.exe ./cmd/setup

Write-Host ""
Write-Host "Done:"
Get-ChildItem build/*.exe | Format-Table Name, Length
