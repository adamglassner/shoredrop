//go:build windows

package ui

import (
	"time"
	"unsafe"
)

// ShowMenu opens a popup menu at screen position (sx, sy). The menu closes
// when an item is chosen or it loses focus; onClose (optional) runs then.
func (a *App) ShowMenu(owner *Window, items []MenuItem, sx, sy, minW int, onClose func()) *Window {
	scale := owner.scale
	S := func(v int) int { return int(float64(v)*scale + 0.5) }
	f := owner.F.Menu
	rowH, sepH, pad := S(34), S(11), S(6)
	w := minW
	h := 2 * pad
	for _, it := range items {
		if it.Separator {
			h += sepH
			continue
		}
		h += rowH
		iw := f.Measure(it.Label) + S(10+22+6+16)
		if it.Shortcut != "" {
			iw += owner.F.Small.Measure(it.Shortcut) + S(44)
		}
		if iw+2*pad > w {
			w = iw + 2*pad
		}
	}
	if w < S(180) {
		w = S(180)
	}
	// keep fully on screen (never clip the last item)
	mon, _, _ := pMonitorFromPoint.Call(uintptr(uint32(sx))|uintptr(uint32(sy))<<32, MONITOR_DEFAULTTONEAREST)
	var mi MONITORINFO
	mi.Size = uint32(unsafe.Sizeof(mi))
	pGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&mi)))
	if mi.Work.Right > 0 {
		if sx+w > int(mi.Work.Right) {
			sx = int(mi.Work.Right) - w
		}
		if sy+h > int(mi.Work.Bottom) {
			if sy-h-S(36) >= int(mi.Work.Top) {
				sy = sy - h - S(36)
			} else {
				sy = int(mi.Work.Bottom) - h
			}
		}
		if sx < int(mi.Work.Left) {
			sx = int(mi.Work.Left)
		}
		if sy < int(mi.Work.Top) {
			sy = int(mi.Work.Top)
		}
	}
	pw := a.newWindow(winOpts{popup: true, owner: owner, x: sx, y: sy, w: int(float64(w)/scale + 0.5), h: int(float64(h)/scale + 0.5)})
	pSetWindowPos.Call(pw.hwnd, 0, uintptr(sx), uintptr(sy), uintptr(w), uintptr(h), SWP_NOZORDER|SWP_NOACTIVATE)
	mv := &menuView{Base: Base{W: pw}, items: items, hover: -1, rowH: rowH, sepH: sepH, pad: pad}
	closed := false
	finish := func(action func()) {
		if closed {
			return
		}
		closed = true
		pw.OnActivate = nil
		pw.destroy()
		lastMenuClose = time.Now()
		if onClose != nil {
			onClose()
		}
		if action != nil {
			a.Post(action)
		}
	}
	mv.choose = func(i int) { finish(items[i].Action) }
	pw.Add(mv)
	pw.Layout = func() { mv.SetBounds(Rect{0, 0, pw.w, pw.h}) }
	pw.OnActivate = func(active bool) {
		if !active {
			finish(nil)
		}
	}
	pw.OnKey = func(e *KeyEvent) bool {
		switch e.VK {
		case VK_ESCAPE:
			finish(nil)
		case VK_UP:
			mv.move(-1)
		case VK_DOWN:
			mv.move(1)
		case VK_RETURN, VK_SPACE:
			if mv.hover >= 0 {
				mv.choose(mv.hover)
			}
		case VK_LEFT, VK_RIGHT:
			if menuNav != nil {
				nav := menuNav
				finish(nil)
				nav(e.VK == VK_RIGHT)
			}
		default:
			return false
		}
		return true
	}
	pw.relayout()
	pShowWindow.Call(pw.hwnd, SW_SHOW)
	pSetForegroundWindow.Call(pw.hwnd)
	return pw
}
