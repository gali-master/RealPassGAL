//go:build windows

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"golang.org/x/sys/windows"

	"realpassgal/core"
)

// ---- Egy példány: ugyanazzal az adatmappával csak egy program futhat ----

var instanceMutex windows.Handle

// singleInstance – hamis, ha ezzel az adatmappával már fut egy RealPassGAL.
func singleInstance(dataDir string) bool {
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(dataDir))))
	name, _ := windows.UTF16PtrFromString(`Local\RealPassGAL_` + hex.EncodeToString(sum[:8]))
	h, err := windows.CreateMutex(nil, false, name)
	if err == windows.ERROR_ALREADY_EXISTS {
		if h != 0 {
			windows.CloseHandle(h)
		}
		return false
	}
	instanceMutex = h // a program futásáig nyitva marad
	return true
}

// releaseInstance – a zár elengedése (frissítés utáni újraindítás előtt).
func releaseInstance() {
	if instanceMutex != 0 {
		windows.CloseHandle(instanceMutex)
		instanceMutex = 0
	}
}

// ---- Kulcskönyv-címkék ----

func (u *mainUI) loadKbInfo() {
	m, err := core.LoadKeybookInfos(u.dir, u.me.StorageKey)
	if err != nil {
		errBox(nil, "A kulcskönyv-nevek fájlja sérült; a nevek most nem látszanak (a kulcskönyvek rendben vannak).", err)
	}
	u.kbInfo = m
	s, err := core.LoadSeen(u.dir, u.me.StorageKey)
	if err != nil {
		s = core.Seen{}
	}
	u.seen = s
}

func (u *mainUI) saveKbInfo() {
	rng, err := core.NewRNG(u.me.Reserve)
	if err == nil {
		err = u.kbInfo.Save(u.dir, u.me.StorageKey, rng)
	}
	if err != nil {
		errBox(u.mw, "A kulcskönyv-nevek mentése nem sikerült.", err)
	}
}

// kbLabel – „Anna – projekt (#a1b2c3d4)”
func (u *mainUI) kbLabel(k *core.Keybook) string {
	n := strings.TrimSpace(u.kbInfo.Get(k).Name)
	if n == "" {
		n = "(névtelen)"
	}
	return n + "  (#" + k.ShortID() + ")"
}

// partnerName – ujjlenyomat (hex) → név.
func (u *mainUI) partnerName(fp string) string {
	for _, p := range u.partners {
		if hex.EncodeToString(p.FingerprintBytes()) == fp {
			return p.Name
		}
	}
	return "ismeretlen"
}

// kbHolders – kinél van meg a könyv (emberi nyelven).
func (u *mainUI) kbHolders(k *core.Keybook) string {
	i := u.kbInfo.Get(k)
	var parts []string
	if i.From != "" {
		parts = append(parts, "tőle kaptad: "+u.partnerName(i.From))
	}
	var given []string
	for _, g := range i.GivenTo {
		given = append(given, u.partnerName(g))
	}
	if len(given) > 0 {
		parts = append(parts, "átadva: "+strings.Join(given, ", "))
	}
	if len(parts) == 0 {
		return "még senkinek nem adtad át"
	}
	return strings.Join(parts, "; ")
}

// askText – egysoros szövegbekérő ablak.
func askText(owner walk.Form, title, prompt, initial string) (string, bool) {
	var (
		dlg      *walk.Dialog
		le       *walk.LineEdit
		okPB, cb *walk.PushButton
	)
	val := ""
	res, _ := Dialog{
		AssignTo: &dlg, Title: title, DefaultButton: &okPB, CancelButton: &cb,
		MinSize: Size{Width: 420, Height: 140}, Layout: VBox{},
		Children: []Widget{
			Label{Text: prompt},
			LineEdit{AssignTo: &le, Text: initial, MaxLength: 60},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				HSpacer{},
				PushButton{AssignTo: &okPB, Text: "OK", OnClicked: func() { val = strings.TrimSpace(le.Text()); dlg.Accept() }},
				PushButton{AssignTo: &cb, Text: "Mégse", OnClicked: func() { dlg.Cancel() }},
			}},
		},
	}.Run(owner)
	return val, res == walk.DlgCmdOK
}

func (u *mainUI) renameKeybook() {
	k := u.selectedKeybook()
	if k == nil {
		infoBox(u.mw, "Előbb jelöld ki a kulcskönyvet a listában.")
		return
	}
	name, ok := askText(u.mw, "Kulcskönyv neve", "Add meg, mire használod ezt a kulcskönyvet (pl. „Anna – projekt”):", u.kbInfo.Get(k).Name)
	if !ok {
		return
	}
	u.kbInfo.Get(k).Name = name
	u.saveKbInfo()
	u.refreshKeybooks()
}

// checkHolders – figyelmeztet, ha egy címzettről nincs feljegyezve, hogy megvan nála a könyv.
// Hamis: a felhasználó visszalépett.
func (u *mainUI) checkHolders(owner walk.Form, k *core.Keybook, recips []*core.PublicKey) bool {
	for _, p := range recips {
		if bytes.Equal(p.FingerprintBytes(), u.me.Public().FingerprintBytes()) || u.kbInfo.Holds(k, p) {
			continue
		}
		msg := fmt.Sprintf("Nincs feljegyezve, hogy %s megkapta ezt a kulcskönyvet:\n%s\n\n"+
			"Ha nincs meg neki, NEM fogja tudni kinyitni a ládát.\n\n"+
			"Igen = megvan neki (korábban átadtam), folytatom\nNem = visszalépek", p.Name, u.kbLabel(k))
		if walk.MsgBox(owner, appTitle, msg, walk.MsgBoxYesNo|walk.MsgBoxIconWarning|walk.MsgBoxDefButton2) != walk.DlgCmdYes {
			return false
		}
		u.kbInfo.MarkGiven(k, p)
		u.saveKbInfo()
	}
	return true
}

// ---- Látott mentések (visszagörgetés-jelzés) ----

// rollbackOK – ha a láda régebbi, mint amit láttunk, rákérdez. Hamis: ne nyissuk meg.
func (u *mainUI) rollbackOK(c *core.Container) bool {
	msg := u.seen.RollbackWarning(c)
	if msg == "" {
		return true
	}
	return walk.MsgBox(u.mw, appTitle, msg+"\n\nMegnyitod mégis?", walk.MsgBoxYesNo|walk.MsgBoxIconWarning|walk.MsgBoxDefButton2) == walk.DlgCmdYes
}

// noteSeen – a láda jelenlegi mentés-sorszámának feljegyzése (bármely szálról hívható).
func (u *mainUI) noteSeen(c *core.Container) {
	u.seenMu.Lock()
	defer u.seenMu.Unlock()
	if !u.seen.Note(c) {
		return
	}
	if rng, err := core.NewRNG(); err == nil {
		u.seen.Save(u.dir, u.me.StorageKey, rng)
	}
}

// ---- Láda beállításai: kulcskönyv és címzettek ----

func (u *mainUI) ladaSettings() {
	if u.mnt != nil {
		infoBox(u.mw, "Előbb csatold le a felcsatolt ládát.")
		return
	}
	st := u.loadSettings()
	fd := &walk.FileDialog{Title: "Melyik láda beállításait módosítod?", Filter: "RealPassGAL konténer (*.gal)|*.gal", InitialDirPath: filepath.Dir(st.LastPath)}
	if ok, _ := fd.ShowOpen(u.mw); !ok {
		return
	}
	path := fd.FilePath
	if l, _ := core.ReadLock(path, u.me, u.partners); l != nil && !(l.Mine(u.me) && !l.Offline) {
		errBox(u.mw, "A ládát most "+l.Who()+" használja vagy vitte el.\nA beállításokat csak akkor lehet módosítani, ha senki sem használja.", nil)
		return
	}
	c, err := core.OpenContainer(path, u.me, u.partners, u.lookupKeybook, true)
	if err != nil {
		errBox(u.mw, "A láda nem nyitható meg.", err)
		return
	}
	defer c.Close()
	if !u.rollbackOK(c) {
		return
	}
	if msg := core.SyncBehind(path, c, u.me, u.partners); msg != "" {
		errBox(u.mw, msg, nil)
		return
	}
	cur, err := c.Recipients(u.me, u.partners)
	if err != nil {
		errBox(u.mw, "A beállítások nem módosíthatók.", err)
		return
	}
	isCur := func(p *core.PublicKey) bool {
		for _, q := range cur {
			if bytes.Equal(q.FingerprintBytes(), p.FingerprintBytes()) {
				return true
			}
		}
		return false
	}
	curKB := u.lookupKeybook(c.H.KeybookID)

	var (
		dlg            *walk.Dialog
		kbCB           *walk.ComboBox
		okPB, cancelPB *walk.PushButton
	)
	kbNames, curIdx := []string{}, -1
	for i, k := range u.keybooks {
		kbNames = append(kbNames, u.kbLabel(k))
		if k.ID == c.H.KeybookID {
			curIdx = i
		}
	}
	checks := make([]*walk.CheckBox, len(u.partners))
	pw := []Widget{}
	for i, p := range u.partners {
		pw = append(pw, CheckBox{AssignTo: &checks[i], Text: p.Name + "   " + p.Fingerprint(), Checked: isCur(p)})
	}
	if len(u.partners) == 0 {
		pw = append(pw, Label{Text: "(nincs partner)"})
	}
	var newKB *core.Keybook
	var recips []*core.PublicKey
	onOK := func() {
		if kbCB.CurrentIndex() < 0 {
			errBox(dlg, "Válassz kulcskönyvet.", nil)
			return
		}
		newKB = u.keybooks[kbCB.CurrentIndex()]
		recips = []*core.PublicKey{u.me.Public()}
		for i, cb := range checks {
			if cb.Checked() {
				recips = append(recips, u.partners[i])
			}
		}
		if len(recips) > core.MaxRecipients {
			errBox(dlg, "Egy ládikának legfeljebb 3 címzettje lehet (veled együtt 4).", nil)
			return
		}
		dlg.Accept()
	}
	if err := (Dialog{
		AssignTo: &dlg, Title: "Láda beállításai – " + filepath.Base(path),
		DefaultButton: &okPB, CancelButton: &cancelPB,
		MinSize: Size{Width: 560, Height: 380}, Layout: VBox{},
		Children: []Widget{
			Label{Text: "Jelenlegi címzettek: " + strings.Join(c.RecipientNames(), ", ")},
			Label{Text: "Jelenlegi kulcskönyv: " + u.kbLabelOrID(curKB, c.H.KeybookID)},
			Label{Text: "Kulcskönyv:"},
			ComboBox{AssignTo: &kbCB, Model: kbNames, CurrentIndex: curIdx},
			GroupBox{Title: "Ki férhet hozzá? (te mindig)", Layout: VBox{}, Children: pw},
			Label{Text: "Kulcskönyv-csere és új címzett: gyors, a tartalom változatlan.\nCímzett kivétele: a láda új belső kulcsot kap, és a teljes tartalom újratitkosodik\n(ehhez legalább annyi szabad hely kell, amennyi adat van benne)."},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				HSpacer{},
				PushButton{AssignTo: &okPB, Text: "Alkalmaz", OnClicked: onOK},
				PushButton{AssignTo: &cancelPB, Text: "Mégse", OnClicked: func() { dlg.Cancel() }},
			}},
		},
	}).Create(u.mw); err != nil {
		errBox(u.mw, "Az ablak nem nyílt meg.", err)
		return
	}
	if dlg.Run() != walk.DlgCmdOK {
		return
	}

	removed := []string{}
	for _, p := range cur {
		found := false
		for _, q := range recips {
			if bytes.Equal(p.FingerprintBytes(), q.FingerprintBytes()) {
				found = true
			}
		}
		if !found {
			removed = append(removed, p.Name)
		}
	}
	added := false
	for _, q := range recips {
		if !isCur(q) {
			added = true
		}
	}
	if newKB.ID == c.H.KeybookID && len(removed) == 0 && !added {
		infoBox(u.mw, "Nem változott semmi.")
		return
	}
	if !u.checkHolders(u.mw, newKB, recips) {
		return
	}
	if len(removed) > 0 {
		if walk.MsgBox(u.mw, appTitle, "Kiveszed a címzettek közül: "+strings.Join(removed, ", ")+
			"\n\nA láda új belső kulcsot kap, és a teljes tartalom újratitkosodik. Utána a kivett személy "+
			"akkor sem tudja kinyitni a ládát, ha korábban hozzáfért.\n\nMehet?",
			walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) != walk.DlgCmdYes {
			return
		}
	}

	core.ReleaseLock(path) // saját, régi foglalás esetén
	if err := core.AcquireLock(path, u.me, c.H.Generation, false, ""); err != nil {
		errBox(u.mw, "A foglalás nem sikerült.", err)
		return
	}
	defer core.ReleaseLock(path)
	rng, err := core.NewRNG(u.me.Reserve)
	if err != nil {
		errBox(u.mw, "Véletlengenerátor hiba.", err)
		return
	}
	c.AfterCommit = func() { core.WriteState(path, u.me, c); u.noteSeen(c) }
	if len(removed) > 0 {
		err = runWithProgress(u.mw, "Újratitkosítás", func(p core.ProgressFunc) error {
			return c.RekeyTo(u.me, recips, newKB, rng, func(done, total int64) bool {
				return p(done*core.BlockSize, total*core.BlockSize)
			})
		})
	} else {
		if curKB == nil {
			errBox(u.mw, "Hiányzik a láda jelenlegi kulcskönyve, ezért nem módosítható.", nil)
			return
		}
		u.mw.SetCursor(walk.CursorWait())
		err = c.Rewrap(u.me, recips, curKB, newKB, rng)
		u.mw.SetCursor(walk.CursorArrow())
	}
	if err != nil {
		errBox(u.mw, "A módosítás nem sikerült; a láda a régi beállítással változatlanul használható.", err)
		return
	}
	u.showInfo(c, 0, nil)
	infoBox(u.mw, "Kész!\n\nCímzettek: "+strings.Join(c.RecipientNames(), ", ")+"\nKulcskönyv: "+u.kbLabel(newKB))
}

func (u *mainUI) kbLabelOrID(k *core.Keybook, id [16]byte) string {
	if k != nil {
		return u.kbLabel(k)
	}
	return "#" + hex.EncodeToString(id[:4]) + " (nincs meg nálad)"
}
