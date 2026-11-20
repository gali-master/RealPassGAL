package core

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"time"
)

const (
	KeybookEntries = 1 << 20 // 1 048 576 kulcs
	KeybookEntry   = 32
	KeybookSize    = KeybookEntries * KeybookEntry // 32 MB

	kbMagic  = "RPGALKB1" // helyi, saját tárolókulccsal titkosított
	kbxMagic = "RPGALKX1" // átadó fájl (pendrive), a partner nyilvános kulcsára lezárva
)

// Keybook – a személyesen cserélt kulcskönyv.
type Keybook struct {
	ID      [16]byte
	Created time.Time
	Data    []byte
}

func NewKeybook(rng io.Reader) (*Keybook, error) {
	k := &Keybook{Created: time.Now().UTC().Truncate(time.Second), Data: make([]byte, KeybookSize)}
	if _, err := io.ReadFull(rng, k.ID[:]); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(rng, k.Data); err != nil {
		return nil, err
	}
	return k, nil
}

func (k *Keybook) Entry(i uint32) ([]byte, error) {
	if i >= KeybookEntries {
		return nil, errors.New("a kulcskönyv elfogyott")
	}
	return k.Data[i*KeybookEntry : (i+1)*KeybookEntry], nil
}

func (k *Keybook) ShortID() string { return hex.EncodeToString(k.ID[:4]) }

// CheckCode – ezt hasonlítjátok össze átadás után: ha egyezik, ugyanaz a könyv.
func (k *Keybook) CheckCode() string {
	h := sha256.New()
	h.Write([]byte("RealPassGAL v1 kulcskonyv ellenorzo"))
	h.Write(k.ID[:])
	h.Write(k.Data)
	return formatCode("KK", h.Sum(nil)[:6])
}

func (k *Keybook) header(magic string) []byte {
	b := append([]byte(magic), k.ID[:]...)
	var t [8]byte
	binary.BigEndian.PutUint64(t[:], uint64(k.Created.Unix()))
	return append(b, t[:]...)
}

// Save – helyi tárolás a saját tárolókulccsal.
func (k *Keybook) Save(path string, storageKey []byte, rng io.Reader) error {
	h := k.header(kbMagic)
	ct, err := seal(storageKey, k.Data, h, rng)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(h, ct...))
}

func parseHeader(raw []byte, magic string) (*Keybook, []byte, error) {
	if len(raw) < 32 || string(raw[:8]) != magic {
		return nil, nil, ErrFormat
	}
	k := &Keybook{}
	copy(k.ID[:], raw[8:24])
	k.Created = time.Unix(int64(binary.BigEndian.Uint64(raw[24:32])), 0).UTC()
	return k, raw[32:], nil
}

func LoadKeybook(path string, storageKey []byte) (*Keybook, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	k, rest, err := parseHeader(raw, kbMagic)
	if err != nil {
		return nil, err
	}
	pt, err := open(storageKey, rest, raw[:32])
	if err != nil {
		return nil, ErrCorrupt
	}
	if len(pt) != KeybookSize {
		return nil, ErrCorrupt
	}
	k.Data = pt
	return k, nil
}

// ExportFor – átadó fájl a partnernek: az ő X25519 kulcsára lezárva, általam aláírva.
// Ha a pendrive elveszik, a megtaláló nem tudja használni.
// Formátum: header(32) | küldő ujjlenyomat(16) | címzett ujjl.(16) | ideiglenes pub(32) | nonce||ct | aláírás(64)
func (k *Keybook) ExportFor(me *Identity, to *PublicKey, rng io.Reader) ([]byte, error) {
	eph, err := ecdh.X25519().GenerateKey(rng)
	if err != nil {
		return nil, err
	}
	shared, err := x25519(eph.Bytes(), to.X25519)
	if err != nil {
		return nil, err
	}
	b := k.header(kbxMagic)
	b = append(b, me.Public().FingerprintBytes()...)
	b = append(b, to.FingerprintBytes()...)
	ephPub := eph.PublicKey().Bytes()
	b = append(b, ephPub...)
	key := hkdf32(shared, k.ID[:], "RealPassGAL v1 kulcskonyv-atadas")
	defer wipe(key)
	ct, err := seal(key, k.Data, b, rng)
	if err != nil {
		return nil, err
	}
	b = append(b, ct...)
	return append(b, me.Sign(b)...), nil
}

// ImportKeybook – átadó fájl beolvasása. A küldőnek ismert partnernek kell lennie.
func ImportKeybook(raw []byte, me *Identity, partners []*PublicKey) (*Keybook, *PublicKey, error) {
	if len(raw) < 32+16+16+32+64 {
		return nil, nil, ErrFormat
	}
	k, rest, err := parseHeader(raw, kbxMagic)
	if err != nil {
		return nil, nil, err
	}
	senderFP, recipFP, ephPub := rest[:16], rest[16:32], rest[32:64]
	if !bytes.Equal(recipFP, me.Public().FingerprintBytes()) {
		return nil, nil, errors.New("ez a kulcskönyv nem neked szól (más címzett ujjlenyomata van benne)")
	}
	var sender *PublicKey
	for _, p := range partners {
		if bytes.Equal(p.FingerprintBytes(), senderFP) {
			sender = p
		}
	}
	if sender == nil {
		return nil, nil, errors.New("a küldő nincs a partnereid között – előbb importáld a nyilvános kulcsát")
	}
	body, sig := raw[:len(raw)-64], raw[len(raw)-64:]
	if !ed25519.Verify(ed25519.PublicKey(sender.Ed25519), body, sig) {
		return nil, nil, errors.New("az aláírás érvénytelen – a fájlt módosították")
	}
	shared, err := x25519(me.XPriv, ephPub)
	if err != nil {
		return nil, nil, err
	}
	key := hkdf32(shared, k.ID[:], "RealPassGAL v1 kulcskonyv-atadas")
	defer wipe(key)
	hdrLen := 32 + 16 + 16 + 32
	pt, err := open(key, body[hdrLen:], body[:hdrLen])
	if err != nil || len(pt) != KeybookSize {
		return nil, nil, ErrCorrupt
	}
	k.Data = pt
	return k, sender, nil
}
