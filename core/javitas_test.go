package core

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Regressziós tesztek a 2026. novemberi független biztonsági audit (RPG-01…03) javításaihoz.

func jSetup(t *testing.T, size int64) (string, *Identity, *Keybook, *RNG, *Container) {
	t.Helper()
	r, _ := NewRNG()
	gal, peter := mk(t, "Gal"), mk(t, "Peter")
	kb, _ := NewKeybook(r)
	p := filepath.Join(t.TempDir(), "titkos.gal")
	c, err := CreateContainer(p, size, gal, []*PublicKey{peter.Public()}, kb, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p, gal, kb, r, c
}

func diskGens(t *testing.T, p string) [2]uint64 {
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var g [2]uint64
	for i := 0; i < 2; i++ {
		b := make([]byte, hdrLen)
		f.ReadAt(b, int64(i)*HeaderSlot)
		g[i] = binary.BigEndian.Uint64(b[32:])
	}
	return g
}

// RPG-01: sikertelen mentés nem tolja el a generációt; a következő mentés a régebbi példányt írja
// felül, és ha közben „áramszünet” jön, az előző érvényes mentés megnyitható marad.
func TestJavitasCommitParitas(t *testing.T) {
	p, gal, kb, r, c := jSetup(t, 1<<20)
	lookup := func([16]byte) *Keybook { return kb }
	c.Commit(gal, r)
	c.Commit(gal, r)
	good := c.H.Generation
	goodHdr := make([]byte, HeaderSlot)
	c.f.ReadAt(goodHdr, int64(good%2)*HeaderSlot)

	var big []*Node
	for i := 0; i < 3000; i++ {
		big = append(big, &Node{ID: uint64(i + 2), Parent: 1, Name: fmt.Sprintf("hosszu_fajlnev_%040d.txt", i)})
	}
	c.Meta.Nodes = big
	if err := c.Commit(gal, r); err == nil {
		t.Fatal("a túl nagy metaadatnak hibát kellett volna adnia")
	}
	if c.H.Generation != good {
		t.Fatalf("hiba után a generáció %d, várt %d", c.H.Generation, good)
	}
	c.Meta.Nodes = nil
	if err := c.Commit(gal, r); err != nil {
		t.Fatal(err)
	}
	g := diskGens(t, p)
	if g[good%2] != good {
		t.Fatalf("a legutóbbi érvényes (%d.) mentés felülíródott: %v", good, g)
	}
	// összeomlás-szimuláció: az új metaadat kiírva, a hozzá tartozó fejléc nem → a régi fejléc visszaírva
	newer := c.H.Generation
	c.f.WriteAt(make([]byte, HeaderSlot), int64(newer%2)*HeaderSlot)
	c.f.Sync()
	c.f.Close()
	c2, err := OpenContainer(p, gal, nil, lookup, false)
	if err != nil {
		t.Fatal("összeomlás után nem nyitható:", err)
	}
	if c2.H.Generation != good {
		t.Fatalf("megnyitott generáció %d, várt %d", c2.H.Generation, good)
	}
	c2.Close()
}

// RPG-01 (2. fele): a legfrissebb fejléc metaadata sérült → visszaesés az előző mentésre, figyelmeztetéssel.
func TestJavitasVisszaesesMetaHiba(t *testing.T) {
	p, gal, kb, r, c := jSetup(t, 1<<20)
	lookup := func([16]byte) *Keybook { return kb }
	c.Commit(gal, r)
	c.Commit(gal, r)
	newest := c.H.Generation
	off := c.H.Layout.metaOff(newest)
	c.f.WriteAt(bytes.Repeat([]byte{0x55}, 200), off+20)
	c.Close()
	c2, err := OpenContainer(p, gal, nil, lookup, true)
	if err != nil {
		t.Fatal("nem esett vissza az előző mentésre:", err)
	}
	defer c2.Close()
	if c2.H.Generation != newest-1 || c2.Warning == "" {
		t.Fatalf("generáció=%d (várt %d), figyelmeztetés=%q", c2.H.Generation, newest-1, c2.Warning)
	}
	// a visszaesett állapotból írva a sérült példány helyére kerül az új mentés
	if err := c2.Commit(gal, r); err != nil {
		t.Fatal(err)
	}
	if c2.H.Generation != newest {
		t.Fatalf("új mentés generációja %d, várt %d", c2.H.Generation, newest)
	}
}

// RPG-03: értelmetlenül nagy fájlméret elutasítva, nincs pánik, a mentés működik.
func TestJavitasTruncateKorlat(t *testing.T) {
	_, gal, _, r, c := jSetup(t, 4<<20)
	fs := NewFS(c, gal, r, false)
	defer fs.Close()
	id, _ := fs.Create("/elofoglalt.bin")
	if err := fs.Truncate(id, 20<<30); err != ErrNoSpace {
		t.Fatalf("20 GB egy 4 MB-os konténerben: %v", err)
	}
	if err := fs.Truncate(id, 1<<63-1); err != ErrNoSpace {
		t.Fatalf("MaxInt64: %v", err)
	}
	if err := fs.Truncate(id, 1<<20); err != nil { // ami elfér, az mehet
		t.Fatal(err)
	}
	if _, err := fs.WriteAt(id, []byte("x"), 1<<62); err != ErrNoSpace {
		t.Fatalf("távoli írás: %v", err)
	}
	if _, err := fs.ReadAt(id, make([]byte, 4), -5); err != ErrInvalid {
		t.Fatalf("negatív olvasás: %v", err)
	}
	fs.Release(id)
	if err := fs.Save(); err != nil {
		t.Fatal(err)
	}
}

// RPG-02: majdnem teli konténerben a felülírás időben „megtelt” hibát ad, a mentés soha nem akad el.
func TestJavitasTeliKontener(t *testing.T) {
	_, gal, _, r, c := jSetup(t, 4<<20)
	fs := NewFS(c, gal, r, false)
	defer fs.Close()
	total, _ := fs.Space()
	nb := int(total)/2 + 2
	id, _ := fs.Create("/nagy.bin")
	if _, err := fs.WriteAt(id, bytes.Repeat([]byte{1}, nb*BlockSize), 0); err != nil {
		t.Fatal(err)
	}
	fs.Release(id)
	if err := fs.Save(); err != nil {
		t.Fatal(err)
	}
	id, _ = fs.Open("/nagy.bin")
	var werr error
	for i := 0; i < nb; i++ { // blokkonként, mint a Windows
		if _, werr = fs.WriteAt(id, bytes.Repeat([]byte{2}, BlockSize), int64(i)*BlockSize); werr != nil {
			break
		}
	}
	fs.Release(id)
	if werr != ErrNoSpace {
		t.Fatalf("a felülírásnak időben helyhiányt kellett volna jeleznie, kapott: %v", werr)
	}
	if err := fs.Save(); err != nil {
		t.Fatal("a mentés elakadt:", err)
	}
	if err := fs.Mkdir("/uj_mappa"); err != nil {
		t.Fatal(err)
	}
	if err := fs.Save(); err != nil {
		t.Fatal("apró változás sem menthető:", err)
	}
	// mentés után a felszabadult hely újra használható: a fájl most már átírható
	id, _ = fs.Open("/nagy.bin")
	for i := 0; i < nb; i++ {
		if _, err := fs.WriteAt(id, bytes.Repeat([]byte{3}, BlockSize), int64(i)*BlockSize); err != nil {
			if err == ErrNoSpace {
				fs.Release(id)
				if err := fs.Save(); err != nil {
					t.Fatal(err)
				}
				id, _ = fs.Open("/nagy.bin")
				i--
				continue
			}
			t.Fatal(err)
		}
	}
	fs.Release(id)
	if err := fs.Save(); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 3)
	fs.ReadAt(id, buf, int64(nb-1)*BlockSize)
	if !bytes.Equal(buf, []byte{3, 3, 3}) {
		t.Fatal("hibás tartalom a darabos átírás után")
	}
}

// 1.2.0: kulcskönyv cseréje – a tartalom változatlan, a régi könyvvel már nem nyílik, az újjal igen.
func TestKulcskonyvCsere(t *testing.T) {
	r, _ := NewRNG()
	gal, anna, peter := mk(t, "Gal"), mk(t, "Anna"), mk(t, "Peter")
	kbPeter, _ := NewKeybook(r)
	kbAnna, _ := NewKeybook(r)
	p := filepath.Join(t.TempDir(), "titkos2.gal")
	c, err := CreateContainer(p, 2<<20, gal, []*PublicKey{anna.Public()}, kbPeter, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	fs := NewFS(c, gal, r, false)
	id, _ := fs.Create("/anna.txt")
	fs.WriteAt(id, []byte("Anna projektje"), 0)
	fs.Release(id)
	if err := fs.Save(); err != nil {
		t.Fatal(err)
	}
	blocksBefore := c.UsedBlocks()
	if err := c.ChangeKeybook(gal, []*PublicKey{anna.Public(), peter.Public()}, kbPeter, kbAnna, r); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(c.UsedBlocks()) != fmt.Sprint(blocksBefore) {
		t.Fatal("a csere megváltoztatta a tartalmat")
	}
	fs.Close()

	only := func(kb *Keybook) KeybookLookup {
		return func(id [16]byte) *Keybook {
			if id == kb.ID {
				return kb
			}
			return nil
		}
	}
	// Anna az új könyvvel kinyitja, és a tartalom megvan
	ca, err := OpenContainer(p, anna, []*PublicKey{gal.Public()}, only(kbAnna), false)
	if err != nil {
		t.Fatal("Anna nem nyitja az új könyvvel:", err)
	}
	fa := NewFS(ca, anna, r, true)
	aid, err := fa.Open("/anna.txt")
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 14)
	fa.ReadAt(aid, buf, 0)
	if string(buf) != "Anna projektje" {
		t.Fatalf("tartalom: %q", buf)
	}
	fa.Close()
	// a legfrissebb fejléc a régi könyvet már nem kéri; Péter (régi könyv, nem címzett) semmiképp
	if _, err := OpenContainer(p, peter, []*PublicKey{gal.Public()}, only(kbPeter), false); err == nil {
		t.Fatal("Péter kinyitotta")
	}
}

// 1.2.0: visszagörgetés-jelzés.
func TestLatottMentesek(t *testing.T) {
	_, gal, _, r, c := jSetup(t, 1<<20)
	dir := t.TempDir()
	s, _ := LoadSeen(dir, gal.StorageKey)
	c.Commit(gal, r)
	c.Commit(gal, r)
	s.Note(c)
	s.Save(dir, gal.StorageKey, r)
	s2, _ := LoadSeen(dir, gal.StorageKey)
	if w := s2.RollbackWarning(c); w != "" {
		t.Fatal("hamis riasztás")
	}
	c.H.Generation--
	if w := s2.RollbackWarning(c); w == "" {
		t.Fatal("régebbi mentést nem jelzett")
	}
}

// 1.2.0: kulcskönyv-címkék.
func TestKulcskonyvCimkek(t *testing.T) {
	r, _ := NewRNG()
	gal, anna := mk(t, "Gal"), mk(t, "Anna")
	kb, _ := NewKeybook(r)
	dir := t.TempDir()
	m, _ := LoadKeybookInfos(dir, gal.StorageKey)
	m.Get(kb).Name = "Anna – projekt"
	m.MarkGiven(kb, anna.Public())
	m.MarkGiven(kb, anna.Public())
	if err := m.Save(dir, gal.StorageKey, r); err != nil {
		t.Fatal(err)
	}
	m2, err := LoadKeybookInfos(dir, gal.StorageKey)
	if err != nil || m2.Get(kb).Name != "Anna – projekt" || !m2.Holds(kb, anna.Public()) || len(m2.Get(kb).GivenTo) != 1 {
		t.Fatal(m2, err)
	}
	if _, err := LoadKeybookInfos(dir, anna.StorageKey); err == nil {
		t.Fatal("idegen kulccsal olvasható")
	}
}

// 1.2.0: címzett eltávolítása új belső kulccsal – a kivett személy a régi rekeszével sem nyit.
func TestCimzettEltavolitas(t *testing.T) {
	r, _ := NewRNG()
	gal, anna, peter := mk(t, "Gal"), mk(t, "Anna"), mk(t, "Peter")
	kbOld, _ := NewKeybook(r)
	kbAnna, _ := NewKeybook(r)
	p := filepath.Join(t.TempDir(), "titkos2.gal")
	c, err := CreateContainer(p, 4<<20, gal, []*PublicKey{anna.Public(), peter.Public()}, kbOld, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	fs := NewFS(c, gal, r, false)
	id, _ := fs.Create("/a.txt")
	fs.WriteAt(id, []byte("titok"), 0)
	fs.Release(id)
	fs.Save()
	// Rewrap nem engedi az eltávolítást
	if err := c.Rewrap(gal, []*PublicKey{gal.Public(), anna.Public()}, kbOld, kbAnna, r); err == nil {
		t.Fatal("Rewrap eltávolított egy címzettet")
	}
	if err := c.RekeyTo(gal, []*PublicKey{gal.Public(), anna.Public()}, kbAnna, r, nil); err != nil {
		t.Fatal(err)
	}
	fs.Close()
	all := func(id [16]byte) *Keybook {
		for _, k := range []*Keybook{kbOld, kbAnna} {
			if k.ID == id {
				return k
			}
		}
		return nil
	}
	if _, err := OpenContainer(p, peter, []*PublicKey{gal.Public()}, all, false); err == nil {
		t.Fatal("Péter (mindkét könyvvel) kinyitotta a legfrissebb állapotot")
	}
	ca, err := OpenContainer(p, anna, []*PublicKey{gal.Public()}, all, false)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(ca.RecipientNames()) != "[Gal Anna]" {
		t.Fatal(ca.RecipientNames())
	}
	fa := NewFS(ca, anna, r, true)
	aid, _ := fa.Open("/a.txt")
	b := make([]byte, 5)
	fa.ReadAt(aid, b, 0)
	if string(b) != "titok" {
		t.Fatalf("%q", b)
	}
	fa.Close()
}
