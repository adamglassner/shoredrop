//go:build windows || darwin

package ui

// Rect is an integer rectangle in device pixels.
type Rect struct{ X, Y, W, H int }

func (r Rect) Contains(x, y int) bool { return x >= r.X && y >= r.Y && x < r.X+r.W && y < r.Y+r.H }
func (r Rect) Inset(dx, dy int) Rect  { return Rect{r.X + dx, r.Y + dy, r.W - 2*dx, r.H - 2*dy} }
func (r Rect) Right() int             { return r.X + r.W }
func (r Rect) Bottom() int            { return r.Y + r.H }
func (r Rect) Empty() bool            { return r.W <= 0 || r.H <= 0 }

func intersect(a, b Rect) Rect {
	x1, y1 := max(a.X, b.X), max(a.Y, b.Y)
	x2, y2 := min(a.X+a.W, b.X+b.W), min(a.Y+a.H, b.Y+b.H)
	if x2 < x1 {
		x2 = x1
	}
	if y2 < y1 {
		y2 = y1
	}
	return Rect{x1, y1, x2 - x1, y2 - y1}
}

// Text alignment flags.
const (
	AlignLeft   = 0
	AlignCenter = 1
	AlignRight  = 2
)
