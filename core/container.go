package core

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

// ---- Elrendezés ----
//
//  [Fejléc 0: 64 KB][Fejléc 1: 64 KB][Metaadat 0][Metaadat 1][Blokk 0][Blokk 1]...
//  Blokk = nonce(24) | adat(64 KB) | tag(16). A generáció paritása dönti el,
//  melyik fejléc-/metaadat-példány az aktuális (mindig a régebbit írjuk felül).

const (
	BlockSize     = 64 * 1024
	BlockSlot     = 24 + BlockSize + 16
	HeaderSlot    = 64 * 1024
	MaxRecipients = 4
	MinContainer  = 1 << 20   // 1 MB
	MaxContainer  = 100 << 30 // 100 GB

	hdrMagic   = "REALPASSGAL\x00"
	hdrVersion = 1
	slotLen    = 120
	hdrSigned  = 600
	hdrLen     = hdrSigned + 64
)

type Layout struct {
	Total    int64
	MetaSize int64
	Blocks   uint32
}

func ComputeLayout(requested int64) (Layout, error) {
	if requested < MinContainer || requested > MaxContainer {
		return Layout{}, errors.New("a méret 1 MB és 100 GB között legyen")
	}
	meta := requested / 64
	if meta < 256<<10 {
		meta = 256 << 10
	}
	if meta > 64<<20 {
		meta = 64 << 20
	}
	meta -= meta % (64 << 10)
	avail := requested - 2*HeaderSlot - 2*meta
	if avail < BlockSlot {
		return Layout{}, errors.New("túl kicsi méret")
	}
	l := Layout{MetaSize: meta, Blocks: uint32(avail / BlockSlot)}
	l.Total = 2*HeaderSlot + 2*meta + int64(l.Blocks)*BlockSlot
	return l, nil
}

func (l Layout) metaOff(i uint64) int64 { return 2*HeaderSlot + int64(i%2)*l.MetaSize }
func (l Layout) blockOff(n uint32) int64 {
	return 2*HeaderSlot + 2*l.MetaSize + int64(n)*BlockSlot
}

// Usable – a felhasználható adatterület bájtban.
func (l Layout) Usable() int64 { return int64(l.Blocks) * BlockSize }

// ---- Fejléc ----

type Slot struct {
	FP      [16]byte
	EphPub  [32]byte
	Wrapped [72]byte // nonce(24) | lezárt mesterkulcs(32) | tag(16)
}

type Header struct {
	UUID       [16]byte
	Generation uint64
	Epoch      uint32
	NSlots     int
	EpochStart time.Time
	Created    time.Time
	Layout     Layout
	KeybookID  [16]byte
	Slots      [MaxRecipients]Slot
	SignerFP   [16]byte
	Sig        [64]byte
}

func (h *Header) marshal() []byte {
	b := make([]byte, hdrLen)
	be := binary.BigEndian
	copy(b[0:], hdrMagic)
	be.PutUint16(b[12:], hdrVersion)
	copy(b[16:], h.UUID[:])
	be.PutUint64(b[32:], h.Generation)
	be.PutUint32(b[40:], h.Epoch)
	b[44] = byte(h.NSlots)
	be.PutUint64(b[48:], uint64(h.EpochStart.Unix()))
	be.PutUint64(b[56:], uint64(h.Created.Unix()))
	be.PutUint32(b[64:], BlockSize)
	be.PutUint32(b[68:], h.Layout.Blocks)
	be.PutUint64(b[72:], uint64(h.Layout.MetaSize))
	be.PutUint64(b[80:], uint64(h.Layout.Total))
	copy(b[88:], h.KeybookID[:])
	for i, s := range h.Slots {
		o := 104 + i*slotLen
		copy(b[o:], s.FP[:])
		copy(b[o+16:], s.EphPub[:])
		copy(b[o+48:], s.Wrapped[:])
	}
	copy(b[584:], h.SignerFP[:])
	copy(b[600:], h.Sig[:])
	return b
}

func parseHeader2(b []byte) (*Header, error) {
	if len(b) < hdrLen || string(b[:12]) != hdrMagic {
		return nil, ErrFormat
	}
	be := binary.BigEndian
	if be.Uint16(b[12:]) != hdrVersion || be.Uint32(b[64:]) != BlockSize || b[44] < 1 || b[44] > MaxRecipients {
		return nil, ErrFormat
	}
	h := &Header{}
	copy(h.UUID[:], b[16:])
	h.Generation = be.Uint64(b[32:])
	h.Epoch = be.Uint32(b[40:])
	h.NSlots = int(b[44])
	h.EpochStart = time.Unix(int64(be.Uint64(b[48:])), 0)
	h.Created = time.Unix(int64(be.Uint64(b[56:])), 0)
	h.Layout = Layout{Blocks: be.Uint32(b[68:]), MetaSize: int64(be.Uint64(b[72:])), Total: int64(be.Uint64(b[80:]))}
	copy(h.KeybookID[:], b[88:104])
	for i := range h.Slots {
		o := 104 + i*slotLen
		copy(h.Slots[i].FP[:], b[o:])
		copy(h.Slots[i].EphPub[:], b[o+16:])
		copy(h.Slots[i].Wrapped[:], b[o+48:o+slotLen])
	}
	copy(h.SignerFP[:], b[584:600])
	copy(h.Sig[:], b[600:664])
	return h, nil
}

func slotAD(uuid [16]byte, epoch uint32, fp []byte) []byte {
	ad := append([]byte("RealPassGAL v1 rekesz"), uuid[:]...)
	ad = binary.BigEndian.AppendUint32(ad, epoch)
	return append(ad, fp...)
}

func slotKEK(shared, kbEntry []byte, uuid [16]byte) []byte {
	ikm := append(append([]byte{}, shared...), kbEntry...)
	defer wipe(ikm)
	return hkdf32(ikm, uuid[:], "RealPassGAL v1 rekesz-kulcs")
}

func makeSlot(r io.Reader, to *PublicKey, kbEntry, dek []byte, uuid [16]byte, epoch uint32) (Slot, error) {
	var s Slot
	eph, err := ecdh.X25519().GenerateKey(r)
	if err != nil {
		return s, err
	}
	shared, err := x25519(eph.Bytes(), to.X25519)
	if err != nil {
		return s, err
	}
	kek := slotKEK(shared, kbEntry, uuid)
	defer wipe(kek)
	fp := to.FingerprintBytes()
	w, err := seal(kek, dek, slotAD(uuid, epoch, fp), r)
	if err != nil {
		return s, err
	}
	copy(s.FP[:], fp)
	copy(s.EphPub[:], eph.PublicKey().Bytes())
	copy(s.Wrapped[:], w)
	return s, nil
}

// ---- Titkosított metaadat ----

type Meta struct {
	V         int               `json:"v"`
	Names     map[string]string `json:"names"` // ujjlenyomat (hex) → név
	CreatedBy string            `json:"created_by"`
	SavedBy   string            `json:"saved_by"`
	SavedAt   time.Time         `json:"saved_at"`
	Nodes     []*Node           `json:"nodes"`   // fájlrendszer (gyökér nélkül)
	NextID    uint64            `json:"next_id"` // következő szabad csomópont-azonosító
}

// ---- Konténer ----

type Container struct {
	Path     string
	H        Header
	Meta     Meta
	SignedBy string
	Warning  string // pl. sérült fejléc-példány, előző mentés használva

	AfterCommit func() // sikeres mentés után (pl. .state fájl frissítése)

	f        *os.File
	metaKey  []byte
	blockKey []byte
}

// ProgressFunc – folyamat jelzése; ha false-t ad vissza, a művelet megszakad.
type ProgressFunc func(done, total int64) bool

var ErrCancelled = errors.New("megszakítva")

func deriveKeys(dek []byte, uuid [16]byte) (meta, block []byte) {
	return hkdf32(dek, uuid[:], "RealPassGAL v1 metaadat"), hkdf32(dek, uuid[:], "RealPassGAL v1 blokk")
}

// CreateContainer – új .gal: teljes véletlen feltöltés, majd 1. generáció.
func CreateContainer(path string, size int64, me *Identity, recipients []*PublicKey, kb *Keybook, r io.Reader, progress ProgressFunc) (*Container, error) {
	l, err := ComputeLayout(size)
	if err != nil {
		return nil, err
	}
	if len(recipients) > MaxRecipients-1 {
		return nil, errors.New("egy ládikának legfeljebb 3 címzettje lehet (veled együtt 4)")
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	fail := func(e error) (*Container, error) {
		f.Close()
		os.Remove(path)
		return nil, e
	}

	// 1) véletlen feltöltés
	buf := make([]byte, 4<<20)
	for done := int64(0); done < l.Total; {
		n := int64(len(buf))
		if l.Total-done < n {
			n = l.Total - done
		}
		if _, err := io.ReadFull(r, buf[:n]); err != nil {
			return fail(err)
		}
		if _, err := f.Write(buf[:n]); err != nil {
			return fail(err)
		}
		done += n
		if progress != nil && !progress(done, l.Total) {
			return fail(ErrCancelled)
		}
	}

	// 2) kulcsok, fejléc
	c := &Container{Path: path, f: f}
	h := &c.H
	io.ReadFull(r, h.UUID[:])
	dek := make([]byte, 32)
	io.ReadFull(r, dek)
	defer wipe(dek)
	now := time.Now().UTC().Truncate(time.Second)
	h.Layout, h.Epoch, h.EpochStart, h.Created, h.KeybookID = l, 0, now, now, kb.ID
	entry, err := kb.Entry(h.Epoch)
	if err != nil {
		return fail(err)
	}
	all := append([]*PublicKey{me.Public()}, recipients...)
	h.NSlots = len(all)
	for i := range h.Slots {
		if i < len(all) {
			if h.Slots[i], err = makeSlot(r, all[i], entry, dek, h.UUID, h.Epoch); err != nil {
				return fail(err)
			}
		} else {
			io.ReadFull(r, h.Slots[i].FP[:]) // üres rekesz: véletlen
			io.ReadFull(r, h.Slots[i].EphPub[:])
			io.ReadFull(r, h.Slots[i].Wrapped[:])
		}
	}
	c.metaKey, c.blockKey = deriveKeys(dek, h.UUID)
	c.Meta = Meta{V: 1, Names: map[string]string{}, CreatedBy: me.Name}
	for _, p := range all {
		c.Meta.Names[hex.EncodeToString(p.FingerprintBytes())] = p.Name
	}
	if err := c.Commit(me, r); err != nil {
		return fail(err)
	}
	return c, nil
}

func metaAD(uuid [16]byte, gen uint64, epoch uint32) []byte {
	ad := append([]byte("RealPassGAL v1 metaadat"), uuid[:]...)
	ad = binary.BigEndian.AppendUint64(ad, gen)
	return binary.BigEndian.AppendUint32(ad, epoch)
}

// Commit – új generáció: metaadat, majd fejléc a régebbi példány helyére.
func (c *Container) Commit(me *Identity, r io.Reader) error {
	h := &c.H
	// A generációt csak sikeres mentés után tartjuk meg: hibánál visszaáll, így a következő
	// próbálkozás is a régebbi példány helyére ír, a legutóbbi érvényes mentés érintetlen marad.
	prevGen := h.Generation
	ok := false
	defer func() {
		if !ok {
			h.Generation = prevGen
		}
	}()
	h.Generation++
	c.Meta.SavedBy, c.Meta.SavedAt = me.Name, time.Now().UTC().Truncate(time.Second)
	pt, err := json.Marshal(c.Meta)
	if err != nil {
		return err
	}
	ct, err := seal(c.metaKey, pt, metaAD(h.UUID, h.Generation, h.Epoch), r)
	if err != nil {
		return err
	}
	if int64(len(ct)+8) > h.Layout.MetaSize {
		return errors.New("a metaadat-terület megtelt")
	}
	area := binary.BigEndian.AppendUint64(nil, uint64(len(ct)))
	area = append(area, ct...)
	if _, err := c.f.WriteAt(area, h.Layout.metaOff(h.Generation)); err != nil {
		return err
	}
	if err := c.f.Sync(); err != nil {
		return err
	}
	copy(h.SignerFP[:], me.Public().FingerprintBytes())
	b := h.marshal()
	copy(h.Sig[:], me.Sign(b[:hdrSigned]))
	copy(b[600:], h.Sig[:])
	if _, err := c.f.WriteAt(b, int64(h.Generation%2)*HeaderSlot); err != nil {
		return err
	}
	if err := c.f.Sync(); err != nil {
		return err
	}
	ok = true
	c.SignedBy = me.Name
	now := time.Now()
	os.Chtimes(c.Path, now, now) // a OneDrive biztosan észrevegye
	if c.AfterCommit != nil {
		c.AfterCommit()
	}
	return nil
}

// KeybookLookup – kulcskönyv keresése azonosító alapján.
type KeybookLookup func(id [16]byte) *Keybook

// OpenContainer – a két fejlécből a legnagyobb generációjú, érvényes aláírású nyer.
func OpenContainer(path string, me *Identity, partners []*PublicKey, lookup KeybookLookup, writable bool) (*Container, error) {
	flag := os.O_RDONLY
	if writable {
		flag = os.O_RDWR
	}
	f, err := os.OpenFile(path, flag, 0)
	if err != nil {
		return nil, err
	}
	c, err := openOn(f, path, me, partners, lookup)
	if err != nil {
		f.Close()
		return nil, err
	}
	return c, nil
}

func openOn(f *os.File, path string, me *Identity, partners []*PublicKey, lookup KeybookLookup) (*Container, error) {
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	known := append([]*PublicKey{me.Public()}, partners...)
	type cand struct {
		h      *Header
		signer *PublicKey
	}
	var cands []cand
	unknownSigner, anyHeader := false, false
	var unknownGen uint64
	badSig := false
	for i := int64(0); i < 2; i++ {
		b := make([]byte, hdrLen)
		if _, err := f.ReadAt(b, i*HeaderSlot); err != nil {
			continue
		}
		h, err := parseHeader2(b)
		if err != nil || h.Generation%2 != uint64(i) {
			continue
		}
		anyHeader = true
		var signer *PublicKey
		for _, p := range known {
			if bytes.Equal(p.FingerprintBytes(), h.SignerFP[:]) {
				signer = p
			}
		}
		if signer == nil {
			unknownSigner = true
			if h.Generation > unknownGen {
				unknownGen = h.Generation
			}
			continue
		}
		if !ed25519.Verify(ed25519.PublicKey(signer.Ed25519), b[:hdrSigned], h.Sig[:]) {
			badSig = true
			continue
		}
		cands = append(cands, cand{h, signer})
	}
	switch {
	case len(cands) == 0 && unknownSigner:
		return nil, errors.New("a konténert olyan valaki mentette, aki nincs a partnereid között")
	case len(cands) == 0 && anyHeader:
		return nil, errors.New("a konténer fejléce sérült vagy módosították")
	case len(cands) == 0:
		return nil, errors.New("ez nem RealPassGAL konténer (.gal)")
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].h.Generation > cands[j].h.Generation })
	if unknownSigner && unknownGen > cands[0].h.Generation {
		return nil, errors.New("a konténer legutóbbi mentését olyan valaki készítette, aki nincs a partnereid között")
	}
	// A legfrissebb érvényes mentéssel próbálkozunk; ha az nem nyitható (pl. áramszünet a mentés
	// közepén), az előző érvényes mentésre esünk vissza – figyelmeztetéssel.
	var firstErr error
	for k, cd := range cands {
		c, err := unlockHeader(f, st.Size(), path, me, cd.h, cd.signer, lookup)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		switch {
		case k > 0:
			c.Warning = "A legutóbbi mentés nem volt teljes (pl. áramszünet vagy megszakadt szinkron), ezért a program az előző érvényes mentést nyitotta meg. Az azóta végzett módosítások hiányozhatnak."
		case badSig:
			c.Warning = "Az egyik fejléc-példány sérült; a program az előző érvényes mentést nyitotta meg."
		}
		return c, nil
	}
	return nil, firstErr
}

// unlockHeader – egy (már aláírás-ellenőrzött) fejléc kinyitása: kulcskönyv, saját rekesz, metaadat.
func unlockHeader(f *os.File, size int64, path string, me *Identity, best *Header, signer *PublicKey, lookup KeybookLookup) (*Container, error) {
	if best.Layout.Total != size {
		return nil, fmt.Errorf("a fájl mérete nem egyezik (%d helyett %d bájt) – csonka vagy még szinkronizál", best.Layout.Total, size)
	}
	kb := lookup(best.KeybookID)
	if kb == nil {
		return nil, fmt.Errorf("hiányzik a kulcskönyv: #%s – kérd el a partneredtől", hex.EncodeToString(best.KeybookID[:4]))
	}
	entry, err := kb.Entry(best.Epoch)
	if err != nil {
		return nil, err
	}
	myFP := me.Public().FingerprintBytes()
	var dek []byte
	for i := 0; i < best.NSlots; i++ {
		s := best.Slots[i]
		if !bytes.Equal(s.FP[:], myFP) {
			continue
		}
		shared, err := x25519(me.XPriv, s.EphPub[:])
		if err != nil {
			return nil, err
		}
		kek := slotKEK(shared, entry, best.UUID)
		dek, err = open(kek, s.Wrapped[:], slotAD(best.UUID, best.Epoch, myFP))
		wipe(kek)
		if err != nil {
			return nil, errors.New("a rekeszed nem nyitható – eltérő kulcskönyv vagy sérült fejléc")
		}
	}
	if dek == nil {
		return nil, errors.New("nem vagy a konténer címzettjei között")
	}
	defer wipe(dek)
	c := &Container{Path: path, H: *best, f: f, SignedBy: signer.Name}
	c.metaKey, c.blockKey = deriveKeys(dek, best.UUID)
	if err := c.readMeta(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Container) readMeta() error {
	h := &c.H
	var lb [8]byte
	off := h.Layout.metaOff(h.Generation)
	if _, err := c.f.ReadAt(lb[:], off); err != nil {
		return err
	}
	n := binary.BigEndian.Uint64(lb[:])
	if n == 0 || n > uint64(h.Layout.MetaSize-8) {
		return errors.New("a metaadat sérült")
	}
	ct := make([]byte, n)
	if _, err := c.f.ReadAt(ct, off+8); err != nil {
		return err
	}
	pt, err := open(c.metaKey, ct, metaAD(h.UUID, h.Generation, h.Epoch))
	if err != nil {
		return errors.New("a metaadat sérült vagy módosították")
	}
	return json.Unmarshal(pt, &c.Meta)
}

func blockAD(uuid [16]byte, n uint32, epoch uint32) []byte {
	ad := append([]byte("RealPassGAL v1 blokk"), uuid[:]...)
	ad = binary.BigEndian.AppendUint32(ad, n)
	return binary.BigEndian.AppendUint32(ad, epoch)
}

// WriteBlock – egy 64 KB-os blokk titkosítva (rövidebb adatot nullákkal tölt ki).
func (c *Container) WriteBlock(n uint32, data []byte, r io.Reader) error {
	return c.writeBlockWith(c.blockKey, c.H.Epoch, n, data, r)
}

func (c *Container) writeBlockWith(key []byte, epoch uint32, n uint32, data []byte, r io.Reader) error {
	if n >= c.H.Layout.Blocks || len(data) > BlockSize {
		return errors.New("érvénytelen blokk")
	}
	pt := make([]byte, BlockSize)
	copy(pt, data)
	ct, err := seal(key, pt, blockAD(c.H.UUID, n, epoch), r)
	if err != nil {
		return err
	}
	_, err = c.f.WriteAt(ct, c.H.Layout.blockOff(n))
	return err
}

func (c *Container) ReadBlock(n uint32) ([]byte, error) {
	if n >= c.H.Layout.Blocks {
		return nil, errors.New("érvénytelen blokk")
	}
	ct := make([]byte, BlockSlot)
	if _, err := c.f.ReadAt(ct, c.H.Layout.blockOff(n)); err != nil {
		return nil, err
	}
	pt, err := open(c.blockKey, ct, blockAD(c.H.UUID, n, c.H.Epoch))
	if err != nil {
		return nil, fmt.Errorf("a(z) %d. blokk sérült vagy módosították", n)
	}
	return pt, nil
}

// UsedBlocks – a fájlrendszer által használt blokkok.
func (c *Container) UsedBlocks() []uint32 {
	var out []uint32
	for _, n := range c.Meta.Nodes {
		for _, b := range n.Blocks {
			if b != Hole {
				out = append(out, b)
			}
		}
	}
	return out
}

// Verify – fejléc, metaadat, minden használt blokk és a fájlok SHA-256 összege.
// Visszaadja az ellenőrzött blokkok és a hibás fájlok számát.
func (c *Container) Verify(progress ProgressFunc) (checked int, err error) {
	if err := c.readMeta(); err != nil {
		return 0, err
	}
	total := int64(len(c.UsedBlocks()))
	var bad []string
	for _, n := range c.Meta.Nodes {
		if n.Dir {
			continue
		}
		h := sha256.New()
		remain := n.Size
		ok := true
		for _, b := range n.Blocks {
			var data []byte
			if b == Hole {
				data = make([]byte, BlockSize)
			} else {
				d, err := c.ReadBlock(b)
				if err != nil {
					ok = false
					break
				}
				data = d
				checked++
				if progress != nil && !progress(int64(checked), total) {
					return checked, ErrCancelled
				}
			}
			k := int64(len(data))
			if remain < k {
				k = remain
			}
			h.Write(data[:k])
			remain -= k
		}
		if ok && n.SHA != nil && !bytes.Equal(h.Sum(nil), n.SHA) {
			ok = false
		}
		if !ok {
			bad = append(bad, n.Name)
		}
	}
	if len(bad) > 0 {
		return checked, fmt.Errorf("%d sérült fájl: %s", len(bad), strings.Join(bad, ", "))
	}
	return checked, nil
}

// RecipientNames – a címzettek neve (a titkosított metaadatból).
func (c *Container) RecipientNames() []string {
	var out []string
	for i := 0; i < c.H.NSlots; i++ {
		fp := hex.EncodeToString(c.H.Slots[i].FP[:])
		if n, ok := c.Meta.Names[fp]; ok {
			out = append(out, n)
		} else {
			out = append(out, "ismeretlen ("+fp[:8]+")")
		}
	}
	return out
}

// KeyAgeText – a „friss, ropogós” kijelzés.
func (c *Container) KeyAgeText() string {
	days := int(time.Since(c.H.EpochStart).Hours() / 24)
	if days < 0 {
		days = 0
	}
	s := fmt.Sprintf("Kulcs: #%06d · %d napos – ", c.H.Epoch, days)
	switch {
	case days < 20:
		return s + "friss, ropogós!"
	case days < 30:
		return s + "lassan cserére érett"
	default:
		return s + "cserére érett – a következő írható felcsatoláskor frissül"
	}
}

func (c *Container) Close() error {
	wipe(c.metaKey)
	wipe(c.blockKey)
	return c.f.Close()
}
