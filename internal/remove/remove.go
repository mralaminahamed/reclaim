// Package remove deletes a directory tree without ever leaving it.
//
// os.RemoveAll resolves paths from the top on every call, descends into
// mount points, and gives up at the first file in a read-only directory. This
// walks the tree through an os.Root opened on the target itself, so no
// symlink -- present from the start or swapped in mid-run -- can move an
// operation outside it. It removes children before parents, one entry at a
// time, so anything it cannot or must not remove leaves its ancestors
// standing and is reported, rather than aborting the rest.
package remove

import (
	"fmt"
	"os"
	"path/filepath"
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

// Test hooks. devOf lets a test fake a mount point; beforeRmdir lets it
// re-populate a directory between emptying it and removing it.
var (
	devOf       = deviceOf
	beforeRmdir = func(root *os.Root, name string) {}
)

// Tree removes target and everything under it.
func Tree(target string) Outcome {
	fi, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return Outcome{Gone: true}
	}
	if err != nil {
		return Outcome{Err: err}
	}
	// A link or a file is one unlink. A link is never followed: what it
	// points at is not part of this tree.
	if !fi.IsDir() {
		if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
			return Outcome{Err: err}
		}
		return Outcome{Gone: true}
	}

	root, err := os.OpenRoot(target)
	if err != nil {
		return Outcome{Err: err}
	}
	w := &walker{root: root, target: target, dev: devOf(fi), uid: uint32(os.Geteuid())}
	emptied := w.dir(".")
	root.Close()

	out := Outcome{Skipped: w.skipped, Err: w.err}
	if emptied {
		if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
			if notEmpty(err) {
				out.Skipped = append(out.Skipped, Skip{target, "changed during removal"})
			} else if out.Err == nil {
				out.Err = err
			}
			return out
		}
		out.Gone = true
	}
	return out
}

type walker struct {
	root    *os.Root
	target  string
	dev     uint64
	uid     uint32
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
// it is now empty. It does not remove name itself.
func (w *walker) dir(name string) bool {
	f, err := w.root.Open(name)
	if err != nil {
		w.fail(name, err)
		return false
	}
	// Go's module cache makes every directory 0555; nothing inside can be
	// unlinked until it is writable again. Only our own directories are
	// changed -- someone else's was read-only for a reason this code does
	// not know, and the removal fails as it should.
	if fi, err := f.Stat(); err == nil {
		if uid, ok := ownerOf(fi); ok && uid == w.uid && fi.Mode().Perm()&0o700 != 0o700 {
			f.Chmod(fi.Mode().Perm() | 0o700)
		}
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
		fi, err := w.root.Lstat(child)
		if os.IsNotExist(err) {
			continue // vanished on its own: nothing to do
		}
		if err != nil {
			w.fail(child, err)
			empty = false
			continue
		}
		if fi.IsDir() {
			if devOf(fi) != w.dev {
				w.skip(child, "mount point: another filesystem")
				empty = false
				continue
			}
			if !w.dir(child) {
				empty = false
				continue
			}
			beforeRmdir(w.root, child)
			if err := w.root.Remove(child); err != nil && !os.IsNotExist(err) {
				if notEmpty(err) {
					w.skip(child, "changed during removal")
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
