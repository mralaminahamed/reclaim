package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mralaminahamed/reclaim/internal/unit"
)

func TestBuildRegistersOnlyExistingPaths(t *testing.T) {
	home := t.TempDir()
	mkdir(t, filepath.Join(home, ".npm/_cacache"))
	mkdir(t, filepath.Join(home, ".gradle/caches"))

	r := Build(Env{Home: home, Has: func(string) bool { return false }})

	if _, ok := r.Get("npm-cacache"); !ok {
		t.Error("npm-cacache not registered though the path exists")
	}
	if _, ok := r.Get("gradle-caches"); !ok {
		t.Error("gradle-caches not registered though the path exists")
	}
	if _, ok := r.Get("pip-cache"); ok {
		t.Error("pip-cache registered though its path does not exist")
	}
}

func TestNativeUnitsRequireTheToolOnPath(t *testing.T) {
	home := t.TempDir()
	r := Build(Env{Home: home, Has: func(bin string) bool { return bin == "npm" }})

	u, ok := r.Get("npm-native")
	if !ok {
		t.Fatal("npm-native missing though npm is installed")
	}
	if u.Kind != unit.KindCmd {
		t.Error("npm-native should be a command unit")
	}
	if _, ok := r.Get("pnpm-native"); ok {
		t.Error("pnpm-native registered though pnpm is not installed")
	}
}

func TestGradleAndMavenAreColdReloadAndOptIn(t *testing.T) {
	home := t.TempDir()
	mkdir(t, filepath.Join(home, ".gradle/caches"))
	mkdir(t, filepath.Join(home, ".m2/repository"))
	r := Build(Env{Home: home, Has: func(string) bool { return false }})

	g, _ := r.Get("gradle-caches")
	if g.Tier != unit.TierColdReload {
		t.Errorf("gradle tier = %v, want TierColdReload", g.Tier)
	}
	if g.Flag != "--gradle" {
		t.Errorf("gradle flag = %q, want --gradle", g.Flag)
	}
	if !g.Reversible {
		t.Error("gradle caches should be reversible: the next build re-downloads")
	}
	m, _ := r.Get("maven-repo")
	if m.Flag != "--maven" {
		t.Errorf("maven flag = %q, want --maven", m.Flag)
	}
}

func TestSteamShaderCacheIsFoundUnderEveryInstallMethod(t *testing.T) {
	home := t.TempDir()
	mkdir(t, filepath.Join(home, ".local/share/Steam/steamapps/shadercache"))
	mkdir(t, filepath.Join(home, ".var/app/com.valvesoftware.Steam/.local/share/Steam/steamapps/shadercache"))
	r := Build(Env{Home: home, Has: func(string) bool { return false }})

	u, ok := r.Get("steam-shadercache")
	if !ok {
		t.Fatal("steam-shadercache not registered though its paths exist")
	}
	if u.Tier != unit.TierArtifact {
		t.Errorf("tier = %v, want TierArtifact", u.Tier)
	}
	if !u.Reversible {
		t.Error("shader cache should be reversible: the game recompiles it")
	}
	if u.Flag != "" {
		t.Errorf("flag = %q, want unflagged", u.Flag)
	}
	if len(u.Paths) != 2 {
		t.Errorf("paths = %v, want exactly the two that exist", u.Paths)
	}
}

func TestLossyUnitsAreMarkedIrreversible(t *testing.T) {
	home := t.TempDir()
	mkdir(t, filepath.Join(home, ".config/Claude/vm_bundles"))
	r := Build(Env{Home: home, Has: func(string) bool { return false }})

	u, ok := r.Get("claude-vm")
	if !ok {
		t.Fatal("claude-vm missing")
	}
	if u.Reversible {
		t.Error("claude-vm must be marked irreversible")
	}
}

// The Claude desktop app keeps its VM bundles under Application Support on
// macOS, not under .config -- easily the largest thing this unit ever finds,
// and silently missed entirely without this path too.
func TestClaudeVMFindsTheMacOSPath(t *testing.T) {
	home := t.TempDir()
	mkdir(t, filepath.Join(home, "Library/Application Support/Claude/vm_bundles"))
	r := Build(Env{Home: home, Has: func(string) bool { return false }})

	u, ok := r.Get("claude-vm")
	if !ok {
		t.Fatal("claude-vm missing though the macOS path exists")
	}
	if len(u.Paths) != 1 || !strings.Contains(u.Paths[0], "Application Support") {
		t.Errorf("paths = %v, want the Application Support path", u.Paths)
	}
}

func TestNoUnitTargetsAProtectedDirectory(t *testing.T) {
	// A catalog entry that names $HOME or a system directory would be caught by
	// the runner's backstop, but it should never get that far.
	home := t.TempDir()
	for _, d := range []string{".npm/_cacache", ".cache/pip", ".gradle/caches", ".m2/repository"} {
		mkdir(t, filepath.Join(home, d))
	}
	r := Build(Env{Home: home, Has: func(string) bool { return true }})

	bad := map[string]bool{"/": true, "/home": true, "/usr": true, "/etc": true, home: true}
	for _, u := range r.All() {
		for _, p := range u.Paths {
			if bad[filepath.Clean(p)] {
				t.Errorf("unit %q targets protected path %q", u.ID, p)
			}
			if !strings.HasPrefix(p, "/") {
				t.Errorf("unit %q has a relative path %q", u.ID, p)
			}
		}
	}
}

func TestEveryUnitHasIDLabelAndTier(t *testing.T) {
	home := t.TempDir()
	for _, d := range []string{".npm/_cacache", ".cache/pip", ".gradle/caches"} {
		mkdir(t, filepath.Join(home, d))
	}
	r := Build(Env{Home: home, Has: func(string) bool { return true }})
	if len(r.All()) == 0 {
		t.Fatal("catalog built nothing")
	}
	for _, u := range r.All() {
		if u.ID == "" || u.Label == "" {
			t.Errorf("unit %+v missing id or label", u)
		}
		if u.Kind == unit.KindPaths && len(u.Paths) == 0 {
			t.Errorf("path unit %q has no paths", u.ID)
		}
		if u.Kind == unit.KindCmd && u.Command == "" {
			t.Errorf("cmd unit %q has no command", u.ID)
		}
	}
}

// Cross-language version manager download caches are safe to clear any time,
// so they need no flag; what they install is costly to redo, so that half is
// opt-in and classified TierColdReload, the same split hf-prune/hf-cache
// draws for model stores.
func TestVersionManagerCachesVsInstallsSplit(t *testing.T) {
	home := t.TempDir()
	mkdir(t, filepath.Join(home, ".cache/mise"))
	mkdir(t, filepath.Join(home, ".local/share/mise/installs"))
	mkdir(t, filepath.Join(home, ".asdf/downloads"))
	mkdir(t, filepath.Join(home, ".asdf/installs"))
	mkdir(t, filepath.Join(home, ".rustup/downloads"))
	mkdir(t, filepath.Join(home, ".volta/cache"))
	r := Build(Env{Home: home, Has: func(string) bool { return false }})

	for _, tc := range []struct {
		id         string
		flag       string
		tier       unit.Tier
		reversible bool
	}{
		{"mise-cache", "", unit.TierPkgCache, true},
		{"mise-installs", "--mise", unit.TierColdReload, true},
		{"asdf-downloads", "", unit.TierPkgCache, true},
		{"asdf-installs", "--asdf", unit.TierColdReload, true},
		{"rustup-downloads", "", unit.TierPkgCache, true},
		{"volta-cache", "", unit.TierPkgCache, true},
	} {
		u, ok := r.Get(tc.id)
		if !ok {
			t.Errorf("%s not registered though its path exists", tc.id)
			continue
		}
		if u.Flag != tc.flag {
			t.Errorf("%s flag = %q, want %q", tc.id, u.Flag, tc.flag)
		}
		if u.Tier != tc.tier {
			t.Errorf("%s tier = %v, want %v", tc.id, u.Tier, tc.tier)
		}
		if u.Reversible != tc.reversible {
			t.Errorf("%s reversible = %v, want %v", tc.id, u.Reversible, tc.reversible)
		}
	}
}

// rustup renames ~/.rustup/tmp into toolchains/ mid-install, so unlike every
// other download cache this tool clears, it must never be swept -- there is
// no lock rule that could tell an in-flight install apart from an idle one.
func TestRustupTmpIsNeverClaimed(t *testing.T) {
	home := t.TempDir()
	mkdir(t, filepath.Join(home, ".rustup/tmp"))
	r := Build(Env{Home: home, Has: func(string) bool { return false }})
	for _, u := range r.All() {
		for _, p := range u.Paths {
			if strings.Contains(p, filepath.Join(".rustup", "tmp")) {
				t.Errorf("unit %q claims .rustup/tmp: %q", u.ID, p)
			}
		}
	}
}

func TestAndroidCacheIsUnflagged(t *testing.T) {
	home := t.TempDir()
	mkdir(t, filepath.Join(home, ".android/cache"))
	r := Build(Env{Home: home, Has: func(string) bool { return false }})
	u, ok := r.Get("android-cache")
	if !ok {
		t.Fatal("android-cache not registered")
	}
	if u.Flag != "" || !u.Reversible {
		t.Errorf("flag %q reversible %v", u.Flag, u.Reversible)
	}
}

// A VM disk kept by a Docker Desktop alternative can hold a database nowhere
// else, same claim docker-volumes makes, so each gets its own flag and stays
// irreversible.
func TestMacVMDisksAreIrreplaceableAndOptIn(t *testing.T) {
	home := t.TempDir()
	mkdir(t, filepath.Join(home, ".orbstack/data"))
	mkdir(t, filepath.Join(home, ".colima"))
	mkdir(t, filepath.Join(home, ".lima"))
	r := Build(Env{Home: home, Has: func(string) bool { return false }})

	for _, tc := range []struct{ id, flag string }{
		{"orbstack-vm", "--orbstack"},
		{"colima-vm", "--colima"},
		{"lima-vm", "--lima"},
	} {
		u, ok := r.Get(tc.id)
		if !ok {
			t.Errorf("%s not registered though its path exists", tc.id)
			continue
		}
		if u.Flag != tc.flag {
			t.Errorf("%s flag = %q, want %q", tc.id, u.Flag, tc.flag)
		}
		if u.Reversible {
			t.Errorf("%s marked reversible; a VM disk may hold a database nowhere else", tc.id)
		}
		if u.Tier != unit.TierIrreplaceable {
			t.Errorf("%s tier = %v, want TierIrreplaceable", tc.id, u.Tier)
		}
	}
}

func mkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}
