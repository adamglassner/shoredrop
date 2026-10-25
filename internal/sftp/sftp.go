// Package sftp implements an SFTP version 3 client over an io.ReadWriteCloser
// (normally an SSH "sftp" subsystem channel).
package sftp

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

const (
	fxpInit     = 1
	fxpVersion  = 2
	fxpOpen     = 3
	fxpClose    = 4
	fxpRead     = 5
	fxpWrite    = 6
	fxpLstat    = 7
	fxpFstat    = 8
	fxpSetstat  = 9
	fxpOpendir  = 11
	fxpReaddir  = 12
	fxpRemove   = 13
	fxpMkdir    = 14
	fxpRmdir    = 15
	fxpRealpath = 16
	fxpStat     = 17
	fxpRename   = 18
	fxpExtended = 200
	fxpStatus   = 101
	fxpHandle   = 102
	fxpData     = 103
	fxpName     = 104
	fxpAttrs    = 105

	attrSize    = 0x1
	attrUIDGID  = 0x2
	attrPerm    = 0x4
	attrACMod   = 0x8
	attrExtends = 0x80000000

	flagRead   = 0x1
	flagWrite  = 0x2
	flagAppend = 0x4
	flagCreat  = 0x8
	flagTrunc  = 0x10

	statusOK  = 0
	statusEOF = 1
)

// StatusError is an SFTP status reply other than OK.
type StatusError struct {
	Code uint32
	Msg  string
}

var statusText = map[uint32]string{
	2: "no such file", 3: "permission denied", 4: "failure", 5: "bad message",
	6: "no connection", 7: "connection lost", 8: "operation unsupported",
}

func (e *StatusError) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	if s, ok := statusText[e.Code]; ok {
		return s
	}
	return fmt.Sprintf("sftp error %d", e.Code)
}

// IsNotExist reports whether err means "no such file".
func IsNotExist(err error) bool {
	var se *StatusError
	return errors.As(err, &se) && se.Code == 2
}

// Attrs are file attributes.
type Attrs struct {
	Flags uint32
	Size  uint64
	UID   uint32
	GID   uint32
	Perm  uint32
	Atime uint32
	Mtime uint32
}

func (a Attrs) IsDir() bool  { return a.Flags&attrPerm != 0 && a.Perm&0o170000 == 0o040000 }
func (a Attrs) IsLink() bool { return a.Flags&attrPerm != 0 && a.Perm&0o170000 == 0o120000 }
func (a Attrs) ModTime() time.Time {
	if a.Flags&attrACMod == 0 {
		return time.Time{}
	}
	return time.Unix(int64(a.Mtime), 0)
}

// Entry is a directory listing entry.
type Entry struct {
	Name     string
	LongName string
	Attrs    Attrs
}

type response struct {
	typ  byte
	data []byte
}

// Client is an SFTP session.
type Client struct {
	rw io.ReadWriteCloser

	wmu     sync.Mutex
	mu      sync.Mutex
	nextID  uint32
	pending map[uint32]chan response
	err     error
	done    chan struct{}

	Extensions map[string]string
}

// NewClient performs the SFTP version negotiation.
func NewClient(rw io.ReadWriteCloser) (*Client, error) {
	c := &Client{rw: rw, pending: map[uint32]chan response{}, done: make(chan struct{}), Extensions: map[string]string{}}
	if err := c.writePkt([]byte{fxpInit, 0, 0, 0, 3}); err != nil {
		return nil, err
	}
	typ, data, err := c.readPkt()
	if err != nil {
		return nil, fmt.Errorf("sftp: handshake failed: %w", err)
	}
	if typ != fxpVersion || len(data) < 4 {
		return nil, errors.New("sftp: unexpected reply to INIT (is SFTP enabled on this server?)")
	}
	r := rd{b: data[4:]}
	for len(r.b) > 0 && r.err == nil {
		k, v := r.str(), r.str()
		c.Extensions[k] = v
	}
	go c.recvLoop()
	return c, nil
}

func (c *Client) writePkt(p []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(p)))
	buf := make([]byte, 0, 4+len(p))
	buf = append(buf, hdr[:]...)
	buf = append(buf, p...)
	_, err := c.rw.Write(buf)
	return err
}

func (c *Client) readPkt() (byte, []byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(c.rw, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || n > 1<<20 {
		return 0, nil, errors.New("sftp: invalid packet length")
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(c.rw, buf); err != nil {
		return 0, nil, err
	}
	return buf[0], buf[1:], nil
}

func (c *Client) recvLoop() {
	for {
		typ, data, err := c.readPkt()
		if err != nil {
			c.mu.Lock()
			if c.err == nil {
				c.err = fmt.Errorf("sftp connection lost: %w", err)
			}
			for id, ch := range c.pending {
				close(ch)
				delete(c.pending, id)
			}
			c.mu.Unlock()
			close(c.done)
			return
		}
		if len(data) < 4 {
			continue
		}
		id := binary.BigEndian.Uint32(data)
		c.mu.Lock()
		ch := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if ch != nil {
			ch <- response{typ, data[4:]}
		}
	}
}

// send issues a request and returns a channel delivering its response.
func (c *Client) send(typ byte, body func(w *wr)) (<-chan response, error) {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return nil, c.err
	}
	id := c.nextID
	c.nextID++
	ch := make(chan response, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	w := &wr{b: []byte{typ}}
	w.u32(id)
	if body != nil {
		body(w)
	}
	if err := c.writePkt(w.b); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}
	return ch, nil
}

func (c *Client) wait(ch <-chan response) (response, error) {
	r, ok := <-ch
	if !ok {
		c.mu.Lock()
		err := c.err
		c.mu.Unlock()
		if err == nil {
			err = errors.New("sftp: connection closed")
		}
		return r, err
	}
	return r, nil
}

func (c *Client) call(typ byte, body func(w *wr)) (response, error) {
	ch, err := c.send(typ, body)
	if err != nil {
		return response{}, err
	}
	return c.wait(ch)
}

func statusErr(r response) error {
	if r.typ != fxpStatus {
		return fmt.Errorf("sftp: unexpected response type %d", r.typ)
	}
	rr := rd{b: r.data}
	code := rr.u32()
	msg := rr.str()
	if code == statusOK {
		return nil
	}
	return &StatusError{Code: code, Msg: msg}
}

func (c *Client) callStatus(typ byte, body func(w *wr)) error {
	r, err := c.call(typ, body)
	if err != nil {
		return err
	}
	return statusErr(r)
}

func (c *Client) callHandle(typ byte, body func(w *wr)) (string, error) {
	r, err := c.call(typ, body)
	if err != nil {
		return "", err
	}
	if r.typ == fxpHandle {
		rr := rd{b: r.data}
		h := rr.str()
		return h, rr.err
	}
	if e := statusErr(r); e != nil {
		return "", e
	}
	return "", errors.New("sftp: expected handle")
}

func (c *Client) callAttrs(typ byte, body func(w *wr)) (Attrs, error) {
	r, err := c.call(typ, body)
	if err != nil {
		return Attrs{}, err
	}
	if r.typ == fxpAttrs {
		rr := rd{b: r.data}
		a := rr.attrs()
		return a, rr.err
	}
	if e := statusErr(r); e != nil {
		return Attrs{}, e
	}
	return Attrs{}, errors.New("sftp: expected attributes")
}

// Close ends the session.
func (c *Client) Close() error { return c.rw.Close() }

// RealPath canonicalises a path ("." gives the home directory).
func (c *Client) RealPath(p string) (string, error) {
	r, err := c.call(fxpRealpath, func(w *wr) { w.str(p) })
	if err != nil {
		return "", err
	}
	if r.typ == fxpName {
		rr := rd{b: r.data}
		if rr.u32() < 1 {
			return "", errors.New("sftp: empty realpath reply")
		}
		return rr.str(), rr.err
	}
	if e := statusErr(r); e != nil {
		return "", e
	}
	return "", errors.New("sftp: bad realpath reply")
}

func (c *Client) Stat(p string) (Attrs, error) {
	return c.callAttrs(fxpStat, func(w *wr) { w.str(p) })
}
func (c *Client) Lstat(p string) (Attrs, error) {
	return c.callAttrs(fxpLstat, func(w *wr) { w.str(p) })
}

// ReadDir lists a directory (excluding "." and "..").
func (c *Client) ReadDir(p string) ([]Entry, error) {
	h, err := c.callHandle(fxpOpendir, func(w *wr) { w.str(p) })
	if err != nil {
		return nil, err
	}
	defer c.closeHandle(h)
	var out []Entry
	for {
		r, err := c.call(fxpReaddir, func(w *wr) { w.str(h) })
		if err != nil {
			return nil, err
		}
		if r.typ == fxpStatus {
			if e := statusErr(r); e != nil {
				var se *StatusError
				if errors.As(e, &se) && se.Code == statusEOF {
					return out, nil
				}
				return nil, e
			}
			return out, nil
		}
		if r.typ != fxpName {
			return nil, errors.New("sftp: unexpected readdir reply")
		}
		rr := rd{b: r.data}
		n := rr.u32()
		for i := uint32(0); i < n && rr.err == nil; i++ {
			e := Entry{Name: rr.str(), LongName: rr.str(), Attrs: rr.attrs()}
			if e.Name == "." || e.Name == ".." {
				continue
			}
			out = append(out, e)
		}
		if rr.err != nil {
			return nil, rr.err
		}
	}
}

func (c *Client) closeHandle(h string) error {
	return c.callStatus(fxpClose, func(w *wr) { w.str(h) })
}

func (c *Client) Remove(p string) error { return c.callStatus(fxpRemove, func(w *wr) { w.str(p) }) }
func (c *Client) Rmdir(p string) error  { return c.callStatus(fxpRmdir, func(w *wr) { w.str(p) }) }
func (c *Client) Mkdir(p string) error {
	return c.callStatus(fxpMkdir, func(w *wr) { w.str(p); w.u32(0) })
}
func (c *Client) Rename(oldp, newp string) error {
	return c.callStatus(fxpRename, func(w *wr) { w.str(oldp); w.str(newp) })
}
func (c *Client) Chmod(p string, mode uint32) error {
	return c.callStatus(fxpSetstat, func(w *wr) { w.str(p); w.u32(attrPerm); w.u32(mode & 0o7777) })
}

// SetModTime sets access and modification times.
func (c *Client) SetModTime(p string, t time.Time) error {
	return c.callStatus(fxpSetstat, func(w *wr) {
		w.str(p)
		w.u32(attrACMod)
		w.u32(uint32(t.Unix()))
		w.u32(uint32(t.Unix()))
	})
}

// File is an open remote file.
type File struct {
	c      *Client
	handle string
	path   string
}

// OpenRead opens a file for reading.
func (c *Client) OpenRead(p string) (*File, error) {
	h, err := c.callHandle(fxpOpen, func(w *wr) { w.str(p); w.u32(flagRead); w.u32(0) })
	if err != nil {
		return nil, err
	}
	return &File{c: c, handle: h, path: p}, nil
}

// OpenWrite opens a file for writing; truncate empties it, otherwise writes
// start at the offsets given to WriteFrom.
func (c *Client) OpenWrite(p string, truncate bool) (*File, error) {
	flags := uint32(flagWrite | flagCreat)
	if truncate {
		flags |= flagTrunc
	}
	h, err := c.callHandle(fxpOpen, func(w *wr) { w.str(p); w.u32(flags); w.u32(0) })
	if err != nil {
		return nil, err
	}
	return &File{c: c, handle: h, path: p}, nil
}

func (f *File) Close() error { return f.c.closeHandle(f.handle) }

const chunk = 32 * 1024
const window = 64

// ReadTo streams the file from offset into w using pipelined requests.
// progress is called with the number of bytes written each time.
func (f *File) ReadTo(w io.Writer, offset int64, stop <-chan struct{}, progress func(int)) error {
	type req struct {
		off int64
		ch  <-chan response
	}
	var queue []req
	next := offset
	eof := false
	issue := func() error {
		for !eof && len(queue) < window {
			off := next
			ch, err := f.c.send(fxpRead, func(w *wr) { w.str(f.handle); w.u64(uint64(off)); w.u32(chunk) })
			if err != nil {
				return err
			}
			queue = append(queue, req{off, ch})
			next += chunk
		}
		return nil
	}
	if err := issue(); err != nil {
		return err
	}
	expect := offset
	for len(queue) > 0 {
		select {
		case <-stop:
			return errStopped
		default:
		}
		q := queue[0]
		queue = queue[1:]
		r, err := f.c.wait(q.ch)
		if err != nil {
			return err
		}
		if r.typ == fxpStatus {
			if e := statusErr(r); e != nil {
				var se *StatusError
				if errors.As(e, &se) && se.Code == statusEOF {
					eof = true
					// drain the rest of the outstanding requests
					for _, rest := range queue {
						f.c.wait(rest.ch)
					}
					return nil
				}
				return e
			}
			continue
		}
		if r.typ != fxpData {
			return errors.New("sftp: unexpected read reply")
		}
		rr := rd{b: r.data}
		data := rr.strb()
		if rr.err != nil {
			return rr.err
		}
		if q.off != expect {
			return errors.New("sftp: out-of-order read")
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
		if progress != nil {
			progress(len(data))
		}
		expect += int64(len(data))
		if len(data) < chunk {
			// Short read: server returned less than asked. Re-sync the pipeline
			// from the actual position by draining and reissuing.
			for _, rest := range queue {
				rr, err := f.c.wait(rest.ch)
				if err != nil {
					return err
				}
				_ = rr
			}
			queue = nil
			next = expect
		}
		if err := issue(); err != nil {
			return err
		}
	}
	return nil
}

var errStopped = errors.New("transfer stopped")

// ErrStopped is returned when a transfer is interrupted via its stop channel.
var ErrStopped = errStopped

// WriteFrom uploads r to the file starting at offset with pipelined writes.
func (f *File) WriteFrom(r io.Reader, offset int64, stop <-chan struct{}, progress func(int)) error {
	var queue []<-chan response
	var sizes []int
	off := offset
	buf := make([]byte, chunk)
	done := false
	flush := func(n int) error {
		for len(queue) > n {
			resp, err := f.c.wait(queue[0])
			if err != nil {
				return err
			}
			if e := statusErr(resp); e != nil {
				return e
			}
			if progress != nil {
				progress(sizes[0])
			}
			queue, sizes = queue[1:], sizes[1:]
		}
		return nil
	}
	for !done {
		select {
		case <-stop:
			flush(0)
			return errStopped
		default:
		}
		n, err := io.ReadFull(r, buf)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			done = true
		} else if err != nil {
			return err
		}
		if n > 0 {
			data := buf[:n]
			o := off
			ch, err := f.c.send(fxpWrite, func(w *wr) { w.str(f.handle); w.u64(uint64(o)); w.strb(data) })
			if err != nil {
				return err
			}
			queue = append(queue, ch)
			sizes = append(sizes, n)
			off += int64(n)
		}
		if err := flush(window - 1); err != nil {
			return err
		}
	}
	return flush(0)
}

// FileMode converts SFTP permission bits into an os.FileMode.
func FileMode(perm uint32) os.FileMode {
	m := os.FileMode(perm & 0o777)
	switch perm & 0o170000 {
	case 0o040000:
		m |= os.ModeDir
	case 0o120000:
		m |= os.ModeSymlink
	}
	return m
}

// --- wire helpers ---

type wr struct{ b []byte }

func (w *wr) u32(v uint32) { w.b = binary.BigEndian.AppendUint32(w.b, v) }
func (w *wr) u64(v uint64) { w.b = binary.BigEndian.AppendUint64(w.b, v) }
func (w *wr) str(s string) { w.u32(uint32(len(s))); w.b = append(w.b, s...) }
func (w *wr) strb(s []byte) {
	w.u32(uint32(len(s)))
	w.b = append(w.b, s...)
}

type rd struct {
	b   []byte
	err error
}

func (r *rd) u32() uint32 {
	if r.err != nil || len(r.b) < 4 {
		r.err = errors.New("sftp: short packet")
		return 0
	}
	v := binary.BigEndian.Uint32(r.b)
	r.b = r.b[4:]
	return v
}
func (r *rd) u64() uint64 {
	if r.err != nil || len(r.b) < 8 {
		r.err = errors.New("sftp: short packet")
		return 0
	}
	v := binary.BigEndian.Uint64(r.b)
	r.b = r.b[8:]
	return v
}
func (r *rd) strb() []byte {
	n := r.u32()
	if r.err != nil || uint32(len(r.b)) < n {
		r.err = errors.New("sftp: short packet")
		return nil
	}
	v := r.b[:n]
	r.b = r.b[n:]
	return v
}
func (r *rd) str() string { return string(r.strb()) }
func (r *rd) attrs() Attrs {
	var a Attrs
	a.Flags = r.u32()
	if a.Flags&attrSize != 0 {
		a.Size = r.u64()
	}
	if a.Flags&attrUIDGID != 0 {
		a.UID, a.GID = r.u32(), r.u32()
	}
	if a.Flags&attrPerm != 0 {
		a.Perm = r.u32()
	}
	if a.Flags&attrACMod != 0 {
		a.Atime, a.Mtime = r.u32(), r.u32()
	}
	if a.Flags&attrExtends != 0 {
		n := r.u32()
		for i := uint32(0); i < n && r.err == nil; i++ {
			r.str()
			r.str()
		}
	}
	return a
}
