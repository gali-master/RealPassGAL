//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/winfsp/cgofuse/fuse"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"realpassgal/core"
)

// galFuse – a core.FS átfordítása a WinFsp (FUSE) hívásaira.
type galFuse struct {
	fuse.FileSystemBase
	fs     *core.FS
	inited chan struct{}
}

func errno(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, core.ErrNotFound):
		return -fuse.ENOENT
	case errors.Is(err, core.ErrExists):
		return -fuse.EEXIST
	case errors.Is(err, core.ErrNotEmpty):
		return -fuse.ENOTEMPTY
	case errors.Is(err, core.ErrNotDir):
		return -fuse.ENOTDIR
	case errors.Is(err, core.ErrIsDir):
		return -fuse.EISDIR
	case errors.Is(err, core.ErrNoSpace):
		return -fuse.ENOSPC
	case errors.Is(err, core.ErrReadOnly):
		return -fuse.EROFS
	case errors.Is(err, core.ErrInvalid):
		return -fuse.EINVAL
	default:
		return -fuse.EIO
	}
}

func fillStat(st *fuse.Stat_t, a core.Attr) {
	*st = fuse.Stat_t{}
	if a.Dir {
		st.Mode = fuse.S_IFDIR | 0777
	} else {
		st.Mode = fuse.S_IFREG | 0666
		st.Size = a.Size
	}
	st.Nlink = 1
	st.Mtim = fuse.NewTimespec(a.Mtime)
	st.Atim = st.Mtim
	st.Ctim = fuse.NewTimespec(a.Ctime)
	st.Birthtim = fuse.NewTimespec(a.Btime)
	st.Blksize = core.BlockSize
	st.Blocks = (a.Size + 511) / 512
}

func (g *galFuse) Init() { close(g.inited) }

func (g *galFuse) Statfs(path string, st *fuse.Statfs_t) int {
	total, free := g.fs.Space()
	*st = fuse.Statfs_t{Bsize: core.BlockSize, Frsize: core.BlockSize, Blocks: total, Bfree: free, Bavail: free, Namemax: core.MaxNameRunes}
	return 0
}

func (g *galFuse) Getattr(path string, st *fuse.Stat_t, fh uint64) int {
	a, err := g.fs.Stat(path)
	if err != nil && fh != ^uint64(0) {
		a, err = g.fs.StatID(fh)
	}
	if err != nil {
		return errno(err)
	}
	fillStat(st, a)
	return 0
}

func (g *galFuse) Mkdir(path string, mode uint32) int { return errno(g.fs.Mkdir(path)) }
func (g *galFuse) Unlink(path string) int             { return errno(g.fs.Unlink(path)) }
func (g *galFuse) Rmdir(path string) int              { return errno(g.fs.Rmdir(path)) }
func (g *galFuse) Rename(o, n string) int             { return errno(g.fs.Rename(o, n)) }
func (g *galFuse) Chmod(path string, mode uint32) int { return 0 }
func (g *galFuse) Chown(path string, u, gid uint32) int {
	return 0
}
func (g *galFuse) Access(path string, mask uint32) int { return 0 }

func (g *galFuse) Utimens(path string, tmsp []fuse.Timespec) int {
	if g.fs.ReadOnly() {
		return -fuse.EROFS
	}
	t := time.Now()
	if len(tmsp) >= 2 {
		t = tmsp[1].Time()
	}
	return errno(g.fs.SetTimes(path, t))
}

func (g *galFuse) Create(path string, flags int, mode uint32) (int, uint64) {
	id, err := g.fs.Create(path)
	if err != nil {
		return errno(err), ^uint64(0)
	}
	return 0, id
}

func (g *galFuse) Open(path string, flags int) (int, uint64) {
	id, err := g.fs.Open(path)
	if err != nil {
		return errno(err), ^uint64(0)
	}
	return 0, id
}

func (g *galFuse) Truncate(path string, size int64, fh uint64) int {
	if fh == ^uint64(0) {
		a, err := g.fs.Stat(path)
		if err != nil {
			return errno(err)
		}
		fh = a.ID
	}
	return errno(g.fs.Truncate(fh, size))
}

func (g *galFuse) Read(path string, buf []byte, ofst int64, fh uint64) int {
	n, err := g.fs.ReadAt(fh, buf, ofst)
	if err != nil && n == 0 {
		return errno(err)
	}
	return n
}

func (g *galFuse) Write(path string, buf []byte, ofst int64, fh uint64) int {
	n, err := g.fs.WriteAt(fh, buf, ofst)
	if err != nil && n == 0 {
		return errno(err)
	}
	return n
}

func (g *galFuse) Flush(path string, fh uint64) int                  { return 0 }
func (g *galFuse) Fsync(path string, datasync bool, fh uint64) int   { return 0 }
func (g *galFuse) Release(path string, fh uint64) int                { g.fs.Release(fh); return 0 }
func (g *galFuse) Opendir(path string) (int, uint64)                 { return 0, 0 }
func (g *galFuse) Releasedir(path string, fh uint64) int             { return 0 }
func (g *galFuse) Fsyncdir(path string, datasync bool, fh uint64) int { return 0 }

func (g *galFuse) Readdir(path string, fill func(name string, stat *fuse.Stat_t, ofst int64) bool, ofst int64, fh uint64) int {
	ents, err := g.fs.ReadDir(path)
	if err != nil {
		return errno(err)
	}
	var st fuse.Stat_t
	if a, err := g.fs.Stat(path); err == nil {
		fillStat(&st, a)
	}
	fill(".", &st, 0)
	fill("..", nil, 0)
	for _, e := range ents {
		var s fuse.Stat_t
		fillStat(&s, e.Attr)
		if !fill(e.Name, &s, 0) {
			break
		}
	}
	return 0
}

// ---- felcsatolás kezelése ----

type mount struct {
	fs     *core.FS
	host   *fuse.FileSystemHost
	letter string
	path   string
	done   chan bool
	hbStop chan struct{}
}

// winfspInstalled – a WinFsp telepítve van-e (a cgofuse is innen tölti be).
func winfspInstalled() bool {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\WinFsp`, registry.QUERY_VALUE|registry.WOW64_32KEY)
	if err != nil {
		return false
	}
	defer k.Close()
	dir, _, err := k.GetStringValue("InstallDir")
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(dir, "bin", "winfsp-x64.dll"))
	return err == nil
}

// freeLetters – szabad meghajtóbetűk D-től Z-ig.
func freeLetters() []string {
	mask, _ := windows.GetLogicalDrives()
	var out []string
	for i := 3; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			out = append(out, string(rune('A'+i)))
		}
	}
	return out
}

func startMount(fs *core.FS, path, letter string) (*mount, error) {
	g := &galFuse{fs: fs, inited: make(chan struct{})}
	host := fuse.NewFileSystemHost(g)
	host.SetCapCaseInsensitive(true)
	host.SetCapReaddirPlus(true)
	m := &mount{fs: fs, host: host, letter: letter, path: path, done: make(chan bool, 1)}
	vol := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if len([]rune(vol)) > 30 {
		vol = string([]rune(vol)[:30])
	}
	opts := []string{"-o", "uid=-1,gid=-1", "-o", "volname=" + vol}
	if fs.ReadOnly() {
		opts = append(opts, "-o", "ro")
	}
	go func() {
		ok := false
		defer func() {
			if r := recover(); r != nil {
				ok = false
			}
			m.done <- ok
		}()
		ok = host.Mount(letter+":", opts)
	}()
	select {
	case <-g.inited:
		return m, nil
	case <-m.done:
		return nil, fmt.Errorf("a(z) %s: meghajtó nem csatolható fel (foglalt a betű, vagy a WinFsp hibát jelzett)", letter)
	case <-time.After(15 * time.Second):
		host.Unmount()
		return nil, errors.New("a felcsatolás nem fejeződött be 15 másodperc alatt")
	}
}

// stop – lecsatolás, majd utolsó mentés és lezárás.
func (m *mount) stop() error {
	m.host.Unmount()
	select {
	case <-m.done:
	case <-time.After(20 * time.Second):
	}
	return m.fs.Close()
}
