package runner

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/mralaminahamed/reclaim/internal/unit"
)

// modcacheTree lays out a directory the way Go's module cache does: the module
// directory and everything in it read-only, so nothing inside can be unlinked
// until its parent is writable again.
func modcacheTree(t *testing.T) (root, file string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	root = filepath.Join(t.TempDir(), "mod")
	mod := filepath.Join(root, "golang.org", "x", "net@v0.58.0", "quic")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	file = filepath.Join(mod, "stream.go")
	if err := os.WriteFile(file, make([]byte, 4096), 0o444); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{mod, filepath.Dir(mod)} {
		if err := os.Chmod(d, 0o555); err != nil {
			t.Fatal(err)
		}
	}
	// If the test fails the tree is still read-only, and t.TempDir could not
	// remove it either.
	t.Cleanup(func() {
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				os.Chmod(p, 0o755)
			}
			return nil
		})
	})
	return root, file
}

func TestApplyRemovesReadOnlyTrees(t *testing.T) {
	root, _ := modcacheTree(t)
	u := &unit.Unit{ID: "go-modcache", Kind: unit.KindPaths, Paths: []string{root}, Bytes: 4096}

	res := (&Runner{Apply: true}).Run([]*unit.Unit{u})

	if res[0].Err != nil {
		t.Fatalf("Err = %v, want the read-only tree removed", res[0].Err)
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Errorf("%s still exists (err=%v)", root, err)
	}
}

func TestDryRunLeavesReadOnlyTreesReadOnly(t *testing.T) {
	root, file := modcacheTree(t)
	u := &unit.Unit{ID: "go-modcache", Kind: unit.KindPaths, Paths: []string{root}, Bytes: 4096}

	(&Runner{Apply: false}).Run([]*unit.Unit{u})

	fi, err := os.Stat(filepath.Dir(file))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o555 {
		t.Errorf("dry run changed %s to %v, want it left 0555", filepath.Dir(file), fi.Mode().Perm())
	}
}

func TestReadOnlyRemovalDoesNotFollowSymlinksOut(t *testing.T) {
	root, file := modcacheTree(t)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.Mkdir(outside, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(outside, 0o755) })
	// The link goes inside a read-only directory: anywhere writable, the first
	// RemoveAll pass would delete it before the permission fix-up ever ran.
	mod := filepath.Dir(file)
	os.Chmod(mod, 0o755)
	if err := os.Symlink(outside, filepath.Join(mod, "link")); err != nil {
		t.Fatal(err)
	}
	os.Chmod(mod, 0o555)
	u := &unit.Unit{ID: "go-modcache", Kind: unit.KindPaths, Paths: []string{root}, Bytes: 4096}

	(&Runner{Apply: true}).Run([]*unit.Unit{u})

	fi, err := os.Stat(outside)
	if err != nil {
		t.Fatalf("directory outside the tree is gone: %v", err)
	}
	if fi.Mode().Perm() != 0o555 {
		t.Errorf("outside directory changed to %v, want it left 0555", fi.Mode().Perm())
	}
}
