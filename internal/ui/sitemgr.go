//go:build windows

package ui

import (
	"fmt"
	"strconv"
	"strings"

	"shoredrop/internal/core"
	"shoredrop/internal/remote"
)

var protoIDs = []string{remote.SFTP, remote.FTPS, remote.FTPSImplicit, remote.FTP}
var protoNames = []string{"SFTP — SSH File Transfer Protocol", "FTPS — FTP over explicit TLS", "FTPS — FTP over implicit TLS (port 990)", "FTP — plain, unencrypted"}
var logonIDs = []string{core.LogonNormal, core.LogonAsk, core.LogonAnonymous, core.LogonKey}
var logonNames = []string{"Normal — save password securely", "Ask for password", "Anonymous", "Key file (SFTP)"}
var modeIDs = []string{"default", "passive", "active"}
var modeNames = []string{"Use global setting", "Passive (recommended)", "Active"}

func indexOf(list []string, v string) int {
	for i, x := range list {
		if x == v {
			return i
		}
	}
	return 0
}

// ---------------------------------------------------------------- tree

type treeRow struct {
	folder string // set for folder rows (full path)
	siteID string
	label  string
	proto  string
	depth  int
}

// SiteTree lists folders and sites.
type SiteTree struct {
	Base
	rows      []treeRow
	sel       string // "f:path" or "s:id"
	hover     int
	sc        vscroll
	collapsed map[string]bool
	OnSelect  func(key string)
	OnOpen    func(key string)
	OnContext func(x, y int)
	focused   bool
}

func (t *SiteTree) rowH() int { return t.W.S(40) }

func (t *SiteTree) build(store *core.SiteStore, filter string) {
	filter = strings.ToLower(strings.TrimSpace(filter))
	var rows []treeRow
	match := func(s core.Site) bool {
		return filter == "" || strings.Contains(strings.ToLower(s.Name), filter) || strings.Contains(strings.ToLower(s.Host), filter) ||
			strings.Contains(strings.ToLower(s.User), filter)
	}
	var walk func(folder string, depth int)
	walk = func(folder string, depth int) {
		for _, f := range store.Folders {
			parent := ""
			if i := strings.LastIndex(f, "/"); i >= 0 {
				parent = f[:i]
			}
			if parent != folder {
				continue
			}
			// skip folders without matches when filtering
			if filter != "" {
				has := false
				for _, s := range store.Sites {
					if (s.Folder == f || strings.HasPrefix(s.Folder, f+"/")) && match(s) {
						has = true
					}
				}
				if !has {
					continue
				}
			}
			name := f
			if i := strings.LastIndex(f, "/"); i >= 0 {
				name = f[i+1:]
			}
			rows = append(rows, treeRow{folder: f, label: name, depth: depth})
			if !t.collapsed[f] || filter != "" {
				walk(f, depth+1)
			}
		}
		for _, s := range store.Sites {
			if s.Folder == folder && match(s) {
				rows = append(rows, treeRow{siteID: s.ID, label: s.Name, proto: s.Protocol, depth: depth})
			}
		}
	}
	walk("", 0)
	t.rows = rows
	t.W.Invalidate()
}

func (r treeRow) key() string {
	if r.siteID != "" {
		return "s:" + r.siteID
	}
	return "f:" + r.folder
}

func (t *SiteTree) rowAt(y int) int {
	i := (y - t.R.Y + t.sc.pos) / t.rowH()
	if y < t.R.Y || i < 0 || i >= len(t.rows) {
		return -1
	}
	return i
}

func (t *SiteTree) CanFocus() bool     { return true }
func (t *SiteTree) SetFocused(f bool)  { t.focused = f; t.W.Invalidate() }
func (t *SiteTree) OnChar(r rune) bool { return false }
func (t *SiteTree) OnKey(e *KeyEvent) bool {
	cur := -1
	for i, r := range t.rows {
		if r.key() == t.sel {
			cur = i
		}
	}
	switch e.VK {
	case VK_UP:
		if cur > 0 {
			t.selectIdx(cur - 1)
		}
	case VK_DOWN:
		if cur < len(t.rows)-1 {
			t.selectIdx(cur + 1)
		}
	case VK_RETURN:
		if cur >= 0 && t.OnOpen != nil {
			t.OnOpen(t.rows[cur].key())
		}
	case VK_LEFT, VK_RIGHT:
		if cur >= 0 && t.rows[cur].folder != "" {
			t.collapsed[t.rows[cur].folder] = e.VK == VK_LEFT
			if t.OnSelect != nil {
				t.OnSelect(t.sel)
			}
		}
	default:
		return false
	}
	return true
}

func (t *SiteTree) selectIdx(i int) {
	t.sel = t.rows[i].key()
	top := i * t.rowH()
	if top < t.sc.pos {
		t.sc.pos = top
	} else if top+t.rowH() > t.sc.pos+t.R.H {
		t.sc.pos = top + t.rowH() - t.R.H
	}
	t.W.Invalidate()
	if t.OnSelect != nil {
		t.OnSelect(t.sel)
	}
}

func (t *SiteTree) OnWheel(delta int, shift bool) bool {
	t.sc.pos -= delta * t.rowH() * 2 / 120
	t.sc.clamp(t.R.H, len(t.rows)*t.rowH())
	t.W.Invalidate()
	return true
}

func (t *SiteTree) OnMouse(e *MouseEvent) bool {
	if t.sc.mouse(t.W, e, t.R, len(t.rows)*t.rowH()) {
		return true
	}
	i := t.rowAt(e.Y)
	switch e.Kind {
	case MouseMove:
		if i != t.hover {
			t.hover = i
			t.W.Invalidate()
		}
	case MouseLeave:
		t.hover = -1
		t.W.Invalidate()
	case MouseDown, MouseRightDown:
		if i >= 0 {
			r := t.rows[i]
			if r.folder != "" && e.Kind == MouseDown && (e.Clicks == 2 || e.X < t.R.X+t.W.S(30)+r.depth*t.W.S(18)) {
				t.collapsed[r.folder] = !t.collapsed[r.folder]
			}
			t.selectIdx(i)
			if e.Clicks == 2 && r.siteID != "" && t.OnOpen != nil {
				t.OnOpen(r.key())
			}
		}
	case MouseRightUp:
		if t.OnContext != nil {
			t.OnContext(e.X, e.Y)
		}
	}
	return true
}

func protoColor(p *Palette, proto string) Color {
	switch proto {
	case remote.SFTP:
		return p.AccentInk
	case remote.FTPS, remote.FTPSImplicit:
		return hex(0x7B68D8)
	}
	return p.Warning
}

func (t *SiteTree) Paint(c *Canvas) {
	p := c.P
	c.PushClip(t.R)
	defer c.PopClip()
	if len(t.rows) == 0 {
		c.TextWrap("No saved sites yet.\nClick “New Site” to add one.", t.W.F.Body, p.Text3, t.R.Inset(t.W.S(12), t.W.S(12)))
		return
	}
	for i, r := range t.rows {
		rr := Rect{t.R.X, t.R.Y + i*t.rowH() - t.sc.pos, t.R.W - t.W.S(8), t.rowH()}
		if rr.Bottom() < t.R.Y || rr.Y > t.R.Bottom() {
			continue
		}
		sel := r.key() == t.sel
		fg := p.Text
		box := Rect{rr.X, rr.Y + t.W.S(2), rr.W, rr.H - t.W.S(4)}
		if sel {
			c.FillRound(box, c.S(8), p.Accent)
			fg = p.AccentText
		} else if i == t.hover {
			c.FillRound(box, c.S(8), p.Hover)
		}
		x := rr.X + t.W.S(10) + r.depth*t.W.S(18)
		if r.folder != "" {
			g := icChevDown
			if t.collapsed[r.folder] {
				g = icChevRight
			}
			c.Glyph(g, t.W.F.IconSmall, ifc(sel, fg, p.Text2), Rect{x, rr.Y, t.W.S(14), rr.H})
			drawFolder(c, x+t.W.S(18), rr.Y+(rr.H-t.W.S(20))/2, t.W.S(20), p, false)
			c.Text(r.label, t.W.F.BodySemi, fg, Rect{x + t.W.S(46), rr.Y, rr.W - x - t.W.S(50), rr.H}, AlignLeft)
			continue
		}
		col := protoColor(p, r.proto)
		ic := Rect{x + t.W.S(2), rr.Y + (rr.H-t.W.S(24))/2, t.W.S(24), t.W.S(24)}
		c.FillRound(ic, c.S(6), ifc(sel, p.AccentText, col))
		c.Glyph(icServer, t.W.F.IconSmall, ifc(sel, p.Accent, p.Surface), ic)
		label := r.label + " — " + remote.ProtocolLabel(r.proto)
		c.Text(label, t.W.F.Body, fg, Rect{x + t.W.S(34), rr.Y, rr.W - x - t.W.S(38), rr.H}, AlignLeft)
	}
	t.sc.paint(c, t.W, t.R, len(t.rows)*t.rowH())
}

// ---------------------------------------------------------------- dialog

type siteMgr struct {
	m     *MainWindow
	d     *Dialog
	store *core.SiteStore

	tree   *SiteTree
	search *TextBox
	btnNew *Button
	btnFld *Button
	tabs   *Tabs

	name, host, port, user, pass, keyFile, remoteDir, localDir, notes *TextBox
	proto, logon, folder, mode                                        *Dropdown
	btnKey, btnLocal                                                  *Button
	btnDelete, btnTest, btnConnect                                    *Button

	curID   string
	loading bool
	labels  []struct {
		text string
		r    Rect
	}
	showPassHint bool
	connectAfter *core.Site
}

func (m *MainWindow) openSiteManager() {
	sm := &siteMgr{m: m, store: m.sites}
	d := m.app.newDialog(m.w, "Site Manager", 980, 660, true)
	sm.d = d
	w := d.Window
	d.NoFooter = true
	sm.tree = &SiteTree{Base: Base{W: w}, hover: -1, collapsed: map[string]bool{}}
	sm.search = NewTextBox(w, "Search connections…")
	sm.search.Icon = icSearch
	sm.search.OnChange = func() { sm.tree.build(sm.store, sm.search.Text()) }
	sm.btnNew = &Button{Base: Base{W: w}, Text: "New Site", Icon: icAdd, Style: BtnPrimary, OnClick: sm.newSite}
	sm.btnFld = &Button{Base: Base{W: w}, Text: "New Folder", Icon: icFolderAdd, Style: BtnGhost, OnClick: sm.newFolder, Font: func() *Font { return w.F.Small }}
	sm.tabs = NewTabs(w, []string{"Details", "Advanced"}, func(int) { sm.layout(d.BodyRect()) })
	sm.name = NewTextBox(w, "My Web Server")
	sm.host = NewTextBox(w, "files.example.com")
	sm.port = NewTextBox(w, "")
	sm.user = NewTextBox(w, "username")
	sm.pass = NewPasswordBox(w, "password")
	sm.keyFile = NewTextBox(w, `C:\Users\you\.ssh\id_ed25519`)
	sm.remoteDir = NewTextBox(w, "/public_html (optional)")
	sm.localDir = NewTextBox(w, `C:\Users\you\Projects\site (optional)`)
	sm.notes = NewMultiLine(w)
	sm.proto = NewDropdown(w, protoNames, 0, func(i int) {
		old := sm.cur()
		if old != nil {
			oldDef := remote.DefaultPort(old.Protocol)
			if p, _ := strconv.Atoi(sm.port.Text()); p == oldDef || p == 0 {
				sm.port.SetText(strconv.Itoa(remote.DefaultPort(protoIDs[i])))
			}
		}
		sm.apply()
		sm.layout(d.BodyRect())
	})
	sm.logon = NewDropdown(w, logonNames, 0, func(int) { sm.apply(); sm.layout(d.BodyRect()) })
	sm.mode = NewDropdown(w, modeNames, 0, func(int) { sm.apply() })
	sm.folder = NewDropdown(w, []string{"(top level)"}, 0, func(int) { sm.apply(); sm.tree.build(sm.store, sm.search.Text()) })
	sm.btnKey = &Button{Base: Base{W: w}, Text: "Choose…", Style: BtnSecondary, OnClick: func() {
		if f, ok := pickFile(w, "Choose a private key file"); ok {
			sm.keyFile.SetText(f)
			sm.apply()
		}
	}}
	sm.btnLocal = &Button{Base: Base{W: w}, Text: "Browse…", Style: BtnSecondary, OnClick: func() {
		if f, ok := pickFolder(w, "Default local folder", sm.localDir.Text()); ok {
			sm.localDir.SetText(f)
			sm.apply()
		}
	}}
	sm.btnDelete = &Button{Base: Base{W: w}, Text: "Delete", Icon: icDelete, Style: BtnGhost, OnClick: sm.deleteSelected}
	sm.btnTest = &Button{Base: Base{W: w}, Text: "Test Connection", Style: BtnSecondary, OnClick: sm.test}
	sm.btnConnect = &Button{Base: Base{W: w}, Text: "Connect", Style: BtnPrimary, OnClick: sm.connect}
	for _, tb := range []*TextBox{sm.name, sm.host, sm.port, sm.user, sm.keyFile, sm.remoteDir, sm.localDir, sm.notes} {
		tb.OnChange = func() { sm.apply() }
	}
	sm.pass.OnChange = func() { sm.apply() }
	sm.name.OnChange = func() { sm.apply(); sm.tree.build(sm.store, sm.search.Text()) }
	for _, tb := range []*TextBox{sm.name, sm.host, sm.port, sm.user, sm.pass, sm.keyFile, sm.remoteDir, sm.localDir} {
		tb.OnEnter = sm.connect
	}
	w.Add(sm.search, sm.btnNew, sm.btnFld, sm.tree, sm.tabs,
		sm.name, sm.proto, sm.host, sm.port, sm.logon, sm.user, sm.pass, sm.keyFile, sm.btnKey, sm.folder,
		sm.remoteDir, sm.localDir, sm.btnLocal, sm.mode, sm.notes,
		sm.btnDelete, sm.btnTest, sm.btnConnect)
	sm.tree.OnSelect = func(key string) {
		sm.tree.build(sm.store, sm.search.Text())
		if strings.HasPrefix(key, "s:") {
			sm.load(key[2:])
		} else {
			sm.load("")
		}
	}
	sm.tree.OnOpen = func(key string) {
		if strings.HasPrefix(key, "s:") {
			sm.connect()
		}
	}
	sm.tree.OnContext = sm.context
	d.LayoutBody = sm.layout
	d.PaintBody = sm.paint
	d.OnClose = func() bool {
		sm.save()
		return true
	}
	sm.tree.build(sm.store, "")
	if len(m.sites.Sites) > 0 {
		sm.tree.sel = "s:" + m.sites.Sites[0].ID
		if m.sess != nil && m.sess.site != nil {
			sm.tree.sel = "s:" + m.sess.site.ID
		}
		sm.load(strings.TrimPrefix(sm.tree.sel, "s:"))
	} else {
		sm.load("")
	}
	d.focus = sm.tree
	d.Run()
	if sm.connectAfter != nil {
		m.connectSite(sm.connectAfter)
	}
}

func (sm *siteMgr) cur() *core.Site {
	if sm.curID == "" {
		return nil
	}
	return sm.store.Find(sm.curID)
}

func (sm *siteMgr) folderOptions() []string {
	return append([]string{"(top level)"}, sm.store.Folders...)
}

func (sm *siteMgr) load(id string) {
	sm.loading = true
	defer func() { sm.loading = false }()
	sm.curID = id
	s := sm.cur()
	sm.folder.Options = sm.folderOptions()
	fields := []*TextBox{sm.name, sm.host, sm.port, sm.user, sm.pass, sm.keyFile, sm.remoteDir, sm.localDir, sm.notes}
	if s == nil {
		for _, f := range fields {
			f.SetText("")
		}
		sm.layout(sm.d.BodyRect())
		return
	}
	sm.name.SetText(s.Name)
	sm.host.SetText(s.Host)
	sm.port.SetText(strconv.Itoa(s.Port))
	sm.user.SetText(s.User)
	pw, _ := core.Credentials.Get(s.CredentialTarget())
	sm.pass.SetText(pw)
	sm.keyFile.SetText(s.KeyFile)
	sm.remoteDir.SetText(s.RemoteDir)
	sm.localDir.SetText(s.LocalDir)
	sm.notes.SetText(strings.ReplaceAll(s.Notes, "\n", "\r\n"))
	sm.proto.Selected = indexOf(protoIDs, s.Protocol)
	sm.logon.Selected = indexOf(logonIDs, s.Logon)
	sm.mode.Selected = indexOf(modeIDs, s.TransferMode)
	sm.folder.Selected = 0
	for i, f := range sm.store.Folders {
		if f == s.Folder {
			sm.folder.Selected = i + 1
		}
	}
	sm.layout(sm.d.BodyRect())
}

// apply copies the form into the selected site (in memory).
func (sm *siteMgr) apply() {
	if sm.loading {
		return
	}
	s := sm.cur()
	if s == nil {
		return
	}
	s.Name = strings.TrimSpace(sm.name.Text())
	if s.Name == "" {
		s.Name = "Untitled site"
	}
	s.Protocol = protoIDs[sm.proto.Selected]
	s.Host = strings.TrimSpace(sm.host.Text())
	s.Port, _ = strconv.Atoi(strings.TrimSpace(sm.port.Text()))
	if s.Port <= 0 || s.Port > 65535 {
		s.Port = remote.DefaultPort(s.Protocol)
	}
	s.Logon = logonIDs[sm.logon.Selected]
	s.User = strings.TrimSpace(sm.user.Text())
	s.KeyFile = strings.TrimSpace(sm.keyFile.Text())
	s.RemoteDir = strings.TrimSpace(sm.remoteDir.Text())
	s.LocalDir = strings.TrimSpace(sm.localDir.Text())
	s.TransferMode = modeIDs[sm.mode.Selected]
	s.Notes = strings.ReplaceAll(sm.notes.Text(), "\r\n", "\n")
	if sm.folder.Selected > 0 && sm.folder.Selected <= len(sm.store.Folders) {
		s.Folder = sm.store.Folders[sm.folder.Selected-1]
	} else {
		s.Folder = ""
	}
	sm.tree.build(sm.store, sm.search.Text())
	sm.d.Invalidate()
}

// save writes sites.json and stores the current password in the credential store.
func (sm *siteMgr) save() {
	sm.apply()
	if s := sm.cur(); s != nil {
		pw := sm.pass.Text()
		if s.Logon == core.LogonNormal && pw != "" {
			if err := core.Credentials.Set(s.CredentialTarget(), s.User, pw); err != nil {
				sm.m.app.Alert(sm.d.Window, MsgError, "Could not save the password", "Windows Credential Manager refused the password: "+err.Error())
			}
		} else {
			core.Credentials.Delete(s.CredentialTarget())
		}
	}
	if err := sm.store.Save(); err != nil {
		sm.m.log.Add(core.LogError, "Could not save sites: %v", err)
	}
}

func (sm *siteMgr) newSite() {
	sm.save()
	folder := ""
	if strings.HasPrefix(sm.tree.sel, "f:") {
		folder = sm.tree.sel[2:]
	} else if s := sm.cur(); s != nil {
		folder = s.Folder
	}
	proto := sm.m.S.DefaultProtocol
	s := core.Site{ID: core.NewID(), Name: "New site", Folder: folder, Protocol: proto, Port: remote.DefaultPort(proto), Logon: core.LogonNormal, TransferMode: "default"}
	sm.store.Upsert(s)
	sm.tree.sel = "s:" + s.ID
	sm.tree.build(sm.store, "")
	sm.search.SetText("")
	sm.load(s.ID)
	sm.tabs.Selected = 0
	sm.layout(sm.d.BodyRect())
	sm.d.SetFocusWidget(sm.name)
	sm.name.SelectAll()
}

func (sm *siteMgr) newFolder() {
	parent := ""
	if strings.HasPrefix(sm.tree.sel, "f:") {
		parent = sm.tree.sel[2:] + "/"
	}
	name, ok := sm.m.app.Prompt(sm.d.Window, "New folder", "Folder name (use / for sub-folders):", "", false)
	if !ok || strings.TrimSpace(name) == "" {
		return
	}
	f := core.CleanFolder(parent + name)
	sm.store.AddFolder(f)
	sm.store.Save()
	sm.tree.sel = "f:" + f
	sm.tree.build(sm.store, sm.search.Text())
	sm.load("")
}

func (sm *siteMgr) deleteSelected() {
	key := sm.tree.sel
	switch {
	case strings.HasPrefix(key, "s:"):
		s := sm.store.Find(key[2:])
		if s == nil {
			return
		}
		if !sm.m.app.Confirm(sm.d.Window, "Delete “"+s.Name+"”?", "The site and its saved password will be removed.", "Delete", true) {
			return
		}
		sm.store.Delete(s.ID)
	case strings.HasPrefix(key, "f:"):
		f := key[2:]
		if !sm.m.app.Confirm(sm.d.Window, "Delete folder “"+f+"”?", "The folder and every site inside it (with saved passwords) will be removed.", "Delete", true) {
			return
		}
		sm.store.DeleteFolder(f)
	default:
		return
	}
	sm.store.Save()
	sm.curID = ""
	sm.tree.sel = ""
	sm.tree.build(sm.store, sm.search.Text())
	if len(sm.tree.rows) > 0 {
		sm.tree.selectIdx(0)
	} else {
		sm.load("")
	}
}

func (sm *siteMgr) context(x, y int) {
	key := sm.tree.sel
	items := []MenuItem{
		{Label: "New Site", Icon: icAdd, Action: sm.newSite},
		{Label: "New Folder…", Icon: icFolderAdd, Action: sm.newFolder},
	}
	if strings.HasPrefix(key, "s:") {
		items = append(items, Sep,
			MenuItem{Label: "Connect", Icon: icPlay, Action: sm.connect},
			MenuItem{Label: "Duplicate", Icon: icCopy, Action: func() {
				sm.save()
				if s := sm.cur(); s != nil {
					c := *s
					c.ID = core.NewID()
					c.Name += " (copy)"
					if pw, err := core.Credentials.Get(s.CredentialTarget()); err == nil {
						core.Credentials.Set(c.CredentialTarget(), c.User, pw)
					}
					sm.store.Upsert(c)
					sm.store.Save()
					sm.tree.sel = "s:" + c.ID
					sm.tree.build(sm.store, sm.search.Text())
					sm.load(c.ID)
				}
			}},
			MenuItem{Label: "Delete", Icon: icDelete, Danger: true, Action: sm.deleteSelected})
	} else if strings.HasPrefix(key, "f:") {
		f := key[2:]
		items = append(items, Sep,
			MenuItem{Label: "Rename Folder…", Icon: icRename, Action: func() {
				base := f
				if i := strings.LastIndex(f, "/"); i >= 0 {
					base = f[i+1:]
				}
				n, ok := sm.m.app.Prompt(sm.d.Window, "Rename folder", "New name:", base, false)
				if ok && strings.TrimSpace(n) != "" {
					sm.store.RenameFolder(f, n)
					sm.store.Save()
					sm.tree.build(sm.store, sm.search.Text())
					sm.load(sm.curID)
				}
			}},
			MenuItem{Label: "Delete Folder", Icon: icDelete, Danger: true, Action: sm.deleteSelected})
	}
	sx, sy := sm.d.ScreenPos(x, y)
	sm.m.app.ShowMenu(sm.d.Window, items, sx, sy, 0, nil)
}

func (sm *siteMgr) test() {
	sm.save()
	s := sm.cur()
	if s == nil || s.Host == "" {
		sm.m.app.Alert(sm.d.Window, MsgInfo, "Nothing to test", "Enter a host for this site first.")
		return
	}
	m := sm.m
	p := remote.Params{Protocol: s.Protocol, Host: s.Host, Port: s.Port, User: s.User, Timeout: m.S.Timeout(),
		Passive: s.TransferMode == "passive" || s.TransferMode != "active" && m.S.FTPMode != "active",
		HostKey: m.checkHostKey, CertCheck: m.checkCert}
	switch s.Logon {
	case core.LogonAnonymous:
		p.User, p.Password = "anonymous", "anonymous@"
	case core.LogonKey:
		p.KeyFile = s.KeyFile
	case core.LogonAsk:
		pw, ok := m.app.Prompt(sm.d.Window, "Password", "Password for "+s.User+"@"+s.Host+":", "", true)
		if !ok {
			return
		}
		p.Password = pw
	default:
		p.Password = sm.pass.Text()
	}
	sm.btnTest.Disabled = true
	sm.btnTest.Text = "Testing…"
	sm.d.Invalidate()
	go func() {
		fs, err := remote.Connect(p)
		var desc, home string
		if err == nil {
			desc, home = fs.Description(), fs.Home()
			_, lerr := fs.List(home)
			fs.Close()
			err = lerr
		}
		m.app.Post(func() {
			sm.btnTest.Disabled = false
			sm.btnTest.Text = "Test Connection"
			sm.d.Invalidate()
			if err != nil {
				m.app.Alert(sm.d.Window, MsgError, "Connection test failed", friendlyErr(err))
				return
			}
			m.app.Alert(sm.d.Window, MsgInfo, "Connection works", fmt.Sprintf("Logged in to %s successfully using %s.\nHome folder: %s", s.Host, desc, home))
		})
	}()
}

func (sm *siteMgr) connect() {
	sm.save()
	s := sm.cur()
	if s == nil {
		return
	}
	if s.Host == "" {
		sm.m.app.Alert(sm.d.Window, MsgInfo, "Enter a host", "This site has no host yet.")
		return
	}
	c := *s
	sm.connectAfter = &c
	sm.d.Close()
}

func (sm *siteMgr) layout(body Rect) {
	d := sm.d
	S := d.S
	left := Rect{0, body.Y, S(310), body.H}
	pad := S(20)
	sm.search.SetBounds(Rect{pad, left.Y + S(18), left.W - 2*pad, S(36)})
	sm.btnNew.SetBounds(Rect{pad, left.Y + S(64), left.W - 2*pad, S(40)})
	sm.btnFld.SetBounds(Rect{left.W - pad - S(120), left.Y + S(114), S(120), S(28)})
	sm.tree.SetBounds(Rect{S(10), left.Y + S(148), left.W - S(20), left.H - S(148) - S(44)})

	x0 := left.W + S(28)
	rw := d.w - x0 - S(28)
	sm.tabs.SetBounds(Rect{x0 - S(8), body.Y + S(62), S(260), S(38)})
	sm.labels = sm.labels[:0]
	s := sm.cur()
	has := s != nil
	details := sm.tabs.Selected == 0
	all := []Widget{sm.name, sm.proto, sm.host, sm.port, sm.logon, sm.user, sm.pass, sm.keyFile, sm.btnKey, sm.folder, sm.remoteDir, sm.localDir, sm.btnLocal, sm.mode, sm.notes, sm.tabs, sm.btnTest, sm.btnConnect, sm.btnDelete}
	for _, x := range all {
		vis := has
		switch v := x.(type) {
		case *TextBox:
			v.Show(vis)
		case *Button:
			v.Show(vis)
		case *Dropdown:
			v.Show(vis)
		case *Tabs:
			v.Show(vis)
		}
	}
	sm.btnDelete.Show(sm.tree.sel != "")
	bottomY := body.Y + body.H - S(60)
	sm.btnConnect.SetBounds(Rect{d.w - S(28) - S(120), bottomY, S(120), S(40)})
	sm.btnTest.SetBounds(Rect{d.w - S(28) - S(120) - S(12) - S(160), bottomY, S(160), S(40)})
	sm.btnDelete.SetBounds(Rect{x0, bottomY, S(110), S(40)})
	if !has {
		return
	}
	y := body.Y + S(116)
	fh := S(36)
	lab := func(t string, x, yy int) {
		sm.labels = append(sm.labels, struct {
			text string
			r    Rect
		}{t, Rect{x, yy, S(300), S(20)}})
	}
	row := func() int { v := y; y += S(66); return v }
	keyMode := sm.logon.Selected == 3
	anon := sm.logon.Selected == 2
	ask := sm.logon.Selected == 1
	showD := func(ws ...Widget) {
		for _, w := range ws {
			switch v := w.(type) {
			case *TextBox:
				v.Show(details)
			case *Button:
				v.Show(details)
			case *Dropdown:
				v.Show(details)
			}
		}
	}
	showA := func(ws ...Widget) {
		for _, w := range ws {
			switch v := w.(type) {
			case *TextBox:
				v.Show(!details)
			case *Button:
				v.Show(!details)
			case *Dropdown:
				v.Show(!details)
			}
		}
	}
	showD(sm.name, sm.proto, sm.host, sm.port, sm.logon, sm.user, sm.pass, sm.keyFile, sm.btnKey)
	showA(sm.folder, sm.remoteDir, sm.localDir, sm.btnLocal, sm.mode, sm.notes)
	if details {
		yy := row()
		half := (rw - S(16)) / 2
		lab("Site Name", x0, yy)
		sm.name.SetBounds(Rect{x0, yy + S(22), half, fh})
		lab("Protocol", x0+half+S(16), yy)
		sm.proto.SetBounds(Rect{x0 + half + S(16), yy + S(22), half, fh})
		yy = row()
		lab("Host", x0, yy)
		sm.host.SetBounds(Rect{x0, yy + S(22), rw - S(126), fh})
		lab("Port", x0+rw-S(110), yy)
		sm.port.SetBounds(Rect{x0 + rw - S(110), yy + S(22), S(110), fh})
		yy = row()
		lab("Logon Type", x0, yy)
		sm.logon.SetBounds(Rect{x0, yy + S(22), half, fh})
		if !anon {
			lab("Username", x0+half+S(16), yy)
		}
		sm.user.SetBounds(Rect{x0 + half + S(16), yy + S(22), half, fh})
		sm.user.Show(!anon)
		yy = row()
		sm.pass.Show(!keyMode && !anon && !ask)
		sm.keyFile.Show(keyMode)
		sm.btnKey.Show(keyMode)
		switch {
		case keyMode:
			lab("Key File (OpenSSH, PEM or PuTTY .ppk)", x0, yy)
			sm.keyFile.SetBounds(Rect{x0, yy + S(22), rw - S(122), fh})
			sm.btnKey.SetBounds(Rect{x0 + rw - S(110), yy + S(22), S(110), fh})
		case anon || ask:
		default:
			lab("Password", x0, yy)
			sm.pass.SetBounds(Rect{x0, yy + S(22), rw, fh})
		}
		sm.showPassHint = !keyMode && !anon
	} else {
		yy := row()
		lab("Folder in Site Manager", x0, yy)
		sm.folder.SetBounds(Rect{x0, yy + S(22), rw, fh})
		yy = row()
		lab("Default remote folder", x0, yy)
		sm.remoteDir.SetBounds(Rect{x0, yy + S(22), rw, fh})
		yy = row()
		lab("Default local folder", x0, yy)
		sm.localDir.SetBounds(Rect{x0, yy + S(22), rw - S(122), fh})
		sm.btnLocal.SetBounds(Rect{x0 + rw - S(110), yy + S(22), S(110), fh})
		yy = row()
		half := (rw - S(16)) / 2
		lab("FTP transfer mode", x0, yy)
		sm.mode.SetBounds(Rect{x0, yy + S(22), half, fh})
		lab("Notes", x0+half+S(16), yy)
		sm.notes.SetBounds(Rect{x0 + half + S(16), yy + S(22), half, bottomY - yy - S(36)})
	}
	d.Invalidate()
}

func (sm *siteMgr) paint(c *Canvas, body Rect) {
	p := c.P
	d := sm.d
	S := d.S
	left := Rect{0, body.Y, S(310), body.H}
	c.FillRect(left, p.Window)
	c.FillRect(Rect{left.W - 1, body.Y, 1, body.H}, p.Border)
	c.Text("SAVED CONNECTIONS", d.F.Tiny, p.Text2, Rect{S(22), left.Y + S(114), S(200), S(28)}, AlignLeft)
	c.Text(fmt.Sprintf("ShoreDrop v%s • %d connections", core.AppVersion, len(sm.store.Sites)), d.F.Small, p.Text2,
		Rect{S(22), left.Y + left.H - S(40), left.W - S(30), S(30)}, AlignLeft)
	x0 := left.W + S(28)
	c.Text("Connection Details", d.F.H1, p.TitleText, Rect{x0, body.Y + S(16), S(400), S(40)}, AlignLeft)
	if sm.cur() == nil {
		msg := "Select a site on the left, or click New Site."
		if strings.HasPrefix(sm.tree.sel, "f:") {
			msg = "Folder “" + sm.tree.sel[2:] + "” — select a site, or click New Site to add one here."
		}
		c.TextWrap(msg, d.F.Body, p.Text2, Rect{x0, body.Y + S(80), d.w - x0 - S(30), S(60)})
		return
	}
	c.FillRect(Rect{x0, body.Y + S(99), d.w - x0 - S(28), 1}, p.Border)
	for _, l := range sm.labels {
		c.Text(l.text, d.F.Label, p.Text, l.r, AlignLeft)
	}
	s := sm.cur()
	if sm.tabs.Selected == 0 {
		var hint string
		switch s.Logon {
		case core.LogonNormal:
			hint = "The password is stored in " + core.CredentialStoreName + ", never in a settings file."
		case core.LogonAsk:
			hint = "You'll be asked for the password each time you connect."
		case core.LogonAnonymous:
			hint = "Logs in as “anonymous” (public FTP servers)."
		case core.LogonKey:
			hint = "If the key has a passphrase you'll be asked for it when connecting."
		}
		if s.Protocol == remote.FTP {
			hint = "⚠ Plain FTP is not encrypted — use FTPS or SFTP if the server supports it."
		}
		c.TextWrap(hint, d.F.Small, ifc(s.Protocol == remote.FTP, p.Warning, p.Text2), Rect{x0, body.Y + S(116) + 4*S(66) - S(4), d.w - x0 - S(28), S(40)})
	}
}
