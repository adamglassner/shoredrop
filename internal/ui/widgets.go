//go:build windows

package ui

import (
	"strconv"
	"unsafe"
)

// ---------------------------------------------------------------- Label

// Label draws static text.
type Label struct {
	Base
	Text  string
	Font  func() *Font
	Color func() Color
	Align int
	Wrap  bool
	Path  bool // path-style ellipsis
}

func NewLabel(w *Window, text string) *Label {
	return &Label{Base: Base{W: w}, Text: text}
}

func (l *Label) Paint(c *Canvas) {
	f := l.W.F.Body
	if l.Font != nil {
		f = l.Font()
	}
	col := c.P.Text
	if l.Color != nil {
		col = l.Color()
	}
	switch {
	case l.Wrap:
		c.TextWrap(l.Text, f, col, l.R)
	case l.Path:
		c.TextPath(l.Text, f, col, l.R)
	default:
		c.Text(l.Text, f, col, l.R, l.Align)
	}
}

// ---------------------------------------------------------------- Button

// Button styles.
const (
	BtnSecondary = iota
	BtnPrimary
	BtnGhost
	BtnDanger
	BtnChrome      // window caption buttons
	BtnChromeClose // caption close button
	BtnPill        // small accent pill (protocol badge)
)

// Button is a clickable push button with optional icon.
type Button struct {
	Base
	Text      string
	Icon      rune
	Style     int
	Disabled  bool
	Selected  bool // toggled/active look for ghost buttons
	OnClick   func()
	Font      func() *Font
	IconOnly  bool
	pressed   bool
	focused   bool
	Focusable bool
}

func NewButton(w *Window, text string, style int, onClick func()) *Button {
	return &Button{Base: Base{W: w}, Text: text, Style: style, OnClick: onClick}
}

func NewIconButton(w *Window, icon rune, onClick func()) *Button {
	return &Button{Base: Base{W: w}, Icon: icon, Style: BtnGhost, OnClick: onClick, IconOnly: true}
}

// PreferredWidth returns a natural width for the button's content.
func (b *Button) PreferredWidth() int {
	f := b.font()
	w := f.Measure(b.Text) + b.W.S(28)
	if b.Icon != 0 && b.Text != "" {
		w += b.W.S(22)
	}
	if b.Text == "" {
		w = b.W.S(32)
	}
	return w
}

func (b *Button) font() *Font {
	if b.Font != nil {
		return b.Font()
	}
	if b.Style == BtnPrimary || b.Style == BtnPill {
		return b.W.F.BodySemi
	}
	return b.W.F.Body
}

func (b *Button) CanFocus() bool     { return b.Focusable && !b.Disabled }
func (b *Button) SetFocused(f bool)  { b.focused = f; b.W.Invalidate() }
func (b *Button) OnChar(r rune) bool { return false }
func (b *Button) OnKey(e *KeyEvent) bool {
	if (e.VK == VK_RETURN || e.VK == VK_SPACE) && !b.Disabled && b.OnClick != nil {
		b.OnClick()
		return true
	}
	return false
}

func (b *Button) OnMouse(e *MouseEvent) bool {
	if b.Disabled {
		return true
	}
	switch e.Kind {
	case MouseDown:
		b.pressed = true
		b.W.Invalidate()
	case MouseUp:
		was := b.pressed
		b.pressed = false
		b.W.Invalidate()
		if was && b.R.Contains(e.X, e.Y) && b.OnClick != nil {
			b.OnClick()
		}
	}
	return true
}

func (b *Button) Paint(c *Canvas) {
	p := c.P
	r := b.R
	rad := c.S(6)
	var bg, fg, border Color
	switch b.Style {
	case BtnPrimary:
		bg, fg = p.Accent, p.AccentText
		if b.Hovered || b.pressed {
			bg = p.AccentHover
		}
	case BtnDanger:
		bg, fg = p.Danger, hex(0xFFFFFF)
		if p.Dark {
			fg = hex(0x2A0E10)
		}
		if b.Hovered {
			bg = p.Danger.Mix(p.Text, 0.12)
		}
	case BtnSecondary:
		bg, fg, border = p.Surface2, p.Text, p.BorderStr
		if b.Hovered || b.pressed {
			bg = p.Hover
		}
	case BtnGhost:
		fg = p.Text2
		if b.Selected {
			bg, fg = p.AccentSoft, p.AccentInk
		}
		if b.Hovered || b.pressed {
			bg = p.Hover
			fg = p.Text
		}
	case BtnChrome:
		fg = p.Text2
		if b.Hovered {
			bg, fg = p.Hover, p.Text
		}
	case BtnChromeClose:
		fg = p.Text2
		if b.Hovered {
			bg, fg = hex(0xC42B1C), hex(0xFFFFFF)
		}
	case BtnPill:
		bg, fg = p.Accent, p.AccentText
		if b.Hovered {
			bg = p.AccentHover
		}
		rad = float64(r.H) / 2
	}
	if b.Disabled {
		if bg.A != 0 {
			bg = bg.Mix(p.Surface2, 0.55)
		}
		fg = p.Text3
		if b.Style == BtnPrimary {
			fg = p.AccentText.Mix(bg, 0.35)
		}
	}
	if b.Style == BtnChrome || b.Style == BtnChromeClose {
		rad = 0
	}
	if bg.A != 0 {
		c.FillRound(r, rad, bg)
	}
	if border.A != 0 {
		c.StrokeRound(r, rad, border, 1)
	}
	if b.focused {
		c.StrokeRound(r.Inset(-c.Si(2), -c.Si(2)), rad+c.S(2), p.Accent, c.S(2))
	}
	f := b.font()
	if b.Text == "" {
		ic := b.W.F.Icon
		if b.Style == BtnChrome || b.Style == BtnChromeClose {
			ic = b.W.F.IconSmall
		}
		c.Glyph(b.Icon, ic, fg, r)
		return
	}
	tw := f.Measure(b.Text)
	x := r.X + (r.W-tw)/2
	if b.Icon != 0 {
		iw := b.W.S(22)
		x = r.X + (r.W-tw-iw)/2
		c.Glyph(b.Icon, b.W.F.Icon, fg, Rect{x, r.Y, b.W.S(16), r.H})
		x += iw
	}
	c.Text(b.Text, f, fg, Rect{x, r.Y, r.X + r.W - x, r.H}, AlignLeft)
}

// ---------------------------------------------------------------- TextBox

// TextBox wraps a native EDIT control inside a custom-drawn frame.
type TextBox struct {
	Base
	hwnd        uintptr
	multiline   bool
	password    bool
	focused     bool
	suppress    bool
	Placeholder string
	Icon        rune
	ReadOnly    bool
	Disabled    bool
	Flat        bool // no frame (inline rename)
	OnChange    func()
	OnEnter     func()
	OnEscape    func()
	OnBlur      func()
	OnKey       func(e *KeyEvent) bool
	FontFn      func() *Font
}

// NewTextBox creates a single-line text box.
func NewTextBox(w *Window, placeholder string) *TextBox {
	return newTextBox(w, placeholder, false, false)
}

// NewPasswordBox creates a masked text box.
func NewPasswordBox(w *Window, placeholder string) *TextBox {
	return newTextBox(w, placeholder, true, false)
}

// NewMultiLine creates a multi-line text box.
func NewMultiLine(w *Window) *TextBox { return newTextBox(w, "", false, true) }

func newTextBox(w *Window, placeholder string, password, multi bool) *TextBox {
	t := &TextBox{Base: Base{W: w}, Placeholder: placeholder, password: password, multiline: multi}
	style := uintptr(WS_CHILD | WS_VISIBLE | WS_TABSTOP | ES_AUTOHSCROLL)
	if password {
		style |= ES_PASSWORD
	}
	if multi {
		style = WS_CHILD | WS_VISIBLE | WS_TABSTOP | ES_MULTILINE | ES_AUTOVSCROLL | ES_WANTRETURN | WS_VSCROLL
	}
	t.hwnd, _, _ = pCreateWindowExW.Call(0, up("EDIT"), up(""), style, 0, 0, 0, 0, w.hwnd, 0, w.app.inst, 0)
	w.edits[t.hwnd] = t
	t.applyFont()
	if placeholder != "" {
		pSendMessageW.Call(t.hwnd, EM_SETCUEBANNER, 0, up(placeholder))
	}
	if multi {
		theme := "Explorer"
		if w.app.pal.Dark {
			theme = "DarkMode_Explorer"
		}
		pSetWindowTheme.Call(t.hwnd, up(theme), 0)
	}
	pSendMessageW.Call(t.hwnd, 0x00C5 /*EM_LIMITTEXT*/, 32767, 0)
	return t
}

func (t *TextBox) applyFont() {
	f := t.W.F.Body
	if t.FontFn != nil {
		f = t.FontFn()
	}
	pSendMessageW.Call(t.hwnd, WM_SETFONT, f.h, 1)
	pSendMessageW.Call(t.hwnd, EM_SETMARGINS, EC_LEFTMARGIN|EC_RIGHTMARGIN, 0)
	t.place()
}

func (t *TextBox) bgColor() Color {
	if t.Disabled || t.ReadOnly {
		return t.W.app.pal.Surface2
	}
	if t.Flat {
		return t.W.app.pal.Sunken
	}
	return t.W.app.pal.Sunken
}

func (t *TextBox) fgColor() Color {
	if t.Disabled {
		return t.W.app.pal.Text3
	}
	return t.W.app.pal.Text
}

func (t *TextBox) SetBounds(r Rect) {
	t.R = r
	t.place()
}

func (t *TextBox) place() {
	r := t.R
	if t.Hidden || r.Empty() {
		pShowWindow.Call(t.hwnd, SW_HIDE)
		return
	}
	f := t.W.F.Body
	if t.FontFn != nil {
		f = t.FontFn()
	}
	padL := t.W.S(10)
	if t.Icon != 0 {
		padL = t.W.S(32)
	}
	padR := t.W.S(8)
	if t.Flat {
		padL, padR = t.W.S(4), t.W.S(2)
	}
	if t.multiline {
		pSetWindowPos.Call(t.hwnd, 0, uintptr(r.X+t.W.S(8)), uintptr(r.Y+t.W.S(6)), uintptr(r.W-t.W.S(12)), uintptr(r.H-t.W.S(10)), SWP_NOZORDER|SWP_NOACTIVATE|SWP_SHOWWINDOW)
		return
	}
	h := f.height + 2
	y := r.Y + (r.H-h)/2
	pSetWindowPos.Call(t.hwnd, 0, uintptr(r.X+padL), uintptr(y), uintptr(r.W-padL-padR), uintptr(h), SWP_NOZORDER|SWP_NOACTIVATE|SWP_SHOWWINDOW)
}

func (t *TextBox) Show(v bool) {
	t.Hidden = !v
	t.place()
}

func (t *TextBox) Text() string { return getWindowText(t.hwnd) }

// SetText replaces the content without firing OnChange.
func (t *TextBox) SetText(s string) {
	if t.Text() == s {
		return
	}
	t.suppress = true
	pSetWindowTextW.Call(t.hwnd, up(s))
	t.suppress = false
}

func (t *TextBox) SelectAll() { pSendMessageW.Call(t.hwnd, EM_SETSEL, 0, ^uintptr(0)) }
func (t *TextBox) Select(a, b int) {
	pSendMessageW.Call(t.hwnd, EM_SETSEL, uintptr(a), uintptr(b))
}

func (t *TextBox) SetReadOnly(v bool) {
	t.ReadOnly = v
	b := uintptr(0)
	if v {
		b = 1
	}
	pSendMessageW.Call(t.hwnd, EM_SETREADONLY, b, 0)
	pInvalidateRect.Call(t.hwnd, 0, 1)
}

func (t *TextBox) SetDisabled(v bool) {
	t.Disabled = v
	e := uintptr(1)
	if v {
		e = 0
	}
	pEnableWindow.Call(t.hwnd, e)
	pInvalidateRect.Call(t.hwnd, 0, 1)
}

func (t *TextBox) SetPlaceholder(s string) {
	t.Placeholder = s
	pSendMessageW.Call(t.hwnd, EM_SETCUEBANNER, 0, up(s))
}

func (t *TextBox) Destroy() {
	delete(t.W.edits, t.hwnd)
	pDestroyWindow.Call(t.hwnd)
}

func (t *TextBox) CanFocus() bool { return !t.Hidden && !t.Disabled }
func (t *TextBox) SetFocused(f bool) {
	if t.focused == f {
		return
	}
	t.focused = f
	if !f && t.OnBlur != nil {
		t.OnBlur()
	}
	t.W.Invalidate()
}
func (t *TextBox) OnMouse(e *MouseEvent) bool {
	if e.Kind == MouseDown {
		pSetFocus.Call(t.hwnd)
	}
	return true
}
func (t *TextBox) Cursor(x, y int) int { return IDC_IBEAM }

func (t *TextBox) Paint(c *Canvas) {
	p := c.P
	if t.Flat {
		c.FillRect(t.R, t.bgColor())
		c.StrokeRound(t.R, 0, p.Accent, c.S(1.5))
		return
	}
	rad := c.S(6)
	c.FillRound(t.R, rad, t.bgColor())
	border := p.BorderStr
	width := 1.0
	if t.focused {
		border = p.Accent
		width = c.S(1.6)
	} else if t.Hovered && !t.Disabled {
		border = p.Text3
	}
	c.StrokeRound(t.R, rad, border, width)
	if t.Icon != 0 {
		c.Glyph(t.Icon, t.W.F.Icon, p.Text3, Rect{t.R.X + t.W.S(6), t.R.Y, t.W.S(22), t.R.H})
	}
}

// IntValue parses the content as an integer.
func (t *TextBox) IntValue(def int) int {
	v, err := strconv.Atoi(t.Text())
	if err != nil {
		return def
	}
	return v
}

// ---------------------------------------------------------------- Toggle

// Toggle is an on/off switch.
type Toggle struct {
	Base
	On       bool
	OnChange func(bool)
	focused  bool
}

func NewToggle(w *Window, on bool, onChange func(bool)) *Toggle {
	return &Toggle{Base: Base{W: w}, On: on, OnChange: onChange}
}

func (t *Toggle) CanFocus() bool     { return true }
func (t *Toggle) SetFocused(f bool)  { t.focused = f; t.W.Invalidate() }
func (t *Toggle) OnChar(r rune) bool { return false }
func (t *Toggle) OnKey(e *KeyEvent) bool {
	if e.VK == VK_SPACE || e.VK == VK_RETURN {
		t.flip()
		return true
	}
	return false
}
func (t *Toggle) flip() {
	t.On = !t.On
	t.W.Invalidate()
	if t.OnChange != nil {
		t.OnChange(t.On)
	}
}
func (t *Toggle) OnMouse(e *MouseEvent) bool {
	if e.Kind == MouseUp && t.R.Contains(e.X, e.Y) {
		t.flip()
	}
	return true
}

func (t *Toggle) Paint(c *Canvas) {
	p := c.P
	w, h := t.W.S(40), t.W.S(22)
	r := Rect{t.R.X + t.R.W - w, t.R.Y + (t.R.H-h)/2, w, h}
	bg := p.ToggleOff
	if t.On {
		bg = p.Accent
		if t.Hovered {
			bg = p.AccentHover
		}
	} else if t.Hovered {
		bg = p.ToggleOff.Mix(p.Text, 0.15)
	}
	c.FillRound(r, float64(h)/2, bg)
	knob := float64(h)/2 - c.S(3)
	cx := float64(r.X) + float64(h)/2
	if t.On {
		cx = float64(r.X+r.W) - float64(h)/2
	}
	kc := hex(0xFFFFFF)
	if t.On && p.Dark {
		kc = p.AccentText
	}
	c.FillCircle(cx, float64(r.Y)+float64(h)/2, knob, kc)
	if t.focused {
		c.StrokeRound(r.Inset(-c.Si(2), -c.Si(2)), float64(h)/2+c.S(2), p.Accent, c.S(1.5))
	}
}

// ---------------------------------------------------------------- Checkbox

// Checkbox is a box with a label.
type Checkbox struct {
	Base
	Text     string
	Checked  bool
	OnChange func(bool)
	focused  bool
}

func NewCheckbox(w *Window, text string, checked bool, onChange func(bool)) *Checkbox {
	return &Checkbox{Base: Base{W: w}, Text: text, Checked: checked, OnChange: onChange}
}

func (b *Checkbox) CanFocus() bool     { return true }
func (b *Checkbox) SetFocused(f bool)  { b.focused = f; b.W.Invalidate() }
func (b *Checkbox) OnChar(r rune) bool { return false }
func (b *Checkbox) OnKey(e *KeyEvent) bool {
	if e.VK == VK_SPACE {
		b.flip()
		return true
	}
	return false
}
func (b *Checkbox) flip() {
	b.Checked = !b.Checked
	b.W.Invalidate()
	if b.OnChange != nil {
		b.OnChange(b.Checked)
	}
}
func (b *Checkbox) OnMouse(e *MouseEvent) bool {
	if e.Kind == MouseUp && b.R.Contains(e.X, e.Y) {
		b.flip()
	}
	return true
}
func (b *Checkbox) Paint(c *Canvas) {
	p := c.P
	s := b.W.S(18)
	box := Rect{b.R.X, b.R.Y + (b.R.H-s)/2, s, s}
	if b.Checked {
		c.FillRound(box, c.S(4), p.Accent)
		drawCheck(c, box, p.AccentText)
	} else {
		c.FillRound(box, c.S(4), p.Sunken)
		border := p.BorderStr
		if b.Hovered {
			border = p.Text3
		}
		c.StrokeRound(box, c.S(4), border, c.S(1.3))
	}
	if b.focused {
		c.StrokeRound(box.Inset(-c.Si(2), -c.Si(2)), c.S(5), p.Accent, c.S(1.5))
	}
	c.Text(b.Text, b.W.F.Body, p.Text, Rect{box.X + s + b.W.S(8), b.R.Y, b.R.W - s - b.W.S(8), b.R.H}, AlignLeft)
}

func drawCheck(c *Canvas, r Rect, col Color) {
	x, y, w, h := float64(r.X), float64(r.Y), float64(r.W), float64(r.H)
	c.Polyline([][2]float64{{x + w*0.25, y + h*0.52}, {x + w*0.43, y + h*0.70}, {x + w*0.76, y + h*0.32}}, col, c.S(2))
}

// ---------------------------------------------------------------- Segmented

// Segmented is a row of mutually exclusive options.
type Segmented struct {
	Base
	Options  []string
	Selected int
	OnChange func(int)
	hoverIdx int
	focused  bool
}

func NewSegmented(w *Window, opts []string, sel int, onChange func(int)) *Segmented {
	return &Segmented{Base: Base{W: w}, Options: opts, Selected: sel, OnChange: onChange, hoverIdx: -1}
}

func (s *Segmented) CanFocus() bool     { return true }
func (s *Segmented) SetFocused(f bool)  { s.focused = f; s.W.Invalidate() }
func (s *Segmented) OnChar(r rune) bool { return false }
func (s *Segmented) OnKey(e *KeyEvent) bool {
	switch e.VK {
	case VK_LEFT:
		if s.Selected > 0 {
			s.set(s.Selected - 1)
		}
		return true
	case VK_RIGHT:
		if s.Selected < len(s.Options)-1 {
			s.set(s.Selected + 1)
		}
		return true
	}
	return false
}

func (s *Segmented) set(i int) {
	if i == s.Selected {
		return
	}
	s.Selected = i
	s.W.Invalidate()
	if s.OnChange != nil {
		s.OnChange(i)
	}
}

func (s *Segmented) idxAt(x int) int {
	if len(s.Options) == 0 {
		return -1
	}
	i := (x - s.R.X) * len(s.Options) / max(1, s.R.W)
	if i < 0 || i >= len(s.Options) {
		return -1
	}
	return i
}

func (s *Segmented) OnMouse(e *MouseEvent) bool {
	switch e.Kind {
	case MouseMove:
		s.hoverIdx = s.idxAt(e.X)
		s.W.Invalidate()
	case MouseLeave:
		s.hoverIdx = -1
		s.W.Invalidate()
	case MouseUp:
		if i := s.idxAt(e.X); i >= 0 && s.R.Contains(e.X, e.Y) {
			s.set(i)
		}
	}
	return true
}

func (s *Segmented) Paint(c *Canvas) {
	p := c.P
	rad := c.S(7)
	c.FillRound(s.R, rad, p.Surface2)
	c.StrokeRound(s.R, rad, p.Border, 1)
	n := len(s.Options)
	pad := s.W.S(3)
	for i, o := range s.Options {
		x0 := s.R.X + s.R.W*i/n
		x1 := s.R.X + s.R.W*(i+1)/n
		seg := Rect{x0 + pad, s.R.Y + pad, x1 - x0 - 2*pad, s.R.H - 2*pad}
		fg := p.Text2
		if i == s.Selected {
			c.FillRound(seg, rad-c.S(2), p.Accent)
			fg = p.AccentText
		} else if i == s.hoverIdx {
			c.FillRound(seg, rad-c.S(2), p.Hover)
			fg = p.Text
		}
		f := s.W.F.Body
		if i == s.Selected {
			f = s.W.F.BodySemi
		}
		c.Text(o, f, fg, seg, AlignCenter)
	}
	if s.focused {
		c.StrokeRound(s.R.Inset(-c.Si(2), -c.Si(2)), rad+c.S(2), p.Accent, c.S(1.5))
	}
}

// ---------------------------------------------------------------- Slider

// Slider picks an integer in [Min, Max].
type Slider struct {
	Base
	Min, Max, Value int
	OnChange        func(int)
	drag            bool
	focused         bool
}

func NewSlider(w *Window, min, max, v int, onChange func(int)) *Slider {
	return &Slider{Base: Base{W: w}, Min: min, Max: max, Value: v, OnChange: onChange}
}

func (s *Slider) CanFocus() bool     { return true }
func (s *Slider) SetFocused(f bool)  { s.focused = f; s.W.Invalidate() }
func (s *Slider) OnChar(r rune) bool { return false }
func (s *Slider) OnKey(e *KeyEvent) bool {
	switch e.VK {
	case VK_LEFT, VK_DOWN:
		s.set(s.Value - 1)
		return true
	case VK_RIGHT, VK_UP:
		s.set(s.Value + 1)
		return true
	}
	return false
}

func (s *Slider) set(v int) {
	if v < s.Min {
		v = s.Min
	}
	if v > s.Max {
		v = s.Max
	}
	if v == s.Value {
		return
	}
	s.Value = v
	s.W.Invalidate()
	if s.OnChange != nil {
		s.OnChange(v)
	}
}

func (s *Slider) track() (int, int) {
	pad := s.W.S(10)
	return s.R.X + pad, s.R.X + s.R.W - pad
}

func (s *Slider) OnMouse(e *MouseEvent) bool {
	x0, x1 := s.track()
	apply := func() {
		t := float64(e.X-x0) / float64(max(1, x1-x0))
		s.set(s.Min + int(t*float64(s.Max-s.Min)+0.5))
	}
	switch e.Kind {
	case MouseDown:
		s.drag = true
		apply()
	case MouseMove:
		if s.drag {
			apply()
		}
	case MouseUp:
		s.drag = false
	}
	return true
}

func (s *Slider) Paint(c *Canvas) {
	p := c.P
	x0, x1 := s.track()
	cy := float64(s.R.Y) + float64(s.R.H)/2
	t := float64(s.Value-s.Min) / float64(max(1, s.Max-s.Min))
	kx := float64(x0) + t*float64(x1-x0)
	c.Line(float64(x0), cy, float64(x1), cy, p.Track, c.S(4))
	c.Line(float64(x0), cy, kx, cy, p.Accent, c.S(4))
	c.FillCircle(kx, cy, c.S(9), p.Accent)
	c.FillCircle(kx, cy, c.S(4), p.Surface)
	if s.focused {
		c.StrokeCircle(kx, cy, c.S(12), p.Accent.WithAlpha(120), c.S(2))
	}
}

// ---------------------------------------------------------------- Dropdown

// Dropdown selects one of several options via a popup list.
type Dropdown struct {
	Base
	Options  []string
	Selected int
	OnChange func(int)
	focused  bool
	open     bool
}

func NewDropdown(w *Window, opts []string, sel int, onChange func(int)) *Dropdown {
	return &Dropdown{Base: Base{W: w}, Options: opts, Selected: sel, OnChange: onChange}
}

func (d *Dropdown) CanFocus() bool     { return true }
func (d *Dropdown) SetFocused(f bool)  { d.focused = f; d.W.Invalidate() }
func (d *Dropdown) OnChar(r rune) bool { return false }
func (d *Dropdown) OnKey(e *KeyEvent) bool {
	switch e.VK {
	case VK_SPACE, VK_RETURN, VK_F2 + 2: // F4
		d.popup()
		return true
	case VK_UP:
		if d.Selected > 0 {
			d.set(d.Selected - 1)
		}
		return true
	case VK_DOWN:
		if e.Alt {
			d.popup()
		} else if d.Selected < len(d.Options)-1 {
			d.set(d.Selected + 1)
		}
		return true
	}
	return false
}

func (d *Dropdown) set(i int) {
	if i == d.Selected {
		return
	}
	d.Selected = i
	d.W.Invalidate()
	if d.OnChange != nil {
		d.OnChange(i)
	}
}

func (d *Dropdown) Value() string {
	if d.Selected >= 0 && d.Selected < len(d.Options) {
		return d.Options[d.Selected]
	}
	return ""
}

func (d *Dropdown) popup() {
	var items []MenuItem
	for i, o := range d.Options {
		i := i
		items = append(items, MenuItem{Label: o, Checked: i == d.Selected, Action: func() { d.set(i) }})
	}
	sx, sy := d.W.ScreenPos(d.R.X, d.R.Y+d.R.H+d.W.S(3))
	d.open = true
	d.W.app.ShowMenu(d.W, items, sx, sy, d.R.W, func() { d.open = false; d.W.Invalidate() })
}

func (d *Dropdown) OnMouse(e *MouseEvent) bool {
	if e.Kind == MouseDown && !d.open {
		d.popup()
	}
	return true
}

func (d *Dropdown) Paint(c *Canvas) {
	p := c.P
	rad := c.S(6)
	c.FillRound(d.R, rad, p.Sunken)
	border := p.BorderStr
	if d.focused || d.open {
		border = p.Accent
	} else if d.Hovered {
		border = p.Text3
	}
	c.StrokeRound(d.R, rad, border, 1)
	c.Text(d.Value(), d.W.F.Body, p.Text, Rect{d.R.X + d.W.S(10), d.R.Y, d.R.W - d.W.S(36), d.R.H}, AlignLeft)
	c.Glyph(icChevDown, d.W.F.IconSmall, p.Text2, Rect{d.R.X + d.R.W - d.W.S(30), d.R.Y, d.W.S(24), d.R.H})
}

// ---------------------------------------------------------------- Tabs

// Tabs is a row of underlined tabs.
type Tabs struct {
	Base
	Items    []string
	Badges   []string
	Selected int
	OnChange func(int)
	hoverIdx int
	widths   []int
}

func NewTabs(w *Window, items []string, onChange func(int)) *Tabs {
	return &Tabs{Base: Base{W: w}, Items: items, OnChange: onChange, hoverIdx: -1}
}

func (t *Tabs) layoutTabs() []Rect {
	var out []Rect
	x := t.R.X
	for i, it := range t.Items {
		w := t.W.F.BodySemi.Measure(it) + t.W.S(28)
		if i < len(t.Badges) && t.Badges[i] != "" {
			w += t.W.F.Tiny.Measure(t.Badges[i]) + t.W.S(20)
		}
		out = append(out, Rect{x, t.R.Y, w, t.R.H})
		x += w + t.W.S(2)
	}
	return out
}

func (t *Tabs) OnMouse(e *MouseEvent) bool {
	rs := t.layoutTabs()
	idx := -1
	for i, r := range rs {
		if r.Contains(e.X, e.Y) {
			idx = i
		}
	}
	switch e.Kind {
	case MouseMove:
		t.hoverIdx = idx
		t.W.Invalidate()
	case MouseLeave:
		t.hoverIdx = -1
		t.W.Invalidate()
	case MouseDown:
		if idx >= 0 && idx != t.Selected {
			t.Selected = idx
			t.W.Invalidate()
			if t.OnChange != nil {
				t.OnChange(idx)
			}
		}
	}
	return true
}

func (t *Tabs) Paint(c *Canvas) {
	p := c.P
	for i, r := range t.layoutTabs() {
		fg := p.Text2
		f := t.W.F.Body
		if i == t.Selected {
			fg = p.AccentInk
			f = t.W.F.BodySemi
			c.FillRect(Rect{r.X + t.W.S(8), r.Y + r.H - t.W.S(3), r.W - t.W.S(16), t.W.S(3)}, p.Accent)
		} else if i == t.hoverIdx {
			c.FillRound(r.Inset(0, t.W.S(4)), c.S(6), p.Hover)
			fg = p.Text
		}
		if i < len(t.Badges) && t.Badges[i] != "" {
			bw := t.W.F.Tiny.Measure(t.Badges[i]) + t.W.S(14)
			tw := f.Measure(t.Items[i])
			x := r.X + (r.W-tw-bw-t.W.S(6))/2
			c.Text(t.Items[i], f, fg, Rect{x, r.Y, tw + t.W.S(2), r.H}, AlignLeft)
			bh := t.W.S(20)
			drawBadge(c, x+tw+t.W.S(6), r.Y+(r.H-bh)/2, bh, t.Badges[i], c.P.AccentInk, c.P.AccentSoft, t.W.F.Tiny)
		} else {
			c.Text(t.Items[i], f, fg, r, AlignCenter)
		}
	}
}

// ---------------------------------------------------------------- Badge

// drawBadge draws a rounded pill with text and returns its width.
func drawBadge(c *Canvas, x, y, h int, text string, fg, bg Color, f *Font) int {
	w := f.Measure(text) + c.Si(14)
	c.FillRound(Rect{x, y, w, h}, float64(h)/2, bg)
	c.Text(text, f, fg, Rect{x, y, w, h}, AlignCenter)
	return w
}

var _ = unsafe.Pointer(nil)
