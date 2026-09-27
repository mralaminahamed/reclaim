package runner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mralaminahamed/reclaim/internal/remove"
	"github.com/mralaminahamed/reclaim/internal/unit"
)

// A failure part way through used to report the full pre-run size as freed.
// Only what actually went may be counted.
func TestPartialFailureReportsOnlyWhatWent(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "locked", "b")
	for _, p := range []string{a, b} {
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, make([]byte, 64<<10), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	locked := filepath.Dir(b)
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	// The locked dir's parent is the unit path's parent, which removal may
	// not change, so its contents cannot be unlinked: target it directly.
	u := &unit.Unit{ID: "u", Kind: unit.KindPaths, Paths: []string{a, b}}

	res := (&Runner{Apply: true}).Run([]*unit.Unit{u})[0]

	if res.Err == nil {
		t.Fatal("want the second target to fail")
	}
	if res.Freed < 64<<10 || res.Freed >= 128<<10 {
		t.Errorf("Freed = %d, want only the first file (%d)", res.Freed, 64<<10)
	}
}

// A re-created directory is a skip: the unit does not fail.
func TestSkippedEntriesDoNotFailTheUnit(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "cache")
	if err := os.MkdirAll(filepath.Join(target, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	u := &unit.Unit{ID: "u", Kind: unit.KindPaths, Paths: []string{target}}
	r := &Runner{Apply: true}
	r.remove = func(p string) remove.Outcome {
		return remove.Outcome{Skipped: []remove.Skip{{Path: p + "/sub", Reason: "changed during removal"}}}
	}

	res := r.Run([]*unit.Unit{u})[0]

	if res.Err != nil {
		t.Fatalf("Err = %v, want none", res.Err)
	}
	if len(res.Skipped) != 1 {
		t.Errorf("Skipped = %v, want the one skip carried through", res.Skipped)
	}
}

// A path whose lexical form is harmless but whose parent resolves into a
// protected directory must be refused at delete time.
func TestResolvedPathIsCheckedBeforeDeleting(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "sneaky")
	// dir/sneaky/etc passes the lexical check; its parent resolves to "/",
	// so the resolved form is /etc, which is protected.
	if err := os.Symlink("/", link); err != nil {
		t.Fatal(err)
	}
	u := &unit.Unit{ID: "u", Kind: unit.KindPaths, Paths: []string{filepath.Join(link, "etc")}}
	called := false
	r := &Runner{Apply: true, remove: func(string) remove.Outcome { called = true; return remove.Outcome{Gone: true} }}

	res := r.Run([]*unit.Unit{u})[0]

	if res.Err == nil || called {
		t.Fatalf("Err = %v, removal called = %v; want refusal before removal", res.Err, called)
	}
}

// Command units report what free space actually did, not the probe estimate.
func TestCommandUnitsReportMeasuredFreed(t *testing.T) {
	avail := []int64{1000, 5000}
	u := &unit.Unit{ID: "c", Kind: unit.KindCmd, Command: "true", Bytes: 999999}
	r := &Runner{Apply: true,
		Exec:  func(string) error { return nil },
		Avail: func(string) (int64, error) { v := avail[0]; avail = avail[1:]; return v, nil },
	}

	res := r.Run([]*unit.Unit{u})[0]

	if res.Freed != 4000 {
		t.Errorf("Freed = %d, want the measured 4000, not the estimate", res.Freed)
	}
}

// With no MountHint the runner measured "/", while probe had resolved the unit
// to the home mount. On a machine with a separate /home the delta was ~0.
func TestMeasuredCommandUsesTheProbedMount(t *testing.T) {
	var asked []string
	u := &unit.Unit{ID: "c", Kind: unit.KindCmd, Command: "true", Mount: "/home"}
	r := &Runner{Apply: true, Exec: func(string) error { return nil },
		Avail: func(p string) (int64, error) { asked = append(asked, p); return 0, nil }}

	r.Run([]*unit.Unit{u})

	if len(asked) == 0 || asked[0] != "/home" {
		t.Errorf("avail asked about %q, want the probed mount /home", asked)
	}
}

// If free space could not be read before the command, "after minus zero" is
// the whole disk. Fall back to the estimate, and do not call it measured.
func TestUnreadableAvailBeforeFallsBackToTheEstimate(t *testing.T) {
	calls := 0
	u := &unit.Unit{ID: "c", Kind: unit.KindCmd, Command: "true", Bytes: 700}
	r := &Runner{Apply: true, Exec: func(string) error { return nil },
		Avail: func(string) (int64, error) {
			calls++
			if calls == 1 {
				return 0, os.ErrPermission
			}
			return 1 << 40, nil
		}}

	res := r.Run([]*unit.Unit{u})[0]

	if res.Freed != 700 {
		t.Errorf("Freed = %d, want the 700 estimate", res.Freed)
	}
	if u.Measured {
		t.Error("an estimate was labelled measured")
	}
}

func TestAMeasuredDeltaIsLabelled(t *testing.T) {
	avail := []int64{1000, 5000}
	u := &unit.Unit{ID: "c", Kind: unit.KindCmd, Command: "true"}
	r := &Runner{Apply: true, Exec: func(string) error { return nil },
		Avail: func(string) (int64, error) { v := avail[0]; avail = avail[1:]; return v, nil }}

	r.Run([]*unit.Unit{u})

	if !u.Measured {
		t.Error("a measured delta was not labelled measured")
	}
}
