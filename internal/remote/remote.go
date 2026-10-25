// Package remote gives FTP, FTPS and SFTP a single file-system interface.
package remote

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"shoredrop/internal/ftpc"
	"shoredrop/internal/sftp"
	"shoredrop/internal/sshc"
)

// Protocol identifiers.
const (
	SFTP         = "sftp"
	FTP          = "ftp"
	FTPS         = "ftps"          // explicit (AUTH TLS)
	FTPSImplicit = "ftps-implicit" // port 990
)

// ProtocolLabel returns a short display name.
func ProtocolLabel(p string) string {
	switch p {
	case SFTP:
		return "SFTP"
	case FTPS:
		return "FTPS"
	case FTPSImplicit:
		return "FTPS (implicit)"
	}
	return "FTP"
}

// DefaultPort returns the standard port for a protocol.
func DefaultPort(p string) int {
	switch p {
	case SFTP:
		return 22
	case FTPSImplicit:
		return 990
	}
	return 21
}

// Entry describes a remote file.
type Entry struct {
	Name    string
	Size    int64
	ModTime time.Time
	IsDir   bool
	IsLink  bool
	Perm    uint32
	HasPerm bool
	PermStr string
}

// Params describe how to connect.
type Params struct {
	Protocol      string
	Host          string
	Port          int
	User          string
	Password      string
	KeyFile       string
	KeyPassphrase string
	Passive       bool
	Timeout       time.Duration
	// HostKey verifies SSH host keys (fingerprint shown to the user).
	HostKey func(host string, port int, keyType string, blob []byte) error
	// CertCheck is called for FTPS certificates that fail normal
	// verification; returning nil accepts the certificate.
	CertCheck func(host string, port int, cs tls.ConnectionState, verifyErr error) error
	// Log receives protocol traffic.
	Log func(dir, line string)
}

// FS is a connected remote file system.
type FS interface {
	Home() string
	List(dir string) ([]Entry, error)
	Stat(p string) (Entry, error)
	Mkdir(p string) error
	Remove(p string) error
	Rmdir(p string) error
	Rename(from, to string) error
	Chmod(p string, mode uint32) error
	Download(p string, offset int64, w io.Writer, stop <-chan struct{}, progress func(int)) error
	Upload(p string, offset int64, r io.Reader, stop <-chan struct{}, progress func(int)) error
	KeepAlive() error
	Close() error
	Abort()
	Description() string
}

// ErrNotExist is returned by Stat for missing paths.
var ErrNotExist = os.ErrNotExist

// Connect opens a connection according to p.
func Connect(p Params) (FS, error) {
	if p.Timeout <= 0 {
		p.Timeout = 30 * time.Second
	}
	if p.Port == 0 {
		p.Port = DefaultPort(p.Protocol)
	}
	host := strings.TrimSpace(p.Host)
	for _, pre := range []string{"sftp://", "ftps://", "ftp://"} {
		host = strings.TrimPrefix(host, pre)
	}
	host = strings.TrimSuffix(host, "/")
	p.Host = host
	if host == "" {
		return nil, errors.New("enter a host name")
	}
	switch p.Protocol {
	case SFTP:
		return dialSFTP(p)
	case FTP, FTPS, FTPSImplicit:
		return dialFTP(p)
	}
	return nil, fmt.Errorf("unknown protocol %q", p.Protocol)
}

// ---------------- SFTP ----------------

type sftpFS struct {
	ssh  *sshc.Client
	ch   *sshc.Channel
	c    *sftp.Client
	home string
	desc string
}

func dialSFTP(p Params) (FS, error) {
	cfg := &sshc.Config{User: p.User, Password: p.Password, Timeout: p.Timeout}
	if p.KeyFile != "" {
		data, err := os.ReadFile(p.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("cannot read key file: %w", err)
		}
		s, err := sshc.ParsePrivateKey(data, p.KeyPassphrase)
		if err != nil {
			return nil, err
		}
		cfg.Signer = s
	}
	if p.HostKey != nil {
		cfg.HostKey = func(kt string, blob []byte) error { return p.HostKey(p.Host, p.Port, kt, blob) }
	}
	if p.Log != nil {
		cfg.Banner = func(s string) { p.Log("<", strings.TrimSpace(s)) }
	}
	addr := net.JoinHostPort(p.Host, strconv.Itoa(p.Port))
	cl, err := sshc.Dial(addr, cfg)
	if err != nil {
		return nil, err
	}
	if p.Log != nil {
		p.Log("<", "Server: "+cl.ServerVersion)
		p.Log("<", "Using "+cl.Algorithms)
	}
	ch, err := cl.OpenSession()
	if err != nil {
		cl.Close()
		return nil, err
	}
	if err := ch.RequestSubsystem("sftp"); err != nil {
		cl.Close()
		return nil, err
	}
	sc, err := sftp.NewClient(ch)
	if err != nil {
		cl.Close()
		return nil, err
	}
	home, err := sc.RealPath(".")
	if err != nil || home == "" {
		home = "/"
	}
	return &sftpFS{ssh: cl, ch: ch, c: sc, home: home, desc: "SFTP"}, nil
}

func (s *sftpFS) Home() string        { return s.home }
func (s *sftpFS) Description() string { return s.desc }
func (s *sftpFS) KeepAlive() error    { return s.ssh.Err() }
func (s *sftpFS) Close() error {
	s.c.Close()
	return s.ssh.Close()
}
func (s *sftpFS) Abort() { s.ssh.Close() }

func convAttrs(name string, a sftp.Attrs) Entry {
	e := Entry{Name: name, Size: int64(a.Size), ModTime: a.ModTime(), IsDir: a.IsDir(), IsLink: a.IsLink()}
	if a.Flags&0x4 != 0 {
		e.Perm = a.Perm & 0o7777
		e.HasPerm = true
		e.PermStr = ftpc.PermString(e.Perm, e.IsDir, e.IsLink)
	}
	return e
}

func (s *sftpFS) List(dir string) ([]Entry, error) {
	list, err := s.c.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(list))
	for _, it := range list {
		e := convAttrs(it.Name, it.Attrs)
		if e.IsLink {
			// Follow symlinks so linked directories can be browsed.
			if a, err := s.c.Stat(path.Join(dir, it.Name)); err == nil {
				t := convAttrs(it.Name, a)
				e.IsDir = t.IsDir
				if !t.IsDir {
					e.Size = t.Size
				}
			}
		}
		out = append(out, e)
	}
	return out, nil
}

func (s *sftpFS) Stat(p string) (Entry, error) {
	a, err := s.c.Stat(p)
	if err != nil {
		if sftp.IsNotExist(err) {
			return Entry{}, ErrNotExist
		}
		return Entry{}, err
	}
	return convAttrs(path.Base(p), a), nil
}

func (s *sftpFS) Mkdir(p string) error              { return s.c.Mkdir(p) }
func (s *sftpFS) Remove(p string) error             { return s.c.Remove(p) }
func (s *sftpFS) Rmdir(p string) error              { return s.c.Rmdir(p) }
func (s *sftpFS) Chmod(p string, mode uint32) error { return s.c.Chmod(p, mode) }
func (s *sftpFS) Rename(from, to string) error      { return s.c.Rename(from, to) }

func (s *sftpFS) Download(p string, offset int64, w io.Writer, stop <-chan struct{}, progress func(int)) error {
	f, err := s.c.OpenRead(p)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.ReadTo(w, offset, stop, progress)
}

func (s *sftpFS) Upload(p string, offset int64, r io.Reader, stop <-chan struct{}, progress func(int)) error {
	f, err := s.c.OpenWrite(p, offset == 0)
	if err != nil {
		return err
	}
	werr := f.WriteFrom(r, offset, stop, progress)
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	return cerr
}

// ---------------- FTP / FTPS ----------------

type ftpFS struct {
	c    *ftpc.Conn
	home string
	desc string
}

func dialFTP(p Params) (FS, error) {
	cfg := ftpc.Config{Host: p.Host, Port: p.Port, User: p.User, Password: p.Password, Passive: p.Passive, Timeout: p.Timeout, Log: p.Log}
	switch p.Protocol {
	case FTPS:
		cfg.Security = ftpc.ExplicitTLS
	case FTPSImplicit:
		cfg.Security = ftpc.ImplicitTLS
	}
	if cfg.Security != ftpc.Plain {
		host, port := p.Host, p.Port
		cfg.TLS = &tls.Config{
			InsecureSkipVerify: true, // verified below, with a user prompt for untrusted certificates
			VerifyConnection: func(cs tls.ConnectionState) error {
				if len(cs.PeerCertificates) == 0 {
					return errors.New("server sent no certificate")
				}
				opts := x509.VerifyOptions{DNSName: host, Intermediates: x509.NewCertPool()}
				for _, c := range cs.PeerCertificates[1:] {
					opts.Intermediates.AddCert(c)
				}
				_, verr := cs.PeerCertificates[0].Verify(opts)
				if verr == nil {
					return nil
				}
				if cs.DidResume {
					return nil // already accepted for this session
				}
				if p.CertCheck != nil {
					return p.CertCheck(host, port, cs, verr)
				}
				return verr
			},
		}
	}
	c, err := ftpc.Dial(cfg)
	if err != nil {
		return nil, err
	}
	home, err := c.Pwd()
	if err != nil || home == "" {
		home = "/"
	}
	desc := "FTP"
	if c.TLSVersion != "" {
		desc = "FTPS • " + c.TLSVersion
	}
	return &ftpFS{c: c, home: home, desc: desc}, nil
}

func (f *ftpFS) Home() string        { return f.home }
func (f *ftpFS) Description() string { return f.desc }
func (f *ftpFS) KeepAlive() error    { return f.c.Noop() }
func (f *ftpFS) Close() error        { return f.c.Close() }
func (f *ftpFS) Abort()              { f.c.Abort() }

func (f *ftpFS) List(dir string) ([]Entry, error) {
	list, err := f.c.List(dir)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(list))
	for _, e := range list {
		out = append(out, Entry{Name: e.Name, Size: e.Size, ModTime: e.ModTime, IsDir: e.IsDir, IsLink: e.IsLink, Perm: e.Perm, HasPerm: e.HasPerm, PermStr: e.PermStr})
	}
	return out, nil
}

func (f *ftpFS) Stat(p string) (Entry, error) {
	if n, err := f.c.Size(p); err == nil {
		return Entry{Name: path.Base(p), Size: n}, nil
	}
	// Not a file (or SIZE unsupported) – check for a directory via its parent listing.
	parent, name := path.Dir(p), path.Base(p)
	list, err := f.c.List(parent)
	if err != nil {
		return Entry{}, ErrNotExist
	}
	for _, e := range list {
		if e.Name == name {
			return Entry{Name: e.Name, Size: e.Size, ModTime: e.ModTime, IsDir: e.IsDir, IsLink: e.IsLink, Perm: e.Perm, HasPerm: e.HasPerm, PermStr: e.PermStr}, nil
		}
	}
	return Entry{}, ErrNotExist
}

func (f *ftpFS) Mkdir(p string) error              { return f.c.Mkdir(p) }
func (f *ftpFS) Remove(p string) error             { return f.c.Delete(p) }
func (f *ftpFS) Rmdir(p string) error              { return f.c.Rmdir(p) }
func (f *ftpFS) Rename(a, b string) error          { return f.c.Rename(a, b) }
func (f *ftpFS) Chmod(p string, mode uint32) error { return f.c.Chmod(p, mode) }
func (f *ftpFS) Download(p string, offset int64, w io.Writer, stop <-chan struct{}, progress func(int)) error {
	return f.c.Retr(p, offset, w, progress)
}
func (f *ftpFS) Upload(p string, offset int64, r io.Reader, stop <-chan struct{}, progress func(int)) error {
	return f.c.Stor(p, offset, r, progress)
}
