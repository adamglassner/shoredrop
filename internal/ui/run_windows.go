//go:build windows

package ui

var pMessageBoxW = user32.NewProc("MessageBoxW")

func showFatal(msg string) {
	pMessageBoxW.Call(0, up(msg), up("ShoreDrop"), 0x10)
}
