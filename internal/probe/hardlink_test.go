package probe

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mralaminahamed/reclaim/internal/unit"
)

// A unit that names both halves of a hard link frees the file; measured one
// target at a time, neither half would claim it.
func TestAUnitCreditsLinksBetweenItsOwnTargets(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	os.MkdirAll(a, 0o755)
	os.MkdirAll(b, 0o755)
	if err := os.WriteFile(filepath.Join(a, "f"), make([]byte, 64<<10), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(a, "f"), filepath.Join(b, "f")); err != nil {
		t.Fatal(err)
	}
	r := unit.NewRegistry()
	r.Add(&unit.Unit{ID: "u", Kind: unit.KindPaths, Paths: []string{a, b}})

	All(r, 1)

	u, _ := r.Get("u")
	if got := u.Bytes; got < 64<<10 {
		t.Errorf("Bytes = %d, want the linked file credited once", got)
	}
}

// Space held by a link from outside the unit is not freed, but hiding it would
// leave the user wondering where the store's size went.
func TestProbeRecordsSharedAndApparentSizes(t *testing.T) {
	root := t.TempDir()
	store, project := filepath.Join(root, "store"), filepath.Join(root, "project")
	os.MkdirAll(store, 0o755)
	os.MkdirAll(project, 0o755)
	if err := os.WriteFile(filepath.Join(store, "f"), make([]byte, 64<<10), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(store, "f"), filepath.Join(project, "f")); err != nil {
		t.Fatal(err)
	}
	r := unit.NewRegistry()
	r.Add(&unit.Unit{ID: "store", Kind: unit.KindPaths, Paths: []string{store}})

	All(r, 1)

	u, _ := r.Get("store")
	if u.Shared < 64<<10 || u.Apparent != 0 {
		t.Errorf("Shared = %d Apparent = %d, want the linked file shared and not in Apparent", u.Shared, u.Apparent)
	}
}
