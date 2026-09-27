package catalog

import (
	"path/filepath"
	"testing"

	"github.com/mralaminahamed/reclaim/internal/unit"
)

// A trim keeps what is still in use; a wipe costs a re-download of all of it.
// Where a tool offers both, the trim is free and runs first, so a run aimed at
// a target can stop before anything in use is lost.
func TestTrimCommandsRunBeforeTheirWipes(t *testing.T) {
	r := Build(Env{Home: t.TempDir(), Has: func(string) bool { return true }})
	for trim, wipe := range map[string]string{"uv-prune": "uv-native", "npm-verify": "npm-native"} {
		tu, ok := r.Get(trim)
		if !ok {
			t.Errorf("%s not registered though its tool is installed", trim)
			continue
		}
		wu, ok := r.Get(wipe)
		if !ok {
			t.Fatalf("%s not registered", wipe)
		}
		if tu.Tier != unit.TierNative || wu.Tier <= tu.Tier {
			t.Errorf("%s tier %d, %s tier %d: want the trim free and the wipe after it",
				trim, tu.Tier, wipe, wu.Tier)
		}
	}
}

func TestTrimCommandsRequireTheirTool(t *testing.T) {
	r := Build(Env{Home: t.TempDir(), Has: func(string) bool { return false }})
	for _, id := range []string{"uv-prune", "npm-verify"} {
		if _, ok := r.Get(id); ok {
			t.Errorf("%s registered without its tool", id)
		}
	}
}

// Only caches whose files stand alone may be trimmed file by file. Half an
// unpacked package is worse than none, so everything else goes whole.
func TestOnlyStandaloneFileCachesAreLRU(t *testing.T) {
	home := t.TempDir()
	lru := map[string]string{
		"go-build":    ".cache/go-build",
		"pip-cache":   ".cache/pip",
		"npm-cacache": ".npm/_cacache",
		"thumbnails":  ".cache/thumbnails",
	}
	for _, rel := range lru {
		mkdir(t, filepath.Join(home, rel))
	}
	mkdir(t, filepath.Join(home, "go/pkg/mod"))
	mkdir(t, filepath.Join(home, ".cargo/registry/src"))

	r := Build(Env{Home: home, Has: func(string) bool { return false }})
	for id := range lru {
		if u, ok := r.Get(id); !ok || !u.LRU {
			t.Errorf("%s: want an LRU unit", id)
		}
	}
	for _, id := range []string{"go-modcache", "cargo-cache"} {
		if u, ok := r.Get(id); ok && u.LRU {
			t.Errorf("%s is LRU: its files are parts of unpacked packages", id)
		}
	}
}
