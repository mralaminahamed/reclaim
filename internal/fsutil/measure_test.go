package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func fill(t *testing.T, p string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, n)
	for i := range data {
		data[i] = 'x'
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// A sparse file claims its length but occupies almost nothing. Reporting the
// length told --free it had freed space that was never allocated.
func TestMeasureReportsAllocatedNotApparentForSparseFiles(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "sparse"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(64 << 20); err != nil {
		t.Fatal(err)
	}
	f.Close()

	u := Measure([]string{dir})

	if u.Apparent != 64<<20 {
		t.Errorf("Apparent = %d, want %d", u.Apparent, 64<<20)
	}
	if u.Allocated >= 1<<20 {
		t.Errorf("Allocated = %d for a sparse file, want near zero", u.Allocated)
	}
}

// The pnpm store is hard-linked into every project's node_modules. Deleting the
// store alone frees none of the linked files, so it must not claim them.
func TestMeasureCreditsHardlinksOnlyWhenAllLinksAreInside(t *testing.T) {
	root := t.TempDir()
	store := filepath.Join(root, "store")
	project := filepath.Join(root, "project")
	fill(t, filepath.Join(store, "pkg"), 64<<10)
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(store, "pkg"), filepath.Join(project, "pkg")); err != nil {
		t.Fatal(err)
	}

	alone := Measure([]string{store})
	both := Measure([]string{store, project})

	if alone.Allocated != 0 {
		t.Errorf("store alone: Allocated = %d, want 0 (its only file is linked elsewhere)", alone.Allocated)
	}
	if alone.Shared < 64<<10 {
		t.Errorf("store alone: Shared = %d, want the linked file", alone.Shared)
	}
	if both.Allocated < 64<<10 || both.Shared != 0 {
		t.Errorf("store+project: Allocated = %d Shared = %d, want the file credited once", both.Allocated, both.Shared)
	}
	if both.Apparent != 64<<10 {
		t.Errorf("store+project: Apparent = %d, want the file counted once", both.Apparent)
	}
}

func TestMeasureIgnoresMissingPaths(t *testing.T) {
	u := Measure([]string{filepath.Join(t.TempDir(), "absent")})
	if u != (Usage{}) {
		t.Errorf("Measure(absent) = %+v, want zero", u)
	}
}

func TestMeasureDoesNotFollowSymlinks(t *testing.T) {
	root := t.TempDir()
	fill(t, filepath.Join(root, "outside", "big"), 1<<20)
	if err := os.MkdirAll(filepath.Join(root, "in"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "outside"), filepath.Join(root, "in", "link")); err != nil {
		t.Fatal(err)
	}

	if u := Measure([]string{filepath.Join(root, "in")}); u.Apparent >= 1<<20 {
		t.Errorf("Apparent = %d, the link target was followed", u.Apparent)
	}
}

func TestMeasureCountsUnreadableSubtrees(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	fill(t, filepath.Join(locked, "f"), 10)
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })

	if u := Measure([]string{root}); u.Unreadable == 0 {
		t.Errorf("Unreadable = 0, want the locked directory counted")
	}
}
