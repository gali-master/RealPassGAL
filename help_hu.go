//go:build windows

package main

import (
	"strings"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

const helpHU = `REALPASSGAL – SÚGÓ

BEMUTATÓ: Súgó menü → „Bemutató indítása” – mintaadatokkal végigvezet a
használaton, a valódi kulcsaidhoz nem nyúl.

1. ELSŐ INDÍTÁS
Add meg a neved és egy erős jelszót. A legjobb jelszó egy 5–6 véletlen szóból
álló mondat, pl. „lila traktor énekel a kamrában hajnalban”. A jelszót NEM
lehet visszaállítani – ha elfelejted, a kulcsod elveszik.
Ezután mozgasd az egeret a sötét mezőben, amíg a csík meg nem telik: ebből
és a Windows véletlenjéből készül a kulcspárod.

2. AZ UJJLENYOMAT
A főablak tetején látod (GAL-xxxx-…). Ez azonosít téged. Ha valaki a
kulcscsere közben becsempészné a saját kulcsát, az ujjlenyomat nem egyezne.

3. KULCSCSERE A PARTNEREDDEL
 a) „Nyilvános kulcsom exportálása…” → keletkezik egy .galpub fájl
    (alapból a Letöltések mappába). Ez nem titkos, mehet e-mailben is.
 b) Partnered ugyanígy elküldi a sajátját; te „Partner importálása…”.
 c) A program kiírja a partnered ujjlenyomatát. Csak akkor fogadd el, ha
    PONTOSAN egyezik azzal, amit ő személyesen vagy telefonon felolvasott.

4. KULCSKÖNYV (csak személyesen!)
 a) Egyikőtök: „Új kulcskönyv generálása…” (egérmozgatás).
 b) „Átadás partnernek (pendrive)…” → .galkbx fájl a pendrive-ra.
    Ezt csak a címzett tudja megnyitni, ezért ha a pendrive elveszik,
    a megtaláló nem tud vele mit kezdeni.
 c) A partner: „Átvétel (importálás)…”.
 d) Hasonlítsátok össze a KK-… ellenőrzőkódot. Ha egyezik, kész.
 e) A .galkbx fájlt utána töröljétek a pendrive-ról.
 A kulcskönyvet SOHA ne küldjétek hálózaton: az ér valamit, hogy csak
 személyesen cseréltétek.

5. KONTÉNER (.gal)
 a) Konténer fül → „Új konténer…”: méret (1 MB – 100 GB), fájlnév,
    kulcskönyv, és pipáld be, mely partnerek férhetnek hozzá.
 b) Egérmozgatás után a program a teljes méretet véletlen adattal tölti fel
    (6 GB kb. 1–2 perc). Kívülről így nem látszik, mennyi benne az adat.
 c) „Megnyitás és ellenőrzés…”: kiírja, kik férnek hozzá, ki mentett
    utoljára, és milyen korú a kulcs („friss, ropogós”).

 FELCSATOLÁS (meghajtóként)
 Előfeltétel: a WinFsp telepítése (egyszer, ingyenes): https://winfsp.dev/rel/
 a) Konténer fül → „Felcsatolás…”: válaszd ki a .gal fájlt és egy szabad
    meghajtóbetűt. A program konténerenként megjegyzi a választott betűt.
 b) A meghajtó megjelenik az Intézőben és a Total Commanderben; úgy
    használod, mint egy pendrive-ot.
 c) Mentés: automatikus, 15 másodperccel az utolsó változás után.
    Nem kell semmit nyomnod.
 d) „Lecsatolás”: azonnal ment és lecsatol. A program bezárása is ezt teszi.
 e) „Csak olvasásra”: semmit nem lehet módosítani, a .gal fájl nem változik.
 Egyszerre csak egy konténer lehet felcsatolva.

 FOGLALÁS (hogy ne írjatok bele egyszerre)
 – Írható felcsatoláskor a .gal mellé kerül egy .lock fájl (ki, melyik gép).
 – Ha más használja, a program szól: OK = csak olvasható, Mégse = kilépés.
 – A program 5 percenként életjelet ír. Ha 30 percig nincs életjel (pl.
   lefagyott gép), felajánlja a foglalás feloldását.
 – A .state fájl az utolsó mentés sorszámát őrzi. Ha a OneDrive még nem
   töltötte le a friss változatot, a program csak olvashatóként engedi.

 ÚJRAKULCSOLÁS („friss, ropogós”)
 – 30 naponta a program lapoz egyet a közös kulcskönyvben: új kulcs, és a
   teljes tartalom újratitkosítása. Ez magától történik az első írható
   felcsatoláskor, miután a kulcs betöltötte a 30 napot.
 – Az „Újrakulcsolás most…” gombbal bármikor kérheted.
 – Feltétel: a konténerben legyen legalább annyi szabad hely, amennyi adat
   van benne. Ha nincs, a program szól, és nem nyúl semmihez.
 – A régi mentések (régebbi .gal példányok) továbbra is kinyithatók.

 ELVITEL / VISSZAHOZÁS (máshol dolgoznál vele, pl. laptopon)
 a) „Elvitel…”: kiválasztod a konténert és a célhelyet. A program
    lefoglalja az eredetit („elvitte: …”), és átmásolja.
 b) Az elvitt példányt bárhol felcsatolhatod és írhatod.
 c) „Visszahozás…”: kiválasztod az elvitt példányt. Ha közben senki nem
    írt az eredetibe, visszamásolja és feloldja a foglalást. Ha igen,
    nem ír felül semmit, hanem ütközést jelez.
 A visszahozás azon a gépen működik, ahol az elvitel történt.

 OneDrive: a .gal fájlon legyen beállítva a „Mindig legyen meg ezen az
 eszközön” opció, különben felcsatoláskor a Windows előbb letölti az egészet.

6. HOL VANNAK A FÁJLOK?
 A program mappájában, az "adatok" almappában (hordozható mód):
   identitas.galid    – a titkos kulcsod (jelszóval titkosítva)
   partnerek.galdb    – a partnereid (titkosítva)
   kulcskonyv_*.galkey – kulcskönyvek (titkosítva)
 Az egész programmappa egyben mozgatható. Érdemes róla biztonsági
 mentést készíteni (pl. pendrive-ra).
 Mentés nélkül, ha a gép tönkremegy, a .gal konténereidet sem tudod megnyitni.

7. BIZTONSÁGI TANÁCSOK
 – A titkosítás a program ismeretében is biztonságos: csak a kulcsokon múlik.
 – A gyenge pont a jelszó és a gép: vírusos gépen semmi sem véd.
 – Ne add ki senkinek az identitas.galid fájlt és a jelszót.

8. FRISSÍTÉS
 A program induláskor (naponta legfeljebb egyszer) a háttérben megnézi,
 van-e újabb verzió. Ha van, megmutatja, mi változott, és megkérdezi:
 frissítsen-e most. A „Frissítés most” letölti az új verziót, ellenőrzi,
 kicseréli a programot, és újraindítja. Az adatok mappához (kulcsok) és a
 konténerekhez nem nyúl. Kézzel: Súgó → Frissítés keresése.
 Biztonság: minden kiadást a kiadó a SAJÁT gépén, a saját RealPassGAL-
 kulcsával ír alá. A program csak olyan frissítést telepít, amelyet te
 vagy az egyik partnered írt alá – és mindig ugyanaz a kiadó. Így egy
 feltört GitHub-fiókkal sem lehet hamis frissítést küldeni.
 Kiadóknak: Súgó → Kiadás aláírása – letölti a GitHubon lévő legújabb
 csomagot, megmutatja a verziót, és aláírás után az „alairasok” mappába
 menti; onnan a GitHub-tároló „alairasok” mappájába kell feltenni.

9. HIBAÜZENETEK
 „hibás jelszó (vagy sérült kulcsfájl)” – rossz jelszó, vagy a fájl sérült.
 „a nyilvános kulcs aláírása érvénytelen” – a .galpub fájlt módosították.
 „ez a kulcskönyv nem neked szól” – más partnernek készült a .galkbx.
 „a küldő nincs a partnereid között” – előbb importáld a küldő .galpub-ját.
 „a fájl sérült vagy módosították” – ne használd; kérj új példányt.
 „nem vagy a konténer címzettjei között” – nem neked (is) készült.
 „hiányzik a kulcskönyv” – kérd el a létrehozótól (Kulcskönyv fül).
 „...olyan valaki mentette, aki nincs a partnereid között” – importáld
   az illető nyilvános kulcsát, vagy kérdezd meg, mi történt.
 „a fájl mérete nem egyezik” – a OneDrive még tölti; várj, és próbáld újra.
 „A felcsatoláshoz telepíteni kell a WinFsp-t” – lásd fent, Felcsatolás.
 „meghajtó nem csatolható fel” – foglalt a betű; válassz másikat.
 „megtelt a konténer” – törölj belőle, vagy készíts nagyobb konténert.
   Egy már bent lévő fájl átírásához is kell szabad hely (a régi változat
   a következő mentésig megmarad – így áramszünetnél sem vész el).
 „A legutóbbi mentés nem volt teljes…” – pl. áramszünet volt mentés
   közben; az előző ép mentés nyílt meg, az utolsó módosítások hiányozhatnak.
 „Gyanús frissítés – nem telepítem” – a letöltött frissítés aláírása
   nem stimmel. Ne erőltesd; szólj a kiadónak.
 „X használja” – foglalt; csak olvasható lesz.
 „A OneDrive még nem töltötte le…” – várj a szinkronra, utána próbáld újra.
 „ÜTKÖZÉS” – az elvitel alatt más is írt; kézzel kell egyeztetni.
 „a foglalásodat valaki feloldotta” – csatold le, és beszélj a partnereddel.`

func showHelp(owner walk.Form) {
	var dlg *walk.Dialog
	if err := (Dialog{
		AssignTo: &dlg,
		Title:    appTitle + " – Súgó",
		MinSize:  Size{Width: 700, Height: 560},
		Layout:   VBox{},
		Children: []Widget{
			TextEdit{
				Text:     strings.ReplaceAll(helpHU, "\n", "\r\n"),
				ReadOnly: true,
				VScroll:  true,
				Font:     Font{Family: "Consolas", PointSize: 10},
			},
			PushButton{Text: "Bezárás", OnClicked: func() { dlg.Accept() }},
		},
	}).Create(owner); err != nil {
		return
	}
	if demo != nil {
		demo.shot(dlg, "15_sugo", nil, func() { dlg.Cancel() })
	}
	dlg.Run()
}
