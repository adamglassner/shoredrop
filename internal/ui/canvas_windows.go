//go:build windows

package ui

import (
	"math"
	"syscall"
	"unsafe"
)

var (
	gdiplus              = syscall.NewLazyDLL("gdiplus.dll")
	gpStartup            = gdiplus.NewProc("GdiplusStartup")
	gpCreateFromHDC      = gdiplus.NewProc("GdipCreateFromHDC")
	gpDeleteGraphics     = gdiplus.NewProc("GdipDeleteGraphics")
	gpSetSmoothingMode   = gdiplus.NewProc("GdipSetSmoothingMode")
	gpSetPixelOffsetMode = gdiplus.NewProc("GdipSetPixelOffsetMode")
	gpCreateSolidFill    = gdiplus.NewProc("GdipCreateSolidFill")
	gpDeleteBrush        = gdiplus.NewProc("GdipDeleteBrush")
	gpCreatePen1         = gdiplus.NewProc("GdipCreatePen1")
	gpDeletePen          = gdiplus.NewProc("GdipDeletePen")
	gpSetPenStartCap     = gdiplus.NewProc("GdipSetPenStartCap")
	gpSetPenEndCap       = gdiplus.NewProc("GdipSetPenEndCap")
	gpSetPenLineJoin     = gdiplus.NewProc("GdipSetPenLineJoin")
	gpCreatePath         = gdiplus.NewProc("GdipCreatePath")
	gpDeletePath         = gdiplus.NewProc("GdipDeletePath")
	gpAddPathArc         = gdiplus.NewProc("GdipAddPathArc")
	gpAddPathLine        = gdiplus.NewProc("GdipAddPathLine")
	gpAddPathLine2       = gdiplus.NewProc("GdipAddPathLine2")
	gpAddPathBezier      = gdiplus.NewProc("GdipAddPathBezier")
	gpClosePathFigure    = gdiplus.NewProc("GdipClosePathFigure")
	gpStartPathFigure    = gdiplus.NewProc("GdipStartPathFigure")
	gpFillPath           = gdiplus.NewProc("GdipFillPath")
	gpDrawPath           = gdiplus.NewProc("GdipDrawPath")
	gpFillEllipse        = gdiplus.NewProc("GdipFillEllipse")
	gpDrawEllipse        = gdiplus.NewProc("GdipDrawEllipse")
	gpFillRectangle      = gdiplus.NewProc("GdipFillRectangle")
	gpDrawLine           = gdiplus.NewProc("GdipDrawLine")
	gpDrawLines          = gdiplus.NewProc("GdipDrawLines")
	gpSetClipRect        = gdiplus.NewProc("GdipSetClipRect")
	gpResetClip          = gdiplus.NewProc("GdipResetClip")
	gpFillPolygon        = gdiplus.NewProc("GdipFillPolygon")
)

var gdipToken uintptr

func initGDIPlus() {
	input := struct {
		Version                  uint32
		DebugEventCallback       uintptr
		SuppressBackgroundThread int32
		SuppressExternalCodecs   int32
	}{Version: 1}
	gpStartup.Call(uintptr(unsafe.Pointer(&gdipToken)), uintptr(unsafe.Pointer(&input)), 0)
}

func f32(v float64) uintptr { return uintptr(math.Float32bits(float32(v))) }

func argb(c Color) uintptr {
	return uintptr(uint32(c.A)<<24 | uint32(c.R)<<16 | uint32(c.G)<<8 | uint32(c.B))
}

type ptf struct{ X, Y float32 }

// Canvas draws onto a GDI device context with GDI+ for anti-aliased shapes
// and GDI for ClearType text.
type Canvas struct {
	hdc   uintptr
	g     uintptr
	w     *Window
	clips []Rect
	P     *Palette
}

func newCanvas(hdc uintptr, w *Window) *Canvas {
	c := &Canvas{hdc: hdc, w: w, P: w.app.pal}
	gpCreateFromHDC.Call(hdc, uintptr(unsafe.Pointer(&c.g)))
	gpSetSmoothingMode.Call(c.g, 4)   // AntiAlias
	gpSetPixelOffsetMode.Call(c.g, 4) // Half
	pSetBkMode.Call(hdc, TRANSPARENT)
	return c
}

func (c *Canvas) release() { gpDeleteGraphics.Call(c.g) }

// S scales a 96-DPI length.
func (c *Canvas) S(v float64) float64 { return v * c.w.scale }
func (c *Canvas) Si(v int) int        { return c.w.S(v) }

func (c *Canvas) PushClip(r Rect) {
	if len(c.clips) > 0 {
		r = intersect(r, c.clips[len(c.clips)-1])
	}
	c.clips = append(c.clips, r)
	c.applyClip()
}

func (c *Canvas) PopClip() {
	c.clips = c.clips[:len(c.clips)-1]
	c.applyClip()
}

func (c *Canvas) applyClip() {
	pRestoreDC.Call(c.hdc, ^uintptr(0)) // -1: restore last saved state
	if len(c.clips) == 0 {
		gpResetClip.Call(c.g)
		return
	}
	r := c.clips[len(c.clips)-1]
	pSaveDC.Call(c.hdc)
	pIntersectClipRect.Call(c.hdc, uintptr(r.X), uintptr(r.Y), uintptr(r.X+r.W), uintptr(r.Y+r.H))
	gpSetClipRect.Call(c.g, f32(float64(r.X)), f32(float64(r.Y)), f32(float64(r.W)), f32(float64(r.H)), 0)
}

func (c *Canvas) brush(col Color) uintptr {
	var b uintptr
	gpCreateSolidFill.Call(argb(col), uintptr(unsafe.Pointer(&b)))
	return b
}

func (c *Canvas) pen(col Color, width float64) uintptr {
	var p uintptr
	gpCreatePen1.Call(argb(col), f32(width), 2, uintptr(unsafe.Pointer(&p)))
	gpSetPenStartCap.Call(p, 2)
	gpSetPenEndCap.Call(p, 2)
	gpSetPenLineJoin.Call(p, 2)
	return p
}

// FillRect fills an axis-aligned rectangle.
func (c *Canvas) FillRect(r Rect, col Color) {
	if r.Empty() {
		return
	}
	if col.A == 255 {
		b, _, _ := pCreateSolidBrush.Call(rgb(col))
		rc := RECT{int32(r.X), int32(r.Y), int32(r.X + r.W), int32(r.Y + r.H)}
		pFillRect.Call(c.hdc, uintptr(unsafe.Pointer(&rc)), b)
		pDeleteObject.Call(b)
		return
	}
	b := c.brush(col)
	gpFillRectangle.Call(c.g, b, f32(float64(r.X)), f32(float64(r.Y)), f32(float64(r.W)), f32(float64(r.H)))
	gpDeleteBrush.Call(b)
}

func (c *Canvas) roundPath(x, y, w, h, rad float64) uintptr {
	var path uintptr
	gpCreatePath.Call(0, uintptr(unsafe.Pointer(&path)))
	if rad*2 > h {
		rad = h / 2
	}
	if rad*2 > w {
		rad = w / 2
	}
	if rad <= 0.5 {
		pts := []ptf{{float32(x), float32(y)}, {float32(x + w), float32(y)}, {float32(x + w), float32(y + h)}, {float32(x), float32(y + h)}}
		gpAddPathLine2.Call(path, uintptr(unsafe.Pointer(&pts[0])), 4)
		gpClosePathFigure.Call(path)
		return path
	}
	d := rad * 2
	gpAddPathArc.Call(path, f32(x), f32(y), f32(d), f32(d), f32(180), f32(90))
	gpAddPathArc.Call(path, f32(x+w-d), f32(y), f32(d), f32(d), f32(270), f32(90))
	gpAddPathArc.Call(path, f32(x+w-d), f32(y+h-d), f32(d), f32(d), f32(0), f32(90))
	gpAddPathArc.Call(path, f32(x), f32(y+h-d), f32(d), f32(d), f32(90), f32(90))
	gpClosePathFigure.Call(path)
	return path
}

// FillRound fills a rounded rectangle; radius in device pixels.
func (c *Canvas) FillRound(r Rect, rad float64, col Color) {
	if r.Empty() || col.A == 0 {
		return
	}
	p := c.roundPath(float64(r.X), float64(r.Y), float64(r.W), float64(r.H), rad)
	b := c.brush(col)
	gpFillPath.Call(c.g, b, p)
	gpDeleteBrush.Call(b)
	gpDeletePath.Call(p)
}

// StrokeRound outlines a rounded rectangle (stroke inside the rect).
func (c *Canvas) StrokeRound(r Rect, rad float64, col Color, width float64) {
	if r.Empty() || col.A == 0 {
		return
	}
	h := width / 2
	p := c.roundPath(float64(r.X)+h, float64(r.Y)+h, float64(r.W)-width, float64(r.H)-width, rad-h)
	pn := c.pen(col, width)
	gpDrawPath.Call(c.g, pn, p)
	gpDeletePen.Call(pn)
	gpDeletePath.Call(p)
}

// FillCircle fills a circle centred at (cx, cy).
func (c *Canvas) FillCircle(cx, cy, rad float64, col Color) {
	b := c.brush(col)
	gpFillEllipse.Call(c.g, b, f32(cx-rad), f32(cy-rad), f32(rad*2), f32(rad*2))
	gpDeleteBrush.Call(b)
}

func (c *Canvas) StrokeCircle(cx, cy, rad float64, col Color, width float64) {
	pn := c.pen(col, width)
	gpDrawEllipse.Call(c.g, pn, f32(cx-rad), f32(cy-rad), f32(rad*2), f32(rad*2))
	gpDeletePen.Call(pn)
}

// Line draws an anti-aliased line with round caps.
func (c *Canvas) Line(x1, y1, x2, y2 float64, col Color, width float64) {
	pn := c.pen(col, width)
	gpDrawLine.Call(c.g, pn, f32(x1), f32(y1), f32(x2), f32(y2))
	gpDeletePen.Call(pn)
}

// Polyline draws connected segments.
func (c *Canvas) Polyline(pts [][2]float64, col Color, width float64) {
	if len(pts) < 2 {
		return
	}
	ps := make([]ptf, len(pts))
	for i, p := range pts {
		ps[i] = ptf{float32(p[0]), float32(p[1])}
	}
	pn := c.pen(col, width)
	gpDrawLines.Call(c.g, pn, uintptr(unsafe.Pointer(&ps[0])), uintptr(len(ps)))
	gpDeletePen.Call(pn)
}

// Polygon fills a closed polygon.
func (c *Canvas) Polygon(pts [][2]float64, col Color) {
	ps := make([]ptf, len(pts))
	for i, p := range pts {
		ps[i] = ptf{float32(p[0]), float32(p[1])}
	}
	b := c.brush(col)
	gpFillPolygon.Call(c.g, b, uintptr(unsafe.Pointer(&ps[0])), uintptr(len(ps)), 0)
	gpDeleteBrush.Call(b)
}

// Text draws single-line text vertically centred in r, with ellipsis.
func (c *Canvas) Text(s string, f *Font, col Color, r Rect, align int) {
	if s == "" || r.W <= 0 {
		return
	}
	old, _, _ := pSelectObject.Call(c.hdc, f.h)
	pSetTextColor.Call(c.hdc, rgb(col))
	rc := RECT{int32(r.X), int32(r.Y), int32(r.X + r.W), int32(r.Y + r.H)}
	u, _ := syscall.UTF16FromString(s)
	flags := uintptr(DT_SINGLELINE | DT_VCENTER | DT_NOPREFIX | DT_END_ELLIPSIS)
	switch align {
	case AlignCenter:
		flags |= DT_CENTER
	case AlignRight:
		flags |= DT_RIGHT
	}
	pDrawTextW.Call(c.hdc, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(&rc)), flags)
	pSelectObject.Call(c.hdc, old)
}

// TextPath draws text with path-style ellipsis (keeps the end visible).
func (c *Canvas) TextPath(s string, f *Font, col Color, r Rect) {
	if s == "" || r.W <= 0 {
		return
	}
	old, _, _ := pSelectObject.Call(c.hdc, f.h)
	pSetTextColor.Call(c.hdc, rgb(col))
	rc := RECT{int32(r.X), int32(r.Y), int32(r.X + r.W), int32(r.Y + r.H)}
	u, _ := syscall.UTF16FromString(s)
	pDrawTextW.Call(c.hdc, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(&rc)),
		DT_SINGLELINE|DT_VCENTER|DT_NOPREFIX|DT_PATH_ELLIPSIS)
	pSelectObject.Call(c.hdc, old)
}

// TextWrap draws word-wrapped text from the top of r and returns its height.
func (c *Canvas) TextWrap(s string, f *Font, col Color, r Rect) int {
	if s == "" {
		return 0
	}
	old, _, _ := pSelectObject.Call(c.hdc, f.h)
	pSetTextColor.Call(c.hdc, rgb(col))
	rc := RECT{int32(r.X), int32(r.Y), int32(r.X + r.W), int32(r.Y + r.H)}
	u, _ := syscall.UTF16FromString(s)
	h, _, _ := pDrawTextW.Call(c.hdc, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(&rc)),
		DT_WORDBREAK|DT_NOPREFIX)
	pSelectObject.Call(c.hdc, old)
	return int(h)
}

// Measure returns the width of s in pixels.
func (c *Canvas) Measure(s string, f *Font) int { return f.Measure(s) }

// Glyph draws an icon from the Segoe MDL2 Assets font centred in r.
func (c *Canvas) Glyph(g rune, f *Font, col Color, r Rect) {
	c.Text(string(g), f, col, r, AlignCenter)
}
