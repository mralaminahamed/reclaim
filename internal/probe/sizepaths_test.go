package probe

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mralaminahamed/reclaim/internal/unit"
)

// A command unit's yield is normally unknowable before it runs, so it reports
// nothing. Some commands are an exception: what "apt-get clean" frees is
// exactly what is sitting in the archive directory, and what purging a kernel
// frees is exactly the files that kernel put on disk. Reporting 0B for those is
// not caution, it is a worse answer than the one available.
func TestCommandUnitIsMeasuredThroughSizePaths(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "blob"), make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}

	r := unit.NewRegistry()
	r.Add(&unit.Unit{ID: "cmd", Kind: unit.KindCmd, Command: "true",
		SizePaths: []string{dir}})
	All(r, 1)

	u, _ := r.Get("cmd")
	if u.Bytes != 4096 {
		t.Fatalf("bytes %d, want 4096", u.Bytes)
	}
}

// A size path that is not there contributes nothing rather than failing the
// probe: the kernel unit names every file a kernel could own, and not all of
// them exist for every kernel.
func TestSizePathsToleratesMissingPaths(t *testing.T) {
	r := unit.NewRegistry()
	r.Add(&unit.Unit{ID: "cmd", Kind: unit.KindCmd, Command: "true",
		SizePaths: []string{"/nonexistent/one", "/nonexistent/two"}})
	All(r, 1)

	u, _ := r.Get("cmd")
	if u.Bytes != 0 {
		t.Fatalf("bytes %d, want 0", u.Bytes)
	}
}

func TestCommandUnitWithoutSizePathsStillReportsNothing(t *testing.T) {
	r := unit.NewRegistry()
	r.Add(&unit.Unit{ID: "cmd", Kind: unit.KindCmd, Command: "true"})
	All(r, 1)

	u, _ := r.Get("cmd")
	if u.Bytes != 0 {
		t.Fatalf("bytes %d, want 0", u.Bytes)
	}
}

// A bounded journal vacuum keeps its window, so it frees what is measured less
// that window -- and nothing at all when the journal is already smaller.
func TestSizeKeepIsSubtractedAndNeverGoesNegative(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "blob"), make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	r := unit.NewRegistry()
	r.Add(&unit.Unit{ID: "part", Kind: unit.KindCmd, Command: "true",
		SizePaths: []string{dir}, SizeKeep: 1024})
	r.Add(&unit.Unit{ID: "under", Kind: unit.KindCmd, Command: "true",
		SizePaths: []string{dir}, SizeKeep: 1 << 20})
	All(r, 1)

	if u, _ := r.Get("part"); u.Bytes != 3072 {
		t.Errorf("part: bytes %d, want 3072", u.Bytes)
	}
	if u, _ := r.Get("under"); u.Bytes != 0 {
		t.Errorf("under: bytes %d, want 0", u.Bytes)
	}
}
