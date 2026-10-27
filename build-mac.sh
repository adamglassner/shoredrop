#!/bin/sh
# Builds ShoreDrop.app for Apple Silicon Macs and packs it into a zip.
#
# On a Mac: needs Go 1.24+ and the Xcode Command Line Tools
#   (xcode-select --install). The bundle is ad-hoc signed with codesign.
#
# On Linux: cross-compiles with clang + lld and a macOS SDK. Set MACOS_SDK to
#   the SDK folder (for example MacOSX14.5.sdk). The bundle is ad-hoc signed
#   with rcodesign (https://github.com/indygreg/apple-platform-rs) if found.
set -e
cd "$(dirname "$0")"
VERSION=1.0.0
OUT=build/mac
mkdir -p "$OUT"

export CGO_ENABLED=1 GOOS=darwin GOARCH=arm64
if [ "$(uname -s)" != "Darwin" ]; then
	: "${MACOS_SDK:?set MACOS_SDK to a macOS SDK folder to cross-compile}"
	export CC="clang -target arm64-apple-macos11 -isysroot $MACOS_SDK -fuse-ld=lld -Wno-unused-command-line-argument"
fi

# -s -w: no DWARF, which also skips dsymutil/strip steps that need Apple tools
go build -trimpath -ldflags "-s -w" -o "$OUT/ShoreDrop" ./cmd/shoredrop
GOOS= GOARCH= CGO_ENABLED=0 go run ./tools/mkicns -o "$OUT/ShoreDrop.icns"
GOOS= GOARCH= CGO_ENABLED=0 go run ./tools/macapp -bin "$OUT/ShoreDrop" -icns "$OUT/ShoreDrop.icns" -out "$OUT/ShoreDrop.app" -version "$VERSION"

if command -v codesign >/dev/null 2>&1; then
	codesign --force --deep --sign - --timestamp=none "$OUT/ShoreDrop.app"
	codesign --verify --verbose "$OUT/ShoreDrop.app"
elif command -v rcodesign >/dev/null 2>&1; then
	rcodesign sign "$OUT/ShoreDrop.app"
	rcodesign print-signature-info "$OUT/ShoreDrop.app/Contents/MacOS/ShoreDrop" | grep -E "flags: CodeSignatureFlags|identifier:"
else
	echo "warning: no codesign or rcodesign; the bundle keeps the linker's ad-hoc signature only" >&2
fi

ZIP="build/ShoreDrop-$VERSION-macOS-arm64.zip"
rm -f "$ZIP"
GOOS= GOARCH= CGO_ENABLED=0 go run ./tools/macapp -out "$OUT/ShoreDrop.app" -zip "$ZIP"
ls -l "$ZIP"
