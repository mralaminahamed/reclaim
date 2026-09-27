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
	devOf       = deviceOf
	mountPoints = fsutil.MountPoints
	afterLstat  = func() {}
	beforeOpen  = func(root *os.Root, name string) {}
	beforeRmdir = func(root *os.Root, name string) {}
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
	w := &walker{root: root, target: target, dev: devOf(fi), uid: uint32(os.Geteuid()),
		mounts: mountsUnder(target)}
	emptied := w.dir(".", fi)
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
	root    *os.Root
	target  string
	dev     uint64
	uid     uint32
	mounts  map[string]bool
	skipped []Skip
	err     error
}

func (w *walker) fail(name string, err error) {
	if w.err == nil {
		w.err = fmt.Errorf("%s: %w", filepath.Join(w.target, name), err)
	}
}

func (w *walker) skip(name, reason string) {
	w.skipped = append(w.skipped, Skip{filepath.Join(w.target, name), reason})
}

// dir empties the directory name (relative to the root) and reports whether
// it is now empty. It does not remove name itself. want is what the caller
// saw at name when it decided to descend; anything else found there now is
// left alone.
func (w *walker) dir(name string, want os.FileInfo) bool {
	beforeOpen(w.root, name)
	f, err := w.root.Open(name)
	if os.IsNotExist(err) {
		return true // removed by someone else: the end state asked for
	}
	if err != nil {
		w.fail(name, err)
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		w.fail(name, err)
		return false
	}
	// Open follows symlinks inside the root. If name was swapped for a link
	// since it was checked, this is a different directory -- possibly one
	// the walk deliberately skipped, such as a mount point.
	if !os.SameFile(fi, want) {
		f.Close()
		w.skip(name, changed)
		return false
	}
	// Go's module cache makes every directory 0555; nothing inside can be
	// unlinked until it is writable again. Only our own directories are
	// changed -- someone else's was read-only for a reason this code does
	// not know, and the removal fails as it should.
	if uid, ok := ownerOf(fi); ok && uid == w.uid && fi.Mode().Perm()&0o700 != 0o700 {
		f.Chmod(fi.Mode().Perm() | 0o700)
	}
	entries, err := f.ReadDir(-1)
	f.Close()
	if err != nil {
		w.fail(name, err)
		return false
	}

	empty := true
	for _, e := range entries {
		child := filepath.Join(name, e.Name())
		cfi, err := w.root.Lstat(child)
		if os.IsNotExist(err) {
			continue // vanished on its own: nothing to do
		}
		if err != nil {
			w.fail(child, err)
			empty = false
			continue
		}
		if cfi.IsDir() {
			if devOf(cfi) != w.dev || w.mounts[child] {
				w.skip(child, "mount point: another filesystem")
				empty = false
				continue
			}
			if !w.dir(child, cfi) {
				empty = false
				continue
			}
			beforeRmdir(w.root, child)
			if err := w.root.Remove(child); err != nil && !os.IsNotExist(err) {
				if notEmpty(err) {
					w.skip(child, changed)
				} else {
					w.fail(child, err)
				}
				empty = false
			}
			continue
		}
		if err := w.root.Remove(child); err != nil && !os.IsNotExist(err) {
			w.fail(child, err)
			empty = false
		}
	}
	return empty
}
