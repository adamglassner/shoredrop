//go:build windows

package ui

import (
	"shoredrop/internal/core"
	"syscall"
	"time"
	"unsafe"
)

// winPlat is the Win32 part of a Window.
type winPlat struct {
	hwnd     uintptr
	dpi      int
	backDC   uintptr
	backBmp  uintptr
	backW    int
	backH    int
	tracking bool
	edits    map[uintptr]*TextBox
}

func (w *Window) Invalidate() {
	if w.hwnd != 0 {
		pInvalidateRect.Call(w.hwnd, 0, 0)
	}
}

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

func (w *Window) nativeFocus(tb *TextBox) {
	if tb != nil {
		pSetFocus.Call(tb.hwnd)
	} else {
		pSetFocus.Call(w.hwnd)
	}
}

func (w *Window) nativeCapture(on bool) {
	if on {
		pSetCapture.Call(w.hwnd)
	} else {
		pReleaseCapture.Call()
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
	w.paintContent(c)
	c.release()
	pBitBlt.Call(hdc, 0, 0, uintptr(w.w), uintptr(w.h), w.backDC, 0, 0, SRCCOPY)
	pEndPaint.Call(w.hwnd, uintptr(unsafe.Pointer(&ps)))
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
	if kind == MouseMove && !w.tracking {
		tme := TRACKMOUSEEVENT{Flags: TME_LEAVE, Hwnd: w.hwnd}
		tme.Size = uint32(unsafe.Sizeof(tme))
		pTrackMouseEvent.Call(uintptr(unsafe.Pointer(&tme)))
		w.tracking = true
	}
	if kind == MouseLeave {
		w.tracking = false
	}
	w.routeMouse(&MouseEvent{Kind: kind, X: x, Y: y, Clicks: clicks, Ctrl: ctrl, Shift: shift})
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
		_, shift, _ := mods()
		w.routeWheel(int(p.X), int(p.Y), hiword(wp), shift)
		return 0, true
	case WM_SETCURSOR:
		if loword(lp) == HTCLIENT {
			x, y := cursorPos()
			p := POINT{int32(x), int32(y)}
			pScreenToClient.Call(w.hwnd, uintptr(unsafe.Pointer(&p)))
			pSetCursor.Call(loadCursor(w.cursorAt(int(p.X), int(p.Y))))
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
		w.handleChar(rune(wp))
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

func (w *Window) acceptFileDrops() { pDragAcceptFiles.Call(w.hwnd, 1) }

// savePlacement records the restored (non-maximized) position and size.
func (w *Window) savePlacement(ws *core.WindowState) {
	var wp WINDOWPLACEMENT
	wp.Length = uint32(unsafe.Sizeof(wp))
	pGetWindowPlacement.Call(w.hwnd, uintptr(unsafe.Pointer(&wp)))
	nr := wp.NormalPosition
	ws.X, ws.Y = int(nr.Left), int(nr.Top)
	ws.W = int(float64(nr.Right-nr.Left) / w.scale)
	ws.H = int(float64(nr.Bottom-nr.Top) / w.scale)
	ws.Maximized = w.Maximized()
}

// showPlaced shows a main window at a saved position (or centred).
func (w *Window) showPlaced(ws core.WindowState) {
	if ws.X != 0 || ws.Y != 0 {
		var wp WINDOWPLACEMENT
		wp.Length = uint32(unsafe.Sizeof(wp))
		pGetWindowPlacement.Call(w.hwnd, uintptr(unsafe.Pointer(&wp)))
		wp.NormalPosition = RECT{int32(ws.X), int32(ws.Y), int32(ws.X + w.S(ws.W)), int32(ws.Y + w.S(ws.H))}
		wp.ShowCmd = SW_SHOWNORMAL
		pSetWindowPlacement.Call(w.hwnd, uintptr(unsafe.Pointer(&wp)))
	} else {
		// centre on the primary work area
		x, y, W, H := w.centerSize(nil, ws.W, ws.H)
		pSetWindowPos.Call(w.hwnd, 0, uintptr(x), uintptr(y), uintptr(W), uintptr(H), SWP_NOZORDER)
	}
	if ws.Maximized {
		pShowWindow.Call(w.hwnd, SW_MAXIMIZE)
	} else {
		pShowWindow.Call(w.hwnd, SW_SHOW)
	}
	pUpdateWindow.Call(w.hwnd)
}
