package core

import (
	"bytes"
	"path/filepath"
	"testing"
)

var testKDF = KDFParams{M: 8 * 1024, T: 1, P: 1}

func mk(t *testing.T, name string) *Identity {
	r, err := NewRNG([]byte("teszt"))
	if err != nil {
		t.Fatal(err)
	}
	id, err := NewIdentity(name, r)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestIdentityRoundtrip(t *testing.T) {
	d := t.TempDir()
	r, _ := NewRNG()
	id := mk(t, "Gál")
	p := filepath.Join(d, IdentityFile)
	if err := id.saveWith(p, "lila traktor énekel a kamrában", testKDF, r); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadIdentity(p, "rossz"); err != ErrWrongPassword {
		t.Fatalf("rossz jelszó: %v", err)
	}
	id2, err := LoadIdentity(p, "lila traktor énekel a kamrában")
	if err != nil || id2.Name != "Gál" || !bytes.Equal(id2.XPriv, id.XPriv) {
		t.Fatalf("betöltés: %v", err)
	}
	pub := id2.Public()
	pp, err := ParsePublicKey(pub.Marshal())
	if err != nil || pp.Fingerprint() != id.Public().Fingerprint() {
		t.Fatalf("pub: %v", err)
	}
	pp.Name = "Hamis"
	if pp.Verify() == nil {
		t.Fatal("módosított név átment")
	}
	t.Log(id.Public().Fingerprint())
}

func TestKeybookTransfer(t *testing.T) {
	r, _ := NewRNG()
	gal, peter, eve := mk(t, "Gál"), mk(t, "Péter"), mk(t, "Éva")
	kb, err := NewKeybook(r)
	if err != nil {
		t.Fatal(err)
	}
	d := t.TempDir()
	p := filepath.Join(d, "kk.galkey")
	if err := kb.Save(p, gal.StorageKey, r); err != nil {
		t.Fatal(err)
	}
	kb2, err := LoadKeybook(p, gal.StorageKey)
	if err != nil || kb2.CheckCode() != kb.CheckCode() {
		t.Fatalf("helyi: %v", err)
	}
	if _, err := LoadKeybook(p, peter.StorageKey); err == nil {
		t.Fatal("idegen tárolókulcs átment")
	}
	x, err := kb.ExportFor(gal, peter.Public(), r)
	if err != nil {
		t.Fatal(err)
	}
	got, sender, err := ImportKeybook(x, peter, []*PublicKey{gal.Public()})
	if err != nil || sender.Name != "Gál" || got.CheckCode() != kb.CheckCode() {
		t.Fatalf("import: %v", err)
	}
	if _, _, err := ImportKeybook(x, eve, []*PublicKey{gal.Public()}); err == nil {
		t.Fatal("rossz címzett átment")
	}
	if _, _, err := ImportKeybook(x, peter, nil); err == nil {
		t.Fatal("ismeretlen küldő átment")
	}
	x[200] ^= 1
	if _, _, err := ImportKeybook(x, peter, []*PublicKey{gal.Public()}); err == nil {
		t.Fatal("módosított fájl átment")
	}
	t.Log(kb.CheckCode())
}

func TestPartnersAndPool(t *testing.T) {
	r, _ := NewRNG()
	gal, peter := mk(t, "Gál"), mk(t, "Péter")
	ps, err := AddPartner(nil, gal, peter.Public())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AddPartner(ps, gal, peter.Public()); err == nil {
		t.Fatal("duplikált")
	}
	if _, err := AddPartner(ps, gal, gal.Public()); err == nil {
		t.Fatal("saját")
	}
	p := filepath.Join(t.TempDir(), PartnersFile)
	SavePartners(p, ps, gal.StorageKey, r)
	ps2, err := LoadPartners(p, gal.StorageKey)
	if err != nil || len(ps2) != 1 || ps2[0].Verify() != nil {
		t.Fatalf("partnerek: %v", err)
	}
	pool := NewEntropyPool()
	pool.AddMouse(1, 1, 5)
	pool.AddMouse(1, 1, 6)
	pool.AddMouse(2, 1, 7)
	if pool.Bits() != 2 {
		t.Fatal("pool számlálás")
	}
	t.Log("\n" + pool.Display())
	for _, pw := range []string{"Kutya2024!", "lila traktor énekel a kamrában hajnalban"} {
		b, l, ok := PasswordStrength(pw)
		t.Logf("%q: %.0f bit, %s, %v", pw, b, l, ok)
	}
}
