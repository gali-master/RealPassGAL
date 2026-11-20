package core

import (
	"crypto/sha256"
	"errors"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

// Hole – „lyuk” a fájlban: nincs lefoglalt blokk, nullákat olvasunk.
const Hole = ^uint32(0)

const (
	RootID       = 1
	SaveDelay    = 15 * time.Second // ennyivel az utolsó változás után mentünk
	cacheBlocks  = 256              // írási gyorsítótár: 256 × 64 KB = 16 MB
	MaxNameRunes = 255
)

// Node – fájl vagy mappa a konténerben. Az időpontok Unix nanoszekundumban.
type Node struct {
	ID     uint64   `json:"id"`
	Parent uint64   `json:"p"`
	Name   string   `json:"n"`
	Dir    bool     `json:"d,omitempty"`
	Size   int64    `json:"s,omitempty"`
	Mtime  int64    `json:"m"`
	Ctime  int64    `json:"c"`
	Btime  int64    `json:"b"`
	Blocks []uint32 `json:"k,omitempty"`
	SHA    []byte   `json:"h,omitempty"`
}

var (
	ErrNotFound = errors.New("nem található")
	ErrExists   = errors.New("már létezik")
	ErrNotEmpty = errors.New("a mappa nem üres")
	ErrNotDir   = errors.New("nem mappa")
	ErrIsDir    = errors.New("ez egy mappa")
	ErrNoSpace  = errors.New("megtelt a konténer")
	ErrReadOnly = errors.New("csak olvasásra van felcsatolva")
	ErrInvalid  = errors.New("érvénytelen művelet")
)

type cacheKey struct {
	id  uint64
	idx int
}

// FS – a konténerben tárolt fájlrendszer. Minden művelet egyetlen zár alatt fut.
type FS struct {
	mu   sync.Mutex
	c    *Container
	me   *Identity
	rng  io.Reader
	ro   bool
	root *Node

	nodes    map[uint64]*Node
	kids     map[uint64]map[string]uint64 // kisbetűs név → azonosító (Windows: kis/nagybetű-független)
	used     []bool                       // hivatkozott vagy mentésig fenntartott blokk
	fresh    map[uint32]bool              // utolsó mentés óta foglalt: helyben felülírható
	pending  []uint32                     // felszabadult, de a mentett állapot még hivatkozik rá
	cache    map[cacheKey][]byte
	open     map[uint64]int
	modified map[uint64]bool
	nextFree uint32
	nfree    int // szabad (nem hivatkozott, nem fenntartott) blokkok száma

	dirty      bool
	lastChange time.Time
	stop       chan struct{}
	wg         sync.WaitGroup
	OnError    func(error) // automatikus mentés hibája
}

func key(name string) string { return strings.ToLower(name) }

// NewFS – a konténer metaadatából felépíti a fát; írható módban elindítja az automatikus mentést.
func NewFS(c *Container, me *Identity, rng io.Reader, readOnly bool) *FS {
	f := &FS{
		c: c, me: me, rng: rng, ro: readOnly,
		nodes:    map[uint64]*Node{},
		kids:     map[uint64]map[string]uint64{RootID: {}},
		used:     make([]bool, c.H.Layout.Blocks),
		fresh:    map[uint32]bool{},
		cache:    map[cacheKey][]byte{},
		open:     map[uint64]int{},
		modified: map[uint64]bool{},
		stop:     make(chan struct{}),
		nfree:    int(c.H.Layout.Blocks),
	}
	t := c.H.Created.UnixNano()
	f.root = &Node{ID: RootID, Dir: true, Mtime: t, Ctime: t, Btime: t}
	f.nodes[RootID] = f.root
	if c.Meta.NextID <= RootID {
		c.Meta.NextID = RootID + 1
	}
	for _, n := range c.Meta.Nodes {
		f.nodes[n.ID] = n
		if n.Dir {
			if f.kids[n.ID] == nil {
				f.kids[n.ID] = map[string]uint64{}
			}
		}
		for _, b := range n.Blocks {
			if b != Hole && b < uint32(len(f.used)) && !f.used[b] {
				f.used[b] = true
				f.nfree--
			}
		}
	}
	for _, n := range c.Meta.Nodes {
		if f.kids[n.Parent] == nil {
			f.kids[n.Parent] = map[string]uint64{}
		}
		f.kids[n.Parent][key(n.Name)] = n.ID
	}
	if !readOnly {
		f.wg.Add(1)
		go f.autosave()
	}
	return f
}

func (f *FS) autosave() {
	defer f.wg.Done()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-f.stop:
			return
		case <-t.C:
			f.mu.Lock()
			if f.dirty && time.Since(f.lastChange) >= SaveDelay {
				if err := f.commitLocked(); err != nil {
					// hibánál nem próbálkozunk másodpercenként: újabb kísérlet SaveDelay múlva
					f.lastChange = time.Now()
					if f.OnError != nil {
						go f.OnError(err)
					}
				}
			}
			f.mu.Unlock()
		}
	}
}

// Close – utolsó mentés, majd a konténer lezárása.
func (f *FS) Close() error {
	if !f.ro {
		close(f.stop)
		f.wg.Wait()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var err error
	if f.dirty {
		err = f.commitLocked()
	}
	if cerr := f.c.Close(); err == nil {
		err = cerr
	}
	return err
}

// Save – azonnali mentés (pl. lecsatolás előtt).
func (f *FS) Save() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.dirty {
		return nil
	}
	return f.commitLocked()
}

func (f *FS) touch() {
	f.dirty = true
	f.lastChange = time.Now()
}

func (f *FS) commitLocked() error {
	for k := range f.modified {
		if f.open[k] == 0 {
			f.hashLocked(k)
		}
	}
	if err := f.flushCacheLocked(); err != nil {
		return err
	}
	nodes := make([]*Node, 0, len(f.nodes))
	for id, n := range f.nodes {
		if id != RootID {
			nodes = append(nodes, n)
		}
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	f.c.Meta.Nodes = nodes
	if err := f.c.Commit(f.me, f.rng); err != nil {
		return err
	}
	for _, b := range f.pending {
		f.used[b] = false
		f.nfree++
	}
	f.pending = nil
	f.fresh = map[uint32]bool{}
	f.dirty = false
	return nil
}

// ---- útvonalak ----

func splitPath(p string) []string {
	var out []string
	for _, s := range strings.Split(strings.ReplaceAll(p, "\\", "/"), "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func (f *FS) lookup(p string) (*Node, error) {
	cur := f.root
	for _, part := range splitPath(p) {
		if !cur.Dir {
			return nil, ErrNotDir
		}
		id, ok := f.kids[cur.ID][key(part)]
		if !ok {
			return nil, ErrNotFound
		}
		cur = f.nodes[id]
	}
	return cur, nil
}

func (f *FS) parentOf(p string) (*Node, string, error) {
	parts := splitPath(p)
	if len(parts) == 0 {
		return nil, "", ErrInvalid
	}
	name := parts[len(parts)-1]
	if len([]rune(name)) > MaxNameRunes {
		return nil, "", ErrInvalid
	}
	dir, err := f.lookup(strings.Join(parts[:len(parts)-1], "/"))
	if err != nil {
		return nil, "", err
	}
	if !dir.Dir {
		return nil, "", ErrNotDir
	}
	return dir, name, nil
}

// ---- lekérdezések ----

type Attr struct {
	Dir                 bool
	Size                int64
	Mtime, Ctime, Btime time.Time
	ID                  uint64
}

func attrOf(n *Node) Attr {
	return Attr{Dir: n.Dir, Size: n.Size, ID: n.ID,
		Mtime: time.Unix(0, n.Mtime), Ctime: time.Unix(0, n.Ctime), Btime: time.Unix(0, n.Btime)}
}

func (f *FS) Stat(p string) (Attr, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, err := f.lookup(p)
	if err != nil {
		return Attr{}, err
	}
	return attrOf(n), nil
}

func (f *FS) StatID(id uint64) (Attr, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := f.nodes[id]
	if n == nil {
		return Attr{}, ErrNotFound
	}
	return attrOf(n), nil
}

type DirEntry struct {
	Name string
	Attr Attr
}

func (f *FS) ReadDir(p string) ([]DirEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, err := f.lookup(p)
	if err != nil {
		return nil, err
	}
	if !d.Dir {
		return nil, ErrNotDir
	}
	var out []DirEntry
	for _, id := range f.kids[d.ID] {
		n := f.nodes[id]
		out = append(out, DirEntry{Name: n.Name, Attr: attrOf(n)})
	}
	sort.Slice(out, func(i, j int) bool { return key(out[i].Name) < key(out[j].Name) })
	return out, nil
}

// Space – összes és szabad blokk.
func (f *FS) Space() (total, free uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	total = uint64(len(f.used))
	if a := f.availLocked(); a > 0 {
		free = uint64(a)
	}
	return
}

func (f *FS) ReadOnly() bool { return f.ro }

// ---- módosítások ----

func (f *FS) create(p string, dir bool) (*Node, error) {
	if f.ro {
		return nil, ErrReadOnly
	}
	parent, name, err := f.parentOf(p)
	if err != nil {
		return nil, err
	}
	if _, ok := f.kids[parent.ID][key(name)]; ok {
		return nil, ErrExists
	}
	now := time.Now().UnixNano()
	n := &Node{ID: f.c.Meta.NextID, Parent: parent.ID, Name: name, Dir: dir, Mtime: now, Ctime: now, Btime: now}
	f.c.Meta.NextID++
	f.nodes[n.ID] = n
	f.kids[parent.ID][key(name)] = n.ID
	if dir {
		f.kids[n.ID] = map[string]uint64{}
	}
	parent.Mtime = now
	f.touch()
	return n, nil
}

func (f *FS) Mkdir(p string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, err := f.create(p, true)
	return err
}

// Create – új üres fájl, visszaadja az azonosítót (megnyitva).
func (f *FS) Create(p string) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, err := f.create(p, false)
	if err != nil {
		return 0, err
	}
	f.open[n.ID]++
	return n.ID, nil
}

// Open – meglévő fájl megnyitása.
func (f *FS) Open(p string) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, err := f.lookup(p)
	if err != nil {
		return 0, err
	}
	if n.Dir {
		return 0, ErrIsDir
	}
	f.open[n.ID]++
	return n.ID, nil
}

// Release – fájl bezárása; ha módosult és senki nem tartja nyitva, kiszámoljuk a SHA-256-ot.
func (f *FS) Release(id uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.open[id] > 0 {
		f.open[id]--
	}
	if f.open[id] == 0 {
		delete(f.open, id)
		if f.modified[id] {
			f.hashLocked(id)
		}
	}
}

func (f *FS) hashLocked(id uint64) {
	delete(f.modified, id)
	n := f.nodes[id]
	if n == nil || n.Dir {
		return
	}
	h := sha256.New()
	remain := n.Size
	for idx := 0; remain > 0; idx++ {
		b, err := f.getBlockLocked(n, idx)
		if err != nil {
			n.SHA = nil
			return
		}
		k := int64(BlockSize)
		if remain < k {
			k = remain
		}
		h.Write(b[:k])
		remain -= k
	}
	n.SHA = h.Sum(nil)
	f.touch()
}

func (f *FS) freeBlock(b uint32) {
	if b == Hole {
		return
	}
	if f.fresh[b] {
		delete(f.fresh, b)
		f.used[b] = false
		f.nfree++
	} else {
		f.pending = append(f.pending, b)
	}
}

func (f *FS) dropNodeLocked(n *Node) {
	for i, b := range n.Blocks {
		delete(f.cache, cacheKey{n.ID, i})
		f.freeBlock(b)
	}
	delete(f.nodes, n.ID)
	delete(f.kids[n.Parent], key(n.Name))
	delete(f.modified, n.ID)
	if n.Dir {
		delete(f.kids, n.ID)
	}
	if p := f.nodes[n.Parent]; p != nil {
		p.Mtime = time.Now().UnixNano()
	}
}

func (f *FS) Unlink(p string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ro {
		return ErrReadOnly
	}
	n, err := f.lookup(p)
	if err != nil {
		return err
	}
	if n.Dir {
		return ErrIsDir
	}
	f.dropNodeLocked(n)
	f.touch()
	return nil
}

func (f *FS) Rmdir(p string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ro {
		return ErrReadOnly
	}
	n, err := f.lookup(p)
	if err != nil {
		return err
	}
	if !n.Dir {
		return ErrNotDir
	}
	if n.ID == RootID {
		return ErrInvalid
	}
	if len(f.kids[n.ID]) > 0 {
		return ErrNotEmpty
	}
	f.dropNodeLocked(n)
	f.touch()
	return nil
}

// Rename – átnevezés/áthelyezés; létező célfájlt felülír, nem üres célmappát nem.
func (f *FS) Rename(oldp, newp string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ro {
		return ErrReadOnly
	}
	n, err := f.lookup(oldp)
	if err != nil {
		return err
	}
	if n.ID == RootID {
		return ErrInvalid
	}
	np, name, err := f.parentOf(newp)
	if err != nil {
		return err
	}
	for a := np; a != nil; a = f.nodes[a.Parent] { // mappát önmagába nem mozgathatunk
		if a.ID == n.ID {
			return ErrInvalid
		}
		if a.ID == RootID {
			break
		}
	}
	if tid, ok := f.kids[np.ID][key(name)]; ok && tid != n.ID {
		t := f.nodes[tid]
		if t.Dir && len(f.kids[t.ID]) > 0 {
			return ErrNotEmpty
		}
		if t.Dir != n.Dir {
			if t.Dir {
				return ErrIsDir
			}
			return ErrNotDir
		}
		f.dropNodeLocked(t)
	}
	now := time.Now().UnixNano()
	delete(f.kids[n.Parent], key(n.Name))
	if op := f.nodes[n.Parent]; op != nil {
		op.Mtime = now
	}
	n.Parent, n.Name, n.Ctime = np.ID, name, now
	f.kids[np.ID][key(name)] = n.ID
	np.Mtime = now
	f.touch()
	return nil
}

func (f *FS) SetTimes(p string, mtime time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ro {
		return ErrReadOnly
	}
	n, err := f.lookup(p)
	if err != nil {
		return err
	}
	n.Mtime = mtime.UnixNano()
	f.touch()
	return nil
}

// ---- adat ----

// getBlockLocked – egy blokk tartalma (gyorsítótár → konténer → lyuk).
func (f *FS) getBlockLocked(n *Node, idx int) ([]byte, error) {
	if b, ok := f.cache[cacheKey{n.ID, idx}]; ok {
		return b, nil
	}
	if idx >= len(n.Blocks) || n.Blocks[idx] == Hole {
		return make([]byte, BlockSize), nil
	}
	return f.c.ReadBlock(n.Blocks[idx])
}

func (f *FS) alloc() (uint32, error) {
	nb := uint32(len(f.used))
	for i := uint32(0); i < nb; i++ {
		b := (f.nextFree + i) % nb
		if !f.used[b] {
			f.used[b] = true
			f.fresh[b] = true
			f.nfree--
			f.nextFree = b + 1
			return b, nil
		}
	}
	return 0, ErrNoSpace
}

func (f *FS) flushBlockLocked(k cacheKey, data []byte) error {
	n := f.nodes[k.id]
	if n == nil || k.idx >= len(n.Blocks) {
		delete(f.cache, k)
		return nil
	}
	b := n.Blocks[k.idx]
	if b == Hole || !f.fresh[b] { // copy-on-write: a mentett állapot blokkját nem írjuk felül
		nb, err := f.alloc()
		if err != nil {
			return err
		}
		f.freeBlock(b)
		b = nb
		n.Blocks[k.idx] = b
	}
	if err := f.c.WriteBlock(b, data, f.rng); err != nil {
		return err
	}
	delete(f.cache, k)
	return nil
}

func (f *FS) flushCacheLocked() error {
	keys := make([]cacheKey, 0, len(f.cache))
	for k := range f.cache {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].id != keys[j].id {
			return keys[i].id < keys[j].id
		}
		return keys[i].idx < keys[j].idx
	})
	for _, k := range keys {
		if err := f.flushBlockLocked(k, f.cache[k]); err != nil {
			return err
		}
	}
	return nil
}

func (f *FS) ReadAt(id uint64, buf []byte, off int64) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := f.nodes[id]
	if n == nil {
		return 0, ErrNotFound
	}
	if off < 0 {
		return 0, ErrInvalid
	}
	if off >= n.Size {
		return 0, nil
	}
	if off+int64(len(buf)) > n.Size {
		buf = buf[:n.Size-off]
	}
	done := 0
	for done < len(buf) {
		pos := off + int64(done)
		idx, in := int(pos/BlockSize), int(pos%BlockSize)
		b, err := f.getBlockLocked(n, idx)
		if err != nil {
			return done, err
		}
		done += copy(buf[done:], b[in:])
	}
	return done, nil
}

// needsAlloc – a blokk kiírásához új blokk kell-e (lyuk, vagy a mentett állapot része: copy-on-write).
func (f *FS) needsAlloc(n *Node, idx int) bool {
	return idx >= len(n.Blocks) || n.Blocks[idx] == Hole || !f.fresh[n.Blocks[idx]]
}

// availLocked – ténylegesen felhasználható szabad blokkok: a gyorsítótárban várakozó,
// új helyet igénylő blokkok már előre le vannak foglalva, így a mentés soha nem futhat ki a helyből.
func (f *FS) availLocked() int {
	reserved := 0
	for k := range f.cache {
		if n := f.nodes[k.id]; n != nil && f.needsAlloc(n, k.idx) {
			reserved++
		}
	}
	return f.nfree - reserved
}

// maxFileSize – a konténer teljes kapacitása; ennél nagyobb fájl biztosan nem fér el.
func (f *FS) maxFileSize() int64 { return int64(len(f.used)) * BlockSize }

func (f *FS) WriteAt(id uint64, data []byte, off int64) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ro {
		return 0, ErrReadOnly
	}
	n := f.nodes[id]
	if n == nil {
		return 0, ErrNotFound
	}
	if n.Dir {
		return 0, ErrIsDir
	}
	if off < 0 {
		return 0, ErrInvalid
	}
	if len(data) == 0 {
		return 0, nil
	}
	end := off + int64(len(data))
	if end < off || end > f.maxFileSize() {
		return 0, ErrNoSpace
	}
	// előzetes helyfoglalás: minden blokk, amely most kerül a gyorsítótárba és új helyet igényel
	// (bővítés, lyuk kitöltése vagy mentett blokk felülírása), előre le van foglalva.
	need := 0
	for idx := int(off / BlockSize); idx <= int((end-1)/BlockSize); idx++ {
		if _, ok := f.cache[cacheKey{n.ID, idx}]; !ok && f.needsAlloc(n, idx) {
			need++
		}
	}
	if need > f.availLocked() {
		return 0, ErrNoSpace
	}
	for len(n.Blocks) < int((end+BlockSize-1)/BlockSize) {
		n.Blocks = append(n.Blocks, Hole)
	}
	done := 0
	for done < len(data) {
		pos := off + int64(done)
		idx, in := int(pos/BlockSize), int(pos%BlockSize)
		k := cacheKey{n.ID, idx}
		b, ok := f.cache[k]
		if !ok {
			if in == 0 && len(data)-done >= BlockSize {
				b = make([]byte, BlockSize) // teljes blokk: nem kell beolvasni
			} else {
				old, err := f.getBlockLocked(n, idx)
				if err != nil {
					return done, err
				}
				b = append([]byte(nil), old...)
			}
			f.cache[k] = b
		}
		done += copy(b[in:], data[done:])
	}
	if end > n.Size {
		n.Size = end
	}
	n.Mtime = time.Now().UnixNano()
	n.SHA = nil
	f.modified[n.ID] = true
	f.touch()
	if len(f.cache) > cacheBlocks {
		if err := f.flushCacheLocked(); err != nil {
			return done, err
		}
	}
	return done, nil
}

func (f *FS) Truncate(id uint64, size int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ro {
		return ErrReadOnly
	}
	n := f.nodes[id]
	if n == nil {
		return ErrNotFound
	}
	if n.Dir {
		return ErrIsDir
	}
	if size < 0 {
		return ErrInvalid
	}
	if size > f.maxFileSize() {
		return ErrNoSpace
	}
	nblocks := int((size + BlockSize - 1) / BlockSize)
	if grow := nblocks - len(n.Blocks); grow > 0 && grow > f.availLocked() {
		return ErrNoSpace // előfoglalás (pl. Intéző-másolás): csak akkor, ha a fájl tényleg elférne
	}
	if nblocks < len(n.Blocks) {
		for i := nblocks; i < len(n.Blocks); i++ {
			delete(f.cache, cacheKey{n.ID, i})
			f.freeBlock(n.Blocks[i])
		}
		n.Blocks = n.Blocks[:nblocks]
	}
	if size < n.Size && size%BlockSize != 0 { // a megmaradó utolsó blokk végét kinullázzuk
		idx := nblocks - 1
		k := cacheKey{n.ID, idx}
		b, ok := f.cache[k]
		if !ok {
			if f.needsAlloc(n, idx) && f.availLocked() < 1 {
				return ErrNoSpace
			}
			old, err := f.getBlockLocked(n, idx)
			if err != nil {
				return err
			}
			b = append([]byte(nil), old...)
			f.cache[k] = b
		}
		for i := int(size % BlockSize); i < BlockSize; i++ {
			b[i] = 0
		}
	}
	for len(n.Blocks) < nblocks {
		n.Blocks = append(n.Blocks, Hole)
	}
	n.Size = size
	n.Mtime = time.Now().UnixNano()
	n.SHA = nil
	f.modified[n.ID] = true
	f.touch()
	return nil
}

// PathOf – egy csomópont teljes útvonala (hibaüzenetekhez).
func (f *FS) PathOf(id uint64) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var parts []string
	for n := f.nodes[id]; n != nil && n.ID != RootID; n = f.nodes[n.Parent] {
		parts = append([]string{n.Name}, parts...)
	}
	return "/" + strings.Join(parts, "/")
}
