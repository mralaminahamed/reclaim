package index

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, path string, n int, age time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, n), 0o644); err != nil {
		t.Fatal(err)
	}
	if age > 0 {
		when := time.Now().Add(-age)
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatal(err)
		}
	}
}

func tree(t *testing.T) string {
	root := t.TempDir()
	write(t, filepath.Join(root, "a/one"), 1000, 0)
	write(t, filepath.Join(root, "a/deep/two"), 2000, 0)
	write(t, filepath.Join(root, "b/three"), 4000, 0)
	write(t, filepath.Join(root, "top"), 10, 0)
	return root
}

func TestBuildTotalsEverySubtree(t *testing.T) {
	root := tree(t)
	tr, err := Build(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := tr.Total(0); got != 7010 {
		t.Errorf("root total %d, want 7010", got)
	}
	kids, ok := tr.Children(root)
	if !ok || len(kids) != 2 {
		t.Fatalf("children %+v", kids)
	}
	if kids[0].Path != filepath.Join(root, "b") || kids[0].Bytes != 4000 {
		t.Errorf("largest child %+v, want b at 4000", kids[0])
	}
	if kids[1].Bytes != 3000 {
		t.Errorf("a total %d, want 3000 including a/deep", kids[1].Bytes)
	}
}

// The walk is concurrent; the answer must not depend on how the work was
// split. One worker and many give the same totals everywhere.
func TestBuildIsIndependentOfWorkerCount(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 40; i++ {
		write(t, filepath.Join(root, string(rune('a'+i%7)), string(rune('a'+i%5)), "f"+string(rune('a'+i))), 100+i, 0)
	}
	one, _ := Build(root, Options{Workers: 1})
	many, _ := Build(root, Options{Workers: 32})
	a, _ := one.Children(root)
	b, _ := many.Children(root)
	if one.Total(0) != many.Total(0) || len(a) != len(b) {
		t.Fatalf("totals %d vs %d", one.Total(0), many.Total(0))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("%+v vs %+v", a[i], b[i])
		}
	}
}

// A refresh reuses a directory whose mtime is unchanged and re-reads one that
// changed. Adding a file changes its directory's mtime, so it is seen.
func TestRefreshSeesAddedFilesAndReusesTheRest(t *testing.T) {
	root := tree(t)
	prev, _ := Build(root, Options{})
	write(t, filepath.Join(root, "b/four"), 500, 0)

	next, _ := Build(root, Options{Prev: prev})
	if next.Total(0) != 7510 {
		t.Errorf("total %d after adding 500 bytes, want 7510", next.Total(0))
	}
}

// What a refresh cannot see, by design: a file growing in place leaves its
// directory's mtime alone. This test pins that limit so it is never mistaken
// for a bug fixed by accident -- or forgotten in the docs.
func TestRefreshDoesNotSeeInPlaceGrowth(t *testing.T) {
	root := tree(t)
	prev, _ := Build(root, Options{})
	dir := filepath.Join(root, "b")
	fi, _ := os.Stat(dir)
	f, _ := os.OpenFile(filepath.Join(dir, "three"), os.O_APPEND|os.O_WRONLY, 0)
	f.Write(make([]byte, 100))
	f.Close()
	os.Chtimes(dir, fi.ModTime(), fi.ModTime())

	next, _ := Build(root, Options{Prev: prev})
	if next.Total(0) != 7010 {
		t.Errorf("total %d: refresh re-read an unchanged directory", next.Total(0))
	}
	full, _ := Build(root, Options{})
	if full.Total(0) != 7110 {
		t.Errorf("full rebuild total %d, want 7110", full.Total(0))
	}
}

func TestRefreshDropsRemovedDirectories(t *testing.T) {
	root := tree(t)
	prev, _ := Build(root, Options{})
	if err := os.RemoveAll(filepath.Join(root, "a/deep")); err != nil {
		t.Fatal(err)
	}
	next, _ := Build(root, Options{Prev: prev})
	if next.Total(0) != 5010 {
		t.Errorf("total %d, want 5010 after removing a/deep", next.Total(0))
	}
}

func TestSkipLeavesADirectoryOut(t *testing.T) {
	root := tree(t)
	tr, _ := Build(root, Options{Skip: func(p string, _ fs.FileInfo) bool {
		return filepath.Base(p) == "b"
	}})
	if tr.Total(0) != 3010 {
		t.Errorf("total %d, want 3010 without b", tr.Total(0))
	}
}

// Cold names the top of each cold subtree once, not every level inside it.
func TestColdReportsTheTopOfEachColdSubtree(t *testing.T) {
	root := t.TempDir()
	old := 400 * 24 * time.Hour
	write(t, filepath.Join(root, "archive/2019/a"), 5000, old)
	write(t, filepath.Join(root, "archive/2020/b"), 5000, old)
	write(t, filepath.Join(root, "live/x"), 5000, 0)
	tr, _ := Build(root, Options{})

	got := tr.Cold(time.Now().Add(-365*24*time.Hour), 1000)
	if len(got) != 1 || got[0].Path != filepath.Join(root, "archive") || got[0].Bytes != 10000 {
		t.Errorf("cold %+v, want only archive at 10000", got)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	root := tree(t)
	tr, _ := Build(root, Options{})
	x := &Index{}
	x.Put(tr)
	p := filepath.Join(t.TempDir(), "sub", "index.gob")
	if err := x.Save(p); err != nil {
		t.Fatal(err)
	}
	y, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	got := y.Tree(filepath.Join(root, "a"))
	if got == nil || got.Total(0) != 7010 {
		t.Fatalf("loaded tree %+v", got)
	}
	if kids, _ := got.Children(filepath.Join(root, "a")); len(kids) != 1 || kids[0].Bytes != 2000 {
		t.Errorf("a's children after load %+v", kids)
	}
}

func TestLoadTreatsMissingAndForeignFilesAsNoIndex(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(filepath.Join(dir, "none")); err != ErrNone {
		t.Errorf("missing: %v", err)
	}
	junk := filepath.Join(dir, "junk")
	os.WriteFile(junk, []byte("not a gob"), 0o644)
	if _, err := Load(junk); err != ErrNone {
		t.Errorf("junk: %v", err)
	}
}

func TestUnreadableDirectoriesAreCounted(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	root := tree(t)
	locked := filepath.Join(root, "b")
	os.Chmod(locked, 0)
	defer os.Chmod(locked, 0o755)
	tr, _ := Build(root, Options{})
	if tr.Unreadable != 1 {
		t.Errorf("unreadable %d, want 1", tr.Unreadable)
	}
	if !strings.HasPrefix(tr.Path(1), root) {
		t.Errorf("path %q", tr.Path(1))
	}
}
