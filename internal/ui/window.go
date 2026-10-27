//go:build windows || darwin

package ui

// Portable window logic: widget tree, hover/capture/focus routing and painting.
// The native window (Win32 HWND or AppKit NSWindow) lives in winPlat.

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
	Clicks int  // 2 for a double click
	Ctrl   bool // Ctrl on Windows, Command on macOS (multi-select)
	Shift  bool
}

// KeyEvent is a key press.
type KeyEvent struct {
	VK       int
	Ctrl     bool // Ctrl on Windows, Command on macOS
	Shift    bool
	Alt      bool // Alt on Windows, Option on macOS
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

// Window is a top-level window with custom-drawn content.
type Window struct {
	winPlat

	app    *App
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
	// ChromeL is the width at the top-left taken by native window controls
	// (the macOS traffic lights); custom-drawn headers start after it.
	ChromeL int

	timers  map[uintptr]func()
	nextTmr uintptr
	closed  bool
	modal   bool
	owner   *Window
	Active  bool
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

func (w *Window) Size() (int, int) { return w.w, w.h }

// SetFocusWidget moves keyboard focus.
func (w *Window) SetFocusWidget(x Widget) {
	if w.focus == x {
		if tb, ok := x.(*TextBox); ok {
			w.nativeFocus(tb)
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
	tb, _ := x.(*TextBox)
	w.nativeFocus(tb)
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

// paintContent draws the background, widgets and overlay onto c.
func (w *Window) paintContent(c *Canvas) {
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
}

func (w *Window) relayout() {
	if w.Layout != nil {
		w.Layout()
	}
	w.Invalidate()
}

// routeMouse delivers a mouse event to the widget tree.
func (w *Window) routeMouse(e *MouseEvent) {
	x, y, kind := e.X, e.Y, e.Kind
	if w.OnMouseAny != nil {
		w.OnMouseAny(e)
	}
	switch kind {
	case MouseMove:
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
			w.nativeCapture(true)
		}
		if m, ok := t.(mouseWidget); ok {
			m.OnMouse(e)
		}
	case MouseUp, MouseRightUp:
		t := w.capture
		if kind == MouseUp {
			w.capture = nil
			w.nativeCapture(false)
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
		if w.capture == nil {
			if m, ok := w.hover.(mouseWidget); ok {
				m.OnMouse(e)
			}
			w.setHover(nil)
		}
	}
}

// routeWheel offers a wheel delta (120 per notch, positive = away from the
// user) to the topmost widget under (x, y) that wants it.
func (w *Window) routeWheel(x, y, delta int, shift bool) {
	for i := len(w.widgets) - 1; i >= 0; i-- {
		wd := w.widgets[i]
		if wd.IsVisible() && wd.Bounds().Contains(x, y) {
			if ww, ok := wd.(wheelWidget); ok && ww.OnWheel(delta, shift) {
				break
			}
		}
	}
}

// cursorAt returns the cursor id for a point in the client area.
func (w *Window) cursorAt(x, y int) int {
	cur := IDC_ARROW
	t := w.capture
	if t == nil {
		t = w.widgetAt(x, y)
	}
	if cw, ok := t.(cursorWidget); ok {
		cur = cw.Cursor(x, y)
	}
	return cur
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

// handleChar delivers a typed character to the focused widget.
func (w *Window) handleChar(r rune) {
	if k, ok := w.focus.(keyWidget); ok {
		k.OnChar(r)
	}
}
