//go:build windows || darwin

package ui

import (
	"fmt"
	"strconv"
	"strings"

	"shoredrop/internal/core"
)

// Dialog is a modal window with ShoreDrop chrome: a header with the logo,
// a body and a footer with buttons.
type Dialog struct {
	*Window
	Title      string
	BigHeader  bool
	FooterText string
	Buttons    []*Button
	Result     int // index of the button pressed, -1 if closed
	LayoutBody func(r Rect)
	PaintBody  func(c *Canvas, r Rect)
	closeBtn   *Button
	headerH    int
	footerH    int
	DefaultBtn int
	NoFooter   bool
}

func (a *App) newDialog(owner *Window, title string, w, h int, big bool) *Dialog {
	win := a.newWindow(winOpts{title: "ShoreDrop — " + title, w: w, h: h, owner: owner, dialog: true})
	d := &Dialog{Window: win, Title: title, BigHeader: big, Result: -1, DefaultBtn: -1}
	d.closeBtn = &Button{Base: Base{W: win}, Icon: icChromeX, Style: BtnChromeClose, OnClick: func() { d.Result = -1; win.Close() }}
	win.Add(d.closeBtn)
	win.Layout = d.layout
	win.PaintBg = d.paint
	win.OnKey = func(e *KeyEvent) bool {
		switch e.VK {
		case VK_ESCAPE:
			d.Result = -1
			win.Close()
			return true
		case VK_RETURN:
			if e.Edit != nil && e.Edit.multiline && !e.Ctrl {
				return false
			}
			if _, isBtn := win.focus.(*Button); isBtn && !e.FromEdit {
				return false
			}
			if d.DefaultBtn >= 0 && d.DefaultBtn < len(d.Buttons) && !d.Buttons[d.DefaultBtn].Disabled {
				d.Buttons[d.DefaultBtn].OnClick()
				return true
			}
		}
		return false
	}
	return d
}

// AddButton appends a footer button; the handler returns true to close.
func (d *Dialog) AddButton(text string, style int, handler func() bool) *Button {
	idx := len(d.Buttons)
	b := NewButton(d.Window, text, style, nil)
	b.Focusable = true
	b.OnClick = func() {
		if handler == nil || handler() {
			d.Result = idx
			d.Close()
		}
	}
	d.Buttons = append(d.Buttons, b)
	d.Add(b)
	return b
}

func (d *Dialog) layout() {
	w := d.Window
	d.headerH = w.S(56)
	if d.BigHeader {
		d.headerH = w.S(84)
	}
	d.footerH = w.S(64)
	if d.NoFooter {
		d.footerH = 0
	}
	w.CaptionH = d.headerH
	cs := w.S(46)
	d.closeBtn.SetBounds(Rect{w.w - cs, 0, cs, w.S(36)})
	d.closeBtn.Show(!nativeChrome)
	x := w.w - w.S(20)
	for i := len(d.Buttons) - 1; i >= 0; i-- {
		b := d.Buttons[i]
		bw := max(b.PreferredWidth(), w.S(96))
		x -= bw
		b.SetBounds(Rect{x, w.h - d.footerH + (d.footerH-w.S(36))/2, bw, w.S(36)})
		x -= w.S(10)
	}
	if d.LayoutBody != nil {
		d.LayoutBody(d.BodyRect())
	}
}

// BodyRect is the area between header and footer.
func (d *Dialog) BodyRect() Rect {
	return Rect{0, d.headerH, d.w, d.h - d.headerH - d.footerH}
}

func (d *Dialog) paint(c *Canvas) {
	p := c.P
	w := d.Window
	c.FillRect(Rect{0, 0, w.w, d.headerH}, p.Window)
	c.FillRect(Rect{0, d.headerH, w.w, w.h - d.headerH}, p.Surface)
	c.FillRect(Rect{0, d.headerH - 1, w.w, 1}, p.Border)
	if d.BigHeader {
		lh := w.S(50)
		drawBrand(c, w, w.ChromeL+w.S(22), (d.headerH-lh)/2, lh, d.Title, true)
	} else {
		ls := w.S(28)
		drawLogo(c, w.ChromeL+w.S(18), (d.headerH-ls)/2, ls, p.Logo)
		c.Text(d.Title, w.F.Title, p.TitleText, Rect{w.ChromeL + w.S(58), 0, w.w - w.ChromeL - w.S(120), d.headerH}, AlignLeft)
	}
	if !d.NoFooter {
		fy := w.h - d.footerH
		c.FillRect(Rect{0, fy, w.w, d.footerH}, p.Surface2)
		c.FillRect(Rect{0, fy, w.w, 1}, p.Border)
		if d.FooterText != "" {
			c.Text(d.FooterText, w.F.Small, p.Text2, Rect{w.S(20), fy, w.w / 2, d.footerH}, AlignLeft)
		}
	}
	if d.PaintBody != nil {
		d.PaintBody(c, d.BodyRect())
	}
}

// Run shows the dialog modally and returns the pressed button index.
func (d *Dialog) Run() int {
	d.relayout()
	if d.DefaultBtn >= 0 && d.DefaultBtn < len(d.Buttons) && d.focus == nil {
		// focus the first text box if any, else the default button
		var first Widget
		for _, x := range d.focusables() {
			if _, ok := x.(*TextBox); ok {
				first = x
				break
			}
		}
		if first == nil {
			first = d.Buttons[d.DefaultBtn]
		}
		d.focus = first
	}
	d.ShowModal()
	return d.Result
}

// Message kinds.
const (
	MsgInfo = iota
	MsgWarning
	MsgError
	MsgQuestion
	MsgSecurity
)

// MessageOpts configures a message dialog.
type MessageOpts struct {
	Kind    int
	Title   string
	Heading string
	Text    string
	Detail  string // monospace detail block (fingerprints, errors)
	Buttons []string
	Primary int // index of the primary (default) button
	Danger  bool
	Check   string // optional checkbox label
	Checked bool
}

// Message shows a modal message and returns the button index and checkbox state.
func (a *App) Message(owner *Window, o MessageOpts) (int, bool) {
	if len(o.Buttons) == 0 {
		o.Buttons = []string{"OK"}
	}
	scale := 1.0
	if owner != nil {
		scale = owner.scale
	}
	S := func(v int) int { return int(float64(v)*scale + 0.5) }
	fonts := a.fontsFor(int(scale * 96))
	textW := 480
	th := measureWrap(fonts.Body, o.Text, S(textW))
	dh := 0
	if o.Detail != "" {
		dh = measureWrap(fonts.Mono, o.Detail, S(textW-30)) + S(26)
	}
	hh := measureWrap(fonts.Title, o.Heading, S(textW))
	bodyH := int(float64(S(24)+hh+S(10)+th+S(18)+dh)/scale) + 20
	if o.Check != "" {
		bodyH += 38
	}
	if bodyH < 120 {
		bodyH = 120
	}
	title := o.Title
	if title == "" {
		title = "ShoreDrop"
	}
	d := a.newDialog(owner, title, 600, 56+bodyH+64, false)
	var cb *Checkbox
	if o.Check != "" {
		cb = NewCheckbox(d.Window, o.Check, o.Checked, nil)
		d.Add(cb)
	}
	for i, b := range o.Buttons {
		style := BtnSecondary
		if i == o.Primary {
			style = BtnPrimary
			if o.Danger {
				style = BtnDanger
			}
		}
		d.AddButton(b, style, nil)
	}
	d.DefaultBtn = o.Primary
	d.LayoutBody = func(r Rect) {
		if cb != nil {
			cb.SetBounds(Rect{r.X + d.S(84), r.Y + r.H - d.S(46), r.W - d.S(110), d.S(28)})
		}
	}
	d.PaintBody = func(c *Canvas, r Rect) {
		p := c.P
		ic := Rect{r.X + d.S(24), r.Y + d.S(24), d.S(40), d.S(40)}
		var col Color
		var g rune
		switch o.Kind {
		case MsgWarning, MsgSecurity:
			col, g = p.Warning, icWarning
		case MsgError:
			col, g = p.Danger, icWarning
		case MsgQuestion:
			col, g = p.AccentInk, '?'
		default:
			col, g = p.AccentInk, icInfo
		}
		if o.Kind == MsgSecurity {
			g = icLock
		}
		c.FillCircle(float64(ic.X+ic.W/2), float64(ic.Y+ic.H/2), float64(ic.W)/2, col.WithAlpha(40))
		if g == '?' {
			c.Text("?", d.F.Title, col, ic, AlignCenter)
		} else {
			c.Glyph(g, d.F.IconBig, col, ic)
		}
		x := r.X + d.S(84)
		y := r.Y + d.S(24)
		tw := d.S(textW)
		if o.Heading != "" {
			h := c.TextWrap(o.Heading, d.F.Title, p.Text, Rect{x, y, tw, d.S(200)})
			y += h + d.S(10)
		}
		h := c.TextWrap(o.Text, d.F.Body, p.Text2, Rect{x, y, tw, d.S(400)})
		y += h + d.S(14)
		if o.Detail != "" {
			dr := Rect{x, y, tw, dh}
			c.FillRound(dr, c.S(6), p.Surface2)
			c.StrokeRound(dr, c.S(6), p.Border, 1)
			c.TextWrap(o.Detail, d.F.Mono, p.Text, dr.Inset(d.S(10), d.S(8)))
		}
	}
	res := d.Run()
	checked := false
	if cb != nil {
		checked = cb.Checked
	}
	return res, checked
}

// Alert shows a simple message with an OK button.
func (a *App) Alert(owner *Window, kind int, heading, text string) {
	a.Message(owner, MessageOpts{Kind: kind, Heading: heading, Text: text})
}

// Confirm asks a yes/no question.
func (a *App) Confirm(owner *Window, heading, text, yes string, danger bool) bool {
	r, _ := a.Message(owner, MessageOpts{Kind: MsgQuestion, Heading: heading, Text: text, Buttons: []string{"Cancel", yes}, Primary: 1, Danger: danger})
	return r == 1
}

// Prompt asks for a line of text.
func (a *App) Prompt(owner *Window, title, label, initial string, password bool) (string, bool) {
	d := a.newDialog(owner, title, 480, 56+132+64, false)
	var tb *TextBox
	if password {
		tb = NewPasswordBox(d.Window, "")
	} else {
		tb = NewTextBox(d.Window, "")
	}
	tb.SetText(initial)
	d.Add(tb)
	var result string
	d.AddButton("Cancel", BtnSecondary, nil)
	d.AddButton("OK", BtnPrimary, func() bool { result = tb.Text(); return true })
	d.DefaultBtn = 1
	d.LayoutBody = func(r Rect) {
		tb.SetBounds(Rect{r.X + d.S(24), r.Y + d.S(62), r.W - d.S(48), d.S(36)})
	}
	d.PaintBody = func(c *Canvas, r Rect) {
		c.TextWrap(label, d.F.Body, c.P.Text, Rect{r.X + d.S(24), r.Y + d.S(20), r.W - d.S(48), d.S(40)})
	}
	d.focus = tb
	tb.SelectAll()
	if d.Run() != 1 {
		return "", false
	}
	return result, true
}

// ChmodDialog edits permission bits. Returns mode, recursive, ok.
func (a *App) ChmodDialog(owner *Window, names []string, mode uint32, hasDirs bool) (uint32, bool, bool) {
	d := a.newDialog(owner, "Change permissions", 520, 56+330+64, false)
	who := []string{"Owner", "Group", "Public"}
	what := []string{"Read", "Write", "Execute"}
	var boxes [3][3]*Checkbox
	num := NewTextBox(d.Window, "")
	syncing := false
	update := func() {
		if syncing {
			return
		}
		var m uint32
		for i := 0; i < 3; i++ {
			for j := 0; j < 3; j++ {
				if boxes[i][j].Checked {
					m |= 1 << uint((2-i)*3+(2-j))
				}
			}
		}
		syncing = true
		num.SetText(fmt.Sprintf("%03o", m|mode&0o7000))
		syncing = false
	}
	for i := range who {
		for j := range what {
			bit := uint32(1) << uint((2-i)*3+(2-j))
			boxes[i][j] = NewCheckbox(d.Window, what[j], mode&bit != 0, func(bool) { update() })
			d.Add(boxes[i][j])
		}
	}
	num.SetText(fmt.Sprintf("%03o", mode&0o7777))
	num.OnChange = func() {
		if syncing {
			return
		}
		v, err := strconv.ParseUint(strings.TrimSpace(num.Text()), 8, 32)
		if err != nil || v > 0o7777 {
			return
		}
		syncing = true
		for i := 0; i < 3; i++ {
			for j := 0; j < 3; j++ {
				boxes[i][j].Checked = uint32(v)&(1<<uint((2-i)*3+(2-j))) != 0
			}
		}
		syncing = false
		d.Invalidate()
	}
	d.Add(num)
	rec := NewCheckbox(d.Window, "Apply to folder contents (all files and sub-folders)", false, nil)
	rec.Show(hasDirs)
	d.Add(rec)
	d.AddButton("Cancel", BtnSecondary, nil)
	var final uint64
	d.AddButton("Apply", BtnPrimary, func() bool {
		v, err := strconv.ParseUint(strings.TrimSpace(num.Text()), 8, 32)
		final = v
		if err != nil || v > 0o7777 {
			a.Alert(d.Window, MsgError, "Invalid value", "Enter an octal permission value such as 644 or 755.")
			return false
		}
		return true
	})
	d.DefaultBtn = 1
	target := names[0]
	if len(names) > 1 {
		target = fmt.Sprintf("%d items", len(names))
	}
	d.LayoutBody = func(r Rect) {
		x0 := r.X + d.S(120)
		for i := 0; i < 3; i++ {
			y := r.Y + d.S(70) + i*d.S(40)
			for j := 0; j < 3; j++ {
				boxes[i][j].SetBounds(Rect{x0 + j*d.S(118), y, d.S(110), d.S(30)})
			}
		}
		num.SetBounds(Rect{x0, r.Y + d.S(200), d.S(110), d.S(34)})
		rec.SetBounds(Rect{r.X + d.S(24), r.Y + d.S(256), r.W - d.S(48), d.S(30)})
	}
	d.PaintBody = func(c *Canvas, r Rect) {
		p := c.P
		c.Text("Permissions for "+target, d.F.BodySemi, p.Text, Rect{r.X + d.S(24), r.Y + d.S(18), r.W - d.S(48), d.S(26)}, AlignLeft)
		c.Text("Changes the remote permission bits (chmod).", d.F.Small, p.Text2, Rect{r.X + d.S(24), r.Y + d.S(40), r.W - d.S(48), d.S(20)}, AlignLeft)
		for i, w := range who {
			c.Text(w, d.F.Label, p.Text, Rect{r.X + d.S(24), r.Y + d.S(70) + i*d.S(40), d.S(90), d.S(30)}, AlignLeft)
		}
		c.Text("Numeric value", d.F.Label, p.Text, Rect{r.X + d.S(24), r.Y + d.S(200), d.S(96), d.S(34)}, AlignLeft)
		c.Text("e.g. 644 for files, 755 for folders", d.F.Small, p.Text3, Rect{r.X + d.S(244), r.Y + d.S(200), d.S(240), d.S(34)}, AlignLeft)
	}
	if d.Run() != 1 {
		return 0, false, false
	}
	return uint32(final), rec.Checked && hasDirs, true
}

// About shows the About box.
func (a *App) About(owner *Window) {
	d := a.newDialog(owner, "About ShoreDrop", 520, 56+330+64, false)
	d.AddButton("Close", BtnPrimary, nil)
	d.DefaultBtn = 0
	d.FooterText = "ShoreDrop v" + core.AppVersion
	d.PaintBody = func(c *Canvas, r Rect) {
		p := c.P
		ls := d.S(84)
		drawLogo(c, r.X+(r.W-ls)/2, r.Y+d.S(26), ls, p.Logo)
		c.Text("ShoreDrop", d.F.Brand, p.TitleText, Rect{r.X, r.Y + d.S(118), r.W, d.S(50)}, AlignCenter)
		c.Text("Version "+core.AppVersion, d.F.BodySemi, p.AccentInk, Rect{r.X, r.Y + d.S(168), r.W, d.S(24)}, AlignCenter)
		c.Text("A modern FTP, FTPS and SFTP client.", d.F.Body, p.Text2, Rect{r.X, r.Y + d.S(198), r.W, d.S(22)}, AlignCenter)
		c.Text("Saved passwords are kept in "+core.CredentialStoreName+".", d.F.Small, p.Text2, Rect{r.X, r.Y + d.S(226), r.W, d.S(20)}, AlignCenter)
		c.Text("Settings: "+core.ConfigDir(), d.F.Small, p.Text3, Rect{r.X + d.S(20), r.Y + d.S(252), r.W - d.S(40), d.S(20)}, AlignCenter)
		c.Text("Keyboard shortcuts: press "+helpShortcut+" in the main window.", d.F.Small, p.Text3, Rect{r.X, r.Y + d.S(276), r.W, d.S(20)}, AlignCenter)
	}
	d.Run()
}
