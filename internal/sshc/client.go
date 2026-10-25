package sshc

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

// Config holds connection parameters.
type Config struct {
	User     string
	Password string
	Signer   Signer // optional private key
	// HostKey is called with the server's host key during the handshake.
	HostKey HostKeyCallback
	// Banner, if set, receives any pre-authentication banner text.
	Banner  func(string)
	Timeout time.Duration
	// Dialer allows tests to override how the TCP connection is made.
	Dial func(network, addr string, timeout time.Duration) (net.Conn, error)
}

// AuthError is returned when the server rejects every offered credential.
type AuthError struct{ Methods []string }

func (e *AuthError) Error() string {
	return fmt.Sprintf("authentication failed (server accepts: %s)", strings.Join(e.Methods, ", "))
}

// Client is an authenticated SSH connection.
type Client struct {
	t *transport

	mu       sync.Mutex
	channels map[uint32]*Channel
	nextID   uint32
	err      error
	closed   chan struct{}

	ServerVersion string
	Algorithms    string
}

// Dial connects, performs the handshake and authenticates.
func Dial(addr string, cfg *Config) (*Client, error) {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	dial := cfg.Dial
	if dial == nil {
		dial = func(n, a string, to time.Duration) (net.Conn, error) { return net.DialTimeout(n, a, to) }
	}
	conn, err := dial("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
		tc.SetKeepAlive(true)
		tc.SetKeepAlivePeriod(30 * time.Second)
	}
	conn.SetDeadline(time.Now().Add(timeout))
	c, err := handshake(conn, cfg)
	if err != nil {
		conn.Close()
		return nil, err
	}
	conn.SetDeadline(time.Time{})
	go c.readLoop()
	go c.keepalive()
	return c, nil
}

func handshake(conn net.Conn, cfg *Config) (*Client, error) {
	t := newTransport(conn, cfg.HostKey)
	if err := t.exchangeVersions(); err != nil {
		return nil, err
	}
	if err := t.kex(nil); err != nil {
		return nil, err
	}
	c := &Client{t: t, channels: map[uint32]*Channel{}, closed: make(chan struct{}),
		ServerVersion: string(t.serverVersion),
		Algorithms:    fmt.Sprintf("%s, %s, %s", t.algs.kex, t.algs.hostKey, t.algs.cipherC2S),
	}
	if err := c.authenticate(cfg); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) authenticate(cfg *Config) error {
	t := c.t
	if err := t.writePacket(newMsg(msgServiceRequest).sstr("ssh-userauth").b); err != nil {
		return err
	}
	for {
		p, err := t.readPacket()
		if err != nil {
			return err
		}
		if p[0] == msgServiceAccept {
			break
		}
		return fmt.Errorf("ssh: unexpected message %d (expected service accept)", p[0])
	}

	const svc = "ssh-connection"
	// readResult waits for success/failure, answering keyboard-interactive prompts.
	readResult := func(kbd bool) (ok bool, methods []string, err error) {
		for {
			p, err := t.readPacket()
			if err != nil {
				return false, nil, err
			}
			switch p[0] {
			case msgUserAuthSuccess:
				return true, nil, nil
			case msgUserAuthFailure:
				r := reader{b: p[1:]}
				m := r.nameList()
				partial := r.boolean()
				_ = partial
				return false, m, nil
			case msgUserAuthBanner:
				r := reader{b: p[1:]}
				if cfg.Banner != nil {
					cfg.Banner(r.sstr())
				}
			case msgUserAuthInfoReq:
				if !kbd {
					// PK_OK or password-change request; treat as failure for this method.
					return false, nil, errors.New("server requested a password change")
				}
				r := reader{b: p[1:]}
				r.sstr()
				r.sstr()
				r.sstr()
				n := r.u32()
				if r.err != nil || n > 32 {
					return false, nil, errShort
				}
				resp := newMsg(msgUserAuthInfoRes).u32(n)
				for i := uint32(0); i < n; i++ {
					r.sstr()
					r.boolean()
					resp.sstr(cfg.Password)
				}
				if err := t.writePacket(resp.b); err != nil {
					return false, nil, err
				}
			default:
				return false, nil, fmt.Errorf("ssh: unexpected message %d during authentication", p[0])
			}
		}
	}

	if err := t.writePacket(newMsg(msgUserAuthRequest).sstr(cfg.User).sstr(svc).sstr("none").b); err != nil {
		return err
	}
	ok, methods, err := readResult(false)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	tried := map[string]bool{}
	for {
		var method string
		for _, want := range []string{"publickey", "password", "keyboard-interactive"} {
			if tried[want] || !contains(methods, want) {
				continue
			}
			if want == "publickey" && cfg.Signer == nil {
				continue
			}
			if want != "publickey" && cfg.Password == "" && cfg.Signer != nil {
				continue
			}
			method = want
			break
		}
		if method == "" {
			return &AuthError{Methods: methods}
		}
		tried[method] = true
		switch method {
		case "publickey":
			algs := cfg.Signer.Algorithms()
			if len(t.serverSigAlgs) > 0 {
				var filtered []string
				for _, a := range algs {
					if contains(t.serverSigAlgs, a) {
						filtered = append(filtered, a)
					}
				}
				if len(filtered) > 0 {
					algs = filtered
				}
			}
			blob := cfg.Signer.PublicBlob()
			for _, alg := range algs {
				data := newMsg(0).str(t.sessionID).byte1(msgUserAuthRequest).sstr(cfg.User).sstr(svc).
					sstr("publickey").boolean(true).sstr(alg).str(blob).b[1:]
				sig, err := cfg.Signer.Sign(alg, data)
				if err != nil {
					return err
				}
				req := newMsg(msgUserAuthRequest).sstr(cfg.User).sstr(svc).sstr("publickey").
					boolean(true).sstr(alg).str(blob).str(sig)
				if err := t.writePacket(req.b); err != nil {
					return err
				}
				ok, m, err := readResult(false)
				if err != nil {
					return err
				}
				if ok {
					return nil
				}
				if m != nil {
					methods = m
				}
			}
		case "password":
			req := newMsg(msgUserAuthRequest).sstr(cfg.User).sstr(svc).sstr("password").boolean(false).sstr(cfg.Password)
			if err := t.writePacket(req.b); err != nil {
				return err
			}
			ok, m, err := readResult(false)
			if err != nil {
				return err
			}
			if ok {
				return nil
			}
			if m != nil {
				methods = m
			}
		case "keyboard-interactive":
			req := newMsg(msgUserAuthRequest).sstr(cfg.User).sstr(svc).sstr("keyboard-interactive").sstr("").sstr("")
			if err := t.writePacket(req.b); err != nil {
				return err
			}
			ok, m, err := readResult(true)
			if err != nil {
				return err
			}
			if ok {
				return nil
			}
			if m != nil {
				methods = m
			}
		}
	}
}

func (c *Client) fail(err error) {
	c.mu.Lock()
	if c.err == nil {
		c.err = err
		close(c.closed)
	}
	chans := make([]*Channel, 0, len(c.channels))
	for _, ch := range c.channels {
		chans = append(chans, ch)
	}
	c.mu.Unlock()
	for _, ch := range chans {
		ch.abort(err)
	}
	c.t.conn.Close()
}

// Err returns the error that terminated the connection, if any.
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *Client) keepalive() {
	tk := time.NewTicker(30 * time.Second)
	defer tk.Stop()
	for {
		select {
		case <-c.closed:
			return
		case <-tk.C:
			if err := c.t.writePacket(newMsg(msgIgnore).sstr("").b); err != nil {
				c.fail(err)
				return
			}
		}
	}
}

func (c *Client) readLoop() {
	for {
		p, err := c.t.readPacket()
		if err != nil {
			c.fail(err)
			return
		}
		r := reader{b: p[1:]}
		switch p[0] {
		case msgGlobalRequest:
			r.sstr()
			if r.boolean() {
				c.t.writePacket([]byte{msgRequestFailure})
			}
		case msgChannelOpen:
			r.sstr()
			sender := r.u32()
			c.t.writePacket(newMsg(msgChannelOpenFail).u32(sender).u32(1).sstr("not supported").sstr("").b)
		case msgChannelOpenOK, msgChannelOpenFail, msgChannelWindow, msgChannelData, msgChannelExtData,
			msgChannelEOF, msgChannelClose, msgChannelRequest, msgChannelSuccess, msgChannelFailure:
			id := r.u32()
			c.mu.Lock()
			ch := c.channels[id]
			c.mu.Unlock()
			if ch != nil {
				ch.handle(p[0], r)
			}
		case msgUserAuthSuccess, msgRequestSuccess, msgRequestFailure:
			// ignore stray replies
		default:
			c.t.writePacket(newMsg(msgUnimplemented).u32(c.t.readSeq - 1).b)
		}
	}
}

// Close terminates the connection.
func (c *Client) Close() error {
	c.t.writePacket(newMsg(msgDisconnect).u32(11).sstr("bye").sstr("").b)
	c.fail(errors.New("connection closed"))
	return nil
}

const (
	localWindow    = 4 << 20
	localMaxPacket = 64 << 10
)

// Channel is an SSH session channel.
type Channel struct {
	c        *Client
	localID  uint32
	remoteID uint32

	mu           sync.Mutex
	cond         *sync.Cond
	remoteWindow uint32
	remoteMax    uint32
	buf          []byte
	consumed     uint32
	eof          bool
	closed       bool
	err          error

	openRes chan error
	reqRes  chan bool
}

// OpenSession opens a "session" channel.
func (c *Client) OpenSession() (*Channel, error) {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return nil, c.err
	}
	id := c.nextID
	c.nextID++
	ch := &Channel{c: c, localID: id, openRes: make(chan error, 1), reqRes: make(chan bool, 4)}
	ch.cond = sync.NewCond(&ch.mu)
	c.channels[id] = ch
	c.mu.Unlock()
	if err := c.t.writePacket(newMsg(msgChannelOpen).sstr("session").u32(id).u32(localWindow).u32(localMaxPacket).b); err != nil {
		return nil, err
	}
	select {
	case err := <-ch.openRes:
		if err != nil {
			return nil, err
		}
	case <-c.closed:
		return nil, c.Err()
	}
	return ch, nil
}

// RequestSubsystem starts a subsystem (e.g. "sftp") on the channel.
func (ch *Channel) RequestSubsystem(name string) error {
	if err := ch.c.t.writePacket(newMsg(msgChannelRequest).u32(ch.remoteID).sstr("subsystem").boolean(true).sstr(name).b); err != nil {
		return err
	}
	select {
	case ok := <-ch.reqRes:
		if !ok {
			return fmt.Errorf("server refused the %s subsystem", name)
		}
		return nil
	case <-ch.c.closed:
		return ch.c.Err()
	}
}

func (ch *Channel) abort(err error) {
	ch.mu.Lock()
	if ch.err == nil {
		ch.err = err
	}
	ch.closed = true
	ch.cond.Broadcast()
	ch.mu.Unlock()
	select {
	case ch.openRes <- err:
	default:
	}
}

func (ch *Channel) handle(t byte, r reader) {
	switch t {
	case msgChannelOpenOK:
		ch.mu.Lock()
		ch.remoteID = r.u32()
		ch.remoteWindow = r.u32()
		ch.remoteMax = r.u32()
		if ch.remoteMax == 0 || ch.remoteMax > 1<<20 {
			ch.remoteMax = 32 << 10
		}
		ch.mu.Unlock()
		ch.openRes <- nil
	case msgChannelOpenFail:
		r.u32()
		ch.c.mu.Lock()
		delete(ch.c.channels, ch.localID)
		ch.c.mu.Unlock()
		ch.openRes <- fmt.Errorf("channel open refused: %s", r.sstr())
	case msgChannelWindow:
		n := r.u32()
		ch.mu.Lock()
		ch.remoteWindow += n
		ch.cond.Broadcast()
		ch.mu.Unlock()
	case msgChannelData:
		d := r.str()
		ch.mu.Lock()
		ch.buf = append(ch.buf, d...)
		ch.cond.Broadcast()
		ch.mu.Unlock()
	case msgChannelExtData:
		// stderr from the subsystem; ignore but keep the window open
		d := r.str()
		ch.c.t.writePacket(newMsg(msgChannelWindow).u32(ch.remoteID).u32(uint32(len(d))).b)
	case msgChannelEOF:
		ch.mu.Lock()
		ch.eof = true
		ch.cond.Broadcast()
		ch.mu.Unlock()
	case msgChannelClose:
		ch.mu.Lock()
		already := ch.closed
		ch.closed = true
		ch.eof = true
		ch.cond.Broadcast()
		ch.mu.Unlock()
		if !already {
			ch.c.t.writePacket(newMsg(msgChannelClose).u32(ch.remoteID).b)
		}
		ch.c.mu.Lock()
		delete(ch.c.channels, ch.localID)
		ch.c.mu.Unlock()
	case msgChannelRequest:
		r.sstr()
		if r.boolean() {
			ch.c.t.writePacket(newMsg(msgChannelFailure).u32(ch.remoteID).b)
		}
	case msgChannelSuccess:
		select {
		case ch.reqRes <- true:
		default:
		}
	case msgChannelFailure:
		select {
		case ch.reqRes <- false:
		default:
		}
	}
}

// Read implements io.Reader.
func (ch *Channel) Read(p []byte) (int, error) {
	ch.mu.Lock()
	for len(ch.buf) == 0 && !ch.eof && !ch.closed {
		ch.cond.Wait()
	}
	if len(ch.buf) == 0 {
		err := ch.err
		ch.mu.Unlock()
		if err != nil {
			return 0, err
		}
		return 0, io.EOF
	}
	n := copy(p, ch.buf)
	ch.buf = ch.buf[n:]
	if len(ch.buf) == 0 {
		ch.buf = nil
	}
	ch.consumed += uint32(n)
	var adjust uint32
	if ch.consumed >= localWindow/2 {
		adjust = ch.consumed
		ch.consumed = 0
	}
	ch.mu.Unlock()
	if adjust > 0 {
		ch.c.t.writePacket(newMsg(msgChannelWindow).u32(ch.remoteID).u32(adjust).b)
	}
	return n, nil
}

// Write implements io.Writer, honouring the remote window.
func (ch *Channel) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		ch.mu.Lock()
		for ch.remoteWindow == 0 && !ch.closed {
			ch.cond.Wait()
		}
		if ch.closed {
			err := ch.err
			ch.mu.Unlock()
			if err == nil {
				err = io.ErrClosedPipe
			}
			return written, err
		}
		n := uint32(len(p))
		if n > ch.remoteWindow {
			n = ch.remoteWindow
		}
		if n > ch.remoteMax-64 && ch.remoteMax > 64 {
			n = ch.remoteMax - 64
		}
		ch.remoteWindow -= n
		ch.mu.Unlock()
		if err := ch.c.t.writePacket(newMsg(msgChannelData).u32(ch.remoteID).str(p[:n]).b); err != nil {
			return written, err
		}
		written += int(n)
		p = p[n:]
	}
	return written, nil
}

// Close closes the channel.
func (ch *Channel) Close() error {
	ch.mu.Lock()
	if ch.closed {
		ch.mu.Unlock()
		return nil
	}
	ch.closed = true
	ch.cond.Broadcast()
	ch.mu.Unlock()
	ch.c.t.writePacket(newMsg(msgChannelEOF).u32(ch.remoteID).b)
	return ch.c.t.writePacket(newMsg(msgChannelClose).u32(ch.remoteID).b)
}
