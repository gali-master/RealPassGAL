package core

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Frissítés: minden kiadáshoz tartozik egy aláírt kísérőfájl (RealPassGAL.zip.sig), amely a csomag
// SHA-256 kódját, a verziót és a változások listáját tartalmazza. Az aláírást a kiadó a saját
// RealPassGAL-identitásával készíti a saját gépén – a GitHubon nincs aláírókulcs, így egy feltört
// GitHub-fiókkal sem lehet hamis frissítést terjeszteni. A program csak akkor frissít, ha az aláíró
// ő maga vagy az egyik partnere, és (első elfogadás után) mindig ugyanaz a kiadó.

const releaseFormat = "RealPassGAL kiadás v1"

// ReleaseSigName – az aláírt kísérőfájl neve a kiadásban.
const ReleaseSigName = "RealPassGAL.zip.sig"

// ReleaseInfo – az aláírt kiadási adatok.
type ReleaseInfo struct {
	Version string    `json:"version"` // pl. 1.1.0
	SHA256  string    `json:"sha256"`  // a RealPassGAL.zip SHA-256 kódja (hex)
	Size    int64     `json:"size"`
	Notes   string    `json:"notes"` // mi változott (magyarul)
	Signed  time.Time `json:"signed"`
}

// SignRelease – kiadás aláírása a saját identitással.
func SignRelease(me *Identity, info ReleaseInfo) ([]byte, error) {
	if _, err := ParseVersion(info.Version); err != nil {
		return nil, err
	}
	b, err := json.Marshal(info)
	if err != nil {
		return nil, err
	}
	msg := append([]byte(releaseFormat+"\x00"), b...)
	return json.MarshalIndent(signedJSON{Format: releaseFormat, Body: b, FP: me.Public().FingerprintBytes(), Sig: me.Sign(msg)}, "", "  ")
}

// VerifyRelease – az aláírt kísérőfájl ellenőrzése a megbízható kulcsokkal (előbb aláírás, csak utána értelmezés).
func VerifyRelease(raw []byte, trusted []*PublicKey) (*ReleaseInfo, *PublicKey, error) {
	var s signedJSON
	if json.Unmarshal(raw, &s) != nil || s.Format != releaseFormat {
		return nil, nil, errors.New("a frissítés aláírt leírója hibás formátumú")
	}
	msg := append([]byte(releaseFormat+"\x00"), s.Body...)
	var signer *PublicKey
	for _, p := range trusted {
		if bytes.Equal(p.FingerprintBytes(), s.FP) && ed25519.Verify(ed25519.PublicKey(p.Ed25519), msg, s.Sig) {
			signer = p
			break
		}
	}
	if signer == nil {
		return nil, nil, errors.New("a frissítést nem te és nem is a partnereid egyike írta alá – nem telepíthető")
	}
	var info ReleaseInfo
	if err := json.Unmarshal(s.Body, &info); err != nil {
		return nil, nil, errors.New("a frissítés leírója sérült")
	}
	if _, err := ParseVersion(info.Version); err != nil {
		return nil, nil, err
	}
	if len(info.SHA256) != 64 {
		return nil, nil, errors.New("a frissítés leírójában hibás az ellenőrzőkód")
	}
	return &info, signer, nil
}

// ParseVersion – "1.2.3" → [1 2 3].
func ParseVersion(v string) ([3]int, error) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".")
	if len(parts) != 3 {
		return out, fmt.Errorf("hibás verziószám: %q", v)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 9999 {
			return out, fmt.Errorf("hibás verziószám: %q", v)
		}
		out[i] = n
	}
	return out, nil
}

// NewerVersion – igaz, ha a "candidate" újabb, mint a "current" (visszalépést soha nem fogadunk el).
func NewerVersion(candidate, current string) bool {
	a, err1 := ParseVersion(candidate)
	b, err2 := ParseVersion(current)
	if err1 != nil || err2 != nil {
		return false
	}
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

// FileSHA256 – fájl SHA-256 kódja (hex).
func FileSHA256(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// updateAllowed – a csomagból csak ezek a fájlok frissülnek; az adatok mappához és a
// WinFsp-telepítőhöz a frissítő soha nem nyúl.
func updateAllowed(name string) bool {
	switch name {
	case "RealPassGAL.exe", "RealPassGAL_utmutato.html", "LICENSE", "HARMADIK_FELEK.md", "VERZIO.txt", "VALTOZASOK.txt":
		return true
	}
	if strings.HasPrefix(name, "hang/") {
		base := name[len("hang/"):]
		return base != "" && !strings.ContainsAny(base, `/\:`) && base != "." && base != ".." &&
			(strings.HasSuffix(base, ".mp3") || strings.HasSuffix(base, ".txt"))
	}
	return false
}

// ZipText – egy kis szövegfájl beolvasása a csomagból (pl. VERZIO.txt).
func ZipText(zipPath, name string) (string, error) {
	z, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", err
	}
	defer z.Close()
	for _, f := range z.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		defer rc.Close()
		b, err := io.ReadAll(io.LimitReader(rc, 64<<10))
		return strings.TrimSpace(strings.TrimPrefix(string(b), "\uFEFF")), err
	}
	return "", fmt.Errorf("a csomagból hiányzik: %s", name)
}

// ApplyUpdate – a (már ellenőrzött) csomag kibontása a programmappába. Minden fájl előbb
// ideiglenes néven íródik ki, és csak ha mind sikerült, akkor kerülnek a helyükre. A futó
// exe-t átnevezi (.old) – Windowson a futó programot átnevezni szabad, felülírni nem.
// Az exeName a futó program fájlneve (a csomagbeli RealPassGAL.exe ezen a néven kerül a helyére).
func ApplyUpdate(zipPath, appDir, exeName string) error {
	z, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer z.Close()
	var written []string
	cleanup := func() {
		for _, w := range written {
			os.Remove(w + ".uj")
		}
	}
	hasExe := false
	var exe string
	for _, f := range z.File {
		name := strings.ReplaceAll(f.Name, `\`, "/")
		if f.FileInfo().IsDir() || !updateAllowed(name) {
			continue
		}
		if f.UncompressedSize64 > 200<<20 {
			cleanup()
			return fmt.Errorf("túl nagy fájl a csomagban: %s", name)
		}
		dst := filepath.Join(appDir, filepath.FromSlash(name))
		if name == "RealPassGAL.exe" {
			dst = filepath.Join(appDir, exeName)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			cleanup()
			return err
		}
		rc, err := f.Open()
		if err != nil {
			cleanup()
			return err
		}
		out, err := os.Create(dst + ".uj")
		if err == nil {
			_, err = io.Copy(out, io.LimitReader(rc, 200<<20))
			if err == nil {
				err = out.Sync()
			}
			if cerr := out.Close(); err == nil {
				err = cerr
			}
		}
		rc.Close()
		written = append(written, dst)
		if err != nil {
			cleanup()
			return err
		}
		if name == "RealPassGAL.exe" {
			hasExe = true
			exe = dst
		}
	}
	if !hasExe {
		cleanup()
		return errors.New("a csomagban nincs RealPassGAL.exe")
	}
	// előbb a kísérőfájlok, legvégül az exe (ha az nem sikerül, a régi visszakerül)
	for _, dst := range written {
		if dst == exe {
			continue
		}
		os.Remove(dst)
		if err := os.Rename(dst+".uj", dst); err != nil {
			cleanup()
			return err
		}
	}
	os.Remove(exe + ".old")
	hadOld := false
	if _, err := os.Stat(exe); err == nil {
		if err := os.Rename(exe, exe+".old"); err != nil {
			os.Remove(exe + ".uj")
			return fmt.Errorf("a futó program nem nevezhető át: %w", err)
		}
		hadOld = true
	}
	if err := os.Rename(exe+".uj", exe); err != nil {
		if hadOld {
			os.Rename(exe+".old", exe)
		}
		os.Remove(exe + ".uj")
		return err
	}
	return nil
}

// CleanupOldExe – az előző frissítés után megmaradt régi exe törlése (induláskor).
func CleanupOldExe(exePath string) {
	os.Remove(exePath + ".old")
}
