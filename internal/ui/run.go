//go:build windows

package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"

	"shoredrop/internal/core"
)

var pMessageBoxW = user32.NewProc("MessageBoxW")

// reportPanic records an unexpected error to crash.log in the settings folder.
func reportPanic(where string, r any) {
	dir := core.ConfigDir()
	os.MkdirAll(dir, 0o700)
	f, err := os.OpenFile(filepath.Join(dir, "crash.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		fmt.Fprintf(f, "%s ShoreDrop %s panic in %s: %v\n%s\n", time.Now().Format(time.RFC3339), core.AppVersion, where, r, debug.Stack())
		f.Close()
	}
}

// Run starts the application and blocks until the main window closes.
func Run() {
	defer func() {
		if r := recover(); r != nil {
			reportPanic("main", r)
			pMessageBoxW.Call(0, up(fmt.Sprintf("ShoreDrop hit an unexpected error and needs to close.\n\n%v\n\nDetails were saved to %s", r,
				filepath.Join(core.ConfigDir(), "crash.log"))), up("ShoreDrop"), 0x10)
		}
	}()
	s := core.LoadSettings()
	a := newApp(&s)
	m := newMainWindow(a, &s)
	m.show()
	a.loop(nil)
}
