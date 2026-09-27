package fsutil

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func stamp(t *testing.T, path string, atime, mtime time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := os.Chtimes(path, now.Add(-atime), now.Add(-mtime)); err != nil {
		t.Fatal(err)
	}
}

func names(root string, fs []string) []string {
	var out []string
	for _, f := range fs {
		rel, _ := filepath.Rel(root, f)
		out = append(out, rel)
	}
	return out
}

func TestFilesOldestFirstOrdersByLastUse(t *testing.T) {
	dir := t.TempDir()
	stamp(t, filepath.Join(dir, "new"), time.Hour, time.Hour)
	stamp(t, filepath.Join(dir, "a", "old"), 30*24*time.Hour, 30*24*time.Hour)
	stamp(t, filepath.Join(dir, "a", "b", "mid"), 5*24*time.Hour, 5*24*time.Hour)

	got := names(dir, FilesOldestFirst(dir))
	want := []string{"a/old", "a/b/mid", "new"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != filepath.FromSlash(want[i]) {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// A file written long ago but read yesterday is in use. Last use is the later
// of the two times, so a cache read on every build is not the first to go.
func TestFilesOldestFirstCountsARecentReadAsUse(t *testing.T) {
	dir := t.TempDir()
	stamp(t, filepath.Join(dir, "read-yesterday"), 24*time.Hour, 90*24*time.Hour)
	stamp(t, filepath.Join(dir, "untouched"), 10*24*time.Hour, 10*24*time.Hour)

	got := names(dir, FilesOldestFirst(dir))
	if len(got) != 2 || got[0] != "untouched" {
		t.Fatalf("got %v, want untouched first", got)
	}
}

// Only regular files are offered: a symlink is not the cache's to trim and
// what it points at is not in the tree.
func TestFilesOldestFirstSkipsSymlinks(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	stamp(t, outside, 99*24*time.Hour, 99*24*time.Hour)
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	outDir := t.TempDir()
	stamp(t, filepath.Join(outDir, "deep"), 99*24*time.Hour, 99*24*time.Hour)
	if err := os.Symlink(outDir, filepath.Join(dir, "dirlink")); err != nil {
		t.Fatal(err)
	}
	stamp(t, filepath.Join(dir, "real"), time.Hour, time.Hour)

	got := names(dir, FilesOldestFirst(dir))
	if len(got) != 1 || got[0] != "real" {
		t.Fatalf("got %v, want only the regular file", got)
	}
}

func TestFilesOldestFirstOfMissingDirIsEmpty(t *testing.T) {
	if got := FilesOldestFirst(filepath.Join(t.TempDir(), "nope")); len(got) != 0 {
		t.Fatalf("got %v, want nothing", got)
	}
}
