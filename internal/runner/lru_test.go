package runner

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mralaminahamed/reclaim/internal/remove"
	"github.com/mralaminahamed/reclaim/internal/unit"
)

// lruCache writes files aged one, two, ... days, oldest last in the list.
func lruCache(t *testing.T, names ...string) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	var paths []string
	for i, n := range names {
		p := filepath.Join(dir, n)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, make([]byte, 4096), 0o644); err != nil {
			t.Fatal(err)
		}
		when := time.Now().Add(-time.Duration(i+1) * 24 * time.Hour)
		if err := os.Chtimes(p, when, when); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	return dir, paths
}

// goneCount is free space that grows by one unit per file deleted.
func goneCount(paths []string) func(string) (int64, error) {
	return func(string) (int64, error) {
		var n int64
		for _, p := range paths {
			if _, err := os.Lstat(p); os.IsNotExist(err) {
				n++
			}
		}
		return n, nil
	}
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func TestLRUUnitGivesUpOldestFilesUntilTargetIsMet(t *testing.T) {
	dir, files := lruCache(t, "new", "sub/mid", "sub/old", "oldest")
	u := &unit.Unit{ID: "c", Kind: unit.KindPaths, Paths: []string{dir}, LRU: true}

	r := &Runner{Apply: true, TargetBytes: 2, TargetPath: "/", Avail: goneCount(files)}
	res := r.Run([]*unit.Unit{u})

	if exists(files[3]) || exists(files[2]) {
		t.Error("the two oldest files survived")
	}
	if !exists(files[0]) || !exists(files[1]) {
		t.Error("newer files were deleted after the target was met")
	}
	if !r.StoppedEarly {
		t.Error("StoppedEarly = false, want true")
	}
	if len(res[0].Removed) != 2 || res[0].Removed[0] != files[3] {
		t.Errorf("Removed = %v, want the two oldest files, oldest first", res[0].Removed)
	}
	if res[0].Freed <= 0 {
		t.Errorf("Freed = %d, want what the deleted files held", res[0].Freed)
	}
}

// Trimming is what a target asks for. Without one there is nothing to stop
// at, and the unit goes whole, as it always did.
func TestLRUUnitWithoutTargetGoesWhole(t *testing.T) {
	dir, _ := lruCache(t, "a", "b")
	u := &unit.Unit{ID: "c", Kind: unit.KindPaths, Paths: []string{dir}, LRU: true}

	(&Runner{Apply: true}).Run([]*unit.Unit{u})

	if exists(dir) {
		t.Error("cache directory survived a run with no target")
	}
}

// Once every file is gone and the target is still short, what is left --
// directories, links -- goes the ordinary way, and the unit ends as removed
// as it would have been without trimming.
func TestLRUUnitShortOfTargetFinishesTheWholeTree(t *testing.T) {
	dir, files := lruCache(t, "a", "sub/b")
	if err := os.Symlink("/nonexistent", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	u := &unit.Unit{ID: "c", Kind: unit.KindPaths, Paths: []string{dir}, LRU: true}

	r := &Runner{Apply: true, TargetBytes: 100, TargetPath: "/", Avail: goneCount(files)}
	res := r.Run([]*unit.Unit{u})

	if exists(dir) {
		t.Error("cache directory survived though the target was never met")
	}
	if r.StoppedEarly {
		t.Error("StoppedEarly = true, but the target was never met")
	}
	if got := res[0].Removed; len(got) == 0 || got[len(got)-1] != dir {
		t.Errorf("Removed = %v, want it to end with the directory itself", got)
	}
}

func TestLRUDryRunDeletesNothing(t *testing.T) {
	dir, files := lruCache(t, "a", "b")
	u := &unit.Unit{ID: "c", Kind: unit.KindPaths, Paths: []string{dir}, LRU: true}

	(&Runner{TargetBytes: 1, TargetPath: "/", Avail: goneCount(files)}).Run([]*unit.Unit{u})

	for _, f := range files {
		if !exists(f) {
			t.Fatalf("dry run deleted %s", f)
		}
	}
}

// Each trimmed file goes through the same remover as a whole target, with its
// swap and mount checks, never a bare unlink.
func TestLRUTrimGoesThroughTheRemover(t *testing.T) {
	dir, files := lruCache(t, "a")
	u := &unit.Unit{ID: "c", Kind: unit.KindPaths, Paths: []string{dir}, LRU: true}
	var asked []string
	r := &Runner{Apply: true, TargetBytes: 5, TargetPath: "/", Avail: goneCount(files)}
	r.remove = func(p string) remove.Outcome {
		asked = append(asked, p)
		return remove.Tree(p)
	}
	r.Run([]*unit.Unit{u})
	if len(asked) == 0 || asked[0] != files[0] {
		t.Fatalf("remove asked for %v, want the file first", asked)
	}
}
