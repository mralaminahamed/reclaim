package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mralaminahamed/reclaim/internal/unit"
)

func tmpEnv(roots []string, idle time.Duration) Env {
	return Env{Home: "/nonexistent-home", Has: func(string) bool { return false },
		TmpRoots: roots, TmpIdle: idle}
}

func writeAt(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Compile caches that tools keep in /tmp regenerate on the next run, so they
// go whatever their age, like any other cache.
func TestTmpCachesAreClaimedByName(t *testing.T) {
	root := t.TempDir()
	writeAt(t, filepath.Join(root, "node-compile-cache/x"))
	writeAt(t, filepath.Join(root, "jest_rs/x"))
	writeAt(t, filepath.Join(root, "phpstan/x"))
	writeAt(t, filepath.Join(root, "report.pdf"))

	u, ok := Build(tmpEnv([]string{root}, 7*24*time.Hour)).Get("tmp-caches")
	if !ok {
		t.Fatal("tmp-caches not registered")
	}
	if len(u.Paths) != 3 || !u.Reversible || u.Flag != "" || u.Tier != unit.TierArtifact {
		t.Errorf("unit %+v", u)
	}
	for _, p := range u.Paths {
		if strings.HasSuffix(p, "report.pdf") {
			t.Error("claimed a file that is not a known cache")
		}
	}
}

// The user's own scratch entries go once nothing has used them for the idle
// period. Created just now, nothing is idle for a week.
func TestTmpIdleTakesOnlyIdleEntries(t *testing.T) {
	root := t.TempDir()
	writeAt(t, filepath.Join(root, "fresh/x"))
	if _, ok := Build(tmpEnv([]string{root}, 7*24*time.Hour)).Get("tmp-idle"); ok {
		t.Fatal("registered with nothing idle")
	}

	time.Sleep(30 * time.Millisecond)
	u, ok := Build(tmpEnv([]string{root}, 10*time.Millisecond)).Get("tmp-idle")
	if !ok {
		t.Fatal("tmp-idle not registered")
	}
	if len(u.Paths) != 1 || u.Paths[0] != filepath.Join(root, "fresh") || u.IdleFor != 10*time.Millisecond {
		t.Errorf("paths %v idleFor %v", u.Paths, u.IdleFor)
	}
	if !u.Reversible || u.Flag != "" {
		t.Errorf("reversible %v flag %q: want default-on", u.Reversible, u.Flag)
	}
	if len(u.Detail) == 0 {
		t.Error("no detail naming the idle rule")
	}
}

// A named cache belongs to tmp-caches; the idle unit does not list it again.
func TestTmpIdleLeavesNamedCachesToTheirUnit(t *testing.T) {
	root := t.TempDir()
	writeAt(t, filepath.Join(root, "node-compile-cache/x"))
	time.Sleep(30 * time.Millisecond)
	if u, ok := Build(tmpEnv([]string{root}, 10*time.Millisecond)).Get("tmp-idle"); ok {
		t.Errorf("tmp-idle also listed %v", u.Paths)
	}
}

func TestTmpRootsFromEnvironment(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	got := tmpRoots(envOf(map[string]string{"RECLAIM_TMP_ROOTS": a + ":rel:" + b}))
	if len(got) != 2 || got[0] != a || got[1] != b {
		t.Errorf("got %v, want the two absolute roots", got)
	}
	got = tmpRoots(envOf(map[string]string{"TMPDIR": a}))
	if !has(got, a) {
		t.Errorf("got %v, want TMPDIR among the defaults", got)
	}
}

// Lower-only, like RECLAIM_HEAVY_THRESHOLD: it exists so tests can make a
// fresh file idle, never so a typo can widen what counts as idle.
func TestTmpIdleCanOnlyBeLowered(t *testing.T) {
	week := 7 * 24 * time.Hour
	for v, want := range map[string]time.Duration{"": week, "1s": time.Second, "720h": week, "junk": week} {
		if got := tmpIdle(envOf(map[string]string{"RECLAIM_TMP_IDLE": v})); got != want {
			t.Errorf("RECLAIM_TMP_IDLE=%q: %v, want %v", v, got, want)
		}
	}
}
