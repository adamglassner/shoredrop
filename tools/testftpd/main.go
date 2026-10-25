// Command testftpd is a tiny FTP/FTPS server for testing ShoreDrop locally.
// It listens on 127.0.0.1 only and serves a folder next to the executable.
//
//	testftpd [-port 2121] [-root .\ftproot] [-user sdtest] [-pass sdtest-2026]
package main

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var (
	root  string
	user  string
	pass  string
	tlsCf *tls.Config
)

func main() {
	port := flag.Int("port", 2121, "port")
	flag.StringVar(&root, "root", "", "folder to serve (default: ftproot next to the exe)")
	flag.StringVar(&user, "user", "sdtest", "username")
	flag.StringVar(&pass, "pass", "sdtest-2026", "password")
	flag.Parse()
	if root == "" {
		exe, _ := os.Executable()
		root = filepath.Join(filepath.Dir(exe), "ftproot")
	}
	os.MkdirAll(root, 0o755)
	tlsCf = &tls.Config{Certificates: []tls.Certificate{selfSigned()}}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("ShoreDrop test FTP/FTPS server\nListening on 127.0.0.1:%d   user %q   password %q\nServing %s\nClose this window to stop.\n\n", *port, user, pass, root)
	for {
		c, err := ln.Accept()
		if err != nil {
			continue
		}
		go handle(c)
	}
}

func selfSigned() tls.Certificate {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(30 * 24 * time.Hour), DNSNames: []string{"localhost"}}
	der, _ := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: k}
}

type sess struct {
	conn   net.Conn
	r      *bufio.Reader
	cwd    string
	authed bool
	user   string
	prot   bool
	pasv   net.Listener
	port   string
	rest   int64
	rnfr   string
}

func (s *sess) reply(f string, a ...any) {
	msg := fmt.Sprintf(f, a...)
	fmt.Printf("  > %s\n", msg)
	fmt.Fprintf(s.conn, "%s\r\n", msg)
}

func (s *sess) local(arg string) (string, error) {
	p := arg
	if !strings.HasPrefix(p, "/") {
		p = path.Join(s.cwd, p)
	}
	p = path.Clean("/" + p)
	return filepath.Join(root, filepath.FromSlash(p)), nil
}

func (s *sess) data() (net.Conn, error) {
	var c net.Conn
	var err error
	if s.pasv != nil {
		s.pasv.(*net.TCPListener).SetDeadline(time.Now().Add(15 * time.Second))
		c, err = s.pasv.Accept()
		s.pasv.Close()
		s.pasv = nil
	} else if s.port != "" {
		c, err = net.DialTimeout("tcp", s.port, 15*time.Second)
		s.port = ""
	} else {
		return nil, fmt.Errorf("no data connection")
	}
	if err != nil {
		return nil, err
	}
	if s.prot {
		tc := tls.Server(c, tlsCf)
		if err := tc.Handshake(); err != nil {
			c.Close()
			return nil, err
		}
		return tc, nil
	}
	return c, nil
}

func closeData(c net.Conn) {
	switch t := c.(type) {
	case *tls.Conn:
		t.CloseWrite()
	case *net.TCPConn:
		t.CloseWrite()
	}
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	io.Copy(io.Discard, c)
	c.Close()
}

func handle(c net.Conn) {
	defer c.Close()
	s := &sess{conn: c, r: bufio.NewReader(c), cwd: "/"}
	fmt.Println("connection from", c.RemoteAddr())
	s.reply("220 ShoreDrop test server ready")
	for {
		line, err := s.r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		cmd, arg, _ := strings.Cut(line, " ")
		cmd = strings.ToUpper(cmd)
		if cmd == "PASS" {
			fmt.Println("  < PASS ****")
		} else {
			fmt.Println("  <", line)
		}
		if !s.dispatch(cmd, arg) {
			return
		}
	}
}

func (s *sess) dispatch(cmd, arg string) bool {
	switch cmd {
	case "AUTH":
		s.reply("234 AUTH TLS ok")
		tc := tls.Server(s.conn, tlsCf)
		if err := tc.Handshake(); err != nil {
			return false
		}
		s.conn = tc
		s.r = bufio.NewReader(tc)
		return true
	case "USER":
		s.user = arg
		s.reply("331 Password required")
		return true
	case "PASS":
		if s.user == user && arg == pass {
			s.authed = true
			s.reply("230 Logged in")
		} else {
			s.reply("530 Login incorrect")
		}
		return true
	case "QUIT":
		s.reply("221 Bye")
		return false
	case "FEAT":
		fmt.Fprintf(s.conn, "211-Features:\r\n UTF8\r\n SIZE\r\n MDTM\r\n REST STREAM\r\n MLSD\r\n EPSV\r\n PBSZ\r\n PROT\r\n AUTH TLS\r\n211 End\r\n")
		return true
	}
	if !s.authed {
		s.reply("530 Please login")
		return true
	}
	fail := func(err error) { s.reply("550 %v", err) }
	switch cmd {
	case "PBSZ":
		s.reply("200 PBSZ=0")
	case "PROT":
		s.prot = strings.ToUpper(arg) == "P"
		s.reply("200 Protection set")
	case "OPTS", "TYPE", "MODE", "STRU":
		s.reply("200 OK")
	case "SYST":
		s.reply("215 UNIX Type: L8")
	case "NOOP":
		s.reply("200 OK")
	case "PWD":
		s.reply(`257 "%s" is the current directory`, s.cwd)
	case "CWD":
		p, _ := s.local(arg)
		if st, err := os.Stat(p); err != nil || !st.IsDir() {
			s.reply("550 No such directory")
			return true
		}
		if !strings.HasPrefix(arg, "/") {
			arg = path.Join(s.cwd, arg)
		}
		s.cwd = path.Clean("/" + arg)
		s.reply("250 OK")
	case "CDUP":
		s.cwd = path.Dir(s.cwd)
		s.reply("250 OK")
	case "PASV", "EPSV":
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			fail(err)
			return true
		}
		s.pasv = ln
		p := ln.Addr().(*net.TCPAddr).Port
		if cmd == "EPSV" {
			s.reply("229 Entering Extended Passive Mode (|||%d|)", p)
		} else {
			s.reply("227 Entering Passive Mode (127,0,0,1,%d,%d)", p>>8, p&255)
		}
	case "PORT":
		n := strings.Split(arg, ",")
		if len(n) == 6 {
			a, _ := strconv.Atoi(n[4])
			b, _ := strconv.Atoi(n[5])
			s.port = fmt.Sprintf("%s.%s.%s.%s:%d", n[0], n[1], n[2], n[3], a*256+b)
		}
		s.reply("200 PORT ok")
	case "EPRT":
		parts := strings.Split(arg, arg[:1])
		s.port = net.JoinHostPort(parts[2], parts[3])
		s.reply("200 EPRT ok")
	case "REST":
		s.rest, _ = strconv.ParseInt(arg, 10, 64)
		s.reply("350 Restarting at %d", s.rest)
	case "LIST", "MLSD", "NLST":
		target := arg
		if strings.HasPrefix(target, "-") {
			target = ""
		}
		p, _ := s.local(target)
		ents, err := os.ReadDir(p)
		if err != nil {
			fail(err)
			return true
		}
		s.reply("150 Opening data connection")
		d, err := s.data()
		if err != nil {
			s.reply("425 %v", err)
			return true
		}
		w := bufio.NewWriter(d)
		for _, e := range ents {
			info, err := e.Info()
			if err != nil {
				continue
			}
			mode := info.Mode().Perm()
			if m, ok := chmods[filepath.Join(p, e.Name())]; ok {
				mode = m
			}
			switch cmd {
			case "MLSD":
				t := "file"
				if e.IsDir() {
					t = "dir"
				}
				fmt.Fprintf(w, "type=%s;size=%d;modify=%s;UNIX.mode=%04o; %s\r\n", t, info.Size(), info.ModTime().UTC().Format("20060102150405"), mode, e.Name())
			case "NLST":
				fmt.Fprintf(w, "%s\r\n", e.Name())
			default:
				fmt.Fprintf(w, "%s 1 owner group %d %s %s\r\n", info.Mode(), info.Size(), info.ModTime().Format("Jan _2 15:04"), e.Name())
			}
		}
		w.Flush()
		closeData(d)
		s.reply("226 Transfer complete")
	case "RETR":
		p, _ := s.local(arg)
		f, err := os.Open(p)
		if err != nil {
			fail(err)
			return true
		}
		defer f.Close()
		f.Seek(s.rest, io.SeekStart)
		s.rest = 0
		s.reply("150 Opening data connection")
		d, err := s.data()
		if err != nil {
			s.reply("425 %v", err)
			return true
		}
		io.Copy(d, f)
		closeData(d)
		s.reply("226 Transfer complete")
	case "STOR", "APPE":
		p, _ := s.local(arg)
		flags := os.O_CREATE | os.O_WRONLY
		if cmd == "APPE" {
			flags |= os.O_APPEND
		} else if s.rest == 0 {
			flags |= os.O_TRUNC
		}
		f, err := os.OpenFile(p, flags, 0o644)
		if err != nil {
			fail(err)
			return true
		}
		if s.rest > 0 {
			f.Truncate(s.rest)
			f.Seek(s.rest, io.SeekStart)
		}
		s.rest = 0
		s.reply("150 Ok to send data")
		d, err := s.data()
		if err != nil {
			f.Close()
			s.reply("425 %v", err)
			return true
		}
		io.Copy(f, d)
		f.Close()
		closeData(d)
		s.reply("226 Transfer complete")
	case "SIZE":
		p, _ := s.local(arg)
		st, err := os.Stat(p)
		if err != nil || st.IsDir() {
			s.reply("550 No such file")
			return true
		}
		s.reply("213 %d", st.Size())
	case "MDTM":
		p, _ := s.local(arg)
		st, err := os.Stat(p)
		if err != nil {
			fail(err)
			return true
		}
		s.reply("213 %s", st.ModTime().UTC().Format("20060102150405"))
	case "DELE":
		p, _ := s.local(arg)
		if err := os.Remove(p); err != nil {
			fail(err)
			return true
		}
		s.reply("250 Deleted")
	case "MKD":
		p, _ := s.local(arg)
		if err := os.Mkdir(p, 0o755); err != nil {
			fail(err)
			return true
		}
		s.reply(`257 "%s" created`, arg)
	case "RMD":
		p, _ := s.local(arg)
		if err := os.Remove(p); err != nil {
			fail(err)
			return true
		}
		s.reply("250 Removed")
	case "RNFR":
		p, _ := s.local(arg)
		if _, err := os.Stat(p); err != nil {
			fail(err)
			return true
		}
		s.rnfr = p
		s.reply("350 Ready for RNTO")
	case "RNTO":
		p, _ := s.local(arg)
		if err := os.Rename(s.rnfr, p); err != nil {
			fail(err)
			return true
		}
		s.reply("250 Renamed")
	case "SITE":
		sub, rest, _ := strings.Cut(arg, " ")
		if strings.ToUpper(sub) == "CHMOD" {
			m, p, _ := strings.Cut(rest, " ")
			v, err := strconv.ParseUint(m, 8, 32)
			lp, _ := s.local(p)
			if _, serr := os.Stat(lp); err != nil || serr != nil {
				s.reply("550 CHMOD failed")
				return true
			}
			// Windows has no Unix permissions; remember them so listings reflect the change.
			chmods[lp] = os.FileMode(v)
			s.reply("200 CHMOD ok")
			return true
		}
		s.reply("500 Unknown SITE command")
	default:
		s.reply("502 Command not implemented")
	}
	return true
}

var chmods = map[string]os.FileMode{}
