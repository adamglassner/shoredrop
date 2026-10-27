//go:build darwin

package ui

/*
#include <stdlib.h>
#include "mac_darwin.h"
*/
import "C"

import (
	"unicode/utf16"
	"unsafe"
)

// tbPlat is the native NSTextField (or NSTextView for multi-line boxes).
type tbPlat struct {
	nh C.SDHandle
	id int
}

var nextTextBoxID int

func newTextBox(w *Window, placeholder string, password, multi bool) *TextBox {
	t := &TextBox{Base: Base{W: w}, Placeholder: placeholder, password: password, multiline: multi}
	nextTextBoxID++
	t.id = nextTextBoxID
	t.nh = C.sd_tb_new(w.nh, C.int(t.id), cbool(password), cbool(multi))
	w.tbs[t.id] = t
	if placeholder != "" {
		t.SetPlaceholder(placeholder)
	}
	t.applyFont()
	t.applyColors()
	return t
}

func (t *TextBox) font() *Font {
	if t.FontFn != nil {
		return t.FontFn()
	}
	return t.W.F.Body
}

func (t *TextBox) applyFont() {
	if t.nh == 0 {
		return
	}
	C.sd_tb_set_font(t.nh, t.font().h)
	t.place()
}

func (t *TextBox) applyColors() {
	if t.nh == 0 {
		return
	}
	fg := t.fgColor()
	ph := t.W.app.pal.Text3
	C.sd_tb_set_colors(t.nh, C.double(fg.R)/255, C.double(fg.G)/255, C.double(fg.B)/255,
		C.double(ph.R)/255, C.double(ph.G)/255, C.double(ph.B)/255)
}

func (t *TextBox) place() {
	if t.nh == 0 {
		return
	}
	r := t.R
	if t.Hidden || r.Empty() {
		C.sd_tb_set_frame(t.nh, 0, 0, 0, 0, 0)
		return
	}
	if t.multiline {
		C.sd_tb_set_frame(t.nh, C.double(r.X+8), C.double(r.Y+6), C.double(r.W-12), C.double(r.H-10), 1)
		return
	}
	padL, padR := 10, 8
	if t.Icon != 0 {
		padL = 32
	}
	if t.Flat {
		padL, padR = 4, 2
	}
	h := t.font().height + 4
	y := r.Y + (r.H-h)/2
	C.sd_tb_set_frame(t.nh, C.double(r.X+padL), C.double(y), C.double(r.W-padL-padR), C.double(h), 1)
}

func (t *TextBox) Text() string {
	if t.nh == 0 {
		return ""
	}
	p := C.sd_tb_text(t.nh)
	defer C.free(unsafe.Pointer(p))
	return C.GoString(p)
}

// SetText replaces the content without firing OnChange.
func (t *TextBox) SetText(s string) {
	if t.nh == 0 || t.Text() == s {
		return
	}
	t.suppress = true
	cs := cstr(s)
	C.sd_tb_set_text(t.nh, cs)
	C.free(unsafe.Pointer(cs))
	t.suppress = false
}

// SelectAll selects the whole text if the box has keyboard focus (giving a
// box focus on macOS selects its text anyway).
func (t *TextBox) SelectAll() {
	if t.nh != 0 && t.W.focus == t && t.W.nh != 0 {
		C.sd_tb_select(t.nh, 0, -1)
	}
}

// Select selects runes a..b.
func (t *TextBox) Select(a, b int) {
	if t.nh == 0 {
		return
	}
	// NSText ranges count UTF-16 code units
	rs := []rune(t.Text())
	u := func(n int) int {
		if n > len(rs) {
			n = len(rs)
		}
		return len(utf16.Encode(rs[:n]))
	}
	C.sd_tb_select(t.nh, C.long(u(a)), C.long(u(b)))
}

func (t *TextBox) SetReadOnly(v bool) {
	t.ReadOnly = v
	t.applyState()
}

func (t *TextBox) SetDisabled(v bool) {
	t.Disabled = v
	t.applyState()
	t.applyColors()
	t.W.Invalidate()
}

func (t *TextBox) applyState() {
	if t.nh != 0 {
		C.sd_tb_set_state(t.nh, cbool(!t.ReadOnly && !t.Disabled), cbool(!t.Disabled))
	}
}

func (t *TextBox) SetPlaceholder(s string) {
	t.Placeholder = s
	if t.nh == 0 {
		return
	}
	cs := cstr(s)
	C.sd_tb_set_placeholder(t.nh, cs)
	C.free(unsafe.Pointer(cs))
}

func (t *TextBox) Destroy() {
	delete(t.W.tbs, t.id)
	if t.W.focus == t {
		t.W.focus = nil
	}
	if t.nh != 0 {
		C.sd_tb_destroy(t.nh)
		t.nh = 0
	}
}

func (t *TextBox) OnMouse(e *MouseEvent) bool {
	if e.Kind == MouseDown {
		t.W.nativeFocus(t)
	}
	return true
}
