//go:build windows

package main

import (
	"fmt"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"

	"realpassgal/core"
)

// collectEntropy – egérmozgatásos véletlengyűjtés. A visszaadott pool kivonatát
// a Windows véletlennel keverve használjuk (core.NewRNG).
func collectEntropy(owner walk.Form, purpose string) (*core.EntropyPool, bool) {
	pool := core.NewEntropyPool()
	var (
		dlg      *walk.Dialog
		area     *walk.CustomWidget
		hexLbl   *walk.Label
		bar      *walk.ProgressBar
		stateLbl *walk.Label
		okPB     *walk.PushButton
		cancelPB *walk.PushButton
	)
	type pt struct{ x, y int }
	var trail []pt
	start := time.Now()

	bg, _ := walk.NewSolidColorBrush(walk.RGB(16, 24, 32))
	dot, _ := walk.NewSolidColorBrush(walk.RGB(80, 220, 120))
	defer bg.Dispose()
	defer dot.Dispose()

	onMove := func(x, y int, _ walk.MouseButton) {
		if pool.AddMouse(x, y, time.Since(start).Nanoseconds()) {
			trail = append(trail, pt{x, y})
			if len(trail) > 300 {
				trail = trail[len(trail)-300:]
			}
		}
		hexLbl.SetText(pool.Display())
		n := pool.Bits()
		if n > core.MinEntropyBits {
			n = core.MinEntropyBits
		}
		bar.SetValue(n)
		if n >= core.MinEntropyBits && !okPB.Enabled() {
			okPB.SetEnabled(true)
			stateLbl.SetText("Kész! Elég véletlen gyűlt össze. Folytathatod, vagy mozgasd még nyugodtan.")
		} else if n < core.MinEntropyBits {
			stateLbl.SetText(fmt.Sprintf("Összegyűlt: %d / %d bit", n, core.MinEntropyBits))
		}
		area.Invalidate()
	}

	paint := func(c *walk.Canvas, _ walk.Rectangle) error {
		b := area.ClientBoundsPixels()
		c.FillRectanglePixels(bg, walk.Rectangle{X: 0, Y: 0, Width: b.Width, Height: b.Height})
		for _, p := range trail {
			c.FillRectanglePixels(dot, walk.Rectangle{X: p.x - 2, Y: p.y - 2, Width: 4, Height: 4})
		}
		return nil
	}

	err := Dialog{
		AssignTo:      &dlg,
		Title:         appTitle + " – véletlen gyűjtése",
		DefaultButton: &okPB,
		CancelButton:  &cancelPB,
		MinSize:       Size{Width: 560, Height: 520},
		Layout:        VBox{},
		Children: []Widget{
			Label{Text: purpose + "\n\nMozgasd az egeret összevissza a sötét mezőben, amíg a csík meg nem telik.\nA mozgásodat a program a Windows saját véletlenjével keveri össze."},
			CustomWidget{
				AssignTo:            &area,
				MinSize:             Size{Width: 520, Height: 240},
				ClearsBackground:    true,
				InvalidatesOnResize: true,
				PaintPixels:         paint,
				OnMouseMove:         onMove,
			},
			Label{
				AssignTo: &hexLbl,
				Text:     pool.Display(),
				Font:     Font{Family: "Consolas", PointSize: 14, Bold: true},
			},
			ProgressBar{AssignTo: &bar, MinValue: 0, MaxValue: core.MinEntropyBits},
			Label{AssignTo: &stateLbl, Text: fmt.Sprintf("Összegyűlt: 0 / %d bit", core.MinEntropyBits)},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					HSpacer{},
					PushButton{AssignTo: &okPB, Text: "Tovább", Enabled: false, OnClicked: func() { dlg.Accept() }},
					PushButton{AssignTo: &cancelPB, Text: "Mégse", OnClicked: func() { dlg.Cancel() }},
				},
			},
		},
	}.Create(owner)
	if err != nil {
		errBox(owner, "Az ablak nem nyílt meg.", err)
		return nil, false
	}
	if demo != nil {
		demo.shot(dlg, "03_eger_veletlen", func() { demoMouse(onMove, area) }, func() { dlg.Cancel() })
	}
	if bemutato != nil {
		bemutato.attach(dlg, bemutato.entropyScript(area, onMove))
	}
	dlg.Run()
	if dlg.Result() != walk.DlgCmdOK || pool.Bits() < core.MinEntropyBits {
		return nil, false
	}
	return pool, true
}
