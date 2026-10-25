//go:build windows

package ui

import (
	"time"
	"unsafe"
)

// MenuItem is an entry in a popup menu.
type MenuItem struct {
	Label     string
	Shortcut  string
	Icon      rune
	Checked   bool
	Disabled  bool
	Separator bool
	Danger    bool
	Action    func()
}

// Sep is a menu separator.
var Sep = MenuItem{Separator: true}

type menuView struct {
	Base
	items  []MenuItem
	hover  int
	rowH   int
	sepH   int
	pad    int
	choose func(i int)
}

func (m *menuView) rowRects() []Rect {
	var out []Rect
	y := m.R.Y + m.pad
	for _, it := range m.items {
		h := m.rowH
		if it.Separator {
			h = m.sepH
		}
		out = append(out, Rect{m.R.X + m.pad, y, m.R.W - 2*m.pad, h})
		y += h
	}
	return out
}

func (m *menuView) indexAt(x, y int) int {
	for i, r := range m.rowRects() {
		if r.Contains(x, y) && !m.items[i].Separator && !m.items[i].Disabled {
			return i
		}
	}
	return -1
}

func (m *menuView) OnMouse(e *MouseEvent) bool {
	switch e.Kind {
	case MouseMove:
		if i := m.indexAt(e.X, e.Y); i != m.hover {
			m.hover = i
			m.W.Invalidate()
		}
	case MouseLeave:
		m.hover = -1
		m.W.Invalidate()
	case MouseUp:
		if i := m.indexAt(e.X, e.Y); i >= 0 {
			m.choose(i)
		}
	}
	return true
}

func (m *menuView) move(d int) {
	n := len(m.items)
	i := m.hover
	for k := 0; k < n; k++ {
		i = (i + d + n) % n
		if !m.items[i].Separator && !m.items[i].Disabled {
			m.hover = i
			m.W.Invalidate()
			return
		}
	}
}

func (m *menuView) Paint(c *Canvas) {
	p := c.P
	c.FillRect(m.R, p.Surface)
	c.StrokeRound(m.R, 0, p.BorderStr, 1)
	f := m.W.F.Menu
	for i, r := range m.rowRects() {
		it := m.items[i]
		if it.Separator {
			cy := r.Y + r.H/2
			c.FillRect(Rect{r.X + m.W.S(8), cy, r.W - m.W.S(16), 1}, p.Border)
			continue
		}
		fg := p.Text
		sub := p.Text3
		if it.Danger {
			fg = p.Danger
		}
		if it.Disabled {
			fg = p.Text3
		}
		if i == m.hover {
			c.FillRound(r, c.S(5), p.Accent)
			fg, sub = p.AccentText, p.AccentText
		}
		x := r.X + m.W.S(10)
		gw := m.W.S(22)
		if it.Checked {
			c.FillCircle(float64(x+gw/2-m.W.S(2)), float64(r.Y+r.H/2), c.S(8), ifc(i == m.hover, p.AccentText, p.Accent))
			drawCheck(c, Rect{x + gw/2 - m.W.S(2) - m.W.S(8), r.Y + r.H/2 - m.W.S(8), m.W.S(16), m.W.S(16)}, ifc(i == m.hover, p.Accent, p.AccentText))
		} else if it.Icon != 0 {
			c.Glyph(it.Icon, m.W.F.Icon, ifc(i == m.hover, fg, p.Text2), Rect{x - m.W.S(2), r.Y, gw, r.H})
		}
		x += gw + m.W.S(6)
		c.Text(it.Label, f, fg, Rect{x, r.Y, r.X + r.W - x - m.W.S(8), r.H}, AlignLeft)
		if it.Shortcut != "" {
			sw := m.W.F.Small.Measure(it.Shortcut)
			sr := Rect{r.X + r.W - sw - m.W.S(22), r.Y + (r.H-m.W.S(22))/2, sw + m.W.S(12), m.W.S(22)}
			if i != m.hover {
				c.FillRound(sr, c.S(5), p.Surface2)
			}
			c.Text(it.Shortcut, m.W.F.Small, sub, sr, AlignCenter)
		}
	}
}

func ifc(cond bool, a, b Color) Color {
	if cond {
		return a
	}
	return b
}

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

// menuNav, if set, lets Left/Right in an open menu move between menu-bar menus.
var menuNav func(right bool)

var lastMenuClose time.Time
