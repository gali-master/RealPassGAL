package core

import (
	"bytes"
	"math/rand"
	"path/filepath"
	"testing"
)

func TestFS(t *testing.T) {
	r, _ := NewRNG()
	gal, peter := mk(t, "Gál"), mk(t, "Péter")
	kb, _ := NewKeybook(r)
	lookup := func([16]byte) *Keybook { return kb }
	p := filepath.Join(t.TempDir(), "titkos.gal")
	c, err := CreateContainer(p, 20<<20, gal, []*PublicKey{peter.Public()}, kb, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	fs := NewFS(c, gal, r, false)

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(fs.Mkdir("/Tervek"))
	must(fs.Mkdir("/Tervek/Ház"))
	if fs.Mkdir("/tervek") != ErrExists {
		t.Fatal("kis/nagybetű: duplikált mappa")
	}
	// nagy fájl, kis darabokban írva (mint a Windows másolás)
	big := make([]byte, 1_234_567)
	rand.New(rand.NewSource(1)).Read(big)
	id, err := fs.Create("/Tervek/Ház/alaprajz.pdf")
	must(err)
	for off := 0; off < len(big); off += 4096 {
		end := off + 4096
		if end > len(big) {
			end = len(big)
		}
		if _, err := fs.WriteAt(id, big[off:end], int64(off)); err != nil {
			t.Fatal(err)
		}
	}
	fs.Release(id)
	small, err := fs.Create("/jegyzet.txt")
	must(err)
	fs.WriteAt(small, []byte("árvíztűrő tükörfúrógép"), 0)
	fs.Release(small)
	must(fs.Save())
	_, free1 := fs.Space()

	// felülírás közepén, csonkolás, átnevezés
	id, _ = fs.Open("/TERVEK/ház/ALAPRAJZ.pdf")
	fs.WriteAt(id, []byte("XYZ"), 100000)
	copy(big[100000:], "XYZ")
	must(fs.Truncate(id, 1_000_000))
	big = big[:1_000_000]
	fs.Release(id)
	must(fs.Rename("/jegyzet.txt", "/Tervek/jegyzet2.txt"))
	if fs.Rmdir("/Tervek") != ErrNotEmpty {
		t.Fatal("nem üres mappa törölhető")
	}
	must(fs.Close())

	// Péter megnyitja, ellenőriz
	c2, err := OpenContainer(p, peter, []*PublicKey{gal.Public()}, lookup, true)
	must(err)
	if n, err := c2.Verify(nil); err != nil {
		t.Fatalf("ellenőrzés: %v (%d)", err, n)
	}
	fs2 := NewFS(c2, peter, r, false)
	id, err = fs2.Open("/Tervek/Ház/alaprajz.pdf")
	must(err)
	got := make([]byte, 2_000_000)
	n, _ := fs2.ReadAt(id, got, 0)
	if !bytes.Equal(got[:n], big) {
		t.Fatalf("tartalom eltér (%d bájt)", n)
	}
	fs2.Release(id)
	id, _ = fs2.Open("/Tervek/jegyzet2.txt")
	n, _ = fs2.ReadAt(id, got, 0)
	if string(got[:n]) != "árvíztűrő tükörfúrógép" {
		t.Fatal("jegyzet")
	}
	fs2.Release(id)
	ents, _ := fs2.ReadDir("/Tervek")
	if len(ents) != 2 {
		t.Fatalf("mappa: %v", ents)
	}
	must(fs2.Unlink("/Tervek/Ház/alaprajz.pdf"))
	must(fs2.Close())

	// törlés után a hely felszabadul (mentés után)
	c3, _ := OpenContainer(p, gal, []*PublicKey{peter.Public()}, lookup, false)
	fs3 := NewFS(c3, gal, r, true)
	_, free3 := fs3.Space()
	if free3 <= free1 {
		t.Fatalf("hely nem szabadult fel: %d → %d", free1, free3)
	}
	if fs3.Mkdir("/x") != ErrReadOnly {
		t.Fatal("csak olvasható módban írt")
	}
	t.Logf("szabad blokk: %d → %d, generáció: %d", free1, free3, c3.H.Generation)
	fs3.Close()

	// megtelés
	c4, _ := OpenContainer(p, gal, []*PublicKey{peter.Public()}, lookup, true)
	fs4 := NewFS(c4, gal, r, false)
	id, _ = fs4.Create("/nagy.bin")
	chunk := make([]byte, 1<<20)
	var werr error
	for i := 0; i < 40 && werr == nil; i++ {
		_, werr = fs4.WriteAt(id, chunk, int64(i)<<20)
	}
	if werr != ErrNoSpace {
		t.Fatalf("megtelés: %v", werr)
	}
	fs4.Release(id)
	must(fs4.Close())
}
