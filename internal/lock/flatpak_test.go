package lock

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mralaminahamed/reclaim/internal/unit"
)

func instance(t *testing.T, runtime, dir, info, bwrap string) {
	t.Helper()
	d := filepath.Join(runtime, ".flatpak", dir)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if info != "" {
		if err := os.WriteFile(filepath.Join(d, "info"), []byte(info), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if bwrap != "" {
		if err := os.WriteFile(filepath.Join(d, "bwrapinfo.json"), []byte(bwrap), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func aliveOnly(pids ...int) func(int) bool {
	return func(p int) bool {
		for _, q := range pids {
			if p == q {
				return true
			}
		}
		return false
	}
}

// Flatpak writes one directory per running instance, and its info file names
// the application. That is the only thing tying a sandboxed process back to
// ~/.var/app/<id>: its command line names a path inside the sandbox.
func TestFlatpakRunningReadsInstanceInfo(t *testing.T) {
	rt := t.TempDir()
	instance(t, rt, "1234567", "[Application]\nname=org.mozilla.firefox\nruntime=runtime/org.freedesktop.Platform\n\n[Instance]\ninstance-id=1234567\n",
		`{"child-pid": 4242, "net-namespace": 1}`)
	instance(t, rt, "7654321", "[Application]\nname=com.slack.Slack\n", `{"child-pid": 5151}`)

	got := FlatpakRunning(rt, aliveOnly(4242, 5151))
	if got["org.mozilla.firefox"] != 4242 || got["com.slack.Slack"] != 5151 || len(got) != 2 {
		t.Fatalf("got %v", got)
	}
}

// An instance whose process is gone is a leftover, not a running app. WebKit
// sandboxes share the directory but write no info file.
func TestFlatpakRunningSkipsDeadAndForeignInstances(t *testing.T) {
	rt := t.TempDir()
	instance(t, rt, "111", "[Application]\nname=org.gimp.GIMP\n", `{"child-pid": 999}`)
	instance(t, rt, "webkit-2886-10", "", `{"child-pid": 2886}`)
	instance(t, rt, "222", "[Runtime]\nname=org.gnome.Platform\nruntime=runtime/org.gnome.Platform\n", `{"child-pid": 777}`)

	if got := FlatpakRunning(rt, aliveOnly(2886, 777)); len(got) != 0 {
		t.Fatalf("got %v, want nothing running", got)
	}
}

// Without a readable pid there is no telling it has exited, so the app counts
// as running: parking a cache for nothing costs a re-run, the other mistake
// costs a live app its cache.
func TestFlatpakRunningWithoutPidCountsAsRunning(t *testing.T) {
	rt := t.TempDir()
	instance(t, rt, "333", "[Application]\nname=org.x.App\n", "")
	if got := FlatpakRunning(rt, aliveOnly()); len(got) != 1 {
		t.Fatalf("got %v, want the app counted as running", got)
	}
}

func TestFlatpakRunningOfMissingDirIsEmpty(t *testing.T) {
	if got := FlatpakRunning(filepath.Join(t.TempDir(), "nope"), aliveOnly()); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func appCache(t *testing.T, root, id string, n int) string {
	t.Helper()
	p := filepath.Join(root, id, "cache")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "blob"), make([]byte, n), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// A running app's cache leaves the shared unit for one of its own, locked and
// measured, and the rest stay cleanable: one open browser must not park every
// flatpak's cache.
func TestFlatpakParksOnlyTheRunningAppsCache(t *testing.T) {
	root := t.TempDir()
	ff := appCache(t, root, "org.mozilla.firefox", 8192)
	gimp := appCache(t, root, "org.gimp.GIMP", 4096)
	r := unit.NewRegistry()
	r.Add(&unit.Unit{ID: "flatpak-app-caches", Tier: unit.TierPkgCache, Reversible: true,
		Kind: unit.KindPaths, Paths: []string{ff, gimp}, Bytes: 12288})

	Flatpak(r, root, map[string]int{"org.mozilla.firefox": 4242})

	all, _ := r.Get("flatpak-app-caches")
	if len(all.Paths) != 1 || all.Paths[0] != gimp || all.LockedBy != "" {
		t.Errorf("shared unit paths %v locked %q, want only GIMP's, unlocked", all.Paths, all.LockedBy)
	}
	if all.Bytes != 4096 {
		t.Errorf("shared unit bytes %d, want re-measured 4096", all.Bytes)
	}
	u, ok := r.Get("flatpak-cache-org.mozilla.firefox")
	if !ok {
		t.Fatal("no unit for the running app")
	}
	if u.LockedBy == "" || u.PID != 4242 || len(u.Paths) != 1 || u.Paths[0] != ff || u.Bytes != 8192 {
		t.Errorf("running app unit %+v", u)
	}
	if u.Tier != all.Tier || !u.Reversible {
		t.Errorf("running app unit tier %v reversible %v, want the shared unit's", u.Tier, u.Reversible)
	}
}

// Every app running: nothing left to clean, and the shared unit says so.
func TestFlatpakAllRunningLocksTheSharedUnit(t *testing.T) {
	root := t.TempDir()
	ff := appCache(t, root, "org.mozilla.firefox", 8192)
	r := unit.NewRegistry()
	r.Add(&unit.Unit{ID: "flatpak-app-caches", Kind: unit.KindPaths, Paths: []string{ff}, Bytes: 8192})

	Flatpak(r, root, map[string]int{"org.mozilla.firefox": 4242})

	all, _ := r.Get("flatpak-app-caches")
	if len(all.Paths) != 0 || all.Bytes != 0 {
		t.Errorf("shared unit %+v, want empty", all)
	}
}
