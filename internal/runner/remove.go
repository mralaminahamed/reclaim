package runner

import (
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// removeAll is os.RemoveAll that also gets through read-only directories.
//
// Go's module cache makes every module directory 0555 so builds cannot edit
// it; `go clean -modcache` has to make it writable again before removing it,
// and so does anything else deleting it. Plain RemoveAll stops at the first
// file in such a directory and leaves a half-deleted cache behind.
//
// Only directories owned by this user are made writable. Anything else was
// read-only for a reason this code does not know, and stays that way: the
// removal fails and reports it, as it did before.
func removeAll(path string) error {
	err := os.RemoveAll(path)
	if err == nil || !os.IsPermission(err) {
		return err
	}
	uid := uint32(os.Geteuid())
	filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		// WalkDir never follows symlinks, so a link cannot point this at a
		// directory outside path.
		if err != nil || !d.IsDir() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); !ok || st.Uid != uid {
			return fs.SkipDir
		}
		if perm := fi.Mode().Perm(); perm&0o700 != 0o700 {
			os.Chmod(p, perm|0o700)
		}
		return nil
	})
	return os.RemoveAll(path)
}
