// Package index keeps a snapshot of where the space on a filesystem is, so
// mapping it does not mean walking it again.
//
// The index is for looking, never for deleting. Everything the cleaner removes
// is measured live at the moment it runs, because a snapshot is by definition
// out of date and "it was a cache an hour ago" is not a reason to delete
// anything now. What the index buys is the other half of the tool: finding
// where the space went, on a disk where a full walk takes minutes.
//
// A refresh re-reads a directory only when its own modification time has
// changed -- which it does whenever an entry is added, removed or renamed. What
// that misses is a file growing in place: a log appended to, a database
// written into. The snapshot records its age for that reason, and a full
// rebuild is always one flag away.
package index

import (
	"encoding/gob"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// version changes whenever the stored layout does. An index of another version
// is rebuilt rather than misread.
const version = 1

// Dir is one directory in the snapshot.
type Dir struct {
	Name   string
	Parent int32
	// Mtime is the directory's own modification time, the refresh key.
	Mtime int64
	// Own and Files cover the files directly inside, not below.
	Own   int64
	Files int32
	// Newest is the latest modification time of any file directly inside.
	Newest int64
	Kids   []int32
}

// Tree is the snapshot of one root.
type Tree struct {
	Root  string
	Built time.Time
	Dirs  []Dir
	// Unreadable counts directories that could not be listed. Their bytes are
	// missing from every total above them.
	Unreadable int

	total  []int64
	newest []int64
}

// Index is every tree, as stored.
type Index struct {
	Version int
	Trees   []*Tree
}

// Options controls a build.
type Options struct {
	// Workers bounds the goroutines walking at once.
	Workers int
	// Prev, when set, is reused for every directory whose mtime is unchanged.
	Prev *Tree
	// Skip reports whether a directory must not be entered: other
	// filesystems, pseudo filesystems.
	Skip func(path string, d fs.FileInfo) bool
}

// Build walks root and returns its snapshot.
func Build(root string, o Options) (*Tree, error) {
	fi, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}
	if o.Workers <= 0 {
		o.Workers = runtime.NumCPU() * 2
	}
	b := &builder{
		t:    &Tree{Root: root, Built: time.Now()},
		sem:  make(chan struct{}, o.Workers),
		skip: o.Skip,
	}
	if o.Prev != nil && o.Prev.Root == root {
		b.prev = o.Prev
		b.prevByPath = o.Prev.paths()
	}
	b.t.Dirs = append(b.t.Dirs, Dir{Name: root, Parent: -1})
	b.wg.Add(1)
	b.visit(0, root, fi)
	b.wg.Wait()
	b.t.derive()
	return b.t, nil
}

type builder struct {
	t          *Tree
	mu         sync.Mutex
	wg         sync.WaitGroup
	sem        chan struct{}
	skip       func(string, fs.FileInfo) bool
	prev       *Tree
	prevByPath map[string]int32
}

// visit fills in directory idx and schedules its children.
func (b *builder) visit(idx int32, path string, fi fs.FileInfo) {
	defer b.wg.Done()
	mtime := fi.ModTime().UnixNano()

	// Unchanged since the last snapshot: its own entries are the same, so its
	// file totals and its list of subdirectories can be reused without
	// listing it. The subdirectories are still visited -- each has its own
	// mtime to check.
	if b.prev != nil {
		if pi, ok := b.prevByPath[path]; ok && b.prev.Dirs[pi].Mtime == mtime {
			p := b.prev.Dirs[pi]
			b.mu.Lock()
			d := &b.t.Dirs[idx]
			d.Mtime, d.Own, d.Files, d.Newest = mtime, p.Own, p.Files, p.Newest
			b.mu.Unlock()
			for _, k := range p.Kids {
				child := filepath.Join(path, b.prev.Dirs[k].Name)
				cfi, err := os.Lstat(child)
				if err != nil || !cfi.IsDir() {
					continue
				}
				b.child(idx, child, cfi)
			}
			return
		}
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		b.mu.Lock()
		b.t.Dirs[idx].Mtime = mtime
		b.t.Unreadable++
		b.mu.Unlock()
		return
	}
	var own, newest int64
	var files int32
	var subdirs []string
	var subinfo []fs.FileInfo
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		if e.IsDir() {
			subdirs = append(subdirs, filepath.Join(path, e.Name()))
			subinfo = append(subinfo, info)
			continue
		}
		own += info.Size()
		files++
		if m := info.ModTime().Unix(); m > newest {
			newest = m
		}
	}
	b.mu.Lock()
	d := &b.t.Dirs[idx]
	d.Mtime, d.Own, d.Files, d.Newest = mtime, own, files, newest
	b.mu.Unlock()
	for i, s := range subdirs {
		b.child(idx, s, subinfo[i])
	}
}

// child registers a subdirectory and visits it -- on a new goroutine when a
// worker slot is free, inline otherwise. Inline keeps the number of goroutines
// bounded by Workers rather than by the number of directories on the disk.
func (b *builder) child(parent int32, path string, fi fs.FileInfo) {
	if b.skip != nil && b.skip(path, fi) {
		return
	}
	b.mu.Lock()
	idx := int32(len(b.t.Dirs))
	b.t.Dirs = append(b.t.Dirs, Dir{Name: filepath.Base(path), Parent: parent})
	b.t.Dirs[parent].Kids = append(b.t.Dirs[parent].Kids, idx)
	b.mu.Unlock()
	b.wg.Add(1)
	select {
	case b.sem <- struct{}{}:
		go func() {
			defer func() { <-b.sem }()
			b.visit(idx, path, fi)
		}()
	default:
		b.visit(idx, path, fi)
	}
}

// derive computes subtree totals. Children are always appended after their
// parent, so one backward pass sees every child before its parent.
func (t *Tree) derive() {
	t.total = make([]int64, len(t.Dirs))
	t.newest = make([]int64, len(t.Dirs))
	for i := len(t.Dirs) - 1; i >= 0; i-- {
		d := t.Dirs[i]
		t.total[i] += d.Own
		if d.Newest > t.newest[i] {
			t.newest[i] = d.Newest
		}
		if d.Parent >= 0 {
			t.total[d.Parent] += t.total[i]
			if t.newest[i] > t.newest[d.Parent] {
				t.newest[d.Parent] = t.newest[i]
			}
		}
	}
}

// Path is the full path of directory i.
func (t *Tree) Path(i int32) string {
	var parts []string
	for ; i > 0; i = t.Dirs[i].Parent {
		parts = append(parts, t.Dirs[i].Name)
	}
	for l, r := 0, len(parts)-1; l < r; l, r = l+1, r-1 {
		parts[l], parts[r] = parts[r], parts[l]
	}
	return filepath.Join(append([]string{t.Root}, parts...)...)
}

func (t *Tree) paths() map[string]int32 {
	out := make(map[string]int32, len(t.Dirs))
	full := make([]string, len(t.Dirs))
	for i := range t.Dirs {
		if i == 0 {
			full[0] = t.Root
		} else {
			full[i] = filepath.Join(full[t.Dirs[i].Parent], t.Dirs[i].Name)
		}
		out[full[i]] = int32(i)
	}
	return out
}

// Total is the bytes under directory i.
func (t *Tree) Total(i int32) int64 { return t.total[i] }

// Entry is a directory in a query result.
type Entry struct {
	Path   string
	Bytes  int64
	Newest time.Time
}

// Children lists the directories directly inside path, largest first.
func (t *Tree) Children(path string) ([]Entry, bool) {
	i, ok := t.find(path)
	if !ok {
		return nil, false
	}
	var out []Entry
	for _, k := range t.Dirs[i].Kids {
		out = append(out, t.entry(k))
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Bytes > out[b].Bytes })
	return out, true
}

// Cold returns the largest directories nothing inside has changed in since
// before, at least min bytes each. A directory is reported instead of its
// children when it qualifies as a whole, so the list names the top of each
// cold subtree once rather than every level of it.
func (t *Tree) Cold(before time.Time, min int64) []Entry {
	cut := before.Unix()
	var out []Entry
	var walk func(i int32)
	walk = func(i int32) {
		if t.total[i] < min {
			return
		}
		if i != 0 && t.newest[i] > 0 && t.newest[i] < cut {
			out = append(out, t.entry(i))
			return
		}
		for _, k := range t.Dirs[i].Kids {
			walk(k)
		}
	}
	walk(0)
	sort.Slice(out, func(a, b int) bool { return out[a].Bytes > out[b].Bytes })
	return out
}

func (t *Tree) entry(i int32) Entry {
	return Entry{Path: t.Path(i), Bytes: t.total[i], Newest: time.Unix(t.newest[i], 0)}
}

// find locates a directory by path, descending from the root by name.
func (t *Tree) find(path string) (int32, bool) {
	path = filepath.Clean(path)
	if path == t.Root {
		return 0, true
	}
	rel, err := filepath.Rel(t.Root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return 0, false
	}
	i := int32(0)
	for _, name := range strings.Split(rel, string(filepath.Separator)) {
		found := false
		for _, k := range t.Dirs[i].Kids {
			if t.Dirs[k].Name == name {
				i, found = k, true
				break
			}
		}
		if !found {
			return 0, false
		}
	}
	return i, true
}

// Tree returns the snapshot whose root holds path, the deepest such.
func (x *Index) Tree(path string) *Tree {
	var best *Tree
	for _, t := range x.Trees {
		if (path == t.Root || strings.HasPrefix(path, strings.TrimSuffix(t.Root, "/")+"/")) &&
			(best == nil || len(t.Root) > len(best.Root)) {
			best = t
		}
	}
	return best
}

// Put replaces the snapshot for a root.
func (x *Index) Put(t *Tree) {
	for i, old := range x.Trees {
		if old.Root == t.Root {
			x.Trees[i] = t
			return
		}
	}
	x.Trees = append(x.Trees, t)
	sort.Slice(x.Trees, func(a, b int) bool { return x.Trees[a].Root < x.Trees[b].Root })
}

// ErrNone means there is no usable index yet.
var ErrNone = errors.New("no index")

// Load reads an index. A missing file, or one written by another version, is
// ErrNone: both mean build one.
func Load(path string) (*Index, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, ErrNone
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var x Index
	if err := gob.NewDecoder(f).Decode(&x); err != nil || x.Version != version {
		return nil, ErrNone
	}
	for _, t := range x.Trees {
		t.derive()
	}
	return &x, nil
}

// Save writes an index atomically: a reader never sees half of one.
func (x *Index) Save(path string) error {
	x.Version = version
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".index-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := gob.NewEncoder(tmp).Encode(x); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// DefaultPath is where the index lives. It is a cache in the XDG sense:
// deleting it loses nothing but time.
func DefaultPath(home string) string {
	return filepath.Join(home, ".cache", "reclaim", "index.gob")
}
