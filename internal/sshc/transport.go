package sshc

import (
	"bufio"
	"bytes"
	"crypto"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
)

const clientVersion = "SSH-2.0-ShoreDrop_1.0.0"

// HostKeyCallback decides whether to trust the server's host key. blob is
// the raw public key; it is called only during the first key exchange.
type HostKeyCallback func(keyType string, blob []byte) error

type transport struct {
	conn net.Conn
	br   *bufio.Reader

	wmu      sync.Mutex // serialises writes; held for the duration of a key exchange
	writeC   packetCipher
	writeSeq uint32

	readC   packetCipher
	readSeq uint32

	serverVersion []byte
	sessionID     []byte
	hostKey       []byte
	hostKeyCB     HostKeyCallback
	strict        bool
	kexCount      int
	serverSigAlgs []string
	algs          algorithms
}

func newTransport(conn net.Conn, cb HostKeyCallback) *transport {
	return &transport{conn: conn, br: bufio.NewReaderSize(conn, 64*1024), writeC: noneCipher{}, readC: noneCipher{}, hostKeyCB: cb}
}

func (t *transport) exchangeVersions() error {
	if _, err := t.conn.Write([]byte(clientVersion + "\r\n")); err != nil {
		return err
	}
	for i := 0; i < 64; i++ {
		line, err := t.br.ReadSlice('\n')
		if err != nil {
			if errors.Is(err, bufio.ErrBufferFull) {
				return errors.New("ssh: server sent an overlong banner line")
			}
			return fmt.Errorf("ssh: no SSH banner from server (is this an SSH port?): %w", err)
		}
		line = bytes.TrimRight(line, "\r\n")
		if bytes.HasPrefix(line, []byte("SSH-")) {
			if !bytes.HasPrefix(line, []byte("SSH-2.0-")) && !bytes.HasPrefix(line, []byte("SSH-1.99-")) {
				return fmt.Errorf("ssh: unsupported protocol version %q", line)
			}
			t.serverVersion = append([]byte(nil), line...)
			return nil
		}
	}
	return errors.New("ssh: server did not send an SSH banner (is this an SSH/SFTP port?)")
}

// writeLocked sends one packet; caller must hold wmu.
func (t *transport) writeLocked(payload []byte) error {
	pkt := t.writeC.seal(t.writeSeq, payload)
	t.writeSeq++
	_, err := t.conn.Write(pkt)
	return err
}

func (t *transport) writePacket(payload []byte) error {
	t.wmu.Lock()
	defer t.wmu.Unlock()
	return t.writeLocked(payload)
}

func (t *transport) readRaw() ([]byte, error) {
	p, err := t.readC.open(t.readSeq, t.br)
	t.readSeq++
	if err != nil {
		return nil, err
	}
	if len(p) == 0 {
		return nil, errShort
	}
	return p, nil
}

// readPacket returns the next higher-layer packet, transparently handling
// transport messages and server-initiated re-keying.
func (t *transport) readPacket() ([]byte, error) {
	for {
		p, err := t.readRaw()
		if err != nil {
			return nil, err
		}
		switch p[0] {
		case msgIgnore, msgDebug, msgUnimplemented:
			continue
		case msgDisconnect:
			r := reader{b: p[1:]}
			r.u32()
			return nil, fmt.Errorf("server closed the connection: %s", r.sstr())
		case msgKexInit:
			if err := t.kex(p); err != nil {
				return nil, err
			}
			continue
		case msgExtInfo:
			t.parseExtInfo(p)
			continue
		}
		return p, nil
	}
}

func (t *transport) parseExtInfo(p []byte) {
	r := reader{b: p[1:]}
	n := r.u32()
	for i := uint32(0); i < n && r.err == nil; i++ {
		name, val := r.sstr(), r.sstr()
		if name == "server-sig-algs" {
			t.serverSigAlgs = strings.Split(val, ",")
		}
	}
}

func (t *transport) buildKexInit() []byte {
	kex := append([]string(nil), supportedKex...)
	if t.kexCount == 0 {
		kex = append(kex, "ext-info-c", "kex-strict-c-v00@openssh.com")
	}
	cookie := make([]byte, 16)
	rand.Read(cookie)
	m := newMsg(msgKexInit).raw(cookie)
	m.sstr(strings.Join(kex, ","))
	m.sstr(strings.Join(supportedHostKeys, ","))
	m.sstr(strings.Join(supportedCiphers, ","))
	m.sstr(strings.Join(supportedCiphers, ","))
	m.sstr(strings.Join(supportedMACs, ","))
	m.sstr(strings.Join(supportedMACs, ","))
	m.sstr("none").sstr("none").sstr("").sstr("")
	m.boolean(false).u32(0)
	return m.b
}

// kex performs a key exchange. serverInit is the server's KEXINIT if it
// initiated the exchange, or nil if we start it.
func (t *transport) kex(serverInit []byte) error {
	t.wmu.Lock()
	defer t.wmu.Unlock()
	first := t.kexCount == 0

	clientInit := t.buildKexInit()
	if err := t.writeLocked(clientInit); err != nil {
		return err
	}
	if serverInit == nil {
		for {
			p, err := t.readRaw()
			if err != nil {
				return err
			}
			if p[0] == msgKexInit {
				serverInit = p
				break
			}
			if p[0] != msgIgnore && p[0] != msgDebug {
				return fmt.Errorf("ssh: unexpected message %d before key exchange", p[0])
			}
		}
	}
	sk, err := parseKexInit(serverInit)
	if err != nil {
		return err
	}
	if first && contains(sk.kex, "kex-strict-s-v00@openssh.com") {
		t.strict = true
		if t.readSeq != 1 {
			return errors.New("ssh: strict key exchange violated by server")
		}
	}
	algs, err := negotiate(sk)
	if err != nil {
		return err
	}
	ignoreGuess := sk.firstFollows && (len(sk.kex) == 0 || sk.kex[0] != algs.kex || len(sk.hostKey) == 0 || sk.hostKey[0] != algs.hostKey)

	method, err := newKexMethod(algs.kex)
	if err != nil {
		return err
	}
	init := newMsg(msgKexDHInit)
	if method.isDH() {
		init.mpint(method.(*dhKex).e)
	} else {
		init.str(method.clientPublic())
	}
	if err := t.writeLocked(init.b); err != nil {
		return err
	}

	readKexMsg := func() ([]byte, error) {
		for {
			p, err := t.readRaw()
			if err != nil {
				return nil, err
			}
			if ignoreGuess {
				ignoreGuess = false
				continue
			}
			switch p[0] {
			case msgIgnore, msgDebug:
				if t.strict && first {
					return nil, errors.New("ssh: unexpected message during strict key exchange")
				}
				continue
			case msgDisconnect:
				r := reader{b: p[1:]}
				r.u32()
				return nil, fmt.Errorf("server closed the connection: %s", r.sstr())
			}
			return p, nil
		}
	}

	reply, err := readKexMsg()
	if err != nil {
		return err
	}
	if reply[0] != msgKexDHReply {
		return fmt.Errorf("ssh: expected key exchange reply, got message %d", reply[0])
	}
	rr := reader{b: reply[1:]}
	hostKeyBlob := rr.str()
	var serverPub []byte
	var fInt []byte
	if method.isDH() {
		fInt = rr.str() // raw mpint body
		serverPub = fInt
	} else {
		serverPub = rr.str()
	}
	sigBlob := rr.str()
	if rr.err != nil {
		return rr.err
	}
	K, err := method.shared(serverPub)
	if err != nil {
		return err
	}
	hf := kexHash(algs.kex)
	h := hf.New()
	hb := newMsg(0)
	hb.sstr(clientVersion).str(t.serverVersion).str(clientInit).str(serverInit).str(hostKeyBlob)
	if method.isDH() {
		hb.mpint(method.(*dhKex).e)
		hb.raw(appendMpintBytes(nil, fInt))
	} else {
		hb.str(method.clientPublic()).str(serverPub)
	}
	hb.raw(K)
	h.Write(hb.b[1:])
	H := h.Sum(nil)

	if err := verifyHostSignature(algs.hostKey, hostKeyBlob, sigBlob, H); err != nil {
		return err
	}
	if first {
		if t.hostKeyCB != nil {
			if err := t.hostKeyCB(KeyType(hostKeyBlob), hostKeyBlob); err != nil {
				return err
			}
		}
		t.sessionID = H
		t.hostKey = hostKeyBlob
	} else if !bytes.Equal(hostKeyBlob, t.hostKey) {
		return errors.New("ssh: host key changed during re-key")
	}

	if err := t.writeLocked([]byte{msgNewKeys}); err != nil {
		return err
	}
	wc, err := t.makeCipher(hf, K, H, algs.cipherC2S, algs.macC2S, 'A', 'C', 'E')
	if err != nil {
		return err
	}
	t.writeC = wc
	if t.strict {
		t.writeSeq = 0
	}
	nk, err := readKexMsg()
	if err != nil {
		return err
	}
	if nk[0] != msgNewKeys {
		return fmt.Errorf("ssh: expected NEWKEYS, got message %d", nk[0])
	}
	rc, err := t.makeCipher(hf, K, H, algs.cipherS2C, algs.macS2C, 'B', 'D', 'F')
	if err != nil {
		return err
	}
	t.readC = rc
	if t.strict {
		t.readSeq = 0
	}
	t.algs = algs
	t.kexCount++
	return nil
}

func (t *transport) makeCipher(hf crypto.Hash, K, H []byte, alg, mac string, ivL, keyL, macL byte) (packetCipher, error) {
	cs := cipherSpecs[alg]
	iv := deriveKey(hf, K, H, t.sessionID, ivL, cs.ivLen)
	key := deriveKey(hf, K, H, t.sessionID, keyL, cs.keyLen)
	var macKey []byte
	if !cs.gcm {
		macKey = deriveKey(hf, K, H, t.sessionID, macL, macSpecs[mac].keyLen)
	}
	return newPacketCipher(alg, mac, key, iv, macKey)
}
