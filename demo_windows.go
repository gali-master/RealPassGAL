//go:build windows

package main

// Bemutató mód: RealPassGAL.exe -demo  (vagy Súgó → Bemutató indítása)
// Mintaadatokkal („Minta Péter”, „Minta Anna”) végigvezet a nulláról a használaton.
// A valódi programablakok mozognak, mellettük narrátorpanel: Vissza / Tovább /
// Automatikus / fejezetek, és ha van magyar hang a gépen, felolvasás.
// A valódi kulcsokhoz nem nyúl; minden mintaadat ideiglenes mappában van.

import (
	"bytes"
	_ "embed"
	"fmt"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"

	"realpassgal/core"
)

var (
	cNavy  = walk.RGB(16, 44, 59)
	cTeal  = walk.RGB(8, 127, 131)
	cGold  = walk.RGB(242, 184, 75)
	cWhite = walk.RGB(255, 255, 255)
	cGreen = walk.RGB(22, 135, 74)
	cMuted = walk.RGB(86, 112, 122)
	cInk   = walk.RGB(20, 43, 53)
)

type demoStep struct {
	title string
	text  string
	run   func(d *demoCtl)
}

type demoCtl struct {
	u       *mainUI
	steps   []demoStep
	step    int
	next    int
	epoch   int
	cur     *walk.Dialog
	pumping bool
	quit    bool
	auto    bool

	nar                *walk.Dialog
	counter, titleL    *walk.Label
	textL              *walk.TextLabel
	prevPB, nextPB     *walk.PushButton
	autoPB             *walk.PushButton
	chapCB             *walk.ComboBox
	voiceCB            *walk.CheckBox
	voiceInfo          *walk.Label
	chapGuard          bool
	voiceOK            bool
	ps1, tmp           string
	speech             *exec.Cmd
	playing            bool
	playID             int
	recorded           bool
	anna               *core.Identity
	contA, contB, disp string
}

var bemutato *demoCtl

//go:embed szefor.png
var szeforPNG []byte

var procMci = syscall.NewLazyDLL("winmm.dll").NewProc("mciSendStringW")

// mci – Windows médialejátszó parancs (mp3/wav lejátszás). UI-szálon hívjuk.
func mci(cmd string) (string, error) {
	buf := make([]uint16, 256)
	p, _ := syscall.UTF16PtrFromString(cmd)
	r, _, _ := procMci.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&buf[0])), 256, 0)
	if r != 0 {
		return "", fmt.Errorf("mci hiba %d", r)
	}
	return syscall.UTF16ToString(buf), nil
}

// ---- animáció ----

type anim struct {
	d  *demoCtl
	ep int
}

func (a *anim) alive() bool { return a.d.epoch == a.ep && !a.d.quit }

func (a *anim) do(fn func()) bool {
	if !a.alive() {
		return false
	}
	done := make(chan struct{})
	a.d.u.mw.Synchronize(func() {
		if a.alive() {
			fn()
		}
		close(done)
	})
	<-done
	return a.alive()
}

func (a *anim) sleep(ms int) bool {
	time.Sleep(time.Duration(ms) * time.Millisecond)
	return a.alive()
}

func (a *anim) typeText(le *walk.LineEdit, s string, after func()) bool {
	cur := []rune{}
	for _, r := range []rune(s) {
		cur = append(cur, r)
		t := string(cur)
		if !a.do(func() {
			le.SetText(t)
			if after != nil {
				after()
			}
		}) || !a.sleep(65) {
			return false
		}
	}
	return true
}

// attach – a bemutató egy ablakát „átveszi”: a néző nem kattinthat bele, a script mozgatja.
func (d *demoCtl) attach(dlg *walk.Dialog, script func(a *anim)) {
	d.cur = dlg
	win.EnableWindow(dlg.Handle(), false)
	if script == nil {
		return
	}
	a := &anim{d: d, ep: d.epoch}
	go func() {
		if a.sleep(500) {
			script(a)
		}
	}()
}

func (d *demoCtl) moved() bool { return d.next >= 0 || d.quit }

// panel – saját bemutató-ablak (pl. telefonhívás, intéző), a stage fölött.
func (d *demoCtl) panel(title string, w, h int, children []Widget, script func(a *anim)) {
	if d.moved() {
		return
	}
	var dlg *walk.Dialog
	err := Dialog{
		AssignTo: &dlg,
		Title:    title,
		MinSize:  Size{Width: w, Height: h},
		Layout:   VBox{Margins: Margins{Left: 18, Top: 16, Right: 18, Bottom: 16}, Spacing: 10},
		Children: children,
	}.Create(d.u.mw)
	if err != nil {
		return
	}
	d.attach(dlg, script)
	dlg.Run()
	d.cur = nil
}

// ---- navigáció ----

func (d *demoCtl) goTo(n int) {
	if n < 0 || n >= len(d.steps) || d.quit {
		return
	}
	d.next = n
	d.epoch++
	d.stopSpeech()
	if d.cur != nil {
		d.cur.Cancel() // a futó pumpa átveszi
		return
	}
	if d.pumping {
		return
	}
	d.pumping = true
	for d.next >= 0 && !d.quit {
		s := d.next
		d.next = -1
		d.enter(s)
	}
	d.pumping = false
}

func (d *demoCtl) enter(s int) {
	d.step = s
	d.epoch++
	ep := d.epoch
	d.updateNarrator()
	d.setStage(s)
	if !d.speak(d.steps[s].text, ep) {
		d.armAuto(ep, d.readTime(s))
	}
	d.steps[s].run(d)
}

func (d *demoCtl) readTime(s int) time.Duration {
	ms := 3000 + 55*len([]rune(d.steps[s].text))
	if ms < 9000 {
		ms = 9000
	}
	return time.Duration(ms) * time.Millisecond
}

func (d *demoCtl) armAuto(ep int, after time.Duration) {
	if !d.auto {
		return
	}
	time.AfterFunc(after, func() {
		d.u.mw.Synchronize(func() {
			if d.auto && d.epoch == ep && d.step < len(d.steps)-1 {
				d.goTo(d.step + 1)
			}
		})
	})
}

func (d *demoCtl) exit() {
	if d.quit {
		return
	}
	d.quit = true
	d.epoch++
	d.stopSpeech()
	if d.cur != nil {
		d.cur.Cancel()
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		d.u.mw.Synchronize(func() { d.u.mw.Close() })
	}()
}

// ---- narrátor ----

func (d *demoCtl) updateNarrator() {
	d.counter.SetText(fmt.Sprintf("%d / %d", d.step+1, len(d.steps)))
	d.titleL.SetText(d.steps[d.step].title)
	d.textL.SetText(d.steps[d.step].text)
	d.chapGuard = true
	d.chapCB.SetCurrentIndex(d.step)
	d.chapGuard = false
	d.prevPB.SetEnabled(d.step > 0)
	d.nextPB.SetEnabled(d.step < len(d.steps)-1)
	if d.step == len(d.steps)-1 && d.auto {
		d.auto = false
		d.autoPB.SetText("▶ Automatikus")
	}
}

func (d *demoCtl) buildNarrator() {
	chapters := []string{}
	for i, s := range d.steps {
		chapters = append(chapters, fmt.Sprintf("%d. %s", i+1, s.title))
	}
	Dialog{
		AssignTo: &d.nar,
		Title:    "RealPassGAL – Bemutató",
		MinSize:  Size{Width: 360, Height: 520},
		MaxSize:  Size{Width: 380, Height: 1200},
		Layout:   VBox{MarginsZero: true, SpacingZero: true},
		Children: []Widget{
			Composite{
				Background: SolidColorBrush{Color: cNavy},
				Layout:     HBox{Margins: Margins{Left: 12, Top: 10, Right: 18, Bottom: 10}},
				Children: []Widget{
					ImageView{Image: szeforBitmap(), Mode: ImageViewModeZoom, MinSize: Size{Width: 80, Height: 116}, MaxSize: Size{Width: 80, Height: 116}},
					Composite{
						Layout: VBox{MarginsZero: true},
						Children: []Widget{
							Label{Text: "RealPassGAL · BEMUTATÓ", TextColor: cWhite, Font: Font{PointSize: 11, Bold: true}},
							Label{Text: "A széfőr mesél", TextColor: cGold, Font: Font{PointSize: 10}},
							VSpacer{},
							Label{AssignTo: &d.counter, Text: "1 / 13", TextColor: cGold, Font: Font{PointSize: 13, Bold: true}},
						},
					},
					HSpacer{},
				},
			},
			Composite{
				Layout: VBox{Margins: Margins{Left: 18, Top: 16, Right: 18, Bottom: 12}, Spacing: 10},
				Children: []Widget{
					Label{AssignTo: &d.titleL, TextColor: cTeal, Font: Font{PointSize: 15, Bold: true}},
					TextLabel{AssignTo: &d.textL, TextColor: cInk, Font: Font{Family: "Segoe UI", PointSize: 11}, MinSize: Size{Width: 334, Height: 300}, MaxSize: Size{Width: 334, Height: 600}},
					VSpacer{},
					ComboBox{AssignTo: &d.chapCB, Model: chapters, OnCurrentIndexChanged: func() {
						if !d.chapGuard && d.chapCB.CurrentIndex() >= 0 {
							d.goTo(d.chapCB.CurrentIndex())
						}
					}},
					Composite{
						Layout: HBox{MarginsZero: true},
						Children: []Widget{
							PushButton{AssignTo: &d.prevPB, Text: "◀ Vissza", OnClicked: func() { d.goTo(d.step - 1) }},
							PushButton{AssignTo: &d.nextPB, Text: "Tovább ▶", OnClicked: func() { d.goTo(d.step + 1) }},
							PushButton{AssignTo: &d.autoPB, Text: "▶ Automatikus", OnClicked: func() {
								d.auto = !d.auto
								if d.auto {
									d.autoPB.SetText("⏸ Szünet")
									d.armAuto(d.epoch, 1500*time.Millisecond)
								} else {
									d.autoPB.SetText("▶ Automatikus")
								}
							}},
						},
					},
					Composite{
						Layout: HBox{MarginsZero: true},
						Children: []Widget{
							CheckBox{AssignTo: &d.voiceCB, Text: "Felolvasás", Enabled: false, OnCheckedChanged: func() {
								if d.voiceCB.Checked() {
									d.speak(d.steps[d.step].text, d.epoch)
								} else {
									d.stopSpeech()
								}
							}},
							HSpacer{},
							PushButton{Text: "Kilépés", OnClicked: d.exit},
						},
					},
					Label{AssignTo: &d.voiceInfo, Text: "Magyar hangot keresek…", TextColor: cMuted, Font: Font{PointSize: 8}},
					Label{Text: "Mintaadatok – a valódi kulcsaidhoz nem nyúl.", TextColor: cMuted, Font: Font{PointSize: 8}},
				},
			},
		},
	}.Create(d.u.mw)
	d.nar.Closing().Attach(func(canceled *bool, _ walk.CloseReason) {
		if !d.quit {
			*canceled = true
			d.exit()
		}
	})
}

// ---- felolvasás (Windows magyar hang: OneCore vagy SAPI) ----

const speakPS1 = `param([string]$TextFile, [switch]$Check)
$ErrorActionPreference = 'Stop'
$w = $null; $sp = $null
try {
  Add-Type -AssemblyName System.Runtime.WindowsRuntime
  $null = [Windows.Media.SpeechSynthesis.SpeechSynthesizer, Windows.Media.SpeechSynthesis, ContentType = WindowsRuntime]
  $null = [Windows.Storage.Streams.DataReader, Windows.Storage.Streams, ContentType = WindowsRuntime]
  $w = [Windows.Media.SpeechSynthesis.SpeechSynthesizer]::AllVoices | Where-Object { $_.Language -like 'hu*' } | Select-Object -First 1
} catch { $w = $null }
if (-not $w) {
  try {
    Add-Type -AssemblyName System.Speech
    $s = New-Object System.Speech.Synthesis.SpeechSynthesizer
    $v = $s.GetInstalledVoices() | Where-Object { $_.VoiceInfo.Culture.Name -like 'hu*' } | Select-Object -First 1
    if ($v) { $sp = @($s, $v.VoiceInfo.Name) }
  } catch { $sp = $null }
}
if ($Check) { if ($w -or $sp) { exit 0 } else { exit 3 } }
$text = [IO.File]::ReadAllText($TextFile, [Text.Encoding]::UTF8)
if ($w) {
  $asTask = ([System.WindowsRuntimeSystemExtensions].GetMethods() | Where-Object { $_.Name -eq 'AsTask' -and $_.GetParameters().Count -eq 1 -and $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncOperation` + "`" + `1' })[0]
  function Await($op, [Type]$t) { $task = $asTask.MakeGenericMethod($t).Invoke($null, @($op)); $task.Wait(-1) | Out-Null; $task.Result }
  $syn = New-Object Windows.Media.SpeechSynthesis.SpeechSynthesizer
  $syn.Voice = $w
  $stream = Await ($syn.SynthesizeTextToStreamAsync($text)) ([Windows.Media.SpeechSynthesis.SpeechSynthesisStream])
  $size = [uint32]$stream.Size
  $reader = New-Object Windows.Storage.Streams.DataReader($stream.GetInputStreamAt(0))
  Await ($reader.LoadAsync($size)) ([uint32]) | Out-Null
  $bytes = New-Object byte[] $size
  $reader.ReadBytes($bytes)
  $ms = New-Object System.IO.MemoryStream(,$bytes)
  (New-Object System.Media.SoundPlayer($ms)).PlaySync()
} else {
  $sp[0].SelectVoice($sp[1]); $sp[0].Speak($text)
}
`

func (d *demoCtl) psCmd(args ...string) *exec.Cmd {
	c := exec.Command("powershell.exe", append([]string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", d.ps1}, args...)...)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	return c
}

func (d *demoCtl) initVoice() {
	if d.audioFile(0) != "" {
		d.recorded = true
		d.voiceInfo.SetText("Hang: felvett hangfájlok (hang mappa)")
		d.voiceCB.SetEnabled(true)
		d.voiceCB.SetChecked(true)
	}
	d.ps1 = filepath.Join(d.tmp, "felolvas.ps1")
	if os.WriteFile(d.ps1, []byte(speakPS1), 0600) != nil {
		return
	}
	go func() {
		ok := d.psCmd("-Check").Run() == nil
		d.u.mw.Synchronize(func() {
			d.voiceOK = ok
			if d.recorded {
				return
			}
			if ok {
				d.voiceInfo.SetText("Hang: a Windows magyar hangja")
				d.voiceCB.SetEnabled(true)
				d.voiceCB.SetChecked(true) // OnCheckedChanged elindítja az aktuális lépést
			} else {
				d.voiceInfo.SetText("Nincs magyar hang a gépen – csak szöveg")
			}
		})
	}()
}

// speak – true, ha elindult a felolvasás (ilyenkor az automatikus léptetés a végét várja).
func (d *demoCtl) speak(text string, ep int) bool {
	if d.voiceCB == nil || !d.voiceCB.Checked() || d.quit {
		return false
	}
	if f := d.audioFile(d.step); f != "" {
		return d.playFile(f, ep)
	}
	if !d.voiceOK {
		return false
	}
	d.stopSpeech()
	f := filepath.Join(d.tmp, fmt.Sprintf("szoveg_%d.txt", ep))
	if os.WriteFile(f, []byte(text), 0600) != nil {
		return false
	}
	c := d.psCmd(f)
	if c.Start() != nil {
		return false
	}
	d.speech = c
	go func() {
		c.Wait()
		os.Remove(f)
		d.u.mw.Synchronize(func() {
			if d.speech == c && d.epoch == ep {
				d.speech = nil
				d.armAuto(ep, 1200*time.Millisecond)
			}
		})
	}()
	return true
}

func (d *demoCtl) stopSpeech() {
	if d.speech != nil && d.speech.Process != nil {
		d.speech.Process.Kill()
	}
	d.speech = nil
	if d.playing {
		mci("stop rpgvoice")
		mci("close rpgvoice")
		d.playing = false
		d.playID++
	}
}

// ---- a „színpad” (a valódi főablak) állapota lépésenként ----

func (d *demoCtl) showContainer(path string) {
	if c, err := core.OpenContainer(path, d.u.me, d.u.partners, d.u.lookupKeybook, false); err == nil {
		n, verr := c.Verify(nil)
		c.Path = d.disp
		d.u.showInfo(c, n, verr)
		c.Close()
	}
}

func (d *demoCtl) setStage(s int) {
	u := d.u
	wasVisible := u.mw.Visible()
	u.mw.SetVisible(s >= 3)
	if s >= 3 && !wasVisible {
		d.layout()
	}
	if s >= 6 {
		u.partners = []*core.PublicKey{d.anna.Public()}
	} else {
		u.partners = nil
	}
	u.refreshPartners()
	if s >= 6 {
		u.loadKeybooks()
	} else {
		u.keybooks = nil
	}
	u.refreshKeybooks()
	switch {
	case s >= 11:
		d.showContainer(d.contB)
	case s >= 9:
		d.showContainer(d.contA)
	default:
		u.infoTE.SetText("Itt jelennek meg a konténer adatai.")
	}
	if s >= 9 && s <= 11 {
		u.mountLbl.SetText("Felcsatolva: X:  ←  " + d.disp + "  (írható, lefoglalva)")
	} else {
		u.mountLbl.SetText("Nincs felcsatolt konténer.")
	}
	switch {
	case s <= 5:
		u.tw.SetCurrentIndex(0)
	case s == 6:
		u.tw.SetCurrentIndex(1)
	default:
		u.tw.SetCurrentIndex(2)
	}
}

// ---- a lépések szkriptjei a valódi ablakokhoz ----

func (d *demoCtl) wizardScript(nameLE, pw1LE, pw2LE *walk.LineEdit, upd func()) func(a *anim) {
	return func(a *anim) {
		pw := "lila traktor énekel a kamrában"
		_ = a.typeText(nameLE, "Minta Péter", nil) && a.sleep(400) &&
			a.typeText(pw1LE, pw, upd) && a.sleep(300) && a.typeText(pw2LE, pw, nil)
	}
}

func (d *demoCtl) entropyScript(area *walk.CustomWidget, onMove func(x, y int, b walk.MouseButton)) func(a *anim) {
	return func(a *anim) {
		var w, h float64
		if !a.do(func() { b := area.ClientBoundsPixels(); w, h = float64(b.Width), float64(b.Height) }) {
			return
		}
		for i := 0; i < 560; i++ {
			t := float64(i) / 560 * 2 * math.Pi
			x := w/2 + 0.42*w*math.Sin(3*t)
			y := h/2 + 0.38*h*math.Sin(4*t+0.6)
			if !a.do(func() { onMove(int(x), int(y), 0) }) || !a.sleep(8) {
				return
			}
		}
	}
}

func (d *demoCtl) sizeScript(sizeLE *walk.LineEdit) func(a *anim) {
	return func(a *anim) {
		_ = a.do(func() { sizeLE.SetText("") }) && a.typeText(sizeLE, "6", nil)
	}
}

func (d *demoCtl) mountScript() func(a *anim) {
	return func(a *anim) {
		if a.sleep(2600) {
			a.do(func() {
				if d.cur != nil {
					d.cur.Cancel() // „Felcsatolás” – a bemutatóban virtuálisan
				}
			})
		}
	}
}

// ---- a lépések ----

func (d *demoCtl) buildSteps() {
	u := d.u
	big := func(s string) Widget {
		return Label{Text: s, TextColor: cNavy, Font: Font{PointSize: 13, Bold: true}}
	}
	line := func(p **walk.Label) Widget {
		return Label{AssignTo: p, Text: " ", TextColor: cInk, Font: Font{PointSize: 11}}
	}
	d.steps = []demoStep{
		{"Mi ez?", "Ez a RealPassGAL: egy zsebre tehető, titkosított széf. Egyetlen fájl, amely lehet a gépeden, egy pendrive-on vagy a OneDrive-on. Aki kulcs nélkül nézi, csak értelmetlen adathalmazt lát. Te kinyitod, és a Windowsban meghajtóként használod. Nézzük meg a nulláról, hogyan kezd hozzá Minta Péter és Minta Anna.",
			func(d *demoCtl) {
				var l1, l2, l3 *walk.Label
				d.panel("RealPassGAL", 560, 320, []Widget{
					Composite{
						Background: SolidColorBrush{Color: cNavy},
						Layout:     VBox{Margins: Margins{Left: 24, Top: 24, Right: 24, Bottom: 24}, Spacing: 8},
						Children: []Widget{
							Label{Text: "RealPassGAL", TextColor: cGold, Font: Font{PointSize: 12, Bold: true}},
							Label{AssignTo: &l1, Text: " ", TextColor: cWhite, Font: Font{PointSize: 22, Bold: true}},
							Label{AssignTo: &l2, Text: " ", TextColor: cWhite, Font: Font{PointSize: 22, Bold: true}},
							Label{AssignTo: &l3, Text: " ", TextColor: cWhite, Font: Font{PointSize: 13}},
						},
					},
				}, func(a *anim) {
					_ = a.do(func() { l1.SetText("Egy fájl.") }) && a.sleep(900) &&
						a.do(func() { l2.SetText("Két lakat.") }) && a.sleep(900) &&
						a.do(func() { l3.SetText("Csak azok nyitják ki, akiket te beengedsz.") })
				})
			}},
		{"Első indítás", "Első indításkor a program megkérdezi a neved, és kér egy jelszót. A jelszó nem a kulcs, hanem a lakat a kulcsfájlodon. A legjobb néhány véletlen szóból álló mondat: könnyű megjegyezni, nehéz kitalálni. Az erősségjelző menet közben mutatja, hol tartasz.",
			func(d *demoCtl) { u.app.newIdentityWizard(u.mw); d.cur = nil }},
		{"Véletlen az egérből", "A kulcs a véletlenből születik. Péter összevissza mozgatja az egeret, a program pedig a mozdulatokat összekeveri a Windows saját véletlenjével. Amikor a csík megtelik, elég véletlen gyűlt össze.",
			func(d *demoCtl) { collectEntropy(u.mw, "A kulcspárodhoz véletlen számokat gyűjtünk."); d.cur = nil }},
		{"Kész a kulcspár", "Elkészült Péter kulcspárja. Az ujjlenyomat egy rövid kód, amely a kulcsát azonosítja. Ezt fogja felolvasni Annának, hogy biztosan egymás kulcsát kapják meg. Közben megjelent a program főablaka is.",
			func(d *demoCtl) {
				d.panel(appTitle, 520, 220, []Widget{
					big("Elkészült a kulcspárod!"),
					Label{Text: "Ujjlenyomatod:", TextColor: cMuted},
					LineEdit{Text: u.me.Public().Fingerprint(), ReadOnly: true, Font: Font{Family: "Consolas", PointSize: 11}},
					Label{Text: "Ezt kell a partnerednek felolvasnod a találkozón vagy telefonon.", TextColor: cInk},
				}, nil)
			}},
		{"Kulcscsere", "Péter és Anna kicserélik a nyilvános kulcsukat. Ez nem titok, mehet e-mailben vagy üzenetben is: a nyilvános kulccsal senki nem tud kinyitni semmit, csak lezárni valamit a partner számára.",
			func(d *demoCtl) {
				var l1, l2, l3 *walk.Label
				d.panel("Kulcscsere", 540, 240, []Widget{
					big("Nyilvános kulcsok cseréje"),
					line(&l1), line(&l2), line(&l3),
				}, func(a *anim) {
					_ = a.do(func() { l1.SetText("✉  Péter → Anna:   Minta Péter.galpub   (e-mailben)") }) && a.sleep(1300) &&
						a.do(func() { l2.SetText("✉  Anna → Péter:   Minta Anna.galpub   (e-mailben)") }) && a.sleep(1300) &&
						a.do(func() { l3.SetText("A nyilvános kulcs nem titok – lehallgatni sem érdemes.") })
				})
			}},
		{"Ujjlenyomat-egyeztetés", "Egyetlen dolgot kell ellenőrizni: hogy tényleg Anna kulcsa érkezett-e meg. Anna telefonon felolvassa az ujjlenyomatát, Péter pedig összeveti azzal, amit a program mutat. Ha minden karakter egyezik, Anna bekerül a partnerek közé.",
			func(d *demoCtl) {
				fp := d.anna.Public().Fingerprint()
				var heard *walk.LineEdit
				var ok *walk.Label
				d.panel("📞 Anna hív", 560, 300, []Widget{
					big("Anna felolvassa az ujjlenyomatát"),
					Label{Text: "Ezt hallod a telefonban:", TextColor: cMuted},
					LineEdit{AssignTo: &heard, ReadOnly: true, Font: Font{Family: "Consolas", PointSize: 12}},
					Label{Text: "Ezt mutatja a programod:", TextColor: cMuted},
					LineEdit{Text: fp, ReadOnly: true, Font: Font{Family: "Consolas", PointSize: 12}},
					Label{AssignTo: &ok, Text: " ", TextColor: cGreen, Font: Font{PointSize: 12, Bold: true}},
				}, func(a *anim) {
					for i := 4; i <= len(fp); i += 5 {
						end := i
						if end > len(fp) {
							end = len(fp)
						}
						if !a.do(func() { heard.SetText(fp[:end]) }) || !a.sleep(550) {
							return
						}
					}
					if a.do(func() { heard.SetText(fp); ok.SetText("✓ Minden karakter egyezik – Anna felvéve a partnerek közé") }) {
						a.do(func() { u.partners = []*core.PublicKey{d.anna.Public()}; u.refreshPartners() })
					}
				})
			}},
		{"Kulcskönyv", "Most jön a második lakat kulcsa: a közös kulcskönyv, egymillió titkos oldallal. Péter generálja, és pendrive-on, kézből kézbe adja át Annának – ez soha nem jár a neten. Végül összevetik az ellenőrzőkódot: ha egyezik, a két könyv azonos.",
			func(d *demoCtl) {
				code := "KK-…"
				if len(u.keybooks) > 0 {
					code = u.keybooks[0].CheckCode()
				}
				var l1, l2, l3, l4 *walk.Label
				d.panel("Kulcskönyv átadása", 560, 270, []Widget{
					big("Kézből kézbe, pendrive-on"),
					line(&l1), line(&l2), line(&l3), line(&l4),
				}, func(a *anim) {
					_ = a.do(func() { l1.SetText("1.  Péter: új kulcskönyv – 1 048 576 titkos oldal") }) && a.sleep(1200) &&
						a.do(func() { l2.SetText("2.  Pendrive-ra: csak Anna kulcsával nyitható") }) && a.sleep(1200) &&
						a.do(func() { l3.SetText("3.  Anna átveszi a saját gépén") }) && a.sleep(1200) &&
						a.do(func() { l4.SetText("4.  Péter: " + code + "   Anna: " + code + "   ✓ egyezik") })
				})
			}},
		{"Új konténer", "Péter létrehozza a közös széfet. Megadja a méretét és a helyét – most a OneDrive-on –, és bepipálja, hogy Anna is hozzáférhet.",
			func(d *demoCtl) { u.newContainer(); d.cur = nil }},
		{"Feltöltés", "A program a teljes méretet véletlen adattal tölti fel. Így kívülről az sem látszik, mennyi valódi adat van a széfben.",
			func(d *demoCtl) {
				err := runWithProgress(u.mw, "Konténer létrehozása – véletlen feltöltés", func(p core.ProgressFunc) error {
					for i := int64(0); i <= 100; i++ {
						if !p(i*(6<<30)/100, 6<<30) {
							return core.ErrCancelled
						}
						time.Sleep(45 * time.Millisecond)
					}
					return nil
				})
				d.cur = nil
				if err == nil && !d.moved() {
					d.showContainer(d.contA)
					d.panel(appTitle, 440, 160, []Widget{big("Kész a konténer:"), Label{Text: d.disp, TextColor: cInk}}, nil)
				}
			}},
		{"Felcsatolás és munka", "Kész a széf. Péter felcsatolja, és a Windowsban megjelenik egy új meghajtó. Úgy használja, mint egy pendrive-ot: másol, megnyit, átnevez. A mentés magától történik, a lemezre minden titkosítva kerül.",
			func(d *demoCtl) {
				u.mountDialog()
				d.cur = nil
				if d.moved() {
					return
				}
				var lb *walk.ListBox
				var st *walk.Label
				items := []string{"📁  Tervek", "📁  Számlák", "📄  szerződés.pdf"}
				d.panel("X:\\  –  titkos  (RealPassGAL)", 560, 330, []Widget{
					LineEdit{Text: "X:\\", ReadOnly: true},
					ListBox{AssignTo: &lb, Font: Font{PointSize: 11}},
					Label{AssignTo: &st, Text: " ", TextColor: cMuted},
				}, func(a *anim) {
					for i := range items {
						n := i + 1
						if !a.do(func() { lb.SetModel(items[:n]) }) || !a.sleep(500) {
							return
						}
					}
					_ = a.do(func() { st.SetText("Péter bemásol egy fájlt…") }) && a.sleep(1300) &&
						a.do(func() { lb.SetModel(append(append([]string{}, items...), "📄  ajánlat.docx")) }) && a.sleep(1500) &&
						a.do(func() { st.SetText("✓ Mentve – titkosítva, 15 másodperccel a változás után, magától") })
				})
			}},
		{"Anna is jönne", "Közben Anna is megnyitná ugyanazt a széfet. A program látja, hogy Péter éppen dolgozik benne, ezért Annánál csak olvasható módban nyílik meg. Így nem írhatják felül egymás munkáját.",
			func(d *demoCtl) {
				since := time.Now().Add(-25 * time.Minute).Format("15:04")
				d.panel("Minta Anna gépén – RealPassGAL", 540, 220, []Widget{
					big("⚠  A konténer foglalt"),
					Label{Text: "A konténert most Minta Péter (PETER-PC, " + since + " óta) használja.", TextColor: cInk, Font: Font{PointSize: 11}},
					Label{Text: "A konténer CSAK OLVASHATÓ lesz.", TextColor: cInk, Font: Font{PointSize: 11, Bold: true}},
					Label{Text: "OK = tovább, Mégse = kilépés", TextColor: cMuted},
				}, nil)
			}},
		{"Újrakulcsolás", "Harminc naponta a program lapoz egyet a közös kulcskönyvben: új kulcs, és a teljes tartalom újratitkosítva. A kulcs így mindig friss, a régi mentések pedig továbbra is kinyithatók maradnak.",
			func(d *demoCtl) {
				err := runWithProgress(u.mw, "Újrakulcsolás", func(p core.ProgressFunc) error {
					for i := int64(0); i <= 60; i++ {
						if !p(i*(24<<20)/60, 24<<20) {
							return core.ErrCancelled
						}
						time.Sleep(50 * time.Millisecond)
					}
					return nil
				})
				d.cur = nil
				if err == nil && !d.moved() {
					d.showContainer(d.contB)
					d.panel(appTitle, 440, 160, []Widget{big("Kész!"), Label{Text: "Kulcs: #000001 · 0 napos – friss, ropogós!", TextColor: cTeal, Font: Font{PointSize: 12, Bold: true}}}, nil)
				}
			}},
		{"Összefoglaló", "Ennyi az egész. Egyszer: kulcspár, kulcscsere, kulcskönyv és a széf létrehozása. Utána minden nap csak felcsatolás, munka, lecsatolás. Egy fájl, két lakat – és csak azok nyitják ki, akiket te beengedsz.",
			func(d *demoCtl) {
				var ls [7]*walk.Label
				ws := []Widget{big("Amit láttál")}
				for i := range ls {
					ws = append(ws, line(&ls[i]))
				}
				texts := []string{"✓  Saját kulcspár – a jelszó csak a lakat rajta", "✓  Kulcscsere – az ujjlenyomat dönt", "✓  Közös kulcskönyv – kézből kézbe", "✓  Széf bárhol: gépen, pendrive-on, OneDrive-on", "✓  Meghajtóként nyílik, magától ment", "✓  Foglalás: senki nem írja felül a másikat", "✓  30 naponta friss kulcs"}
				d.panel("Összefoglaló", 520, 340, ws, func(a *anim) {
					for i := range texts {
						k := i
						if !a.do(func() { ls[k].SetText(texts[k]) }) || !a.sleep(600) {
							return
						}
					}
				})
			}},
	}
}

// ---- indítás ----

func startDemoProcess() {
	exe, err := os.Executable()
	if err == nil {
		err = exec.Command(exe, "-demo").Start()
	}
	if err != nil {
		errBox(nil, "A bemutató nem indult el.", err)
	}
}

func runDemoMode() {
	tmp, err := os.MkdirTemp("", "RealPassGAL_bemutato_")
	if err != nil {
		errBox(nil, "Nem tudok ideiglenes mappát létrehozni.", err)
		return
	}
	defer os.RemoveAll(tmp)
	dataDir := filepath.Join(tmp, "adatok")
	os.MkdirAll(dataDir, 0700)

	rng, _ := core.NewRNG()
	me, _ := core.NewIdentity("Minta Péter", rng)
	anna, _ := core.NewIdentity("Minta Anna", rng)
	partners := []*core.PublicKey{anna.Public()}
	kb, _ := core.NewKeybook(rng)
	kb.Save(core.KeybookPath(dataDir, kb), me.StorageKey, rng)
	contA := filepath.Join(tmp, "titkos.gal")
	c, err := core.CreateContainer(contA, 24<<20, me, partners, kb, rng, nil)
	if err != nil {
		errBox(nil, "A mintakonténer nem készült el.", err)
		return
	}
	fs := core.NewFS(c, me, rng, false)
	fs.Mkdir("/Tervek")
	fs.Mkdir("/Számlák")
	for _, f := range []struct {
		p string
		n int
	}{{"/Tervek/alaprajz.pdf", 900 << 10}, {"/Számlák/2026-11.pdf", 120 << 10}, {"/szerződés.pdf", 300 << 10}} {
		id, _ := fs.Create(f.p)
		fs.WriteAt(id, make([]byte, f.n), 0)
		fs.Release(id)
	}
	fs.Close()
	contB := filepath.Join(tmp, "titkos_ujrakulcsolt.gal")
	core.CopyFile(contB, contA, nil)
	if cb, err := core.OpenContainer(contB, me, partners, func([16]byte) *core.Keybook { return kb }, true); err == nil {
		cb.Rekey(me, partners, kb, rng, nil)
		cb.Close()
	}

	disp := `C:\Users\MintaPeter\OneDrive\titkos.gal`
	a := &app{dir: dataDir, password: "minta", me: me}
	a.saveSettings(settings{LastPath: disp, Letters: map[string]string{}, Carried: map[string]carried{}})
	bemutato = &demoCtl{anna: anna, contA: contA, contB: contB, disp: disp, tmp: tmp, next: -1}
	a.mainWindow()
}

// start – a főablak (színpad) indulásakor.
func (d *demoCtl) start(u *mainUI) {
	d.u = u
	d.buildSteps()
	d.buildNarrator()
	d.nar.Show()
	d.layout() // Show után, hogy a Windows ne tegye vissza középre
	u.mw.SetVisible(false)
	d.initVoice()
	go func() {
		time.Sleep(300 * time.Millisecond)
		u.mw.Synchronize(func() { d.goTo(0) })
	}()
}

func szeforBitmap() *walk.Bitmap {
	im, err := png.Decode(bytes.NewReader(szeforPNG))
	if err != nil {
		return nil
	}
	b, err := walk.NewBitmapFromImage(im)
	if err != nil {
		return nil
	}
	return b
}

// audioFile – felvett hang a lépéshez: hang\01.mp3 (vagy .wav / .m4a) az exe mellett.
func (d *demoCtl) audioFile(step int) string {
	dir, err := core.AppDir()
	if err != nil {
		return ""
	}
	for _, ext := range []string{".mp3", ".wav", ".m4a"} {
		p := filepath.Join(dir, "hang", fmt.Sprintf("%02d%s", step+1, ext))
		if st, err := os.Stat(p); err == nil && st.Size() > 0 {
			return p
		}
	}
	return ""
}

func (d *demoCtl) playFile(f string, ep int) bool {
	d.stopSpeech()
	if _, err := mci(`open "` + f + `" type mpegvideo alias rpgvoice`); err != nil {
		return false
	}
	if _, err := mci("play rpgvoice"); err != nil {
		mci("close rpgvoice")
		return false
	}
	d.playing = true
	d.playID++
	id := d.playID
	go func() {
		for {
			time.Sleep(300 * time.Millisecond)
			mode := ""
			done := make(chan struct{})
			d.u.mw.Synchronize(func() {
				if d.playing && d.playID == id {
					mode, _ = mci("status rpgvoice mode")
				}
				close(done)
			})
			<-done
			if mode == "playing" {
				continue
			}
			d.u.mw.Synchronize(func() {
				if d.playing && d.playID == id {
					d.playing = false
					mci("close rpgvoice")
					if d.epoch == ep {
						d.armAuto(ep, 1200*time.Millisecond)
					}
				}
			})
			return
		}
	}()
	return true
}

// layout – egy monitoron, egymás mellett: balra a program, jobbra a keskeny narrátor.
func (d *demoCtl) layout() {
	u := d.u
	// egy monitoron, egymás mellett: balra a program, jobbra a keskeny narrátor
	hm := win.MonitorFromWindow(u.mw.Handle(), win.MONITOR_DEFAULTTONEAREST)
	var mi win.MONITORINFO
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	win.GetMonitorInfo(hm, &mi)
	wa := mi.RcWork
	scale := float64(win.GetDpiForWindow(u.mw.Handle())) / 96
	if scale <= 0 {
		scale = 1
	}
	px := func(v int) int32 { return int32(float64(v) * scale) }
	workW, workH := wa.Right-wa.Left, wa.Bottom-wa.Top
	narW, gap := px(380), px(10)
	stageW := px(840)
	if max := workW - narW - gap - px(30); stageW > max {
		stageW = max
	}
	h := px(640)
	if h > workH-px(30) {
		h = workH - px(30)
	}
	x0 := wa.Left + (workW-(stageW+gap+narW))/2
	y0 := wa.Top + (workH-h)/2
	win.SetWindowPos(u.mw.Handle(), 0, x0, y0, stageW, h, win.SWP_NOZORDER)
	win.SetWindowPos(d.nar.Handle(), win.HWND_TOPMOST, x0+stageW+gap, y0, narW, h, 0)
}
