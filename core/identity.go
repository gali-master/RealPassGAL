package core

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	identityFormat = "RealPassGAL-identitas"
	pubkeyFormat   = "RealPassGAL-nyilvanos-kulcs"
	identityAD     = "RealPassGAL v1 identitas"
)

// Identity – a saját titkos kulcsok. Lemezen jelszóval (Argon2id) titkosítva.
type Identity struct {
	Name       string    `json:"name"`
	Created    time.Time `json:"created"`
	XPriv      []byte    `json:"x25519"`
	EdSeed     []byte    `json:"ed25519"`
	StorageKey []byte    `json:"storage_key"` // ezzel titkosítjuk a kulcskönyvet, partnerlistát
	Reserve    []byte    `json:"reserve"`     // véletlen-tartalék (egérből) az automatikus újrakulcsoláshoz
}

type identityFile struct {
	Format string    `json:"format"`
	V      int       `json:"v"`
	KDF    KDFParams `json:"kdf"`
	Data   []byte    `json:"data"`
}

func ValidName(name string) error {
	n := strings.TrimSpace(name)
	if n == "" || utf8.RuneCountInString(n) > 40 {
		return errors.New("a név 1–40 karakter legyen")
	}
	return nil
}

func NewIdentity(name string, rng io.Reader) (*Identity, error) {
	if err := ValidName(name); err != nil {
		return nil, err
	}
	id := &Identity{Name: strings.TrimSpace(name), Created: time.Now().UTC().Truncate(time.Second)}
	id.XPriv = make([]byte, 32)
	id.EdSeed = make([]byte, 32)
	id.StorageKey = make([]byte, 32)
	id.Reserve = make([]byte, 64)
	for _, b := range [][]byte{id.XPriv, id.EdSeed, id.StorageKey, id.Reserve} {
		if _, err := io.ReadFull(rng, b); err != nil {
			return nil, err
		}
	}
	return id, nil
}

func (id *Identity) Save(path, password string, rng io.Reader) error {
	kdf := DefaultKDF
	return id.saveWith(path, password, kdf, rng)
}

func (id *Identity) saveWith(path, password string, kdf KDFParams, rng io.Reader) error {
	kdf.Salt = make([]byte, 16)
	if _, err := io.ReadFull(rng, kdf.Salt); err != nil {
		return err
	}
	pt, err := json.Marshal(id)
	if err != nil {
		return err
	}
	key := kdf.Derive(password)
	defer wipe(key)
	ct, err := seal(key, pt, []byte(identityAD), rng)
	wipe(pt)
	if err != nil {
		return err
	}
	out, _ := json.MarshalIndent(identityFile{Format: identityFormat, V: 1, KDF: kdf, Data: ct}, "", "  ")
	return writeFileAtomic(path, out)
}

func LoadIdentity(path, password string) (*Identity, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f identityFile
	if json.Unmarshal(raw, &f) != nil || f.Format != identityFormat || f.V != 1 {
		return nil, ErrFormat
	}
	key := f.KDF.Derive(password)
	defer wipe(key)
	pt, err := open(key, f.Data, []byte(identityAD))
	if err != nil {
		return nil, ErrWrongPassword
	}
	defer wipe(pt)
	var id Identity
	if err := json.Unmarshal(pt, &id); err != nil {
		return nil, ErrCorrupt
	}
	return &id, nil
}

func (id *Identity) edPriv() ed25519.PrivateKey { return ed25519.NewKeyFromSeed(id.EdSeed) }

func (id *Identity) Sign(msg []byte) []byte { return ed25519.Sign(id.edPriv(), msg) }

// Public – a saját nyilvános kulcs, a névvel együtt aláírva.
func (id *Identity) Public() *PublicKey {
	p := &PublicKey{
		Name:    id.Name,
		Created: id.Created,
		X25519:  x25519Pub(id.XPriv),
		Ed25519: []byte(id.edPriv().Public().(ed25519.PublicKey)),
	}
	p.Sig = id.Sign(p.signedBytes())
	return p
}

// RefreshReserve – új tartalék a megadott RNG-ből (minden egeres gyűjtés után).
func (id *Identity) RefreshReserve(rng io.Reader) {
	r := make([]byte, 64)
	io.ReadFull(rng, r)
	id.Reserve = r
}

// PublicKey – partnerhez átadható nyilvános kulcs.
type PublicKey struct {
	Format  string    `json:"format"`
	V       int       `json:"v"`
	Name    string    `json:"name"`
	Created time.Time `json:"created"`
	X25519  []byte    `json:"x25519"`
	Ed25519 []byte    `json:"ed25519"`
	Sig     []byte    `json:"sig"`
}

func (p *PublicKey) signedBytes() []byte {
	b := []byte("RealPassGAL v1 nyilvanos kulcs\x00")
	b = lp(b, []byte(p.Name))
	var t [8]byte
	binary.BigEndian.PutUint64(t[:], uint64(p.Created.Unix()))
	b = append(b, t[:]...)
	b = lp(b, p.X25519)
	return lp(b, p.Ed25519)
}

func (p *PublicKey) Verify() error {
	if len(p.X25519) != 32 || len(p.Ed25519) != ed25519.PublicKeySize || ValidName(p.Name) != nil {
		return ErrFormat
	}
	if !ed25519.Verify(ed25519.PublicKey(p.Ed25519), p.signedBytes(), p.Sig) {
		return errors.New("a nyilvános kulcs aláírása érvénytelen – a fájlt módosították")
	}
	return nil
}

func (p *PublicKey) FingerprintBytes() []byte {
	h := sha256.New()
	h.Write([]byte("RealPassGAL v1 ujjlenyomat"))
	h.Write(p.X25519)
	h.Write(p.Ed25519)
	return h.Sum(nil)[:16]
}

// Fingerprint pl. GAL-7F3A-91C2-04BE-55D0-1A2B-3C4D-5E6F-7081
func (p *PublicKey) Fingerprint() string { return formatCode("GAL", p.FingerprintBytes()) }

func formatCode(prefix string, b []byte) string {
	s := strings.ToUpper(hex.EncodeToString(b))
	parts := []string{prefix}
	for i := 0; i < len(s); i += 4 {
		parts = append(parts, s[i:i+4])
	}
	return strings.Join(parts, "-")
}

func (p *PublicKey) Marshal() []byte {
	c := *p
	c.Format, c.V = pubkeyFormat, 1
	out, _ := json.MarshalIndent(c, "", "  ")
	return out
}

func ParsePublicKey(raw []byte) (*PublicKey, error) {
	var p PublicKey
	if json.Unmarshal(raw, &p) != nil || p.Format != pubkeyFormat || p.V != 1 {
		return nil, ErrFormat
	}
	if err := p.Verify(); err != nil {
		return nil, err
	}
	return &p, nil
}

func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
