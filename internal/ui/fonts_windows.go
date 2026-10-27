//go:build windows

package ui

import (
	"syscall"
	"unsafe"
)

// Font is a GDI font at a specific pixel size.
type Font struct {
	h      uintptr
	height int // line height in pixels
	ascent int
}

var measureDC uintptr

func newFont(face string, px float64, weight int) *Font {
	if measureDC == 0 {
		measureDC, _, _ = pCreateCompatibleDC.Call(0)
	}
	h, _, _ := pCreateFontW.Call(uintptr(int32(-int(px+0.5))), 0, 0, 0, uintptr(weight), 0, 0, 0,
		1 /*DEFAULT_CHARSET*/, 0, 0, 5 /*CLEARTYPE_QUALITY*/, 0, up(face))
	f := &Font{h: h}
	old, _, _ := pSelectObject.Call(measureDC, h)
	var tm TEXTMETRICW
	pGetTextMetricsW.Call(measureDC, uintptr(unsafe.Pointer(&tm)))
	pSelectObject.Call(measureDC, old)
	f.height = int(tm.Height)
	f.ascent = int(tm.Ascent)
	return f
}

// Measure returns the width of s.
func (f *Font) Measure(s string) int {
	if s == "" {
		return 0
	}
	u, _ := syscall.UTF16FromString(s)
	old, _, _ := pSelectObject.Call(measureDC, f.h)
	var sz SIZE
	pGetTextExtentPoint32W.Call(measureDC, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(&sz)))
	pSelectObject.Call(measureDC, old)
	return int(sz.CX)
}

func (f *Font) free() { pDeleteObject.Call(f.h) }

var fontFaceCache = map[string]bool{}

func fontExists(face string) bool {
	if v, ok := fontFaceCache[face]; ok {
		return v
	}
	found := false
	type logfont struct {
		Height, Width, Escapement, Orientation, Weight int32
		Italic, Underline, StrikeOut, CharSet          byte
		OutPrecision, ClipPrecision, Quality, Pitch    byte
		FaceName                                       [32]uint16
	}
	var lf logfont
	lf.CharSet = 1
	copy(lf.FaceName[:], syscall.StringToUTF16(face))
	cb := syscall.NewCallback(func(a, b, c, d uintptr) uintptr {
		found = true
		return 0
	})
	dc := measureDC
	if dc == 0 {
		dc, _, _ = pCreateCompatibleDC.Call(0)
		measureDC = dc
	}
	pEnumFontFamiliesExW.Call(dc, uintptr(unsafe.Pointer(&lf)), cb, 0, 0)
	fontFaceCache[face] = found
	return found
}

func makeFonts(scale float64) *Fonts {
	ui := "Segoe UI"
	semi := "Segoe UI Semibold"
	icon := "Segoe MDL2 Assets"
	if fontExists("Segoe Fluent Icons") {
		icon = "Segoe Fluent Icons"
	}
	s := func(v float64) float64 { return v * scale }
	return &Fonts{
		Body:      newFont(ui, s(14), 400),
		BodySemi:  newFont(semi, s(14), 600),
		BodyBold:  newFont(ui, s(14), 700),
		Small:     newFont(ui, s(12.5), 400),
		SmallSemi: newFont(semi, s(12.5), 600),
		Tiny:      newFont(semi, s(11), 600),
		Label:     newFont(semi, s(13), 600),
		Title:     newFont(semi, s(17), 600),
		H1:        newFont(semi, s(24), 600),
		Brand:     newFont(ui, s(38), 700),
		BrandSub:  newFont(ui, s(17), 400),
		Mono:      newFont("Consolas", s(13), 400),
		Icon:      newFont(icon, s(15), 400),
		IconSmall: newFont(icon, s(11), 400),
		IconBig:   newFont(icon, s(20), 400),
		Menu:      newFont(ui, s(14), 400),
	}
}

func (f *Fonts) free() {
	for _, x := range []*Font{f.Body, f.BodySemi, f.BodyBold, f.Small, f.SmallSemi, f.Tiny, f.Label, f.Title, f.H1, f.Brand, f.BrandSub, f.Mono, f.Icon, f.IconSmall, f.IconBig, f.Menu} {
		x.free()
	}
}

// measureWrap returns the height of word-wrapped text at width w.
func measureWrap(f *Font, text string, w int) int {
	if text == "" {
		return 0
	}
	u, _ := syscall.UTF16FromString(text)
	old, _, _ := pSelectObject.Call(measureDC, f.h)
	rc := RECT{0, 0, int32(w), 0}
	pDrawTextW.Call(measureDC, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(&rc)), DT_WORDBREAK|DT_CALCRECT|DT_NOPREFIX)
	pSelectObject.Call(measureDC, old)
	return int(rc.Bottom)
}
