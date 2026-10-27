//go:build darwin

package ui

/*
#cgo CFLAGS: -fobjc-arc -mmacosx-version-min=11.0 -Wno-deprecated-declarations
#cgo LDFLAGS: -framework Cocoa -framework CoreText -framework QuartzCore -mmacosx-version-min=11.0
#include <stdlib.h>
#include "mac_darwin.h"
*/
import "C"

import (
	"runtime"
	"strings"
	"time"
	"unicode/utf8"
	"unsafe"

	"shoredrop/internal/core"
)

// AppKit must run on the process's main thread, which is the thread the Go
// runtime starts main.main on as long as it is locked here during init.
func init() { runtime.LockOSThread() }

// NSEventModifierFlags
const (
	modShift   = 1 << 17
	modControl = 1 << 18
	modOption  = 1 << 19
	modCommand = 1 << 20
)

// appPlat is the AppKit part of App.
type appPlat struct {
	windows    map[int]*Window
	nextWin    int
	modalDepth int
	actions    map[int]func()
	menuTags   map[int][]int // dynamic menu index → tags currently registered
	menuFns    map[int]func() []MenuItem
	nextPopup  int
	onQuit     func() bool
}

// winPlat is the AppKit part of a Window.
type winPlat struct {
	id       int
	nh       C.SDHandle // SDWindow
	timerH   map[uintptr]C.SDHandle
	tbs      map[int]*TextBox
	wheelAcc float64
	cursor   int
}

func cstr(s string) *C.char { return C.CString(s) }

func cbool(b bool) C.int {
	if b {
		return 1
	}
	return 0
}

func newApp(s *core.Settings) *App {
	C.sd_app_init()
	a := &App{S: s, fonts: map[int]*Fonts{}}
	a.windows = map[int]*Window{}
	a.actions = map[int]func(){}
	a.menuTags = map[int][]int{}
	a.menuFns = map[int]func() []MenuItem{}
	theApp = a
	a.resolveTheme()
	C.sd_app_set_appearance(cbool(a.pal.Dark))
	return a
}

// SystemDark reports whether macOS is in dark mode.
func SystemDark() bool { return C.sd_system_dark() != 0 }

func (a *App) applyThemeNative() {
	C.sd_app_set_appearance(cbool(a.pal.Dark))
	for _, w := range a.windows {
		for _, tb := range w.tbs {
			tb.applyColors()
		}
		w.Invalidate()
	}
}

func (a *App) wake() { C.sd_app_wake() }

// loop runs the AppKit event loop until the main window closes.
func (a *App) loop(until func() bool) {
	if until != nil {
		panic("nested loops use ShowModal on macOS")
	}
	C.sd_app_activate()
	C.sd_app_run()
}

func (a *App) newWindow(o winOpts) *Window {
	a.nextWin++
	w := &Window{app: a, popup: o.popup, dialog: o.dialog && !o.resizable, owner: o.owner, scale: 1}
	w.id = a.nextWin
	w.tbs = map[int]*TextBox{}
	w.timerH = map[uintptr]C.SDHandle{}
	w.cursor = -1
	w.F = a.fontsFor(96)
	a.windows[w.id] = w
	kind := C.int(0)
	if o.dialog || o.owner != nil {
		kind = 1
	}
	var owner C.SDHandle
	if o.owner != nil {
		owner = o.owner.nh
	}
	t := cstr(o.title)
	defer C.free(unsafe.Pointer(t))
	w.nh = C.sd_win_new(C.int(w.id), kind, t, C.double(o.w), C.double(o.h), owner, cbool(o.resizable))
	w.ChromeL = int(C.sd_win_chrome_left(w.nh) + 0.5)
	var cw, ch C.double
	C.sd_win_content_size(w.nh, &cw, &ch)
	w.w, w.h = int(cw), int(ch)
	return w
}

func findWin(id C.int) *Window {
	if theApp == nil {
		return nil
	}
	w := theApp.windows[int(id)]
	if w == nil || w.closed || w.nh == 0 {
		return nil
	}
	return w
}

// ---------------------------------------------------------------- Window

func (w *Window) Invalidate() {
	if w.nh != 0 && !w.closed {
		C.sd_win_invalidate(w.nh)
	}
}

func (w *Window) SetTitle(s string) {
	t := cstr(s)
	defer C.free(unsafe.Pointer(t))
	C.sd_win_set_title(w.nh, t)
}

// Close asks the window to close (OnClose may refuse), like WM_CLOSE.
func (w *Window) Close() { w.app.Post(w.tryClose) }

func (w *Window) tryClose() {
	if w.closed {
		return
	}
	if w.OnClose != nil && !w.OnClose() {
		return
	}
	w.destroy()
}

func (w *Window) destroy() {
	if w.closed {
		return
	}
	w.closed = true
	for id, t := range w.timerH {
		C.sd_timer_kill(t)
		delete(w.timerH, id)
	}
	for _, tb := range w.tbs {
		if tb.nh != 0 {
			C.sd_tb_destroy(tb.nh)
			tb.nh = 0
		}
	}
	if w.modal {
		C.sd_win_stop_modal()
	}
	var owner C.SDHandle
	if w.owner != nil && !w.owner.closed {
		owner = w.owner.nh
	}
	C.sd_win_destroy(w.nh, owner)
	w.nh = 0
	if w.OnDestroy != nil {
		w.OnDestroy()
	}
	delete(w.app.windows, w.id)
	if w == w.app.main {
		C.sd_app_stop()
	}
}

func (w *Window) Maximized() bool { return C.sd_win_is_zoomed(w.nh) != 0 }
func (w *Window) ToggleMaximize() { C.sd_win_zoom(w.nh) }
func (w *Window) Minimize()       { C.sd_win_miniaturize(w.nh) }

// SetTimer calls fn every interval on the UI thread; returns an id for KillTimer.
func (w *Window) SetTimer(interval time.Duration, fn func()) uintptr {
	if w.timers == nil {
		w.timers = map[uintptr]func(){}
	}
	w.nextTmr++
	id := w.nextTmr
	w.timers[id] = fn
	w.timerH[id] = C.sd_timer_new(C.int(w.id), C.uintptr_t(id), C.double(interval.Seconds()))
	return id
}

func (w *Window) KillTimer(id uintptr) {
	if t, ok := w.timerH[id]; ok {
		C.sd_timer_kill(t)
		delete(w.timerH, id)
	}
	delete(w.timers, id)
}

func (w *Window) nativeFocus(tb *TextBox) {
	if w.nh == 0 {
		return
	}
	if tb != nil && tb.nh != 0 {
		C.sd_tb_focus(w.nh, tb.nh)
	} else {
		C.sd_win_focus_content(w.nh)
	}
}

func (w *Window) nativeCapture(on bool) {}

// ShowModal shows w as a modal dialog over its owner and blocks until it closes.
func (w *Window) ShowModal() {
	w.modal = true
	w.relayout()
	f := w.focus
	if f == nil {
		if l := w.focusables(); len(l) > 0 {
			f = l[0]
		}
	}
	w.focus = nil
	if f != nil {
		w.SetFocusWidget(f)
	}
	if w.closed {
		return
	}
	w.app.modalDepth++
	C.sd_win_run_modal(w.nh)
	w.app.modalDepth--
}

// ScreenPos converts client coordinates to screen coordinates.
func (w *Window) ScreenPos(x, y int) (int, int) {
	var sx, sy C.double
	C.sd_win_to_screen(w.nh, C.double(x), C.double(y), &sx, &sy)
	return int(sx + 0.5), int(sy + 0.5)
}

// ClientPos converts screen coordinates to client coordinates.
func (w *Window) ClientPos(x, y int) (int, int) {
	var cx, cy C.double
	C.sd_win_from_screen(w.nh, C.double(x), C.double(y), &cx, &cy)
	return int(cx + 0.5), int(cy + 0.5)
}

func (w *Window) acceptFileDrops() { C.sd_win_accept_drops(w.nh) }

func (w *Window) savePlacement(ws *core.WindowState) {
	var x, y, cw, ch C.double
	C.sd_win_get_frame(w.nh, &x, &y, &cw, &ch)
	ws.Maximized = w.Maximized()
	if ws.Maximized {
		return // keep the last restored frame
	}
	ws.X, ws.Y, ws.W, ws.H = int(x), int(y), int(cw), int(ch)
}

func (w *Window) showPlaced(ws core.WindowState) {
	if w.MinW > 0 {
		C.sd_win_set_min(w.nh, C.double(w.MinW), C.double(w.MinH))
	}
	if ws.X != 0 || ws.Y != 0 {
		C.sd_win_set_frame(w.nh, C.double(ws.X), C.double(ws.Y), C.double(max(ws.W, w.MinW)), C.double(max(ws.H, w.MinH)))
	} else {
		C.sd_win_center(w.nh)
	}
	C.sd_win_show(w.nh)
	if ws.Maximized && !w.Maximized() {
		w.ToggleMaximize()
	}
}

func setCursor(w *Window, id int) {
	// set every time: AppKit changes the cursor itself over text fields and
	// window edges, so a cached value can be stale
	w.cursor = id
	C.sd_set_cursor(C.int(id))
}

// keyDown reports whether a mouse button (VK 1/2) or modifier is held.
func keyDown(vk int) bool {
	switch vk {
	case 0x01:
		return C.sd_mouse_buttons()&1 != 0
	case 0x02:
		return C.sd_mouse_buttons()&2 != 0
	case VK_SHIFT:
		return C.sd_modifier_flags()&modShift != 0
	case VK_CONTROL:
		return C.sd_modifier_flags()&modCommand != 0
	case VK_MENU:
		return C.sd_modifier_flags()&modOption != 0
	}
	return false
}

func setClipboardText(s string) {
	t := cstr(s)
	defer C.free(unsafe.Pointer(t))
	C.sd_clipboard_set(t)
}

func showFatal(msg string) {
	t := cstr(msg)
	defer C.free(unsafe.Pointer(t))
	C.sd_alert(t)
}

// ---------------------------------------------------------------- callbacks from AppKit

//export goDraw
func goDraw(id C.int, ctx unsafe.Pointer, cw, ch C.double) {
	w := findWin(id)
	if w == nil {
		return
	}
	defer recoverUI("draw")
	w.w, w.h = int(cw), int(ch)
	c := &Canvas{ctx: ctx, w: w, P: w.app.pal}
	w.paintContent(c)
}

//export goResize
func goResize(id C.int, cw, ch C.double) {
	w := findWin(id)
	if w == nil {
		return
	}
	defer recoverUI("resize")
	nw, nh := int(cw), int(ch)
	if nw == w.w && nh == w.h {
		return
	}
	w.w, w.h = nw, nh
	w.relayout()
}

func modsFrom(m C.ulong) (ctrl, shift, alt bool) {
	return m&modCommand != 0, m&modShift != 0, m&modOption != 0
}

//export goMouse
func goMouse(id C.int, kind C.int, x, y C.double, clicks C.int, mods C.ulong) C.int {
	w := findWin(id)
	if w == nil {
		return 0
	}
	defer recoverUI("mouse")
	ix, iy := int(x), int(y)
	ctrl, shift, _ := modsFrom(mods)
	k := int(kind)
	if k == MouseDown && !w.popup && iy < w.CaptionH && w.widgetAt(ix, iy) == nil && w.capture == nil {
		// empty caption area: let AppKit move the window
		if _, isTB := w.focus.(*TextBox); isTB {
			w.SetFocusWidget(nil)
		}
		return 1
	}
	w.routeMouse(&MouseEvent{Kind: k, X: ix, Y: iy, Clicks: int(clicks), Ctrl: ctrl, Shift: shift})
	if !w.closed && (k == MouseMove || k == MouseUp) {
		setCursor(w, w.cursorAt(ix, iy))
	}
	if k == MouseLeave {
		w.cursor = -1
	}
	return 0
}

//export goWheel
func goWheel(id C.int, x, y, dy C.double, precise C.int, mods C.ulong) {
	w := findWin(id)
	if w == nil {
		return
	}
	defer recoverUI("wheel")
	// Windows reports 120 units per notch, which ShoreDrop scrolls as three
	// rows (~84 pt); trackpads report points, wheels report lines.
	if precise != 0 {
		w.wheelAcc += float64(dy) * 120 / 84
	} else {
		w.wheelAcc += float64(dy) * 40
	}
	d := int(w.wheelAcc)
	if d == 0 {
		return
	}
	w.wheelAcc -= float64(d)
	_, shift, _ := modsFrom(mods)
	w.routeWheel(int(x), int(y), d, shift)
}

// macVK maps a macOS virtual key code to the Win32 VK code ShoreDrop uses.
func macVK(code int, chars string) int {
	switch code {
	case 36, 76:
		return VK_RETURN
	case 48:
		return VK_TAB
	case 49:
		return VK_SPACE
	case 51:
		return VK_BACK
	case 117:
		return VK_DELETE
	case 53:
		return VK_ESCAPE
	case 123:
		return VK_LEFT
	case 124:
		return VK_RIGHT
	case 125:
		return VK_DOWN
	case 126:
		return VK_UP
	case 115:
		return VK_HOME
	case 119:
		return VK_END
	case 116:
		return VK_PRIOR
	case 121:
		return VK_NEXT
	case 122:
		return VK_F1
	case 120:
		return VK_F2
	case 96:
		return VK_F5
	case 98:
		return VK_F7
	case 109:
		return VK_F10
	case 43:
		return VK_OEM_COMMA
	case 47:
		return VK_OEM_PERIOD
	case 33:
		return VK_OEM_4
	case 30:
		return VK_OEM_6
	case 110:
		return VK_APPS
	}
	// letters and digits by their ANSI key codes, independent of layout
	letters := map[int]byte{0: 'A', 11: 'B', 8: 'C', 2: 'D', 14: 'E', 3: 'F', 5: 'G', 4: 'H', 34: 'I', 38: 'J', 40: 'K',
		37: 'L', 46: 'M', 45: 'N', 31: 'O', 35: 'P', 12: 'Q', 15: 'R', 1: 'S', 17: 'T', 32: 'U', 9: 'V', 13: 'W', 7: 'X',
		16: 'Y', 6: 'Z', 29: '0', 18: '1', 19: '2', 20: '3', 21: '4', 23: '5', 22: '6', 26: '7', 28: '8', 25: '9'}
	if b, ok := letters[code]; ok {
		return int(b)
	}
	if r, _ := utf8.DecodeRuneInString(strings.ToUpper(chars)); r > 0 && r < 128 {
		return int(r)
	}
	return 0
}

//export goKey
func goKey(id C.int, code C.int, chars *C.char, mods C.ulong, tb C.int) C.int {
	w := findWin(id)
	if w == nil {
		return 0
	}
	defer recoverUI("key")
	s := C.GoString(chars)
	ctrl, shift, alt := modsFrom(mods)
	var vk int
	switch code {
	case -1:
		vk = VK_BROWSER_BACK
	case -2:
		vk = VK_BROWSER_FORWARD
	default:
		vk = macVK(int(code), s)
	}
	e := &KeyEvent{VK: vk, Ctrl: ctrl, Shift: shift, Alt: alt}
	if tb > 0 {
		t := w.tbs[int(tb)]
		if t == nil {
			return 0
		}
		e.FromEdit, e.Edit = true, t
		switch {
		case vk == VK_RETURN && (!t.multiline || ctrl):
			if t.OnEnter != nil {
				t.OnEnter()
				return 1
			}
		case vk == VK_ESCAPE && t.OnEscape != nil:
			t.OnEscape()
			return 1
		}
		if t.OnKey != nil && t.OnKey(e) {
			return 1
		}
		if w.handleKey(e) {
			return 1
		}
		if vk == VK_ESCAPE && !t.multiline {
			return 1 // don't let the field editor revert or beep
		}
		return 0
	}
	handled := vk != 0 && w.handleKey(e)
	if !ctrl && !w.closed {
		for _, r := range s {
			if r >= 32 && r != 127 && (r < 0xF700 || r > 0xF8FF) {
				w.handleChar(r)
				handled = true
			}
		}
	}
	return cbool(handled)
}

//export goTextChanged
func goTextChanged(id C.int, tb C.int) {
	w := findWin(id)
	if w == nil {
		return
	}
	defer recoverUI("text")
	if t := w.tbs[int(tb)]; t != nil && !t.suppress && t.OnChange != nil {
		t.OnChange()
	}
}

//export goSyncFocus
func goSyncFocus(id C.int, tb C.int) {
	w := findWin(id)
	if w == nil {
		return
	}
	defer recoverUI("focus")
	if t := w.tbs[int(tb)]; t != nil {
		if w.focus != t {
			if f, ok := w.focus.(focusWidget); ok {
				f.SetFocused(false)
			}
			w.focus = t
		}
		t.SetFocused(true)
		return
	}
	if t, ok := w.focus.(*TextBox); ok {
		t.SetFocused(false)
	}
}

//export goShouldClose
func goShouldClose(id C.int) C.int {
	if w := findWin(id); w != nil {
		w.Close()
	}
	return 0
}

//export goActivate
func goActivate(id C.int, active C.int) {
	w := findWin(id)
	if w == nil {
		return
	}
	defer recoverUI("activate")
	w.Active = active != 0
	if w.OnActivate != nil {
		w.OnActivate(w.Active)
	}
	w.Invalidate()
}

//export goDrop
func goDrop(id C.int, paths *C.char, x, y C.double) {
	w := findWin(id)
	if w == nil || w.OnDrop == nil {
		return
	}
	defer recoverUI("drop")
	list := strings.Split(C.GoString(paths), "\n")
	w.OnDrop(list, int(x), int(y))
}

//export goTimer
func goTimer(id C.int, tid C.uintptr_t) {
	w := findWin(id)
	if w == nil {
		return
	}
	defer recoverUI("timer")
	if fn := w.timers[uintptr(tid)]; fn != nil {
		fn()
	}
}

//export goThemeChanged
func goThemeChanged() {
	if theApp != nil {
		defer recoverUI("theme")
		theApp.systemThemeChanged()
	}
}

//export goRunPosted
func goRunPosted() {
	if theApp != nil {
		defer recoverUI("posted")
		theApp.runPosted()
	}
}

//export goShouldTerminate
func goShouldTerminate() C.int {
	a := theApp
	if a == nil || a.main == nil || a.main.closed {
		return 1
	}
	if a.modalDepth > 0 {
		return 0
	}
	if a.main.OnClose != nil && !a.main.OnClose() {
		return 0
	}
	return 1
}

// recoverUI keeps a panic in one event handler from taking down the app;
// it is logged to crash.log like the Windows build does for fatal errors.
func recoverUI(where string) {
	if r := recover(); r != nil {
		reportPanic(where, r)
	}
}
