package core

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestReleaseSignVerify(t *testing.T) {
	gal, peter, mallory := mk(t, "Gal"), mk(t, "Peter"), mk(t, "Mallory")
	info := ReleaseInfo{Version: "1.1.0", SHA256: "ab" + string(make([]byte, 0)) + "0000000000000000000000000000000000000000000000000000000000000000"[:62], Notes: "javítások"}
	raw, err := SignRelease(gal, info)
	if err != nil {
		t.Fatal(err)
	}
	// Péter programja: Gál a partnere → elfogadja
	got, signer, err := VerifyRelease(raw, []*PublicKey{peter.Public(), gal.Public()})
	if err != nil || got.Version != "1.1.0" || signer.Name != "Gal" {
		t.Fatal(got, signer, err)
	}
	// ismeretlen aláíró → elutasítás
	if _, _, err := VerifyRelease(raw, []*PublicKey{peter.Public(), mallory.Public()}); err == nil {
		t.Fatal("ismeretlen aláírót elfogadott")
	}
	// módosított tartalom → elutasítás
	bad := append([]byte(nil), raw...)
	for i := range bad {
		if bad[i] == '1' {
			bad[i] = '2'
			break
		}
	}
	if _, _, err := VerifyRelease(bad, []*PublicKey{gal.Public()}); err == nil {
		t.Fatal("módosított leírót elfogadott")
	}
	if !NewerVersion("1.1.0", "1.0.0") || NewerVersion("1.0.0", "1.1.0") || NewerVersion("1.1.0", "1.1.0") || !NewerVersion("1.10.0", "1.9.9") {
		t.Fatal("verzió-összehasonlítás")
	}
}

func TestApplyUpdate(t *testing.T) {
	dir := t.TempDir()
	zp := filepath.Join(t.TempDir(), "RealPassGAL.zip")
	zf, _ := os.Create(zp)
	w := zip.NewWriter(zf)
	add := func(name, body string) {
		f, _ := w.Create(name)
		f.Write([]byte(body))
	}
	add("RealPassGAL.exe", "UJ EXE")
	add("hang/01.mp3", "hang")
	add("../kivul.txt", "zip-slip")
	add("adatok/identitas.galid", "tilos")
	add("winfsp/winfsp.msi", "nem kell")
	add("hang/../../x.mp3", "zip-slip 2")
	w.Close()
	zf.Close()
	os.WriteFile(filepath.Join(dir, "RealPassGAL.exe"), []byte("REGI EXE"), 0644)
	os.MkdirAll(filepath.Join(dir, "adatok"), 0700)
	os.WriteFile(filepath.Join(dir, "adatok", "identitas.galid"), []byte("SAJAT KULCS"), 0600)

	if err := ApplyUpdate(zp, dir, "RealPassGAL.exe"); err != nil {
		t.Fatal(err)
	}
	chk := func(p, want string) {
		b, err := os.ReadFile(filepath.Join(dir, p))
		if err != nil || string(b) != want {
			t.Fatalf("%s: %q %v", p, b, err)
		}
	}
	chk("RealPassGAL.exe", "UJ EXE")
	chk("RealPassGAL.exe.old", "REGI EXE")
	chk("hang/01.mp3", "hang")
	chk("adatok/identitas.galid", "SAJAT KULCS")
	for _, p := range []string{"winfsp/winfsp.msi", "../kivul.txt", "x.mp3"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err == nil {
			t.Fatal("nem engedélyezett fájl kibontva:", p)
		}
	}
	CleanupOldExe(filepath.Join(dir, "RealPassGAL.exe"))
	if _, err := os.Stat(filepath.Join(dir, "RealPassGAL.exe.old")); err == nil {
		t.Fatal(".old megmaradt")
	}
}
