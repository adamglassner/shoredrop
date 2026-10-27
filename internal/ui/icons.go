//go:build windows || darwin

package ui

import (
	"path/filepath"
	"strings"

	"shoredrop/internal/logo"
)

// drawLogo draws the ShoreDrop mark in a size×size box.
func drawLogo(c *Canvas, x, y, size int, col Color) {
	lines, w := logo.Strokes(float64(size))
	for _, l := range lines {
		pts := make([][2]float64, len(l))
		for i, p := range l {
			pts[i] = [2]float64{p[0] + float64(x), p[1] + float64(y)}
		}
		c.Polyline(pts, col, w)
	}
}

// drawBrand draws logo + "ShoreDrop" + an optional subtitle; returns the right edge.
func drawBrand(c *Canvas, w *Window, x, y, h int, sub string, subBig bool) int {
	p := c.P
	ls := h
	drawLogo(c, x, y, ls, p.Logo)
	tx := x + ls + w.S(14)
	f := w.F.Brand
	tw := f.Measure("ShoreDrop")
	c.Text("ShoreDrop", f, p.TitleText, Rect{tx, y - w.S(4), tw + w.S(4), h + w.S(4)}, AlignLeft)
	ex := tx + tw
	if sub != "" {
		sf := w.F.BrandSub
		if !subBig {
			sf = w.F.Small
		}
		sw := sf.Measure(sub)
		// baseline-align the subtitle with the brand text
		by := y + h - w.S(2) - sf.height + (f.height-f.ascent)/3
		if subBig {
			c.FillCircle(float64(ex+w.S(12)), float64(y+h/2+w.S(2)), c.S(2.5), p.Text3)
			c.Text(sub, sf, p.Text2, Rect{ex + w.S(22), y, sw + w.S(4), h + w.S(4)}, AlignLeft)
			ex += w.S(22) + sw
		} else {
			c.Text(sub, sf, p.Text3, Rect{ex + w.S(8), by, sw + w.S(4), sf.height}, AlignLeft)
			ex += w.S(8) + sw
		}
	}
	return ex
}

// drawFolder draws a folder icon in a size×size box.
func drawFolder(c *Canvas, x, y, size int, p *Palette, open bool) {
	s := float64(size)
	fx, fy := float64(x), float64(y)
	tab := Rect{x + int(s*0.06), y + int(s*0.18), int(s * 0.42), int(s * 0.2)}
	c.FillRound(tab, s*0.07, p.FolderTab)
	body := Rect{x + int(s*0.06), y + int(s*0.28), int(s * 0.88), int(s * 0.58)}
	c.FillRound(body, s*0.08, p.Folder)
	// subtle highlight line
	c.Line(fx+s*0.12, fy+s*0.36, fx+s*0.88, fy+s*0.36, p.Folder.Mix(hex(0xFFFFFF), 0.35), s*0.035)
}

// fileKind picks a tint for a file icon by extension.
func fileKind(name string, p *Palette) (Color, string) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	switch ext {
	case "zip", "gz", "tgz", "7z", "rar", "tar", "bz2", "xz":
		return p.Warning, "zip"
	case "png", "jpg", "jpeg", "gif", "svg", "webp", "bmp", "ico", "tif", "tiff":
		return hex(0x8A5CD1), "img"
	case "html", "htm", "css", "js", "ts", "php", "py", "go", "rb", "java", "c", "h", "cpp", "cs", "json", "xml", "yml", "yaml", "sh", "sql", "env", "toml", "ini", "conf":
		return p.AccentInk, "code"
	case "pdf":
		return p.Danger, "pdf"
	case "mp3", "wav", "flac", "ogg", "m4a", "mp4", "mov", "mkv", "avi", "webm":
		return p.Info, "media"
	case "txt", "md", "log", "csv", "doc", "docx", "rtf", "xls", "xlsx", "ppt", "pptx":
		return p.Text2, "doc"
	}
	return p.FileLine, ""
}

// drawFile draws a document icon tinted by file type.
func drawFile(c *Canvas, x, y, size int, name string, p *Palette) {
	s := float64(size)
	fx, fy := float64(x), float64(y)
	tint, kind := fileKind(name, p)
	l, t := fx+s*0.2, fy+s*0.08
	r, b := fx+s*0.8, fy+s*0.92
	fold := s * 0.2
	pts := [][2]float64{{l, t}, {r - fold, t}, {r, t + fold}, {r, b}, {l, b}}
	c.Polygon(pts, p.FileFill)
	outline := append(pts, pts[0])
	c.Polyline(outline, p.FileLine, c.S(1.2))
	c.Polyline([][2]float64{{r - fold, t}, {r - fold, t + fold}, {r, t + fold}}, p.FileLine, c.S(1.2))
	if kind != "" {
		c.FillRound(Rect{int(l + s*0.1), int(b - s*0.28), int((r - l) - s*0.2), int(s * 0.16)}, s*0.04, tint)
	} else {
		for i := 0; i < 3; i++ {
			yy := t + s*0.38 + float64(i)*s*0.14
			c.Line(l+s*0.12, yy, r-s*0.12, yy, p.FileLine.WithAlpha(150), c.S(1))
		}
	}
}

// drawStatusDot draws a small coloured status dot.
func drawStatusDot(c *Canvas, cx, cy int, col Color) {
	c.FillCircle(float64(cx), float64(cy), c.S(4.5), col.WithAlpha(60))
	c.FillCircle(float64(cx), float64(cy), c.S(3.2), col)
}

// drawCheckCircle draws a filled circle with a check mark.
func drawCheckCircle(c *Canvas, x, y, size int, bg, fg Color) {
	c.FillCircle(float64(x)+float64(size)/2, float64(y)+float64(size)/2, float64(size)/2, bg)
	drawCheck(c, Rect{x, y, size, size}, fg)
}

// drawArrow draws an up or down arrow glyph.
func drawArrow(c *Canvas, r Rect, up bool, col Color) {
	cx := float64(r.X) + float64(r.W)/2
	t, b := float64(r.Y)+float64(r.H)*0.2, float64(r.Y)+float64(r.H)*0.8
	hw := float64(r.W) * 0.28
	w := c.S(1.8)
	c.Line(cx, t, cx, b, col, w)
	if up {
		c.Polyline([][2]float64{{cx - hw, t + hw}, {cx, t}, {cx + hw, t + hw}}, col, w)
	} else {
		c.Polyline([][2]float64{{cx - hw, b - hw}, {cx, b}, {cx + hw, b - hw}}, col, w)
	}
}
