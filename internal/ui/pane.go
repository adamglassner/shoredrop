//go:build windows

package ui

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"shoredrop/internal/core"
	"shoredrop/internal/remote"
)

// Pane is one side of the dual-pane browser.
type Pane struct {
	m        *MainWindow
	w        *Window
	isRemote bool

	list                               *FileList
	path, filter                       *TextBox
	btnBack, btnFwd, btnUp, btnRefresh *Button

	dir     string
	hist    []string
	fut     []string
	rect    Rect
	loading bool
	gen     int
	entries map[string]remote.Entry

	pendingSelect string
}

// Navigation modes.
const (
	navUser = iota
	navSync
	navRefresh
)

func newPane(m *MainWindow, isRemote bool) *Pane {
	w := m.w
	p := &Pane{m: m, w: w, isRemote: isRemote, entries: map[string]remote.Entry{}}
	p.list = NewFileList(w, isRemote)
	p.path = NewTextBox(w, "")
	p.path.Icon = icFolder
	p.filter = NewTextBox(w, "Filter")
	p.filter.Icon = icSearch
	p.btnBack = NewIconButton(w, icBack, p.back)
	p.btnFwd = NewIconButton(w, icForward, p.forward)
	p.btnUp = NewIconButton(w, icUp, p.up)
	p.btnRefresh = NewIconButton(w, icRefresh, p.refresh)
	w.Add(p.list, p.path, p.filter, p.btnBack, p.btnFwd, p.btnUp, p.btnRefresh)

	p.path.OnEnter = func() {
		t := strings.TrimSpace(p.path.Text())
		if !isRemote {
			t = normalizeLocal(t, p.dir)
		} else if !strings.HasPrefix(t, "/") {
			t = path.Join(p.dir, t)
		}
		p.navigate(t, true, navUser)
		w.SetFocusWidget(p.list)
	}
	p.path.OnEscape = func() { p.path.SetText(p.displayPath()); w.SetFocusWidget(p.list) }
	p.filter.OnChange = func() { p.list.SetFilter(p.filter.Text()) }
	p.filter.OnEscape = func() { p.filter.SetText(""); p.list.SetFilter(""); w.SetFocusWidget(p.list) }
	p.filter.OnEnter = func() { w.SetFocusWidget(p.list) }

	p.list.OnOpen = p.open
	p.list.OnContext = func(x, y int) { m.paneContext(p, x, y) }
	p.list.OnRename = func(o, n string) { m.doRename(p, o, n) }
	p.list.OnSelChange = func() { m.active = p; w.Invalidate() }
	p.list.OnDragStart = func() bool { return m.beginDrag(p) }
	p.list.OnDragMove = func(x, y int) { m.dragMove(x, y) }
	p.list.OnDragEnd = func(x, y int, cancel bool) { m.dragEnd(x, y, cancel) }
	return p
}

func (p *Pane) layout(r Rect) {
	S := p.w.S
	p.rect = r
	navY := r.Y + S(58)
	bs := S(32)
	x := r.X + S(10)
	for _, b := range []*Button{p.btnBack, p.btnFwd, p.btnUp} {
		b.SetBounds(Rect{x, navY, bs, bs})
		x += bs + S(2)
	}
	p.btnRefresh.SetBounds(Rect{r.X + r.W - S(12) - bs, r.Y + S(13), bs, bs})
	fw := min(S(190), r.W/3)
	p.filter.SetBounds(Rect{r.X + r.W - S(10) - fw, navY, fw, bs})
	p.path.SetBounds(Rect{x + S(6), navY, r.X + r.W - S(10) - fw - S(8) - x - S(6), bs})
	ly := navY + bs + S(10)
	p.list.SetBounds(Rect{r.X + 1, ly, r.W - 2, r.Y + r.H - ly - S(8)})
	p.updateButtons()
}

func (p *Pane) updateButtons() {
	p.btnBack.Disabled = len(p.hist) < 2
	p.btnFwd.Disabled = len(p.fut) == 0
	_, hasUp := p.parent()
	p.btnUp.Disabled = !hasUp
	conn := !p.isRemote || p.m.connected()
	p.btnRefresh.Disabled = !conn
	p.path.SetDisabled(!conn)
	p.filter.SetDisabled(!conn)
	if !conn {
		p.btnBack.Disabled, p.btnFwd.Disabled, p.btnUp.Disabled = true, true, true
	}
}

func (p *Pane) title() (string, string) {
	if p.isRemote {
		if !p.m.connected() {
			return "Remote — Not connected", "Connect with Quick Connect or the Site Manager"
		}
		sub := p.m.sess.url(p.dir)
		if !p.loading {
			sub += fmt.Sprintf(" • %d items", p.count())
		}
		return "Remote — " + p.dir, sub
	}
	if p.dir == "" {
		return "Local — This PC", "Drives"
	}
	name := filepath.Base(p.dir)
	if len(p.dir) <= 3 {
		name = p.dir
	}
	sub := p.dir
	if !p.loading {
		sub += fmt.Sprintf(" • %d items", p.count())
	}
	return "Local — " + name, sub
}

func (p *Pane) count() int {
	n := 0
	for _, r := range p.list.AllRows() {
		if !r.IsParent {
			n++
		}
	}
	return n
}

func (p *Pane) paint(c *Canvas) {
	pl := c.P
	S := p.w.S
	r := p.rect
	c.FillRound(r, c.S(10), pl.Surface)
	border := pl.Border
	if p.m.active == p {
		border = pl.BorderStr
	}
	c.StrokeRound(r, c.S(10), border, 1)
	if p.list.DropHighlight {
		c.StrokeRound(r, c.S(10), pl.Accent, c.S(2))
	}
	// header icon
	ic := Rect{r.X + S(14), r.Y + S(13), S(32), S(32)}
	c.FillRound(ic, c.S(8), pl.AccentSoft)
	g := icPC
	if p.isRemote {
		g = icCloud
	}
	c.Glyph(g, p.w.F.IconBig, pl.AccentInk, ic)
	t, sub := p.title()
	tx := ic.X + ic.W + S(12)
	tw := p.btnRefresh.R.X - tx - S(8)
	c.Text(t, p.w.F.Title, pl.TitleText, Rect{tx, r.Y + S(8), tw, S(24)}, AlignLeft)
	c.TextPath(sub, p.w.F.Small, pl.Text2, Rect{tx, r.Y + S(31), tw, S(18)})
	c.FillRect(Rect{r.X + 1, p.list.R.Y - 1, r.W - 2, 1}, pl.Border)
}

func (p *Pane) displayPath() string {
	if !p.isRemote && p.dir == "" {
		return "This PC"
	}
	return p.dir
}

func (p *Pane) parent() (string, bool) {
	if p.isRemote {
		if p.dir == "" || p.dir == "/" {
			return "", false
		}
		return path.Dir(p.dir), true
	}
	return parentLocal(p.dir)
}

func (p *Pane) showDisconnected() {
	p.dir = ""
	p.hist, p.fut = nil, nil
	p.list.SetRows(nil)
	p.list.EmptyText = "Not connected"
	p.list.EmptySub = "Enter a host above and press Connect, or open the Site Manager (Ctrl+S)."
	p.path.SetText("")
	p.updateButtons()
	p.w.Invalidate()
}

// navigate loads dir; addHist records it in back/forward history.
func (p *Pane) navigate(dir string, addHist bool, mode int) {
	fromSync := mode == navSync
	if p.isRemote && !p.m.connected() {
		return
	}
	p.gen++
	gen := p.gen
	p.loading = true
	p.list.Loading = true
	p.w.Invalidate()
	done := func(rows []FileRow, entries map[string]remote.Entry, newDir string, err error) {
		if gen != p.gen {
			return
		}
		p.loading = false
		p.list.Loading = false
		if err != nil {
			p.updateButtons()
			p.w.Invalidate()
			msg := friendlyErr(err)
			if fromSync {
				p.m.setStatus(stWarn, "Synchronized browsing: %s does not exist on the %s side", newDir, map[bool]string{true: "remote", false: "local"}[p.isRemote])
				p.m.log.Add(core.LogWarning, "Synchronized browsing: cannot open %s (%s)", newDir, msg)
				return
			}
			p.m.log.Add(core.LogError, "Cannot open %s: %s", newDir, msg)
			p.m.setStatus(stError, "Cannot open %s: %s", newDir, msg)
			if p.dir == newDir || len(p.list.AllRows()) == 0 && p.dir == "" {
				p.list.EmptyText = "Cannot open this folder"
				p.list.EmptySub = msg
				p.list.SetRows(nil)
			}
			p.path.SetText(p.displayPath())
			return
		}
		changed := newDir != p.dir
		p.dir = newDir
		if entries != nil {
			p.entries = entries
		}
		p.list.EmptyText, p.list.EmptySub = "", ""
		if changed {
			p.list.scroll = 0
			p.list.ClearSelection()
		}
		p.list.SetRows(rows)
		if changed && p.list.cursor < 0 && len(rows) > 0 {
			p.list.cursor = 0
		}
		if p.pendingSelect != "" {
			p.list.SelectNames(p.pendingSelect)
			p.pendingSelect = ""
		}
		p.path.SetText(p.displayPath())
		if addHist && (len(p.hist) == 0 || p.hist[len(p.hist)-1] != newDir) {
			p.hist = append(p.hist, newDir)
			if len(p.hist) > 100 {
				p.hist = p.hist[1:]
			}
			p.fut = nil
		}
		p.updateButtons()
		if !p.isRemote && newDir != "" {
			p.m.S.LastLocalDir = newDir
		}
		if p.isRemote && p.m.pendingSync {
			p.m.pendingSync = false
			p.m.setSync(true)
		}
		if p.m.syncOn {
			if changed && mode == navUser {
				p.m.syncFollow(p)
			}
			p.m.updateBadges()
		}
		p.w.Invalidate()
	}
	if p.isRemote {
		target := dir
		if target == "" {
			target = "/"
		}
		showHidden := p.m.S.ShowHidden
		p.m.sess.Do(func(fs remote.FS) error {
			list, err := fs.List(target)
			if err != nil {
				p.m.app.Post(func() { done(nil, nil, target, err) })
				return err
			}
			rows, entries := remoteRows(list, target, showHidden)
			p.m.app.Post(func() { done(rows, entries, target, nil) })
			return nil
		}, nil)
		return
	}
	showHidden := p.m.S.ShowHidden
	go func() {
		rows, err := listLocal(dir, showHidden)
		p.m.app.Post(func() { done(rows, nil, dir, err) })
	}()
}

func remoteRows(list []remote.Entry, dir string, showHidden bool) ([]FileRow, map[string]remote.Entry) {
	rows := make([]FileRow, 0, len(list)+1)
	entries := map[string]remote.Entry{}
	if dir != "/" {
		rows = append(rows, FileRow{Name: "..", IsParent: true, IsDir: true})
	}
	for _, e := range list {
		hidden := strings.HasPrefix(e.Name, ".")
		if hidden && !showHidden {
			continue
		}
		perm := e.PermStr
		rows = append(rows, FileRow{Name: e.Name, Size: e.Size, ModTime: e.ModTime, IsDir: e.IsDir, IsLink: e.IsLink, Perm: perm, Mode: e.Perm, HasMode: e.HasPerm, Hidden: hidden})
		entries[e.Name] = e
	}
	return rows, entries
}

func (p *Pane) refresh() {
	if p.isRemote && !p.m.connected() {
		return
	}
	p.navigate(p.dir, false, navRefresh)
}

func (p *Pane) up() {
	if par, ok := p.parent(); ok {
		// select the folder we came from once loaded
		if p.isRemote {
			p.pendingSelect = path.Base(p.dir)
		} else if p.dir != "" {
			p.pendingSelect = filepath.Base(p.dir)
			if len(p.dir) <= 3 {
				p.pendingSelect = p.dir
			}
		}
		p.navigate(par, true, navUser)
	}
}

func (p *Pane) back() {
	if len(p.hist) < 2 {
		return
	}
	cur := p.hist[len(p.hist)-1]
	p.hist = p.hist[:len(p.hist)-1]
	p.fut = append(p.fut, cur)
	p.navigate(p.hist[len(p.hist)-1], false, navUser)
}

func (p *Pane) forward() {
	if len(p.fut) == 0 {
		return
	}
	next := p.fut[len(p.fut)-1]
	p.fut = p.fut[:len(p.fut)-1]
	p.hist = append(p.hist, next)
	p.navigate(next, false, navUser)
}

func (p *Pane) childPath(name string) string {
	if p.isRemote {
		return path.Join(p.dir, name)
	}
	if p.dir == "" {
		return name
	}
	return filepath.Join(p.dir, name)
}

func (p *Pane) open(r FileRow) {
	p.m.active = p
	switch {
	case r.IsParent:
		p.up()
	case r.IsDrive:
		p.navigate(r.Name, true, navUser)
	case r.IsDir:
		p.navigate(p.childPath(r.Name), true, navUser)
	default:
		// double-click on a file transfers it to the other side
		if !p.m.connected() {
			if !p.isRemote {
				shellOpen(p.childPath(r.Name))
			}
			return
		}
		if p.isRemote {
			p.m.downloadRows(p.list.Selected(), p.m.local.dir)
		} else {
			p.m.uploadRows(p.list.Selected(), p.m.remote.dir)
		}
	}
}

// ---------------------------------------------------------------- local file system

func listLocal(dir string, showHidden bool) ([]FileRow, error) {
	if dir == "" {
		return drives(), nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	rows := []FileRow{{Name: "..", IsParent: true, IsDir: true}}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		hidden := false
		if d, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
			hidden = d.FileAttributes&(syscall.FILE_ATTRIBUTE_HIDDEN|syscall.FILE_ATTRIBUTE_SYSTEM) != 0
		}
		if strings.HasPrefix(e.Name(), "$") {
			hidden = true
		}
		if hidden && !showHidden {
			continue
		}
		mode := info.Mode()
		isDir := mode.IsDir()
		isLink := mode&os.ModeSymlink != 0
		if isLink {
			if st, err := os.Stat(filepath.Join(dir, e.Name())); err == nil {
				isDir = st.IsDir()
			}
		}
		rows = append(rows, FileRow{Name: e.Name(), Size: info.Size(), ModTime: info.ModTime(), IsDir: isDir, IsLink: isLink,
			Perm: localPerm(mode, isDir), Hidden: hidden})
	}
	return rows, nil
}

func localPerm(m os.FileMode, dir bool) string {
	b := []byte("-rw-rw-rw-")
	if m&0o200 == 0 {
		b[2], b[5], b[8] = '-', '-', '-'
	}
	if dir {
		b[0] = 'd'
		b[3], b[6], b[9] = 'x', 'x', 'x'
	}
	return string(b)
}

func drives() []FileRow {
	mask, _, _ := pGetLogicalDrives.Call()
	var rows []FileRow
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		root := string(rune('A'+i)) + `:\`
		t, _, _ := pGetDriveTypeW.Call(up(root))
		if t == 1 /*NO_ROOT_DIR*/ {
			continue
		}
		rows = append(rows, FileRow{Name: root, IsDir: true, IsDrive: true, Perm: driveLabel(root, t)})
	}
	return rows
}

func driveLabel(root string, t uintptr) string {
	buf := make([]uint16, 64)
	pGetVolumeInfoW.Call(up(root), uintptr(unsafe.Pointer(&buf[0])), 64, 0, 0, 0, 0, 0)
	if l := syscall.UTF16ToString(buf); l != "" {
		return l
	}
	switch t {
	case 2:
		return "Removable"
	case 4:
		return "Network"
	case 5:
		return "CD/DVD"
	}
	return "Local Disk"
}

func parentLocal(dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	clean := filepath.Clean(dir)
	if len(clean) <= 3 && strings.HasSuffix(clean, `:\`) || len(clean) == 2 && clean[1] == ':' {
		return "", true // drive root → This PC
	}
	if strings.HasPrefix(clean, `\\`) {
		// UNC: \\server\share is the top
		parts := strings.Split(strings.TrimPrefix(clean, `\\`), `\`)
		if len(parts) <= 2 {
			return "", true
		}
	}
	par := filepath.Dir(clean)
	if par == clean {
		return "", true
	}
	return par, true
}

func normalizeLocal(input, cur string) string {
	t := strings.Trim(strings.TrimSpace(input), `"`)
	if t == "" || strings.EqualFold(t, "This PC") {
		return ""
	}
	if strings.HasPrefix(t, "~") {
		home, _ := os.UserHomeDir()
		t = home + t[1:]
	}
	t = os.ExpandEnv(strings.ReplaceAll(t, "%", "$"))
	if len(t) == 2 && t[1] == ':' {
		t += `\`
	}
	if !filepath.IsAbs(t) && cur != "" {
		t = filepath.Join(cur, t)
	}
	return filepath.Clean(t)
}

func shellOpen(target string) {
	pShellExecuteW.Call(0, up("open"), up(target), 0, 0, SW_SHOWNORMAL)
}
