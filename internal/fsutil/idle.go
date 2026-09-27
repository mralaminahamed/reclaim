package fsutil

import (
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// IdleEntries lists the entries directly inside dir, a scratch directory
// such as /tmp, that uid owns and that nothing has used since cutoff.
//
// An entry is idle only when it and everything under it were last read,
// written and changed before cutoff. Change time counts because extracting
// an archive keeps its old modification times, so a tree unpacked a minute
// ago would otherwise look years idle. An entry holding a socket, a pipe or
// a device is never idle: those are rendezvous points for something running,
// and their times say nothing about whether it still is. Nor is one reaching
// into another filesystem, or one that cannot be read in full -- what cannot
// be examined cannot be shown to be idle.
func IdleEntries(dir string, cutoff time.Time, uid uint32) []string {
	top, err := os.Lstat(dir)
	if err != nil || !top.IsDir() {
		return nil
	}
	dev, _ := statOf(top)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		info, err := os.Lstat(p)
		if err != nil {
			continue
		}
		st, ok := statOf(info)
		if !ok || st.uid != uid || st.dev != dev.dev {
			continue
		}
		if idleTree(p, cutoff, dev.dev) {
			out = append(out, p)
		}
	}
	return out
}

// IdleSince reports whether path and everything under it are unused since
// cutoff, by the rules of IdleEntries. A missing path is not idle: there is
// nothing to say about it.
func IdleSince(path string, cutoff time.Time) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	st, _ := statOf(info)
	return idleTree(path, cutoff, st.dev)
}

func idleTree(root string, cutoff time.Time, dev uint64) bool {
	idle := true
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := info.Mode()
		if !mode.IsRegular() && !mode.IsDir() && mode&fs.ModeSymlink == 0 {
			idle = false
			return filepath.SkipAll
		}
		if st, ok := statOf(info); ok && st.dev != dev {
			idle = false
			return filepath.SkipAll
		}
		if lastTouch(info).After(cutoff) {
			idle = false
			return filepath.SkipAll
		}
		return nil
	})
	return idle && err == nil
}

// lastTouch is the latest of access, modification and change time -- except
// that a directory's access time is left out. Listing a directory sets it,
// and reclaim lists every directory it examines, so counting it would make
// the check its own evidence of use.
func lastTouch(fi fs.FileInfo) time.Time {
	t := fi.ModTime()
	if !fi.IsDir() {
		t = lastUse(fi)
	}
	if c, ok := changeTime(fi); ok && c.After(t) {
		t = c
	}
	return t
}

// OwnedBy reports whether path itself, not following a link, belongs to uid.
func OwnedBy(path string, uid uint32) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	st, ok := statOf(info)
	return ok && st.uid == uid
}
