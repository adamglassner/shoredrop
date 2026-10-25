// Command setup builds ShoreDrop-Setup.exe, a per-user installer that
// carries ShoreDrop.exe inside it. Build ShoreDrop.exe first and copy it to
// cmd/setup/payload/ShoreDrop.exe (the build scripts do this).
package main

import (
	_ "embed"

	"shoredrop/internal/core"
	"shoredrop/internal/ui"
)

//go:embed payload/ShoreDrop.exe
var payload []byte

func main() { ui.RunSetup(payload, core.AppVersion) }
