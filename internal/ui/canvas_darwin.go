//go:build darwin

package ui

/*
#include <stdlib.h>
#include "mac_darwin.h"
*/
import "C"

import (
	"math"
	"unsafe"
)

// Canvas draws with Core Graphics into the CGContext of an SDView. The view
// is flipped, so coordinates match the Windows build: points, y down.
type Canvas struct {
	ctx unsafe.Pointer
	w   *Window
	P   *Palette
}

func cc(c Color) (C.double, C.double, C.double, C.double) {
	return C.double(c.R) / 255, C.double(c.G) / 255, C.double(c.B) / 255, C.double(c.A) / 255
}

// S scales a 96-DPI length (macOS draws in points, so the scale is 1).
func (c *Canvas) S(v float64) float64 { return v * c.w.scale }
func (c *Canvas) Si(v int) int        { return c.w.S(v) }

func (c *Canvas) PushClip(r Rect) {
	C.sd_push_clip(c.ctx, C.double(r.X), C.double(r.Y), C.double(r.W), C.double(r.H))
}

func (c *Canvas) PopClip() { C.sd_pop_clip(c.ctx) }

func (c *Canvas) FillRect(r Rect, col Color) {
	if r.Empty() || col.A == 0 {
		return
	}
	R, G, B, A := cc(col)
	C.sd_fill_rect(c.ctx, C.double(r.X), C.double(r.Y), C.double(r.W), C.double(r.H), R, G, B, A)
}

// FillRound fills a rounded rectangle.
func (c *Canvas) FillRound(r Rect, rad float64, col Color) {
	if r.Empty() || col.A == 0 {
		return
	}
	R, G, B, A := cc(col)
	C.sd_fill_round(c.ctx, C.double(r.X), C.double(r.Y), C.double(r.W), C.double(r.H), C.double(rad), R, G, B, A)
}

// StrokeRound outlines a rounded rectangle (stroke inside the rect).
func (c *Canvas) StrokeRound(r Rect, rad float64, col Color, width float64) {
	if r.Empty() || col.A == 0 {
		return
	}
	R, G, B, A := cc(col)
	C.sd_stroke_round(c.ctx, C.double(r.X), C.double(r.Y), C.double(r.W), C.double(r.H), C.double(rad), C.double(width), R, G, B, A)
}

// FillCircle fills a circle centred at (cx, cy).
func (c *Canvas) FillCircle(cx, cy, rad float64, col Color) {
	R, G, B, A := cc(col)
	C.sd_fill_ellipse(c.ctx, C.double(cx-rad), C.double(cy-rad), C.double(rad*2), C.double(rad*2), R, G, B, A)
}

func (c *Canvas) StrokeCircle(cx, cy, rad float64, col Color, width float64) {
	R, G, B, A := cc(col)
	C.sd_stroke_ellipse(c.ctx, C.double(cx-rad), C.double(cy-rad), C.double(rad*2), C.double(rad*2), C.double(width), R, G, B, A)
}

func (c *Canvas) path(pts [][2]float64, fill bool, col Color, width float64) {
	if len(pts) < 2 {
		return
	}
	buf := (*[1 << 20]C.double)(C.malloc(C.size_t(len(pts) * 2 * 8)))[: len(pts)*2 : len(pts)*2]
	defer C.free(unsafe.Pointer(&buf[0]))
	for i, p := range pts {
		buf[2*i], buf[2*i+1] = C.double(p[0]), C.double(p[1])
	}
	R, G, B, A := cc(col)
	C.sd_path(c.ctx, &buf[0], C.int(len(pts)), cbool(fill), C.double(width), R, G, B, A)
}

// Line draws an anti-aliased line with round caps.
func (c *Canvas) Line(x1, y1, x2, y2 float64, col Color, width float64) {
	c.path([][2]float64{{x1, y1}, {x2, y2}}, false, col, width)
}

// Polyline draws connected segments.
func (c *Canvas) Polyline(pts [][2]float64, col Color, width float64) { c.path(pts, false, col, width) }

// Polygon fills a closed polygon.
func (c *Canvas) Polygon(pts [][2]float64, col Color) { c.path(pts, true, col, 0) }

func (c *Canvas) text(s string, f *Font, col Color, r Rect, align int, middle bool) {
	if s == "" || r.W <= 0 {
		return
	}
	t := cstr(s)
	defer C.free(unsafe.Pointer(t))
	R, G, B, A := cc(col)
	C.sd_draw_text(c.ctx, f.h, t, C.double(r.X), C.double(r.Y), C.double(r.W), C.double(r.H), C.int(align), cbool(middle), R, G, B, A)
}

// Text draws single-line text vertically centred in r, with ellipsis.
func (c *Canvas) Text(s string, f *Font, col Color, r Rect, align int) {
	c.text(s, f, col, r, align, false)
}

// TextPath draws text with the middle elided so both ends stay visible.
func (c *Canvas) TextPath(s string, f *Font, col Color, r Rect) {
	c.text(s, f, col, r, AlignLeft, true)
}

// TextWrap draws word-wrapped text from the top of r and returns its height.
func (c *Canvas) TextWrap(s string, f *Font, col Color, r Rect) int {
	if s == "" {
		return 0
	}
	t := cstr(s)
	defer C.free(unsafe.Pointer(t))
	R, G, B, A := cc(col)
	h := C.sd_wrap(c.ctx, f.h, t, C.double(r.X), C.double(r.Y), C.double(r.W), C.double(r.H), R, G, B, A)
	return int(math.Ceil(float64(h)))
}

// Measure returns the width of s.
func (c *Canvas) Measure(s string, f *Font) int { return f.Measure(s) }

// Glyph draws an icon centred in r using the SF Symbol for the icon id.
func (c *Canvas) Glyph(g rune, f *Font, col Color, r Rect) {
	if name, ok := sfSymbols[g]; ok {
		t := cstr(name)
		defer C.free(unsafe.Pointer(t))
		R, G, B, A := cc(col)
		if C.sd_draw_symbol(c.ctx, t, C.double(f.size*0.82), C.double(r.X), C.double(r.Y), C.double(r.W), C.double(r.H), R, G, B, A) != 0 {
			return
		}
	}
	c.Text(string(g), f, col, r, AlignCenter)
}

// ---------------------------------------------------------------- fonts

// Font is a Core Text font.
type Font struct {
	h      C.SDHandle
	size   float64
	height int // line height in points
	ascent int
}

const (
	fontUI   = 0
	fontMono = 1
)

func newFont(size float64, weight, kind int) *Font {
	f := &Font{h: C.sd_font_new(C.double(size), C.int(weight), C.int(kind)), size: size}
	var asc, desc, lead C.double
	C.sd_font_metrics(f.h, &asc, &desc, &lead)
	f.height = int(math.Ceil(float64(asc + desc)))
	f.ascent = int(math.Round(float64(asc)))
	return f
}

// Measure returns the width of s.
func (f *Font) Measure(s string) int {
	if s == "" {
		return 0
	}
	t := cstr(s)
	defer C.free(unsafe.Pointer(t))
	return int(math.Ceil(float64(C.sd_text_width(f.h, t))))
}

// makeFonts builds the font set. Sizes are in points and follow the macOS
// type scale (13 pt body text) rather than the Windows pixel sizes.
func makeFonts(scale float64) *Fonts {
	return &Fonts{
		Body:      newFont(13, 400, fontUI),
		BodySemi:  newFont(13, 600, fontUI),
		BodyBold:  newFont(13, 700, fontUI),
		Small:     newFont(11.5, 400, fontUI),
		SmallSemi: newFont(11.5, 600, fontUI),
		Tiny:      newFont(10.5, 600, fontUI),
		Label:     newFont(12, 600, fontUI),
		Title:     newFont(15.5, 600, fontUI),
		H1:        newFont(22, 600, fontUI),
		Brand:     newFont(34, 700, fontUI),
		BrandSub:  newFont(15.5, 400, fontUI),
		Mono:      newFont(12, 400, fontMono),
		Icon:      newFont(15, 400, fontUI),
		IconSmall: newFont(11, 400, fontUI),
		IconBig:   newFont(20, 400, fontUI),
		Menu:      newFont(13, 400, fontUI),
	}
}

// measureWrap returns the height of word-wrapped text at width w.
func measureWrap(f *Font, text string, w int) int {
	if text == "" {
		return 0
	}
	t := cstr(text)
	defer C.free(unsafe.Pointer(t))
	return int(math.Ceil(float64(C.sd_wrap(nil, f.h, t, 0, 0, C.double(w), 0, 0, 0, 0, 0))))
}

// sfSymbols maps ShoreDrop icon ids to SF Symbols available on macOS 11.
var sfSymbols = map[rune]string{
	icBack:       "chevron.backward",
	icForward:    "chevron.forward",
	icUp:         "arrow.up",
	icRefresh:    "arrow.clockwise",
	icSettings:   "gearshape",
	icSearch:     "magnifyingglass",
	icCheck:      "checkmark",
	icClose:      "xmark",
	icPause:      "pause.fill",
	icPlay:       "play.fill",
	icDownload:   "arrow.down.to.line",
	icUpload:     "arrow.up.to.line",
	icAdd:        "plus",
	icDelete:     "trash",
	icRename:     "pencil",
	icSync:       "arrow.triangle.2.circlepath",
	icHome:       "house",
	icPC:         "desktopcomputer",
	icCloud:      "cloud",
	icChevDown:   "chevron.down",
	icChevRight:  "chevron.right",
	icChevUp:     "chevron.up",
	icMin:        "minus",
	icMax:        "square",
	icRestore:    "square.on.square",
	icChromeX:    "xmark",
	icInfo:       "info.circle",
	icWarning:    "exclamationmark.triangle",
	icLock:       "lock",
	icUnlock:     "lock.open",
	icFolderAdd:  "folder.badge.plus",
	icServer:     "server.rack",
	icGlobe:      "globe",
	icLink:       "link",
	icKey:        "key",
	icCopy:       "doc.on.doc",
	icOpen:       "arrow.up.right.square",
	icFolder:     "folder",
	icList:       "list.bullet",
	icClear:      "xmark.circle",
	icSun:        "sun.max",
	icMoon:       "moon",
	icStatus:     "circle.fill",
	icMore:       "ellipsis",
	icEdit:       "pencil",
	icSave:       "square.and.arrow.down",
	icPlug:       "network",
	icHistory:    "clock.arrow.circlepath",
	icDocument:   "doc",
	icShield:     "checkmark.shield",
	icSliders:    "slider.horizontal.3",
	icPalette:    "paintpalette",
	icTransfer:   "arrow.up.arrow.down",
	icDisconnect: "eject",
}
