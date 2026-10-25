package sshc

import (
	"crypto"
	"crypto/ecdh"
	"crypto/rand"
	"errors"
	"fmt"
	"hash"
	"math/big"
)

var supportedKex = []string{
	"curve25519-sha256", "curve25519-sha256@libssh.org",
	"ecdh-sha2-nistp256", "ecdh-sha2-nistp384", "ecdh-sha2-nistp521",
	"diffie-hellman-group14-sha256", "diffie-hellman-group14-sha1",
}

var supportedHostKeys = []string{
	"ssh-ed25519",
	"ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521",
	"rsa-sha2-512", "rsa-sha2-256", "ssh-rsa",
}

// RFC 3526 group 14 (2048-bit MODP).
var group14P, _ = new(big.Int).SetString(
	"FFFFFFFFFFFFFFFFC90FDAA22168C234C4C6628B80DC1CD129024E088A67CC74"+
		"020BBEA63B139B22514A08798E3404DDEF9519B3CD3A431B302B0A6DF25F1437"+
		"4FE1356D6D51C245E485B576625E7EC6F44C42E9A637ED6B0BFF5CB6F406B7ED"+
		"EE386BFB5A899FA5AE9F24117C4B1FE649286651ECE45B3DC2007CB8A163BF05"+
		"98DA48361C55D39A69163FA8FD24CF5F83655D23DCA3AD961C62F356208552BB"+
		"9ED529077096966D670C354E4ABC9804F1746C08CA18217C32905E462E36CE3B"+
		"E39E772C180E86039B2783A2EC07A28FB5C55DF06F4C52C9DE2BCBF695581718"+
		"3995497CEA956AE515D2261898FA051015728E5A8AACAA68FFFFFFFFFFFFFFFF", 16)

func kexHash(alg string) crypto.Hash {
	switch alg {
	case "ecdh-sha2-nistp384":
		return crypto.SHA384
	case "ecdh-sha2-nistp521":
		return crypto.SHA512
	case "diffie-hellman-group14-sha1":
		return crypto.SHA1
	}
	return crypto.SHA256
}

// kexMethod produces our ephemeral public value and, given the server's
// value, the shared secret K already encoded as an SSH mpint.
type kexMethod interface {
	clientPublic() []byte // encoded as it appears on the wire (string or mpint body)
	isDH() bool
	shared(serverPub []byte) ([]byte, error) // returns mpint-encoded K
}

type ecdhKex struct{ priv *ecdh.PrivateKey }

func (k *ecdhKex) clientPublic() []byte { return k.priv.PublicKey().Bytes() }
func (k *ecdhKex) isDH() bool           { return false }
func (k *ecdhKex) shared(serverPub []byte) ([]byte, error) {
	pub, err := k.priv.Curve().NewPublicKey(serverPub)
	if err != nil {
		return nil, fmt.Errorf("ssh: invalid server key exchange value: %w", err)
	}
	s, err := k.priv.ECDH(pub)
	if err != nil {
		return nil, err
	}
	return appendMpintBytes(nil, s), nil
}

type dhKex struct{ x, e *big.Int }

func (k *dhKex) clientPublic() []byte { return k.e.Bytes() }
func (k *dhKex) isDH() bool           { return true }
func (k *dhKex) shared(serverPub []byte) ([]byte, error) {
	f := new(big.Int).SetBytes(serverPub)
	one := big.NewInt(1)
	pm1 := new(big.Int).Sub(group14P, one)
	if f.Cmp(one) <= 0 || f.Cmp(pm1) >= 0 {
		return nil, errors.New("ssh: invalid DH value from server")
	}
	return appendMpint(nil, new(big.Int).Exp(f, k.x, group14P)), nil
}

func newKexMethod(alg string) (kexMethod, error) {
	var curve ecdh.Curve
	switch alg {
	case "curve25519-sha256", "curve25519-sha256@libssh.org":
		curve = ecdh.X25519()
	case "ecdh-sha2-nistp256":
		curve = ecdh.P256()
	case "ecdh-sha2-nistp384":
		curve = ecdh.P384()
	case "ecdh-sha2-nistp521":
		curve = ecdh.P521()
	case "diffie-hellman-group14-sha256", "diffie-hellman-group14-sha1":
		xb := make([]byte, 64)
		rand.Read(xb)
		x := new(big.Int).SetBytes(xb)
		return &dhKex{x: x, e: new(big.Int).Exp(big.NewInt(2), x, group14P)}, nil
	default:
		return nil, fmt.Errorf("ssh: unsupported key exchange %s", alg)
	}
	priv, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &ecdhKex{priv: priv}, nil
}

// deriveKey implements RFC 4253 section 7.2.
func deriveKey(h crypto.Hash, K, H, sessionID []byte, letter byte, n int) []byte {
	newH := func() hash.Hash { return h.New() }
	d := newH()
	d.Write(K)
	d.Write(H)
	d.Write([]byte{letter})
	d.Write(sessionID)
	out := d.Sum(nil)
	for len(out) < n {
		d = newH()
		d.Write(K)
		d.Write(H)
		d.Write(out)
		out = d.Sum(out)
	}
	return out[:n]
}

type algorithms struct {
	kex, hostKey         string
	cipherC2S, cipherS2C string
	macC2S, macS2C       string
}

func choose(client, server []string, what string) (string, error) {
	for _, c := range client {
		for _, s := range server {
			if c == s {
				return c, nil
			}
		}
	}
	return "", fmt.Errorf("ssh: no common %s algorithm (server offers: %v)", what, server)
}

type kexInitMsg struct {
	kex, hostKey, ciphersC2S, ciphersS2C, macsC2S, macsS2C, compC2S, compS2C []string
	firstFollows                                                             bool
}

func parseKexInit(p []byte) (*kexInitMsg, error) {
	r := reader{b: p[1:]}
	if len(r.b) < 16 {
		return nil, errShort
	}
	r.b = r.b[16:] // cookie
	m := &kexInitMsg{}
	m.kex = r.nameList()
	m.hostKey = r.nameList()
	m.ciphersC2S = r.nameList()
	m.ciphersS2C = r.nameList()
	m.macsC2S = r.nameList()
	m.macsS2C = r.nameList()
	m.compC2S = r.nameList()
	m.compS2C = r.nameList()
	r.nameList()
	r.nameList()
	m.firstFollows = r.boolean()
	if r.err != nil {
		return nil, r.err
	}
	return m, nil
}

func negotiate(s *kexInitMsg) (algorithms, error) {
	var a algorithms
	var err error
	if a.kex, err = choose(supportedKex, s.kex, "key exchange"); err != nil {
		return a, err
	}
	if a.hostKey, err = choose(supportedHostKeys, s.hostKey, "host key"); err != nil {
		return a, err
	}
	if a.cipherC2S, err = choose(supportedCiphers, s.ciphersC2S, "cipher"); err != nil {
		return a, err
	}
	if a.cipherS2C, err = choose(supportedCiphers, s.ciphersS2C, "cipher"); err != nil {
		return a, err
	}
	if !cipherSpecs[a.cipherC2S].gcm {
		if a.macC2S, err = choose(supportedMACs, s.macsC2S, "MAC"); err != nil {
			return a, err
		}
	}
	if !cipherSpecs[a.cipherS2C].gcm {
		if a.macS2C, err = choose(supportedMACs, s.macsS2C, "MAC"); err != nil {
			return a, err
		}
	}
	if _, err = choose([]string{"none"}, s.compC2S, "compression"); err != nil {
		return a, err
	}
	if _, err = choose([]string{"none"}, s.compS2C, "compression"); err != nil {
		return a, err
	}
	return a, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
