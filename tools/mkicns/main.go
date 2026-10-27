// Command mkicns renders the ShoreDrop macOS app icon (the wave-and-arrow
// mark on a rounded tile, following the macOS icon grid) and writes it as an
// .icns file, plus an optional 1024 px PNG.
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"

	"shoredrop/internal/logo"
)

func main() {
	out := flag.String("o", "ShoreDrop.icns", "output .icns")
	pngOut := flag.String("png", "", "also write the 1024 px icon as PNG")
	flag.Parse()

	cache := map[int][]byte{}
	render := func(size int) []byte {
		if b, ok := cache[size]; ok {
			return b
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, icon(size)); err != nil {
			panic(err)
		}
		cache[size] = buf.Bytes()
		return buf.Bytes()
	}
	entries := []struct {
		typ  string
		size int
	}{
		{"icp4", 16}, {"icp5", 32}, {"icp6", 64}, {"ic07", 128}, {"ic08", 256}, {"ic09", 512},
		{"ic10", 1024}, {"ic11", 32}, {"ic12", 64}, {"ic13", 256}, {"ic14", 512},
	}
	var body bytes.Buffer
	for _, e := range entries {
		data := render(e.size)
		body.WriteString(e.typ)
		binary.Write(&body, binary.BigEndian, uint32(len(data)+8))
		body.Write(data)
	}
	var file bytes.Buffer
	file.WriteString("icns")
	binary.Write(&file, binary.BigEndian, uint32(body.Len()+8))
	file.Write(body.Bytes())
	must(os.WriteFile(*out, file.Bytes(), 0o644))
	if *pngOut != "" {
		must(os.WriteFile(*pngOut, render(1024), 0o644))
	}
	fmt.Println("wrote", *out)
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// inTile reports whether (x, y) in unit tile space (-1..1) is inside the
// rounded-square "squircle" used by macOS icons (a superellipse).
func inTile(x, y float64) bool {
	const n = 5.0
	return math.Pow(math.Abs(x), n)+math.Pow(math.Abs(y), n) <= 1
}

// icon renders the app icon at size×size pixels. On the macOS grid the tile
// is 824/1024 of the canvas, centred, with a soft shadow below it.
func icon(size int) *image.RGBA {
	s := float64(size)
	tile := s * 824 / 1024
	off := (s - tile) / 2
	half := tile / 2
	cx, cy := off+half, off+half

	// coverage of the tile, supersampled
	const ss = 4
	cov := make([]float64, size*size)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			hit := 0
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					px := (float64(x) + (float64(sx)+0.5)/ss - cx) / half
					py := (float64(y) + (float64(sy)+0.5)/ss - cy) / half
					if inTile(px, py) {
						hit++
					}
				}
			}
			cov[y*size+x] = float64(hit) / (ss * ss)
		}
	}

	// shadow: the tile mask moved down and blurred
	shadow := make([]float64, size*size)
	dy := int(math.Round(s * 10 / 1024))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if sy := y - dy; sy >= 0 {
				shadow[y*size+x] = cov[sy*size+x]
			}
		}
	}
	r := int(math.Round(s * 14 / 1024))
	for i := 0; i < 3 && r > 0; i++ {
		boxBlur(shadow, size, r)
	}

	img := image.NewRGBA(image.Rect(0, 0, size, size))
	top := [3]float64{0xFF, 0xFF, 0xFF}
	bot := [3]float64{0xDD, 0xF1, 0xF0}
	for y := 0; y < size; y++ {
		t := (float64(y) - off) / tile
		t = math.Max(0, math.Min(1, t))
		var fill [3]float64
		for k := range fill {
			fill[k] = top[k] + (bot[k]-top[k])*t
		}
		for x := 0; x < size; x++ {
			i := y*size + x
			a := cov[i]
			sa := shadow[i] * 0.30 * (1 - a)
			// premultiplied: shadow (black) under tile
			R := fill[0] * a
			G := fill[1] * a
			B := fill[2] * a
			A := a + sa
			if A > 0 {
				img.SetRGBA(x, y, color.RGBA{uint8(R + 0.5), uint8(G + 0.5), uint8(B + 0.5), uint8(math.Min(255, A*255) + 0.5)})
			}
		}
	}

	// a hairline edge keeps the white tile visible on light backgrounds
	if size >= 32 {
		edge := make([]float64, size*size)
		for i, a := range cov {
			if a > 0 && a < 1 {
				edge[i] = 1
			}
		}
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				i := y*size + x
				if edge[i] == 0 {
					continue
				}
				c := img.RGBAAt(x, y)
				k := 0.12
				c.R = uint8(float64(c.R) * (1 - k))
				c.G = uint8(float64(c.G) * (1 - k))
				c.B = uint8(float64(c.B) * (1 - k))
				img.SetRGBA(x, y, c)
			}
		}
	}

	// the mark
	ls := int(math.Round(tile * 0.70))
	mark := logo.Rasterize(ls, logo.Teal, 0)
	lx := int(math.Round(cx - float64(ls)/2))
	ly := int(math.Round(cy - float64(ls)/2 + tile*0.01))
	for y := 0; y < ls; y++ {
		for x := 0; x < ls; x++ {
			m := mark.RGBAAt(x, y)
			if m.A == 0 {
				continue
			}
			dx, dy := lx+x, ly+y
			if dx < 0 || dy < 0 || dx >= size || dy >= size {
				continue
			}
			d := img.RGBAAt(dx, dy)
			fa := float64(m.A) / 255
			blend := func(src, dst uint8) uint8 { return uint8(float64(src) + float64(dst)*(1-fa) + 0.5) }
			img.SetRGBA(dx, dy, color.RGBA{blend(m.R, d.R), blend(m.G, d.G), blend(m.B, d.B), blend(m.A, d.A)})
		}
	}
	return img
}

// boxBlur blurs v (size×size) in place with a box of radius r.
func boxBlur(v []float64, size, r int) {
	tmp := make([]float64, len(v))
	w := float64(2*r + 1)
	for y := 0; y < size; y++ {
		sum := 0.0
		for x := -r; x <= r; x++ {
			sum += v[y*size+clamp(x, size)]
		}
		for x := 0; x < size; x++ {
			tmp[y*size+x] = sum / w
			sum += v[y*size+clamp(x+r+1, size)] - v[y*size+clamp(x-r, size)]
		}
	}
	for x := 0; x < size; x++ {
		sum := 0.0
		for y := -r; y <= r; y++ {
			sum += tmp[clamp(y, size)*size+x]
		}
		for y := 0; y < size; y++ {
			v[y*size+x] = sum / w
			sum += tmp[clamp(y+r+1, size)*size+x] - tmp[clamp(y-r, size)*size+x]
		}
	}
}

func clamp(i, n int) int {
	if i < 0 {
		return 0
	}
	if i >= n {
		return n - 1
	}
	return i
}
