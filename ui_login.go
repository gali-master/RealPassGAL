//go:build windows

package main

import (
	"fmt"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"

	"realpassgal/core"
)

// newIdentityWizard – első indításkor: név, jelszó, egér, kulcspár.
func (a *app) newIdentityWizard(owner walk.Form) bool {
	var (
		dlg                  *walk.Dialog
		nameLE, pw1LE, pw2LE *walk.LineEdit
		strengthLbl          *walk.Label
		okPB, cancelPB       *walk.PushButton
	)
	updateStrength := func() {
		bits, label, ok := core.PasswordStrength(pw1LE.Text())
		s := fmt.Sprintf("Jelszó erőssége: %s (~%.0f bit)", label, bits)
		if !ok {
			s += " – legalább 12 karakter és „közepes” erősség kell"
		}
		strengthLbl.SetText(s)
	}

	accepted := false
	onOK := func() {
		name := nameLE.Text()
		if err := core.ValidName(name); err != nil {
			errBox(dlg, "Hibás név.", err)
			return
		}
		if _, _, ok := core.PasswordStrength(pw1LE.Text()); !ok {
			errBox(dlg, "A jelszó túl gyenge. Tipp: 5–6 véletlen szóból álló mondat, pl.\n„lila traktor énekel a kamrában hajnalban”.", nil)
			return
		}
		if pw1LE.Text() != pw2LE.Text() {
			errBox(dlg, "A két jelszó nem egyezik.", nil)
			return
		}
		pool, ok := collectEntropy(dlg, "A kulcspárodhoz véletlen számokat gyűjtünk.")
		if !ok {
			return
		}
		rng, err := core.NewRNG(pool.Digest())
		if err != nil {
			errBox(dlg, "Véletlengenerátor hiba.", err)
			return
		}
		id, err := core.NewIdentity(name, rng)
		if err != nil {
			errBox(dlg, "Kulcsgenerálási hiba.", err)
			return
		}
		dlg.SetCursor(walk.CursorWait())
		err = id.Save(a.idPath(), pw1LE.Text(), rng)
		dlg.SetCursor(walk.CursorArrow())
		if err != nil {
			errBox(dlg, "A kulcsfájl mentése nem sikerült.", err)
			return
		}
		a.me, a.password = id, pw1LE.Text()
		infoBox(dlg, "Elkészült a kulcspárod!\n\nUjjlenyomatod:\n"+id.Public().Fingerprint()+
			"\n\nEzt kell a partnerednek felolvasnod a találkozón vagy telefonon.\n\n"+
			"FONTOS: a jelszót nem lehet visszaállítani. Ha elfelejted, a kulcsod elveszik.")
		accepted = true
		dlg.Accept()
	}

	err := Dialog{
		AssignTo:      &dlg,
		Title:         appTitle + " – új kulcspár",
		DefaultButton: &okPB,
		CancelButton:  &cancelPB,
		MinSize:       Size{Width: 520, Height: 320},
		Layout:        Grid{Columns: 2},
		Children: []Widget{
			Label{ColumnSpan: 2, Text: "Üdvözöl a RealPassGAL!\nMég nincs kulcspárod ezen a gépen, most elkészítjük.", Font: Font{PointSize: 11, Bold: true}},
			Label{Text: "Neved:"},
			LineEdit{AssignTo: &nameLE, MaxLength: 40},
			Label{Text: "Jelszó:"},
			LineEdit{AssignTo: &pw1LE, PasswordMode: true, OnTextChanged: updateStrength},
			Label{Text: "Jelszó újra:"},
			LineEdit{AssignTo: &pw2LE, PasswordMode: true},
			Label{ColumnSpan: 2, AssignTo: &strengthLbl, Text: "Jelszó erőssége: –"},
			Label{ColumnSpan: 2, Text: "Tipp: több véletlen szóból álló mondat erősebb és könnyebb megjegyezni,\nmint a „Kutya2024!” típusú jelszó."},
			VSpacer{ColumnSpan: 2},
			Composite{
				ColumnSpan: 2,
				Layout:     HBox{MarginsZero: true},
				Children: []Widget{
					HSpacer{},
					PushButton{AssignTo: &okPB, Text: "Tovább: véletlen gyűjtése", OnClicked: onOK},
					PushButton{AssignTo: &cancelPB, Text: "Kilépés", OnClicked: func() { dlg.Cancel() }},
				},
			},
		},
	}.Create(owner)
	if err != nil {
		errBox(owner, "Az ablak nem nyílt meg.", err)
		return false
	}
	if demo != nil {
		demo.shot(dlg, "02_uj_kulcspar", func() {
			nameLE.SetText("Minta Péter")
			pw1LE.SetText("lila traktor énekel a kamrában")
			pw2LE.SetText("lila traktor énekel a kamrában")
			updateStrength()
		}, func() { dlg.Cancel() })
	}
	if bemutato != nil {
		bemutato.attach(dlg, bemutato.wizardScript(nameLE, pw1LE, pw2LE, updateStrength))
	}
	dlg.Run()
	if accepted {
		a.loadPartners()
	}
	return accepted
}

// loginDialog – jelszó bekérése; a név csak sikeres jelszó után derül ki.
func (a *app) loginDialog(owner walk.Form) bool {
	var (
		dlg            *walk.Dialog
		pwLE           *walk.LineEdit
		okPB, cancelPB *walk.PushButton
	)
	accepted := false
	onOK := func() {
		dlg.SetCursor(walk.CursorWait())
		id, err := core.LoadIdentity(a.idPath(), pwLE.Text())
		dlg.SetCursor(walk.CursorArrow())
		if err != nil {
			errBox(dlg, "Nem sikerült megnyitni a kulcsodat.", err)
			pwLE.SetText("")
			pwLE.SetFocus()
			return
		}
		a.me, a.password = id, pwLE.Text()
		if err := a.loadPartners(); err != nil {
			errBox(dlg, "A partnerlista sérült, üresen indulok.", err)
		}
		accepted = true
		dlg.Accept()
	}
	err := Dialog{
		AssignTo:      &dlg,
		Title:         appTitle,
		DefaultButton: &okPB,
		CancelButton:  &cancelPB,
		MinSize:       Size{Width: 380, Height: 160},
		Layout:        VBox{},
		Children: []Widget{
			Label{Text: "RealPassGAL", Font: Font{PointSize: 14, Bold: true}},
			Label{Text: "Add meg a jelszavad:"},
			LineEdit{AssignTo: &pwLE, PasswordMode: true},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					HSpacer{},
					PushButton{AssignTo: &okPB, Text: "Belépés", OnClicked: onOK},
					PushButton{AssignTo: &cancelPB, Text: "Kilépés", OnClicked: func() { dlg.Cancel() }},
				},
			},
		},
	}.Create(owner)
	if err != nil {
		errBox(owner, "Az ablak nem nyílt meg.", err)
		return false
	}
	if demo != nil {
		demo.shot(dlg, "01_belepes", func() { pwLE.SetText("lila traktor") }, func() { dlg.Cancel() })
	}
	dlg.Run()
	return accepted
}
