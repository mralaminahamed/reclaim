package fsutil

import (
	"io/fs"
	"os"
	"path/filepath"
)

// Usage is what a set of paths occupies.
//
// Allocated is the number that matters: blocks actually held on disk, which
// is what deleting gives back. Apparent is the sum of file lengths, kept for
// comparison. A sparse file has a large Apparent and a tiny Allocated.
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

type linked struct {
	seen, nlink uint64
	bytes       int64
}

// Measure walks every path, never following symlinks and never leaving the
// filesystem each path starts on, and returns what the whole set occupies.
// Paths are measured together so a file hard-linked between two of them is
// credited once, and only when every one of its links is inside the set.
// Directories themselves are not counted, only what they contain.
func Measure(paths []string) Usage {
	var u Usage
	links := map[inode]*linked{}
	for _, root := range paths {
		info, err := os.Lstat(root)
		if err != nil {
			if !os.IsNotExist(err) {
				u.Unreadable++
			}
			continue
		}
		rootSt, _ := statOf(info)
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
				if ok && st.dev != rootSt.dev {
					return fs.SkipDir
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
				l = &linked{nlink: st.nlink, bytes: alloc}
				links[k] = l
				u.Apparent += fi.Size()
			}
			l.seen++
			return nil
		})
	}
	for _, l := range links {
		if l.seen >= l.nlink {
			u.Allocated += l.bytes
		} else {
			u.Shared += l.bytes
		}
	}
	return u
}
