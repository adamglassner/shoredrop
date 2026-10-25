// Package ftpc is a dependency-free FTP client supporting plain FTP,
// explicit FTPS (AUTH TLS) and implicit FTPS, in passive or active mode.
package ftpc

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Security selects how the connection is protected.
type Security int

const (
	Plain Security = iota
	ExplicitTLS
	ImplicitTLS
)

// Config holds connection parameters.
type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	Security Security
	Passive  bool
	Timeout  time.Duration
	// TLS is used for FTPS; ServerName is filled in automatically.
	TLS *tls.Config
	// Log receives protocol traffic (passwords are masked).
	Log func(dir, line string)
}

// Error is an FTP error reply.
type Error struct {
	Code int
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("%d %s", e.Code, e.Msg) }

// Conn is a logged-in FTP control connection.
type Conn struct {
	cfg  Config
	conn net.Conn
	tp   *textproto.Conn
	mu   sync.Mutex
	tls  bool

	quiet bool
	dmu   sync.Mutex
	data  net.Conn // current data connection, closed by Abort

	features map[string]string
	mlsd     bool
	epsv     bool
	utf8     bool
	sessions tls.ClientSessionCache

	TLSVersion string
	Welcome    string
}

func (c *Conn) log(dir, line string) {
	if c.cfg.Log != nil && !c.quiet {
		c.cfg.Log(dir, line)
	}
}

// Dial connects and logs in.
func Dial(cfg Config) (*Conn, error) {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.Port == 0 {
		if cfg.Security == ImplicitTLS {
			cfg.Port = 990
		} else {
			cfg.Port = 21
		}
	}
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	raw, err := net.DialTimeout("tcp", addr, cfg.Timeout)
	if err != nil {
		return nil, err
	}
	c := &Conn{cfg: cfg, conn: raw, sessions: tls.NewLRUClientSessionCache(16)}
	raw.SetDeadline(time.Now().Add(cfg.Timeout))
	if cfg.Security == ImplicitTLS {
		tc := tls.Client(raw, c.tlsConfig())
		if err := tc.Handshake(); err != nil {
			raw.Close()
			return nil, fmt.Errorf("TLS handshake failed: %w", err)
		}
		c.conn = tc
		c.tls = true
		c.TLSVersion = tlsVersionName(tc.ConnectionState().Version)
	}
	c.tp = textproto.NewConn(c.conn)
	code, msg, err := c.readReply()
	if err != nil {
		c.conn.Close()
		return nil, fmt.Errorf("no FTP greeting from server (is this an FTP port?): %w", err)
	}
	if code != 220 {
		c.conn.Close()
		return nil, &Error{code, msg}
	}
	c.Welcome = msg
	if err := c.login(); err != nil {
		c.conn.Close()
		return nil, err
	}
	c.conn.SetDeadline(time.Time{})
	return c, nil
}

func tlsVersionName(v uint16) string {
	switch v {
	case tls.VersionTLS13:
		return "TLS 1.3"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS10:
		return "TLS 1.0"
	}
	return "TLS"
}

func (c *Conn) tlsConfig() *tls.Config {
	var t *tls.Config
	if c.cfg.TLS != nil {
		t = c.cfg.TLS.Clone()
	} else {
		t = &tls.Config{}
	}
	if t.ServerName == "" {
		t.ServerName = c.cfg.Host
	}
	t.ClientSessionCache = c.sessions
	if t.MinVersion == 0 {
		t.MinVersion = tls.VersionTLS12
	}
	return t
}

func (c *Conn) login() error {
	if c.cfg.Security == ExplicitTLS {
		code, msg, err := c.cmd("AUTH TLS")
		if err != nil {
			return err
		}
		if code != 234 {
			return fmt.Errorf("server does not support FTPS (AUTH TLS refused: %d %s)", code, msg)
		}
		tc := tls.Client(c.conn, c.tlsConfig())
		if err := tc.Handshake(); err != nil {
			return fmt.Errorf("TLS handshake failed: %w", err)
		}
		c.conn = tc
		c.tp = textproto.NewConn(tc)
		c.tls = true
		c.TLSVersion = tlsVersionName(tc.ConnectionState().Version)
	}
	user := c.cfg.User
	if user == "" {
		user = "anonymous"
	}
	code, msg, err := c.cmd("USER " + user)
	if err != nil {
		return err
	}
	if code == 331 || code == 332 {
		pass := c.cfg.Password
		if pass == "" && user == "anonymous" {
			pass = "anonymous@"
		}
		code, msg, err = c.cmd("PASS " + pass)
		if err != nil {
			return err
		}
	}
	if code != 230 && code != 202 {
		return &Error{code, msg}
	}
	if c.tls {
		c.cmd("PBSZ 0")
		if code, msg, err := c.cmd("PROT P"); err != nil {
			return err
		} else if code != 200 {
			return fmt.Errorf("server refused encrypted data connections: %d %s", code, msg)
		}
	}
	c.features = map[string]string{}
	if code, msg, err := c.cmd("FEAT"); err == nil && code == 211 {
		for _, l := range strings.Split(msg, "\n") {
			l = strings.TrimSpace(l)
			if l == "" || strings.HasPrefix(l, "Features") || strings.HasPrefix(l, "End") || strings.HasPrefix(l, "Extensions") {
				continue
			}
			k, v, _ := strings.Cut(l, " ")
			c.features[strings.ToUpper(k)] = v
		}
	}
	_, c.mlsd = c.features["MLSD"]
	_, c.epsv = c.features["EPSV"]
	if _, ok := c.features["UTF8"]; ok {
		c.utf8 = true
		c.cmd("OPTS UTF8 ON")
	}
	if code, msg, err := c.cmd("TYPE I"); err != nil {
		return err
	} else if code != 200 {
		return &Error{code, msg}
	}
	return nil
}

func (c *Conn) readReply() (int, string, error) {
	line, err := c.tp.ReadLine()
	if err != nil {
		return 0, "", err
	}
	if len(line) < 4 {
		return 0, "", fmt.Errorf("malformed FTP reply %q", line)
	}
	code, err := strconv.Atoi(line[:3])
	if err != nil {
		return 0, "", fmt.Errorf("malformed FTP reply %q", line)
	}
	c.log("<", line)
	msg := line[4:]
	if line[3] == '-' {
		var b strings.Builder
		b.WriteString(msg)
		end := line[:3] + " "
		for {
			l, err := c.tp.ReadLine()
			if err != nil {
				return 0, "", err
			}
			c.log("<", l)
			if strings.HasPrefix(l, end) {
				b.WriteString("\n" + l[4:])
				break
			}
			b.WriteString("\n" + l)
		}
		msg = b.String()
	}
	return code, msg, nil
}

func (c *Conn) send(line string) error {
	shown := line
	if strings.HasPrefix(line, "PASS ") {
		shown = "PASS ********"
	}
	c.log(">", shown)
	return c.tp.PrintfLine("%s", line)
}

func (c *Conn) cmd(line string) (int, string, error) {
	if err := c.send(line); err != nil {
		return 0, "", err
	}
	return c.readReply()
}

// expect runs a command and requires a reply code in the given class (2 = 2xx).
func (c *Conn) expect(line string, class int) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.conn.SetDeadline(time.Now().Add(c.cfg.Timeout))
	defer c.conn.SetDeadline(time.Time{})
	code, msg, err := c.cmd(line)
	if err != nil {
		return "", err
	}
	if code/100 != class {
		return "", &Error{code, msg}
	}
	return msg, nil
}

// Close logs out.
func (c *Conn) Close() error {
	c.mu.Lock()
	c.conn.SetDeadline(time.Now().Add(3 * time.Second))
	c.send("QUIT")
	c.mu.Unlock()
	return c.conn.Close()
}

// Abort closes the connection immediately (used to cancel transfers).
func (c *Conn) Abort() {
	c.dmu.Lock()
	if c.data != nil {
		c.data.Close()
	}
	c.dmu.Unlock()
	c.conn.Close()
}

func (c *Conn) setData(dc net.Conn) {
	c.dmu.Lock()
	c.data = dc
	c.dmu.Unlock()
}

// Pwd returns the current directory.
func (c *Conn) Pwd() (string, error) {
	msg, err := c.expect("PWD", 2)
	if err != nil {
		return "", err
	}
	if i := strings.IndexByte(msg, '"'); i >= 0 {
		rest := msg[i+1:]
		var b strings.Builder
		for j := 0; j < len(rest); j++ {
			if rest[j] == '"' {
				if j+1 < len(rest) && rest[j+1] == '"' {
					b.WriteByte('"')
					j++
					continue
				}
				break
			}
			b.WriteByte(rest[j])
		}
		return b.String(), nil
	}
	return "/", nil
}

func (c *Conn) Cwd(p string) error   { _, err := c.expect("CWD "+p, 2); return err }
func (c *Conn) Mkdir(p string) error { _, err := c.expect("MKD "+p, 2); return err }
func (c *Conn) Rmdir(p string) error { _, err := c.expect("RMD "+p, 2); return err }
func (c *Conn) Delete(p string) error {
	_, err := c.expect("DELE "+p, 2)
	return err
}
func (c *Conn) Rename(from, to string) error {
	if _, err := c.expect("RNFR "+from, 3); err != nil {
		return err
	}
	_, err := c.expect("RNTO "+to, 2)
	return err
}
func (c *Conn) Chmod(p string, mode uint32) error {
	_, err := c.expect(fmt.Sprintf("SITE CHMOD %03o %s", mode&0o7777, p), 2)
	return err
}

// Size returns the size of a file (SIZE command).
func (c *Conn) Size(p string) (int64, error) {
	msg, err := c.expect("SIZE "+p, 2)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(msg), 10, 64)
}

// Noop keeps the connection alive.
func (c *Conn) Noop() error {
	c.quiet = true
	_, err := c.expect("NOOP", 2)
	c.quiet = false
	return err
}

// openData prepares a data connection and issues the transfer command.
// The returned connection must be passed to finishData.
func (c *Conn) openData(command string, offset int64) (net.Conn, error) {
	c.conn.SetDeadline(time.Now().Add(c.cfg.Timeout))
	defer c.conn.SetDeadline(time.Time{})
	if offset > 0 {
		code, msg, err := c.cmd(fmt.Sprintf("REST %d", offset))
		if err != nil {
			return nil, err
		}
		if code != 350 {
			return nil, fmt.Errorf("server cannot resume transfers: %d %s", code, msg)
		}
	}
	var dc net.Conn
	var ln net.Listener
	if c.cfg.Passive {
		addr, err := c.passiveAddr()
		if err != nil {
			return nil, err
		}
		dc, err = net.DialTimeout("tcp", addr, c.cfg.Timeout)
		if err != nil {
			return nil, fmt.Errorf("could not open data connection to %s: %w", addr, err)
		}
	} else {
		var err error
		ln, err = c.activeListen()
		if err != nil {
			return nil, err
		}
		defer ln.Close()
	}
	code, msg, err := c.cmd(command)
	if err != nil {
		if dc != nil {
			dc.Close()
		}
		return nil, err
	}
	if code != 125 && code != 150 {
		if dc != nil {
			dc.Close()
		}
		return nil, &Error{code, msg}
	}
	if ln != nil {
		ln.(*net.TCPListener).SetDeadline(time.Now().Add(c.cfg.Timeout))
		dc, err = ln.Accept()
		if err != nil {
			return nil, fmt.Errorf("server did not open the active-mode data connection (try passive mode): %w", err)
		}
	}
	if c.tls {
		tc := tls.Client(dc, c.tlsConfig())
		tc.SetDeadline(time.Now().Add(c.cfg.Timeout))
		if err := tc.Handshake(); err != nil {
			dc.Close()
			return nil, fmt.Errorf("TLS handshake on data connection failed: %w", err)
		}
		tc.SetDeadline(time.Time{})
		dc = tc
	}
	c.setData(dc)
	return dc, nil
}

func (c *Conn) passiveAddr() (string, error) {
	host, _, _ := net.SplitHostPort(c.conn.RemoteAddr().String())
	if c.epsv {
		code, msg, err := c.cmd("EPSV")
		if err != nil {
			return "", err
		}
		if code == 229 {
			// (|||port|)
			if i := strings.Index(msg, "(|||"); i >= 0 {
				rest := msg[i+4:]
				if j := strings.IndexByte(rest, '|'); j > 0 {
					return net.JoinHostPort(host, rest[:j]), nil
				}
			}
		}
		c.epsv = false
	}
	code, msg, err := c.cmd("PASV")
	if err != nil {
		return "", err
	}
	if code != 227 {
		return "", &Error{code, msg}
	}
	start := strings.IndexByte(msg, '(')
	end := strings.LastIndexByte(msg, ')')
	var nums string
	if start >= 0 && end > start {
		nums = msg[start+1 : end]
	} else {
		// some servers omit the parentheses
		f := strings.FieldsFunc(msg, func(r rune) bool { return r == ' ' })
		nums = f[len(f)-1]
	}
	parts := strings.Split(nums, ",")
	if len(parts) != 6 {
		return "", fmt.Errorf("could not parse PASV reply %q", msg)
	}
	p1, _ := strconv.Atoi(strings.TrimSpace(parts[4]))
	p2, _ := strconv.Atoi(strings.TrimSpace(parts[5]))
	ip := net.ParseIP(strings.Join(parts[:4], "."))
	// Servers behind NAT often report a private address; use the control
	// connection's address in that case (as most clients do).
	useHost := host
	if ip != nil && !ip.IsPrivate() && !ip.IsUnspecified() && !ip.IsLoopback() {
		if cip := net.ParseIP(host); cip == nil || !cip.IsPrivate() {
			useHost = ip.String()
		}
	}
	return net.JoinHostPort(useHost, strconv.Itoa(p1<<8|p2)), nil
}

func (c *Conn) activeListen() (net.Listener, error) {
	local, _, _ := net.SplitHostPort(c.conn.LocalAddr().String())
	ln, err := net.Listen("tcp", net.JoinHostPort(local, "0"))
	if err != nil {
		return nil, err
	}
	_, ps, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(ps)
	ip := net.ParseIP(local)
	var code int
	var msg string
	if ip4 := ip.To4(); ip4 != nil {
		code, msg, err = c.cmd(fmt.Sprintf("PORT %d,%d,%d,%d,%d,%d", ip4[0], ip4[1], ip4[2], ip4[3], port>>8, port&0xff))
	} else {
		code, msg, err = c.cmd(fmt.Sprintf("EPRT |2|%s|%d|", local, port))
	}
	if err != nil {
		ln.Close()
		return nil, err
	}
	if code != 200 {
		ln.Close()
		return nil, &Error{code, msg}
	}
	return ln, nil
}

// finishData closes the data connection and reads the completion reply.
func (c *Conn) finishData(dc net.Conn) error {
	// Half-close and wait for the server to finish, so unread bytes (such as
	// TLS 1.3 session tickets) don't turn our close into a TCP reset that
	// could make the server discard the end of an upload.
	switch t := dc.(type) {
	case *tls.Conn:
		t.CloseWrite()
	case *net.TCPConn:
		t.CloseWrite()
	}
	dc.SetReadDeadline(time.Now().Add(c.cfg.Timeout))
	io.Copy(io.Discard, dc)
	dc.Close()
	c.setData(nil)
	c.conn.SetDeadline(time.Now().Add(c.cfg.Timeout))
	defer c.conn.SetDeadline(time.Time{})
	code, msg, err := c.readReply()
	if err != nil {
		return err
	}
	if code/100 != 2 {
		return &Error{code, msg}
	}
	return nil
}

// Entry is a parsed directory entry.
type Entry struct {
	Name    string
	Size    int64
	ModTime time.Time
	IsDir   bool
	IsLink  bool
	Perm    uint32 // permission bits if known
	PermStr string // e.g. "drwxr-xr-x" or raw MLSD perm facts
	HasPerm bool
}

// List returns the entries of a directory.
func (c *Conn) List(dir string) ([]Entry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.conn.SetDeadline(time.Now().Add(c.cfg.Timeout))
	code, msg, err := c.cmd("CWD " + dir)
	c.conn.SetDeadline(time.Time{})
	if err != nil {
		return nil, err
	}
	if code/100 != 2 {
		return nil, &Error{code, msg}
	}
	cmd := "LIST -a"
	if c.mlsd {
		cmd = "MLSD"
	}
	dc, err := c.openData(cmd, 0)
	if err != nil {
		var fe *Error
		if !c.mlsd && errors.As(err, &fe) && fe.Code/100 == 5 {
			// some servers reject "-a"
			dc, err = c.openData("LIST", 0)
		}
		if err != nil {
			return nil, err
		}
	}
	data, rerr := io.ReadAll(bufio.NewReader(dc))
	if err := c.finishData(dc); err != nil {
		return nil, err
	}
	if rerr != nil {
		return nil, rerr
	}
	var out []Entry
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if line == "" {
			continue
		}
		var e Entry
		var ok bool
		if c.mlsd {
			e, ok = parseMLSD(line)
		} else {
			e, ok = parseLIST(line, time.Now())
		}
		if ok && e.Name != "." && e.Name != ".." {
			out = append(out, e)
		}
	}
	return out, nil
}

// Retr downloads p starting at offset, writing into w.
func (c *Conn) Retr(p string, offset int64, w io.Writer, progress func(int)) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	dc, err := c.openData("RETR "+p, offset)
	if err != nil {
		return err
	}
	_, cerr := copyProgress(w, dc, progress)
	ferr := c.finishData(dc)
	if cerr != nil {
		return cerr
	}
	return ferr
}

// Stor uploads r to p. If offset > 0 the upload is appended via REST+STOR.
func (c *Conn) Stor(p string, offset int64, r io.Reader, progress func(int)) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cmd := "STOR " + p
	var dc net.Conn
	var err error
	if offset > 0 {
		dc, err = c.openData(cmd, offset)
		if err != nil {
			// fall back to APPE when REST STOR is not supported
			dc, err = c.openData("APPE "+p, 0)
		}
	} else {
		dc, err = c.openData(cmd, 0)
	}
	if err != nil {
		return err
	}
	_, cerr := copyProgress(dc, r, progress)
	ferr := c.finishData(dc)
	if cerr != nil {
		return cerr
	}
	return ferr
}

func copyProgress(dst io.Writer, src io.Reader, progress func(int)) (int64, error) {
	buf := make([]byte, 64*1024)
	var total int64
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return total, werr
			}
			total += int64(n)
			if progress != nil {
				progress(n)
			}
		}
		if err == io.EOF {
			return total, nil
		}
		if err != nil {
			return total, err
		}
	}
}

// Join builds a remote path.
func Join(dir, name string) string { return path.Join(dir, name) }
