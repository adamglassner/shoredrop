//go:build windows

package ui

import (
	"fmt"
	"strconv"
	"strings"

	"shoredrop/internal/core"
	"shoredrop/internal/remote"
)

// NavList is a vertical list of sections with icons.
type NavList struct {
	Base
	Items    []string
	Icons    []rune
	Selected int
	hover    int
	OnChange func(int)
}

func (n *NavList) rowH() int { return n.W.S(46) }

func (n *NavList) OnMouse(e *MouseEvent) bool {
	i := (e.Y - n.R.Y) / n.rowH()
	if i < 0 || i >= len(n.Items) {
		i = -1
	}
	switch e.Kind {
	case MouseMove:
		if i != n.hover {
			n.hover = i
			n.W.Invalidate()
		}
	case MouseLeave:
		n.hover = -1
		n.W.Invalidate()
	case MouseDown:
		if i >= 0 && i != n.Selected {
			n.Selected = i
			n.W.Invalidate()
			if n.OnChange != nil {
				n.OnChange(i)
			}
		}
	}
	return true
}

func (n *NavList) Paint(c *Canvas) {
	p := c.P
	for i, it := range n.Items {
		r := Rect{n.R.X, n.R.Y + i*n.rowH(), n.R.W, n.rowH() - n.W.S(4)}
		fg, ic := p.Text, p.Text2
		if i == n.Selected {
			c.FillRound(r, c.S(8), p.Accent)
			fg, ic = p.AccentText, p.AccentText
		} else if i == n.hover {
			c.FillRound(r, c.S(8), p.Hover)
		}
		c.Glyph(n.Icons[i], n.W.F.IconBig, ic, Rect{r.X + n.W.S(10), r.Y, n.W.S(28), r.H})
		f := n.W.F.Body
		if i == n.Selected {
			f = n.W.F.BodySemi
		}
		c.Text(it, f, fg, Rect{r.X + n.W.S(48), r.Y, r.W - n.W.S(52), r.H}, AlignLeft)
	}
}

type settingsDlg struct {
	m   *MainWindow
	d   *Dialog
	s   core.Settings // working copy
	nav *NavList

	pages [][]Widget
	rows  []settingRow

	defProto             *Dropdown
	timeout              *TextBox
	slider               *Slider
	resume               *Toggle
	syncDef              *Toggle
	theme                *Segmented
	theme2               *Segmented
	ftpMode              *Segmented
	keepAlive            *Toggle
	warnFTP              *Toggle
	dlDir                *TextBox
	dlBrowse             *Button
	upDir                *TextBox
	exists               *Segmented
	confirm              *Toggle
	hidden               *Toggle
	forgetKeys, forgetPw *Button
	openCfg              *Button
}

type settingRow struct {
	page  int
	title string
	sub   string
	r     Rect
}

func (m *MainWindow) openSettings() {
	sd := &settingsDlg{m: m, s: *m.S}
	d := m.app.newDialog(m.w, "ShoreDrop Settings", 900, 700, true)
	sd.d = d
	w := d.Window
	d.FooterText = "ShoreDrop v" + core.AppVersion
	sd.nav = &NavList{Base: Base{W: w}, Items: []string{"General", "Connection", "Transfers", "Appearance", "Security", "About"},
		Icons: []rune{icSettings, icPlug, icTransfer, icPalette, icShield, icInfo}, hover: -1}
	sd.nav.OnChange = func(int) { sd.layout(d.BodyRect()) }
	w.Add(sd.nav)

	themeIdx := map[string]int{"light": 0, "dark": 1, "system": 2}
	sd.defProto = NewDropdown(w, protoNames, indexOf(protoIDs, sd.s.DefaultProtocol), func(i int) { sd.s.DefaultProtocol = protoIDs[i] })
	sd.timeout = NewTextBox(w, "30")
	sd.timeout.SetText(strconv.Itoa(sd.s.TimeoutSec))
	sd.slider = NewSlider(w, 1, 10, sd.s.MaxTransfers, func(v int) { sd.s.MaxTransfers = v; d.Invalidate() })
	sd.resume = NewToggle(w, sd.s.ResumeTransfers, func(v bool) { sd.s.ResumeTransfers = v })
	sd.syncDef = NewToggle(w, sd.s.SyncByDefault, func(v bool) { sd.s.SyncByDefault = v })
	setTheme := func(i int) {
		sd.s.Theme = []string{"light", "dark", "system"}[i]
		sd.theme.Selected, sd.theme2.Selected = i, i
		// preview immediately; Cancel restores the previous mode
		m.app.S.Theme = sd.s.Theme
		m.app.applyTheme()
	}
	sd.theme = NewSegmented(w, []string{"Light", "Dark", "System"}, themeIdx[sd.s.Theme], setTheme)
	sd.theme2 = NewSegmented(w, []string{"Light", "Dark", "Follow System"}, themeIdx[sd.s.Theme], setTheme)
	sd.ftpMode = NewSegmented(w, []string{"Passive (recommended)", "Active"}, map[bool]int{true: 1, false: 0}[sd.s.FTPMode == "active"], func(i int) {
		sd.s.FTPMode = []string{"passive", "active"}[i]
	})
	sd.keepAlive = NewToggle(w, sd.s.KeepAlive, func(v bool) { sd.s.KeepAlive = v })
	sd.warnFTP = NewToggle(w, sd.s.WarnPlainFTP, func(v bool) { sd.s.WarnPlainFTP = v })
	sd.dlDir = NewTextBox(w, `C:\Users\you\Downloads`)
	sd.dlDir.SetText(sd.s.DownloadDir)
	sd.dlBrowse = NewButton(w, "Browse…", BtnSecondary, func() {
		if f, ok := pickFolder(w, "Default download folder", sd.dlDir.Text()); ok {
			sd.dlDir.SetText(f)
		}
	})
	sd.upDir = NewTextBox(w, "/public_html  (empty = server home folder)")
	sd.upDir.SetText(sd.s.UploadDir)
	sd.exists = NewSegmented(w, []string{"Overwrite", "Resume", "Skip"}, indexOf([]string{"overwrite", "resume", "skip"}, sd.s.ExistsAction), func(i int) {
		sd.s.ExistsAction = []string{"overwrite", "resume", "skip"}[i]
	})
	sd.confirm = NewToggle(w, sd.s.ConfirmDelete, func(v bool) { sd.s.ConfirmDelete = v })
	sd.hidden = NewToggle(w, sd.s.ShowHidden, func(v bool) { sd.s.ShowHidden = v })
	sd.forgetKeys = NewButton(w, "Forget saved server keys and certificates", BtnSecondary, func() {
		if m.app.Confirm(w, "Forget trusted servers?", "ShoreDrop will ask you to confirm each server's identity again on the next connection.", "Forget", true) {
			m.trust.HostKeys = map[string]string{}
			m.trust.Certs = map[string]string{}
			m.trust.Save()
			m.sessionTrust = map[string]string{}
		}
	})
	sd.forgetPw = NewButton(w, "Remove all saved passwords", BtnSecondary, func() {
		if m.app.Confirm(w, "Remove saved passwords?", fmt.Sprintf("All passwords saved for your %d site(s) will be deleted from %s.", len(m.sites.Sites), core.CredentialStoreName), "Remove", true) {
			m.sites.ForgetAllPasswords()
		}
	})
	sd.openCfg = NewButton(w, "Open settings folder", BtnSecondary, func() { shellOpen(core.ConfigDir()) })

	sd.pages = [][]Widget{
		{sd.defProto, sd.timeout, sd.slider, sd.resume, sd.syncDef, sd.theme},
		{sd.ftpMode, sd.keepAlive},
		{sd.dlDir, sd.dlBrowse, sd.upDir, sd.exists, sd.confirm, sd.hidden},
		{sd.theme2},
		{sd.warnFTP, sd.forgetKeys, sd.forgetPw},
		{sd.openCfg},
	}
	for _, pg := range sd.pages {
		w.Add(pg...)
	}
	prevTheme := m.S.Theme
	d.AddButton("Reset to defaults", BtnSecondary, func() bool {
		if !m.app.Confirm(w, "Reset all settings?", "Every preference returns to its default. Saved sites and passwords are not affected.", "Reset", true) {
			return false
		}
		def := core.DefaultSettings()
		def.Window = m.S.Window
		*m.S = def
		m.S.Save()
		m.app.applyTheme()
		m.q.Dispatch()
		return true
	})
	d.AddButton("Cancel", BtnSecondary, func() bool {
		m.app.S.Theme = prevTheme
		m.app.applyTheme()
		return true
	})
	d.AddButton("Save", BtnPrimary, func() bool {
		t, err := strconv.Atoi(strings.TrimSpace(sd.timeout.Text()))
		if err != nil || t < 5 || t > 600 {
			m.app.Alert(w, MsgError, "Invalid timeout", "Enter a connection timeout between 5 and 600 seconds.")
			sd.nav.Selected = 0
			sd.layout(d.BodyRect())
			return false
		}
		sd.s.TimeoutSec = t
		sd.s.DownloadDir = strings.TrimSpace(sd.dlDir.Text())
		up := strings.TrimSpace(sd.upDir.Text())
		if up != "" && !strings.HasPrefix(up, "/") {
			up = "/" + up
		}
		sd.s.UploadDir = up
		hiddenChanged := sd.s.ShowHidden != m.S.ShowHidden
		sd.s.Window = m.S.Window
		sd.s.PlainFTPOK = m.S.PlainFTPOK
		*m.S = sd.s
		if err := m.S.Save(); err != nil {
			m.app.Alert(w, MsgError, "Could not save settings", err.Error())
		}
		m.app.applyTheme()
		m.q.Dispatch()
		if hiddenChanged {
			m.refreshAll()
		}
		m.log.Add(core.LogInfo, "Settings saved")
		return true
	})
	d.DefaultBtn = 2
	d.OnClose = func() bool {
		if d.Result == -1 { // closed with X or Esc: same as Cancel
			m.app.S.Theme = prevTheme
			m.app.applyTheme()
		}
		return true
	}
	d.LayoutBody = sd.layout
	d.PaintBody = sd.paint
	d.focus = sd.nav
	d.Run()
}

func showW(x Widget, v bool) {
	switch t := x.(type) {
	case *TextBox:
		t.Show(v)
	case interface{ Show(bool) }:
		t.Show(v)
	}
}

func (sd *settingsDlg) layout(body Rect) {
	d := sd.d
	S := d.S
	navW := S(250)
	sd.nav.SetBounds(Rect{S(14), body.Y + S(16), navW - S(28), S(46) * len(sd.nav.Items)})
	for i, pg := range sd.pages {
		for _, x := range pg {
			showW(x, i == sd.nav.Selected)
		}
	}
	x0 := navW + S(30)
	rw := d.w - x0 - S(30)
	y := body.Y + S(76)
	sd.rows = sd.rows[:0]
	fh := S(36)
	add := func(title, sub string, ctl Widget, ctlW int, below bool) {
		h := S(50)
		if below {
			sd.rows = append(sd.rows, settingRow{sd.nav.Selected, title, sub, Rect{x0, y, rw, S(22)}})
			if ctl != nil {
				cy := y + S(26)
				if sub != "" {
					cy += S(18)
				}
				ctl.SetBounds(Rect{x0, cy, ctlW, fh})
				y = cy + fh + S(18)
			}
			return
		}
		sd.rows = append(sd.rows, settingRow{sd.nav.Selected, title, sub, Rect{x0, y, rw - ctlW - S(16), h}})
		if ctl != nil {
			ctl.SetBounds(Rect{x0 + rw - ctlW, y + (h-fh)/2, ctlW, fh})
		}
		y += h + S(6)
	}
	switch sd.nav.Selected {
	case 0:
		add("Default protocol", "", sd.defProto, rw, true)
		add("Connection timeout (seconds)", "", sd.timeout, S(120), true)
		add("Max simultaneous transfers", "", sd.slider, rw-S(60), true)
		add("Resume interrupted transfers", "Continue partial uploads and downloads where they stopped", sd.resume, S(48), false)
		add("Keep remote directory in sync while browsing", "Turn on synchronized browsing automatically after connecting", sd.syncDef, S(48), false)
		add("Appearance / Theme", "", sd.theme, S(330), true)
	case 1:
		add("FTP data connection mode", "Passive works behind most routers and firewalls. Use Active only if the server requires it.", sd.ftpMode, S(380), true)
		add("Keep connections alive", "Send a small keep-alive every 30 seconds so idle connections aren't dropped", sd.keepAlive, S(48), false)
	case 2:
		add("Default download folder", "Where the local pane opens and downloads land by default", nil, 0, true)
		sd.dlDir.SetBounds(Rect{x0, y + S(44), rw - S(122), fh})
		sd.dlBrowse.SetBounds(Rect{x0 + rw - S(110), y + S(44), S(110), fh})
		y += S(44) + fh + S(18)
		add("Default upload folder (remote)", "Folder the remote pane opens after connecting, unless the site sets one", sd.upDir, rw, true)
		add("When a file already exists", "", sd.exists, S(330), true)
		add("Confirm before deleting", "", sd.confirm, S(48), false)
		add("Show hidden files", "Files starting with a dot, and hidden Windows files", sd.hidden, S(48), false)
	case 3:
		add("Theme", "Choose light, dark, or follow the Windows setting. The choice is remembered.", sd.theme2, S(420), true)
	case 4:
		add("Warn before connecting with plain FTP", "Plain FTP sends passwords and files unencrypted", sd.warnFTP, S(48), false)
		y += S(100)
		sd.forgetKeys.SetBounds(Rect{x0, y, sd.forgetKeys.PreferredWidth(), fh})
		sd.forgetPw.SetBounds(Rect{x0, y + fh + S(12), sd.forgetPw.PreferredWidth(), fh})
	case 5:
		sd.openCfg.SetBounds(Rect{x0, body.Y + S(330), sd.openCfg.PreferredWidth(), fh})
	}
	d.Invalidate()
}

func (sd *settingsDlg) paint(c *Canvas, body Rect) {
	p := c.P
	d := sd.d
	S := d.S
	navW := S(250)
	c.FillRect(Rect{0, body.Y, navW, body.H}, p.Window)
	c.FillRect(Rect{navW - 1, body.Y, 1, body.H}, p.Border)
	x0 := navW + S(30)
	rw := d.w - x0 - S(30)
	c.Text(sd.nav.Items[sd.nav.Selected], d.F.H1, p.TitleText, Rect{x0, body.Y + S(18), rw, S(40)}, AlignLeft)
	for _, r := range sd.rows {
		if r.sub == "" {
			c.Text(r.title, d.F.BodySemi, p.Text, r.r, AlignLeft)
			continue
		}
		if r.r.H > S(30) {
			c.Text(r.title, d.F.BodySemi, p.Text, Rect{r.r.X, r.r.Y + S(4), r.r.W, S(22)}, AlignLeft)
			c.Text(r.sub, d.F.Small, p.Text2, Rect{r.r.X, r.r.Y + S(26), r.r.W, S(20)}, AlignLeft)
		} else {
			c.Text(r.title, d.F.BodySemi, p.Text, r.r, AlignLeft)
			c.Text(r.sub, d.F.Small, p.Text2, Rect{r.r.X, r.r.Y + S(22), r.r.W, S(20)}, AlignLeft)
		}
	}
	switch sd.nav.Selected {
	case 0:
		sr := sd.slider.R
		c.Text(strconv.Itoa(sd.slider.Value), d.F.Title, p.AccentInk, Rect{sr.X + sr.W + S(14), sr.Y, S(40), sr.H}, AlignLeft)
		c.Text(fmt.Sprintf("Up to %d files transfer in parallel, each over its own connection.", sd.slider.Value), d.F.Small, p.Text2, Rect{sr.X, sr.Y + sr.H, sr.W, S(18)}, AlignLeft)
	case 1:
		c.TextWrap("Connection timeout is on the General page. SFTP always uses a single encrypted connection per transfer; FTP opens a separate data connection for each listing and file.",
			d.F.Small, p.Text2, Rect{x0, sd.ftpMode.R.Y + S(150), rw, S(60)})
	case 3:
		// small previews of both themes
		y := sd.theme2.R.Y + S(66)
		pw := (rw - S(20)) / 2
		for i, pal := range []*Palette{&Light, &Dark} {
			r := Rect{x0 + i*(pw+S(20)), y, pw, S(170)}
			c.FillRound(r, c.S(10), pal.Window)
			c.StrokeRound(r, c.S(10), ifc(sd.s.Theme == []string{"light", "dark"}[i], p.Accent, p.Border), c.S(2))
			drawLogo(c, r.X+S(14), r.Y+S(12), S(26), pal.Logo)
			c.Text("ShoreDrop", d.F.BodyBold, pal.TitleText, Rect{r.X + S(48), r.Y + S(10), S(120), S(30)}, AlignLeft)
			card := Rect{r.X + S(12), r.Y + S(50), r.W - S(24), S(104)}
			c.FillRound(card, c.S(8), pal.Surface)
			c.StrokeRound(card, c.S(8), pal.Border, 1)
			for k, n := range []string{"index.html", "style.css", "images"} {
				ry := card.Y + S(8) + k*S(30)
				if k == 0 {
					c.FillRound(Rect{card.X + S(6), ry, card.W - S(12), S(28)}, c.S(5), pal.Selection)
				}
				c.Text(n, d.F.Body, ifc(k == 0, pal.SelText, pal.Text), Rect{card.X + S(16), ry, card.W - S(80), S(28)}, AlignLeft)
				c.Text("12 KB", d.F.Small, ifc(k == 0, pal.SelText, pal.Text2), Rect{card.X + card.W - S(64), ry, S(56), S(28)}, AlignLeft)
			}
			c.Text([]string{"Light", "Dark"}[i], d.F.SmallSemi, p.Text2, Rect{r.X, r.Y + r.H + S(6), r.W, S(20)}, AlignCenter)
		}
	case 4:
		c.TextWrap("Saved site passwords are stored in "+core.CredentialStoreName+" — never in ShoreDrop's settings files. "+
			"SSH host keys and accepted FTPS certificates are remembered so ShoreDrop can warn you if a server's identity changes.",
			d.F.Body, p.Text2, Rect{x0, body.Y + S(150), rw, S(80)})
	case 5:
		ls := S(72)
		drawLogo(c, x0, body.Y+S(80), ls, p.Logo)
		c.Text("ShoreDrop", d.F.Brand, p.TitleText, Rect{x0 + ls + S(18), body.Y + S(80), S(300), S(50)}, AlignLeft)
		c.Text("Version "+core.AppVersion, d.F.BodySemi, p.AccentInk, Rect{x0 + ls + S(20), body.Y + S(128), S(300), S(24)}, AlignLeft)
		c.TextWrap("A modern FTP, FTPS and SFTP client with a dual-pane browser, transfer queue and site manager.\n\nProtocols: "+
			remote.ProtocolLabel(remote.SFTP)+", FTPS (explicit and implicit TLS), FTP.\nSettings folder: "+core.ConfigDir(),
			d.F.Body, p.Text2, Rect{x0, body.Y + S(180), rw, S(140)})
	}
}
