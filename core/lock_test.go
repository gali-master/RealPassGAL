package core

import (
	"path/filepath"
	"testing"
)

func TestLockAndState(t *testing.T) {
	r, _ := NewRNG()
	gal, peter := mk(t, "Gál"), mk(t, "Péter")
	kb, _ := NewKeybook(r)
	p := filepath.Join(t.TempDir(), "titkos.gal")
	c, err := CreateContainer(p, 2<<20, gal, []*PublicKey{peter.Public()}, kb, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if l, _ := ReadLock(p, gal, nil); l != nil {
		t.Fatal("nincs még lock")
	}
	if err := AcquireLock(p, gal, c.H.Generation, false, ""); err != nil {
		t.Fatal(err)
	}
	if AcquireLock(p, peter, 1, false, "") == nil {
		t.Fatal("dupla foglalás")
	}
	l, _ := ReadLock(p, peter, []*PublicKey{gal.Public()})
	if l == nil || l.Mine(peter) || l.Name != "Gál" || l.Stale() {
		t.Fatalf("Péter szemével: %+v", l)
	}
	t.Log("Péter látja:", l.Who())
	l2, _ := ReadLock(p, gal, []*PublicKey{peter.Public()})
	if !l2.Mine(gal) {
		t.Fatal("saját lock")
	}
	if Heartbeat(p, peter, []*PublicKey{gal.Public()}, 5) == nil {
		t.Fatal("idegen életjel")
	}
	if err := Heartbeat(p, gal, []*PublicKey{peter.Public()}, 2); err != nil {
		t.Fatal(err)
	}
	// állapot: Gál ment (2. gen), Péter régi másolata (1. gen) mögötte van
	c.Commit(gal, r)
	WriteState(p, gal, c)
	old := *c
	old.H.Generation = 1
	if msg := SyncBehind(p, &old, peter, []*PublicKey{gal.Public()}); msg == "" {
		t.Fatal("szinkron-lemaradás nem észlelve")
	} else {
		t.Log(msg)
	}
	if SyncBehind(p, c, peter, []*PublicKey{gal.Public()}) != "" {
		t.Fatal("friss állapot hibásan lemaradt")
	}
	ReleaseLock(p)
	if l, _ := ReadLock(p, gal, nil); l != nil {
		t.Fatal("lock maradt")
	}
	dst := filepath.Join(t.TempDir(), "elvitt.gal")
	if err := CopyFile(dst, p, nil); err != nil {
		t.Fatal(err)
	}
	c.Close()
}
