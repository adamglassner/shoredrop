//go:build windows || darwin

package ui

import (
	"sync"

	"shoredrop/internal/core"
)

// App owns the UI thread, windows and appearance.
type App struct {
	appPlat

	pal   *Palette
	S     *core.Settings
	main  *Window
	fonts map[int]*Fonts

	postMu  sync.Mutex
	posted  []func()
	pending bool

	ThemeChanged []func()
}

var theApp *App

func (a *App) fontsFor(dpi int) *Fonts {
	if f := a.fonts[dpi]; f != nil {
		return f
	}
	f := makeFonts(float64(dpi) / 96)
	a.fonts[dpi] = f
	return f
}

func (a *App) resolveTheme() {
	dark := false
	switch a.S.Theme {
	case "dark":
		dark = true
	case "light":
	default:
		dark = SystemDark()
	}
	if dark {
		a.pal = &Dark
	} else {
		a.pal = &Light
	}
}

// SetTheme switches appearance and persists it.
func (a *App) SetTheme(mode string) {
	a.S.Theme = mode
	a.S.Save()
	a.applyTheme()
}

func (a *App) applyTheme() {
	a.resolveTheme()
	a.applyThemeNative()
	for _, fn := range a.ThemeChanged {
		fn()
	}
}

func (a *App) systemThemeChanged() {
	if a.S.Theme == "system" {
		a.applyTheme()
	}
}

// Post runs fn on the UI thread (safe to call from any goroutine).
func (a *App) Post(fn func()) {
	a.postMu.Lock()
	a.posted = append(a.posted, fn)
	need := !a.pending
	a.pending = true
	a.postMu.Unlock()
	if need {
		a.wake()
	}
}

// Call runs fn on the UI thread and waits for it.
func (a *App) Call(fn func()) {
	done := make(chan struct{})
	a.Post(func() {
		defer close(done)
		fn()
	})
	<-done
}

func (a *App) runPosted() {
	a.postMu.Lock()
	list := a.posted
	a.posted = nil
	a.pending = false
	a.postMu.Unlock()
	for _, fn := range list {
		fn()
	}
}

type winOpts struct {
	title      string
	w, h       int // 96-DPI client size
	x, y       int // screen position for popups
	owner      *Window
	popup      bool
	dialog     bool
	resizable  bool
	noActivate bool
}
