package core

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// Helyi, a tárolókulccsal titkosított kis adatfájlok (kulcskönyv-címkék, látott mentések).

func loadSealedJSON(path string, key []byte, ad string, v any) error {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	pt, err := open(key, raw, []byte(ad))
	if err != nil {
		return ErrCorrupt
	}
	if json.Unmarshal(pt, v) != nil {
		return ErrCorrupt
	}
	return nil
}

func saveSealedJSON(path string, key []byte, ad string, v any, r io.Reader) error {
	pt, err := json.Marshal(v)
	if err != nil {
		return err
	}
	ct, err := seal(key, pt, []byte(ad), r)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, ct)
}

// ---- Kulcskönyv-címkék: név, és kinek adtuk át / kitől kaptuk ----

const (
	KeybookInfoFile = "kulcskonyvek.galdb"
	kbInfoAD        = "RealPassGAL v1 kulcskonyv-cimkek"
)

type KeybookInfo struct {
	Name    string   `json:"nev,omitempty"`
	GivenTo []string `json:"atadva,omitempty"` // partner-ujjlenyomatok (hex)
	From    string   `json:"kapta,omitempty"`  // kitől kaptuk (hex), ha nem mi generáltuk
}

// KeybookInfos – kulcs: a kulcskönyv azonosítója (hex).
type KeybookInfos map[string]*KeybookInfo

func LoadKeybookInfos(dir string, key []byte) (KeybookInfos, error) {
	m := KeybookInfos{}
	err := loadSealedJSON(filepath.Join(dir, KeybookInfoFile), key, kbInfoAD, &m)
	if m == nil {
		m = KeybookInfos{}
	}
	return m, err
}

func (m KeybookInfos) Save(dir string, key []byte, r io.Reader) error {
	return saveSealedJSON(filepath.Join(dir, KeybookInfoFile), key, kbInfoAD, m, r)
}

// Get – a kulcskönyv címkéje (ha még nincs, létrehozza).
func (m KeybookInfos) Get(k *Keybook) *KeybookInfo {
	id := hex.EncodeToString(k.ID[:])
	if m[id] == nil {
		m[id] = &KeybookInfo{}
	}
	return m[id]
}

// MarkGiven – feljegyzi, hogy a könyvet ennek a partnernek átadtuk.
func (m KeybookInfos) MarkGiven(k *Keybook, p *PublicKey) {
	i, fp := m.Get(k), hex.EncodeToString(p.FingerprintBytes())
	for _, g := range i.GivenTo {
		if g == fp {
			return
		}
	}
	i.GivenTo = append(i.GivenTo, fp)
}

// Holds – tudjuk-e, hogy a partnernél megvan ez a könyv (átadtuk neki, vagy tőle kaptuk).
func (m KeybookInfos) Holds(k *Keybook, p *PublicKey) bool {
	i := m[hex.EncodeToString(k.ID[:])]
	if i == nil {
		return false
	}
	fp := hex.EncodeToString(p.FingerprintBytes())
	if i.From == fp {
		return true
	}
	for _, g := range i.GivenTo {
		if g == fp {
			return true
		}
	}
	return false
}

// ---- Látott mentések: visszagörgetés (régi példány visszacsempészése) jelzése ----

const (
	SeenFile = "latott_mentesek.galdb"
	seenAD   = "RealPassGAL v1 latott-mentesek"
)

// Seen – konténer-azonosító (hex) → a legnagyobb látott mentés-sorszám.
type Seen map[string]uint64

func LoadSeen(dir string, key []byte) (Seen, error) {
	m := Seen{}
	err := loadSealedJSON(filepath.Join(dir, SeenFile), key, seenAD, &m)
	if m == nil {
		m = Seen{}
	}
	return m, err
}

func (m Seen) Save(dir string, key []byte, r io.Reader) error {
	return saveSealedJSON(filepath.Join(dir, SeenFile), key, seenAD, m, r)
}

// Note – feljegyzi a konténer jelenlegi mentés-sorszámát; igaz, ha változott.
func (m Seen) Note(c *Container) bool {
	id := hex.EncodeToString(c.H.UUID[:])
	if c.H.Generation > m[id] {
		m[id] = c.H.Generation
		return true
	}
	return false
}

// RollbackWarning – ha a konténer régebbi, mint amit ezen a gépen már láttunk, leírja.
func (m Seen) RollbackWarning(c *Container) string {
	last := m[hex.EncodeToString(c.H.UUID[:])]
	if last == 0 || c.H.Generation >= last {
		return ""
	}
	return "Ez a láda RÉGEBBI, mint amit legutóbb láttál: most " + Nth(c.H.Generation) +
		" mentés van meg, de te már " + Nth(last) + " mentést is láttál.\n\n" +
		"Lehetséges okok: a OneDrive még nem töltötte le a legfrissebbet, egy régi másolatot nyitottál meg, " +
		"vagy valaki egy régebbi példányt tett a helyére. Kinyitni így sem tudja senki, de a legutóbbi " +
		"módosítások hiányozhatnak."
}

// ---- Kulcskönyv cseréje egy meglévő ládán ----

// recipients – a ládika címzettjeinek nyilvános kulcsai (mindenkit ismernünk kell).
func (c *Container) recipients(me *Identity, partners []*PublicKey) ([]*PublicKey, error) {
	h := &c.H
	known := append([]*PublicKey{me.Public()}, partners...)
	var out []*PublicKey
	for i := 0; i < h.NSlots; i++ {
		var found *PublicKey
		for _, p := range known {
			if bytes.Equal(p.FingerprintBytes(), h.Slots[i].FP[:]) {
				found = p
			}
		}
		if found == nil {
			name := c.Meta.Names[hex.EncodeToString(h.Slots[i].FP[:])]
			return nil, errors.New("ehhez minden címzett nyilvános kulcsa kell, de \"" + name + "\" nincs a partnereid között")
		}
		out = append(out, found)
	}
	return out, nil
}

// recoverDEK – a saját rekesz kinyitása (a láda belső kulcsa).
func (c *Container) recoverDEK(me *Identity, kb *Keybook) ([]byte, error) {
	h := &c.H
	entry, err := kb.Entry(h.Epoch)
	if err != nil {
		return nil, err
	}
	myFP := me.Public().FingerprintBytes()
	for i := 0; i < h.NSlots; i++ {
		s := h.Slots[i]
		if !bytes.Equal(s.FP[:], myFP) {
			continue
		}
		shared, err := x25519(me.XPriv, s.EphPub[:])
		if err != nil {
			return nil, err
		}
		kek := slotKEK(shared, entry, h.UUID)
		dek, err := open(kek, s.Wrapped[:], slotAD(h.UUID, h.Epoch, myFP))
		wipe(kek)
		wipe(shared)
		if err != nil {
			return nil, errors.New("a saját rekeszed nem nyitható")
		}
		return dek, nil
	}
	return nil, errors.New("nem vagy a láda címzettjei között")
}

// checkRecipients – a saját kulcs benne van, nincs ismétlődés, és belefér a rekeszekbe.
func checkRecipients(me *Identity, recips []*PublicKey) error {
	if len(recips) > MaxRecipients {
		return errors.New("egy ládikának legfeljebb 3 címzettje lehet (veled együtt 4)")
	}
	seen := map[string]bool{}
	mine := false
	for _, p := range recips {
		fp := hex.EncodeToString(p.FingerprintBytes())
		if seen[fp] {
			return errors.New("egy címzett kétszer szerepel")
		}
		seen[fp] = true
		if bytes.Equal(p.FingerprintBytes(), me.Public().FingerprintBytes()) {
			mine = true
		}
	}
	if !mine {
		return errors.New("a saját kulcsodnak mindig a címzettek között kell lennie")
	}
	return nil
}

// Recipients – a jelenlegi címzettek (a saját kulccsal együtt), ha mindenkit ismerünk.
func (c *Container) Recipients(me *Identity, partners []*PublicKey) ([]*PublicKey, error) {
	return c.recipients(me, partners)
}

// Rewrap – a címzett-rekeszek újrazárása (más kulcskönyvvel és/vagy új címzettel).
// A tartalom (blokkok, metaadat, belső kulcs) változatlan, egyetlen fejléc-mentés.
// Címzett ELTÁVOLÍTÁSÁRA nem alkalmas (arra: RekeyTo). Hiba vagy áramszünet esetén a
// régi fejléc érvényes marad.
func (c *Container) Rewrap(me *Identity, recips []*PublicKey, oldKB, newKB *Keybook, r io.Reader) error {
	h := &c.H
	if oldKB == nil || oldKB.ID != h.KeybookID {
		return errors.New("hiányzik a láda jelenlegi kulcskönyve")
	}
	if err := checkRecipients(me, recips); err != nil {
		return err
	}
	// eltávolítás tilos: minden jelenlegi címzettnek benne kell lennie
	for i := 0; i < h.NSlots; i++ {
		found := false
		for _, p := range recips {
			if bytes.Equal(p.FingerprintBytes(), h.Slots[i].FP[:]) {
				found = true
			}
		}
		if !found {
			return errors.New("címzett eltávolításához teljes újrakulcsolás kell")
		}
	}
	dek, err := c.recoverDEK(me, oldKB)
	if err != nil {
		return err
	}
	defer wipe(dek)
	entry, err := newKB.Entry(h.Epoch)
	if err != nil {
		return err
	}
	var slots [MaxRecipients]Slot
	for i := range slots {
		if i < len(recips) {
			if slots[i], err = makeSlot(r, recips[i], entry, dek, h.UUID, h.Epoch); err != nil {
				return err
			}
		} else {
			io.ReadFull(r, slots[i].FP[:])
			io.ReadFull(r, slots[i].EphPub[:])
			io.ReadFull(r, slots[i].Wrapped[:])
		}
	}
	oldH, oldNames := c.H, c.Meta.Names
	names := map[string]string{}
	for k, v := range oldNames {
		names[k] = v
	}
	for _, p := range recips {
		names[hex.EncodeToString(p.FingerprintBytes())] = p.Name
	}
	c.Meta.Names = names
	h.KeybookID, h.Slots, h.NSlots = newKB.ID, slots, len(recips)
	if err := c.Commit(me, r); err != nil {
		c.H, c.Meta.Names = oldH, oldNames
		return err
	}
	// második mentés: a régi (régi kulcskönyves) fejléc-példány se maradjon meg
	return c.Commit(me, r)
}

// ChangeKeybook – csak a kulcskönyv cseréje, a címzettek változatlanok.
func (c *Container) ChangeKeybook(me *Identity, partners []*PublicKey, oldKB, newKB *Keybook, r io.Reader) error {
	if newKB.ID == c.H.KeybookID {
		return errors.New("a láda már ezt a kulcskönyvet használja")
	}
	recips, err := c.recipients(me, partners)
	if err != nil {
		return err
	}
	return c.Rewrap(me, recips, oldKB, newKB, r)
}
