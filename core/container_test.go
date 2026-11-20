package core

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestContainer(t *testing.T) {
	r, _ := NewRNG()
	gal, peter, eve := mk(t, "Gál"), mk(t, "Péter"), mk(t, "Éva")
	kb, _ := NewKeybook(r)
	lookup := func(id [16]byte) *Keybook {
		if id == kb.ID {
			return kb
		}
		return nil
	}
	p := filepath.Join(t.TempDir(), "titkos.gal")
	for _, sz := range []int64{MinContainer, 5 << 20} {
		l, err := ComputeLayout(sz)
		if err != nil || l.Total > sz || l.Blocks < 1 {
			t.Fatalf("layout %d: %+v %v", sz, l, err)
		}
		t.Logf("%d bájt → %d blokk, %d hasznos", sz, l.Blocks, l.Usable())
	}
	c, err := CreateContainer(p, 5<<20, gal, []*PublicKey{peter.Public()}, kb, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.WriteBlock(3, []byte("helló világ"), r); err != nil {
		t.Fatal(err)
	}
	c.Commit(gal, r)
	c.Close()

	// Péter megnyitja
	c2, err := OpenContainer(p, peter, []*PublicKey{gal.Public()}, lookup, true)
	if err != nil {
		t.Fatal(err)
	}
	b, err := c2.ReadBlock(3)
	if err != nil || !bytes.HasPrefix(b, []byte("helló világ")) {
		t.Fatalf("blokk: %v", err)
	}
	if c2.SignedBy != "Gál" || c2.H.Generation != 2 || len(c2.RecipientNames()) != 2 {
		t.Fatalf("infó: %s %d %v", c2.SignedBy, c2.H.Generation, c2.RecipientNames())
	}
	t.Log(c2.RecipientNames(), c2.KeyAgeText())
	c2.Commit(peter, r) // Péter ment → 3. generáció
	c2.Close()

	c3, err := OpenContainer(p, gal, []*PublicKey{peter.Public()}, lookup, false)
	if err != nil || c3.SignedBy != "Péter" || c3.H.Generation != 3 {
		t.Fatalf("Gál újra: %v", err)
	}
	c3.Close()

	// nem címzett
	if _, err := OpenContainer(p, eve, []*PublicKey{gal.Public()}, lookup, false); err == nil {
		t.Fatal("Éva megnyitotta")
	} else {
		t.Log("Éva:", err)
	}
	// ismeretlen aláíró
	if _, err := OpenContainer(p, gal, nil, lookup, false); err == nil {
		t.Fatal("ismeretlen aláíró átment")
	} else {
		t.Log("partner nélkül:", err)
	}
	// hiányzó kulcskönyv
	if _, err := OpenContainer(p, gal, []*PublicKey{peter.Public()}, func([16]byte) *Keybook { return nil }, false); err == nil {
		t.Fatal("kulcskönyv nélkül")
	} else {
		t.Log(err)
	}
	// aktuális fejléc (gen 3 → 1. példány) rontása: a régebbire (gen 2) kell visszaesnie
	f, _ := os.OpenFile(p, os.O_RDWR, 0)
	f.WriteAt([]byte{0xFF}, HeaderSlot+40)
	f.Close()
	c4, err := OpenContainer(p, gal, []*PublicKey{peter.Public()}, lookup, false)
	if err != nil || c4.H.Generation != 2 || c4.Warning == "" {
		t.Fatalf("visszaesés: %v", err)
	}
	c4.Close()
	// blokk rontása
	f, _ = os.OpenFile(p, os.O_RDWR, 0)
	f.WriteAt([]byte{0x00}, c4.H.Layout.blockOff(3)+100)
	f.Close()
	c5, _ := OpenContainer(p, gal, []*PublicKey{peter.Public()}, lookup, false)
	if _, err := c5.ReadBlock(3); err == nil {
		t.Fatal("rontott blokk átment")
	} else {
		t.Log(err)
	}
	c5.Close()
}
