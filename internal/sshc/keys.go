package sshc

import (
	"bytes"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// ErrPassphraseNeeded is returned by ParsePrivateKey for encrypted keys when
// no passphrase (or a wrong one) was supplied.
var ErrPassphraseNeeded = errors.New("this private key is protected by a passphrase")

// Fingerprint returns the OpenSSH-style SHA256 fingerprint of a public key blob.
func Fingerprint(blob []byte) string {
	h := sha256.Sum256(blob)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(h[:])
}

// KeyType returns the algorithm name embedded in a public key blob.
func KeyType(blob []byte) string {
	r := reader{b: blob}
	return r.sstr()
}

func curveFor(name string) (elliptic.Curve, crypto.Hash) {
	switch name {
	case "nistp256":
		return elliptic.P256(), crypto.SHA256
	case "nistp384":
		return elliptic.P384(), crypto.SHA384
	case "nistp521":
		return elliptic.P521(), crypto.SHA512
	}
	return nil, 0
}

func hashOf(h crypto.Hash, data []byte) []byte {
	switch h {
	case crypto.SHA1:
		s := sha1.Sum(data)
		return s[:]
	case crypto.SHA256:
		s := sha256.Sum256(data)
		return s[:]
	case crypto.SHA384:
		s := sha512.Sum384(data)
		return s[:]
	case crypto.SHA512:
		s := sha512.Sum512(data)
		return s[:]
	}
	return nil
}

// verifyHostSignature checks the server's signature over the exchange hash.
// alg is the negotiated host key algorithm.
func verifyHostSignature(alg string, keyBlob, sigBlob, data []byte) error {
	kr := reader{b: keyBlob}
	keyType := kr.sstr()
	sr := reader{b: sigBlob}
	sigType := sr.sstr()
	sig := sr.str()
	if kr.err != nil || sr.err != nil {
		return errShort
	}
	if sigType != alg {
		return fmt.Errorf("ssh: host signature type %s does not match %s", sigType, alg)
	}
	switch keyType {
	case "ssh-ed25519":
		pk := kr.str()
		if kr.err != nil || len(pk) != ed25519.PublicKeySize {
			return errShort
		}
		if !ed25519.Verify(ed25519.PublicKey(pk), data, sig) {
			return errors.New("ssh: host key signature invalid")
		}
		return nil
	case "ssh-rsa":
		e := kr.mpint()
		n := kr.mpint()
		if kr.err != nil || !e.IsInt64() {
			return errShort
		}
		pub := &rsa.PublicKey{N: n, E: int(e.Int64())}
		var h crypto.Hash
		switch alg {
		case "rsa-sha2-256":
			h = crypto.SHA256
		case "rsa-sha2-512":
			h = crypto.SHA512
		case "ssh-rsa":
			h = crypto.SHA1
		default:
			return fmt.Errorf("ssh: bad RSA signature algorithm %s", alg)
		}
		if err := rsa.VerifyPKCS1v15(pub, h, hashOf(h, data), sig); err != nil {
			return errors.New("ssh: host key signature invalid")
		}
		return nil
	}
	if strings.HasPrefix(keyType, "ecdsa-sha2-") {
		curve, h := curveFor(kr.sstr())
		q := kr.str()
		if curve == nil || kr.err != nil {
			return errors.New("ssh: unsupported ECDSA curve")
		}
		//lint:ignore SA1019 elliptic.Unmarshal is the simplest route for SEC1 points
		x, y := elliptic.Unmarshal(curve, q) //nolint:staticcheck
		if x == nil {
			return errors.New("ssh: invalid ECDSA point")
		}
		rr := reader{b: sig}
		rs, ss := rr.mpint(), rr.mpint()
		if rr.err != nil {
			return errShort
		}
		if !ecdsa.Verify(&ecdsa.PublicKey{Curve: curve, X: x, Y: y}, hashOf(h, data), rs, ss) {
			return errors.New("ssh: host key signature invalid")
		}
		return nil
	}
	return fmt.Errorf("ssh: unsupported host key type %s", keyType)
}

// Signer is a private key usable for public key authentication.
type Signer interface {
	PublicBlob() []byte
	// Algorithms returns the signature algorithms usable with this key, best first.
	Algorithms() []string
	Sign(alg string, data []byte) ([]byte, error)
}

type ed25519Signer struct{ k ed25519.PrivateKey }

func (s ed25519Signer) PublicBlob() []byte {
	return newMsg(0).sstr("ssh-ed25519").str(s.k.Public().(ed25519.PublicKey)).b[1:]
}
func (s ed25519Signer) Algorithms() []string { return []string{"ssh-ed25519"} }
func (s ed25519Signer) Sign(alg string, data []byte) ([]byte, error) {
	return newMsg(0).sstr("ssh-ed25519").str(ed25519.Sign(s.k, data)).b[1:], nil
}

type rsaSigner struct{ k *rsa.PrivateKey }

func (s rsaSigner) PublicBlob() []byte {
	return newMsg(0).sstr("ssh-rsa").mpint(big.NewInt(int64(s.k.E))).mpint(s.k.N).b[1:]
}
func (s rsaSigner) Algorithms() []string {
	return []string{"rsa-sha2-512", "rsa-sha2-256", "ssh-rsa"}
}
func (s rsaSigner) Sign(alg string, data []byte) ([]byte, error) {
	h := crypto.SHA512
	switch alg {
	case "rsa-sha2-256":
		h = crypto.SHA256
	case "ssh-rsa":
		h = crypto.SHA1
	}
	sig, err := rsa.SignPKCS1v15(rand.Reader, s.k, h, hashOf(h, data))
	if err != nil {
		return nil, err
	}
	return newMsg(0).sstr(alg).str(sig).b[1:], nil
}

type ecdsaSigner struct {
	k     *ecdsa.PrivateKey
	curve string
}

func (s ecdsaSigner) alg() string { return "ecdsa-sha2-" + s.curve }
func (s ecdsaSigner) PublicBlob() []byte {
	//nolint:staticcheck
	q := elliptic.Marshal(s.k.Curve, s.k.X, s.k.Y)
	return newMsg(0).sstr(s.alg()).sstr(s.curve).str(q).b[1:]
}
func (s ecdsaSigner) Algorithms() []string { return []string{s.alg()} }
func (s ecdsaSigner) Sign(alg string, data []byte) ([]byte, error) {
	_, h := curveFor(s.curve)
	r, ss, err := ecdsa.Sign(rand.Reader, s.k, hashOf(h, data))
	if err != nil {
		return nil, err
	}
	inner := newMsg(0).mpint(r).mpint(ss).b[1:]
	return newMsg(0).sstr(s.alg()).str(inner).b[1:], nil
}

func curveName(c elliptic.Curve) string {
	switch c {
	case elliptic.P256():
		return "nistp256"
	case elliptic.P384():
		return "nistp384"
	case elliptic.P521():
		return "nistp521"
	}
	return ""
}

func signerFromKey(k any) (Signer, error) {
	switch key := k.(type) {
	case ed25519.PrivateKey:
		return ed25519Signer{key}, nil
	case *ed25519.PrivateKey:
		return ed25519Signer{*key}, nil
	case *rsa.PrivateKey:
		return rsaSigner{key}, nil
	case *ecdsa.PrivateKey:
		n := curveName(key.Curve)
		if n == "" {
			return nil, errors.New("unsupported ECDSA curve")
		}
		return ecdsaSigner{key, n}, nil
	}
	return nil, fmt.Errorf("unsupported private key type %T", k)
}

// ParsePrivateKey reads OpenSSH, PEM (PKCS#1, SEC1, PKCS#8) and PuTTY .ppk
// private keys.
func ParsePrivateKey(data []byte, passphrase string) (Signer, error) {
	text := strings.TrimSpace(string(data))
	if strings.HasPrefix(text, "PuTTY-User-Key-File-") {
		return parsePPK(text, passphrase)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("unrecognised private key format")
	}
	switch block.Type {
	case "OPENSSH PRIVATE KEY":
		return parseOpenSSHKey(block.Bytes, passphrase)
	}
	der := block.Bytes
	//nolint:staticcheck // legacy encrypted PEM is still common for RSA keys
	if x509.IsEncryptedPEMBlock(block) {
		if passphrase == "" {
			return nil, ErrPassphraseNeeded
		}
		d, err := x509.DecryptPEMBlock(block, []byte(passphrase)) //nolint:staticcheck
		if err != nil {
			return nil, ErrPassphraseNeeded
		}
		der = d
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		k, err := x509.ParsePKCS1PrivateKey(der)
		if err != nil {
			return nil, err
		}
		return signerFromKey(k)
	case "EC PRIVATE KEY":
		k, err := x509.ParseECPrivateKey(der)
		if err != nil {
			return nil, err
		}
		return signerFromKey(k)
	case "PRIVATE KEY":
		k, err := x509.ParsePKCS8PrivateKey(der)
		if err != nil {
			return nil, err
		}
		return signerFromKey(k)
	case "ENCRYPTED PRIVATE KEY":
		return nil, errors.New("encrypted PKCS#8 keys are not supported; convert the key with ssh-keygen or PuTTYgen")
	}
	return nil, fmt.Errorf("unsupported key type %q", block.Type)
}

func parseOpenSSHKey(b []byte, passphrase string) (Signer, error) {
	const magic = "openssh-key-v1\x00"
	if !bytes.HasPrefix(b, []byte(magic)) {
		return nil, errors.New("invalid OpenSSH key")
	}
	r := reader{b: b[len(magic):]}
	cipherName := r.sstr()
	kdf := r.sstr()
	r.str() // kdf options
	n := r.u32()
	if r.err != nil || n != 1 {
		return nil, errors.New("invalid OpenSSH key")
	}
	r.str() // public key
	priv := r.str()
	if r.err != nil {
		return nil, errShort
	}
	if cipherName != "none" || kdf != "none" {
		return nil, errors.New("passphrase-protected OpenSSH keys need converting: run  ssh-keygen -p -N \"\" -f <keyfile>  on a copy, or export the key from PuTTYgen as a .ppk (version 2) file")
	}
	pr := reader{b: priv}
	c1, c2 := pr.u32(), pr.u32()
	if c1 != c2 {
		return nil, ErrPassphraseNeeded
	}
	kt := pr.sstr()
	switch kt {
	case "ssh-ed25519":
		pr.str()
		sk := pr.str()
		if pr.err != nil || len(sk) != ed25519.PrivateKeySize {
			return nil, errShort
		}
		return ed25519Signer{ed25519.PrivateKey(append([]byte(nil), sk...))}, nil
	case "ssh-rsa":
		nn, e, d, iqmp, p, q := pr.mpint(), pr.mpint(), pr.mpint(), pr.mpint(), pr.mpint(), pr.mpint()
		_ = iqmp
		if pr.err != nil {
			return nil, errShort
		}
		k := &rsa.PrivateKey{PublicKey: rsa.PublicKey{N: nn, E: int(e.Int64())}, D: d, Primes: []*big.Int{p, q}}
		if err := k.Validate(); err != nil {
			return nil, err
		}
		k.Precompute()
		return rsaSigner{k}, nil
	}
	if strings.HasPrefix(kt, "ecdsa-sha2-") {
		cn := pr.sstr()
		curve, _ := curveFor(cn)
		q := pr.str()
		d := pr.mpint()
		if curve == nil || pr.err != nil {
			return nil, errors.New("unsupported ECDSA key")
		}
		x, y := elliptic.Unmarshal(curve, q) //nolint:staticcheck
		if x == nil {
			return nil, errors.New("invalid ECDSA key")
		}
		k := &ecdsa.PrivateKey{PublicKey: ecdsa.PublicKey{Curve: curve, X: x, Y: y}, D: d}
		return ecdsaSigner{k, cn}, nil
	}
	return nil, fmt.Errorf("unsupported key type %s", kt)
}

// --- PuTTY .ppk (versions 2 and 3, unencrypted; version 2 encrypted) ---

func parsePPK(text, passphrase string) (Signer, error) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	hdr := map[string]string{}
	var pubB64, privB64 strings.Builder
	version := 0
	for i := 0; i < len(lines); i++ {
		k, v, ok := strings.Cut(lines[i], ": ")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch {
		case strings.HasPrefix(k, "PuTTY-User-Key-File-"):
			fmt.Sscanf(k, "PuTTY-User-Key-File-%d", &version)
			hdr["Algorithm"] = v
		case k == "Public-Lines" || k == "Private-Lines":
			var n int
			fmt.Sscanf(v, "%d", &n)
			dst := &pubB64
			if k == "Private-Lines" {
				dst = &privB64
			}
			for j := 0; j < n && i+1 < len(lines); j++ {
				i++
				dst.WriteString(strings.TrimSpace(lines[i]))
			}
		default:
			hdr[k] = v
		}
	}
	pub, err := base64.StdEncoding.DecodeString(pubB64.String())
	if err != nil {
		return nil, errors.New("invalid .ppk public key")
	}
	priv, err := base64.StdEncoding.DecodeString(privB64.String())
	if err != nil {
		return nil, errors.New("invalid .ppk private key")
	}
	enc := hdr["Encryption"]
	if enc != "none" {
		if version != 2 || enc != "aes256-cbc" {
			return nil, errors.New("passphrase-protected PuTTY v3 keys aren't supported; in PuTTYgen choose Key > Parameters > PPK version 2, or save without a passphrase")
		}
		if passphrase == "" {
			return nil, ErrPassphraseNeeded
		}
		// PuTTY v2 key derivation: SHA1(seq||passphrase) for seq 0,1.
		var key []byte
		for seq := 0; seq < 2; seq++ {
			h := sha1.New()
			h.Write([]byte{0, 0, 0, byte(seq)})
			h.Write([]byte(passphrase))
			key = h.Sum(key)
		}
		blk, _ := aes.NewCipher(key[:32])
		if len(priv)%16 != 0 {
			return nil, errors.New("invalid .ppk private key")
		}
		cipher.NewCBCDecrypter(blk, make([]byte, 16)).CryptBlocks(priv, priv)
		// Verify MAC to detect a wrong passphrase.
		mk := sha1.Sum([]byte("putty-private-key-file-mac-key" + passphrase))
		m := hmac.New(sha1.New, mk[:])
		for _, f := range [][]byte{[]byte(hdr["Algorithm"]), []byte(enc), []byte(hdr["Comment"]), pub, priv} {
			var l [4]byte
			l[0], l[1], l[2], l[3] = byte(len(f)>>24), byte(len(f)>>16), byte(len(f)>>8), byte(len(f))
			m.Write(l[:])
			m.Write(f)
		}
		if want, _ := hex.DecodeString(hdr["Private-MAC"]); !hmac.Equal(m.Sum(nil), want) {
			return nil, ErrPassphraseNeeded
		}
	}
	pr := reader{b: pub}
	alg := pr.sstr()
	vr := reader{b: priv}
	switch alg {
	case "ssh-ed25519":
		pk := pr.str()
		sk := vr.str()
		if pr.err != nil || vr.err != nil || len(sk) != 32 {
			return nil, errShort
		}
		k := ed25519.NewKeyFromSeed(sk)
		if !bytes.Equal(k.Public().(ed25519.PublicKey), pk) {
			return nil, errors.New("ppk key mismatch")
		}
		return ed25519Signer{k}, nil
	case "ssh-rsa":
		e, n := pr.mpint(), pr.mpint()
		d, p, q := vr.mpint(), vr.mpint(), vr.mpint()
		if pr.err != nil || vr.err != nil {
			return nil, errShort
		}
		k := &rsa.PrivateKey{PublicKey: rsa.PublicKey{N: n, E: int(e.Int64())}, D: d, Primes: []*big.Int{p, q}}
		if err := k.Validate(); err != nil {
			return nil, err
		}
		k.Precompute()
		return rsaSigner{k}, nil
	}
	if strings.HasPrefix(alg, "ecdsa-sha2-") {
		cn := pr.sstr()
		curve, _ := curveFor(cn)
		q := pr.str()
		d := vr.mpint()
		if curve == nil || pr.err != nil || vr.err != nil {
			return nil, errors.New("unsupported ECDSA key")
		}
		x, y := elliptic.Unmarshal(curve, q) //nolint:staticcheck
		if x == nil {
			return nil, errors.New("invalid ECDSA key")
		}
		return ecdsaSigner{&ecdsa.PrivateKey{PublicKey: ecdsa.PublicKey{Curve: curve, X: x, Y: y}, D: d}, cn}, nil
	}
	return nil, fmt.Errorf("unsupported .ppk key type %s", alg)
}
