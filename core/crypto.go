package core

import (
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
)

var (
	ErrWrongPassword = errors.New("hibás jelszó (vagy sérült kulcsfájl)")
	ErrCorrupt       = errors.New("a fájl sérült vagy módosították")
	ErrFormat        = errors.New("ismeretlen fájlformátum")
)

// KDFParams – Argon2id paraméterek. M KiB-ban.
type KDFParams struct {
	M    uint32 `json:"m"`
	T    uint32 `json:"t"`
	P    uint8  `json:"p"`
	Salt []byte `json:"salt"`
}

// DefaultKDF: 256 MB memória, 3 kör, 4 szál (~0,5–1,5 s egy mai PC-n).
var DefaultKDF = KDFParams{M: 256 * 1024, T: 3, P: 4}

func (k KDFParams) Derive(password string) []byte {
	return argon2.IDKey([]byte(password), k.Salt, k.T, k.M, k.P, 32)
}

// seal: XChaCha20-Poly1305, kimenet = nonce(24) || titkosított || tag(16)
func seal(key, plaintext, ad []byte, rng io.Reader) ([]byte, error) {
	a, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, chacha20poly1305.NonceSizeX, chacha20poly1305.NonceSizeX+len(plaintext)+a.Overhead())
	if _, err := io.ReadFull(rng, nonce); err != nil {
		return nil, err
	}
	return a.Seal(nonce, nonce, plaintext, ad), nil
}

func open(key, data, ad []byte) ([]byte, error) {
	a, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	if len(data) < chacha20poly1305.NonceSizeX+a.Overhead() {
		return nil, ErrCorrupt
	}
	pt, err := a.Open(nil, data[:chacha20poly1305.NonceSizeX], data[chacha20poly1305.NonceSizeX:], ad)
	if err != nil {
		return nil, ErrCorrupt
	}
	return pt, nil
}

func x25519(priv, pub []byte) ([]byte, error) {
	k, err := ecdh.X25519().NewPrivateKey(priv)
	if err != nil {
		return nil, err
	}
	p, err := ecdh.X25519().NewPublicKey(pub)
	if err != nil {
		return nil, err
	}
	return k.ECDH(p)
}

func x25519Pub(priv []byte) []byte {
	k, err := ecdh.X25519().NewPrivateKey(priv)
	if err != nil {
		panic(err)
	}
	return k.PublicKey().Bytes()
}

func hkdf32(secret, salt []byte, info string) []byte {
	k, err := hkdf.Key(sha256.New, secret, salt, info, 32)
	if err != nil {
		panic(err)
	}
	return k
}

// lp: hossz-előtagolt mező az aláírt bájtsorokhoz.
func lp(dst []byte, b []byte) []byte {
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(b)))
	dst = append(dst, l[:]...)
	return append(dst, b...)
}
