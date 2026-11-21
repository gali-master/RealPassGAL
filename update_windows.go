//go:build windows

package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/lxn/walk"

	"realpassgal/core"
)

// Frissítés-figyelő: induláskor (legfeljebb naponta egyszer) a háttérben megnézi, van-e újabb,
// a kiadó által aláírt verzió. A kulcsokhoz (adatok mappa) és a konténerekhez soha nem nyúl.

const (
	releaseRepo   = "gali-master/RealPassGAL"
	latestBase    = "https://github.com/" + releaseRepo + "/releases/latest/download/"
	checkInterval = 20 * time.Hour
	maxZipSize    = 300 << 20
)

// updateState – helyi, nem titkos feljegyzés (adatok\frissites.json).
type updateState struct {
	LastCheck time.Time `json:"utolso_ellenorzes"`
	Publisher string    `json:"kiado_ujjlenyomat,omitempty"` // az első elfogadott kiadó – később csak tőle fogadunk el frissítést
}

func (a *app) updateStatePath() string { return filepath.Join(a.dir, "frissites.json") }

func (a *app) loadUpdateState() updateState {
	var s updateState
	if b, err := os.ReadFile(a.updateStatePath()); err == nil {
		json.Unmarshal(b, &s)
	}
	return s
}

func (a *app) saveUpdateState(s updateState) {
	b, _ := json.MarshalIndent(s, "", "  ")
	os.WriteFile(a.updateStatePath(), b, 0600)
}

// httpClient – a nagy csomag letöltésére nincs teljes időkorlát (lassú net), csak a kapcsolódásra és a válaszra.
var httpClient = &http.Client{Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	TLSHandshakeTimeout:   30 * time.Second,
	ResponseHeaderTimeout: 30 * time.Second,
}}

func httpGet(url string, limit int64) ([]byte, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errNoSig
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("a szerver válasza: %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err == nil && int64(len(b)) > limit {
		err = errors.New("túl nagy válasz")
	}
	return b, err
}

var errNoSig = errors.New("a legújabb kiadáshoz még nincs aláírás")

// checkUpdates – manual: a menüből indítva (mindig ellenőriz, és az eredményt is kiírja).
func (u *mainUI) checkUpdates(manual bool) {
	st := u.loadUpdateState()
	if !manual && time.Since(st.LastCheck) < checkInterval {
		return
	}
	raw, err := httpGet(latestBase+core.ReleaseSigName, 64<<10)
	if !manual && err != nil {
		return // nincs internet, nincs még aláírás: csendben kihagyjuk, holnap újra
	}
	st.LastCheck = time.Now()
	u.saveUpdateState(st)
	if err != nil {
		u.mw.Synchronize(func() {
			if err == errNoSig {
				infoBox(u.mw, "Ez a legfrissebb aláírt verzió: "+Version)
			} else {
				errBox(u.mw, "A frissítés keresése nem sikerült (van internet?).", err)
			}
		})
		return
	}
	trusted := append([]*core.PublicKey{u.me.Public()}, u.partners...)
	info, signer, err := core.VerifyRelease(raw, trusted)
	if err != nil {
		u.mw.Synchronize(func() { errBox(u.mw, "Gyanús frissítés – nem telepítem.", err) })
		return
	}
	fp := hex.EncodeToString(signer.FingerprintBytes())
	if st.Publisher != "" && st.Publisher != fp {
		u.mw.Synchronize(func() {
			errBox(u.mw, "Gyanús frissítés – nem telepítem.",
				fmt.Errorf("ezt a kiadást %s írta alá, de eddig mindig más kiadótól érkeztek a frissítések", signer.Name))
		})
		return
	}
	if !core.NewerVersion(info.Version, Version) {
		if manual {
			u.mw.Synchronize(func() { infoBox(u.mw, "Ez a legfrissebb verzió: "+Version) })
		}
		return
	}
	u.mw.Synchronize(func() { u.offerUpdate(info, signer, fp) })
}

func (u *mainUI) offerUpdate(info *core.ReleaseInfo, signer *core.PublicKey, fp string) {
	notes := strings.TrimSpace(info.Notes)
	if notes == "" {
		notes = "(nincs leírás)"
	}
	msg := fmt.Sprintf("Új verzió érhető el: %s (most: %s)\nAláírta: %s\n\nMi változott:\n%s\n\n"+
		"Frissítsem most? A kulcsaidhoz és a konténereidhez nem nyúlok, utána a program újraindul.\n"+
		"(Nem = később; holnap újra szólok.)", info.Version, Version, signer.Name, notes)
	if walk.MsgBox(u.mw, appTitle+" – frissítés", msg, walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) != walk.DlgCmdYes {
		return
	}
	if u.mnt != nil {
		if walk.MsgBox(u.mw, appTitle, "A frissítéshez előbb lecsatolom a konténert (mentéssel). Mehet?",
			walk.MsgBoxOKCancel|walk.MsgBoxIconQuestion) != walk.DlgCmdOK || !u.unmount() {
			return
		}
	}
	exe, err := os.Executable()
	if err != nil {
		errBox(u.mw, "A frissítés nem sikerült.", err)
		return
	}
	appDir := filepath.Dir(exe)
	zipPath := filepath.Join(appDir, "RealPassGAL_frissites.zip")
	defer os.Remove(zipPath)
	url := "https://github.com/" + releaseRepo + "/releases/download/v" + info.Version + "/RealPassGAL.zip"
	err = runWithProgress(u.mw, "Frissítés letöltése…", func(p core.ProgressFunc) error {
		return download(url, zipPath, info.Size, p)
	})
	if err == nil {
		var sum string
		var n int64
		sum, n, err = core.FileSHA256(zipPath)
		if err == nil && (sum != strings.ToLower(info.SHA256) || (info.Size > 0 && n != info.Size)) {
			err = errors.New("a letöltött csomag ellenőrzőkódja nem egyezik az aláírttal – nem telepítem")
		}
	}
	if err == nil {
		err = core.ApplyUpdate(zipPath, appDir, filepath.Base(exe))
	}
	if err != nil {
		errBox(u.mw, "A frissítés nem sikerült. A program változatlan maradt.", err)
		return
	}
	st := u.loadUpdateState()
	st.Publisher = fp
	u.saveUpdateState(st)
	infoBox(u.mw, "A frissítés kész ("+info.Version+"). A program most újraindul.")
	releaseInstance() // az újraindított program ne higgye, hogy már fut egy példány
	if err := exec.Command(exe).Start(); err != nil {
		errBox(u.mw, "Indítsd el kézzel újra a programot.", err)
	}
	u.mw.Close()
}

func download(url, dst string, size int64, p core.ProgressFunc) error {
	resp, err := httpClient.Do(mustReq(url))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("a szerver válasza: %s", resp.Status)
	}
	total := size
	if total <= 0 {
		total = resp.ContentLength
	}
	if total <= 0 {
		total = 1
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	buf := make([]byte, 256<<10)
	var done int64
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			done += int64(n)
			if done > maxZipSize {
				return errors.New("a letöltés túl nagy")
			}
			if _, err := out.Write(buf[:n]); err != nil {
				return err
			}
			if !p(min(done, total), total) {
				return errors.New("megszakítva")
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	return out.Sync()
}

func mustReq(url string) *http.Request {
	r, _ := http.NewRequest(http.MethodGet, url, nil)
	return r
}

// ---- Kiadás aláírása (a kiadó gépén) ----

// signRelease – letölti a GitHubon lévő legújabb csomagot, megmutatja a verziót és a
// változásokat, majd a saját identitással aláírja. Az eredmény a programmappába kerül.
func (u *mainUI) signRelease() {
	exe, err := os.Executable()
	if err != nil {
		errBox(u.mw, "Az aláírás nem sikerült.", err)
		return
	}
	appDir := filepath.Dir(exe)
	zipPath := filepath.Join(appDir, "RealPassGAL_alairando.zip")
	defer os.Remove(zipPath)
	err = runWithProgress(u.mw, "A legújabb kiadás letöltése…", func(p core.ProgressFunc) error {
		return download(latestBase+"RealPassGAL.zip", zipPath, 0, p)
	})
	var ver, notes, sum string
	var size int64
	if err == nil {
		ver, err = core.ZipText(zipPath, "VERZIO.txt")
	}
	if err == nil {
		notes, _ = core.ZipText(zipPath, "VALTOZASOK.txt")
		sum, size, err = core.FileSHA256(zipPath)
	}
	if err == nil {
		_, err = core.ParseVersion(ver)
	}
	if err != nil {
		errBox(u.mw, "Az aláírás nem sikerült.", err)
		return
	}
	msg := fmt.Sprintf("A GitHubon lévő legújabb kiadás:\n\nVerzió: %s\nMéret: %s\nSHA-256: %s\n\nMi változott:\n%s\n\n"+
		"Aláírod a saját kulcsoddal (%s)? Csak akkor írd alá, ha ezt a kiadást te indítottad.",
		ver, humanSize(size), sum, notes, u.me.Name)
	if walk.MsgBox(u.mw, appTitle+" – kiadás aláírása", msg, walk.MsgBoxYesNo|walk.MsgBoxIconQuestion|walk.MsgBoxDefButton2) != walk.DlgCmdYes {
		return
	}
	raw, err := core.SignRelease(u.me, core.ReleaseInfo{Version: ver, SHA256: sum, Size: size, Notes: notes, Signed: time.Now().UTC().Truncate(time.Second)})
	if err == nil {
		err = os.MkdirAll(filepath.Join(appDir, "alairasok"), 0755)
	}
	out := filepath.Join(appDir, "alairasok", "v"+ver+".sig")
	if err == nil {
		err = os.WriteFile(out, raw, 0644)
	}
	if err != nil {
		errBox(u.mw, "Az aláírás nem sikerült.", err)
		return
	}
	infoBox(u.mw, "Az aláírás elkészült:\n"+out+"\n\nTedd fel a GitHub-tároló „alairasok” mappájába; "+
		"onnan automatikusan a kiadás mellé kerül, és a programok ettől kezdve felajánlják a frissítést.")
}
