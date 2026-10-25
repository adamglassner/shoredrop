//go:build windows

package ui

import (
	"crypto/sha256"
	"crypto/tls"
	enchex "encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"shoredrop/internal/core"
	"shoredrop/internal/remote"
	"shoredrop/internal/sshc"
)

// Session is the browsing connection. Operations run one at a time on a
// dedicated goroutine; a dropped connection is re-established once
// automatically.
type Session struct {
	params remote.Params
	fs     remote.FS
	label  string
	site   *core.Site
	jobs   chan func()
	done   chan struct{}
	app    *App
	log    *core.Log
}

func newSession(a *App, p remote.Params, fs remote.FS, label string, site *core.Site, log *core.Log) *Session {
	s := &Session{params: p, fs: fs, label: label, site: site, jobs: make(chan func(), 64), done: make(chan struct{}), app: a, log: log}
	go func() {
		for {
			select {
			case job := <-s.jobs:
				job()
			case <-s.done:
				return
			}
		}
	}()
	return s
}

func isConnErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	s := strings.ToLower(err.Error())
	for _, k := range []string{"connection lost", "connection reset", "broken pipe", "closed", "forcibly", "aborted", "421 "} {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

// Do runs fn against the connection; done (optional) receives the result on the UI thread.
func (s *Session) Do(fn func(fs remote.FS) error, done func(err error)) {
	job := func() {
		err := fn(s.fs)
		if isConnErr(err) {
			s.log.Add(core.LogWarning, "Connection to %s lost (%v) — reconnecting…", s.params.Host, err)
			p := s.params
			fs, cerr := remote.Connect(p)
			if cerr == nil {
				old := s.fs
				s.fs = fs
				go old.Close()
				s.log.Add(core.LogSuccess, "Reconnected to %s", s.params.Host)
				err = fn(fs)
			} else {
				s.log.Add(core.LogError, "Reconnect failed: %v", cerr)
			}
		}
		if done != nil {
			s.app.Post(func() { done(err) })
		}
	}
	select {
	case s.jobs <- job:
	case <-s.done:
	}
}

func (s *Session) close() {
	select {
	case <-s.done:
		return
	default:
	}
	close(s.done)
	s.fs.Close()
}

func (s *Session) url(dir string) string {
	scheme := "sftp"
	switch s.params.Protocol {
	case remote.FTP:
		scheme = "ftp"
	case remote.FTPS, remote.FTPSImplicit:
		scheme = "ftps"
	}
	host := s.params.Host
	if s.params.Port != remote.DefaultPort(s.params.Protocol) {
		host = net.JoinHostPort(host, strconv.Itoa(s.params.Port))
	}
	user := ""
	if s.params.User != "" {
		user = s.params.User + "@"
	}
	return scheme + "://" + user + host + dir
}

// connRequest is everything needed to (re)connect.
type connRequest struct {
	params remote.Params
	label  string
	site   *core.Site
}

func friendlyErr(err error) string {
	if err == nil {
		return ""
	}
	var ae *sshc.AuthError
	if errors.As(err, &ae) {
		return "Login failed — check the username and password (or key). " + err.Error()
	}
	s := err.Error()
	low := strings.ToLower(s)
	switch {
	case strings.Contains(low, "530"):
		return "Login failed — the server rejected the username or password. (" + s + ")"
	case strings.Contains(low, "no such host"):
		return "The host name could not be found. Check the spelling of the server address."
	case strings.Contains(low, "auth tls refused") || strings.Contains(low, "does not support ftps"):
		return "This server does not support FTPS (FTP over TLS). Try SFTP, or plain FTP if you accept that it is unencrypted. (" + s + ")"
	case strings.Contains(low, "connection refused") || strings.Contains(low, "actively refused"):
		return "The server refused the connection. Check the port number and protocol."
	case strings.Contains(low, "timeout") || strings.Contains(low, "failed to respond") || strings.Contains(low, "did not properly respond"):
		return "The connection timed out. The server may be offline, or a firewall is blocking it."
	case errors.Is(err, os.ErrPermission) || strings.Contains(low, "permission denied") || strings.Contains(low, "access is denied"):
		return "Permission denied. (" + s + ")"
	case errors.Is(err, os.ErrNotExist) || strings.Contains(low, "no such file"):
		return "No such file or folder. (" + s + ")"
	}
	return s
}

// ---------------------------------------------------------------- connect

func (m *MainWindow) quickConnect() {
	host := strings.TrimSpace(m.qcHost.Text())
	if host == "" {
		m.app.Alert(m.w, MsgInfo, "Enter a host", "Type the server address (for example files.example.com) in the Host box, then press Connect.")
		m.w.SetFocusWidget(m.qcHost)
		return
	}
	proto := m.protocol
	low := strings.ToLower(host)
	switch {
	case strings.HasPrefix(low, "sftp://"):
		proto = remote.SFTP
	case strings.HasPrefix(low, "ftps://"):
		proto = remote.FTPS
	case strings.HasPrefix(low, "ftp://"):
		proto = remote.FTP
	}
	if proto != m.protocol {
		m.setProtocol(proto)
	}
	for _, pre := range []string{"sftp://", "ftps://", "ftp://"} {
		if strings.HasPrefix(low, pre) {
			host = host[len(pre):]
		}
	}
	host = strings.TrimSuffix(host, "/")
	port, err := strconv.Atoi(strings.TrimSpace(m.qcPort.Text()))
	if h, ps, err2 := net.SplitHostPort(host); err2 == nil {
		host = h
		port, err = strconv.Atoi(ps)
	}
	if err != nil || port <= 0 || port > 65535 {
		port = remote.DefaultPort(proto)
	}
	m.qcHost.SetText(host)
	m.qcPort.SetText(strconv.Itoa(port))
	p := remote.Params{Protocol: proto, Host: host, Port: port, User: strings.TrimSpace(m.qcUser.Text()), Password: m.qcPass.Text()}
	m.S.LastHost, m.S.LastUser, m.S.LastPort, m.S.LastProtocol = host, p.User, port, proto
	m.S.Save()
	label := host
	if p.User != "" {
		label = p.User + "@" + host
	}
	m.connect(&connRequest{params: p, label: label + " — " + remote.ProtocolLabel(proto)})
}

func (m *MainWindow) connectSite(s *core.Site) {
	p := remote.Params{Protocol: s.Protocol, Host: s.Host, Port: s.Port, User: s.User}
	switch s.Logon {
	case core.LogonAnonymous:
		p.User = "anonymous"
		p.Password = "anonymous@"
	case core.LogonKey:
		p.KeyFile = s.KeyFile
	case core.LogonAsk:
		pw, ok := m.app.Prompt(m.w, "Password", fmt.Sprintf("Password for %s on %s:", s.User, s.Host), "", true)
		if !ok {
			return
		}
		p.Password = pw
	default:
		pw, err := core.Credentials.Get(s.CredentialTarget())
		if err != nil && !errors.Is(err, core.ErrNoCredential) {
			m.log.Add(core.LogWarning, "Could not read the saved password from %s: %v", core.CredentialStoreName, err)
		}
		p.Password = pw
	}
	switch s.TransferMode {
	case "active":
		p.Passive = false
	case "passive":
		p.Passive = true
	}
	site := *s
	m.connect(&connRequest{params: p, label: s.Label(), site: &site})
}

func (m *MainWindow) reconnect() {
	if m.lastParams != nil {
		r := *m.lastParams
		m.connect(&r)
	}
}

func (m *MainWindow) connect(req *connRequest) {
	p := req.params
	if p.Host == "" {
		return
	}
	if p.Port == 0 {
		p.Port = remote.DefaultPort(p.Protocol)
	}
	if req.site == nil || req.site.TransferMode == "" || req.site.TransferMode == "default" {
		p.Passive = m.S.FTPMode != "active"
	}
	// Plain FTP warning.
	if p.Protocol == remote.FTP && m.S.WarnPlainFTP && !m.S.PlainFTPAcknowledged(p.Host) {
		res, dontWarn := m.app.Message(m.w, MessageOpts{
			Kind:    MsgSecurity,
			Title:   "Unencrypted connection",
			Heading: "This connection is not encrypted",
			Text: "Plain FTP sends your username, password and every file in clear text. Anyone on the network path " +
				"(public Wi-Fi, your ISP, the hosting network) could read or change them.\n\n" +
				"If the server supports it, use FTPS (FTP over TLS) or SFTP instead.",
			Buttons: []string{"Cancel", "Connect anyway", "Use FTPS instead"},
			Primary: 2,
			Check:   "Don't warn me again for " + p.Host,
		})
		switch res {
		case 2:
			p.Protocol = remote.FTPS
			req.label = strings.Replace(req.label, "— FTP", "— FTPS", 1)
			if req.site == nil {
				m.setProtocol(remote.FTPS)
			}
		case 1:
			if dontWarn {
				m.S.PlainFTPOK = append(m.S.PlainFTPOK, p.Host)
				m.S.Save()
			}
			m.log.Add(core.LogWarning, "Connecting to %s with plain, unencrypted FTP", p.Host)
		default:
			return
		}
	}
	// Private key passphrase.
	if p.KeyFile != "" && p.KeyPassphrase == "" {
		data, err := os.ReadFile(p.KeyFile)
		if err != nil {
			m.app.Alert(m.w, MsgError, "Cannot read key file", err.Error())
			return
		}
		if _, err := sshc.ParsePrivateKey(data, ""); errors.Is(err, sshc.ErrPassphraseNeeded) {
			for {
				pw, ok := m.app.Prompt(m.w, "Key passphrase", "Enter the passphrase for "+filepath.Base(p.KeyFile)+":", "", true)
				if !ok {
					return
				}
				if _, err := sshc.ParsePrivateKey(data, pw); err == nil {
					p.KeyPassphrase = pw
					break
				}
			}
		} else if err != nil {
			m.app.Alert(m.w, MsgError, "Unsupported key file", err.Error())
			return
		}
	}
	p.Timeout = m.S.Timeout()
	p.HostKey = m.checkHostKey
	p.CertCheck = m.checkCert
	p.Log = m.log.Protocol("")
	req.params = p
	m.lastParams = req

	if m.sess != nil {
		old := m.sess
		m.sess = nil
		m.setSync(false)
		go old.close()
		m.remote.showDisconnected()
	}
	m.connGen++
	gen := m.connGen
	m.connecting = true
	m.setStatus(stBusy, "Connecting to %s:%d (%s)…", p.Host, p.Port, remote.ProtocolLabel(p.Protocol))
	m.log.Add(core.LogInfo, "Connecting to %s:%d using %s…", p.Host, p.Port, remote.ProtocolLabel(p.Protocol))
	m.qcConnect.Disabled = true
	m.qcConnect.Text = "Connecting…"
	m.layout()
	go func() {
		fs, err := remote.Connect(p)
		m.app.Post(func() {
			m.qcConnect.Disabled = false
			m.qcConnect.Text = "Connect"
			if gen != m.connGen {
				if fs != nil {
					go fs.Close()
				}
				return
			}
			m.connecting = false
			if err != nil {
				msg := friendlyErr(err)
				m.log.Add(core.LogError, "Could not connect to %s: %s", p.Host, msg)
				m.setStatus(stError, "Connection failed: %s", msg)
				m.layout()
				m.app.Alert(m.w, MsgError, "Could not connect to "+p.Host, msg)
				return
			}
			m.sess = newSession(m.app, p, fs, req.label, req.site, m.log)
			m.qcMode = false
			m.log.Add(core.LogSuccess, "Connected to %s (%s)", p.Host, fs.Description())
			m.setStatus(stOK, "Connected to %s (%s)", p.Host, fs.Description())
			m.layout()
			start := fs.Home()
			if req.site != nil && req.site.RemoteDir != "" {
				start = req.site.RemoteDir
			} else if m.S.UploadDir != "" {
				start = m.S.UploadDir
			}
			m.remote.hist, m.remote.fut = nil, nil
			m.remote.navigate(start, true, navRefresh)
			if req.site != nil && req.site.LocalDir != "" {
				if st, err := os.Stat(req.site.LocalDir); err == nil && st.IsDir() {
					m.local.navigate(req.site.LocalDir, true, navRefresh)
				}
			}
			if m.S.SyncByDefault {
				m.pendingSync = true
			}
			m.w.SetFocusWidget(m.remote.list)
			m.active = m.remote
		})
	}()
}

func (m *MainWindow) disconnect() {
	if m.sess == nil {
		return
	}
	s := m.sess
	m.sess = nil
	m.connGen++
	m.setSync(false)
	go s.close()
	m.log.Add(core.LogInfo, "Disconnected from %s", s.params.Host)
	m.setStatus(stIdle, "Disconnected from %s", s.params.Host)
	m.remote.showDisconnected()
	m.qcMode = false
	m.layout()
}

func hostKeyID(host string, port int) string {
	return strings.ToLower(host) + ":" + strconv.Itoa(port)
}

// checkHostKey runs on the connecting goroutine and asks the user on the UI thread.
func (m *MainWindow) checkHostKey(host string, port int, keyType string, blob []byte) error {
	id := hostKeyID(host, port)
	fp := keyType + " " + sshc.Fingerprint(blob)
	var known, trusted string
	m.app.Call(func() {
		known = m.trust.HostKeys[id]
		trusted = m.sessionTrust[id]
	})
	if known == fp || trusted == fp {
		return nil
	}
	res := -1
	m.app.Call(func() {
		if known == "" {
			res, _ = m.app.Message(m.w, MessageOpts{
				Kind:    MsgSecurity,
				Title:   "Unknown server key",
				Heading: "First connection to " + host,
				Text: "ShoreDrop has not seen this server before, so it can't confirm the server is who it claims to be. " +
					"If you can, compare the fingerprint below with the one your hosting provider publishes.",
				Detail:  "Server:  " + id + "\nType:    " + keyType + "\nSHA256:  " + strings.TrimPrefix(sshc.Fingerprint(blob), "SHA256:"),
				Buttons: []string{"Cancel", "Connect once", "Trust and connect"},
				Primary: 2,
			})
		} else {
			res, _ = m.app.Message(m.w, MessageOpts{
				Kind:    MsgError,
				Title:   "Server key changed",
				Heading: "Warning: the server's identity has changed",
				Text: "The key presented by " + host + " is different from the one saved earlier. This happens when a server is " +
					"reinstalled — but it can also mean someone is intercepting your connection. Only continue if you know why the key changed.",
				Detail:  "Saved:  " + known + "\nNow:    " + fp,
				Buttons: []string{"Cancel", "Connect once", "Update saved key"},
				Primary: 0,
				Danger:  true,
			})
		}
		switch res {
		case 2:
			m.trust.HostKeys[id] = fp
			m.trust.Save()
			m.log.Add(core.LogInfo, "Saved host key for %s (%s)", id, fp)
		case 1:
			m.sessionTrust[id] = fp
		}
	})
	if res < 1 {
		return errors.New("host key not accepted")
	}
	return nil
}

func (m *MainWindow) checkCert(host string, port int, cs tls.ConnectionState, verr error) error {
	id := hostKeyID(host, port)
	leaf := cs.PeerCertificates[0]
	sum := sha256.Sum256(leaf.Raw)
	fp := enchex.EncodeToString(sum[:])
	var known, trusted string
	m.app.Call(func() { known = m.trust.Certs[id]; trusted = m.sessionTrust["cert:"+id] })
	if known == fp || trusted == fp {
		return nil
	}
	res := -1
	m.app.Call(func() {
		pretty := strings.ToUpper(fp)
		var parts []string
		for i := 0; i < len(pretty); i += 2 {
			parts = append(parts, pretty[i:i+2])
		}
		head := "The server's certificate could not be verified"
		if known != "" {
			head = "The server's certificate has changed"
		}
		res, _ = m.app.Message(m.w, MessageOpts{
			Kind:    MsgSecurity,
			Title:   "Certificate not trusted",
			Heading: head,
			Text: "The connection is encrypted, but ShoreDrop can't confirm the identity of " + host + ". " +
				"This is common for self-signed certificates on private servers. Only continue if you trust this server.",
			Detail: fmt.Sprintf("Subject:  %s\nIssuer:   %s\nValid:    %s – %s\nSHA-256:  %s\nReason:   %v",
				leaf.Subject.CommonName, leaf.Issuer.CommonName, leaf.NotBefore.Format("2006-01-02"), leaf.NotAfter.Format("2006-01-02"),
				strings.Join(parts[:16], ":")+"…", verr),
			Buttons: []string{"Cancel", "Connect once", "Always trust"},
			Primary: 0,
		})
		switch res {
		case 2:
			m.trust.Certs[id] = fp
			m.trust.Save()
		case 1:
			m.sessionTrust["cert:"+id] = fp
		}
	})
	if res < 1 {
		return errors.New("certificate not accepted")
	}
	return nil
}

// ---------------------------------------------------------------- synchronized browsing

func (m *MainWindow) setSync(on bool) {
	if on && !m.connected() {
		on = false
	}
	m.syncOn = on
	m.syncToggle.On = on
	if on {
		m.syncLocal, m.syncRemote = m.local.dir, m.remote.dir
		m.log.Add(core.LogInfo, "Synchronized browsing on: %s ⇄ %s", m.syncLocal, m.syncRemote)
		m.setStatus(stOK, "Synchronized browsing on — local and remote folders follow each other")
	} else if m.sess != nil {
		m.setStatus(stOK, "Synchronized browsing off")
	}
	m.local.list.ShowBadges = on
	m.remote.list.ShowBadges = on
	m.updateBadges()
	m.w.Invalidate()
}

// syncFollow makes the other pane follow a navigation in p.
func (m *MainWindow) syncFollow(p *Pane) {
	if !m.syncOn || m.local.dir == "" {
		return
	}
	if p.isRemote {
		rel, ok := relRemote(m.syncRemote, p.dir)
		if !ok {
			m.setStatus(stWarn, "Synchronized browsing: %s is outside the synchronized folder %s", p.dir, m.syncRemote)
			return
		}
		target := filepath.Join(m.syncLocal, filepath.FromSlash(rel))
		if target != m.local.dir {
			m.local.navigate(target, true, navSync)
		}
		return
	}
	rel, err := filepath.Rel(m.syncLocal, p.dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		m.setStatus(stWarn, "Synchronized browsing: %s is outside the synchronized folder %s", p.dir, m.syncLocal)
		return
	}
	target := path.Join(m.syncRemote, filepath.ToSlash(rel))
	if target != m.remote.dir {
		m.remote.navigate(target, true, navSync)
	}
}

func relRemote(base, p string) (string, bool) {
	base = strings.TrimSuffix(base, "/")
	if p == base || base == "" && p == "/" {
		return ".", true
	}
	if base == "" {
		return strings.TrimPrefix(p, "/"), true
	}
	if strings.HasPrefix(p, base+"/") {
		return p[len(base)+1:], true
	}
	return "", false
}

// updateBadges compares the two panes by name and size.
func (m *MainWindow) updateBadges() {
	if !m.syncOn {
		return
	}
	lrows, rrows := m.local.list.AllRows(), m.remote.list.AllRows()
	li := map[string]FileRow{}
	ri := map[string]FileRow{}
	for _, r := range lrows {
		li[strings.ToLower(r.Name)] = r
	}
	for _, r := range rrows {
		ri[strings.ToLower(r.Name)] = r
	}
	badge := func(a FileRow, other map[string]FileRow) int {
		b, ok := other[strings.ToLower(a.Name)]
		switch {
		case !ok:
			return BadgeOnlyHere
		case a.IsDir && b.IsDir:
			return BadgeInSync
		case a.IsDir != b.IsDir || a.Size != b.Size:
			return BadgeModified
		}
		return BadgeInSync
	}
	for i := range lrows {
		lrows[i].Badge = badge(lrows[i], ri)
	}
	for i := range rrows {
		rrows[i].Badge = badge(rrows[i], li)
	}
	m.local.list.refreshView()
	m.remote.list.refreshView()
}

var _ = time.Now
