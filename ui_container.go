//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"

	"realpassgal/core"
)

func humanSize(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/(1<<20))
	default:
		return fmt.Sprintf("%d KB", b>>10)
	}
}

func (u *mainUI) lookupKeybook(id [16]byte) *core.Keybook {
	for _, k := range u.keybooks {
		if k.ID == id {
			return k
		}
	}
	return nil
}

// runWithProgress – hosszú művelet háttérszálon, folyamatjelzővel és megszakítással.
func runWithProgress(owner walk.Form, title string, work func(p core.ProgressFunc) error) error {
	var (
		dlg *walk.Dialog
		pb  *walk.ProgressBar
		lbl *walk.Label
	)
	var cancel, finished atomic.Bool
	var result error
	err := Dialog{
		AssignTo: &dlg,
		Title:    title,
		MinSize:  Size{Width: 420, Height: 140},
		Layout:   VBox{},
		Children: []Widget{
			Label{AssignTo: &lbl, Text: "Indul…"},
			ProgressBar{AssignTo: &pb, MinValue: 0, MaxValue: 1000},
			PushButton{Text: "Megszakítás", OnClicked: func() { cancel.Store(true) }},
		},
	}.Create(owner)
	if err != nil {
		return err
	}
	if demo != nil && demo.progressShot != "" {
		name := demo.progressShot
		demo.progressShot = ""
		demo.shot(dlg, name, nil, nil)
	}
	if bemutato != nil {
		bemutato.attach(dlg, nil)
	}
	dlg.Closing().Attach(func(canceled *bool, _ walk.CloseReason) {
		if !finished.Load() {
			*canceled = true
			cancel.Store(true)
		}
	})
	dlg.Starting().Attach(func() {
		go func() {
			result = work(func(done, total int64) bool {
				dlg.Synchronize(func() {
					pb.SetValue(int(done * 1000 / total))
					lbl.SetText(fmt.Sprintf("%s / %s", humanSize(done), humanSize(total)))
				})
				return !cancel.Load()
			})
			finished.Store(true)
			dlg.Synchronize(func() { dlg.Accept() })
		}()
	})
	dlg.Run()
	return result
}

func parseSize(num, unit string) (int64, error) {
	v, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(num), ",", "."), 64)
	if err != nil || v <= 0 {
		return 0, errors.New("a méret egy pozitív szám legyen (pl. 6 vagy 0,5)")
	}
	mul := float64(1 << 20)
	if unit == "GB" {
		mul = 1 << 30
	}
	return int64(v * mul), nil
}

func (u *mainUI) newContainer() {
	if len(u.keybooks) == 0 {
		infoBox(u.mw, "Előbb kell egy kulcskönyv (Kulcskönyv fül), és a partnereidnek is meg kell lennie.")
		return
	}
	appDir, _ := core.AppDir()
	defaultPath := filepath.Join(appDir, "titkos.gal")
	if demo != nil {
		defaultPath = demo.display // fotós mód: valódi útvonal ne látszódjon
	}
	if bemutato != nil {
		defaultPath = bemutato.disp
	}
	var (
		dlg            *walk.Dialog
		sizeLE, pathLE *walk.LineEdit
		unitCB, kbCB   *walk.ComboBox
		okPB, cancelPB *walk.PushButton
	)
	checks := make([]*walk.CheckBox, len(u.partners))
	partnerWidgets := []Widget{}
	for i, p := range u.partners {
		partnerWidgets = append(partnerWidgets, CheckBox{AssignTo: &checks[i], Text: p.Name + "   " + p.Fingerprint(), Checked: true})
	}
	if len(u.partners) == 0 {
		partnerWidgets = append(partnerWidgets, Label{Text: "(nincs partner – csak te fogod tudni megnyitni)"})
	}
	kbNames := []string{}
	for _, k := range u.keybooks {
		kbNames = append(kbNames, "#"+k.ShortID()+"  "+k.CheckCode())
	}

	var size int64
	var recips []*core.PublicKey
	var kb *core.Keybook
	var path string
	onOK := func() {
		var err error
		if size, err = parseSize(sizeLE.Text(), unitCB.Text()); err != nil {
			errBox(dlg, "Hibás méret.", err)
			return
		}
		if _, err = core.ComputeLayout(size); err != nil {
			errBox(dlg, "Hibás méret.", err)
			return
		}
		recips = nil
		for i, cb := range checks {
			if cb.Checked() {
				recips = append(recips, u.partners[i])
			}
		}
		kb = u.keybooks[kbCB.CurrentIndex()]
		path = ensureExt(strings.TrimSpace(pathLE.Text()), ".gal")
		if _, err := os.Stat(path); err == nil {
			errBox(dlg, "Ilyen nevű fájl már van – válassz másik nevet.", nil)
			return
		}
		dlg.Accept()
	}
	browse := func() {
		fd := &walk.FileDialog{
			Title:          "Konténer helye",
			Filter:         "RealPassGAL konténer (*.gal)|*.gal",
			InitialDirPath: filepath.Dir(pathLE.Text()),
			FilePath:       pathLE.Text(),
		}
		if ok, _ := fd.ShowSave(dlg); ok {
			pathLE.SetText(ensureExt(fd.FilePath, ".gal"))
		}
	}

	err := Dialog{
		AssignTo:      &dlg,
		Title:         "Új konténer",
		DefaultButton: &okPB,
		CancelButton:  &cancelPB,
		MinSize:       Size{Width: 560, Height: 360},
		Layout:        Grid{Columns: 3},
		Children: []Widget{
			Label{Text: "Méret:"},
			LineEdit{AssignTo: &sizeLE, Text: "1"},
			ComboBox{AssignTo: &unitCB, Model: []string{"MB", "GB"}, CurrentIndex: 1},
			Label{ColumnSpan: 3, Text: "1 MB és 100 GB között. A teljes méretet most lefoglalja és véletlen adattal tölti fel."},
			Label{Text: "Fájl:"},
			LineEdit{AssignTo: &pathLE, Text: defaultPath},
			PushButton{Text: "Tallózás…", OnClicked: browse},
			Label{Text: "Kulcskönyv:"},
			ComboBox{AssignTo: &kbCB, ColumnSpan: 2, Model: kbNames, CurrentIndex: 0},
			GroupBox{
				ColumnSpan: 3,
				Title:      "Ki férhet hozzá? (te mindig)",
				Layout:     VBox{},
				Children:   partnerWidgets,
			},
			VSpacer{ColumnSpan: 3},
			Composite{
				ColumnSpan: 3,
				Layout:     HBox{MarginsZero: true},
				Children: []Widget{
					HSpacer{},
					PushButton{AssignTo: &okPB, Text: "Tovább: véletlen gyűjtése", OnClicked: onOK},
					PushButton{AssignTo: &cancelPB, Text: "Mégse", OnClicked: func() { dlg.Cancel() }},
				},
			},
		},
	}.Create(u.mw)
	if err != nil {
		return
	}
	if demo != nil {
		demo.shot(dlg, "09_uj_kontener", func() { sizeLE.SetText("6") }, func() { dlg.Cancel() })
	}
	if bemutato != nil {
		bemutato.attach(dlg, bemutato.sizeScript(sizeLE))
	}
	if dlg.Run() != walk.DlgCmdOK {
		return
	}

	pool, ok := collectEntropy(u.mw, "Az új konténer mesterkulcsához véletlen számokat gyűjtünk.")
	if !ok {
		return
	}
	rng, err := core.NewRNG(pool.Digest(), u.me.Reserve)
	if err != nil {
		errBox(u.mw, "Véletlengenerátor hiba.", err)
		return
	}
	var c *core.Container
	err = runWithProgress(u.mw, "Konténer létrehozása – véletlen feltöltés", func(p core.ProgressFunc) error {
		var e error
		c, e = core.CreateContainer(path, size, u.me, recips, kb, rng, p)
		return e
	})
	u.saveIdentityReserve(rng)
	if errors.Is(err, core.ErrCancelled) {
		infoBox(u.mw, "Megszakítva, a félkész fájlt töröltem.")
		return
	}
	if err != nil {
		errBox(u.mw, "A konténer létrehozása nem sikerült.", err)
		return
	}
	u.showInfo(c, 0, nil)
	c.Close()
	infoBox(u.mw, "Kész a konténer:\n"+path)
}

func (u *mainUI) openContainer() {
	appDir, _ := core.AppDir()
	fd := &walk.FileDialog{
		Title:          "Konténer megnyitása",
		Filter:         "RealPassGAL konténer (*.gal)|*.gal",
		InitialDirPath: appDir,
	}
	if ok, _ := fd.ShowOpen(u.mw); !ok {
		return
	}
	c, err := core.OpenContainer(fd.FilePath, u.me, u.partners, u.lookupKeybook, false)
	if err != nil {
		u.infoTE.SetText("Nem sikerült megnyitni:\r\n" + fd.FilePath + "\r\n\r\n" + err.Error())
		errBox(u.mw, "A konténer nem nyitható meg.", err)
		return
	}
	defer c.Close()
	var n int
	verr := runWithProgress(u.mw, "Ellenőrzés", func(p core.ProgressFunc) error {
		var e error
		n, e = c.Verify(func(done, total int64) bool {
			return p(done*core.BlockSize, total*core.BlockSize)
		})
		return e
	})
	u.showInfo(c, n, verr)
}

func (u *mainUI) showInfo(c *core.Container, checked int, verr error) {
	h := c.H
	t := func(x interface{ Format(string) string }) string { return x.Format("2006-01-02 15:04") }
	lines := []string{
		"Fájl:              " + c.Path,
		"Méret:             " + humanSize(h.Layout.Total) + "  (hasznos: " + humanSize(h.Layout.Usable()) + ")",
		"Címzettek:         " + strings.Join(c.RecipientNames(), ", "),
		"Létrehozta:        " + c.Meta.CreatedBy + " · " + t(h.Created.Local()),
		fmt.Sprintf("Utoljára mentette: %s · %s · %d. generáció", c.SignedBy, t(c.Meta.SavedAt.Local()), h.Generation),
		"Kulcskönyv:        #" + fmt.Sprintf("%x", h.KeybookID[:4]),
		"                   " + c.KeyAgeText(),
		"",
	}
	if c.Warning != "" {
		lines = append(lines, "FIGYELEM: "+c.Warning)
	}
	if verr != nil {
		lines = append(lines, "ELLENŐRZÉS: HIBA – "+verr.Error())
	} else {
		lines = append(lines, fmt.Sprintf("Ellenőrzés: rendben (aláírás, fejléc, metaadat; használt blokk: %d)", checked))
	}
	u.infoTE.SetText(strings.Join(lines, "\r\n"))
}
