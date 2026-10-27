//go:build windows

package ui

// tbPlat is the native EDIT control behind a TextBox.
type tbPlat struct {
	hwnd uintptr
}

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

func (t *TextBox) OnMouse(e *MouseEvent) bool {
	if e.Kind == MouseDown {
		pSetFocus.Call(t.hwnd)
	}
	return true
}
