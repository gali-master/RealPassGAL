//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"

	"realpassgal/core"
)

// settings – nem titkos beállítások (adatok\beallitasok.json).
type settings struct {
	LastPath string             `json:"last_path"`
	Letters  map[string]string  `json:"letters"` // konténer útvonala (kisbetűs) → meghajtóbetű
	Carried  map[string]carried `json:"carried"` // elvitt másolat útvonala (kisbetűs) → eredeti
}

type carried struct {
	Source     string `json:"source"`
	UUID       []byte `json:"uuid"`
	Generation uint64 `json:"generation"`
}

func (a *app) settingsPath() string { return filepath.Join(a.dir, "beallitasok.json") }

func (a *app) loadSettings() settings {
	s := settings{}
	if raw, err := os.ReadFile(a.settingsPath()); err == nil {
		json.Unmarshal(raw, &s)
	}
	if s.Letters == nil {
		s.Letters = map[string]string{}
	}
	if s.Carried == nil {
		s.Carried = map[string]carried{}
	}
	return s
}

func (a *app) saveSettings(s settings) {
	raw, _ := json.MarshalIndent(s, "", "  ")
	os.WriteFile(a.settingsPath(), raw, 0600)
}

func (u *mainUI) refreshMountState() {
	if u.mnt == nil {
		u.mountLbl.SetText("Nincs felcsatolt konténer.")
		return
	}
	mode := "írható, lefoglalva"
	if u.mnt.fs.ReadOnly() {
		mode = "csak olvasható"
	}
	u.mountLbl.SetText("Felcsatolva: " + u.mnt.letter + ":  ←  " + u.mnt.path + "  (" + mode + ")")
}

// okOrQuit – OK: tovább (csak olvasható), Mégse: kilépés a felcsatolásból.
func (u *mainUI) okOrQuit(msg string) bool {
	return walk.MsgBox(u.mw, appTitle, msg+"\n\nA konténer CSAK OLVASHATÓ lesz.\nOK = tovább, Mégse = kilépés",
		walk.MsgBoxOKCancel|walk.MsgBoxIconWarning) == walk.DlgCmdOK
}

func (u *mainUI) mountDialog() {
	if u.mnt != nil {
		infoBox(u.mw, "Már fel van csatolva egy konténer ("+u.mnt.letter+":). Előbb csatold le.")
		return
	}
	if demo == nil && bemutato == nil && !winfspInstalled() {
		errBox(u.mw, "A felcsatoláshoz telepíteni kell a WinFsp-t (ingyenes):\nhttps://winfsp.dev/rel/\n\n"+
			"Telepítés után indítsd újra a RealPassGAL-t. Részletek a Súgóban (F1).", nil)
		return
	}
	st := u.loadSettings()
	appDir, _ := core.AppDir()
	start := st.LastPath
	if start == "" {
		start = filepath.Join(appDir, "titkos.gal")
	}
	var (
		dlg            *walk.Dialog
		pathLE         *walk.LineEdit
		letterCB       *walk.ComboBox
		roCB           *walk.CheckBox
		okPB, cancelPB *walk.PushButton
	)
	var path, letter string
	var ro bool
	letters := freeLetters()
	if len(letters) == 0 {
		errBox(u.mw, "Nincs szabad meghajtóbetű.", nil)
		return
	}
	pickLetter := func(p string) int {
		if l, ok := st.Letters[strings.ToLower(p)]; ok {
			for i, x := range letters {
				if x == l {
					return i
				}
			}
		}
		return 0
	}
	browse := func() {
		fd := &walk.FileDialog{
			Title:          "Konténer kiválasztása",
			Filter:         "RealPassGAL konténer (*.gal)|*.gal",
			InitialDirPath: filepath.Dir(pathLE.Text()),
		}
		if ok, _ := fd.ShowOpen(dlg); ok {
			pathLE.SetText(fd.FilePath)
			letterCB.SetCurrentIndex(pickLetter(fd.FilePath))
		}
	}
	err := Dialog{
		AssignTo:      &dlg,
		Title:         "Felcsatolás",
		DefaultButton: &okPB,
		CancelButton:  &cancelPB,
		MinSize:       Size{Width: 560, Height: 200},
		Layout:        Grid{Columns: 3},
		Children: []Widget{
			Label{Text: "Konténer:"},
			LineEdit{AssignTo: &pathLE, Text: start},
			PushButton{Text: "Tallózás…", OnClicked: browse},
			Label{Text: "Meghajtóbetű:"},
			ComboBox{AssignTo: &letterCB, Model: letters, CurrentIndex: pickLetter(start)},
			HSpacer{},
			CheckBox{AssignTo: &roCB, ColumnSpan: 3, Text: "Csak olvasásra (semmit nem lehet módosítani)"},
			VSpacer{ColumnSpan: 3},
			Composite{
				ColumnSpan: 3,
				Layout:     HBox{MarginsZero: true},
				Children: []Widget{
					HSpacer{},
					PushButton{AssignTo: &okPB, Text: "Felcsatolás", OnClicked: func() {
						// az értékeket az ablak bezárása ELŐTT kell kiolvasni
						path = strings.Trim(strings.TrimSpace(pathLE.Text()), "\"")
						letter = letterCB.Text()
						ro = roCB.Checked()
						if path == "" || letter == "" {
							errBox(dlg, "Add meg a konténert és a meghajtóbetűt.", nil)
							return
						}
						dlg.Accept()
					}},
					PushButton{AssignTo: &cancelPB, Text: "Mégse", OnClicked: func() { dlg.Cancel() }},
				},
			},
		},
	}.Create(u.mw)
	if err != nil {
		return
	}
	if demo != nil {
		demo.shot(dlg, "12_felcsatolas", nil, func() { dlg.Cancel() })
	}
	if bemutato != nil {
		bemutato.attach(dlg, bemutato.mountScript())
	}
	if dlg.Run() != walk.DlgCmdOK {
		return
	}

	// 1) foglalás ellenőrzése
	takeOver := false
	if !ro {
		l, _ := core.ReadLock(path, u.me, u.partners)
		switch {
		case l == nil:
		case l.Mine(u.me) && l.Offline:
			if !u.okOrQuit("Ezt a konténert TE vitted el (" + l.OfflineTo + ").\nAmíg vissza nem hozod, itt nem írható.") {
				return
			}
			ro = true
		case l.Mine(u.me):
			takeOver = true // a saját korábbi (pl. lefagyott) foglalásunk ezen a gépen
		case l.Offline:
			if !u.okOrQuit(l.Who() + " elvitte a konténert, és máshol dolgozik vele.") {
				return
			}
			ro = true
		case l.ByMe(u.me) && !l.Offline:
			// a saját foglalásunk egy MÁSIK gépen (pl. ott megszakadt a kapcsolat)
			r := walk.MsgBox(u.mw, appTitle, "Ezt a foglalást te hoztad létre a(z) „"+l.Machine+"” gépen ("+
				l.Since.Local().Format("2006-01-02 15:04")+" óta).\n\n"+
				"Ha ott már nem használod a ládát (pl. megszakadt a kapcsolat), átveheted.\n\n"+
				"Igen = átveszem, írható lesz\nNem = csak olvasható\nMégse = kilépés",
				walk.MsgBoxYesNoCancel|walk.MsgBoxIconQuestion|walk.MsgBoxDefButton2)
			switch r {
			case walk.DlgCmdYes:
				takeOver = true
			case walk.DlgCmdNo:
				ro = true
			default:
				return
			}
		case l.Stale():
			r := walk.MsgBox(u.mw, appTitle, "A konténert "+l.Who()+" foglalta, de "+
				time.Since(l.Heartbeat).Round(time.Minute).String()+" óta nem jelentkezett (valószínűleg lefagyott).\n\n"+
				"Igen = feloldom a foglalást és írható lesz\nNem = csak olvasható\nMégse = kilépés",
				walk.MsgBoxYesNoCancel|walk.MsgBoxIconWarning|walk.MsgBoxDefButton2)
			switch r {
			case walk.DlgCmdYes:
				takeOver = true
			case walk.DlgCmdNo:
				ro = true
			default:
				return
			}
		default:
			if !u.okOrQuit("A konténert most " + l.Who() + " használja.") {
				return
			}
			ro = true
		}
	}

	u.mw.SetCursor(walk.CursorWait())
	defer u.mw.SetCursor(walk.CursorArrow())
	c, err := core.OpenContainer(path, u.me, u.partners, u.lookupKeybook, !ro)
	if err != nil {
		errBox(u.mw, "A konténer nem nyitható meg.", err)
		return
	}
	if c.Warning != "" {
		infoBox(u.mw, c.Warning)
	}
	if !u.rollbackOK(c) {
		c.Close()
		return
	}
	u.noteSeen(c)

	// 2) szinkronvédelem
	if !ro {
		if msg := core.SyncBehind(path, c, u.me, u.partners); msg != "" {
			if !u.okOrQuit(msg) {
				c.Close()
				return
			}
			c.Close()
			if c, err = core.OpenContainer(path, u.me, u.partners, u.lookupKeybook, false); err != nil {
				errBox(u.mw, "A konténer nem nyitható meg.", err)
				return
			}
			ro = true
		}
	}

	// 3) foglalás
	if !ro {
		if takeOver {
			core.ReleaseLock(path)
		}
		if err := core.AcquireLock(path, u.me, c.H.Generation, false, ""); err != nil {
			if !u.okOrQuit("A foglalás nem sikerült: " + err.Error()) {
				c.Close()
				return
			}
			c.Close()
			if c, err = core.OpenContainer(path, u.me, u.partners, u.lookupKeybook, false); err != nil {
				errBox(u.mw, "A konténer nem nyitható meg.", err)
				return
			}
			ro = true
		}
	}

	rng, err := core.NewRNG(u.me.Reserve)
	if err != nil {
		c.Close()
		errBox(u.mw, "Véletlengenerátor hiba.", err)
		return
	}
	if !ro {
		c.AfterCommit = func() { core.WriteState(path, u.me, c); u.noteSeen(c) }
		// 4) 30 naponta lapozás a kulcskönyvben
		if c.NeedsRekey() {
			infoBox(u.mw, "A kulcs betöltötte a 30 napot, ezért most lapozunk egyet a kulcskönyvben:\n"+
				"új kulcs, és a teljes tartalom újratitkosítása. Ez pár másodperctől pár percig tart.")
			u.rekey(c, rng)
		}
	}
	fs := core.NewFS(c, u.me, rng, ro)
	fs.OnError = func(e error) {
		u.mw.Synchronize(func() { errBox(u.mw, "Az automatikus mentés nem sikerült!", e) })
	}
	m, err := startMount(fs, path, letter)
	if err != nil {
		fs.Close()
		if !ro {
			core.ReleaseLock(path)
		}
		errBox(u.mw, "A felcsatolás nem sikerült.", err)
		return
	}
	if !ro {
		m.hbStop = make(chan struct{})
		go u.heartbeat(m, c)
	}
	u.mnt = m
	st.LastPath = path
	st.Letters[strings.ToLower(path)] = letter
	u.saveSettings(st)
	u.refreshMountState()
	u.showInfo(c, 0, nil)
}

// heartbeat – 5 percenként életjel a foglalásba; ha elvették, figyelmeztet.
func (u *mainUI) heartbeat(m *mount, c *core.Container) {
	t := time.NewTicker(core.HeartbeatEvery)
	defer t.Stop()
	warned := false
	for {
		select {
		case <-m.hbStop:
			return
		case <-t.C:
			if err := core.Heartbeat(m.path, u.me, u.partners, c.H.Generation); err != nil && !warned {
				warned = true
				u.mw.Synchronize(func() {
					errBox(u.mw, "FIGYELEM: "+err.Error()+".\nCsatold le a konténert, és egyeztess a partnereddel!", nil)
				})
			}
		}
	}
}

func (u *mainUI) unmount() bool {
	if u.mnt == nil {
		return true
	}
	m := u.mnt
	u.mw.SetCursor(walk.CursorWait())
	if m.hbStop != nil {
		close(m.hbStop)
	}
	err := m.stop()
	if err == nil && !m.fs.ReadOnly() {
		if l, _ := core.ReadLock(m.path, u.me, u.partners); l != nil && l.Mine(u.me) {
			core.ReleaseLock(m.path)
		}
	}
	u.mw.SetCursor(walk.CursorArrow())
	u.mnt = nil
	u.refreshMountState()
	if err != nil {
		errBox(u.mw, "Lecsatolás közben hiba történt a mentésnél! A foglalás megmaradt.", err)
		return false
	}
	return true
}

// ---- Elvitel / Visszahozás ----

func (u *mainUI) carryOut() {
	if u.mnt != nil {
		infoBox(u.mw, "Előbb csatold le a felcsatolt konténert.")
		return
	}
	st := u.loadSettings()
	fd := &walk.FileDialog{Title: "Melyik konténert viszed el?", Filter: "RealPassGAL konténer (*.gal)|*.gal", InitialDirPath: filepath.Dir(st.LastPath)}
	if ok, _ := fd.ShowOpen(u.mw); !ok {
		return
	}
	src := fd.FilePath
	if l, _ := core.ReadLock(src, u.me, u.partners); l != nil && !(l.Mine(u.me) && l.Stale()) {
		errBox(u.mw, "A konténer foglalt: "+l.Who()+"\nMost nem vihető el.", nil)
		return
	}
	c, err := core.OpenContainer(src, u.me, u.partners, u.lookupKeybook, false)
	if err != nil {
		errBox(u.mw, "A konténer nem nyitható meg.", err)
		return
	}
	gen, uuid := c.H.Generation, c.H.UUID
	behind := core.SyncBehind(src, c, u.me, u.partners)
	c.Close()
	if behind != "" {
		errBox(u.mw, behind, nil)
		return
	}
	sd := &walk.FileDialog{Title: "Hova viszed? (pl. laptop, pendrive)", Filter: "RealPassGAL konténer (*.gal)|*.gal",
		InitialDirPath: core.DownloadsDir(), FilePath: filepath.Join(core.DownloadsDir(), filepath.Base(src))}
	if ok, _ := sd.ShowSave(u.mw); !ok {
		return
	}
	dst := ensureExt(sd.FilePath, ".gal")
	if strings.EqualFold(filepath.Clean(dst), filepath.Clean(src)) {
		errBox(u.mw, "A cél nem lehet ugyanaz a fájl.", nil)
		return
	}
	if err := core.AcquireLock(src, u.me, gen, true, core.Hostname()+": "+dst); err != nil {
		errBox(u.mw, "A foglalás nem sikerült.", err)
		return
	}
	err = runWithProgress(u.mw, "Elvitel – másolás", func(p core.ProgressFunc) error { return core.CopyFile(dst, src, p) })
	if err != nil {
		core.ReleaseLock(src)
		errBox(u.mw, "A másolás nem sikerült, a foglalást visszavontam.", err)
		return
	}
	st.Carried[strings.ToLower(dst)] = carried{Source: src, UUID: uuid[:], Generation: gen}
	u.saveSettings(st)
	infoBox(u.mw, "Elvitted:\n"+dst+"\n\nAz eredeti ("+src+") mostantól a partnereidnél csak olvasható.\n"+
		"Ha végeztél, a „Visszahozás…” gombbal tedd vissza.")
}

func (u *mainUI) carryBack() {
	if u.mnt != nil {
		infoBox(u.mw, "Előbb csatold le a felcsatolt konténert.")
		return
	}
	st := u.loadSettings()
	fd := &walk.FileDialog{Title: "Melyik elvitt konténert hozod vissza?", Filter: "RealPassGAL konténer (*.gal)|*.gal", InitialDirPath: core.DownloadsDir()}
	if ok, _ := fd.ShowOpen(u.mw); !ok {
		return
	}
	dst := fd.FilePath
	rec, ok := st.Carried[strings.ToLower(dst)]
	if !ok {
		errBox(u.mw, "Erről a fájlról nincs elviteli feljegyzés ezen a gépen.\n(Csak azon a gépen hozható vissza, ahol elvitted.)", nil)
		return
	}
	src := rec.Source
	l, _ := core.ReadLock(src, u.me, u.partners)
	if l == nil || !l.Mine(u.me) || !l.Offline {
		who := "senki"
		if l != nil {
			who = l.Who()
		}
		if walk.MsgBox(u.mw, appTitle, "Az eredetin már nincs meg az elviteli foglalásod (most: "+who+").\n"+
			"Lehet, hogy valaki feloldotta és dolgozott vele.\n\nFolytatod az ellenőrzéssel?", walk.MsgBoxYesNo|walk.MsgBoxIconWarning) != walk.DlgCmdYes {
			return
		}
	}
	co, err := core.OpenContainer(src, u.me, u.partners, u.lookupKeybook, false)
	if err != nil {
		errBox(u.mw, "Az eredeti konténer nem nyitható meg.", err)
		return
	}
	srcGen, srcSigner := co.H.Generation, co.SignedBy
	co.Close()
	cc, err := core.OpenContainer(dst, u.me, u.partners, u.lookupKeybook, false)
	if err != nil {
		errBox(u.mw, "Az elvitt konténer nem nyitható meg.", err)
		return
	}
	sameUUID := string(cc.H.UUID[:]) == string(rec.UUID)
	cc.Close()
	switch {
	case !sameUUID:
		errBox(u.mw, "Ez nem ugyanaz a konténer, amit elvittél.", nil)
		return
	case srcGen != rec.Generation:
		errBox(u.mw, "ÜTKÖZÉS: az elvitel óta "+srcSigner+" módosította az eredetit ("+core.Nth(srcGen)+" mentés).\n"+
			"Nem írom felül, mert elveszne a munkája.\n\nMegoldás: csatold fel a kettőt egymás után, és másold át kézzel a változásaidat.", nil)
		return
	}
	err = runWithProgress(u.mw, "Visszahozás – másolás", func(p core.ProgressFunc) error { return core.CopyFile(src, dst, p) })
	if err != nil {
		errBox(u.mw, "A visszamásolás nem sikerült; az eredeti érintetlen, a foglalás megmaradt.", err)
		return
	}
	if c, err := core.OpenContainer(src, u.me, u.partners, u.lookupKeybook, false); err == nil {
		core.WriteState(src, u.me, c)
		c.Close()
	}
	core.ReleaseLock(src)
	delete(st.Carried, strings.ToLower(dst))
	u.saveSettings(st)
	infoBox(u.mw, "Visszahozva, a foglalás feloldva.\n\nAz elvitt másolat ("+dst+") megmaradt; ha már nem kell, töröld nyugodtan.")
}

var _ = errors.New

// rekey – újrakulcsolás folyamatjelzővel; hiba esetén a régi kulcs marad érvényben.
func (u *mainUI) rekey(c *core.Container, rng *core.RNG) bool {
	kb := u.lookupKeybook(c.H.KeybookID)
	if kb == nil {
		errBox(u.mw, "Az újrakulcsoláshoz hiányzik a kulcskönyv.", nil)
		return false
	}
	err := runWithProgress(u.mw, "Újrakulcsolás", func(p core.ProgressFunc) error {
		return c.Rekey(u.me, u.partners, kb, rng, func(done, total int64) bool {
			return p(done*core.BlockSize, total*core.BlockSize)
		})
	})
	if err != nil {
		errBox(u.mw, "Az újrakulcsolás most nem sikerült; a régi kulcs érvényes marad, minden adat rendben van.", err)
		return false
	}
	infoBox(u.mw, "Kész!\n"+c.KeyAgeText())
	return true
}

// rekeyNow – kézi újrakulcsolás (nem kell kivárni a 30 napot).
func (u *mainUI) rekeyNow() {
	if u.mnt != nil {
		infoBox(u.mw, "Előbb csatold le a felcsatolt konténert.")
		return
	}
	st := u.loadSettings()
	fd := &walk.FileDialog{Title: "Melyik konténert kulcsoljuk újra?", Filter: "RealPassGAL konténer (*.gal)|*.gal", InitialDirPath: filepath.Dir(st.LastPath)}
	if ok, _ := fd.ShowOpen(u.mw); !ok {
		return
	}
	path := fd.FilePath
	if l, _ := core.ReadLock(path, u.me, u.partners); l != nil && !(l.Mine(u.me) && !l.Offline) {
		errBox(u.mw, "A konténer foglalt: "+l.Who()+"\nMost nem kulcsolható újra.", nil)
		return
	}
	c, err := core.OpenContainer(path, u.me, u.partners, u.lookupKeybook, true)
	if err != nil {
		errBox(u.mw, "A konténer nem nyitható meg.", err)
		return
	}
	defer c.Close()
	if msg := core.SyncBehind(path, c, u.me, u.partners); msg != "" {
		errBox(u.mw, msg, nil)
		return
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
	if u.rekey(c, rng) {
		u.showInfo(c, 0, nil)
	}
}
