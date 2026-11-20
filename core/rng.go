// Package core – RealPassGAL kriptográfiai mag (platformfüggetlen).
package core

import (
	"crypto/rand"
	"crypto/sha512"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"strings"

	"golang.org/x/crypto/chacha20"
)

// MinEntropyBits – ennyi egérmozdulatot (≈ bitet) kell gyűjteni.
const MinEntropyBits = 512

// EntropyPool az egérmozgásból gyűjtött véletlent keveri SHA-512-vel.
type EntropyPool struct {
	h      hash.Hash
	events int
	lastX  int
	lastY  int
}

func NewEntropyPool() *EntropyPool {
	p := &EntropyPool{h: sha512.New(), lastX: -1, lastY: -1}
	p.h.Write([]byte("RealPassGAL v1 eger-pool"))
	var os [32]byte
	rand.Read(os[:])
	p.h.Write(os[:])
	return p
}

// AddMouse egy egéresemény; true, ha új pozíció volt (1 bitnek számoljuk).
func (p *EntropyPool) AddMouse(x, y int, nanos int64) bool {
	var b [24]byte
	binary.LittleEndian.PutUint64(b[0:], uint64(int64(x)))
	binary.LittleEndian.PutUint64(b[8:], uint64(int64(y)))
	binary.LittleEndian.PutUint64(b[16:], uint64(nanos))
	p.h.Write(b[:])
	if x == p.lastX && y == p.lastY {
		return false
	}
	p.lastX, p.lastY = x, y
	p.events++
	return true
}

func (p *EntropyPool) Bits() int { return p.events }

// Digest a pool aktuális kivonata (64 bájt).
func (p *EntropyPool) Digest() []byte { return p.h.Sum(nil) }

// Display a „pörgő karakterek”: a kivonat első 24 bájtja hexában, 3 sorban.
func (p *EntropyPool) Display() string {
	d := p.Digest()[:24]
	s := strings.ToUpper(hex.EncodeToString(d))
	var sb strings.Builder
	for i := 0; i < len(s); i += 2 {
		sb.WriteString(s[i : i+2])
		if (i/2+1)%8 == 0 {
			sb.WriteString("\r\n")
		} else {
			sb.WriteByte(' ')
		}
	}
	return strings.TrimSpace(sb.String())
}

// RNG = Windows/OS véletlen XOR ChaCha20(mag), ahol a mag az OS véletlen
// és a megadott extra (egér, tartalék) SHA-512 keveréke. Ha bármelyik forrás
// jó, a kimenet jó.
type RNG struct{ c *chacha20.Cipher }

func NewRNG(extra ...[]byte) (*RNG, error) {
	var osr [64]byte
	if _, err := rand.Read(osr[:]); err != nil {
		return nil, err
	}
	h := sha512.New()
	h.Write([]byte("RealPassGAL v1 RNG"))
	h.Write(osr[:])
	for _, e := range extra {
		var l [8]byte
		binary.LittleEndian.PutUint64(l[:], uint64(len(e)))
		h.Write(l[:])
		h.Write(e)
	}
	seed := h.Sum(nil)
	c, err := chacha20.NewUnauthenticatedCipher(seed[:32], seed[32:44])
	if err != nil {
		return nil, err
	}
	wipe(seed)
	return &RNG{c: c}, nil
}

func (r *RNG) Read(b []byte) (int, error) {
	if _, err := rand.Read(b); err != nil {
		return 0, err
	}
	r.c.XORKeyStream(b, b)
	return len(b), nil
}

func (r *RNG) Bytes(n int) []byte {
	b := make([]byte, n)
	r.Read(b)
	return b
}

func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
