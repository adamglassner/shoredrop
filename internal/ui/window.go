//go:build windows

package ui

import (
	"syscall"
	"time"
	"unsafe"
)

// Mouse event kinds.
const (
	MouseDown = iota
	MouseUp
	MouseMove
	MouseRightDown
	MouseRightUp
	MouseLeave
)

// MouseEvent is delivered to widgets.
type MouseEvent struct {
	Kind   int
	X, Y   int
	Clicks int // 2 for a double click
	Ctrl   bool
	Shift  bool
}

// KeyEvent is a key press.
type KeyEvent struct {
	VK       int
	Ctrl     bool
	Shift    bool
	Alt      bool
	FromEdit bool // focus is in a text box
	Edit     *TextBox
}

// Widget is anything drawn inside a Window.
type Widget interface {
	Bounds() Rect
	SetBounds(Rect)
	IsVisible() bool
	Paint(c *Canvas)
}

type mouseWidget interface {
	OnMouse(e *MouseEvent) bool
}
type wheelWidget interface {
	OnWheel(delta int, shift bool) bool
}
type keyWidget interface {
	OnKey(e *KeyEvent) bool
	OnChar(r rune) bool
}
type hoverWidget interface {
	SetHover(bool)
}
type cursorWidget interface {
	Cursor(x, y int) int
}
type focusWidget interface {
	SetFocused(bool)
	CanFocus() bool
}

// Base implements the common parts of Widget.
type Base struct {
	R       Rect
	Hidden  bool
	Hovered bool
	W       *Window
}

func (b *Base) Bounds() Rect     { return b.R }
func (b *Base) SetBounds(r Rect) { b.R = r }
func (b *Base) IsVisible() bool  { return !b.Hidden }
func (b *Base) SetHover(h bool)  { b.Hovered = h; b.W.Invalidate() }
func (b *Base) Show(v bool)      { b.Hidden = !v }

// Window is a top-level window with custom-drawn chrome.
type Window struct {
	app    *App
	hwnd   uintptr
	dpi    int
	scale  float64
	F      *Fonts
	w, h   int
	popup  bool
	dialog bool

	widgets []Widget
	hover   Widget
	capture Widget
	focus   Widget

	PaintBg    func(c *Canvas)
	Overlay    func(c *Canvas)
	Layout     func()
	OnKey      func(e *KeyEvent) bool
	OnClose    func() bool
	OnDestroy  func()
	OnDrop     func(files []string, x, y int)
	OnActivate func(active bool)
	OnMouseAny func(e *MouseEvent) // sees every mouse event before widgets
	CaptionH   int                 // device pixels of draggable caption from the top
	MinW, MinH int                 // 96-DPI units

	timers   map[uintptr]func()
	nextTmr  uintptr
	backDC   uintptr
	backBmp  uintptr
	backW    int
	backH    int
	tracking bool
	closed   bool
	modal    bool
	owner    *Window
	edits    map[uintptr]*TextBox
	Active   bool
}

// S scales a 96-DPI length to device pixels.
func (w *Window) S(v int) int          { return int(float64(v)*w.scale + 0.5) }
func (w *Window) Sf(v float64) float64 { return v * w.scale }

func (w *Window) Add(ws ...Widget) {
	for _, x := range ws {
		w.widgets = append(w.widgets, x)
	}
}

func (w *Window) Remove(x Widget) {
	for i, y := range w.widgets {
		if y == x {
			w.widgets = append(w.widgets[:i], w.widgets[i+1:]...)
			break
		}
	}
	if w.hover == x {
		w.hover = nil
	}
	if w.capture == x {
		w.capture = nil
	}
	if w.focus == x {
		w.focus = nil
	}
}

func (w *Window) Invalidate() {
	if w.hwnd != 0 {
		pInvalidateRect.Call(w.hwnd, 0, 0)
	}
}

func (w *Window) Size() (int, int) { return w.w, w.h }

func (w *Window) SetTitle(s string) { pSetWindowTextW.Call(w.hwnd, up(s)) }

func (w *Window) Close() { pPostMessageW.Call(w.hwnd, WM_CLOSE, 0, 0) }

func (w *Window) Maximized() bool {
	r, _, _ := pIsZoomed.Call(w.hwnd)
	return r != 0
}

func (w *Window) ToggleMaximize() {
	if w.Maximized() {
		pShowWindow.Call(w.hwnd, SW_RESTORE)
	} else {
		pShowWindow.Call(w.hwnd, SW_MAXIMIZE)
	}
}

func (w *Window) Minimize() { pShowWindow.Call(w.hwnd, SW_MINIMIZE) }

// SetTimer calls fn every interval on the UI thread; returns an id for KillTimer.
func (w *Window) SetTimer(interval time.Duration, fn func()) uintptr {
	if w.timers == nil {
		w.timers = map[uintptr]func(){}
	}
	w.nextTmr++
	id := w.nextTmr
	w.timers[id] = fn
	pSetTimer.Call(w.hwnd, id, uintptr(interval/time.Millisecond), 0)
	return id
}

func (w *Window) KillTimer(id uintptr) {
	pKillTimer.Call(w.hwnd, id)
	delete(w.timers, id)
}

// SetFocusWidget moves keyboard focus.
func (w *Window) SetFocusWidget(x Widget) {
	if w.focus == x {
		if tb, ok := x.(*TextBox); ok {
			pSetFocus.Call(tb.hwnd)
		}
		return
	}
	if f, ok := w.focus.(focusWidget); ok {
		f.SetFocused(false)
	}
	w.focus = x
	if f, ok := x.(focusWidget); ok {
		f.SetFocused(true)
	}
	if tb, ok := x.(*TextBox); ok {
		pSetFocus.Call(tb.hwnd)
	} else {
		pSetFocus.Call(w.hwnd)
	}
	w.Invalidate()
}

func (w *Window) Focused() Widget { return w.focus }

func (w *Window) focusables() []Widget {
	var out []Widget
	for _, x := range w.widgets {
		if f, ok := x.(focusWidget); ok && x.IsVisible() && f.CanFocus() {
			out = append(out, x)
		}
	}
	return out
}

// FocusNext moves focus forward or backward (Tab / Shift+Tab).
func (w *Window) FocusNext(back bool) {
	list := w.focusables()
	if len(list) == 0 {
		return
	}
	idx := -1
	for i, x := range list {
		if x == w.focus {
			idx = i
		}
	}
	if back {
		idx--
		if idx < 0 {
			idx = len(list) - 1
		}
	} else {
		idx = (idx + 1) % len(list)
	}
	w.SetFocusWidget(list[idx])
	if tb, ok := list[idx].(*TextBox); ok {
		tb.SelectAll()
	}
}

func (w *Window) widgetAt(x, y int) Widget {
	for i := len(w.widgets) - 1; i >= 0; i-- {
		wd := w.widgets[i]
		if !wd.IsVisible() {
			continue
		}
		if _, ok := wd.(mouseWidget); !ok {
			continue
		}
		if wd.Bounds().Contains(x, y) {
			return wd
		}
	}
	return nil
}

func (w *Window) setHover(x Widget) {
	if w.hover == x {
		return
	}
	if h, ok := w.hover.(hoverWidget); ok {
		h.SetHover(false)
	}
	w.hover = x
	if h, ok := x.(hoverWidget); ok {
		h.SetHover(true)
	}
}

func (w *Window) ensureBackBuffer() {
	if w.backDC != 0 && w.backW == w.w && w.backH == w.h {
		return
	}
	if w.backDC != 0 {
		pDeleteObject.Call(w.backBmp)
		pDeleteDC.Call(w.backDC)
	}
	sdc, _, _ := pGetDC.Call(w.hwnd)
	w.backDC, _, _ = pCreateCompatibleDC.Call(sdc)
	bw, bh := w.w, w.h
	if bw < 1 {
		bw = 1
	}
	if bh < 1 {
		bh = 1
	}
	w.backBmp, _, _ = pCreateCompatibleBitmap.Call(sdc, uintptr(bw), uintptr(bh))
	pSelectObject.Call(w.backDC, w.backBmp)
	pReleaseDC.Call(w.hwnd, sdc)
	w.backW, w.backH = w.w, w.h
}

func (w *Window) paint() {
	var ps PAINTSTRUCT
	hdc, _, _ := pBeginPaint.Call(w.hwnd, uintptr(unsafe.Pointer(&ps)))
	w.ensureBackBuffer()
	c := newCanvas(w.backDC, w)
	c.FillRect(Rect{0, 0, w.w, w.h}, w.app.pal.Window)
	if w.PaintBg != nil {
		w.PaintBg(c)
	}
	for _, x := range w.widgets {
		if x.IsVisible() && !x.Bounds().Empty() {
			x.Paint(c)
		}
	}
	if w.Overlay != nil {
		w.Overlay(c)
	}
	c.release()
	pBitBlt.Call(hdc, 0, 0, uintptr(w.w), uintptr(w.h), w.backDC, 0, 0, SRCCOPY)
	pEndPaint.Call(w.hwnd, uintptr(unsafe.Pointer(&ps)))
}

func (w *Window) relayout() {
	if w.Layout != nil {
		w.Layout()
	}
	w.Invalidate()
}

func (w *Window) setDPI(dpi int) {
	if dpi <= 0 {
		dpi = 96
	}
	w.dpi = dpi
	w.scale = float64(dpi) / 96
	w.F = w.app.fontsFor(dpi)
	for _, tb := range w.edits {
		tb.applyFont()
	}
}

func (w *Window) resizeBorder() int {
	return w.S(6)
}

func (w *Window) hitTest(sx, sy int) uintptr {
	var p POINT
	p.X, p.Y = int32(sx), int32(sy)
	pScreenToClient.Call(w.hwnd, uintptr(unsafe.Pointer(&p)))
	x, y := int(p.X), int(p.Y)
	if !w.popup && !w.Maximized() && !w.dialog {
		b := w.resizeBorder()
		left, right, top, bottom := x < b, x >= w.w-b, y < b, y >= w.h-b
		switch {
		case top && left:
			return HTTOPLEFT
		case top && right:
			return HTTOPRIGHT
		case bottom && left:
			return HTBOTTOMLEFT
		case bottom && right:
			return HTBOTTOMRIGHT
		case left:
			return HTLEFT
		case right:
			return HTRIGHT
		case top:
			return HTTOP
		case bottom:
			return HTBOTTOM
		}
	}
	if y < w.CaptionH && w.widgetAt(x, y) == nil {
		return HTCAPTION
	}
	return HTCLIENT
}

func mods() (ctrl, shift, alt bool) {
	return keyDown(VK_CONTROL), keyDown(VK_SHIFT), keyDown(VK_MENU)
}

func (w *Window) mouse(kind int, lp uintptr, clicks int) {
	x, y := loword(lp), hiword(lp)
	ctrl, shift, _ := mods()
	e := &MouseEvent{Kind: kind, X: x, Y: y, Clicks: clicks, Ctrl: ctrl, Shift: shift}
	if w.OnMouseAny != nil {
		w.OnMouseAny(e)
	}
	switch kind {
	case MouseMove:
		if !w.tracking {
			tme := TRACKMOUSEEVENT{Flags: TME_LEAVE, Hwnd: w.hwnd}
			tme.Size = uint32(unsafe.Sizeof(tme))
			pTrackMouseEvent.Call(uintptr(unsafe.Pointer(&tme)))
			w.tracking = true
		}
		if w.capture != nil {
			if m, ok := w.capture.(mouseWidget); ok {
				m.OnMouse(e)
			}
			return
		}
		t := w.widgetAt(x, y)
		w.setHover(t)
		if m, ok := t.(mouseWidget); ok {
			m.OnMouse(e)
		}
	case MouseDown, MouseRightDown:
		t := w.widgetAt(x, y)
		if t != nil {
			if f, ok := t.(focusWidget); ok && f.CanFocus() {
				w.SetFocusWidget(t)
			} else if _, isTB := w.focus.(*TextBox); isTB && kind == MouseDown {
				// clicking elsewhere takes focus away from text boxes
				w.SetFocusWidget(nil)
			}
		} else if _, isTB := w.focus.(*TextBox); isTB {
			w.SetFocusWidget(nil)
		}
		if kind == MouseDown {
			w.capture = t
			pSetCapture.Call(w.hwnd)
		}
		if m, ok := t.(mouseWidget); ok {
			m.OnMouse(e)
		}
	case MouseUp, MouseRightUp:
		t := w.capture
		if kind == MouseUp {
			w.capture = nil
			pReleaseCapture.Call()
		} else {
			t = w.widgetAt(x, y)
		}
		if t == nil {
			t = w.widgetAt(x, y)
		}
		if m, ok := t.(mouseWidget); ok {
			m.OnMouse(e)
		}
		if kind == MouseUp {
			w.setHover(w.widgetAt(x, y))
		}
	case MouseLeave:
		w.tracking = false
		if w.capture == nil {
			if m, ok := w.hover.(mouseWidget); ok {
				m.OnMouse(e)
			}
			w.setHover(nil)
		}
	}
}

// handleKey routes a key press; returns true if consumed.
func (w *Window) handleKey(e *KeyEvent) bool {
	if w.OnKey != nil && w.OnKey(e) {
		return true
	}
	if e.VK == VK_TAB && !e.Ctrl {
		w.FocusNext(e.Shift)
		return true
	}
	if !e.FromEdit {
		if k, ok := w.focus.(keyWidget); ok && k.OnKey(e) {
			return true
		}
	}
	return false
}

func (w *Window) wndProc(msg uint32, wp, lp uintptr) (uintptr, bool) {
	switch msg {
	case WM_NCCALCSIZE:
		if wp != 0 {
			if w.Maximized() {
				p := (*NCCALCSIZE_PARAMS)(unsafe.Pointer(lp))
				fx, _, _ := pGetSystemMetricsForDpi.Call(SM_CXFRAME, uintptr(w.dpi))
				fy, _, _ := pGetSystemMetricsForDpi.Call(SM_CYFRAME, uintptr(w.dpi))
				pad, _, _ := pGetSystemMetricsForDpi.Call(SM_CXPADDEDBORDER, uintptr(w.dpi))
				p.Rgrc[0].Left += int32(fx + pad)
				p.Rgrc[0].Right -= int32(fx + pad)
				p.Rgrc[0].Top += int32(fy + pad)
				p.Rgrc[0].Bottom -= int32(fy + pad)
			}
			return 0, true
		}
	case WM_NCHITTEST:
		if w.popup {
			return HTCLIENT, true
		}
		return w.hitTest(loword(lp), hiword(lp)), true
	case WM_NCACTIVATE:
		// keep DWM from repainting a default frame
		r, _, _ := pDefWindowProcW.Call(w.hwnd, uintptr(msg), wp, ^uintptr(0))
		return r, true
	case WM_ACTIVATE:
		w.Active = loword(wp) != 0
		if w.OnActivate != nil {
			w.OnActivate(w.Active)
		}
		if w.Active {
			if tb, ok := w.focus.(*TextBox); ok {
				pSetFocus.Call(tb.hwnd)
			}
		}
		w.Invalidate()
	case WM_ERASEBKGND:
		return 1, true
	case WM_PAINT:
		w.paint()
		return 0, true
	case WM_SIZE:
		w.w, w.h = int(lp&0xffff), int((lp>>16)&0xffff)
		w.relayout()
		return 0, true
	case WM_GETMINMAXINFO:
		mmi := (*MINMAXINFO)(unsafe.Pointer(lp))
		if w.MinW > 0 {
			mmi.MinTrackSize.X = int32(w.S(w.MinW))
			mmi.MinTrackSize.Y = int32(w.S(w.MinH))
		}
		return 0, true
	case WM_DPICHANGED:
		w.setDPI(hiword(wp))
		r := (*RECT)(unsafe.Pointer(lp))
		pSetWindowPos.Call(w.hwnd, 0, uintptr(r.Left), uintptr(r.Top), uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top), SWP_NOZORDER|SWP_NOACTIVATE)
		w.relayout()
		return 0, true
	case WM_MOUSEMOVE:
		w.mouse(MouseMove, lp, 0)
		return 0, true
	case WM_LBUTTONDOWN:
		w.mouse(MouseDown, lp, 1)
		return 0, true
	case WM_LBUTTONDBLCLK:
		w.mouse(MouseDown, lp, 2)
		return 0, true
	case WM_LBUTTONUP:
		w.mouse(MouseUp, lp, 1)
		return 0, true
	case WM_RBUTTONDOWN:
		w.mouse(MouseRightDown, lp, 1)
		return 0, true
	case WM_RBUTTONUP:
		w.mouse(MouseRightUp, lp, 1)
		return 0, true
	case WM_MOUSELEAVE:
		w.mouse(MouseLeave, 0, 0)
		return 0, true
	case WM_CAPTURECHANGED:
		if lp != w.hwnd && w.capture != nil {
			if m, ok := w.capture.(mouseWidget); ok {
				x, y := cursorPos()
				p := POINT{int32(x), int32(y)}
				pScreenToClient.Call(w.hwnd, uintptr(unsafe.Pointer(&p)))
				m.OnMouse(&MouseEvent{Kind: MouseUp, X: int(p.X), Y: int(p.Y)})
			}
			w.capture = nil
		}
	case WM_MOUSEWHEEL:
		x, y := loword(lp), hiword(lp)
		p := POINT{int32(x), int32(y)}
		pScreenToClient.Call(w.hwnd, uintptr(unsafe.Pointer(&p)))
		delta := hiword(wp)
		_, shift, _ := mods()
		for i := len(w.widgets) - 1; i >= 0; i-- {
			wd := w.widgets[i]
			if wd.IsVisible() && wd.Bounds().Contains(int(p.X), int(p.Y)) {
				if ww, ok := wd.(wheelWidget); ok && ww.OnWheel(delta, shift) {
					break
				}
			}
		}
		return 0, true
	case WM_SETCURSOR:
		if loword(lp) == HTCLIENT {
			x, y := cursorPos()
			p := POINT{int32(x), int32(y)}
			pScreenToClient.Call(w.hwnd, uintptr(unsafe.Pointer(&p)))
			cur := IDC_ARROW
			t := w.capture
			if t == nil {
				t = w.widgetAt(int(p.X), int(p.Y))
			}
			if cw, ok := t.(cursorWidget); ok {
				cur = cw.Cursor(int(p.X), int(p.Y))
			}
			pSetCursor.Call(loadCursor(cur))
			return 1, true
		}
	case WM_KEYDOWN, WM_SYSKEYDOWN:
		ctrl, shift, alt := mods()
		if w.handleKey(&KeyEvent{VK: int(wp), Ctrl: ctrl, Shift: shift, Alt: alt}) {
			return 0, true
		}
		if msg == WM_SYSKEYDOWN && int(wp) == VK_MENU {
			return 0, true // don't enter the (non-existent) system menu loop
		}
	case WM_SYSCHAR:
		return 0, true
	case WM_CHAR:
		if k, ok := w.focus.(keyWidget); ok {
			k.OnChar(rune(wp))
		}
		return 0, true
	case WM_TIMER:
		if fn := w.timers[wp]; fn != nil {
			fn()
		}
		return 0, true
	case WM_COMMAND:
		if tb := w.edits[lp]; tb != nil {
			switch hiword(wp) & 0xffff {
			case EN_CHANGE:
				if !tb.suppress && tb.OnChange != nil {
					tb.OnChange()
				}
			case EN_SETFOCUS:
				if w.focus != tb {
					if f, ok := w.focus.(focusWidget); ok {
						f.SetFocused(false)
					}
					w.focus = tb
				}
				tb.SetFocused(true)
			case EN_KILLFOCUS:
				tb.SetFocused(false)
			}
		}
		return 0, true
	case WM_CTLCOLOREDIT, WM_CTLCOLORSTATIC:
		if tb := w.edits[lp]; tb != nil {
			bg := tb.bgColor()
			pSetTextColor.Call(wp, rgb(tb.fgColor()))
			pSetBkColor.Call(wp, rgb(bg))
			return w.app.solidBrush(bg), true
		}
	case WM_DROPFILES:
		files := dropFiles(wp)
		var p POINT
		pDragQueryPoint.Call(wp, uintptr(unsafe.Pointer(&p)))
		pDragFinish.Call(wp)
		if w.OnDrop != nil {
			w.OnDrop(files, int(p.X), int(p.Y))
		}
		return 0, true
	case WM_SETTINGCHANGE, WM_THEMECHANGED:
		if lp != 0 {
			s := syscall.UTF16ToString((*[64]uint16)(unsafe.Pointer(lp))[:])
			if s == "ImmersiveColorSet" {
				w.app.systemThemeChanged()
			}
		}
	case WM_CLOSE:
		if w.OnClose != nil && !w.OnClose() {
			return 0, true
		}
		w.destroy()
		return 0, true
	case WM_DESTROY:
		if w.OnDestroy != nil {
			w.OnDestroy()
		}
		delete(w.app.windows, w.hwnd)
		w.closed = true
		if w == w.app.main {
			pPostQuitMessage.Call(0)
		}
		return 0, true
	case WM_APP + 1:
		w.app.runPosted()
		return 0, true
	}
	return 0, false
}

func (w *Window) destroy() {
	if w.owner != nil {
		pEnableWindow.Call(w.owner.hwnd, 1)
		if !w.popup {
			pSetActiveWindow.Call(w.owner.hwnd)
		}
	}
	for id := range w.timers {
		pKillTimer.Call(w.hwnd, id)
	}
	if w.backDC != 0 {
		pDeleteObject.Call(w.backBmp)
		pDeleteDC.Call(w.backDC)
		w.backDC = 0
	}
	pDestroyWindow.Call(w.hwnd)
}

func dropFiles(h uintptr) []string {
	n, _, _ := pDragQueryFileW.Call(h, 0xFFFFFFFF, 0, 0)
	var out []string
	for i := uintptr(0); i < n; i++ {
		l, _, _ := pDragQueryFileW.Call(h, i, 0, 0)
		buf := make([]uint16, l+1)
		pDragQueryFileW.Call(h, i, uintptr(unsafe.Pointer(&buf[0])), l+1)
		out = append(out, syscall.UTF16ToString(buf))
	}
	return out
}

// ShowModal shows w as a modal dialog over owner and blocks until it closes.
func (w *Window) ShowModal() {
	w.modal = true
	if w.owner != nil {
		pEnableWindow.Call(w.owner.hwnd, 0)
	}
	pShowWindow.Call(w.hwnd, SW_SHOW)
	pSetForegroundWindow.Call(w.hwnd)
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
	w.app.loop(func() bool { return w.closed })
}

// CenterOn positions w (size in 96-DPI units) centred over the owner.
func (w *Window) centerSize(owner *Window, cw, ch int) (int, int, int, int) {
	W, H := w.S(cw), w.S(ch)
	var r RECT
	if owner != nil {
		pGetWindowRect.Call(owner.hwnd, uintptr(unsafe.Pointer(&r)))
	} else {
		sw, _, _ := pGetSystemMetrics.Call(SM_CXSCREEN)
		sh, _, _ := pGetSystemMetrics.Call(SM_CYSCREEN)
		r = RECT{0, 0, int32(sw), int32(sh)}
	}
	x := int(r.Left) + (int(r.Right-r.Left)-W)/2
	y := int(r.Top) + (int(r.Bottom-r.Top)-H)/2
	// keep on the owner's monitor work area
	mon, _, _ := pMonitorFromWindow.Call(owner.hwndOrZero(), MONITOR_DEFAULTTONEAREST)
	var mi MONITORINFO
	mi.Size = uint32(unsafe.Sizeof(mi))
	if pGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&mi))); mi.Work.Right > 0 {
		if x+W > int(mi.Work.Right) {
			x = int(mi.Work.Right) - W
		}
		if y+H > int(mi.Work.Bottom) {
			y = int(mi.Work.Bottom) - H
		}
		if x < int(mi.Work.Left) {
			x = int(mi.Work.Left)
		}
		if y < int(mi.Work.Top) {
			y = int(mi.Work.Top)
		}
	}
	return x, y, W, H
}

func (w *Window) hwndOrZero() uintptr {
	if w == nil {
		return 0
	}
	return w.hwnd
}

// ScreenPos converts client coordinates to screen coordinates.
func (w *Window) ScreenPos(x, y int) (int, int) {
	p := POINT{int32(x), int32(y)}
	pClientToScreen.Call(w.hwnd, uintptr(unsafe.Pointer(&p)))
	return int(p.X), int(p.Y)
}

// ClientPos converts screen coordinates to client coordinates.
func (w *Window) ClientPos(x, y int) (int, int) {
	p := POINT{int32(x), int32(y)}
	pScreenToClient.Call(w.hwnd, uintptr(unsafe.Pointer(&p)))
	return int(p.X), int(p.Y)
}

func (w *Window) applyDarkFrame() {
	dark := int32(0)
	if w.app.pal.Dark {
		dark = 1
	}
	pDwmSetWindowAttribute.Call(w.hwnd, 20, uintptr(unsafe.Pointer(&dark)), 4)
	col := uint32(rgb(w.app.pal.Border))
	pDwmSetWindowAttribute.Call(w.hwnd, 34, uintptr(unsafe.Pointer(&col)), 4) // DWMWA_BORDER_COLOR (Win11)
	cap := uint32(rgb(w.app.pal.Window))
	pDwmSetWindowAttribute.Call(w.hwnd, 35, uintptr(unsafe.Pointer(&cap)), 4) // DWMWA_CAPTION_COLOR
}
