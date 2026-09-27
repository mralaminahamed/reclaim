package remove

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mk(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gone(t *testing.T, p string) bool {
	t.Helper()
	_, err := os.Lstat(p)
	return os.IsNotExist(err)
}

func TestTreeRemovesATree(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	mk(t, filepath.Join(root, "a", "b", "f"))
	mk(t, filepath.Join(root, "g"))

	out := Tree(root)

	if out.Err != nil || !out.Gone || !gone(t, root) {
		t.Fatalf("Tree = %+v; root gone = %v", out, gone(t, root))
	}
}

func TestTreeOnAMissingPathIsGone(t *testing.T) {
	out := Tree(filepath.Join(t.TempDir(), "absent"))
	if out.Err != nil || !out.Gone {
		t.Fatalf("Tree(absent) = %+v", out)
	}
}

func TestTreeRemovesAFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	mk(t, p)
	if out := Tree(p); out.Err != nil || !gone(t, p) {
		t.Fatalf("Tree(file) = %+v", out)
	}
}

// A cache path that is a link to somewhere else is the link's business, not
// ours: removing the link is safe, following it is not.
func TestTreeRemovesOnlyALinkedTarget(t *testing.T) {
	dir := t.TempDir()
	mk(t, filepath.Join(dir, "outside", "precious"))
	link := filepath.Join(dir, "cache")
	if err := os.Symlink(filepath.Join(dir, "outside"), link); err != nil {
		t.Fatal(err)
	}

	out := Tree(link)

	if out.Err != nil || !gone(t, link) {
		t.Fatalf("Tree(link) = %+v", out)
	}
	if gone(t, filepath.Join(dir, "outside", "precious")) {
		t.Fatal("the link was followed and its target deleted")
	}
}

func TestTreeDoesNotFollowALinkInside(t *testing.T) {
	dir := t.TempDir()
	mk(t, filepath.Join(dir, "outside", "precious"))
	root := filepath.Join(dir, "cache")
	mk(t, filepath.Join(root, "f"))
	if err := os.Symlink(filepath.Join(dir, "outside"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}

	if out := Tree(root); out.Err != nil || !gone(t, root) {
		t.Fatalf("Tree = %+v", out)
	}
	if gone(t, filepath.Join(dir, "outside", "precious")) {
		t.Fatal("a link inside the tree was followed")
	}
}

func TestTreeRemovesReadOnlyDirectoriesItOwns(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	root := filepath.Join(t.TempDir(), "mod")
	mod := filepath.Join(root, "golang.org", "x", "net@v0.58.0")
	mk(t, filepath.Join(mod, "stream.go"))
	for _, d := range []string{mod, filepath.Dir(mod)} {
		if err := os.Chmod(d, 0o555); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				os.Chmod(p, 0o755)
			}
			return nil
		})
	})

	if out := Tree(root); out.Err != nil || !gone(t, root) {
		t.Fatalf("Tree(read-only) = %+v", out)
	}
}

// A bind mount or tmpfs inside a cache belongs to another filesystem. The
// walk must stop at it and leave its parents standing.
func TestTreeDoesNotCrossIntoAnotherDevice(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	mk(t, filepath.Join(root, "mnt", "precious"))
	mk(t, filepath.Join(root, "junk"))
	mnt := filepath.Join(root, "mnt")

	real := devOf
	t.Cleanup(func() { devOf = real })
	devOf = func(fi fs.FileInfo) uint64 {
		if fi.Name() == "mnt" && fi.IsDir() {
			return real(fi) + 1
		}
		return real(fi)
	}

	out := Tree(root)

	if out.Err != nil {
		t.Fatalf("Err = %v; a mount point is a skip, not a failure", out.Err)
	}
	if gone(t, filepath.Join(mnt, "precious")) {
		t.Fatal("the walk crossed into another device")
	}
	if !gone(t, filepath.Join(root, "junk")) {
		t.Error("siblings of the mount point were not removed")
	}
	if out.Gone || len(out.Skipped) == 0 || !strings.Contains(out.Skipped[0].Reason, "mount") {
		t.Errorf("Outcome = %+v, want the mount point reported as skipped", out)
	}
}

// uv or gopls may write into a cache while it is being removed. That is not a
// failure of the run, and must not stop it.
func TestTreeReportsARecreatedDirectoryAsSkipped(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	mk(t, filepath.Join(root, "sub", "f"))

	real := beforeRmdir
	t.Cleanup(func() { beforeRmdir = real })
	beforeRmdir = func(r *os.Root, name string) {
		if filepath.Base(name) == "sub" {
			f, _ := r.Create(filepath.Join(name, "new"))
			if f != nil {
				f.Close()
			}
		}
	}

	out := Tree(root)

	if out.Err != nil {
		t.Fatalf("Err = %v, want a skip", out.Err)
	}
	if len(out.Skipped) == 0 {
		t.Fatalf("Outcome = %+v, want the re-populated directory skipped", out)
	}
}

func TestTreeFailsOnADirectoryItCannotWrite(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	// Tree only makes directories *inside* the target writable. The target's
	// own parent is not its to change, so a read-only parent must surface as
	// an error rather than be chmodded.
	parent := filepath.Join(t.TempDir(), "p")
	target := filepath.Join(parent, "f")
	mk(t, target)
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(parent, 0o755) })

	if out := Tree(target); out.Err == nil {
		t.Fatalf("Tree = %+v, want a permission error: the target's parent is not ours to change", out)
	}
}

// The target is swapped for a symlink between the check and the open. The walk
// must notice it is no longer looking at what it checked, and delete nothing.
func TestTreeRefusesATargetSwappedAfterTheCheck(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "cache")
	mk(t, filepath.Join(target, "f"))
	mk(t, filepath.Join(dir, "outside", "precious"))

	real := afterLstat
	t.Cleanup(func() { afterLstat = real })
	afterLstat = func() {
		os.Rename(target, target+".old")
		os.Symlink("outside", target)
	}

	out := Tree(target)

	if gone(t, filepath.Join(dir, "outside", "precious")) {
		t.Fatal("the walk followed a symlink swapped in for the target")
	}
	if out.Err != nil || len(out.Skipped) == 0 {
		t.Errorf("Outcome = %+v, want a skip for the changed target", out)
	}
}

// Same race one level down: a child directory swapped for a link to something
// the walk had decided to leave alone.
func TestTreeRefusesAChildSwappedAfterTheCheck(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	mk(t, filepath.Join(root, "a", "f"))
	mk(t, filepath.Join(root, "keep", "precious"))

	realDev, realOpen := devOf, beforeOpen
	t.Cleanup(func() { devOf, beforeOpen = realDev, realOpen })
	devOf = func(fi fs.FileInfo) uint64 {
		if fi.Name() == "keep" && fi.IsDir() {
			return realDev(fi) + 1
		}
		return realDev(fi)
	}
	beforeOpen = func(r *os.Root, name string) {
		if name == "a" {
			os.Rename(filepath.Join(root, "a"), filepath.Join(root, "a.old"))
			os.Symlink("keep", filepath.Join(root, "a"))
		}
	}

	Tree(root)

	if gone(t, filepath.Join(root, "keep", "precious")) {
		t.Fatal("a child swapped for a link led the walk into a skipped mount")
	}
}

// A bind mount shares its source's device number, so the device check alone
// cannot see it. The mount table can.
func TestTreeDoesNotDescendIntoABindMount(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	mk(t, filepath.Join(root, "bind", "precious"))
	mk(t, filepath.Join(root, "junk"))

	real := mountPoints
	t.Cleanup(func() { mountPoints = real })
	canon, _ := filepath.EvalSymlinks(root)
	mountPoints = func() []string { return []string{filepath.Join(canon, "bind")} }

	out := Tree(root)

	if gone(t, filepath.Join(root, "bind", "precious")) {
		t.Fatal("the walk descended into a bind mount")
	}
	if !gone(t, filepath.Join(root, "junk")) || out.Err != nil {
		t.Errorf("Outcome = %+v; siblings should go and the mount be skipped", out)
	}
}

// Something else deleting part of the tree while it is walked (uv cache prune
// running at the same time) is not a failure: the end state is what was asked.
func TestTreeToleratesADirectoryThatVanishes(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	mk(t, filepath.Join(root, "sub", "f"))

	real := beforeOpen
	t.Cleanup(func() { beforeOpen = real })
	beforeOpen = func(r *os.Root, name string) {
		if name == "sub" {
			os.RemoveAll(filepath.Join(root, "sub"))
		}
	}

	out := Tree(root)

	if out.Err != nil || !out.Gone {
		t.Fatalf("Outcome = %+v, want the tree gone and no error", out)
	}
}
