package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

const (
	MaxPartners  = 200 // a partnerlista hossza; egy ládikának legfeljebb MaxRecipients címzettje lehet
	partnersAD   = "RealPassGAL v1 partnerek"
	IdentityFile = "identitas.galid"
	PartnersFile = "partnerek.galdb"
)

// AppDir – a program (exe) mappája.
func AppDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return filepath.Dir(exe), nil
}

// DataDir – hordozható mód: a program (exe) melletti "adatok" mappa.
func DataDir() (string, error) {
	a, err := AppDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(a, "adatok")
	return d, os.MkdirAll(d, 0700)
}

// DownloadsDir – a felhasználó Letöltések mappája (exportok alapértelmezett helye).
func DownloadsDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(h, "Downloads")
}

// Partners – megbízható partnerkulcsok, a tárolókulccsal titkosítva (módosítás ellen is véd).
func LoadPartners(path string, storageKey []byte) ([]*PublicKey, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	pt, err := open(storageKey, raw, []byte(partnersAD))
	if err != nil {
		return nil, ErrCorrupt
	}
	var ps []*PublicKey
	if err := json.Unmarshal(pt, &ps); err != nil {
		return nil, ErrCorrupt
	}
	return ps, nil
}

func SavePartners(path string, ps []*PublicKey, storageKey []byte, rng io.Reader) error {
	pt, _ := json.Marshal(ps)
	ct, err := seal(storageKey, pt, []byte(partnersAD), rng)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, ct)
}

// AddPartner – ellenőrzi, hogy nem saját, nem duplikált, és van-e még hely.
func AddPartner(ps []*PublicKey, me *Identity, p *PublicKey) ([]*PublicKey, error) {
	if bytes.Equal(p.FingerprintBytes(), me.Public().FingerprintBytes()) {
		return ps, errors.New("ez a saját nyilvános kulcsod")
	}
	for _, q := range ps {
		if bytes.Equal(q.FingerprintBytes(), p.FingerprintBytes()) {
			return ps, errors.New("ez a partner már szerepel a listán")
		}
	}
	if len(ps) >= MaxPartners {
		return ps, errors.New("legfeljebb 200 partner lehet")
	}
	return append(ps, p), nil
}

// KeybookFiles – a helyi kulcskönyv-fájlok listája.
func KeybookFiles(dir string) []string {
	m, _ := filepath.Glob(filepath.Join(dir, "kulcskonyv_*.galkey"))
	sort.Strings(m)
	return m
}

func KeybookPath(dir string, k *Keybook) string {
	return filepath.Join(dir, "kulcskonyv_"+k.ShortID()+".galkey")
}

// PasswordStrength – becsült bitek és magyar értékelés. Szavas jelmondatnál
// szavanként ~11 bitet számolunk (óvatos becslés), egyébként karakterkészlet alapján.
func PasswordStrength(pw string) (bits float64, label string, ok bool) {
	words := strings.Fields(pw)
	if len(words) >= 4 {
		bits = float64(len(words)) * 11
	} else {
		var lo, up, dg, sy bool
		for _, r := range pw {
			switch {
			case unicode.IsLower(r):
				lo = true
			case unicode.IsUpper(r):
				up = true
			case unicode.IsDigit(r):
				dg = true
			default:
				sy = true
			}
		}
		cs := 0
		if lo {
			cs += 26
		}
		if up {
			cs += 26
		}
		if dg {
			cs += 10
		}
		if sy {
			cs += 33
		}
		n := len([]rune(pw))
		if cs > 0 {
			bits = float64(n) * math.Log2(float64(cs)) * 0.6 // emberi jelszavak kiszámíthatóbbak
		}
	}
	switch {
	case bits < 40:
		label = "gyenge"
	case bits < 55:
		label = "közepes"
	case bits < 75:
		label = "erős"
	default:
		label = "nagyon erős"
	}
	ok = bits >= 40 && len([]rune(pw)) >= 12
	return
}
