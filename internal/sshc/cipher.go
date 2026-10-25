package sshc

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
)

const maxPacket = 256 * 1024

// packetCipher frames, encrypts and authenticates SSH binary packets.
type packetCipher interface {
	seal(seq uint32, payload []byte) []byte
	open(seq uint32, r io.Reader) ([]byte, error)
}

// --- algorithm tables ---

type cipherSpec struct {
	keyLen, ivLen int
	gcm           bool
}

var cipherSpecs = map[string]cipherSpec{
	"aes128-gcm@openssh.com": {16, 12, true},
	"aes256-gcm@openssh.com": {32, 12, true},
	"aes128-ctr":             {16, 16, false},
	"aes192-ctr":             {24, 16, false},
	"aes256-ctr":             {32, 16, false},
}

var supportedCiphers = []string{
	"aes128-gcm@openssh.com", "aes256-gcm@openssh.com",
	"aes128-ctr", "aes192-ctr", "aes256-ctr",
}

type macSpec struct {
	keyLen int
	etm    bool
	newH   func() hash.Hash
}

var macSpecs = map[string]macSpec{
	"hmac-sha2-256-etm@openssh.com": {32, true, sha256.New},
	"hmac-sha2-512-etm@openssh.com": {64, true, sha512.New},
	"hmac-sha2-256":                 {32, false, sha256.New},
	"hmac-sha2-512":                 {64, false, sha512.New},
	"hmac-sha1":                     {20, false, sha1.New},
}

var supportedMACs = []string{
	"hmac-sha2-256-etm@openssh.com", "hmac-sha2-512-etm@openssh.com",
	"hmac-sha2-256", "hmac-sha2-512", "hmac-sha1",
}

func newPacketCipher(alg, macAlg string, key, iv, macKey []byte) (packetCipher, error) {
	spec, ok := cipherSpecs[alg]
	if !ok {
		return nil, fmt.Errorf("ssh: unsupported cipher %s", alg)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if spec.gcm {
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		g := &gcmCipher{aead: aead}
		copy(g.iv[:], iv)
		return g, nil
	}
	ms, ok := macSpecs[macAlg]
	if !ok {
		return nil, fmt.Errorf("ssh: unsupported MAC %s", macAlg)
	}
	return &ctrCipher{
		stream: cipher.NewCTR(block, iv),
		mac:    hmac.New(ms.newH, macKey),
		etm:    ms.etm,
	}, nil
}

func padLen(n, block int) int {
	p := block - n%block
	if p < 4 {
		p += block
	}
	return p
}

// --- none (before the first key exchange) ---

type noneCipher struct{}

func (noneCipher) seal(_ uint32, payload []byte) []byte {
	pl := padLen(5+len(payload), 8)
	out := make([]byte, 5+len(payload)+pl)
	binary.BigEndian.PutUint32(out, uint32(1+len(payload)+pl))
	out[4] = byte(pl)
	copy(out[5:], payload)
	rand.Read(out[5+len(payload):])
	return out
}

func (noneCipher) open(_ uint32, r io.Reader) ([]byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:4])
	if n < 5 || n > maxPacket {
		return nil, errors.New("ssh: invalid packet length")
	}
	pl := int(hdr[4])
	rest := make([]byte, n-1)
	if _, err := io.ReadFull(r, rest); err != nil {
		return nil, err
	}
	if pl >= len(rest) {
		return nil, errors.New("ssh: invalid padding")
	}
	return rest[:len(rest)-pl], nil
}

// --- AES-CTR + HMAC (both encrypt-and-MAC and encrypt-then-MAC) ---

type ctrCipher struct {
	stream cipher.Stream
	mac    hash.Hash
	etm    bool
}

func (c *ctrCipher) macOf(seq uint32, data ...[]byte) []byte {
	c.mac.Reset()
	var s [4]byte
	binary.BigEndian.PutUint32(s[:], seq)
	c.mac.Write(s[:])
	for _, d := range data {
		c.mac.Write(d)
	}
	return c.mac.Sum(nil)
}

func (c *ctrCipher) seal(seq uint32, payload []byte) []byte {
	var pl int
	if c.etm {
		pl = padLen(1+len(payload), 16)
	} else {
		pl = padLen(5+len(payload), 16)
	}
	pkt := make([]byte, 5+len(payload)+pl)
	binary.BigEndian.PutUint32(pkt, uint32(1+len(payload)+pl))
	pkt[4] = byte(pl)
	copy(pkt[5:], payload)
	rand.Read(pkt[5+len(payload):])
	if c.etm {
		c.stream.XORKeyStream(pkt[4:], pkt[4:])
		return append(pkt, c.macOf(seq, pkt)...)
	}
	m := c.macOf(seq, pkt)
	c.stream.XORKeyStream(pkt, pkt)
	return append(pkt, m...)
}

func (c *ctrCipher) open(seq uint32, r io.Reader) ([]byte, error) {
	macLen := c.mac.Size()
	if c.etm {
		var lb [4]byte
		if _, err := io.ReadFull(r, lb[:]); err != nil {
			return nil, err
		}
		n := binary.BigEndian.Uint32(lb[:])
		if n < 5 || n > maxPacket || n%16 != 0 {
			return nil, errors.New("ssh: invalid packet length")
		}
		buf := make([]byte, int(n)+macLen)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		body, tag := buf[:n], buf[n:]
		if subtle.ConstantTimeCompare(c.macOf(seq, lb[:], body), tag) != 1 {
			return nil, errors.New("ssh: MAC verification failed")
		}
		c.stream.XORKeyStream(body, body)
		pl := int(body[0])
		if pl+1 > len(body) {
			return nil, errors.New("ssh: invalid padding")
		}
		return body[1 : len(body)-pl], nil
	}
	first := make([]byte, 16)
	if _, err := io.ReadFull(r, first); err != nil {
		return nil, err
	}
	c.stream.XORKeyStream(first, first)
	n := binary.BigEndian.Uint32(first[:4])
	if n < 12 || n > maxPacket || (n+4)%16 != 0 {
		return nil, errors.New("ssh: invalid packet length")
	}
	rest := make([]byte, int(n)+4-16+macLen)
	if _, err := io.ReadFull(r, rest); err != nil {
		return nil, err
	}
	enc, tag := rest[:len(rest)-macLen], rest[len(rest)-macLen:]
	c.stream.XORKeyStream(enc, enc)
	full := append(first, enc...)
	if subtle.ConstantTimeCompare(c.macOf(seq, full), tag) != 1 {
		return nil, errors.New("ssh: MAC verification failed")
	}
	pl := int(full[4])
	if 5+pl > len(full) {
		return nil, errors.New("ssh: invalid padding")
	}
	return full[5 : len(full)-pl], nil
}

// --- AES-GCM (RFC 5647 as profiled by OpenSSH) ---

type gcmCipher struct {
	aead cipher.AEAD
	iv   [12]byte
}

func (g *gcmCipher) incIV() {
	ctr := binary.BigEndian.Uint64(g.iv[4:])
	binary.BigEndian.PutUint64(g.iv[4:], ctr+1)
}

func (g *gcmCipher) seal(_ uint32, payload []byte) []byte {
	pl := padLen(1+len(payload), 16)
	n := 1 + len(payload) + pl
	out := make([]byte, 4, 4+n+16)
	binary.BigEndian.PutUint32(out, uint32(n))
	plain := make([]byte, n)
	plain[0] = byte(pl)
	copy(plain[1:], payload)
	rand.Read(plain[1+len(payload):])
	out = g.aead.Seal(out, g.iv[:], plain, out[:4])
	g.incIV()
	return out
}

func (g *gcmCipher) open(_ uint32, r io.Reader) ([]byte, error) {
	var lb [4]byte
	if _, err := io.ReadFull(r, lb[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(lb[:])
	if n < 16 || n > maxPacket || n%16 != 0 {
		return nil, errors.New("ssh: invalid packet length")
	}
	buf := make([]byte, int(n)+16)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	plain, err := g.aead.Open(buf[:0], g.iv[:], buf, lb[:])
	if err != nil {
		return nil, errors.New("ssh: GCM authentication failed")
	}
	g.incIV()
	pl := int(plain[0])
	if pl+1 > len(plain) {
		return nil, errors.New("ssh: invalid padding")
	}
	return plain[1 : len(plain)-pl], nil
}
