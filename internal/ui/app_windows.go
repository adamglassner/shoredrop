//go:build windows

package ui

import (
	"runtime"
	"syscall"
	"unsafe"

	"shoredrop/internal/core"
)

// appPlat is the Win32 part of App.
type appPlat struct {
	inst    uintptr
	windows map[uintptr]*Window
	brushes map[Color]uintptr
	icon    uintptr
	iconSm  uintptr
}

var (
	creating   *Window
	wndProcCB  = syscall.NewCallback(globalWndProc)
	classesReg bool
)

func globalWndProc(hwnd, msg, wp, lp uintptr) uintptr {
	w := theApp.windows[hwnd]
	if w == nil && creating != nil {
		w = creating
		w.hwnd = hwnd
		theApp.windows[hwnd] = w
	}
	if w != nil {
		if r, ok := w.wndProc(uint32(msg), wp, lp); ok {
			return r
		}
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, msg, wp, lp)
	return r
}

func newApp(s *core.Settings) *App {
	runtime.LockOSThread()
	pSetProcessDpiAwarenessCx.Call(^uintptr(3)) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 (-4)
	pCoInitializeEx.Call(0, 2)
	initGDIPlus()
	a := &App{S: s, fonts: map[int]*Fonts{}}
	a.windows = map[uintptr]*Window{}
	a.brushes = map[Color]uintptr{}
	a.inst, _, _ = pGetModuleHandleW.Call(0)
	theApp = a
	a.icon, _, _ = pLoadImageW.Call(a.inst, 1, 1 /*IMAGE_ICON*/, 0, 0, 0x40 /*LR_DEFAULTSIZE*/)
	a.iconSm, _, _ = pLoadImageW.Call(a.inst, 1, 1, 16, 16, 0)
	a.resolveTheme()
	a.registerClasses()
	return a
}

func (a *App) registerClasses() {
	if classesReg {
		return
	}
	classesReg = true
	for _, c := range []struct {
		name  string
		style uint32
	}{{"ShoreDropWindow", CS_DBLCLKS}, {"ShoreDropPopup", CS_DBLCLKS | CS_DROPSHADOW}} {
		wc := WNDCLASSEXW{
			Style:     c.style,
			WndProc:   wndProcCB,
			Instance:  a.inst,
			Icon:      a.icon,
			IconSm:    a.iconSm,
			Cursor:    loadCursor(IDC_ARROW),
			ClassName: utf16p(c.name),
		}
		wc.Size = uint32(unsafe.Sizeof(wc))
		pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	}
}

func (a *App) solidBrush(c Color) uintptr {
	if b := a.brushes[c]; b != 0 {
		return b
	}
	b, _, _ := pCreateSolidBrush.Call(rgb(c))
	a.brushes[c] = b
	return b
}

// SystemDark reports whether Windows apps are set to dark mode.
func SystemDark() bool {
	var v uint32
	size := uint32(4)
	r, _, _ := pRegGetValueW.Call(0x80000001, /*HKEY_CURRENT_USER*/
		up(`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`), up("AppsUseLightTheme"),
		0x10 /*RRF_RT_REG_DWORD*/, 0, uintptr(unsafe.Pointer(&v)), uintptr(unsafe.Pointer(&size)))
	if r != 0 {
		return false
	}
	return v == 0
}

func (a *App) applyThemeNative() {
	for _, w := range a.windows {
		w.applyDarkFrame()
		for h, tb := range w.edits {
			if tb.multiline {
				theme := "Explorer"
				if a.pal.Dark {
					theme = "DarkMode_Explorer"
				}
				pSetWindowTheme.Call(h, up(theme), 0)
			}
		}
		pRedrawWindow.Call(w.hwnd, 0, 0, 0x0001|0x0004|0x0080|0x0100) // INVALIDATE|ERASE|ALLCHILDREN|UPDATENOW
	}
}

func (a *App) wake() {
	if a.main != nil {
		pPostMessageW.Call(a.main.hwnd, WM_APP+1, 0, 0)
	}
}

func (a *App) newWindow(o winOpts) *Window {
	w := &Window{app: a, popup: o.popup, dialog: o.dialog && !o.resizable, owner: o.owner}
	w.edits = map[uintptr]*TextBox{}
	dpi := 96
	if o.owner != nil {
		dpi = o.owner.dpi
	} else {
		r, _, _ := pGetDpiForSystem.Call()
		if r > 0 {
			dpi = int(r)
		}
	}
	w.setDPI(dpi)
	class := "ShoreDropWindow"
	style := uintptr(WS_OVERLAPPEDWINDOW | WS_CLIPCHILDREN)
	ex := uintptr(WS_EX_APPWINDOW)
	var parent uintptr
	if o.owner != nil {
		parent = o.owner.hwnd
		ex = 0
	}
	if o.dialog {
		style = WS_POPUP | WS_CAPTION | WS_SYSMENU | WS_CLIPCHILDREN | WS_MINIMIZEBOX
		if o.resizable {
			style |= WS_THICKFRAME | WS_MAXIMIZEBOX
		}
	}
	if o.popup {
		class = "ShoreDropPopup"
		style = WS_POPUP | WS_CLIPCHILDREN
		ex = WS_EX_TOOLWINDOW | WS_EX_TOPMOST
		if o.noActivate {
			ex |= WS_EX_NOACTIVATE
		}
	}
	W, H := w.S(o.w), w.S(o.h)
	x, y := uintptr(0x80000000), uintptr(0x80000000) // CW_USEDEFAULT
	if o.popup {
		x, y = uintptr(o.x), uintptr(o.y)
	} else if o.owner != nil {
		cx, cy, _, _ := w.centerSize(o.owner, o.w, o.h)
		x, y = uintptr(cx), uintptr(cy)
	}
	creating = w
	pCreateWindowExW.Call(ex, up(class), up(o.title), style, x, y, uintptr(W), uintptr(H), parent, 0, a.inst, 0)
	creating = nil
	if w.hwnd == 0 {
		panic("CreateWindowEx failed")
	}
	// Re-check the DPI of the monitor the window landed on.
	if r, _, _ := pGetDpiForWindow.Call(w.hwnd); int(r) != w.dpi && r > 0 && o.owner == nil {
		w.setDPI(int(r))
		pSetWindowPos.Call(w.hwnd, 0, 0, 0, uintptr(w.S(o.w)), uintptr(w.S(o.h)), SWP_NOMOVE|SWP_NOZORDER|SWP_NOACTIVATE)
	}
	if !o.popup {
		m := MARGINS{0, 0, 1, 0}
		pDwmExtendFrameIntoClientArea.Call(w.hwnd, uintptr(unsafe.Pointer(&m)))
		pref := uint32(2) // DWMWCP_ROUND
		pDwmSetWindowAttribute.Call(w.hwnd, 33, uintptr(unsafe.Pointer(&pref)), 4)
	} else {
		pref := uint32(3) // DWMWCP_ROUNDSMALL
		pDwmSetWindowAttribute.Call(w.hwnd, 33, uintptr(unsafe.Pointer(&pref)), 4)
	}
	w.applyDarkFrame()
	pSetWindowPos.Call(w.hwnd, 0, 0, 0, 0, 0, SWP_NOMOVE|SWP_NOSIZE|SWP_NOZORDER|SWP_NOACTIVATE|SWP_FRAMECHANGED)
	return w
}

// loop pumps messages until until() reports true (or WM_QUIT).
func (a *App) loop(until func() bool) {
	var m MSG
	for {
		if until != nil && until() {
			return
		}
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			if until != nil {
				pPostQuitMessage.Call(m.WParam)
			}
			return
		}
		if a.preTranslate(&m) {
			continue
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func (a *App) findEdit(h uintptr) (*Window, *TextBox) {
	parent, _, _ := pGetParent.Call(h)
	if w := a.windows[parent]; w != nil {
		if tb := w.edits[h]; tb != nil {
			return w, tb
		}
	}
	return nil, nil
}

func (a *App) preTranslate(m *MSG) bool {
	switch m.Message {
	case WM_KEYDOWN, WM_SYSKEYDOWN:
		w, tb := a.findEdit(m.Hwnd)
		if tb == nil {
			return false
		}
		ctrl, shift, alt := mods()
		e := &KeyEvent{VK: int(m.WParam), Ctrl: ctrl, Shift: shift, Alt: alt, FromEdit: true, Edit: tb}
		switch {
		case e.VK == VK_RETURN && (!tb.multiline || ctrl):
			if tb.OnEnter != nil {
				tb.OnEnter()
				return true
			}
		case e.VK == VK_ESCAPE && tb.OnEscape != nil:
			tb.OnEscape()
			return true
		case e.VK == 'A' && ctrl && !alt:
			tb.SelectAll()
			return true
		}
		if tb.OnKey != nil && tb.OnKey(e) {
			return true
		}
		return w.handleKey(e)
	case WM_CHAR:
		_, tb := a.findEdit(m.Hwnd)
		if tb != nil && !tb.multiline && (m.WParam == '\r' || m.WParam == '\t' || m.WParam == 0x1b || m.WParam == 1) {
			return true
		}
		if tb != nil && tb.multiline && (m.WParam == '\t' || m.WParam == 0x1b || m.WParam == 1) {
			return true
		}
	}
	return false
}
