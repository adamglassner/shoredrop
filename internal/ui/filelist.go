//go:build windows || darwin

package ui

import (
	"path"
	"sort"
	"strings"
	"time"

	"shoredrop/internal/core"
)

// Sync comparison badges.
const (
	BadgeNone = iota
	BadgeInSync
	BadgeModified
	BadgeOnlyHere
)

// FileRow is one entry in a FileList.
type FileRow struct {
	Name     string
	Size     int64
	ModTime  time.Time
	IsDir    bool
	IsLink   bool
	IsParent bool
	IsDrive  bool
	Perm     string
	Mode     uint32
	HasMode  bool
	Hidden   bool
	Badge    int
}

// FileList is a sortable, filterable, multi-select file table.
type FileList struct {
	Base
	all  []FileRow
	rows []FileRow

	Filter   string
	SortCol  int
	SortDesc bool

	sel      map[string]bool
	anchor   int
	cursor   int
	scroll   int
	hoverRow int
	focused  bool

	EmptyText string
	EmptySub  string
	Loading   bool
	Remote    bool

	DropHighlight bool
	DropRow       int
	ShowBadges    bool

	OnOpen      func(r FileRow)
	OnContext   func(x, y int)
	OnSelChange func()
	OnKeyAction func(e *KeyEvent) bool
	OnRename    func(oldName, newName string)
	OnDragMove  func(x, y int)
	OnDragEnd   func(x, y int, cancelled bool)
	OnDragStart func() bool

	downX, downY int
	downRow      int
	pending      bool
	dragging     bool
	sbDrag       bool
	sbOff        int
	sbHover      bool
	renameBox    *TextBox
	renameName   string
	typeahead    string
	typeTime     time.Time
}

func NewFileList(w *Window, remote bool) *FileList {
	return &FileList{Base: Base{W: w}, sel: map[string]bool{}, hoverRow: -1, DropRow: -1, Remote: remote, cursor: -1}
}

func (f *FileList) rowH() int  { return f.W.S(30) }
func (f *FileList) headH() int { return f.W.S(30) }

// colX returns the x positions [name, size, date, perm, end].
func (f *FileList) cols() [5]int {
	r := f.R
	sbw := f.W.S(10)
	perm := f.W.S(104)
	date := f.W.S(150)
	size := f.W.S(84)
	end := r.X + r.W - sbw
	if r.W < f.W.S(520) {
		perm = 0
	}
	if r.W < f.W.S(400) {
		date = f.W.S(120)
	}
	p := end - perm
	d := p - date
	s := d - size
	return [5]int{r.X, s, d, p, end}
}

// SetRows replaces the content, keeping the selection where possible.
func (f *FileList) SetRows(rows []FileRow) {
	f.all = rows
	f.cancelRename()
	f.refreshView()
}

// AllRows returns the unfiltered rows.
func (f *FileList) AllRows() []FileRow { return f.all }

func wildcardMatch(pattern, name string) bool {
	if !strings.ContainsAny(pattern, "*?") {
		return strings.Contains(name, pattern)
	}
	ok, _ := path.Match(pattern, name)
	return ok
}

func (f *FileList) refreshView() {
	var rows []FileRow
	flt := strings.ToLower(strings.TrimSpace(f.Filter))
	for _, r := range f.all {
		if flt != "" && !r.IsParent && !wildcardMatch(flt, strings.ToLower(r.Name)) {
			continue
		}
		rows = append(rows, r)
	}
	col, desc := f.SortCol, f.SortDesc
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.IsParent != b.IsParent {
			return a.IsParent
		}
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		less := false
		switch col {
		case 1:
			if a.Size != b.Size {
				less = a.Size < b.Size
			} else {
				less = strings.ToLower(a.Name) < strings.ToLower(b.Name)
			}
		case 2:
			if !a.ModTime.Equal(b.ModTime) {
				less = a.ModTime.Before(b.ModTime)
			} else {
				less = strings.ToLower(a.Name) < strings.ToLower(b.Name)
			}
		case 3:
			less = a.Perm < b.Perm
		default:
			la, lb := strings.ToLower(a.Name), strings.ToLower(b.Name)
			if la == lb {
				less = a.Name < b.Name
			} else {
				less = naturalLess(la, lb)
			}
		}
		if desc {
			return !less
		}
		return less
	})
	f.rows = rows
	// drop selections that vanished
	present := map[string]bool{}
	for _, r := range rows {
		present[r.Name] = true
	}
	for k := range f.sel {
		if !present[k] {
			delete(f.sel, k)
		}
	}
	if f.cursor >= len(rows) {
		f.cursor = len(rows) - 1
	}
	f.clampScroll()
	f.W.Invalidate()
}

// naturalLess compares strings treating digit runs as numbers.
func naturalLess(a, b string) bool {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		ca, cb := a[i], b[j]
		if isDigit(ca) && isDigit(cb) {
			si, sj := i, j
			for i < len(a) && isDigit(a[i]) {
				i++
			}
			for j < len(b) && isDigit(b[j]) {
				j++
			}
			na, nb := strings.TrimLeft(a[si:i], "0"), strings.TrimLeft(b[sj:j], "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			continue
		}
		if ca != cb {
			return ca < cb
		}
		i++
		j++
	}
	return len(a)-i < len(b)-j
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// SetFilter applies a name filter (substring or wildcard).
func (f *FileList) SetFilter(s string) {
	f.Filter = s
	f.refreshView()
}

func (f *FileList) Rows() []FileRow { return f.rows }

// Selected returns the selected rows (never the ".." entry).
func (f *FileList) Selected() []FileRow {
	var out []FileRow
	for _, r := range f.rows {
		if f.sel[r.Name] && !r.IsParent {
			out = append(out, r)
		}
	}
	return out
}

func (f *FileList) SelectNames(names ...string) {
	f.sel = map[string]bool{}
	for _, n := range names {
		f.sel[n] = true
	}
	for i, r := range f.rows {
		if f.sel[r.Name] {
			f.cursor = i
			f.anchor = i
			f.ensureVisible(i)
			break
		}
	}
	f.selChanged()
}

func (f *FileList) ClearSelection() {
	f.sel = map[string]bool{}
	f.selChanged()
}

func (f *FileList) selChanged() {
	f.W.Invalidate()
	if f.OnSelChange != nil {
		f.OnSelChange()
	}
}

func (f *FileList) viewH() int { return f.R.H - f.headH() }

func (f *FileList) contentH() int { return len(f.rows) * f.rowH() }

func (f *FileList) clampScroll() {
	maxS := f.contentH() - f.viewH()
	if maxS < 0 {
		maxS = 0
	}
	if f.scroll > maxS {
		f.scroll = maxS
	}
	if f.scroll < 0 {
		f.scroll = 0
	}
}

func (f *FileList) ensureVisible(i int) {
	if i < 0 {
		return
	}
	top := i * f.rowH()
	if top < f.scroll {
		f.scroll = top
	} else if top+f.rowH() > f.scroll+f.viewH() {
		f.scroll = top + f.rowH() - f.viewH()
	}
	f.clampScroll()
}

func (f *FileList) rowAt(y int) int {
	if y < f.R.Y+f.headH() {
		return -1
	}
	i := (y - f.R.Y - f.headH() + f.scroll) / f.rowH()
	if i < 0 || i >= len(f.rows) {
		return -1
	}
	return i
}

func (f *FileList) rowRect(i int) Rect {
	return Rect{f.R.X, f.R.Y + f.headH() + i*f.rowH() - f.scroll, f.R.W - f.W.S(10), f.rowH()}
}

func (f *FileList) sbRect() (track Rect, thumb Rect, ok bool) {
	ch, vh := f.contentH(), f.viewH()
	if ch <= vh || vh <= 0 {
		return
	}
	track = Rect{f.R.X + f.R.W - f.W.S(10), f.R.Y + f.headH(), f.W.S(10), vh}
	th := max(f.W.S(28), vh*vh/ch)
	ty := track.Y + (vh-th)*f.scroll/max(1, ch-vh)
	thumb = Rect{track.X + f.W.S(2), ty, f.W.S(6), th}
	return track, thumb, true
}

func (f *FileList) CanFocus() bool { return true }
func (f *FileList) SetFocused(v bool) {
	f.focused = v
	f.W.Invalidate()
}

func (f *FileList) OnWheel(delta int, shift bool) bool {
	f.cancelRename()
	f.scroll -= delta * f.rowH() * 3 / 120
	f.clampScroll()
	f.W.Invalidate()
	return true
}

func (f *FileList) Cursor(x, y int) int { return IDC_ARROW }

func (f *FileList) clickSelect(i int, ctrl, shift bool) {
	if i < 0 {
		if !ctrl && !shift {
			f.sel = map[string]bool{}
		}
		f.selChanged()
		return
	}
	name := f.rows[i].Name
	switch {
	case shift:
		if !ctrl {
			f.sel = map[string]bool{}
		}
		a, b := f.anchor, i
		if a < 0 || a >= len(f.rows) {
			a = i
		}
		if a > b {
			a, b = b, a
		}
		for k := a; k <= b; k++ {
			f.sel[f.rows[k].Name] = true
		}
	case ctrl:
		f.sel[name] = !f.sel[name]
		if !f.sel[name] {
			delete(f.sel, name)
		}
		f.anchor = i
	default:
		f.sel = map[string]bool{name: true}
		f.anchor = i
	}
	f.cursor = i
	f.selChanged()
}

func (f *FileList) OnMouse(e *MouseEvent) bool {
	track, thumb, hasSB := f.sbRect()
	switch e.Kind {
	case MouseDown:
		if hasSB && track.Contains(e.X, e.Y) {
			f.cancelRename()
			if thumb.Contains(e.X, e.Y) {
				f.sbDrag = true
				f.sbOff = e.Y - thumb.Y
			} else {
				if e.Y < thumb.Y {
					f.scroll -= f.viewH()
				} else {
					f.scroll += f.viewH()
				}
				f.clampScroll()
				f.W.Invalidate()
			}
			return true
		}
		if e.Y < f.R.Y+f.headH() {
			cx := f.cols()
			col := 0
			for k := 1; k <= 3; k++ {
				if e.X >= cx[k] && cx[k+1] > cx[k] && e.X < cx[k+1] {
					col = k
				}
			}
			if col == f.SortCol {
				f.SortDesc = !f.SortDesc
			} else {
				f.SortCol, f.SortDesc = col, false
			}
			f.refreshView()
			return true
		}
		f.cancelRename()
		i := f.rowAt(e.Y)
		if e.Clicks == 2 && i >= 0 {
			f.sel = map[string]bool{f.rows[i].Name: true}
			f.cursor = i
			f.selChanged()
			if f.OnOpen != nil {
				f.OnOpen(f.rows[i])
			}
			return true
		}
		if i >= 0 && f.sel[f.rows[i].Name] && !e.Ctrl && !e.Shift {
			// keep multi-selection for dragging; collapse on mouse up
			f.cursor = i
		} else {
			f.clickSelect(i, e.Ctrl, e.Shift)
		}
		f.downX, f.downY, f.downRow = e.X, e.Y, i
		f.pending = i >= 0 && !f.rows[i].IsParent && !f.rows[i].IsDrive
	case MouseMove:
		if f.sbDrag {
			ch, vh := f.contentH(), f.viewH()
			th := thumb.H
			pos := e.Y - f.sbOff - track.Y
			if vh-th > 0 {
				f.scroll = pos * (ch - vh) / (vh - th)
			}
			f.clampScroll()
			f.W.Invalidate()
			return true
		}
		if f.dragging {
			if f.OnDragMove != nil {
				f.OnDragMove(e.X, e.Y)
			}
			return true
		}
		if f.pending && keyDown(0x01) {
			dx, dy := e.X-f.downX, e.Y-f.downY
			th := f.W.S(5)
			if dx*dx+dy*dy > th*th {
				f.pending = false
				if f.OnDragStart != nil && f.OnDragStart() {
					f.dragging = true
					if f.OnDragMove != nil {
						f.OnDragMove(e.X, e.Y)
					}
				}
			}
			return true
		}
		sbh := hasSB && track.Contains(e.X, e.Y)
		i := f.rowAt(e.Y)
		if sbh {
			i = -1
		}
		if i != f.hoverRow || sbh != f.sbHover {
			f.hoverRow = i
			f.sbHover = sbh
			f.W.Invalidate()
		}
	case MouseLeave:
		f.hoverRow = -1
		f.sbHover = false
		f.W.Invalidate()
	case MouseUp:
		if f.sbDrag {
			f.sbDrag = false
			return true
		}
		if f.dragging {
			f.dragging = false
			if f.OnDragEnd != nil {
				f.OnDragEnd(e.X, e.Y, false)
			}
			return true
		}
		if f.pending && !e.Ctrl && !e.Shift {
			i := f.rowAt(e.Y)
			if i == f.downRow && i >= 0 && len(f.sel) > 1 {
				f.clickSelect(i, false, false)
			}
		}
		f.pending = false
	case MouseRightDown:
		f.cancelRename()
		i := f.rowAt(e.Y)
		if i >= 0 && !f.sel[f.rows[i].Name] {
			f.clickSelect(i, false, false)
		} else if i < 0 {
			f.clickSelect(-1, false, false)
		}
	case MouseRightUp:
		if f.OnContext != nil && e.Y >= f.R.Y+f.headH() {
			f.OnContext(e.X, e.Y)
		}
	}
	return true
}

// CancelDrag aborts an in-progress drag (Escape).
func (f *FileList) CancelDrag() {
	if f.dragging {
		f.dragging = false
		if f.OnDragEnd != nil {
			f.OnDragEnd(0, 0, true)
		}
	}
}

func (f *FileList) moveCursor(to int, shift bool) {
	if len(f.rows) == 0 {
		return
	}
	if to < 0 {
		to = 0
	}
	if to >= len(f.rows) {
		to = len(f.rows) - 1
	}
	if shift {
		f.clickSelect(to, false, true)
	} else {
		f.clickSelect(to, false, false)
	}
	f.ensureVisible(to)
}

func (f *FileList) OnKey(e *KeyEvent) bool {
	if f.OnKeyAction != nil && f.OnKeyAction(e) {
		return true
	}
	page := max(1, f.viewH()/f.rowH()-1)
	switch e.VK {
	case VK_UP:
		f.moveCursor(f.cursor-1, e.Shift)
	case VK_DOWN:
		f.moveCursor(f.cursor+1, e.Shift)
	case VK_PRIOR:
		f.moveCursor(f.cursor-page, e.Shift)
	case VK_NEXT:
		f.moveCursor(f.cursor+page, e.Shift)
	case VK_HOME:
		f.moveCursor(0, e.Shift)
	case VK_END:
		f.moveCursor(len(f.rows)-1, e.Shift)
	case VK_RETURN:
		if f.cursor >= 0 && f.cursor < len(f.rows) && f.OnOpen != nil {
			f.OnOpen(f.rows[f.cursor])
		}
	case 'A':
		if !e.Ctrl {
			return false
		}
		f.sel = map[string]bool{}
		for _, r := range f.rows {
			if !r.IsParent {
				f.sel[r.Name] = true
			}
		}
		f.selChanged()
	case VK_ESCAPE:
		if f.dragging {
			f.CancelDrag()
			return true
		}
		return false
	default:
		return false
	}
	return true
}

func (f *FileList) OnChar(r rune) bool {
	if r < 32 {
		return false
	}
	if time.Since(f.typeTime) > time.Second {
		f.typeahead = ""
	}
	f.typeTime = time.Now()
	f.typeahead += strings.ToLower(string(r))
	for i, row := range f.rows {
		if strings.HasPrefix(strings.ToLower(row.Name), f.typeahead) {
			f.moveCursor(i, false)
			return true
		}
	}
	return true
}

// BeginRename shows an inline editor over the named row.
func (f *FileList) BeginRename(name string) {
	idx := -1
	for i, r := range f.rows {
		if r.Name == name {
			idx = i
		}
	}
	if idx < 0 {
		return
	}
	f.ensureVisible(idx)
	f.cancelRename()
	rr := f.rowRect(idx)
	cx := f.cols()
	x := rr.X + f.W.S(40)
	box := NewTextBox(f.W, "")
	box.Flat = true
	box.SetText(name)
	f.renameBox = box
	f.renameName = name
	box.SetBounds(Rect{x, rr.Y + f.W.S(3), cx[1] - x - f.W.S(4), rr.H - f.W.S(6)})
	f.W.Add(box)
	commit := func() {
		if f.renameBox != box {
			return
		}
		nn := strings.TrimSpace(box.Text())
		old := f.renameName
		f.cancelRename()
		if nn != "" && nn != old && f.OnRename != nil {
			f.OnRename(old, nn)
		}
	}
	box.OnEnter = commit
	box.OnBlur = commit
	box.OnEscape = func() { f.cancelRename() }
	f.W.SetFocusWidget(box)
	// select the base name without extension
	if dot := strings.LastIndex(name, "."); dot > 0 {
		box.Select(0, len([]rune(name[:dot])))
	} else {
		box.SelectAll()
	}
}

func (f *FileList) cancelRename() {
	if f.renameBox == nil {
		return
	}
	box := f.renameBox
	f.renameBox = nil
	box.OnBlur = nil
	f.W.Remove(box)
	if f.W.focus == box {
		f.W.focus = nil
	}
	box.Destroy()
	f.W.SetFocusWidget(f)
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Format("Jan 2, 2006 15:04")
}

func (f *FileList) Paint(c *Canvas) {
	p := c.P
	r := f.R
	cx := f.cols()
	hh := f.headH()
	// header
	head := Rect{r.X, r.Y, r.W, hh}
	c.FillRect(head, p.Surface2)
	c.FillRect(Rect{r.X, r.Y + hh - 1, r.W, 1}, p.Border)
	titles := []string{"Name", "Size", "Date modified", "Permissions"}
	for k := 0; k < 4; k++ {
		x0, x1 := cx[k], cx[k+1]
		if x1-x0 <= 0 {
			continue
		}
		pad := f.W.S(12)
		if k == 0 {
			pad = f.W.S(40)
		}
		tr := Rect{x0 + pad, r.Y, x1 - x0 - pad - f.W.S(16), hh}
		fg := p.Text2
		if k == f.SortCol {
			fg = p.Text
		}
		c.Text(titles[k], f.W.F.SmallSemi, fg, tr, AlignLeft)
		if k == f.SortCol {
			g := icChevUp
			if f.SortDesc {
				g = icChevDown
			}
			tw := f.W.F.SmallSemi.Measure(titles[k])
			c.Glyph(g, f.W.F.IconSmall, p.AccentInk, Rect{tr.X + tw + f.W.S(4), r.Y, f.W.S(14), hh})
		}
	}
	body := Rect{r.X, r.Y + hh, r.W, r.H - hh}
	c.PushClip(body)
	defer c.PopClip()
	if f.DropHighlight && f.DropRow < 0 {
		c.FillRect(body, p.AccentSoft)
	}
	if len(f.rows) == 0 {
		msg := f.EmptyText
		if f.Loading {
			msg = "Loading…"
		}
		if msg == "" {
			msg = "This folder is empty"
			if f.Filter != "" {
				msg = "No files match the filter"
			}
		}
		mr := Rect{body.X, body.Y + body.H/2 - f.W.S(30), body.W, f.W.S(24)}
		c.Text(msg, f.W.F.BodySemi, p.Text2, mr, AlignCenter)
		if f.EmptySub != "" && !f.Loading {
			c.Text(f.EmptySub, f.W.F.Small, p.Text3, Rect{body.X + f.W.S(16), mr.Y + mr.H + f.W.S(4), body.W - f.W.S(32), f.W.S(20)}, AlignCenter)
		}
	}
	first := f.scroll / f.rowH()
	last := min(len(f.rows), (f.scroll+f.viewH())/f.rowH()+1)
	is := f.W.S(20)
	for i := first; i < last; i++ {
		row := f.rows[i]
		rr := f.rowRect(i)
		bg := Color{}
		selected := f.sel[row.Name] && !row.IsParent
		switch {
		case f.DropHighlight && i == f.DropRow:
			bg = p.AccentSoft
		case selected && f.focused:
			bg = p.Selection
		case selected:
			bg = p.Selection.Mix(p.Surface, 0.45)
		case i == f.hoverRow:
			bg = p.Hover
		}
		if bg.A != 0 {
			c.FillRound(Rect{rr.X + f.W.S(4), rr.Y + 1, rr.W - f.W.S(6), rr.H - 2}, c.S(5), bg)
		}
		if f.DropHighlight && i == f.DropRow {
			c.StrokeRound(Rect{rr.X + f.W.S(4), rr.Y + 1, rr.W - f.W.S(6), rr.H - 2}, c.S(5), p.Accent, c.S(1.5))
		}
		if selected && f.focused {
			c.FillRound(Rect{rr.X + f.W.S(4), rr.Y + f.W.S(7), f.W.S(3), rr.H - f.W.S(14)}, c.S(1.5), p.Accent)
		}
		ix := rr.X + f.W.S(12)
		iy := rr.Y + (rr.H-is)/2
		switch {
		case row.IsParent:
			drawFolder(c, ix, iy, is, p, false)
			c.Glyph(icUp, f.W.F.IconSmall, p.Text, Rect{ix, iy + f.W.S(3), is, is})
		case row.IsDrive:
			c.Glyph('', f.W.F.Icon, p.Text2, Rect{ix, iy, is, is})
		case row.IsDir:
			drawFolder(c, ix, iy, is, p, false)
		default:
			drawFile(c, ix, iy, is, row.Name, p)
		}
		if row.IsLink {
			c.Glyph(icLink, f.W.F.IconSmall, p.AccentInk, Rect{ix + is - f.W.S(8), iy + is - f.W.S(9), f.W.S(10), f.W.S(10)})
		}
		fg := p.Text
		if selected && f.focused {
			fg = p.SelText
		}
		if row.Hidden {
			fg = p.Text2
		}
		nameR := Rect{rr.X + f.W.S(40), rr.Y, cx[1] - rr.X - f.W.S(44), rr.H}
		if f.ShowBadges && row.Badge != BadgeNone && !row.IsParent {
			var text string
			var bfg, bbg Color
			switch row.Badge {
			case BadgeInSync:
				text, bfg, bbg = "in sync", p.Success, p.SuccessSoft
			case BadgeModified:
				text, bfg, bbg = "modified", p.Warning, p.WarningSoft
			case BadgeOnlyHere:
				text, bfg, bbg = "only here", p.Info, p.Surface2
			}
			bw := f.W.F.Tiny.Measure(text) + f.W.S(14)
			if nameR.W > bw+f.W.S(60) {
				bh := f.W.S(18)
				drawBadge(c, cx[1]-bw-f.W.S(8), rr.Y+(rr.H-bh)/2, bh, text, bfg, bbg, f.W.F.Tiny)
				nameR.W -= bw + f.W.S(12)
			}
		}
		name := row.Name
		if row.IsParent {
			name = ".."
		}
		c.Text(name, f.W.F.Body, fg, nameR, AlignLeft)
		sub := p.Text2
		if selected && f.focused {
			sub = p.SelText
		}
		pad := f.W.S(12)
		if cx[2] > cx[1] {
			s := "—"
			if !row.IsDir && !row.IsParent && !row.IsDrive {
				s = core.FormatSize(row.Size)
			}
			if row.IsParent {
				s = ""
			}
			c.Text(s, f.W.F.Body, sub, Rect{cx[1] + pad, rr.Y, cx[2] - cx[1] - pad, rr.H}, AlignLeft)
		}
		if cx[3] > cx[2] && !row.IsParent {
			c.Text(formatTime(row.ModTime), f.W.F.Body, sub, Rect{cx[2] + pad, rr.Y, cx[3] - cx[2] - pad, rr.H}, AlignLeft)
		}
		if cx[4] > cx[3] && !row.IsParent {
			c.Text(row.Perm, f.W.F.Mono, sub, Rect{cx[3] + pad, rr.Y, cx[4] - cx[3] - pad, rr.H}, AlignLeft)
		}
	}
	if track, thumb, ok := f.sbRect(); ok {
		_ = track
		col := p.Text3.WithAlpha(110)
		if f.sbHover || f.sbDrag {
			col = p.Text2.WithAlpha(180)
		}
		c.FillRound(thumb, float64(thumb.W)/2, col)
	}
}
