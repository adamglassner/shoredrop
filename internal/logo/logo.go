// Package logo defines the ShoreDrop mark: three teal waves with a small,
// straight down-arrow above them. The same geometry drives the in-app
// drawing and the generated application icon.
package logo

import (
	"image"
	"image/color"
	"math"
)

// Teal is the brand colour.
var Teal = color.RGBA{0x02, 0x8A, 0x8E, 0xFF}

// Strokes returns the logo as polylines inside a size×size box, plus the
// stroke width. All strokes use round caps and joins.
func Strokes(size float64) (lines [][][2]float64, width float64) {
	width = 0.085 * size
	if width < 1.7 {
		width = 1.7
	}
	x0, x1 := 0.07, 0.93
	lambda := (x1 - x0) / 2 // two full waves across
	amp := 0.052
	for _, yc := range []float64{0.47, 0.67, 0.87} {
		var pts [][2]float64
		for i := 0; i <= 48; i++ {
			x := x0 + (x1-x0)*float64(i)/48
			y := yc - amp*math.Sin(2*math.Pi*(x-x0)/lambda)
			pts = append(pts, [2]float64{x * size, y * size})
		}
		lines = append(lines, pts)
	}
	// straight down-arrow
	ax := 0.75
	lines = append(lines,
		[][2]float64{{ax * size, 0.05 * size}, {ax * size, 0.31 * size}},
		[][2]float64{{(ax - 0.095) * size, 0.215 * size}, {ax * size, 0.31 * size}, {(ax + 0.095) * size, 0.215 * size}},
	)
	return lines, width
}

type seg struct{ ax, ay, bx, by float64 }

func distSeg(px, py float64, s seg) float64 {
	dx, dy := s.bx-s.ax, s.by-s.ay
	l2 := dx*dx + dy*dy
	t := 0.0
	if l2 > 0 {
		t = ((px-s.ax)*dx + (py-s.ay)*dy) / l2
		t = math.Max(0, math.Min(1, t))
	}
	ex, ey := s.ax+t*dx-px, s.ay+t*dy-py
	return math.Sqrt(ex*ex + ey*ey)
}

// Rasterize renders the logo at size×size pixels with anti-aliasing on a
// transparent background. pad is the fraction of the size left as margin.
func Rasterize(size int, col color.RGBA, pad float64) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	inner := float64(size) * (1 - 2*pad)
	off := float64(size) * pad
	lines, w := Strokes(inner)
	if size <= 20 {
		w = math.Max(w, 1.9)
	} else if size <= 32 {
		w = math.Max(w, 2.6)
	}
	var segs []seg
	for _, l := range lines {
		for i := 0; i+1 < len(l); i++ {
			segs = append(segs, seg{l[i][0] + off, l[i][1] + off, l[i+1][0] + off, l[i+1][1] + off})
		}
	}
	const ss = 5
	half := w / 2
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			// quick reject
			cx, cy := float64(x)+0.5, float64(y)+0.5
			near := false
			for _, s := range segs {
				if distSeg(cx, cy, s) < half+1.5 {
					near = true
					break
				}
			}
			if !near {
				continue
			}
			hits := 0
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					px := float64(x) + (float64(sx)+0.5)/ss
					py := float64(y) + (float64(sy)+0.5)/ss
					for _, s := range segs {
						if distSeg(px, py, s) <= half {
							hits++
							break
						}
					}
				}
			}
			if hits > 0 {
				a := float64(hits) / (ss * ss)
				img.SetRGBA(x, y, color.RGBA{
					uint8(float64(col.R)*a + 0.5), uint8(float64(col.G)*a + 0.5), uint8(float64(col.B)*a + 0.5), uint8(255*a + 0.5)})
			}
		}
	}
	return img
}
