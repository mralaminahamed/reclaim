package fsutil

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// FilesOldestFirst lists the regular files under dir, least recently used
// first, for trimming a cache instead of wiping it.
//
// Last use is the later of a file's access and modification times. Access
// time alone lies on a noatime mount and modification time alone lies about a
// cache that is read on every build and written once; the later of the two is
// never older than the truth. Symlinks are not followed and not listed, and
// the walk stays on dir's filesystem. Ties go by path, so the order is stable.
func FilesOldestFirst(dir string) []string {
	top, err := os.Lstat(dir)
	if err != nil || !top.IsDir() {
		return nil
	}
	dev, _ := statOf(top)

	type file struct {
		path string
		used time.Time
	}
	var files []file
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if st, ok := statOf(info); ok && st.dev != dev.dev {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Mode().IsRegular() {
			files = append(files, file{p, lastUse(info)})
		}
		return nil
	})
	sort.Slice(files, func(i, j int) bool {
		if !files[i].used.Equal(files[j].used) {
			return files[i].used.Before(files[j].used)
		}
		return files[i].path < files[j].path
	})
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.path
	}
	return out
}

func lastUse(fi fs.FileInfo) time.Time {
	if a, ok := accessTime(fi); ok && a.After(fi.ModTime()) {
		return a
	}
	return fi.ModTime()
}
