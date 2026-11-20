//go:build windows

package main

// Fotós mód: RealPassGAL.exe -kepek
// Mintaadatokkal ("Minta Péter", "Minta Anna") sorban megnyitja az ablakokat, és
// mindegyikről PNG-t ment a program melletti "kepek" mappába. A valódi kulcsokhoz
// és konténerekhez nem nyúl; a mintaadatok egy ideiglenes almappába kerülnek,
// amelyet a végén töröl.

import (
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"github.com/lxn/walk"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"

	"realpassgal/core"
)

type photo struct {
	out          string
	count        int
	failed       []string
	progressShot string
	container    string // mintakonténer valódi helye
	display      string // ahogy a képeken látszik
}

var demo *photo

var procDwmGetWindowAttribute = windows.NewLazySystemDLL("dwmapi.dll").NewProc("DwmGetWindowAttribute")

type shotForm interface {
	Starting() *walk.Event
	Synchronize(func())
	Handle() win.HWND
}

// front – előtérbe hozza az ablakot (UI-szálon hívandó).
func front(hwnd win.HWND) {
	win.SetForegroundWindow(hwnd)
	win.BringWindowToTop(hwnd)
}

// visibleRect – az ablak LÁTHATÓ kerete (a Windows 10/11 láthatatlan keretei nélkül).
func visibleRect(hwnd win.HWND) win.RECT {
	var r win.RECT
	hr, _, _ := procDwmGetWindowAttribute.Call(uintptr(hwnd), 9, uintptr(unsafe.Pointer(&r)), unsafe.Sizeof(r)) // DWMWA_EXTENDED_FRAME_BOUNDS
	if hr != 0 {
		win.GetWindowRect(hwnd, &r)
	}
	return r
}

// capture – amit a képernyőn az ablak helyén látni, PNG-be (bármely szálról hívható).
func (p *photo) capture(hwnd win.HWND, name string) {
	r := visibleRect(hwnd)
	w, h := r.Right-r.Left, r.Bottom-r.Top
	if w <= 0 || h <= 0 {
		p.failed = append(p.failed, name)
		return
	}
	screen := win.GetDC(0)
	defer win.ReleaseDC(0, screen)
	dc := win.CreateCompatibleDC(screen)
	defer win.DeleteDC(dc)
	bmp := win.CreateCompatibleBitmap(screen, w, h)
	defer win.DeleteObject(win.HGDIOBJ(bmp))
	old := win.SelectObject(dc, win.HGDIOBJ(bmp))
	win.BitBlt(dc, 0, 0, w, h, screen, r.Left, r.Top, win.SRCCOPY|win.CAPTUREBLT)
	win.SelectObject(dc, old)

	var bi win.BITMAPINFO
	bi.BmiHeader.BiSize = uint32(unsafe.Sizeof(bi.BmiHeader))
	bi.BmiHeader.BiWidth = w
	bi.BmiHeader.BiHeight = -h // felülről lefelé
	bi.BmiHeader.BiPlanes = 1
	bi.BmiHeader.BiBitCount = 32
	bi.BmiHeader.BiCompression = win.BI_RGB
	buf := make([]byte, int(w)*int(h)*4)
	if win.GetDIBits(dc, bmp, 0, uint32(h), &buf[0], &bi, win.DIB_RGB_COLORS) == 0 {
		p.failed = append(p.failed, name)
		return
	}
	img := image.NewRGBA(image.Rect(0, 0, int(w), int(h)))
	for i := 0; i < len(buf); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = buf[i+2], buf[i+1], buf[i], 255
	}
	f, err := os.Create(filepath.Join(p.out, name+".png"))
	if err != nil {
		p.failed = append(p.failed, name)
		return
	}
	defer f.Close()
	if png.Encode(f, img) != nil {
		p.failed = append(p.failed, name)
		return
	}
	p.count++
}

// shot – ablak megnyílása után: kitöltés (prep), előtérbe hozás, várakozás, fotó, bezárás (done).
func (p *photo) shot(f shotForm, name string, prep func(), done func()) {
	f.Starting().Attach(func() {
		go func() {
			time.Sleep(600 * time.Millisecond)
			if prep != nil {
				f.Synchronize(prep)
			}
			f.Synchronize(func() { front(f.Handle()) })
			time.Sleep(1200 * time.Millisecond) // hadd rajzoljon ki mindent
			p.capture(f.Handle(), name)
			if done != nil {
				f.Synchronize(done)
			}
		}()
	})
}

// msgBox – üzenetablak megjelenítése, lefotózása és bezárása.
func (p *photo) msgBox(owner walk.Form, title, text string, style walk.MsgBoxStyle, name string, dismiss uintptr) {
	go func() {
		t16, _ := syscall.UTF16PtrFromString(title)
		cls, _ := syscall.UTF16PtrFromString("#32770")
		for i := 0; i < 50; i++ {
			time.Sleep(100 * time.Millisecond)
			if h := win.FindWindow(cls, t16); h != 0 {
				win.SetForegroundWindow(h)
				time.Sleep(1000 * time.Millisecond)
				p.capture(h, name)
				win.PostMessage(h, win.WM_COMMAND, dismiss, 0)
				return
			}
		}
		p.failed = append(p.failed, name)
	}()
	walk.MsgBox(owner, title, text, style)
}

// demoMouse – „egérmozgás” egy szép görbe mentén, hogy a csík kb. 3/4-ig teljen.
func demoMouse(onMove func(x, y int, b walk.MouseButton), area *walk.CustomWidget) {
	b := area.ClientBoundsPixels()
	cx, cy := float64(b.Width)/2, float64(b.Height)/2
	for i := 0; i < 380; i++ {
		t := float64(i) / 380 * 2 * math.Pi
		x := cx + 0.42*float64(b.Width)*math.Sin(3*t)
		y := cy + 0.38*float64(b.Height)*math.Sin(4*t+0.6)
		onMove(int(x), int(y), 0)
	}
}

func (a *app) shownDir() string {
	if demo != nil || bemutato != nil {
		return `C:\Users\MintaPeter\RealPassGAL\adatok` // fotós mód: valódi útvonal ne látszódjon
	}
	return a.dir
}

func runPhotoMode() {
	appDir, err := core.AppDir()
	if err != nil {
		return
	}
	out := filepath.Join(appDir, "kepek")
	base := filepath.Join(out, "_minta")
	os.RemoveAll(base)
	dataDir := filepath.Join(base, "adatok")
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		errBox(nil, "Nem tudom létrehozni a kepek mappát.", err)
		return
	}
	defer os.RemoveAll(base)
	demo = &photo{out: out, container: filepath.Join(base, "titkos.gal"), display: `C:\Users\MintaPeter\OneDrive\titkos.gal`}

	// mintaadatok
	rng, _ := core.NewRNG()
	me, _ := core.NewIdentity("Minta Péter", rng)
	anna, _ := core.NewIdentity("Minta Anna", rng)
	a := &app{dir: dataDir, password: "minta", me: me, partners: []*core.PublicKey{anna.Public()}}
	kb, _ := core.NewKeybook(rng)
	kb.Save(core.KeybookPath(dataDir, kb), me.StorageKey, rng)
	c, err := core.CreateContainer(demo.container, 24<<20, me, a.partners, kb, rng, nil)
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
	}{{"/Tervek/alaprajz.pdf", 900 << 10}, {"/Tervek/homlokzat.jpg", 1400 << 10}, {"/Számlák/2026-11.pdf", 120 << 10}, {"/szerződés.pdf", 300 << 10}} {
		id, _ := fs.Create(f.p)
		fs.WriteAt(id, make([]byte, f.n), 0)
		fs.Release(id)
	}
	fs.Close()
	a.saveSettings(settings{LastPath: demo.display, Letters: map[string]string{}, Carried: map[string]carried{}})

	// 1–3: belépés, új kulcspár, egérablak, kész kulcspár
	a.loginDialog(nil)
	a.newIdentityWizard(nil)
	collectEntropy(nil, "A kulcspárodhoz véletlen számokat gyűjtünk.")
	demo.msgBox(nil, appTitle, "Elkészült a kulcspárod!\n\nUjjlenyomatod:\n"+me.Public().Fingerprint()+
		"\n\nEzt kell a partnerednek felolvasnod a találkozón vagy telefonon.\n\n"+
		"FONTOS: a jelszót nem lehet visszaállítani. Ha elfelejted, a kulcsod elveszik.",
		walk.MsgBoxIconInformation, "04_kulcspar_kesz", win.IDOK)

	// 5–: főablak, a többit a photoScript végzi
	a.mainWindow()

	msg := fmt.Sprintf("Elkészült %d kép ide:\n%s", demo.count, out)
	if len(demo.failed) > 0 {
		msg += fmt.Sprintf("\n\nNem sikerült: %v", demo.failed)
	}
	infoBox(nil, msg)
}

// photoScript – a főablakon belüli képek sorban.
func (u *mainUI) photoScript() {
	ui := func(f func()) {
		done := make(chan struct{})
		u.mw.Synchronize(func() { f(); close(done) })
		<-done
	}
	wait := func(ms int) { time.Sleep(time.Duration(ms) * time.Millisecond) }
	snap := func(name string) {
		ui(func() { front(u.mw.Handle()) })
		wait(1000)
		demo.capture(u.mw.Handle(), name)
	}
	anna := u.partners[0]
	var kb *core.Keybook
	if len(u.keybooks) > 0 {
		kb = u.keybooks[0]
	}

	wait(900)
	ui(func() { u.tw.SetCurrentIndex(0) })
	wait(600)
	snap("05_fo_kulcsok")
	ui(func() {
		demo.msgBox(u.mw, "Ujjlenyomat ellenőrzése", fmt.Sprintf("Név: %s\nUjjlenyomat:\n%s\n\nPONTOSAN egyezik ez azzal az ujjlenyomattal, amit %s személyesen vagy telefonon felolvasott neked?\n\nHa nem vagy biztos benne, válaszd a Nem-et!",
			anna.Name, anna.Fingerprint(), anna.Name), walk.MsgBoxYesNo|walk.MsgBoxIconQuestion|walk.MsgBoxDefButton2, "06_ujjlenyomat_ellenorzes", win.IDNO)
	})
	ui(func() { u.tw.SetCurrentIndex(1) })
	wait(600)
	snap("07_kulcskonyv")
	if kb != nil {
		ui(func() {
			demo.msgBox(u.mw, appTitle, "Átvéve "+anna.Name+" kulcskönyve: #"+kb.ShortID()+
				"\n\nEllenőrzőkód: "+kb.CheckCode()+"\nHasonlítsd össze azzal, amit "+anna.Name+" lát!\n\n"+
				"Az átadó fájlt (pendrive-on) ezután nyugodtan törölheted.", walk.MsgBoxIconInformation, "08_kulcskonyv_atveve", win.IDOK)
		})
	}
	ui(func() { u.tw.SetCurrentIndex(2) })
	wait(400)
	ui(func() { u.newContainer() })
	demo.progressShot = "10_kontener_letrehozasa"
	ui(func() {
		runWithProgress(u.mw, "Konténer létrehozása – véletlen feltöltés", func(p core.ProgressFunc) error {
			for i := int64(0); i <= 50; i++ {
				p(i*(6<<30)/50, 6<<30)
				time.Sleep(50 * time.Millisecond)
			}
			return nil
		})
	})
	ui(func() {
		if c, err := core.OpenContainer(demo.container, u.me, u.partners, u.lookupKeybook, false); err == nil {
			n, verr := c.Verify(nil)
			c.Path = demo.display
			u.showInfo(c, n, verr)
			c.Close()
		}
		u.mountLbl.SetText("Felcsatolva: X:  ←  " + demo.display + "  (írható, lefoglalva)")
	})
	wait(700)
	snap("11_kontener_felcsatolva")
	ui(func() { u.mountDialog() })
	since := time.Now().Add(-40 * time.Minute).Format("2006-01-02 15:04")
	ui(func() {
		demo.msgBox(u.mw, appTitle, "A konténert most "+anna.Name+" (ANNA-PC, "+since+" óta) használja.\n\nA konténer CSAK OLVASHATÓ lesz.\nOK = tovább, Mégse = kilépés",
			walk.MsgBoxOKCancel|walk.MsgBoxIconWarning, "13_foglalt_csak_olvashato", win.IDCANCEL)
	})
	ui(func() {
		demo.msgBox(u.mw, appTitle, "Kész!\nKulcs: #000001 · 0 napos – friss, ropogós!", walk.MsgBoxIconInformation, "14_ujrakulcsolas_kesz", win.IDOK)
	})
	ui(func() { showHelp(u.mw) })
	wait(300)
	ui(func() { u.mw.Close() })
}
