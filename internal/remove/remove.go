// Package remove deletes a directory tree without ever leaving it.
//
// os.RemoveAll resolves paths from the top on every call, descends into
// mount points, and gives up at the first file in a read-only directory. This
// walks the tree through os.Root handles: the target is opened relative to a
// handle on its parent, and every directory is opened relative to a handle on
// the tree. After each open, what was opened is compared with what was
// checked, so a directory swapped for a symlink between the two -- the
// target itself or anything inside it -- is refused rather than followed.
//
// It removes children before parents, one entry at a time, so anything it
// cannot or must not remove leaves its ancestors standing and is reported,
// rather than aborting the rest. It never crosses into another filesystem:
// neither a different device nor a bind mount of the same one.
package remove

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mralaminahamed/reclaim/internal/fsutil"
)

// Skip is something deliberately left in place, with why.
type Skip struct {
	Path, Reason string
}

// Outcome of removing one target. Gone reports the target no longer exists.
// Err is set only for a real failure; a skip is not one.
type Outcome struct {
	Gone    bool
	Skipped []Skip
	Err     error
}

const changed = "changed during removal"

// Test hooks. devOf fakes a device boundary, mountPoints the mount table;
// the others open a window at each point where the tree could change under
// the walk.
var (
	devOf        = deviceOf
	mountPoints  = fsutil.MountPoints
	afterLstat   = func() {}
	beforeOpen   = func(root *os.Root, name string) {}
	beforeRmdir  = func(root *os.Root, name string) {}
	beforeUnlink = func(root *os.Root, name string) {}
)

// Tree removes target and everything under it.
func Tree(target string) Outcome {
	base := filepath.Base(target)
	parent, err := os.OpenRoot(filepath.Dir(target))
	if os.IsNotExist(err) {
		return Outcome{Gone: true}
	}
	if err != nil {
		return Outcome{Err: err}
	}
	defer parent.Close()

	fi, err := parent.Lstat(base)
	if os.IsNotExist(err) {
		return Outcome{Gone: true}
	}
	if err != nil {
		return Outcome{Err: err}
	}
	// A link or a file is one unlink. A link is never followed: what it
	// points at is not part of this tree.
	if !fi.IsDir() {
		if err := parent.Remove(base); err != nil && !os.IsNotExist(err) {
			return Outcome{Err: err}
		}
		return Outcome{Gone: true}
	}

	afterLstat()
	root, err := parent.OpenRoot(base)
	if os.IsNotExist(err) {
		return Outcome{Gone: true}
	}
	if err != nil {
		// A symlink swapped in that points out of the parent is refused by
		// the Root itself; that is the race, not a failure.
		return Outcome{Skipped: []Skip{{target, changed}}}
	}
	rfi, err := root.Stat(".")
	if err != nil || !os.SameFile(fi, rfi) {
		root.Close()
		return Outcome{Skipped: []Skip{{target, changed}}}
	}
	w := &walker{target: target, dev: devOf(fi), uid: uint32(os.Geteuid()),
		mounts: mountsUnder(target)}
	emptied := w.empty(root, ".")
	root.Close()

	out := Outcome{Skipped: w.skipped, Err: w.err}
	if !emptied {
		return out
	}
	if err := parent.Remove(base); err != nil && !os.IsNotExist(err) {
		if notEmpty(err) {
			out.Skipped = append(out.Skipped, Skip{target, changed})
		} else if out.Err == nil {
			out.Err = err
		}
		return out
	}
	out.Gone = true
	return out
}

// mountsUnder returns the mount points strictly inside target, relative to
// it. The mount table names real paths, so target is resolved first.
func mountsUnder(target string) map[string]bool {
	canon, err := filepath.EvalSymlinks(target)
	if err != nil {
		canon = target
	}
	prefix := canon + string(filepath.Separator)
	out := map[string]bool{}
	for _, m := range mountPoints() {
		if strings.HasPrefix(m, prefix) {
			out[strings.TrimPrefix(m, prefix)] = true
		}
	}
	return out
}

type walker struct {
	target  string
	dev     uint64
	uid     uint32
	mounts  map[string]bool
	skipped []Skip
	err     error
}

func (w *walker) fail(rel string, err error) {
	if w.err == nil {
		w.err = fmt.Errorf("%s: %w", filepath.Join(w.target, rel), err)
	}
}

func (w *walker) skip(rel, reason string) {
	w.skipped = append(w.skipped, Skip{filepath.Join(w.target, rel), reason})
}

// empty removes everything inside the directory d, whose path relative to the
// target is rel, and reports whether it is now empty. It does not remove d.
//
// Each directory is worked on through its own Root, opened from its parent's,
// so every operation is a single path component: nothing is re-resolved from
// the top, and nothing can be redirected by a component higher up.
func (w *walker) empty(d *os.Root, rel string) bool {
	f, err := d.Open(".")
	if err != nil {
		w.fail(rel, err)
		return false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		w.fail(rel, err)
		return false
	}
	// Go's module cache makes every directory 0555; nothing inside can be
	// unlinked until it is writable again. Only our own directories are
	// changed -- someone else's was read-only for a reason this code does
	// not know, and the removal fails as it should.
	mode := fi.Mode().Perm()
	chmodded := false
	if uid, ok := ownerOf(fi); ok && uid == w.uid && mode&0o700 != 0o700 {
		chmodded = f.Chmod(mode|0o700) == nil
	}
	entries, err := f.ReadDir(-1)
	if err != nil {
		w.fail(rel, err)
		return false
	}

	isEmpty := true
	for _, e := range entries {
		name := e.Name()
		childRel := filepath.Join(rel, name)
		cfi, err := d.Lstat(name)
		if os.IsNotExist(err) {
			continue // vanished on its own: nothing to do
		}
		if err != nil {
			w.fail(childRel, err)
			isEmpty = false
			continue
		}
		if !cfi.IsDir() {
			beforeUnlink(d, name)
			if err := d.Remove(name); err != nil && !os.IsNotExist(err) {
				if notEmpty(err) {
					w.skip(childRel, changed) // replaced by a directory mid-run
				} else {
					w.fail(childRel, err)
				}
				isEmpty = false
			}
			continue
		}
		if devOf(cfi) != w.dev || w.mounts[childRel] {
			w.skip(childRel, "mount point: another filesystem")
			isEmpty = false
			continue
		}
		if !w.descend(d, name, childRel, cfi) {
			isEmpty = false
			continue
		}
		beforeRmdir(d, name)
		if err := d.Remove(name); err != nil && !os.IsNotExist(err) {
			if notEmpty(err) {
				w.skip(childRel, changed)
			} else {
				w.fail(childRel, err)
			}
			isEmpty = false
		}
	}
	// The mode was changed only so the directory could be deleted. If it is
	// staying, it goes back to what it was.
	if !isEmpty && chmodded {
		f.Chmod(mode)
	}
	return isEmpty
}

// descend opens the child directory name of d and empties it. want is what
// Lstat saw there; if something else is found now -- the directory was
// swapped for a symlink, perhaps to a mount point the walk skipped -- it is
// left alone. A child that vanished counts as emptied.
func (w *walker) descend(d *os.Root, name, rel string, want os.FileInfo) bool {
	beforeOpen(d, rel)
	sub, err := d.OpenRoot(name)
	if os.IsNotExist(err) {
		return true // removed by someone else: the end state asked for
	}
	if err != nil {
		// A symlink swapped in that points out of d is refused by the Root.
		w.skip(rel, changed)
		return false
	}
	defer sub.Close()
	sfi, err := sub.Stat(".")
	if err != nil || !os.SameFile(sfi, want) {
		w.skip(rel, changed)
		return false
	}
	return w.empty(sub, rel)
}
