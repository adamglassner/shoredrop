// Package sshc is a small, dependency-free SSH-2 client (transport, user
// authentication and session channels) written for ShoreDrop's SFTP support.
package sshc

import (
	"encoding/binary"
	"errors"
	"math/big"
)

// Message numbers (RFC 4250).
const (
	msgDisconnect      = 1
	msgIgnore          = 2
	msgUnimplemented   = 3
	msgDebug           = 4
	msgServiceRequest  = 5
	msgServiceAccept   = 6
	msgExtInfo         = 7
	msgKexInit         = 20
	msgNewKeys         = 21
	msgKexDHInit       = 30 // also KEX_ECDH_INIT
	msgKexDHReply      = 31 // also KEX_ECDH_REPLY
	msgUserAuthRequest = 50
	msgUserAuthFailure = 51
	msgUserAuthSuccess = 52
	msgUserAuthBanner  = 53
	msgUserAuthInfoReq = 60 // also PK_OK / PASSWD_CHANGEREQ
	msgUserAuthInfoRes = 61
	msgGlobalRequest   = 80
	msgRequestSuccess  = 81
	msgRequestFailure  = 82
	msgChannelOpen     = 90
	msgChannelOpenOK   = 91
	msgChannelOpenFail = 92
	msgChannelWindow   = 93
	msgChannelData     = 94
	msgChannelExtData  = 95
	msgChannelEOF      = 96
	msgChannelClose    = 97
	msgChannelRequest  = 98
	msgChannelSuccess  = 99
	msgChannelFailure  = 100
)

var errShort = errors.New("ssh: malformed packet")

// builder assembles SSH wire-format messages.
type builder struct{ b []byte }

func newMsg(t byte) *builder { return &builder{b: []byte{t}} }

func (w *builder) byte1(v byte) *builder { w.b = append(w.b, v); return w }
func (w *builder) boolean(v bool) *builder {
	if v {
		return w.byte1(1)
	}
	return w.byte1(0)
}
func (w *builder) u32(v uint32) *builder { w.b = binary.BigEndian.AppendUint32(w.b, v); return w }
func (w *builder) u64(v uint64) *builder { w.b = binary.BigEndian.AppendUint64(w.b, v); return w }
func (w *builder) str(s []byte) *builder {
	w.u32(uint32(len(s)))
	w.b = append(w.b, s...)
	return w
}
func (w *builder) sstr(s string) *builder { return w.str([]byte(s)) }
func (w *builder) raw(s []byte) *builder  { w.b = append(w.b, s...); return w }
func (w *builder) mpint(n *big.Int) *builder {
	w.b = appendMpint(w.b, n)
	return w
}

func appendMpint(b []byte, n *big.Int) []byte {
	if n.Sign() == 0 {
		return binary.BigEndian.AppendUint32(b, 0)
	}
	bs := n.Bytes()
	if bs[0]&0x80 != 0 {
		bs = append([]byte{0}, bs...)
	}
	b = binary.BigEndian.AppendUint32(b, uint32(len(bs)))
	return append(b, bs...)
}

// mpintFromBytes encodes an unsigned big-endian byte string as mpint (used for
// curve25519 shared secrets, RFC 8731).
func appendMpintBytes(b []byte, raw []byte) []byte {
	for len(raw) > 0 && raw[0] == 0 {
		raw = raw[1:]
	}
	if len(raw) > 0 && raw[0]&0x80 != 0 {
		b = binary.BigEndian.AppendUint32(b, uint32(len(raw)+1))
		b = append(b, 0)
		return append(b, raw...)
	}
	b = binary.BigEndian.AppendUint32(b, uint32(len(raw)))
	return append(b, raw...)
}

// reader parses SSH wire-format messages.
type reader struct {
	b   []byte
	err error
}

func (r *reader) fail() { r.err = errShort }

func (r *reader) byte1() byte {
	if r.err != nil || len(r.b) < 1 {
		r.fail()
		return 0
	}
	v := r.b[0]
	r.b = r.b[1:]
	return v
}
func (r *reader) boolean() bool { return r.byte1() != 0 }
func (r *reader) u32() uint32 {
	if r.err != nil || len(r.b) < 4 {
		r.fail()
		return 0
	}
	v := binary.BigEndian.Uint32(r.b)
	r.b = r.b[4:]
	return v
}
func (r *reader) u64() uint64 {
	if r.err != nil || len(r.b) < 8 {
		r.fail()
		return 0
	}
	v := binary.BigEndian.Uint64(r.b)
	r.b = r.b[8:]
	return v
}
func (r *reader) str() []byte {
	n := r.u32()
	if r.err != nil || uint32(len(r.b)) < n {
		r.fail()
		return nil
	}
	v := r.b[:n]
	r.b = r.b[n:]
	return v
}
func (r *reader) sstr() string { return string(r.str()) }
func (r *reader) mpint() *big.Int {
	s := r.str()
	if r.err != nil {
		return nil
	}
	if len(s) > 0 && s[0]&0x80 != 0 {
		r.fail() // negative numbers are never valid here
		return nil
	}
	return new(big.Int).SetBytes(s)
}
func (r *reader) nameList() []string {
	s := r.sstr()
	if s == "" {
		return nil
	}
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return out
}
