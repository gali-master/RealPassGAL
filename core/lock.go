package core

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"
)

const (
	HeartbeatEvery = 5 * time.Minute
	LockStaleAfter = 30 * time.Minute
	lockFormat     = "RealPassGAL-foglalas"
	stateFormat    = "RealPassGAL-allapot"
)

func LockPath(gal string) string  { return gal + ".lock" }
func StatePath(gal string) string { return gal + ".state" }

func Hostname() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "ismeretlen gép"
	}
	return h
}

// signedJSON – aláírt, nem titkos kis fájlok (foglalás, állapot) közös burka.
type signedJSON struct {
	Format string          `json:"format"`
	Body   []byte `json:"body"` // pontos bájtok (base64) – az aláírás ezekre vonatkozik
	FP     []byte          `json:"fp"`
	Sig    []byte          `json:"sig"`
}

func writeSigned(path, format string, me *Identity, body any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	msg := append([]byte(format+"\x00"), b...)
	out, _ := json.MarshalIndent(signedJSON{Format: format, Body: b, FP: me.Public().FingerprintBytes(), Sig: me.Sign(msg)}, "", "  ")
	return writeFileAtomic(path, out)
}

// readSigned – visszaadja a törzset és az aláírót (nil, ha ismeretlen vagy érvénytelen).
func readSigned(path, format string, known []*PublicKey, body any) (*PublicKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s signedJSON
	if json.Unmarshal(raw, &s) != nil || s.Format != format {
		return nil, ErrFormat
	}
	if err := json.Unmarshal(s.Body, body); err != nil {
		return nil, ErrFormat
	}
	msg := append([]byte(format+"\x00"), s.Body...)
	for _, p := range known {
		if bytes.Equal(p.FingerprintBytes(), s.FP) && ed25519.Verify(ed25519.PublicKey(p.Ed25519), msg, s.Sig) {
			return p, nil
		}
	}
	return nil, nil
}

// ---- Foglalás (.lock) ----

type Lock struct {
	Name       string    `json:"name"`
	Machine    string    `json:"machine"`
	Since      time.Time `json:"since"`
	Heartbeat  time.Time `json:"heartbeat"`
	Generation uint64    `json:"generation"`
	Offline    bool      `json:"offline"` // elvitték (letöltve máshol dolgoznak vele)
	OfflineTo  string    `json:"offline_to,omitempty"`

	Signer *PublicKey `json:"-"` // nil: ismeretlen vagy hamis aláírás
}

func (l *Lock) Mine(me *Identity) bool {
	return l.Signer != nil && bytes.Equal(l.Signer.FingerprintBytes(), me.Public().FingerprintBytes()) && l.Machine == Hostname()
}

func (l *Lock) Stale() bool { return !l.Offline && time.Since(l.Heartbeat) > LockStaleAfter }

// Who – ki foglalja, emberi nyelven.
func (l *Lock) Who() string {
	n := l.Name
	if l.Signer == nil {
		n = "ismeretlen (érvénytelen aláírás!)"
	}
	return n + " (" + l.Machine + ", " + l.Since.Local().Format("2006-01-02 15:04") + " óta)"
}

// ReadLock – nil, nil ha nincs foglalás.
func ReadLock(gal string, me *Identity, partners []*PublicKey) (*Lock, error) {
	var l Lock
	signer, err := readSigned(LockPath(gal), lockFormat, append([]*PublicKey{me.Public()}, partners...), &l)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return &Lock{Name: "ismeretlen", Machine: "?", Since: time.Now()}, nil // sérült lock: idegennek tekintjük
	}
	l.Signer = signer
	if signer != nil {
		l.Name = signer.Name
	}
	return &l, nil
}

// AcquireLock – foglalás létrehozása. Ha közben más is létrehozta, hibát ad.
func AcquireLock(gal string, me *Identity, gen uint64, offline bool, offlineTo string) error {
	now := time.Now().UTC().Truncate(time.Second)
	l := Lock{Name: me.Name, Machine: Hostname(), Since: now, Heartbeat: now, Generation: gen, Offline: offline, OfflineTo: offlineTo}
	f, err := os.OpenFile(LockPath(gal), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return errors.New("közben valaki más foglalta le a konténert")
	}
	f.Close()
	return writeSigned(LockPath(gal), lockFormat, me, l)
}

// Heartbeat – életjel; ha a foglalás már nem a miénk, hibát ad.
func Heartbeat(gal string, me *Identity, partners []*PublicKey, gen uint64) error {
	l, err := ReadLock(gal, me, partners)
	if err != nil {
		return err
	}
	if l == nil || !l.Mine(me) {
		return errors.New("a foglalásodat valaki feloldotta vagy átvette")
	}
	l.Heartbeat, l.Generation = time.Now().UTC().Truncate(time.Second), gen
	return writeSigned(LockPath(gal), lockFormat, me, l)
}

func ReleaseLock(gal string) error {
	err := os.Remove(LockPath(gal))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// ---- Állapot (.state): az utolsó mentés sorszáma a szinkronvédelemhez ----

type State struct {
	UUID       []byte    `json:"uuid"`
	Generation uint64    `json:"generation"`
	SavedBy    string    `json:"saved_by"`
	SavedAt    time.Time `json:"saved_at"`
}

func WriteState(gal string, me *Identity, c *Container) error {
	return writeSigned(StatePath(gal), stateFormat, me, State{UUID: c.H.UUID[:], Generation: c.H.Generation, SavedBy: me.Name, SavedAt: time.Now().UTC().Truncate(time.Second)})
}

// SyncBehind – ha a helyi .gal régebbi, mint az utolsó ismert mentés, leírja miért.
func SyncBehind(gal string, c *Container, me *Identity, partners []*PublicKey) string {
	var s State
	signer, err := readSigned(StatePath(gal), stateFormat, append([]*PublicKey{me.Public()}, partners...), &s)
	if err != nil || signer == nil || !bytes.Equal(s.UUID, c.H.UUID[:]) {
		return ""
	}
	if s.Generation > c.H.Generation {
		return "A OneDrive még nem töltötte le a legfrissebb változatot: " + signer.Name + " már " + Nth(s.Generation) +
			" mentésnél tart, nálad még csak " + Nth(c.H.Generation) + " van meg. Várj a szinkronra."
	}
	return ""
}

func itoa(n uint64) string { return string(appendUint(nil, n)) }

// Nth – névelővel és ponttal: „az 1.”, „a 2.”, „az 5.”, „a 10.”, „az 1000.”
func Nth(n uint64) string {
	s := itoa(n)
	art := "a"
	if s[0] == '5' || (s[0] == '1' && len(s)%3 == 1) {
		art = "az"
	}
	return art + " " + s + "."
}
func appendUint(b []byte, n uint64) []byte {
	if n >= 10 {
		b = appendUint(b, n/10)
	}
	return append(b, byte('0'+n%10))
}

// CopyFile – másolás folyamatjelzővel (elvitel/visszahozás).
func CopyFile(dst, src string, progress ProgressFunc) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	tmp := dst + ".masolas"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	buf := make([]byte, 4<<20)
	var done int64
	for {
		n, rerr := in.Read(buf)
		if n > 0 {
			if _, err := out.Write(buf[:n]); err != nil {
				out.Close()
				os.Remove(tmp)
				return err
			}
			done += int64(n)
			if progress != nil && !progress(done, st.Size()) {
				out.Close()
				os.Remove(tmp)
				return ErrCancelled
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			out.Close()
			os.Remove(tmp)
			return rerr
		}
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	out.Close()
	return os.Rename(tmp, dst)
}
