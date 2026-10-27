//go:build windows || darwin

package ui

import (
	"fmt"
	"strings"
	"time"

	"shoredrop/internal/core"
)

// vscroll is a reusable thin vertical scrollbar.
type vscroll struct {
	pos   int
	drag  bool
	off   int
	hover bool
}

func (s *vscroll) geom(w *Window, area Rect, content int) (track, thumb Rect, ok bool) {
	if content <= area.H || area.H <= 0 {
		return
	}
	track = Rect{area.X + area.W - w.S(10), area.Y, w.S(10), area.H}
	th := max(w.S(28), area.H*area.H/content)
	ty := track.Y + (area.H-th)*s.pos/max(1, content-area.H)
	thumb = Rect{track.X + w.S(2), ty, w.S(6), th}
	return track, thumb, true
}

func (s *vscroll) clamp(view, content int) {
	m := content - view
	if m < 0 {
		m = 0
	}
	if s.pos > m {
		s.pos = m
	}
	if s.pos < 0 {
		s.pos = 0
	}
}

// mouse handles scrollbar interaction; returns true if consumed.
func (s *vscroll) mouse(w *Window, e *MouseEvent, area Rect, content int) bool {
	track, thumb, ok := s.geom(w, area, content)
	switch e.Kind {
	case MouseDown:
		if ok && track.Contains(e.X, e.Y) {
			if thumb.Contains(e.X, e.Y) {
				s.drag = true
				s.off = e.Y - thumb.Y
			} else if e.Y < thumb.Y {
				s.pos -= area.H
			} else {
				s.pos += area.H
			}
			s.clamp(area.H, content)
			w.Invalidate()
			return true
		}
	case MouseMove:
		if s.drag {
			if area.H-thumb.H > 0 {
				s.pos = (e.Y - s.off - track.Y) * (content - area.H) / (area.H - thumb.H)
			}
			s.clamp(area.H, content)
			w.Invalidate()
			return true
		}
		h := ok && track.Contains(e.X, e.Y)
		if h != s.hover {
			s.hover = h
			w.Invalidate()
		}
	case MouseUp:
		if s.drag {
			s.drag = false
			return true
		}
	}
	return false
}

func (s *vscroll) paint(c *Canvas, w *Window, area Rect, content int) {
	if _, thumb, ok := s.geom(w, area, content); ok {
		col := c.P.Text3.WithAlpha(110)
		if s.hover || s.drag {
			col = c.P.Text2.WithAlpha(180)
		}
		c.FillRound(thumb, float64(thumb.W)/2, col)
	}
}

// ---------------------------------------------------------------- QueueView

// QueueView shows the transfer queue as a table.
type QueueView struct {
	Base
	Q         *core.Queue
	sel       map[int64]bool
	sc        vscroll
	hoverRow  int
	hoverAct  int
	items     []*core.Item
	OnContext func(x, y int)
	focused   bool
}

func NewQueueView(w *Window, q *core.Queue) *QueueView {
	return &QueueView{Base: Base{W: w}, Q: q, sel: map[int64]bool{}, hoverRow: -1, hoverAct: -1}
}

func (v *QueueView) rowH() int  { return v.W.S(34) }
func (v *QueueView) headH() int { return v.W.S(28) }

func (v *QueueView) body() Rect {
	return Rect{v.R.X, v.R.Y + v.headH(), v.R.W, v.R.H - v.headH()}
}

// column x positions: name, dir, size, progress, speed, remaining, actions, end
func (v *QueueView) cols() [8]int {
	end := v.R.X + v.R.W - v.W.S(12)
	act := v.W.S(84)
	rem := v.W.S(82)
	spd := v.W.S(92)
	prog := v.W.S(200)
	size := v.W.S(84)
	dir := v.W.S(118)
	if v.R.W < v.W.S(900) {
		prog = v.W.S(150)
		dir = v.W.S(104)
	}
	a := end - act
	r := a - rem
	s := r - spd
	p := s - prog
	z := p - size
	d := z - dir
	return [8]int{v.R.X, d, z, p, s, r, a, end}
}

func (v *QueueView) Selected() []int64 {
	var out []int64
	for _, it := range v.items {
		if v.sel[it.ID] {
			out = append(out, it.ID)
		}
	}
	return out
}

func (v *QueueView) CanFocus() bool     { return true }
func (v *QueueView) SetFocused(f bool)  { v.focused = f; v.W.Invalidate() }
func (v *QueueView) OnChar(r rune) bool { return false }
func (v *QueueView) OnKey(e *KeyEvent) bool {
	switch e.VK {
	case VK_DELETE:
		v.Q.Remove(v.Selected()...)
		return true
	case 'A':
		if e.Ctrl {
			for _, it := range v.items {
				v.sel[it.ID] = true
			}
			v.W.Invalidate()
			return true
		}
	}
	return false
}

func (v *QueueView) OnWheel(delta int, shift bool) bool {
	v.sc.pos -= delta * v.rowH() * 3 / 120
	v.sc.clamp(v.body().H, len(v.items)*v.rowH())
	v.W.Invalidate()
	return true
}

func (v *QueueView) rowAt(y int) int {
	b := v.body()
	if y < b.Y {
		return -1
	}
	i := (y - b.Y + v.sc.pos) / v.rowH()
	if i < 0 || i >= len(v.items) {
		return -1
	}
	return i
}

// actions returns the action buttons for an item: icon and handler.
func (v *QueueView) actions(it *core.Item) []struct {
	icon rune
	fn   func()
	tip  string
} {
	type act = struct {
		icon rune
		fn   func()
		tip  string
	}
	q := v.Q
	id := it.ID
	switch it.Status {
	case core.Active, core.Queued:
		return []act{{icPause, func() { q.Pause(id) }, "Pause"}, {icClose, func() { q.Cancel(id) }, "Cancel"}}
	case core.Paused:
		return []act{{icPlay, func() { q.Resume(id) }, "Resume"}, {icClose, func() { q.Cancel(id) }, "Cancel"}}
	case core.Failed, core.Cancelled:
		return []act{{icRetry, func() { q.Retry(id) }, "Retry"}, {icDelete, func() { q.Remove(id) }, "Remove"}}
	default:
		return []act{{0, nil, ""}, {icDelete, func() { q.Remove(id) }, "Remove"}}
	}
}

func (v *QueueView) actionRects(row Rect) [2]Rect {
	cx := v.cols()
	s := v.W.S(26)
	y := row.Y + (row.H-s)/2
	return [2]Rect{{cx[6] + v.W.S(8), y, s, s}, {cx[6] + v.W.S(8) + s + v.W.S(6), y, s, s}}
}

func (v *QueueView) rowRect(i int) Rect {
	b := v.body()
	return Rect{b.X, b.Y + i*v.rowH() - v.sc.pos, b.W - v.W.S(10), v.rowH()}
}

func (v *QueueView) OnMouse(e *MouseEvent) bool {
	content := len(v.items) * v.rowH()
	if v.sc.mouse(v.W, e, v.body(), content) {
		return true
	}
	i := v.rowAt(e.Y)
	act := -1
	if i >= 0 {
		for k, ar := range v.actionRects(v.rowRect(i)) {
			if ar.Contains(e.X, e.Y) {
				act = k
			}
		}
	}
	switch e.Kind {
	case MouseMove:
		if i != v.hoverRow || act != v.hoverAct {
			v.hoverRow, v.hoverAct = i, act
			v.W.Invalidate()
		}
	case MouseLeave:
		v.hoverRow, v.hoverAct = -1, -1
		v.W.Invalidate()
	case MouseDown:
		if i < 0 {
			v.sel = map[int64]bool{}
			v.W.Invalidate()
			return true
		}
		it := v.items[i]
		if act >= 0 {
			if a := v.actions(it)[act]; a.fn != nil {
				a.fn()
			}
			return true
		}
		if e.Ctrl {
			v.sel[it.ID] = !v.sel[it.ID]
		} else {
			v.sel = map[int64]bool{it.ID: true}
		}
		v.W.Invalidate()
	case MouseRightDown:
		if i >= 0 && !v.sel[v.items[i].ID] {
			v.sel = map[int64]bool{v.items[i].ID: true}
			v.W.Invalidate()
		}
	case MouseRightUp:
		if v.OnContext != nil {
			v.OnContext(e.X, e.Y)
		}
	}
	return true
}

func (v *QueueView) Paint(c *Canvas) {
	p := c.P
	v.items = v.Q.Items()
	r := v.R
	cx := v.cols()
	hh := v.headH()
	c.FillRect(Rect{r.X, r.Y, r.W, hh}, p.Surface2)
	c.FillRect(Rect{r.X, r.Y + hh - 1, r.W, 1}, p.Border)
	heads := []string{"FILE NAME", "DIRECTION", "SIZE", "PROGRESS", "SPEED", "REMAINING", "ACTIONS"}
	for k, h := range heads {
		pad := v.W.S(10)
		if k == 0 {
			pad = v.W.S(44)
		}
		c.Text(h, v.W.F.Tiny, p.Text2, Rect{cx[k] + pad, r.Y, cx[k+1] - cx[k] - pad, hh}, AlignLeft)
	}
	body := v.body()
	content := len(v.items) * v.rowH()
	v.sc.clamp(body.H, content)
	c.PushClip(body)
	defer c.PopClip()
	if len(v.items) == 0 {
		c.Text("No transfers yet", v.W.F.BodySemi, p.Text2, Rect{body.X, body.Y + body.H/2 - v.W.S(26), body.W, v.W.S(22)}, AlignCenter)
		c.Text("Drag files between the panes, or right-click a file and choose Upload or Download.", v.W.F.Small, p.Text3,
			Rect{body.X + v.W.S(16), body.Y + body.H/2, body.W - v.W.S(32), v.W.S(20)}, AlignCenter)
		return
	}
	first := v.sc.pos / v.rowH()
	last := min(len(v.items), (v.sc.pos+body.H)/v.rowH()+1)
	for i := first; i < last; i++ {
		it := v.items[i]
		rr := v.rowRect(i)
		if v.sel[it.ID] {
			c.FillRound(Rect{rr.X + v.W.S(4), rr.Y + 1, rr.W - v.W.S(6), rr.H - 2}, c.S(5), ifc(v.focused, p.Selection, p.Selection.Mix(p.Surface, 0.45)))
		} else if i == v.hoverRow {
			c.FillRound(Rect{rr.X + v.W.S(4), rr.Y + 1, rr.W - v.W.S(6), rr.H - 2}, c.S(5), p.Hover)
		}
		if i > first || v.sc.pos == 0 {
			c.FillRect(Rect{rr.X + v.W.S(10), rr.Y + rr.H - 1, rr.W - v.W.S(20), 1}, p.Border.WithAlpha(120))
		}
		up := it.Dir == core.Upload
		statusCol := p.AccentInk
		// status icon
		ir := Rect{rr.X + v.W.S(14), rr.Y + (rr.H-v.W.S(20))/2, v.W.S(20), v.W.S(20)}
		switch it.Status {
		case core.Done:
			drawCheckCircle(c, ir.X, ir.Y, ir.W, p.Success, p.Surface)
			statusCol = p.Success
		case core.Failed:
			c.FillCircle(float64(ir.X+ir.W/2), float64(ir.Y+ir.H/2), float64(ir.W)/2, p.Danger)
			c.Text("!", v.W.F.BodyBold, p.Surface, ir, AlignCenter)
			statusCol = p.Danger
		case core.Paused:
			c.Glyph(icPause, v.W.F.Icon, p.Warning, ir)
			statusCol = p.Warning
		case core.Cancelled, core.Skipped:
			c.Glyph(icClose, v.W.F.Icon, p.Text3, ir)
			statusCol = p.Text2
		default:
			drawArrow(c, ir, up, p.AccentInk)
		}
		name := it.Name()
		if it.IsDir {
			name += "/"
		}
		c.Text(name, v.W.F.BodySemi, p.Text, Rect{rr.X + v.W.S(44), rr.Y, cx[1] - rr.X - v.W.S(48), rr.H}, AlignLeft)
		// direction / status text
		dir := "Upload"
		if !up {
			dir = "Download"
		}
		switch it.Status {
		case core.Active:
			dir = map[bool]string{true: "Uploading", false: "Downloading"}[up]
		case core.Queued:
			dir = map[bool]string{true: "Upload", false: "Download"}[up] + " · queued"
		case core.Done:
			dir = "Completed"
		default:
			dir = it.Status.String()
		}
		c.Text(dir, v.W.F.Body, statusCol, Rect{cx[1] + v.W.S(10), rr.Y, cx[2] - cx[1] - v.W.S(10), rr.H}, AlignLeft)
		size := core.FormatSize(it.Size)
		if it.IsDir {
			size = "folder"
		}
		c.Text(size, v.W.F.Body, p.Text2, Rect{cx[2] + v.W.S(10), rr.Y, cx[3] - cx[2] - v.W.S(10), rr.H}, AlignLeft)
		if it.Status == core.Failed {
			c.Text("⚠ "+it.Err, v.W.F.Small, p.Danger, Rect{cx[3] + v.W.S(10), rr.Y, cx[6] - cx[3] - v.W.S(14), rr.H}, AlignLeft)
		} else {
			// progress bar
			pr := it.Progress()
			bx := cx[3] + v.W.S(10)
			bw := cx[4] - bx - v.W.S(50)
			bh := v.W.S(8)
			by := rr.Y + (rr.H-bh)/2
			c.FillRound(Rect{bx, by, bw, bh}, float64(bh)/2, p.Track)
			fill := p.Accent
			if it.Status == core.Done {
				fill = p.Success
			} else if it.Status == core.Paused {
				fill = p.Warning
			} else if it.Status == core.Cancelled || it.Status == core.Skipped {
				fill = p.Text3
			}
			if pr > 0 {
				c.FillRound(Rect{bx, by, max(bh, int(float64(bw)*pr)), bh}, float64(bh)/2, fill)
			}
			c.Text(fmt.Sprintf("%d%%", int(pr*100+0.0001)), v.W.F.SmallSemi, p.Text, Rect{cx[4] - v.W.S(46), rr.Y, v.W.S(44), rr.H}, AlignRight)
			spd, rem := "—", "—"
			if it.Status == core.Active {
				spd = core.FormatSpeed(it.Speed)
				if d := it.Remaining(); d >= 0 {
					rem = core.FormatDuration(d) + " left"
				}
			}
			c.Text(spd, v.W.F.Body, p.Text2, Rect{cx[4] + v.W.S(10), rr.Y, cx[5] - cx[4] - v.W.S(10), rr.H}, AlignLeft)
			c.Text(rem, v.W.F.Body, p.Text2, Rect{cx[5] + v.W.S(10), rr.Y, cx[6] - cx[5] - v.W.S(10), rr.H}, AlignLeft)
		}
		for k, a := range v.actions(it) {
			if a.icon == 0 {
				continue
			}
			ar := v.actionRects(rr)[k]
			bg := p.Surface2
			fg := p.Text2
			if i == v.hoverRow && k == v.hoverAct {
				bg = p.Hover
				fg = p.Text
				if a.icon == icClose || a.icon == icDelete {
					fg = p.Danger
				}
			}
			c.FillRound(ar, c.S(6), bg)
			c.StrokeRound(ar, c.S(6), p.Border, 1)
			c.Glyph(a.icon, v.W.F.IconSmall, fg, ar)
		}
	}
	v.sc.paint(c, v.W, body, content)
}

// ---------------------------------------------------------------- LogView

// LogView shows the activity log.
type LogView struct {
	Base
	Log       *core.Log
	sc        vscroll
	follow    bool
	lastLen   int
	OnContext func(x, y int)
}

func NewLogView(w *Window, l *core.Log) *LogView {
	return &LogView{Base: Base{W: w}, Log: l, follow: true}
}

func (v *LogView) lineH() int { return v.W.F.Mono.height + v.W.S(5) }

func (v *LogView) body() Rect { return v.R.Inset(v.W.S(6), v.W.S(6)) }

func (v *LogView) OnWheel(delta int, shift bool) bool {
	v.sc.pos -= delta * v.lineH() * 3 / 120
	n := v.Log.Len()
	v.sc.clamp(v.body().H, n*v.lineH())
	v.follow = v.sc.pos >= n*v.lineH()-v.body().H-v.lineH()
	v.W.Invalidate()
	return true
}

func (v *LogView) OnMouse(e *MouseEvent) bool {
	n := v.Log.Len()
	if v.sc.mouse(v.W, e, v.body(), n*v.lineH()) {
		v.follow = v.sc.pos >= n*v.lineH()-v.body().H-v.lineH()
		return true
	}
	if e.Kind == MouseRightUp && v.OnContext != nil {
		v.OnContext(e.X, e.Y)
	}
	return true
}

func (v *LogView) Paint(c *Canvas) {
	p := c.P
	entries, _ := v.Log.Snapshot()
	b := v.body()
	content := len(entries) * v.lineH()
	if v.follow {
		v.sc.pos = content - b.H
	}
	v.sc.clamp(b.H, content)
	c.PushClip(v.R)
	defer c.PopClip()
	if len(entries) == 0 {
		c.Text("Connection and transfer activity will appear here.", v.W.F.Body, p.Text3, v.R, AlignCenter)
		return
	}
	first := v.sc.pos / v.lineH()
	last := min(len(entries), (v.sc.pos+b.H)/v.lineH()+1)
	tw := v.W.F.Mono.Measure("00:00:00") + v.W.S(14)
	for i := first; i < last; i++ {
		e := entries[i]
		y := b.Y + i*v.lineH() - v.sc.pos
		col := p.Text
		tag := ""
		switch e.Kind {
		case core.LogCommand:
			col, tag = p.Info, ""
		case core.LogResponse:
			col = p.Text2
		case core.LogSuccess:
			col = p.Success
		case core.LogWarning:
			col = p.Warning
		case core.LogError:
			col = p.Danger
		}
		c.Text(e.Time.Format("15:04:05"), v.W.F.Mono, p.Text3, Rect{b.X + v.W.S(4), y, tw, v.lineH()}, AlignLeft)
		text := strings.ReplaceAll(e.Text, "\n", " ⏎ ")
		c.Text(tag+text, v.W.F.Mono, col, Rect{b.X + tw, y, b.W - tw - v.W.S(14), v.lineH()}, AlignLeft)
	}
	v.sc.paint(c, v.W, b, content)
}

// Text returns the whole log as plain text.
func (v *LogView) Text() string {
	entries, _ := v.Log.Snapshot()
	var sb strings.Builder
	for _, e := range entries {
		sb.WriteString(e.Time.Format(time.DateTime))
		sb.WriteString("  ")
		sb.WriteString(e.Text)
		sb.WriteString("\r\n")
	}
	return sb.String()
}
