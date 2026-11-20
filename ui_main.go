//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"

	"realpassgal/core"
)

type mainUI struct {
	*app
	mw        *walk.MainWindow
	partnerLB *walk.ListBox
	kbLB      *walk.ListBox
	infoTE    *walk.TextEdit
	mountLbl  *walk.Label
	tw        *walk.TabWidget
	mnt       *mount
	keybooks  []*core.Keybook
}

func (a *app) mainWindow() {
	u := &mainUI{app: a}
	u.loadKeybooks()
	fp := a.me.Public().Fingerprint()

	err := MainWindow{
		AssignTo: &u.mw,
		Title:    appTitle + " – " + a.me.Name,
		MinSize:  Size{Width: 640, Height: 480},
		Size:     Size{Width: 720, Height: 540},
		Layout:   VBox{},
		MenuItems: []MenuItem{
			Menu{
				Text: "&Súgó",
				Items: []MenuItem{
					Action{Text: "Súgó", Shortcut: Shortcut{Key: walk.KeyF1}, OnTriggered: func() { showHelp(u.mw) }},
					Action{Text: "Bemutató indítása", OnTriggered: startDemoProcess},
					Action{Text: "Névjegy", OnTriggered: func() {
						infoBox(u.mw, "RealPassGAL – 5. lépés (kulcsok, konténer, felcsatolás, foglalás, újrakulcsolás)\nX25519 + Ed25519 + kulcskönyv, XChaCha20-Poly1305, Argon2id")
					}},
				},
			},
		},
		Children: []Widget{
			Label{Text: "Üdvözlöm, " + a.me.Name + "!", Font: Font{PointSize: 16, Bold: true}},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					Label{Text: "Ujjlenyomatod:"},
					LineEdit{Text: fp, ReadOnly: true, Font: Font{Family: "Consolas", PointSize: 11}},
				},
			},
			TabWidget{
				AssignTo: &u.tw,
				Pages: []TabPage{
					{
						Title:  "Kulcsok",
						Layout: VBox{},
						Children: []Widget{
							Composite{
								Layout: HBox{MarginsZero: true},
								Children: []Widget{
									PushButton{Text: "Nyilvános kulcsom exportálása…", OnClicked: u.exportPub},
									HSpacer{},
								},
							},
							GroupBox{
								Title:  "Partnerek (legfeljebb 3)",
								Layout: VBox{},
								Children: []Widget{
									ListBox{AssignTo: &u.partnerLB, Font: Font{Family: "Consolas", PointSize: 10}},
									Composite{
										Layout: HBox{MarginsZero: true},
										Children: []Widget{
											PushButton{Text: "Partner importálása…", OnClicked: u.importPartner},
											PushButton{Text: "Partner törlése", OnClicked: u.removePartner},
											HSpacer{},
										},
									},
								},
							},
						},
					},
					{
						Title:  "Kulcskönyv",
						Layout: VBox{},
						Children: []Widget{
							Label{Text: "A kulcskönyv 1 048 576 darab 32 bájtos kulcs (32 MB). Személyesen, pendrive-on adjátok át.\nÁtadás után hasonlítsátok össze az ellenőrzőkódot (KK-…): ha egyezik, a két könyv azonos."},
							ListBox{AssignTo: &u.kbLB, Font: Font{Family: "Consolas", PointSize: 10}},
							Composite{
								Layout: HBox{MarginsZero: true},
								Children: []Widget{
									PushButton{Text: "Új kulcskönyv generálása…", OnClicked: u.newKeybook},
									PushButton{Text: "Átadás partnernek (pendrive)…", OnClicked: u.exportKeybook},
									PushButton{Text: "Átvétel (importálás)…", OnClicked: u.importKeybook},
									HSpacer{},
								},
							},
						},
					},
					{
						Title:  "Konténer",
						Layout: VBox{},
						Children: []Widget{
							Composite{
								Layout: HBox{MarginsZero: true},
								Children: []Widget{
									PushButton{Text: "Felcsatolás…", OnClicked: u.mountDialog},
									PushButton{Text: "Lecsatolás", OnClicked: func() {
										if u.unmount() {
											infoBox(u.mw, "Lecsatolva és mentve.")
										}
									}},
									PushButton{Text: "Elvitel…", OnClicked: u.carryOut},
									PushButton{Text: "Visszahozás…", OnClicked: u.carryBack},
									HSpacer{},
									PushButton{Text: "Új konténer…", OnClicked: u.newContainer},
									PushButton{Text: "Megnyitás és ellenőrzés…", OnClicked: u.openContainer},
									PushButton{Text: "Újrakulcsolás most…", OnClicked: u.rekeyNow},
								},
							},
							Label{AssignTo: &u.mountLbl, Text: "Nincs felcsatolt konténer.", Font: Font{PointSize: 10, Bold: true}},
							TextEdit{
								AssignTo: &u.infoTE,
								ReadOnly: true,
								VScroll:  true,
								Font:     Font{Family: "Consolas", PointSize: 10},
								Text:     "Itt jelennek meg a konténer adatai.",
							},
						},
					},
				},
			},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					Label{Text: "Adatmappa: " + a.shownDir()},
					HSpacer{},
					PushButton{Text: "Súgó (F1)", OnClicked: func() { showHelp(u.mw) }},
				},
			},
		},
	}.Create()
	if err != nil {
		errBox(nil, "A főablak nem nyílt meg.", err)
		return
	}
	u.refreshPartners()
	u.refreshKeybooks()
	u.mw.Closing().Attach(func(canceled *bool, _ walk.CloseReason) {
		if u.mnt != nil && !u.unmount() {
			if walk.MsgBox(u.mw, appTitle, "A mentés nem sikerült. Mégis kilépsz?", walk.MsgBoxYesNo|walk.MsgBoxIconWarning) != walk.DlgCmdYes {
				*canceled = true
			}
		}
	})
	if demo != nil {
		u.mw.Starting().Attach(func() { go u.photoScript() })
	}
	if bemutato != nil {
		u.mw.Starting().Attach(func() { bemutato.start(u) })
	}
	u.mw.Run()
}

// ---- Kulcsok fül ----

func (u *mainUI) refreshPartners() {
	items := []string{}
	for _, p := range u.partners {
		items = append(items, fmt.Sprintf("%-16s %s", p.Name, p.Fingerprint()))
	}
	u.partnerLB.SetModel(items)
}

func (u *mainUI) exportPub() {
	dlg := &walk.FileDialog{
		Title:          "Nyilvános kulcs mentése",
		Filter:         "RealPassGAL nyilvános kulcs (*.galpub)|*.galpub",
		InitialDirPath: core.DownloadsDir(),
		FilePath:       filepath.Join(core.DownloadsDir(), u.me.Name+".galpub"),
	}
	if ok, _ := dlg.ShowSave(u.mw); !ok {
		return
	}
	path := ensureExt(dlg.FilePath, ".galpub")
	if err := os.WriteFile(path, u.me.Public().Marshal(), 0644); err != nil {
		errBox(u.mw, "A mentés nem sikerült.", err)
		return
	}
	infoBox(u.mw, "Elmentve:\n"+path+"\n\nEz a fájl nem titkos, nyugodtan elküldheted e-mailben is.\n"+
		"A partnered importáláskor ellenőrizze ezt az ujjlenyomatot:\n\n"+u.me.Public().Fingerprint())
}

func (u *mainUI) importPartner() {
	dlg := &walk.FileDialog{
		Title:          "Partner nyilvános kulcsa",
		Filter:         "RealPassGAL nyilvános kulcs (*.galpub)|*.galpub",
		InitialDirPath: core.DownloadsDir(),
	}
	if ok, _ := dlg.ShowOpen(u.mw); !ok {
		return
	}
	raw, err := os.ReadFile(dlg.FilePath)
	if err != nil {
		errBox(u.mw, "A fájl nem olvasható.", err)
		return
	}
	p, err := core.ParsePublicKey(raw)
	if err != nil {
		errBox(u.mw, "Ez nem érvényes RealPassGAL nyilvános kulcs.", err)
		return
	}
	q := fmt.Sprintf("Név: %s\nUjjlenyomat:\n%s\n\nPONTOSAN egyezik ez azzal az ujjlenyomattal, amit %s személyesen vagy telefonon felolvasott neked?\n\nHa nem vagy biztos benne, válaszd a Nem-et!",
		p.Name, p.Fingerprint(), p.Name)
	if walk.MsgBox(u.mw, "Ujjlenyomat ellenőrzése", q, walk.MsgBoxYesNo|walk.MsgBoxIconQuestion|walk.MsgBoxDefButton2) != walk.DlgCmdYes {
		return
	}
	ps, err := core.AddPartner(u.partners, u.me, p)
	if err != nil {
		errBox(u.mw, "Nem vehető fel.", err)
		return
	}
	u.partners = ps
	if err := u.savePartners(); err != nil {
		errBox(u.mw, "A partnerlista mentése nem sikerült.", err)
		return
	}
	u.refreshPartners()
	infoBox(u.mw, p.Name+" felvéve a partnereid közé.")
}

func (u *mainUI) removePartner() {
	i := u.partnerLB.CurrentIndex()
	if i < 0 || i >= len(u.partners) {
		infoBox(u.mw, "Előbb jelöld ki a partnert a listában.")
		return
	}
	p := u.partners[i]
	if walk.MsgBox(u.mw, appTitle, "Biztosan törlöd: "+p.Name+"?", walk.MsgBoxYesNo|walk.MsgBoxIconWarning|walk.MsgBoxDefButton2) != walk.DlgCmdYes {
		return
	}
	u.partners = append(u.partners[:i:i], u.partners[i+1:]...)
	if err := u.savePartners(); err != nil {
		errBox(u.mw, "A partnerlista mentése nem sikerült.", err)
	}
	u.refreshPartners()
}

// ---- Kulcskönyv fül ----

func (u *mainUI) loadKeybooks() {
	u.keybooks = nil
	for _, f := range core.KeybookFiles(u.dir) {
		if k, err := core.LoadKeybook(f, u.me.StorageKey); err == nil {
			u.keybooks = append(u.keybooks, k)
		}
	}
}

func (u *mainUI) refreshKeybooks() {
	items := []string{}
	for _, k := range u.keybooks {
		items = append(items, fmt.Sprintf("#%s  létrehozva: %s  ellenőrzőkód: %s",
			k.ShortID(), k.Created.Local().Format("2006-01-02 15:04"), k.CheckCode()))
	}
	if len(items) == 0 {
		items = append(items, "(még nincs kulcskönyv)")
	}
	u.kbLB.SetModel(items)
}

// saveIdentityReserve – új véletlen-tartalék és a kulcsfájl újramentése.
func (u *mainUI) saveIdentityReserve(rng *core.RNG) {
	u.me.RefreshReserve(rng)
	if err := u.me.Save(u.idPath(), u.password, rng); err != nil {
		errBox(u.mw, "A véletlen-tartalék mentése nem sikerült (a kulcskönyv rendben van).", err)
	}
}

func (u *mainUI) newKeybook() {
	pool, ok := collectEntropy(u.mw, "Az új kulcskönyvhöz véletlen számokat gyűjtünk.")
	if !ok {
		return
	}
	rng, err := core.NewRNG(pool.Digest(), u.me.Reserve)
	if err != nil {
		errBox(u.mw, "Véletlengenerátor hiba.", err)
		return
	}
	u.mw.SetCursor(walk.CursorWait())
	k, err := core.NewKeybook(rng)
	if err == nil {
		err = k.Save(core.KeybookPath(u.dir, k), u.me.StorageKey, rng)
	}
	u.saveIdentityReserve(rng)
	u.mw.SetCursor(walk.CursorArrow())
	if err != nil {
		errBox(u.mw, "A kulcskönyv létrehozása nem sikerült.", err)
		return
	}
	u.keybooks = append(u.keybooks, k)
	u.refreshKeybooks()
	infoBox(u.mw, "Kész a kulcskönyv: #"+k.ShortID()+"\nEllenőrzőkód: "+k.CheckCode()+
		"\n\nKövetkező lépés: „Átadás partnernek (pendrive)…”.")
}

func (u *mainUI) selectedKeybook() *core.Keybook {
	i := u.kbLB.CurrentIndex()
	if len(u.keybooks) == 1 {
		return u.keybooks[0]
	}
	if i < 0 || i >= len(u.keybooks) {
		return nil
	}
	return u.keybooks[i]
}

func (u *mainUI) exportKeybook() {
	k := u.selectedKeybook()
	if k == nil {
		infoBox(u.mw, "Előbb generálj vagy jelölj ki egy kulcskönyvet.")
		return
	}
	if len(u.partners) == 0 {
		infoBox(u.mw, "Előbb importáld a partnered nyilvános kulcsát (Kulcsok fül).")
		return
	}
	p := u.choosePartner()
	if p == nil {
		return
	}
	dlg := &walk.FileDialog{
		Title:          "Kulcskönyv átadó fájl mentése (pl. pendrive-ra)",
		Filter:         "RealPassGAL kulcskönyv-átadás (*.galkbx)|*.galkbx",
		InitialDirPath: core.DownloadsDir(),
		FilePath:       filepath.Join(core.DownloadsDir(), "kulcskonyv_"+k.ShortID()+"_"+p.Name+".galkbx"),
	}
	if ok, _ := dlg.ShowSave(u.mw); !ok {
		return
	}
	rng, err := core.NewRNG(u.me.Reserve)
	if err != nil {
		errBox(u.mw, "Véletlengenerátor hiba.", err)
		return
	}
	data, err := k.ExportFor(u.me, p, rng)
	if err == nil {
		err = os.WriteFile(ensureExt(dlg.FilePath, ".galkbx"), data, 0600)
	}
	if err != nil {
		errBox(u.mw, "Az átadó fájl mentése nem sikerült.", err)
		return
	}
	infoBox(u.mw, "Elmentve. A fájlt csak "+p.Name+" tudja megnyitni a saját kulcsával.\n\n"+
		"Átvétel után hasonlítsátok össze az ellenőrzőkódot:\n"+k.CheckCode()+
		"\n\nHálózaton NE küldd el – a kulcskönyv lényege, hogy csak személyesen cseréltek.")
}

func (u *mainUI) choosePartner() *core.PublicKey {
	if len(u.partners) == 1 {
		return u.partners[0]
	}
	var (
		dlg  *walk.Dialog
		cb   *walk.ComboBox
		okPB *walk.PushButton
	)
	sel := -1
	names := []string{}
	for _, p := range u.partners {
		names = append(names, p.Name)
	}
	res, _ := Dialog{
		AssignTo: &dlg, Title: "Kinek szól?", DefaultButton: &okPB,
		MinSize: Size{Width: 300, Height: 120}, Layout: VBox{},
		Children: []Widget{
			ComboBox{AssignTo: &cb, Model: names, CurrentIndex: 0},
			PushButton{AssignTo: &okPB, Text: "OK", OnClicked: func() { sel = cb.CurrentIndex(); dlg.Accept() }},
		},
	}.Run(u.mw)
	if res != walk.DlgCmdOK || sel < 0 || sel >= len(u.partners) {
		return nil
	}
	return u.partners[sel]
}

func (u *mainUI) importKeybook() {
	dlg := &walk.FileDialog{
		Title:          "Kulcskönyv átvétele",
		Filter:         "RealPassGAL kulcskönyv-átadás (*.galkbx)|*.galkbx",
		InitialDirPath: core.DownloadsDir(),
	}
	if ok, _ := dlg.ShowOpen(u.mw); !ok {
		return
	}
	raw, err := os.ReadFile(dlg.FilePath)
	if err != nil {
		errBox(u.mw, "A fájl nem olvasható.", err)
		return
	}
	k, sender, err := core.ImportKeybook(raw, u.me, u.partners)
	if err != nil {
		errBox(u.mw, "A kulcskönyv nem vehető át.", err)
		return
	}
	for _, e := range u.keybooks {
		if e.ID == k.ID {
			infoBox(u.mw, "Ez a kulcskönyv már megvan (#"+k.ShortID()+").")
			return
		}
	}
	rng, err := core.NewRNG(u.me.Reserve)
	if err == nil {
		err = k.Save(core.KeybookPath(u.dir, k), u.me.StorageKey, rng)
	}
	if err != nil {
		errBox(u.mw, "A kulcskönyv mentése nem sikerült.", err)
		return
	}
	u.keybooks = append(u.keybooks, k)
	u.refreshKeybooks()
	infoBox(u.mw, "Átvéve "+sender.Name+" kulcskönyve: #"+k.ShortID()+
		"\n\nEllenőrzőkód: "+k.CheckCode()+"\nHasonlítsd össze azzal, amit "+sender.Name+" lát!\n\n"+
		"Az átadó fájlt (pendrive-on) ezután nyugodtan törölheted.")
}

func ensureExt(path, ext string) string {
	if filepath.Ext(path) == "" {
		return path + ext
	}
	return path
}
