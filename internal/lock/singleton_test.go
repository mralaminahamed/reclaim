package lock

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/mralaminahamed/reclaim/internal/unit"
)

func chromiumApp(t *testing.T, target string) string {
	t.Helper()
	app := filepath.Join(t.TempDir(), "goose")
	if err := os.MkdirAll(filepath.Join(app, "Cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(app, "SingletonLock")); err != nil {
		t.Fatal(err)
	}
	return app
}

// Chromium and every Electron app keep a SingletonLock symlink naming
// "host-pid" while running. That identifies a live app the name table has
// never heard of.
func TestSingletonLockOfALiveProcessLocksItsCaches(t *testing.T) {
	app := chromiumApp(t, "box-4242")
	r := unit.NewRegistry()
	r.Add(&unit.Unit{ID: "c", Kind: unit.KindPaths, Paths: []string{filepath.Join(app, "Cache")}})

	Singletons(r, "box", func(pid int) bool { return pid == 4242 })

	u, _ := r.Get("c")
	if u.LockedBy != "goose" || u.PID != 4242 {
		t.Errorf("LockedBy = %q PID = %d, want goose/4242", u.LockedBy, u.PID)
	}
}

// A lock left behind by a crash names a pid that is gone.
func TestStaleSingletonLockLocksNothing(t *testing.T) {
	app := chromiumApp(t, "box-4242")
	r := unit.NewRegistry()
	r.Add(&unit.Unit{ID: "c", Kind: unit.KindPaths, Paths: []string{filepath.Join(app, "Cache")}})

	Singletons(r, "box", func(int) bool { return false })

	if u, _ := r.Get("c"); u.LockedBy != "" {
		t.Errorf("locked by a dead process: %q", u.LockedBy)
	}
}

// On a shared home the lock may belong to another machine, where the pid
// means nothing here -- but the app is running there, so it still locks.
func TestSingletonLockFromAnotherHostStillLocks(t *testing.T) {
	app := chromiumApp(t, "otherbox-"+strconv.Itoa(os.Getpid()))
	r := unit.NewRegistry()
	r.Add(&unit.Unit{ID: "c", Kind: unit.KindPaths, Paths: []string{filepath.Join(app, "Cache")}})

	Singletons(r, "box", func(int) bool { return false })

	if u, _ := r.Get("c"); u.LockedBy == "" {
		t.Error("an app running on another host sharing this home was not treated as running")
	}
}
