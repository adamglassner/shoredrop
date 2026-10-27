//go:build windows || darwin

package ui

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"shoredrop/internal/core"
	"shoredrop/internal/remote"
)

// Status kinds for the status bar.
const (
	stIdle = iota
	stBusy
	stOK
	stWarn
	stError
)

type menuDef struct {
	title string
	key   int // mnemonic VK
	items func() []MenuItem
	rect  Rect
}

// MainWindow is the ShoreDrop main window.
type MainWindow struct {
	app   *App
	w     *Window
	S     *core.Settings
	sites *core.SiteStore
	trust *core.TrustStore
	log   *core.Log
	q     *core.Queue

	btnMin, btnMax, btnClose, btnTheme, btnGear *Button

	menus     []*menuDef
	menuHover int
	menuOpen  int
	menuBar   *menuBarWidget

	qcHost, qcUser, qcPass, qcPort *TextBox
	qcProto                        *Button
	qcConnect                      *Button
	qcSites                        *Button
	qcMode                         bool // show quick-connect fields even while connected

	sessPill   *Button
	syncToggle *Toggle
	btnDisc    *Button
	btnNewConn *Button
	protocol   string

	local, remote *Pane
	active        *Pane

	tabs      *Tabs
	queueView *QueueView
	logView   *LogView
	qBtns     []*Button

	splitV, splitH *splitter

	sess       *Session
	connecting bool
	connGen    int

	syncOn     bool
	syncLocal  string
	syncRemote string

	status     string
	statusKind int

	drag *dragState

	// layout rects
	rHeader, rMenu, rQC, rPanes, rBottom, rStatus Rect

	seen          map[int64]core.Status
	refreshLocal  bool
	refreshRemote bool
	refreshTimer  uintptr
	keepTimer     uintptr
	sessionTrust  map[string]string

	lastClosedMenu int
	pendingSync    bool
	lastParams     *connRequest
}

type dragState struct {
	from   *Pane
	rows   []FileRow
	x, y   int
	target *Pane
	row    int
}

// splitter is a draggable divider.
type splitter struct {
	Base
	vertical bool
	onDrag   func(pos int)
	drag     bool
	off      int
}

func (s *splitter) Paint(c *Canvas) {
	if s.Hovered || s.drag {
		r := s.R
		if s.vertical {
			c.FillRound(Rect{r.X + r.W/2 - s.W.S(2), r.Y + r.H/2 - s.W.S(20), s.W.S(4), s.W.S(40)}, c.S(2), c.P.Accent.WithAlpha(160))
		} else {
			c.FillRound(Rect{r.X + r.W/2 - s.W.S(20), r.Y + r.H/2 - s.W.S(2), s.W.S(40), s.W.S(4)}, c.S(2), c.P.Accent.WithAlpha(160))
		}
	}
}

func (s *splitter) Cursor(x, y int) int {
	if s.vertical {
		return IDC_SIZEWE
	}
	return IDC_SIZENS
}

func (s *splitter) OnMouse(e *MouseEvent) bool {
	switch e.Kind {
	case MouseDown:
		s.drag = true
		if s.vertical {
			s.off = e.X - s.R.X
		} else {
			s.off = e.Y - s.R.Y
		}
	case MouseMove:
		if s.drag {
			if s.vertical {
				s.onDrag(e.X - s.off)
			} else {
				s.onDrag(e.Y - s.off)
			}
		}
	case MouseUp:
		s.drag = false
		s.W.Invalidate()
	}
	return true
}

// menuBarWidget handles clicks on the menu titles.
type menuBarWidget struct {
	Base
	m *MainWindow
}

func (mb *menuBarWidget) Paint(c *Canvas) {}

func (mb *menuBarWidget) OnMouse(e *MouseEvent) bool {
	m := mb.m
	idx := -1
	for i, d := range m.menus {
		if d.rect.Contains(e.X, e.Y) {
			idx = i
		}
	}
	switch e.Kind {
	case MouseMove:
		if idx != m.menuHover {
			m.menuHover = idx
			m.w.Invalidate()
		}
	case MouseLeave:
		m.menuHover = -1
		m.w.Invalidate()
	case MouseDown:
		if idx >= 0 {
			if idx == m.lastClosedMenu && time.Since(lastMenuClose) < 250*time.Millisecond {
				return true // clicking the open menu's title closes it
			}
			m.openMenu(idx)
		}
	}
	return true
}

func newMainWindow(a *App, s *core.Settings) *MainWindow {
	m := &MainWindow{app: a, S: s, menuHover: -1, menuOpen: -1, seen: map[int64]core.Status{}, sessionTrust: map[string]string{}, lastClosedMenu: -1}
	m.sites = core.LoadSites()
	m.trust = core.LoadTrust()
	m.log = &core.Log{}
	m.q = core.NewQueue(m.log)
	m.q.Max = func() int { return m.S.MaxTransfers }
	m.q.ExistsAction = func() string { return m.S.ExistsAction }
	m.q.ResumeOn = func() bool { return m.S.ResumeTransfers }
	m.protocol = s.LastProtocol
	if m.protocol == "" {
		m.protocol = s.DefaultProtocol
	}

	ws := s.Window
	w := a.newWindow(winOpts{title: "ShoreDrop", w: max(ws.W, 1000), h: max(ws.H, 680)})
	m.w = w
	a.main = w
	w.MinW, w.MinH = 980, 640
	w.acceptFileDrops()

	m.btnMin = &Button{Base: Base{W: w}, Icon: icMin, Style: BtnChrome, OnClick: w.Minimize}
	m.btnMax = &Button{Base: Base{W: w}, Icon: icMax, Style: BtnChrome, OnClick: w.ToggleMaximize}
	m.btnClose = &Button{Base: Base{W: w}, Icon: icChromeX, Style: BtnChromeClose, OnClick: w.Close}
	m.btnTheme = NewIconButton(w, icMoon, m.toggleTheme)
	m.btnGear = NewIconButton(w, icSettings, m.openSettings)
	m.menuBar = &menuBarWidget{Base: Base{W: w}, m: m}
	w.Add(m.btnMin, m.btnMax, m.btnClose, m.btnTheme, m.btnGear, m.menuBar)

	// quick connect
	m.qcHost = NewTextBox(w, "host or IP address")
	m.qcUser = NewTextBox(w, "username")
	m.qcPass = NewPasswordBox(w, "password")
	m.qcPort = NewTextBox(w, "")
	m.qcHost.SetText(s.LastHost)
	m.qcUser.SetText(s.LastUser)
	if s.LastPort > 0 {
		m.qcPort.SetText(strconv.Itoa(s.LastPort))
	} else {
		m.qcPort.SetText(strconv.Itoa(remote.DefaultPort(m.protocol)))
	}
	for _, tb := range []*TextBox{m.qcHost, m.qcUser, m.qcPass, m.qcPort} {
		tb.OnEnter = m.quickConnect
		tb.OnEscape = func() {
			if m.connected() && m.qcMode {
				m.qcMode = false
				m.layout()
				w.SetFocusWidget(m.remote.list)
			}
		}
		w.Add(tb)
	}
	m.qcProto = &Button{Base: Base{W: w}, Text: remote.ProtocolLabel(m.protocol), Icon: icChevDown, Style: BtnPill, OnClick: m.protocolMenu}
	m.qcConnect = &Button{Base: Base{W: w}, Text: "Connect", Icon: icPlay, Style: BtnPrimary, OnClick: m.quickConnect}
	m.qcSites = &Button{Base: Base{W: w}, Text: "Sites", Icon: icServer, Style: BtnSecondary, OnClick: m.openSiteManager}
	w.Add(m.qcProto, m.qcConnect, m.qcSites)

	m.sessPill = &Button{Base: Base{W: w}, Style: BtnSecondary, OnClick: m.sessionMenu}
	m.syncToggle = NewToggle(w, false, func(on bool) { m.setSync(on) })
	m.btnDisc = &Button{Base: Base{W: w}, Text: "Disconnect", Icon: icDisconnect, Style: BtnSecondary, OnClick: m.disconnect}
	m.btnNewConn = &Button{Base: Base{W: w}, Text: "Quick Connect", Icon: icAdd, Style: BtnGhost, OnClick: func() { m.qcMode = true; m.layout(); w.SetFocusWidget(m.qcHost) }}
	w.Add(m.sessPill, m.syncToggle, m.btnDisc, m.btnNewConn)

	m.local = newPane(m, false)
	m.remote = newPane(m, true)
	m.active = m.local

	m.tabs = NewTabs(w, []string{"Transfer Queue", "Transfer Log"}, func(i int) { m.layout() })
	m.queueView = NewQueueView(w, m.q)
	m.queueView.OnContext = m.queueMenu
	m.logView = NewLogView(w, m.log)
	m.logView.OnContext = func(x, y int) {
		sx, sy := w.ScreenPos(x, y)
		a.ShowMenu(w, []MenuItem{
			{Label: "Copy log", Icon: icCopy, Action: func() { setClipboardText(m.logView.Text()) }},
			{Label: "Clear log", Icon: icClear, Action: m.log.Clear},
		}, sx, sy, 0, nil)
	}
	w.Add(m.tabs, m.queueView, m.logView)
	for _, b := range []struct {
		t  string
		ic rune
		fn func()
	}{
		{"Pause all", icPause, m.q.PauseAll},
		{"Resume all", icPlay, m.q.ResumeAll},
		{"Retry failed", icRetry, m.q.RetryFailed},
		{"Clear finished", icClear, m.q.ClearFinished},
	} {
		btn := &Button{Base: Base{W: w}, Text: b.t, Icon: b.ic, Style: BtnGhost, OnClick: b.fn, Font: func() *Font { return w.F.Small }}
		m.qBtns = append(m.qBtns, btn)
		w.Add(btn)
	}

	m.splitV = &splitter{Base: Base{W: w}, vertical: true}
	m.splitV.onDrag = func(x int) {
		if m.rPanes.W > 0 {
			f := float64(x-m.rPanes.X+w.S(6)) / float64(m.rPanes.W)
			if f < 0.2 {
				f = 0.2
			}
			if f > 0.8 {
				f = 0.8
			}
			m.S.Window.PaneSplit = f
			m.layout()
		}
	}
	m.splitH = &splitter{Base: Base{W: w}}
	m.splitH.onDrag = func(y int) {
		h := m.rStatus.Y - y - w.S(12)
		minH, maxH := w.S(140), w.h-w.S(420)
		if h < minH {
			h = minH
		}
		if h > maxH {
			h = maxH
		}
		m.S.Window.QueueHeight = int(float64(h) / w.scale)
		m.layout()
	}
	w.Add(m.splitV, m.splitH)

	m.buildMenus()
	if nativeMenuBar {
		m.setupNativeMenus()
	}
	w.Layout = m.layout
	w.PaintBg = m.paint
	w.Overlay = m.paintOverlay
	w.OnKey = m.onKey
	w.OnClose = m.onClose
	w.OnDrop = m.onDropFiles
	w.OnActivate = func(bool) { w.Invalidate() }

	m.log.OnAdd = func() { a.Post(func() { m.logView.W.Invalidate() }) }
	pendingQ := false
	m.q.OnChange = func() {
		if pendingQ {
			return
		}
		pendingQ = true
		a.Post(func() { pendingQ = false; m.queueChanged() })
	}
	a.ThemeChanged = append(a.ThemeChanged, func() { m.updateThemeIcon(); w.Invalidate() })
	m.updateThemeIcon()
	m.setStatus(stIdle, "Not connected")
	m.log.Add(core.LogInfo, "ShoreDrop %s started", core.AppVersion)

	startDir := s.LastLocalDir
	if startDir == "" {
		startDir = s.DownloadDir
	}
	if st, err := os.Stat(startDir); err != nil || !st.IsDir() {
		startDir, _ = os.UserHomeDir()
	}
	m.local.navigate(startDir, true, navUser)
	m.remote.showDisconnected()
	m.keepTimer = w.SetTimer(30*time.Second, m.keepAlive)
	return m
}

func (m *MainWindow) updateThemeIcon() {
	if m.app.pal.Dark {
		m.btnTheme.Icon = icSun
	} else {
		m.btnTheme.Icon = icMoon
	}
}

func (m *MainWindow) toggleTheme() {
	if m.app.pal.Dark {
		m.app.SetTheme("light")
	} else {
		m.app.SetTheme("dark")
	}
	m.log.Add(core.LogInfo, "Appearance set to %s mode", m.S.Theme)
}

func (m *MainWindow) setStatus(kind int, format string, args ...any) {
	m.statusKind = kind
	if len(args) > 0 {
		m.status = fmt.Sprintf(format, args...)
	} else {
		m.status = format
	}
	m.w.Invalidate()
}

func (m *MainWindow) connected() bool { return m.sess != nil }

// ---------------------------------------------------------------- layout

func (m *MainWindow) layout() {
	w := m.w
	W, H := w.w, w.h
	S := w.S
	m.btnMax.Icon = icMax
	if w.Maximized() {
		m.btnMax.Icon = icRestore
	}
	headerH := S(72)
	m.rHeader = Rect{0, 0, W, headerH}
	w.CaptionH = headerH + S(4)
	cw, ch := S(46), S(34)
	m.btnClose.SetBounds(Rect{W - cw, 0, cw, ch})
	m.btnMax.SetBounds(Rect{W - 2*cw, 0, cw, ch})
	m.btnMin.SetBounds(Rect{W - 3*cw, 0, cw, ch})
	for _, b := range []*Button{m.btnClose, m.btnMax, m.btnMin} {
		b.Show(!nativeChrome)
	}
	m.btnGear.SetBounds(Rect{W - S(16) - S(36), S(38), S(36), S(30)})
	m.btnTheme.SetBounds(Rect{W - S(16) - S(78), S(38), S(36), S(30)})

	m.rMenu = Rect{0, headerH, W, S(34)}
	if nativeMenuBar {
		m.rMenu.H = 0 // the menus are in the macOS menu bar
	}
	m.menuBar.SetBounds(m.rMenu)
	m.menuBar.Show(!nativeMenuBar)
	x := S(14)
	for _, d := range m.menus {
		tw := w.F.Menu.Measure(d.title) + S(24)
		d.rect = Rect{x, m.rMenu.Y + S(3), tw, m.rMenu.H - S(6)}
		x += tw + S(2)
	}

	pad := S(16)
	m.rQC = Rect{pad, m.rMenu.Y + m.rMenu.H + S(8), W - 2*pad, S(58)}
	m.layoutQC()

	m.rStatus = Rect{0, H - S(32), W, S(32)}
	qh := S(m.S.Window.QueueHeight)
	top := m.rQC.Y + m.rQC.H + S(12)
	if maxQ := m.rStatus.Y - top - S(220); qh > maxQ {
		qh = max(S(120), maxQ)
	}
	m.rBottom = Rect{pad, m.rStatus.Y - qh - S(10), W - 2*pad, qh}
	m.rPanes = Rect{pad, top, W - 2*pad, m.rBottom.Y - S(12) - top}
	gap := S(12)
	lw := int(float64(m.rPanes.W-gap) * m.S.Window.PaneSplit)
	m.local.layout(Rect{m.rPanes.X, m.rPanes.Y, lw, m.rPanes.H})
	m.remote.layout(Rect{m.rPanes.X + lw + gap, m.rPanes.Y, m.rPanes.W - lw - gap, m.rPanes.H})
	m.splitV.SetBounds(Rect{m.rPanes.X + lw, m.rPanes.Y, gap, m.rPanes.H})
	m.splitH.SetBounds(Rect{m.rPanes.X, m.rPanes.Y + m.rPanes.H, m.rPanes.W, gap})

	b := m.rBottom
	m.tabs.SetBounds(Rect{b.X + S(10), b.Y + S(4), S(420), S(40)})
	bx := b.X + b.W - S(10)
	for i := len(m.qBtns) - 1; i >= 0; i-- {
		btn := m.qBtns[i]
		bw := w.F.Small.Measure(btn.Text) + S(40)
		bx -= bw
		btn.SetBounds(Rect{bx, b.Y + S(8), bw, S(30)})
		bx -= S(4)
		btn.Show(m.tabs.Selected == 0)
	}
	inner := Rect{b.X + 1, b.Y + S(48), b.W - 2, b.H - S(48) - 1}
	m.queueView.SetBounds(inner)
	m.logView.SetBounds(inner)
	m.queueView.Show(m.tabs.Selected == 0)
	m.logView.Show(m.tabs.Selected == 1)
	w.Invalidate()
}

func (m *MainWindow) layoutQC() {
	w := m.w
	S := w.S
	r := m.rQC
	showQC := !m.connected() || m.qcMode
	for _, tb := range []*TextBox{m.qcHost, m.qcUser, m.qcPass, m.qcPort} {
		tb.Show(showQC)
	}
	m.qcProto.Show(showQC)
	m.qcConnect.Show(showQC)
	m.qcSites.Show(showQC)
	m.sessPill.Show(!showQC)
	m.syncToggle.Show(m.connected() && !showQC)
	m.btnDisc.Show(!showQC)
	m.btnNewConn.Show(!showQC)

	fh := S(36)
	fy := r.Y + (r.H-fh)/2
	if showQC {
		x := r.X + S(14)
		lab := func(s string) int { return w.F.Label.Measure(s) + S(10) }
		right := r.X + r.W - S(12)
		sitesW := m.qcSites.PreferredWidth()
		connW := m.qcConnect.PreferredWidth() + S(6)
		protoW := m.qcProto.PreferredWidth()
		syncW := 0
		portW := S(64)
		fixed := lab("Host") + lab("Username") + lab("Password") + lab("Port") + portW + protoW + connW + sitesW + syncW + S(14+12*7)
		flex := r.W - fixed
		hostW := flex * 38 / 100
		userW := flex * 28 / 100
		passW := flex - hostW - userW
		place := func(label string, tb *TextBox, fw int) {
			x += lab(label)
			tb.SetBounds(Rect{x, fy, fw, fh})
			x += fw + S(12)
		}
		place("Host", m.qcHost, hostW)
		place("Username", m.qcUser, userW)
		place("Password", m.qcPass, passW)
		place("Port", m.qcPort, portW)
		m.qcProto.SetBounds(Rect{x, fy + S(3), protoW, fh - S(6)})
		x += protoW + S(12)
		m.qcConnect.SetBounds(Rect{x, fy, connW, fh})
		x += connW + S(12)
		m.qcSites.SetBounds(Rect{x, fy, sitesW, fh})
		_ = right
		return
	}
	// session strip
	m.sessPill.Text = m.sess.label
	m.sessPill.Icon = icServer
	pw := min(m.sessPill.PreferredWidth()+S(10), r.W/3)
	m.sessPill.SetBounds(Rect{r.X + S(12), fy, pw, fh})
	right := r.X + r.W - S(12)
	dw := m.btnDisc.PreferredWidth()
	m.btnDisc.SetBounds(Rect{right - dw, fy, dw, fh})
	nw := m.btnNewConn.PreferredWidth()
	m.btnNewConn.SetBounds(Rect{right - dw - S(8) - nw, fy, nw, fh})
	m.syncToggle.SetBounds(Rect{right - dw - nw - S(30) - S(44), fy, S(44), fh})
}

// ---------------------------------------------------------------- painting

func (m *MainWindow) paint(c *Canvas) {
	p := c.P
	w := m.w
	S := w.S
	// header
	ls := S(50)
	drawBrand(c, w, w.ChromeL+S(22), (m.rHeader.H-ls)/2+S(2), ls, "v"+core.AppVersion, false)
	// menu bar
	if nativeMenuBar {
		c.FillRect(Rect{0, m.rMenu.Y, w.w, 1}, p.Border)
	} else {
		m.paintMenuBar(c)
	}
	m.paintBody(c)
}

func (m *MainWindow) paintMenuBar(c *Canvas) {
	p := c.P
	w := m.w
	c.FillRect(Rect{0, m.rMenu.Y, w.w, m.rMenu.H}, p.Surface2)
	c.FillRect(Rect{0, m.rMenu.Y, w.w, 1}, p.Border)
	c.FillRect(Rect{0, m.rMenu.Y + m.rMenu.H - 1, w.w, 1}, p.Border)
	for i, d := range m.menus {
		fg := p.Text
		if i == m.menuOpen {
			c.FillRound(d.rect, c.S(6), p.Accent)
			fg = p.AccentText
		} else if i == m.menuHover {
			c.FillRound(d.rect, c.S(6), p.Hover)
		}
		c.Text(d.title, w.F.Menu, fg, d.rect, AlignCenter)
	}
}

func (m *MainWindow) paintBody(c *Canvas) {
	p := c.P
	w := m.w
	S := w.S
	// quick connect / session panel
	r := m.rQC
	c.FillRound(r, c.S(10), p.Surface)
	c.StrokeRound(r, c.S(10), p.Border, 1)
	if m.qcHost.IsVisible() {
		lab := func(tb *TextBox, s string) {
			lw := w.F.Label.Measure(s) + S(10)
			c.Text(s, w.F.Label, p.Text, Rect{tb.R.X - lw, r.Y, lw, r.H}, AlignLeft)
		}
		lab(m.qcHost, "Host")
		lab(m.qcUser, "Username")
		lab(m.qcPass, "Password")
		lab(m.qcPort, "Port")
	} else if m.sess != nil {
		x := m.sessPill.R.X + m.sessPill.R.W + S(16)
		col := p.Success
		txt := "Connected"
		if m.connecting {
			col, txt = p.Warning, "Connecting…"
		}
		drawStatusDot(c, x+S(5), r.Y+r.H/2, col)
		c.Text(txt+" • "+m.sess.fs.Description(), w.F.Body, p.Text2, Rect{x + S(16), r.Y, m.syncToggle.R.X - x - S(200), r.H}, AlignLeft)
	}
	if m.syncToggle.IsVisible() {
		if !m.qcHost.IsVisible() {
			lbl := "Synchronized browsing"
			lw := w.F.BodySemi.Measure(lbl)
			c.Text(lbl, w.F.BodySemi, p.Text, Rect{m.syncToggle.R.X - lw - S(10), r.Y, lw + S(4), r.H}, AlignLeft)
		} else {
			c.Glyph(icSync, w.F.Icon, ifc(m.syncOn, p.AccentInk, p.Text3), Rect{m.syncToggle.R.X - S(26), r.Y, S(24), r.H})
		}
	}
	m.local.paint(c)
	m.remote.paint(c)
	// bottom panel
	b := m.rBottom
	c.FillRound(b, c.S(10), p.Surface)
	c.StrokeRound(b, c.S(10), p.Border, 1)
	st := m.q.Stats()
	badge := ""
	if st.Total > 0 {
		badge = fmt.Sprintf("%d", st.Total)
		if st.Active > 0 {
			badge = fmt.Sprintf("%d • %d active", st.Total, st.Active)
		}
	}
	m.tabs.Badges = []string{badge, ""}
	c.FillRect(Rect{b.X + 1, b.Y + S(47), b.W - 2, 1}, p.Border)
	m.paintStatus(c)
}

func (m *MainWindow) paintStatus(c *Canvas) {
	p := c.P
	w := m.w
	S := w.S
	r := m.rStatus
	c.FillRect(r, p.Surface2)
	c.FillRect(Rect{0, r.Y, r.W, 1}, p.Border)
	col := p.Text3
	switch m.statusKind {
	case stOK:
		col = p.Success
	case stBusy:
		col = p.Warning
	case stWarn:
		col = p.Warning
	case stError:
		col = p.Danger
	}
	x := S(18)
	if m.statusKind == stOK {
		drawCheckCircle(c, x-S(2), r.Y+(r.H-S(16))/2, S(16), p.Success, p.Surface2)
	} else {
		drawStatusDot(c, x+S(5), r.Y+r.H/2, col)
	}
	st := m.q.Stats()
	right := r.W - S(16)
	ver := "ShoreDrop v" + core.AppVersion
	vw := w.F.Small.Measure(ver)
	c.Text(ver, w.F.Small, p.Text3, Rect{right - vw, r.Y, vw, r.H}, AlignLeft)
	right -= vw + S(18)
	var info string
	if st.Total > 0 {
		info = fmt.Sprintf("Total: %s / %s", core.FormatSize(st.BytesDone), core.FormatSize(st.Bytes))
		if st.Speed > 0 {
			info = core.FormatSpeed(st.Speed) + "   •   " + info
		}
		if st.Active > 0 || st.Queued > 0 {
			info = fmt.Sprintf("%d active, %d queued   •   ", st.Active, st.Queued) + info
		}
		if st.Failed > 0 {
			info += fmt.Sprintf("   •   %d failed", st.Failed)
		}
	} else {
		info = "Queue empty"
	}
	iw := w.F.Small.Measure(info)
	c.Text(info, w.F.Small, p.Text2, Rect{right - iw, r.Y, iw + S(2), r.H}, AlignLeft)
	right -= iw + S(14)
	// sparkline of recent speed
	if len(st.History) > 1 {
		sw, sh := S(90), S(16)
		sx, sy := right-sw, r.Y+(r.H-sh)/2
		mx := 1.0
		for _, v := range st.History {
			if v > mx {
				mx = v
			}
		}
		var pts [][2]float64
		n := len(st.History)
		for i, v := range st.History {
			pts = append(pts, [2]float64{float64(sx) + float64(sw)*float64(i)/float64(max(1, n-1)), float64(sy+sh) - float64(sh)*v/mx})
		}
		c.Polyline(pts, p.AccentInk, c.S(1.4))
		right = sx - S(12)
	}
	c.Text(m.status, w.F.Body, p.Text, Rect{x + S(18), r.Y, right - x - S(18), r.H}, AlignLeft)
}

func (m *MainWindow) paintOverlay(c *Canvas) {
	if m.drag == nil {
		return
	}
	p := c.P
	w := m.w
	n := len(m.drag.rows)
	txt := m.drag.rows[0].Name
	if n > 1 {
		txt = fmt.Sprintf("%d items", n)
	}
	verb := ""
	if m.drag.target != nil && m.drag.target != m.drag.from {
		if m.drag.target.isRemote {
			verb = "Upload "
		} else {
			verb = "Download "
		}
	} else if m.drag.target == m.drag.from && m.drag.row >= 0 {
		verb = "Move into folder: "
	}
	label := verb + txt
	tw := w.F.BodySemi.Measure(label) + w.S(40)
	r := Rect{m.drag.x + w.S(14), m.drag.y + w.S(10), tw, w.S(32)}
	c.FillRound(Rect{r.X + 2, r.Y + 3, r.W, r.H}, c.S(8), p.Shadow)
	bg, fg := p.Surface, p.Text
	if verb != "" {
		bg, fg = p.Accent, p.AccentText
	}
	c.FillRound(r, c.S(8), bg)
	c.StrokeRound(r, c.S(8), p.BorderStr, 1)
	ic := icDocument
	if m.drag.target != nil && m.drag.target != m.drag.from {
		if m.drag.target.isRemote {
			ic = icUpload
		} else {
			ic = icDownload
		}
	}
	c.Glyph(ic, w.F.Icon, fg, Rect{r.X + w.S(8), r.Y, w.S(20), r.H})
	c.Text(label, w.F.BodySemi, fg, Rect{r.X + w.S(32), r.Y, r.W - w.S(36), r.H}, AlignLeft)
}

// ---------------------------------------------------------------- menus

func (m *MainWindow) buildMenus() {
	m.menus = []*menuDef{
		{title: "File", key: 'F', items: m.fileMenu},
		{title: "View", key: 'V', items: m.viewMenu},
		{title: "Transfer", key: 'T', items: m.transferMenu},
		{title: "Bookmarks", key: 'B', items: m.bookmarksMenu},
		{title: "Help", key: 'H', items: m.helpMenu},
	}
}

func (m *MainWindow) openMenu(i int) {
	if i < 0 || i >= len(m.menus) {
		return
	}
	d := m.menus[i]
	m.menuOpen = i
	m.w.Invalidate()
	sx, sy := m.w.ScreenPos(d.rect.X, d.rect.Y+d.rect.H+m.w.S(4))
	menuNav = func(right bool) {
		n := len(m.menus)
		if right {
			m.app.Post(func() { m.openMenu((i + 1) % n) })
		} else {
			m.app.Post(func() { m.openMenu((i + n - 1) % n) })
		}
	}
	m.app.ShowMenu(m.w, d.items(), sx, sy, m.w.S(240), func() {
		menuNav = nil
		m.lastClosedMenu = i
		m.menuOpen = -1
		m.w.Invalidate()
	})
}

func (m *MainWindow) fileMenu() []MenuItem {
	return []MenuItem{
		{Label: "Site Manager…", Shortcut: "Ctrl+S", Icon: icServer, Action: m.openSiteManager},
		{Label: "Quick Connect", Shortcut: "Ctrl+Q", Icon: icPlug, Action: m.focusQuickConnect},
		{Label: "Save Connection to Site Manager…", Icon: icSave, Disabled: !m.connected(), Action: m.saveSessionAsSite},
		Sep,
		{Label: "New Folder", Shortcut: "F7", Icon: icFolderAdd, Action: func() { m.newFolder(m.active) }},
		{Label: "Rename", Shortcut: "F2", Icon: icRename, Action: func() { m.rename(m.active) }},
		{Label: "Delete", Shortcut: "Del", Icon: icDelete, Action: m.deleteCommand},
		{Label: "Change Permissions…", Icon: icPermissions, Disabled: !m.connected(), Action: m.chmodSelected},
		Sep,
		{Label: "Settings…", Shortcut: "Ctrl+,", Icon: icSettings, Action: m.openSettings},
		Sep,
		{Label: "Exit", Shortcut: "Alt+F4", Icon: icClose, Action: m.w.Close},
	}
}

func (m *MainWindow) viewMenu() []MenuItem {
	return []MenuItem{
		{Label: "Light Mode", Checked: m.S.Theme == "light", Icon: icSun, Action: func() { m.app.SetTheme("light") }},
		{Label: "Dark Mode", Checked: m.S.Theme == "dark", Icon: icMoon, Action: func() { m.app.SetTheme("dark") }},
		{Label: "Follow System", Checked: m.S.Theme == "system", Icon: icPC, Action: func() { m.app.SetTheme("system") }},
		{Label: "Toggle Light / Dark", Shortcut: "Ctrl+T", Action: m.toggleTheme},
		Sep,
		{Label: "Refresh", Shortcut: "F5", Icon: icRefresh, Action: m.refreshAll},
		{Label: "Show Hidden Files", Shortcut: "Ctrl+H", Checked: m.S.ShowHidden, Action: m.toggleHidden},
		{Label: "Filter Files", Shortcut: "Ctrl+F", Icon: icSearch, Action: func() { m.w.SetFocusWidget(m.active.filter) }},
		{Label: "Go to Folder", Shortcut: "Ctrl+L", Icon: icFolder, Action: func() { m.w.SetFocusWidget(m.active.path); m.active.path.SelectAll() }},
		Sep,
		{Label: "Transfer Queue", Checked: m.tabs.Selected == 0, Action: func() { m.tabs.Selected = 0; m.layout() }},
		{Label: "Transfer Log", Checked: m.tabs.Selected == 1, Action: func() { m.tabs.Selected = 1; m.layout() }},
	}
}

func (m *MainWindow) transferMenu() []MenuItem {
	return []MenuItem{
		{Label: "Upload Selected", Shortcut: "Ctrl+U", Icon: icUpload, Disabled: !m.connected(), Action: m.uploadSelected},
		{Label: "Download Selected", Shortcut: "Ctrl+D", Icon: icDownload, Disabled: !m.connected(), Action: m.downloadSelected},
		Sep,
		{Label: "Pause All", Shortcut: "Ctrl+P", Icon: icPause, Action: m.q.PauseAll},
		{Label: "Resume All", Shortcut: "Ctrl+Shift+P", Icon: icPlay, Action: m.q.ResumeAll},
		{Label: "Retry Failed", Icon: icRetry, Action: m.q.RetryFailed},
		{Label: "Clear Finished", Icon: icClear, Action: m.q.ClearFinished},
		{Label: "Cancel All", Icon: icClose, Danger: true, Action: m.q.CancelAll},
		Sep,
		{Label: "Disconnect", Shortcut: "Ctrl+W", Icon: icDisconnect, Disabled: !m.connected(), Action: m.disconnect},
		{Label: "Reconnect", Shortcut: "Ctrl+Shift+R", Icon: icRefresh, Disabled: m.lastParams == nil, Action: m.reconnect},
		{Label: "Synchronized Browsing", Shortcut: "Ctrl+Y", Checked: m.syncOn, Disabled: !m.connected(), Action: func() { m.setSync(!m.syncOn) }},
	}
}

func (m *MainWindow) bookmarksMenu() []MenuItem {
	var items []MenuItem
	for i := range m.sites.Sites {
		s := m.sites.Sites[i]
		label := s.Label()
		if s.Folder != "" {
			label = s.Folder + " / " + label
		}
		items = append(items, MenuItem{Label: label, Icon: icServer, Action: func() { m.connectSite(&s) }})
		if len(items) >= 20 {
			break
		}
	}
	if len(items) == 0 {
		items = append(items, MenuItem{Label: "No saved sites yet", Disabled: true})
	}
	items = append(items, Sep,
		MenuItem{Label: "Bookmark Current Connection…", Icon: icAdd, Disabled: !m.connected(), Action: m.saveSessionAsSite},
		MenuItem{Label: "Site Manager…", Shortcut: "Ctrl+S", Icon: icServer, Action: m.openSiteManager})
	return items
}

func (m *MainWindow) helpMenu() []MenuItem {
	return []MenuItem{
		{Label: "Keyboard Shortcuts", Shortcut: "F1", Icon: icInfo, Action: m.showShortcuts},
		{Label: "Open Settings Folder", Icon: icFolder, Action: func() { shellOpen(core.ConfigDir()) }},
		Sep,
		{Label: "About ShoreDrop", Icon: icInfo, Action: func() { m.app.About(m.w) }},
	}
}

func (m *MainWindow) showShortcuts() {
	m.app.Message(m.w, MessageOpts{Kind: MsgInfo, Title: "Keyboard Shortcuts", Heading: "Keyboard shortcuts",
		Text:   "Shortcuts work from the file lists; most also work while typing in a box.",
		Detail: shortcutHelp})
}

func (m *MainWindow) protocolMenu() {
	var items []MenuItem
	for _, pr := range []string{remote.SFTP, remote.FTPS, remote.FTPSImplicit, remote.FTP} {
		pr := pr
		label := map[string]string{
			remote.SFTP:         "SFTP — SSH File Transfer Protocol",
			remote.FTPS:         "FTPS — FTP over explicit TLS",
			remote.FTPSImplicit: "FTPS — FTP over implicit TLS",
			remote.FTP:          "FTP — plain, unencrypted",
		}[pr]
		items = append(items, MenuItem{Label: label, Checked: pr == m.protocol, Action: func() { m.setProtocol(pr) }})
	}
	sx, sy := m.w.ScreenPos(m.qcProto.R.X, m.qcProto.R.Y+m.qcProto.R.H+m.w.S(6))
	m.app.ShowMenu(m.w, items, sx, sy, 0, nil)
}

func (m *MainWindow) setProtocol(pr string) {
	old := m.protocol
	m.protocol = pr
	m.qcProto.Text = remote.ProtocolLabel(pr)
	if pr == remote.FTPSImplicit {
		m.qcProto.Text = "FTPS·990"
	}
	if cur := m.qcPort.Text(); cur == "" || cur == strconv.Itoa(remote.DefaultPort(old)) {
		m.qcPort.SetText(strconv.Itoa(remote.DefaultPort(pr)))
	}
	m.layout()
}

func (m *MainWindow) sessionMenu() {
	if m.sess == nil {
		return
	}
	items := []MenuItem{
		{Label: "Reconnect", Icon: icRefresh, Action: m.reconnect},
		{Label: "Disconnect", Icon: icDisconnect, Action: m.disconnect},
		Sep,
		{Label: "Save to Site Manager…", Icon: icSave, Action: m.saveSessionAsSite},
		{Label: "Copy server address", Icon: icCopy, Action: func() {
			setClipboardText(m.sess.url(m.remote.dir))
		}},
	}
	sx, sy := m.w.ScreenPos(m.sessPill.R.X, m.sessPill.R.Y+m.sessPill.R.H+m.w.S(6))
	m.app.ShowMenu(m.w, items, sx, sy, m.sessPill.R.W, nil)
}

func (m *MainWindow) focusQuickConnect() {
	m.qcMode = true
	m.layout()
	m.w.SetFocusWidget(m.qcHost)
	m.qcHost.SelectAll()
}

// ---------------------------------------------------------------- keys

func (m *MainWindow) onKey(e *KeyEvent) bool {
	if m.drag != nil && e.VK == VK_ESCAPE {
		m.drag.from.list.CancelDrag()
		return true
	}
	if nativeMenuBar && e.Ctrl && !e.FromEdit {
		// Finder-style navigation: ⌘[ ⌘] ⌘↑
		switch e.VK {
		case VK_OEM_4:
			m.active.back()
			return true
		case VK_OEM_6:
			m.active.forward()
			return true
		case VK_UP:
			m.active.up()
			return true
		}
	}
	if e.Alt && !e.Ctrl && !(nativeMenuBar && e.FromEdit) {
		for i, d := range m.menus {
			if e.VK == d.key && !nativeMenuBar {
				m.openMenu(i)
				return true
			}
		}
		switch e.VK {
		case VK_LEFT:
			m.active.back()
			return true
		case VK_RIGHT:
			m.active.forward()
			return true
		case VK_UP:
			m.active.up()
			return true
		}
		return false
	}
	if e.VK == VK_F10 && !nativeMenuBar {
		m.openMenu(0)
		return true
	}
	if e.Ctrl {
		switch e.VK {
		case 'R':
			if e.Shift {
				m.reconnect()
			} else {
				m.refreshAll()
			}
		case 'U':
			m.uploadSelected()
		case 'D':
			m.downloadSelected()
		case 'S':
			m.openSiteManager()
		case 'Q':
			m.focusQuickConnect()
		case 'W':
			m.disconnect()
		case 'P':
			if e.Shift {
				m.q.ResumeAll()
			} else {
				m.q.PauseAll()
			}
		case 'Y':
			if m.connected() {
				m.setSync(!m.syncOn)
			}
		case 'T':
			m.toggleTheme()
		case 'H':
			m.toggleHidden()
		case 'L':
			m.w.SetFocusWidget(m.active.path)
			m.active.path.SelectAll()
		case 'F':
			m.w.SetFocusWidget(m.active.filter)
			m.active.filter.SelectAll()
		case VK_OEM_COMMA:
			m.openSettings()
		default:
			return false
		}
		return true
	}
	switch e.VK {
	case VK_F5:
		m.refreshAll()
		return true
	case VK_F1:
		m.showShortcuts()
		return true
	case VK_BROWSER_BACK:
		m.active.back()
		return true
	case VK_BROWSER_FORWARD:
		m.active.forward()
		return true
	}
	if e.FromEdit {
		return false
	}
	switch e.VK {
	case VK_F2:
		m.rename(m.active)
	case VK_DELETE:
		if _, ok := m.w.focus.(*QueueView); ok {
			return false
		}
		m.deleteSelected(m.active)
	case VK_F7:
		m.newFolder(m.active)
	case VK_BACK:
		if qv, ok := m.w.focus.(*QueueView); ok && nativeMenuBar {
			qv.Q.Remove(qv.Selected()...) // ⌫ is the Mac delete key
			return true
		}
		m.active.up()
	case VK_APPS:
		r := m.active.list.R
		m.paneContext(m.active, r.X+r.W/3, r.Y+m.w.S(60))
	default:
		return false
	}
	return true
}

// deleteCommand deletes the selection in the focused list: queue entries if
// the transfer queue has focus, otherwise files in the active pane.
func (m *MainWindow) deleteCommand() {
	if qv, ok := m.w.focus.(*QueueView); ok {
		qv.Q.Remove(qv.Selected()...)
		return
	}
	m.deleteSelected(m.active)
}

func (m *MainWindow) toggleHidden() {
	m.S.ShowHidden = !m.S.ShowHidden
	m.S.Save()
	m.refreshAll()
}

func (m *MainWindow) refreshAll() {
	m.local.refresh()
	if m.connected() {
		m.remote.refresh()
	}
}

// ---------------------------------------------------------------- lifecycle

func (m *MainWindow) onClose() bool {
	st := m.q.Stats()
	if st.Active > 0 || st.Queued > 0 {
		if !m.app.Confirm(m.w, "Transfers are still running", fmt.Sprintf("%d transfer(s) are active or queued. Quit ShoreDrop and stop them?", st.Active+st.Queued), "Quit", true) {
			return false
		}
		m.q.CancelAll()
	}
	m.w.savePlacement(&m.S.Window)
	if m.local.dir != "" {
		m.S.LastLocalDir = m.local.dir
	}
	// Every preference is saved the moment it changes; on exit only the
	// window layout is written, merged into what is on disk, so a second
	// ShoreDrop window closing later can't undo changes made in another.
	disk := core.LoadSettings()
	disk.Window = m.S.Window
	disk.LastLocalDir = m.S.LastLocalDir
	disk.Save()
	if m.sess != nil {
		go m.sess.close()
	}
	go m.q.CloseIdle()
	return true
}

func (m *MainWindow) show() {
	m.w.showPlaced(m.S.Window)
	m.layout()
	if m.S.LastHost == "" {
		m.w.SetFocusWidget(m.qcHost)
	} else {
		m.w.SetFocusWidget(m.local.list)
	}
}

func (m *MainWindow) keepAlive() {
	if m.sess != nil && m.S.KeepAlive {
		m.sess.Do(func(fs remote.FS) error { return fs.KeepAlive() }, func(err error) {})
	}
}

var _ = strings.TrimSpace
