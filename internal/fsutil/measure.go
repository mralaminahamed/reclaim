package fsutil

import (
	"io/fs"
	"os"
	"path/filepath"
)

// Usage is what a set of paths occupies.
//
// Allocated is the number that matters: blocks actually held on disk, which
// is what deleting gives back. Apparent is the sum of the lengths of the same
// files, kept for comparison. A sparse file has a large Apparent and a tiny
// Allocated.
//
// Shared is allocated space held by files hard-linked from outside the
// measured set. Deleting the set does not free it -- the other link keeps the
// data -- so it is reported beside Allocated, never inside it.
type Usage struct {
	Apparent, Allocated, Shared int64
	// Unreadable counts entries that could not be examined, so a report can
	// say "at least".
	Unreadable int
}

type inode struct{ dev, ino uint64 }

// listMounts is the mount table; a variable so a test can fake a bind mount.
var listMounts = MountPoints

type linked struct {
	seen, nlink uint64
	bytes, size int64
}

// Measure walks every path, never following symlinks and never leaving the
// filesystem each path starts on, and returns what the whole set occupies.
// Paths are measured together so a file hard-linked between two of them is
// credited once, and only when every one of its links is inside the set.
// Directories themselves are not counted, only what they contain.
func Measure(paths []string) Usage {
	var u Usage
	links := map[inode]*linked{}
	mounts := map[string]bool{}
	for _, m := range listMounts() {
		mounts[m] = true
	}
	for _, root := range paths {
		info, err := os.Lstat(root)
		if err != nil {
			if !os.IsNotExist(err) {
				u.Unreadable++
			}
			continue
		}
		rootSt, _ := statOf(info)
		// The mount table names real paths; walk paths are lexical. Map one
		// onto the other through the resolved root.
		canon, err := filepath.EvalSymlinks(root)
		if err != nil {
			canon = root
		}
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				u.Unreadable++
				return nil //nolint:nilerr // one unreadable subtree must not end the walk
			}
			fi, err := d.Info()
			if err != nil {
				u.Unreadable++
				return nil //nolint:nilerr
			}
			st, ok := statOf(fi)
			if d.IsDir() {
				// Another filesystem, or a bind mount of this one: removal
				// leaves both alone, so they are not part of what it frees.
				if ok && st.dev != rootSt.dev {
					return fs.SkipDir
				}
				if p != root {
					rel, _ := filepath.Rel(root, p)
					if mounts[filepath.Join(canon, rel)] {
						return fs.SkipDir
					}
				}
				return nil
			}
			if !ok {
				u.Apparent += fi.Size()
				u.Allocated += fi.Size()
				return nil
			}
			alloc := st.blocks * 512
			if st.nlink <= 1 {
				u.Apparent += fi.Size()
				u.Allocated += alloc
				return nil
			}
			k := inode{st.dev, st.ino}
			l := links[k]
			if l == nil {
				l = &linked{nlink: st.nlink, bytes: alloc, size: fi.Size()}
				links[k] = l
			}
			l.seen++
			return nil
		})
	}
	for _, l := range links {
		if l.seen >= l.nlink {
			u.Allocated += l.bytes
			u.Apparent += l.size
		} else {
			u.Shared += l.bytes
		}
	}
	return u
}
