package core

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"
)

func TestRekey(t *testing.T) {
	r, _ := NewRNG()
	gal, peter := mk(t, "Gál"), mk(t, "Péter")
	kb, _ := NewKeybook(r)
	lookup := func([16]byte) *Keybook { return kb }
	p := filepath.Join(t.TempDir(), "titkos.gal")
	c, err := CreateContainer(p, 8<<20, gal, []*PublicKey{peter.Public()}, kb, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	fs := NewFS(c, gal, r, false)
	data := bytes.Repeat([]byte("titkos adat "), 200000) // ~2.4 MB
	id, _ := fs.Create("/a.bin")
	fs.WriteAt(id, data, 0)
	fs.Release(id)
	fs.Close()
	backup := filepath.Join(t.TempDir(), "regi.gal")
	CopyFile(backup, p, nil)

	c2, _ := OpenContainer(p, gal, []*PublicKey{peter.Public()}, lookup, true)
	c2.H.EpochStart = time.Now().Add(-31 * 24 * time.Hour)
	if !c2.NeedsRekey() {
		t.Fatal("30 nap után kellene")
	}
	if err := c2.Rekey(gal, nil, kb, r, nil); err == nil {
		t.Fatal("ismeretlen címzett mellett nem szabadna")
	}
	if err := c2.Rekey(gal, []*PublicKey{peter.Public()}, kb, r, nil); err != nil {
		t.Fatal(err)
	}
	if c2.H.Epoch != 1 || c2.NeedsRekey() {
		t.Fatalf("korszak: %d", c2.H.Epoch)
	}
	t.Log(c2.KeyAgeText())
	c2.Close()

	c3, err := OpenContainer(p, peter, []*PublicKey{gal.Public()}, lookup, false)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := c3.Verify(nil); err != nil {
		t.Fatalf("ellenőrzés: %v (%d)", err, n)
	}
	fs3 := NewFS(c3, peter, r, true)
	id, _ = fs3.Open("/a.bin")
	got := make([]byte, len(data))
	n, _ := fs3.ReadAt(id, got, 0)
	if !bytes.Equal(got[:n], data) {
		t.Fatal("tartalom eltér újrakulcsolás után")
	}
	fs3.Close()

	// régi mentés továbbra is nyitható (nincs égetés)
	cb, err := OpenContainer(backup, peter, []*PublicKey{gal.Public()}, lookup, false)
	if err != nil || cb.H.Epoch != 0 {
		t.Fatalf("régi mentés: %v", err)
	}
	cb.Close()

	// kevés szabad hely: nem nyúl semmihez
	c4, _ := OpenContainer(p, gal, []*PublicKey{peter.Public()}, lookup, true)
	fs4 := NewFS(c4, gal, r, false)
	id, _ = fs4.Create("/b.bin")
	fs4.WriteAt(id, bytes.Repeat([]byte{1}, 3<<20), 0)
	fs4.Release(id)
	fs4.Close()
	c5, _ := OpenContainer(p, gal, []*PublicKey{peter.Public()}, lookup, true)
	gen := c5.H.Generation
	if err := c5.Rekey(gal, []*PublicKey{peter.Public()}, kb, r, nil); err == nil {
		t.Fatal("helyhiány mellett nem szabadna")
	} else {
		t.Log(err)
	}
	if c5.H.Generation != gen || c5.H.Epoch != 1 {
		t.Fatal("hiba után változott az állapot")
	}
	c5.Close()
}
